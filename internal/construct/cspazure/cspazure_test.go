// SPDX-License-Identifier: AGPL-3.0-only

// Package cspazure_test — contract tests for the csp_azure construct.
//
// Tests cover:
//
//	(a) Interface conformance: Kind/Scope/Signals/Interval.
//	(b) Metric inventory per sub-signal (key series present).
//	(c) Two name traps byte-exact:
//	    - azure_microsoft_sql_servers_databases_storage_maximum_bytes (no _maximum before _bytes)
//	    - azure_microsoft_dbforpostgresql_flexibleservers_connections_connections_failed_total_count
//	      (literal double "connections_")
//	(d) resourceID fully lowercased; last segment == resourceName.
//	(e) All-Set behaviour: even _total_count names are NOT monotonically accumulated
//	    across multiple ticks with a fixed RNG seed (assert a known series can produce
//	    non-monotonic values, proving Set not Add is used).
//	(f) Sub-signal subset config respected.
//	(g) Deterministic identity across independent Builds.
//	(h) PG fixture cohesion: when fx.DBs contains a postgres fixture the PG resource
//	    name matches that fixture.
//	(i) Logs sub-signal: streams with correct label keys.
package cspazure_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/cspazure"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// tick runs one Tick on a fresh construct built with cfg and fx, at a fixed midday time.
func tick(t *testing.T, cfg *cspazure.Config, fx *fixture.Set) (*coretest.MetricCapture, *coretest.LogCapture) {
	t.Helper()
	reg := cspazure.Registration()
	c, err := reg.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	w := coretest.World(mc, lc, nil)
	now := time.Date(2026, 6, 12, 14, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return mc, lc
}

// defaultFx returns a fixture.Set suitable for csp_azure (Seed only; no DBs).
func defaultFx() *fixture.Set {
	return &fixture.Set{Seed: "test"}
}

// defaultCfg returns the default Config (zero value → applyDefaults fills in:
// the serverless ingestion path with credential defaulted to "azure").
func defaultCfg() *cspazure.Config {
	return &cspazure.Config{}
}

// exporterCfg returns a Config pinned to the azure_exporter ingestion path.
func exporterCfg() *cspazure.Config {
	return &cspazure.Config{IngestionPath: "azure_exporter"}
}

// serverlessCfg returns a Config pinned to the serverless path with an explicit credential.
func serverlessCfg() *cspazure.Config {
	return &cspazure.Config{IngestionPath: "serverless", Credential: "ps_azure"}
}

// ── (a) Interface conformance ─────────────────────────────────────────────────

func TestRegistrationConformance(t *testing.T) {
	reg := cspazure.Registration()
	if reg.Kind != "csp_azure" {
		t.Errorf("Kind=%q want %q", reg.Kind, "csp_azure")
	}
	if reg.Scope != core.ScopeSubstrate {
		t.Errorf("Scope=%v want ScopeSubstrate", reg.Scope)
	}
	if reg.NewConfig == nil {
		t.Error("NewConfig is nil")
	}
	if reg.Build == nil {
		t.Error("Build is nil")
	}
}

func TestInterfaceConformance(t *testing.T) {
	reg := cspazure.Registration()
	c, err := reg.Build(reg.NewConfig(), defaultFx())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.Kind() != "csp_azure" {
		t.Errorf("Kind()=%q want %q", c.Kind(), "csp_azure")
	}
	sigs := c.Signals()
	if len(sigs) != 2 {
		t.Fatalf("Signals() len=%d want 2", len(sigs))
	}
	hasMetrics, hasLogs := false, false
	for _, s := range sigs {
		if s == core.Metrics {
			hasMetrics = true
		}
		if s == core.Logs {
			hasLogs = true
		}
	}
	if !hasMetrics {
		t.Error("Signals() missing Metrics")
	}
	if !hasLogs {
		t.Error("Signals() missing Logs")
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval()=%v want 60s", c.Interval())
	}
}

// ── (b) Metric inventory per sub-signal ───────────────────────────────────────

// computeInventory is the expected set of compute metrics.
var computeInventory = []string{
	"azure_microsoft_compute_virtualmachines_vmavailabilitymetric_average_count",
	"azure_microsoft_compute_virtualmachines_percentage_cpu_average_percent",
	"azure_microsoft_compute_virtualmachines_available_memory_bytes_average_bytes",
	"azure_microsoft_compute_virtualmachines_cpu_credits_consumed_average_count",
	"azure_microsoft_compute_virtualmachines_cpu_credits_remaining_average_count",
	"azure_microsoft_compute_virtualmachines_disk_read_bytes_total_bytes",
	"azure_microsoft_compute_virtualmachines_disk_write_bytes_total_bytes",
	"azure_microsoft_compute_virtualmachines_disk_read_operations_sec_average_countpersecond",
	"azure_microsoft_compute_virtualmachines_disk_write_operations_sec_average_countpersecond",
	"azure_microsoft_compute_virtualmachines_inbound_flows_average_count",
	"azure_microsoft_compute_virtualmachines_outbound_flows_average_count",
	"azure_microsoft_compute_virtualmachines_network_in_total_total_bytes",
	"azure_microsoft_compute_virtualmachines_network_out_total_total_bytes",
}

var databasesInventory = []string{
	"azure_microsoft_sql_servers_databases_connection_successful_total_count",
	"azure_microsoft_sql_servers_databases_deadlock_total_count",
	"azure_microsoft_sql_servers_databases_sessions_count_average_count",
	"azure_microsoft_sql_servers_databases_cpu_percent_average_percent",
	"azure_microsoft_sql_servers_databases_cpu_limit_average_count",
	"azure_microsoft_sql_servers_databases_cpu_used_average_count",
	// ⚠ TRAP: no _maximum before _bytes
	"azure_microsoft_sql_servers_databases_storage_maximum_bytes",
	"azure_microsoft_sql_servers_databases_storage_percent_maximum_percent",
	"azure_microsoft_sql_servers_databases_dtu_used_average_count",
	"azure_microsoft_sql_servers_databases_dtu_consumption_percent_average_percent",
	"azure_microsoft_sql_servers_databases_dtu_limit_average_count",
	"azure_microsoft_sql_servers_elasticpools_allocated_data_storage_average_bytes",
	"azure_microsoft_sql_servers_elasticpools_storage_used_average_bytes",
	"azure_microsoft_sql_servers_elasticpools_storage_limit_average_bytes",
	"azure_microsoft_sql_servers_elasticpools_cpu_percent_average_percent",
	"azure_microsoft_sql_servers_elasticpools_sql_instance_memory_percent_maximum_percent",
	"azure_microsoft_sql_servers_elasticpools_edtu_used_average_count",
	"azure_microsoft_sql_servers_elasticpools_sessions_count_average_count",
	"azure_microsoft_sql_servers_elasticpools_allocated_data_storage_percent_average_percent",
	"azure_microsoft_sql_servers_elasticpools_storage_percent_average_percent",
	"azure_microsoft_dbforpostgresql_flexibleservers_active_connections_average_count",
	"azure_microsoft_dbforpostgresql_flexibleservers_connections_succeeded_total_count",
	// ⚠ TRAP: literal double "connections_"
	"azure_microsoft_dbforpostgresql_flexibleservers_connections_connections_failed_total_count",
	"azure_microsoft_dbforpostgresql_flexibleservers_cpu_percent_average_percent",
	"azure_microsoft_dbforpostgresql_flexibleservers_storage_used_maximum_bytes",
	"azure_microsoft_dbforpostgresql_flexibleservers_storage_percent_maximum_percent",
	"azure_microsoft_dbforpostgresql_flexibleservers_read_iops_maximum_count",
	"azure_microsoft_dbforpostgresql_flexibleservers_write_iops_maximum_count",
	"azure_microsoft_dbforpostgresql_flexibleservers_database_size_bytes_average_bytes",
	"azure_microsoft_dbforpostgresql_flexibleservers_storage_percent_average_percent",
	"azure_microsoft_dbforpostgresql_flexibleservers_memory_percent_average_percent",
	"azure_microsoft_dbforpostgresql_flexibleservers_read_iops_average_count",
	"azure_microsoft_dbforpostgresql_flexibleservers_write_iops_average_count",
	"azure_microsoft_dbforpostgresql_flexibleservers_network_bytes_ingress_total_bytes",
	"azure_microsoft_dbforpostgresql_flexibleservers_network_bytes_egress_total_bytes",
	"azure_microsoft_dbforpostgresql_flexibleservers_read_throughput_average_count",
	"azure_microsoft_dbforpostgresql_flexibleservers_write_throughput_average_count",
}

var storageInventory = []string{
	"azure_microsoft_storage_storageaccounts_blobservices_containercount_average_count",
	"azure_microsoft_storage_storageaccounts_blobservices_blobcount_average_count",
	"azure_microsoft_storage_storageaccounts_blobservices_blobcapacity_average_bytes",
	"azure_microsoft_storage_storageaccounts_blobservices_indexcapacity_average_bytes",
	"azure_microsoft_storage_storageaccounts_blobservices_ingress_total_bytes",
	"azure_microsoft_storage_storageaccounts_blobservices_egress_total_bytes",
	"azure_microsoft_storage_storageaccounts_blobservices_availability_average_percent",
	"azure_microsoft_storage_storageaccounts_blobservices_transactions_total_count",
	"azure_microsoft_storage_storageaccounts_queueservices_queuecount_average_count",
	"azure_microsoft_storage_storageaccounts_queueservices_queuemessagecount_average_count",
	"azure_microsoft_storage_storageaccounts_queueservices_queuecapacity_average_bytes",
	"azure_microsoft_storage_storageaccounts_queueservices_ingress_total_bytes",
	"azure_microsoft_storage_storageaccounts_queueservices_egress_total_bytes",
	"azure_microsoft_storage_storageaccounts_queueservices_availability_average_percent",
	"azure_microsoft_storage_storageaccounts_queueservices_transactions_total_count",
}

var networkingInventory = []string{
	"azure_microsoft_network_loadbalancers_syncount_total_count",
	"azure_microsoft_network_loadbalancers_packetcount_total_count",
	"azure_microsoft_network_loadbalancers_bytecount_total_bytes",
	"azure_microsoft_network_loadbalancers_snatconnectioncount_total_count",
	"azure_microsoft_network_loadbalancers_usedsnatports_average_count",
	"azure_microsoft_network_loadbalancers_allocatedsnatports_average_count",
	"azure_microsoft_network_applicationgateways_totalrequests_total_count",
	"azure_microsoft_network_applicationgateways_failedrequests_total_count",
	"azure_microsoft_network_applicationgateways_responsestatus_total_count",
	"azure_microsoft_network_applicationgateways_throughput_average_bytespersecond",
	"azure_microsoft_network_applicationgateways_applicationgatewaytotaltime_average_milliseconds",
	"azure_microsoft_network_applicationgateways_currentconnections_total_count",
	"azure_microsoft_cdn_profiles_percentage4xx_average_percent",
	"azure_microsoft_cdn_profiles_percentage5xx_average_percent",
	"azure_microsoft_cdn_profiles_requestsize_total_bytes",
	"azure_microsoft_cdn_profiles_responsesize_total_bytes",
	"azure_microsoft_cdn_profiles_totallatency_average_milliseconds",
	"azure_microsoft_cdn_profiles_originhealthpercentage_average_percent",
	"azure_microsoft_cdn_profiles_originlatency_average_milliseconds",
	"azure_microsoft_cdn_profiles_originrequestcount_total_count",
	"azure_microsoft_cdn_profiles_requestcount_total_count",
	// ⚠ VNet — no aggregation suffix (ends _count directly)
	"azure_microsoft_network_virtualnetworks_subnets_count",
	"azure_microsoft_network_virtualnetworks_availableaddresses_count",
	"azure_microsoft_network_virtualnetworks_connectedpeerings_count",
	"azure_microsoft_network_virtualnetworks_peerings_count",
	"azure_microsoft_network_virtualnetworks_availablesubnetaddresses_count",
	"azure_microsoft_network_virtualnetworks_assignedsubnetaddresses_count",
}

var messagingInventory = []string{
	"azure_microsoft_eventhub_namespaces_activeconnections_maximum_count",
	"azure_microsoft_eventhub_namespaces_connectionsopened_maximum_count",
	"azure_microsoft_eventhub_namespaces_connectionsclosed_maximum_count",
	"azure_microsoft_eventhub_namespaces_incomingrequests_total_count",
	"azure_microsoft_eventhub_namespaces_successfulrequests_total_count",
	"azure_microsoft_eventhub_namespaces_throttledrequests_total_count",
	"azure_microsoft_eventhub_namespaces_usererrors_total_count",
	"azure_microsoft_eventhub_namespaces_servererrors_total_count",
	"azure_microsoft_eventhub_namespaces_incomingbytes_total_bytes",
	"azure_microsoft_eventhub_namespaces_outgoingbytes_total_bytes",
	"azure_microsoft_eventhub_namespaces_incomingmessages_total_count",
	"azure_microsoft_eventhub_namespaces_outgoingmessages_total_count",
	"azure_microsoft_eventhub_namespaces_capturedmessages_total_count",
	"azure_microsoft_servicebus_namespaces_incomingmessages_total_count",
	"azure_microsoft_servicebus_namespaces_outgoingmessages_total_count",
	"azure_microsoft_servicebus_namespaces_incomingrequests_total_count",
	"azure_microsoft_servicebus_namespaces_successfulrequests_total_count",
	"azure_microsoft_servicebus_namespaces_activeconnections_total_count",
	"azure_microsoft_servicebus_namespaces_usererrors_total_count",
	"azure_microsoft_servicebus_namespaces_servererrors_total_count",
	"azure_microsoft_servicebus_namespaces_messages_average_count",
	"azure_microsoft_servicebus_namespaces_activemessages_average_count",
	"azure_microsoft_servicebus_namespaces_size_average_bytes",
}

func TestComputeInventory(t *testing.T) {
	mc, _ := tick(t, defaultCfg(), defaultFx())
	names := nameSet(mc)
	for _, want := range computeInventory {
		if !names[want] {
			t.Errorf("compute: missing series %q", want)
		}
	}
}

func TestDatabasesInventory(t *testing.T) {
	mc, _ := tick(t, defaultCfg(), defaultFx())
	names := nameSet(mc)
	for _, want := range databasesInventory {
		if !names[want] {
			t.Errorf("databases: missing series %q", want)
		}
	}
}

func TestStorageInventory(t *testing.T) {
	mc, _ := tick(t, defaultCfg(), defaultFx())
	names := nameSet(mc)
	for _, want := range storageInventory {
		if !names[want] {
			t.Errorf("storage: missing series %q", want)
		}
	}
}

func TestNetworkingInventory(t *testing.T) {
	mc, _ := tick(t, defaultCfg(), defaultFx())
	names := nameSet(mc)
	for _, want := range networkingInventory {
		if !names[want] {
			t.Errorf("networking: missing series %q", want)
		}
	}
}

func TestMessagingInventory(t *testing.T) {
	mc, _ := tick(t, defaultCfg(), defaultFx())
	names := nameSet(mc)
	for _, want := range messagingInventory {
		if !names[want] {
			t.Errorf("messaging: missing series %q", want)
		}
	}
}

// ── (c) Name traps byte-exact ─────────────────────────────────────────────────

// TestStorageMaximumBytesTrap asserts the exact SQL metric name (no _maximum before _bytes).
func TestStorageMaximumBytesTrap(t *testing.T) {
	mc, _ := tick(t, defaultCfg(), defaultFx())
	const want = "azure_microsoft_sql_servers_databases_storage_maximum_bytes"
	const bad = "azure_microsoft_sql_servers_databases_storage_maximum_maximum_bytes"
	names := nameSet(mc)
	if !names[want] {
		t.Errorf("TRAP: missing %q (storage_maximum_bytes trap)", want)
	}
	if names[bad] {
		t.Errorf("TRAP: emitted wrong form %q (extra _maximum)", bad)
	}
}

// TestDoubleConnectionsTrap asserts the literal double "connections_" in the PG metric name.
func TestDoubleConnectionsTrap(t *testing.T) {
	mc, _ := tick(t, defaultCfg(), defaultFx())
	const want = "azure_microsoft_dbforpostgresql_flexibleservers_connections_connections_failed_total_count"
	const bad = "azure_microsoft_dbforpostgresql_flexibleservers_connections_failed_total_count"
	names := nameSet(mc)
	if !names[want] {
		t.Errorf("TRAP: missing %q (double connections_ trap)", want)
	}
	if names[bad] {
		t.Errorf("TRAP: emitted wrong (single connections_) form %q", bad)
	}
}

// ── (d) resourceID lowercase + last segment == resourceName ──────────────────

// TestResourceIDLowercasedExporter verifies that on the azure_exporter path every emitted
// series has a fully-lowercased resourceID (signals/cspazure.md [slug: cspazure]).
func TestResourceIDLowercasedExporter(t *testing.T) {
	mc, _ := tick(t, exporterCfg(), defaultFx())
	for _, s := range mc.All() {
		rid, ok := s.Labels["resourceID"]
		if !ok {
			t.Errorf("series %q missing resourceID label", s.Name)
			continue
		}
		if rid != strings.ToLower(rid) {
			t.Errorf("series %q: azure_exporter resourceID not fully lowercased: %q", s.Name, rid)
		}
	}
}

// TestResourceIDPascalCaseServerless verifies that on the serverless path the resourceID
// preserves canonical Azure provider casing (e.g. Microsoft.ServiceBus, Microsoft.Cdn) and
// is mirrored on the `instance` label (signals/cspazure.md [slug: cspazure], SK-16).
func TestResourceIDPascalCaseServerless(t *testing.T) {
	mc, _ := tick(t, serverlessCfg(), defaultFx())
	sawPascal := false
	for _, s := range mc.All() {
		rid := s.Labels["resourceID"]
		if strings.Contains(rid, "/providers/Microsoft.") {
			sawPascal = true
		}
		if strings.Contains(rid, "/providers/microsoft.") {
			t.Errorf("series %q: serverless resourceID has lowercased provider namespace: %q", s.Name, rid)
		}
		if inst := s.Labels["instance"]; inst != rid {
			t.Errorf("series %q: serverless instance=%q must equal resourceID=%q", s.Name, inst, rid)
		}
	}
	if !sawPascal {
		t.Error("serverless: no PascalCase provider namespace observed in any resourceID")
	}
}

// TestResourceIDLastSegmentEqualsResourceName verifies the label_replace join key.
// The app extracts the resource name via label_replace(.+/(.*)); the last segment of
// the resourceID MUST equal the resourceName label on every series that carries both.
//
// Exception: Azure Storage blob/queue service resources have ARM paths that end in
// "/blobservices/default" or "/queueservices/default" — the meaningful resource is the
// storage account, so resourceName is the account name (not "default"). These are
// exempted from this assertion; they are exercised by TestStorageInventory instead.
func TestResourceIDLastSegmentEqualsResourceName(t *testing.T) {
	mc, _ := tick(t, defaultCfg(), defaultFx())
	for _, s := range mc.All() {
		rid, hasRID := s.Labels["resourceID"]
		rn, hasRN := s.Labels["resourceName"]
		if !hasRID || !hasRN {
			continue
		}
		// SQL elastic-pool exception (SK-16): azure_exporter resourceName is the <server>
		// (not the pool), while the resourceID still ends /elasticpools/<pool>.
		if strings.Contains(strings.ToLower(rid), "/elasticpools/") {
			continue
		}
		// Extract last segment.
		idx := strings.LastIndex(rid, "/")
		if idx < 0 {
			t.Errorf("series %q: resourceID has no '/': %q", s.Name, rid)
			continue
		}
		lastSeg := rid[idx+1:]
		// On serverless, resourceName is the joined "<parent>/<child>" form for nested
		// resources, so compare the resourceID last segment to the LAST component of
		// resourceName (live-confirmed managed-scraper convention).
		rnLast := rn
		if k := strings.LastIndex(rn, "/"); k >= 0 {
			rnLast = rn[k+1:]
		}
		if lastSeg != rnLast {
			t.Errorf("series %q: resourceID last segment %q != resourceName tail %q (resourceName=%q)",
				s.Name, lastSeg, rnLast, rn)
		}
	}
}

// ── (e) All-Set behaviour: _total_count series are not monotonically accumulated ─

// TestWindowGaugeAllSet is the window-gauge invariant test (extract §1.3).
//
// Method: run the same construct for many ticks with a deterministic time that does NOT
// advance (meaning the shape factor stays constant — any change in output is a function
// of the state mechanism, not the signal). Because st.Set replaces the gauge value on
// every call, and the shape noise produces values in [1-δ, 1+δ] range rather than
// accumulating, repeated ticks with the same state should NOT produce monotonically
// increasing values for _total_count series.
//
// We verify this by:
//  1. Running 5 ticks at the same timestamp on the same construct (noise will vary
//     because the shape engine uses a fresh noise call each time).
//  2. For the anchor _total_count series, collecting all emitted values.
//  3. Asserting that NOT all consecutive values are non-decreasing (i.e. at least one
//     tick-to-tick pair decreases or stays equal for at least one series).
//
// If Add were used instead of Set, values would be strictly monotonically increasing
// across ticks, never decreasing. This test would fail under Add.
func TestWindowGaugeAllSet(t *testing.T) {
	reg := cspazure.Registration()
	cfg := defaultCfg()
	c, err := reg.Build(cfg, defaultFx())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Use a fixed time so shape factor is constant; noise will produce varying values.
	now := time.Date(2026, 6, 12, 14, 0, 0, 0, time.UTC)
	const seriesName = "azure_microsoft_eventhub_namespaces_incomingrequests_total_count"

	const nTicks = 10
	var values []float64
	for range nTicks {
		mc := &coretest.MetricCapture{}
		lc := &coretest.LogCapture{}
		w := coretest.World(mc, lc, nil)
		if err := c.Tick(context.Background(), now, w); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		// Collect any series matching the target name.
		for _, s := range mc.Find(seriesName) {
			values = append(values, s.Value)
		}
	}

	if len(values) == 0 {
		t.Fatalf("no samples of %q found across %d ticks", seriesName, nTicks)
	}

	// With st.Set, successive values for a gauge can decrease (when noise drives bf lower).
	// With st.Add, values would be strictly monotonically non-decreasing.
	// We check that the max value is NOT nTicks × (min positive value), i.e. there's no
	// linear accumulation.
	//
	// Robust check: compute the ratio of the max observed value to the first observed value.
	// Under Add with bf>0, the ratio would approach nTicks (linear growth).
	// Under Set, values fluctuate around a stable level; ratio stays near 1.
	first := values[0]
	if first <= 0 {
		t.Logf("first value for %q is %.2f — skipping ratio check (factor may be 0)", seriesName, first)
		return
	}
	maxV := first
	for _, v := range values {
		if v > maxV {
			maxV = v
		}
	}
	ratio := maxV / first
	// Under Set: ratio ≤ ~3 (noise ≤ ±15%; shape factor at midday ≈ 1.0).
	// Under Add: ratio ≥ nTicks (every tick accumulates).
	if ratio >= float64(nTicks)/2 {
		t.Errorf("WINDOW-GAUGE INVARIANT VIOLATED: %q max/first ratio=%.1f >= %.1f — "+
			"values appear to accumulate (Add used instead of Set). values=%v",
			seriesName, ratio, float64(nTicks)/2, values)
	}
}

// ── (f) Sub-signal subset config ─────────────────────────────────────────────

// TestSubSignalSubset verifies that only compute metrics are emitted when sub_signals=["compute"].
func TestSubSignalSubset(t *testing.T) {
	cfg := &cspazure.Config{SubSignals: []string{"compute"}}
	mc, _ := tick(t, cfg, defaultFx())
	names := nameSet(mc)

	// Compute series must be present.
	for _, want := range computeInventory {
		if !names[want] {
			t.Errorf("subset=compute: missing compute series %q", want)
		}
	}
	// Databases series must be absent.
	for _, absent := range databasesInventory {
		if names[absent] {
			t.Errorf("subset=compute: databases series %q must be absent", absent)
		}
	}
	// Storage series must be absent.
	for _, absent := range storageInventory {
		if names[absent] {
			t.Errorf("subset=compute: storage series %q must be absent", absent)
		}
	}
}

// TestSubSignalLogsOnly verifies that no metric series are emitted when sub_signals=["logs"].
func TestSubSignalLogsOnly(t *testing.T) {
	cfg := &cspazure.Config{SubSignals: []string{"logs"}}
	mc, lc := tick(t, cfg, defaultFx())
	if len(mc.All()) > 0 {
		t.Errorf("sub_signals=[logs]: expected 0 metric series, got %d", len(mc.All()))
	}
	if len(lc.Streams) == 0 {
		t.Error("sub_signals=[logs]: expected log streams, got none")
	}
}

// ── REGRESSION: sub-signal gating completeness ───────────────────────────────

// TestSubSignalEmptyDefaultsToAll verifies that an empty SubSignals list (zero Config)
// enables ALL service families — the canonical default path.
// This is the blueprint-reachable path:
//
//	features: { csp_azure: { enabled: true } }   # no sub_signals key → all families
func TestSubSignalEmptyDefaultsToAll(t *testing.T) {
	// Zero Config → applyDefaults → SubSignals = allSubSignals.
	mc, lc := tick(t, &cspazure.Config{}, defaultFx())
	names := nameSet(mc)

	// One representative metric from each family must be present.
	representatives := map[string]string{
		"compute":    "azure_microsoft_compute_virtualmachines_percentage_cpu_average_percent",
		"databases":  "azure_microsoft_sql_servers_databases_cpu_percent_average_percent",
		"storage":    "azure_microsoft_storage_storageaccounts_blobservices_blobcount_average_count",
		"networking": "azure_microsoft_network_loadbalancers_bytecount_total_bytes",
		"messaging":  "azure_microsoft_eventhub_namespaces_incomingmessages_total_count",
	}
	for family, metric := range representatives {
		if !names[metric] {
			t.Errorf("empty sub_signals (default-all): family %q representative %q absent", family, metric)
		}
	}
	// Logs family: streams must be present.
	if len(lc.Streams) == 0 {
		t.Error("empty sub_signals (default-all): no log streams — logs family not emitted")
	}
}

// TestSubSignalDatabasesOnlyGating verifies that sub_signals=["databases"] emits ONLY
// database family metrics and suppresses all other service families.
// Blueprint equivalent:
//
//	features: { csp_azure: { enabled: true, sub_signals: [databases] } }
func TestSubSignalDatabasesOnlyGating(t *testing.T) {
	cfg := &cspazure.Config{SubSignals: []string{"databases"}}
	mc, lc := tick(t, cfg, defaultFx())
	names := nameSet(mc)

	// Databases series must be present.
	for _, want := range databasesInventory {
		if !names[want] {
			t.Errorf("sub_signals=[databases]: missing databases series %q", want)
		}
	}
	// All other metric families must be absent.
	absentFamilies := map[string][]string{
		"compute":    computeInventory,
		"storage":    storageInventory,
		"networking": networkingInventory,
		"messaging":  messagingInventory,
	}
	for family, inv := range absentFamilies {
		for _, absent := range inv {
			if names[absent] {
				t.Errorf("sub_signals=[databases]: family %q series %q must be absent", family, absent)
			}
		}
	}
	// Logs must also be absent (logs is a separate sub-signal).
	if len(lc.Streams) > 0 {
		t.Errorf("sub_signals=[databases]: expected 0 log streams, got %d", len(lc.Streams))
	}
}

// ── (g) Deterministic identity across Builds ─────────────────────────────────

// TestDeterministicIdentity verifies that two independent Builds with the same seed
// emit identical series names and resourceID values.
func TestDeterministicIdentity(t *testing.T) {
	mc1, _ := tick(t, defaultCfg(), &fixture.Set{Seed: "determinism-test"})
	mc2, _ := tick(t, defaultCfg(), &fixture.Set{Seed: "determinism-test"})

	names1 := nameSet(mc1)
	names2 := nameSet(mc2)
	if len(names1) != len(names2) {
		t.Errorf("deterministic: first build=%d series, second build=%d series",
			len(names1), len(names2))
	}
	for n := range names1 {
		if !names2[n] {
			t.Errorf("deterministic: series %q present in first build but not second", n)
		}
	}

	// Also check that the resourceID values are the same across both runs.
	rids1 := resourceIDs(mc1)
	rids2 := resourceIDs(mc2)
	for rid := range rids1 {
		if !rids2[rid] {
			t.Errorf("deterministic: resourceID %q in first build but not second", rid)
		}
	}
}

// ── (h) PG fixture cohesion ────────────────────────────────────────────────────

// TestPGFixtureCohesion verifies that when fx.DBs contains a postgres fixture, the
// emitted PG Flexible Server resourceName equals that fixture's Name.
func TestPGFixtureCohesion(t *testing.T) {
	pgDB := &fixture.DB{
		Engine: "postgres",
		Name:   "pg-app-server-xyz",
	}
	fx := &fixture.Set{
		Seed: "cohesion-test",
		DBs:  []*fixture.DB{pgDB},
	}
	mc, _ := tick(t, defaultCfg(), fx)

	// Find any series that is a PG flexible server metric.
	const pgPrefix = "azure_microsoft_dbforpostgresql_flexibleservers_active_connections_average_count"
	found := false
	for _, s := range mc.Find(pgPrefix) {
		rn := s.Labels["resourceName"]
		if rn == pgDB.Name {
			found = true
			break
		}
	}
	if !found {
		// Collect the actual resourceNames we see for this metric.
		var rns []string
		for _, s := range mc.Find(pgPrefix) {
			rns = append(rns, s.Labels["resourceName"])
		}
		t.Errorf("PG cohesion: expected resourceName=%q for PG anchor metric, got %v",
			pgDB.Name, rns)
	}
}

// ── (i) Logs sub-signal ───────────────────────────────────────────────────────

// TestLogsSubSignal verifies the Event Hubs log stream shape.
func TestLogsSubSignal(t *testing.T) {
	_, lc := tick(t, defaultCfg(), defaultFx())

	if len(lc.Streams) == 0 {
		t.Fatal("no log streams emitted")
	}
	for _, st := range lc.Streams {
		if st.Labels["job"] != "integrations/azure_event_hubs" {
			t.Errorf("stream job=%q want %q", st.Labels["job"], "integrations/azure_event_hubs")
		}
		if st.Labels["topic"] == "" {
			t.Error("stream missing topic label")
		}
		if len(st.Lines) == 0 {
			t.Error("stream has no lines")
		}
		for _, ln := range st.Lines {
			if !strings.HasPrefix(ln.Body, "{") {
				t.Errorf("log line not JSON (does not start with '{'): %q", ln.Body)
			}
		}
	}
}

// ── (j) Base label set completeness ──────────────────────────────────────────

// TestBaseLabelSetExporter verifies the azure_exporter-path base label set (signals/cspazure.md [slug: cspazure]):
// job=integrations/azure, interval/timespan=PT1M, opaque-hash instance, NO credential/region.
func TestBaseLabelSetExporter(t *testing.T) {
	required := []string{
		"job", "resourceID", "subscriptionID", "subscriptionName",
		"resourceGroup", "interval", "timespan", "instance",
	}
	mc, _ := tick(t, exporterCfg(), defaultFx())
	for _, s := range mc.All() {
		for _, k := range required {
			if _, ok := s.Labels[k]; !ok {
				t.Errorf("series %q missing required exporter label %q", s.Name, k)
			}
		}
		if s.Labels["job"] != "integrations/azure" {
			t.Errorf("series %q: job=%q want %q", s.Name, s.Labels["job"], "integrations/azure")
		}
		if s.Labels["interval"] != "PT1M" || s.Labels["timespan"] != "PT1M" {
			t.Errorf("series %q: interval=%q timespan=%q want PT1M/PT1M",
				s.Name, s.Labels["interval"], s.Labels["timespan"])
		}
		if _, ok := s.Labels["credential"]; ok {
			t.Errorf("series %q: azure_exporter path must NOT carry credential label", s.Name)
		}
	}
}

// TestBaseLabelSetServerless verifies the serverless-path base label set (signals/cspazure.md [slug: cspazure],
// SK-16): job=cloud/azure/microsoft-<...>, credential present, region present, instance =
// resourceID, and NO interval/timespan.
func TestBaseLabelSetServerless(t *testing.T) {
	required := []string{
		"job", "resourceID", "subscriptionID", "subscriptionName",
		"resourceGroup", "credential", "region", "instance",
	}
	mc, _ := tick(t, serverlessCfg(), defaultFx())
	for _, s := range mc.All() {
		for _, k := range required {
			if _, ok := s.Labels[k]; !ok {
				t.Errorf("series %q missing required serverless label %q", s.Name, k)
			}
		}
		if !strings.HasPrefix(s.Labels["job"], "cloud/azure/microsoft-") {
			t.Errorf("series %q: job=%q want prefix cloud/azure/microsoft-", s.Name, s.Labels["job"])
		}
		if s.Labels["credential"] != "ps_azure" {
			t.Errorf("series %q: credential=%q want %q", s.Name, s.Labels["credential"], "ps_azure")
		}
		if _, ok := s.Labels["interval"]; ok {
			t.Errorf("series %q: serverless path must NOT carry interval label", s.Name)
		}
		if _, ok := s.Labels["timespan"]; ok {
			t.Errorf("series %q: serverless path must NOT carry timespan label", s.Name)
		}
	}
}

// TestServerlessCredentialDefault verifies a zero Config builds on the serverless default
// path with the credential label defaulted to "azure" (deployment overrides via the knob).
func TestServerlessCredentialDefault(t *testing.T) {
	mc, _ := tick(t, &cspazure.Config{}, defaultFx())
	if len(mc.All()) == 0 {
		t.Fatal("no series emitted")
	}
	for _, s := range mc.All() {
		if s.Labels["credential"] != "azure" {
			t.Errorf("series %q: default credential=%q want %q", s.Name, s.Labels["credential"], "azure")
			break
		}
	}
}

// TestInvalidIngestionPathRejected verifies an unknown ingestion_path fails loud at Build.
func TestInvalidIngestionPathRejected(t *testing.T) {
	reg := cspazure.Registration()
	if _, err := reg.Build(&cspazure.Config{IngestionPath: "bogus"}, defaultFx()); err == nil {
		t.Fatal("expected Build error for ingestion_path=bogus, got nil")
	}
}

// TestAppGatewayInstancePathDetermined verifies the App Gateway `instance` label is now
// path-determined like every other resource (SK-16): serverless → resourceID; azure_exporter
// → opaque hash. The old fixed instance="integrations/azure_exporter" is gone.
func TestAppGatewayInstancePathDetermined(t *testing.T) {
	const agMetric = "azure_microsoft_network_applicationgateways_failedrequests_total_count"

	mcS, _ := tick(t, serverlessCfg(), defaultFx())
	for _, s := range mcS.Find(agMetric) {
		if s.Labels["instance"] != s.Labels["resourceID"] {
			t.Errorf("serverless %q: instance=%q want resourceID=%q",
				agMetric, s.Labels["instance"], s.Labels["resourceID"])
		}
	}

	mcE, _ := tick(t, exporterCfg(), defaultFx())
	for _, s := range mcE.Find(agMetric) {
		inst := s.Labels["instance"]
		if inst == "integrations/azure_exporter" || inst == s.Labels["resourceID"] || inst == "" {
			t.Errorf("exporter %q: instance=%q should be an opaque hash (not the old fixed value/resourceID)",
				agMetric, inst)
		}
	}
}

// TestVNetRegionLabel verifies that VNet metrics carry the region label.
func TestVNetRegionLabel(t *testing.T) {
	mc, _ := tick(t, defaultCfg(), defaultFx())
	const vnetMetric = "azure_microsoft_network_virtualnetworks_subnets_count"
	series := mc.Find(vnetMetric)
	if len(series) == 0 {
		t.Fatalf("no %q series found", vnetMetric)
	}
	for _, s := range series {
		if _, ok := s.Labels["region"]; !ok {
			t.Errorf("%q missing region label", vnetMetric)
		}
	}
}

// TestVNetNoAggregationSuffix verifies that VNet metrics do NOT have _average_ or _total_
// in the name (extract §D.12 / §J.6).
func TestVNetNoAggregationSuffix(t *testing.T) {
	mc, _ := tick(t, defaultCfg(), defaultFx())
	names := nameSet(mc)
	// These correct names should exist.
	correctVNetNames := []string{
		"azure_microsoft_network_virtualnetworks_subnets_count",
		"azure_microsoft_network_virtualnetworks_availableaddresses_count",
		"azure_microsoft_network_virtualnetworks_connectedpeerings_count",
		"azure_microsoft_network_virtualnetworks_peerings_count",
	}
	for _, n := range correctVNetNames {
		if !names[n] {
			t.Errorf("VNet: missing %q", n)
		}
	}
	// These wrong names must NOT exist.
	wrongNames := []string{
		"azure_microsoft_network_virtualnetworks_subnets_average_count",
		"azure_microsoft_network_virtualnetworks_subnets_total_count",
	}
	for _, n := range wrongNames {
		if names[n] {
			t.Errorf("VNet TRAP: wrong aggregation-suffixed name emitted: %q", n)
		}
	}
}

// TestEventHubDimensionEntityName verifies per-hub metrics carry dimension_EntityName
// (underscore separator + CamelCase, matching the azure_exporter convention).
func TestEventHubDimensionEntityName(t *testing.T) {
	mc, _ := tick(t, defaultCfg(), defaultFx())
	const metric = "azure_microsoft_eventhub_namespaces_incomingmessages_total_count"
	series := mc.Find(metric)
	if len(series) == 0 {
		t.Fatalf("no %q series found", metric)
	}
	for _, s := range series {
		if _, ok := s.Labels["dimension_EntityName"]; !ok {
			t.Errorf("%q missing dimension_EntityName label (got labels: %v)", metric, s.Labels)
		}
		// Old camelCase form must be absent.
		if _, ok := s.Labels["dimensionEntityName"]; ok {
			t.Errorf("%q has old camelCase label dimensionEntityName — must use dimension_EntityName", metric)
		}
	}
}

// TestStorageTransactionDimensionLabels verifies that blob and queue transaction series
// carry dimension_ApiName and dimension_ResponseType (underscore + CamelCase).
func TestStorageTransactionDimensionLabels(t *testing.T) {
	mc, _ := tick(t, defaultCfg(), defaultFx())
	for _, metric := range []string{
		"azure_microsoft_storage_storageaccounts_blobservices_transactions_total_count",
		"azure_microsoft_storage_storageaccounts_queueservices_transactions_total_count",
	} {
		series := mc.Find(metric)
		if len(series) == 0 {
			t.Fatalf("no %q series found", metric)
		}
		for _, s := range series {
			if _, ok := s.Labels["dimension_ApiName"]; !ok {
				t.Errorf("%q missing dimension_ApiName label (got: %v)", metric, s.Labels)
			}
			if _, ok := s.Labels["dimension_ResponseType"]; !ok {
				t.Errorf("%q missing dimension_ResponseType label (got: %v)", metric, s.Labels)
			}
			// Old camelCase forms must be absent.
			if _, ok := s.Labels["dimensionApiname"]; ok {
				t.Errorf("%q has old camelCase label dimensionApiname", metric)
			}
			if _, ok := s.Labels["dimensionResponseType"]; ok {
				t.Errorf("%q has old camelCase label dimensionResponseType", metric)
			}
		}
	}
}

// TestFrontDoorRequestCountDimensionLabels verifies that Front Door requestcount series
// carry dimension_Endpoint, dimension_ClientCountry, and dimension_HttpStatusGroup.
func TestFrontDoorRequestCountDimensionLabels(t *testing.T) {
	mc, _ := tick(t, defaultCfg(), defaultFx())
	const metric = "azure_microsoft_cdn_profiles_requestcount_total_count"
	series := mc.Find(metric)
	if len(series) == 0 {
		t.Fatalf("no %q series found", metric)
	}
	for _, s := range series {
		if _, ok := s.Labels["dimension_Endpoint"]; !ok {
			t.Errorf("%q missing dimension_Endpoint label (got: %v)", metric, s.Labels)
		}
		if _, ok := s.Labels["dimension_ClientCountry"]; !ok {
			t.Errorf("%q missing dimension_ClientCountry label (got: %v)", metric, s.Labels)
		}
		if _, ok := s.Labels["dimension_HttpStatusGroup"]; !ok {
			t.Errorf("%q missing dimension_HttpStatusGroup label (got: %v)", metric, s.Labels)
		}
		// Old camelCase forms must be absent.
		for _, old := range []string{"dimensionEndpoint", "dimensionClientCountry", "dimensionHttpStatusGroup"} {
			if _, ok := s.Labels[old]; ok {
				t.Errorf("%q has old camelCase label %q", metric, old)
			}
		}
	}
}

// TestBlobCapacityTierDimensions verifies that blobcount and blobcapacity series carry
// dimension_BlobType and dimension_Tier, with one series per tier in the expected enum.
func TestBlobCapacityTierDimensions(t *testing.T) {
	mc, _ := tick(t, defaultCfg(), defaultFx())
	expectedTiers := []string{"hot", "cool", "transactionoptimized", "untiered"}

	for _, metric := range []string{
		"azure_microsoft_storage_storageaccounts_blobservices_blobcount_average_count",
		"azure_microsoft_storage_storageaccounts_blobservices_blobcapacity_average_bytes",
	} {
		series := mc.Find(metric)
		if len(series) == 0 {
			t.Fatalf("no %q series found", metric)
		}
		foundTiers := map[string]bool{}
		for _, s := range series {
			tier, hasTier := s.Labels["dimension_Tier"]
			_, hasBlobType := s.Labels["dimension_BlobType"]
			if !hasTier {
				t.Errorf("%q missing dimension_Tier label (got: %v)", metric, s.Labels)
				continue
			}
			if !hasBlobType {
				t.Errorf("%q missing dimension_BlobType label (got: %v)", metric, s.Labels)
				continue
			}
			if s.Labels["dimension_BlobType"] != "blockblob" {
				t.Errorf("%q dimension_BlobType=%q want %q", metric, s.Labels["dimension_BlobType"], "blockblob")
			}
			foundTiers[tier] = true
		}
		for _, tier := range expectedTiers {
			if !foundTiers[tier] {
				t.Errorf("%q missing tier %q series", metric, tier)
			}
		}
	}
}

// ── azure_exporter-path dimension form (SK-16) ───────────────────────────────

// TestExporterMultiDimensionNoUnderscore verifies that on the azure_exporter path MULTIPLE
// dimensions ride as dimension<Name> (NO underscore, CamelCase) — e.g. Front Door
// requestcount carries dimensionEndpoint/dimensionClientCountry/dimensionHttpStatusGroup,
// NOT the serverless dimension_<Name> form.
func TestExporterMultiDimensionNoUnderscore(t *testing.T) {
	mc, _ := tick(t, exporterCfg(), defaultFx())
	const metric = "azure_microsoft_cdn_profiles_requestcount_total_count"
	series := mc.Find(metric)
	if len(series) == 0 {
		t.Fatalf("no %q series found", metric)
	}
	for _, s := range series {
		for _, want := range []string{"dimensionEndpoint", "dimensionClientCountry", "dimensionHttpStatusGroup"} {
			if _, ok := s.Labels[want]; !ok {
				t.Errorf("%q missing exporter label %q (got: %v)", metric, want, s.Labels)
			}
		}
		for _, bad := range []string{"dimension_Endpoint", "dimension_ClientCountry", "dimension_HttpStatusGroup"} {
			if _, ok := s.Labels[bad]; ok {
				t.Errorf("%q has serverless underscore label %q on exporter path", metric, bad)
			}
		}
		// HttpStatusGroup value must be uppercased on the exporter path.
		if v := s.Labels["dimensionHttpStatusGroup"]; v != strings.ToUpper(v) {
			t.Errorf("%q dimensionHttpStatusGroup=%q must be uppercase on exporter path", metric, v)
		}
	}
}

// TestExporterSingleDimensionBare verifies that on the azure_exporter path a SINGLE
// requested dimension is the bare label dimension="<value>" — e.g. Service Bus per-entity
// metrics carry dimension=<queue> rather than dimension_EntityName.
func TestExporterSingleDimensionBare(t *testing.T) {
	mc, _ := tick(t, exporterCfg(), defaultFx())
	const metric = "azure_microsoft_servicebus_namespaces_size_average_bytes"
	series := mc.Find(metric)
	if len(series) == 0 {
		t.Fatalf("no %q series found", metric)
	}
	for _, s := range series {
		if _, ok := s.Labels["dimension"]; !ok {
			t.Errorf("%q missing bare dimension label on exporter path (got: %v)", metric, s.Labels)
		}
		if _, ok := s.Labels["dimension_EntityName"]; ok {
			t.Errorf("%q has serverless dimension_EntityName on exporter path", metric)
		}
		if _, ok := s.Labels["dimensionEntityName"]; ok {
			t.Errorf("%q has multi-dimension form for a single dimension on exporter path", metric)
		}
	}
}

// TestServerlessStatusGroupLowercase verifies the serverless path emits lowercase
// HttpStatusGroup values (2xx/4xx/5xx) — contrast the exporter uppercase form.
func TestServerlessStatusGroupLowercase(t *testing.T) {
	mc, _ := tick(t, serverlessCfg(), defaultFx())
	const metric = "azure_microsoft_cdn_profiles_requestcount_total_count"
	for _, s := range mc.Find(metric) {
		v := s.Labels["dimension_HttpStatusGroup"]
		if v != strings.ToLower(v) {
			t.Errorf("%q dimension_HttpStatusGroup=%q must be lowercase on serverless path", metric, v)
		}
	}
}

// TestElasticPoolResourceNameByPath verifies the SQL elastic-pool resourceName policy
// (SK-16, live-confirmed managed scraper 2026-06-14): azure_exporter → resourceName=<server>;
// serverless → the joined "<server>/<pool>" form. The resourceID always ends
// /elasticpools/<pool> with lowercase "elasticpools".
func TestElasticPoolResourceNameByPath(t *testing.T) {
	const metric = "azure_microsoft_sql_servers_elasticpools_storage_used_average_bytes"

	mcE, _ := tick(t, exporterCfg(), defaultFx())
	poolE := mcE.Find(metric)
	if len(poolE) == 0 {
		t.Fatalf("no %q series found (exporter)", metric)
	}
	for _, s := range poolE {
		rn := s.Labels["resourceName"]
		if !strings.Contains(strings.ToLower(s.Labels["resourceID"]),
			"/servers/"+strings.ToLower(rn)+"/elasticpools/") {
			t.Errorf("exporter %q: resourceName=%q not the server in resourceID=%q",
				metric, rn, s.Labels["resourceID"])
		}
	}

	mcS, _ := tick(t, serverlessCfg(), defaultFx())
	poolS := mcS.Find(metric)
	if len(poolS) == 0 {
		t.Fatalf("no %q series found (serverless)", metric)
	}
	for _, s := range poolS {
		rn := s.Labels["resourceName"]
		// Expect "<server>/<pool>"; resourceID ends /elasticpools/<pool>.
		if !strings.Contains(rn, "/") {
			t.Errorf("serverless %q: resourceName=%q want joined <server>/<pool> form", metric, rn)
		}
		if !strings.HasSuffix(strings.ToLower(s.Labels["resourceID"]), "/elasticpools/"+strings.ToLower(rn[strings.LastIndex(rn, "/")+1:])) {
			t.Errorf("serverless %q: resourceID=%q does not end /elasticpools/<pool> (rn=%q)",
				metric, s.Labels["resourceID"], rn)
		}
	}
}

// TestSqlDatabaseResourceNameByPath verifies SQL database resourceName (live-confirmed):
// serverless → "<server>/<db>"; azure_exporter → the bare <db>.
func TestSqlDatabaseResourceNameByPath(t *testing.T) {
	const metric = "azure_microsoft_sql_servers_databases_cpu_percent_average_percent"

	mcS, _ := tick(t, serverlessCfg(), defaultFx())
	for _, s := range mcS.Find(metric) {
		rn := s.Labels["resourceName"]
		if !strings.Contains(rn, "/") {
			t.Errorf("serverless %q: resourceName=%q want joined <server>/<db> form", metric, rn)
		}
	}

	mcE, _ := tick(t, exporterCfg(), defaultFx())
	for _, s := range mcE.Find(metric) {
		if rn := s.Labels["resourceName"]; strings.Contains(rn, "/") {
			t.Errorf("exporter %q: resourceName=%q want bare <db>, got joined form", metric, rn)
		}
	}
}

// TestStorageResourceIDAccountOnly verifies the storage resourceID is account-only on BOTH
// paths (live-confirmed managed scraper 2026-06-14) — the blob/queue namespace lives in the
// serverless job slug + metric name, never the resourceID.
func TestStorageResourceIDAccountOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *cspazure.Config
	}{{"serverless", serverlessCfg()}, {"exporter", exporterCfg()}} {
		mc, _ := tick(t, tc.cfg, defaultFx())
		for _, metric := range []string{
			"azure_microsoft_storage_storageaccounts_blobservices_blobcount_average_count",
			"azure_microsoft_storage_storageaccounts_queueservices_queuecount_average_count",
		} {
			series := mc.Find(metric)
			if len(series) == 0 {
				t.Fatalf("%s: no %q series", tc.name, metric)
			}
			for _, s := range series {
				rid := strings.ToLower(s.Labels["resourceID"])
				if strings.Contains(rid, "/blobservices") || strings.Contains(rid, "/queueservices") ||
					strings.HasSuffix(rid, "/default") {
					t.Errorf("%s %q: resourceID must be account-only, got %q", tc.name, metric, s.Labels["resourceID"])
				}
				rn := s.Labels["resourceName"]
				if !strings.HasSuffix(rid, "/"+strings.ToLower(rn)) {
					t.Errorf("%s %q: resourceID=%q must end with the account resourceName=%q",
						tc.name, metric, s.Labels["resourceID"], rn)
				}
			}
		}
	}
}

// TestServerlessJobSlugMatchesProvider verifies serverless job slugs match the live managed
// scraper format (microsoft-<provider>-<type>[-<subtype>]), incl. the nested SQL + storage
// sub-service forms confirmed on a live reference cluster (2026-06-14).
func TestServerlessJobSlugMatchesProvider(t *testing.T) {
	mc, _ := tick(t, serverlessCfg(), defaultFx())
	want := map[string]string{
		"azure_microsoft_compute_virtualmachines_percentage_cpu_average_percent":         "cloud/azure/microsoft-compute-virtualmachines",
		"azure_microsoft_sql_servers_databases_cpu_percent_average_percent":              "cloud/azure/microsoft-sql-servers-databases",
		"azure_microsoft_sql_servers_elasticpools_storage_used_average_bytes":            "cloud/azure/microsoft-sql-servers-elasticpools",
		"azure_microsoft_storage_storageaccounts_blobservices_blobcount_average_count":   "cloud/azure/microsoft-storage-storageaccounts-blobservices",
		"azure_microsoft_storage_storageaccounts_queueservices_queuecount_average_count": "cloud/azure/microsoft-storage-storageaccounts-queueservices",
		"azure_microsoft_servicebus_namespaces_messages_average_count":                   "cloud/azure/microsoft-servicebus-namespaces",
	}
	for metric, job := range want {
		series := mc.Find(metric)
		if len(series) == 0 {
			t.Errorf("no %q series", metric)
			continue
		}
		if got := series[0].Labels["job"]; got != job {
			t.Errorf("%q: job=%q want %q", metric, got, job)
		}
	}
}

// TestExporterDeterministicInstance verifies the opaque exporter instance hash is
// deterministic across independent Builds with the same seed.
func TestExporterDeterministicInstance(t *testing.T) {
	mc1, _ := tick(t, exporterCfg(), &fixture.Set{Seed: "inst-determinism"})
	mc2, _ := tick(t, exporterCfg(), &fixture.Set{Seed: "inst-determinism"})
	inst := func(mc *coretest.MetricCapture) map[string]string {
		out := map[string]string{}
		for _, s := range mc.All() {
			out[s.Labels["resourceID"]] = s.Labels["instance"]
		}
		return out
	}
	i1, i2 := inst(mc1), inst(mc2)
	for rid, v := range i1 {
		if i2[rid] != v {
			t.Errorf("instance for %q not deterministic: %q vs %q", rid, v, i2[rid])
		}
	}
}

// TestResourceTagsByConfig verifies the opt-in tags knob: configured tags ride as
// `tag_<key>` on every series (both paths); omitting tags emits NO tag labels (matching a
// default managed scraper, live-confirmed to surface no tags).
func TestResourceTagsByConfig(t *testing.T) {
	// No tags configured → no tag_* labels.
	mcNone, _ := tick(t, serverlessCfg(), defaultFx())
	for _, s := range mcNone.All() {
		for k := range s.Labels {
			if strings.HasPrefix(k, "tag_") {
				t.Errorf("series %q: unexpected tag label %q when no tags configured", s.Name, k)
			}
		}
	}

	// Tags configured → tag_<key> on every series, on both paths.
	tags := map[string]string{"app": "checkout", "env": "prod", "owner": "team-a"}
	for _, tc := range []struct {
		name string
		cfg  *cspazure.Config
	}{
		{"serverless", &cspazure.Config{IngestionPath: "serverless", Credential: "ps_azure", Tags: tags}},
		{"exporter", &cspazure.Config{IngestionPath: "azure_exporter", Tags: tags}},
	} {
		mc, _ := tick(t, tc.cfg, defaultFx())
		if len(mc.All()) == 0 {
			t.Fatalf("%s: no series", tc.name)
		}
		for _, s := range mc.All() {
			for k, v := range tags {
				if s.Labels["tag_"+k] != v {
					t.Errorf("%s series %q: tag_%s=%q want %q", tc.name, s.Name, k, s.Labels["tag_"+k], v)
				}
			}
		}
	}
}

// ── (k) AI sub-signal (Azure OpenAI / Cognitive Services) ────────────────────

// aiInventory is the expected set of account-level AI metrics (no per-deployment dims).
var aiAccountInventory = []string{
	"azure_microsoft_cognitiveservices_accounts_total_calls_total_count",
	"azure_microsoft_cognitiveservices_accounts_successful_calls_total_count",
	"azure_microsoft_cognitiveservices_accounts_blocked_calls_total_count",
	"azure_microsoft_cognitiveservices_accounts_total_errors_total_count",
	"azure_microsoft_cognitiveservices_accounts_client_errors_total_count",
	"azure_microsoft_cognitiveservices_accounts_server_errors_total_count",
	"azure_microsoft_cognitiveservices_accounts_total_token_calls_total_count",
}

// aiDeployInventory is the set of per-deployment AI metrics.
var aiDeployInventory = []string{
	"azure_microsoft_cognitiveservices_accounts_processed_prompt_tokens_total_count",
	"azure_microsoft_cognitiveservices_accounts_generated_completion_tokens_total_count",
	"azure_microsoft_cognitiveservices_accounts_tokens_per_second_average_count",
	"azure_microsoft_cognitiveservices_accounts_processed_fine_tuned_training_hours_total_count",
}

// TestAIInventory verifies the ai sub-signal emits all expected metric families.
func TestAIInventory(t *testing.T) {
	cfg := &cspazure.Config{SubSignals: []string{"ai"}}
	mc, _ := tick(t, cfg, defaultFx())
	names := nameSet(mc)
	for _, want := range aiAccountInventory {
		if !names[want] {
			t.Errorf("ai: missing account-level series %q", want)
		}
	}
	for _, want := range aiDeployInventory {
		if !names[want] {
			t.Errorf("ai: missing per-deployment series %q", want)
		}
	}
}

// TestAINotInDefault verifies that the ai sub-signal is NOT emitted by the default (empty) config.
// This is the M3 invariant: existing blueprints must remain byte-identical (n-1).
func TestAINotInDefault(t *testing.T) {
	mc, _ := tick(t, &cspazure.Config{}, defaultFx())
	names := nameSet(mc)
	for _, absent := range append(aiAccountInventory, aiDeployInventory...) {
		if names[absent] {
			t.Errorf("ai NOT-IN-DEFAULT: series %q must be absent when sub_signals is empty (default-all), got it emitted", absent)
		}
	}
}

// TestAINotInDefaultEmptyDefaultsToAll re-confirms that sub_signals=[] → allSubSignals,
// and allSubSignals does NOT include "ai".
func TestAIEmptySubSignalsDoesNotActivateAI(t *testing.T) {
	// Zero Config → applyDefaults → SubSignals = allSubSignals (no "ai").
	mc, lc := tick(t, &cspazure.Config{}, defaultFx())
	names := nameSet(mc)
	// existing families still present
	if !names["azure_microsoft_compute_virtualmachines_percentage_cpu_average_percent"] {
		t.Error("compute family absent in default-all — unexpected regression")
	}
	_ = lc
	// ai family absent
	for _, absent := range aiAccountInventory {
		if names[absent] {
			t.Errorf("ai opt-in violated: %q present in default-all emission", absent)
		}
	}
}

// TestAIEnvLabelStamped verifies that when fx.Env is set the `env` label is stamped on every
// ai series, and when fx.Env is nil the `env` label is absent (I13).
func TestAIEnvLabelStamped(t *testing.T) {
	cfg := &cspazure.Config{SubSignals: []string{"ai"}}

	// With Env — env label must be present and equal Env.Name.
	fxWithEnv := &fixture.Set{
		Seed: "test",
		Env:  &fixture.Env{Name: "staging", Weight: 0.5, NonProd: true},
	}
	mcEnv, _ := tick(t, cfg, fxWithEnv)
	if len(mcEnv.All()) == 0 {
		t.Fatal("ai with Env: no series emitted")
	}
	for _, s := range mcEnv.All() {
		if !strings.HasPrefix(s.Name, "azure_microsoft_cognitiveservices_") {
			continue
		}
		v, ok := s.Labels["env"]
		if !ok {
			t.Errorf("ai env-scoped: series %q missing env label", s.Name)
		} else if v != "staging" {
			t.Errorf("ai env-scoped: series %q env=%q want %q", s.Name, v, "staging")
		}
	}

	// Without Env — env label must be absent (I13: absent never "").
	fxNoEnv := &fixture.Set{Seed: "test"}
	mcNoEnv, _ := tick(t, cfg, fxNoEnv)
	if len(mcNoEnv.All()) == 0 {
		t.Fatal("ai without Env: no series emitted")
	}
	for _, s := range mcNoEnv.All() {
		if !strings.HasPrefix(s.Name, "azure_microsoft_cognitiveservices_") {
			continue
		}
		if v, ok := s.Labels["env"]; ok {
			t.Errorf("ai aggregate: series %q must NOT carry env label (got env=%q)", s.Name, v)
		}
	}
}

// TestAIResourceIDCasing verifies the ARM provider path casing for cognitiveservices.
// Serverless path: provider segment must be Microsoft.CognitiveServices/accounts.
func TestAIResourceIDCasing(t *testing.T) {
	cfg := &cspazure.Config{
		SubSignals:    []string{"ai"},
		IngestionPath: "serverless",
		Credential:    "ps_azure",
	}
	mc, _ := tick(t, cfg, defaultFx())
	const anchor = "azure_microsoft_cognitiveservices_accounts_total_calls_total_count"
	series := mc.Find(anchor)
	if len(series) == 0 {
		t.Fatalf("no %q series found on serverless path", anchor)
	}
	for _, s := range series {
		rid := s.Labels["resourceID"]
		if !strings.Contains(rid, "/providers/Microsoft.CognitiveServices/accounts/") {
			t.Errorf("serverless AI: resourceID=%q want Microsoft.CognitiveServices/accounts segment", rid)
		}
	}
}

// TestAIPerDeploymentDimensions verifies that per-deployment metrics carry
// ModelDeploymentName and ModelName dimension labels on the serverless path.
func TestAIPerDeploymentDimensions(t *testing.T) {
	cfg := &cspazure.Config{SubSignals: []string{"ai"}}
	mc, _ := tick(t, cfg, defaultFx())
	const metric = "azure_microsoft_cognitiveservices_accounts_processed_prompt_tokens_total_count"
	series := mc.Find(metric)
	if len(series) == 0 {
		t.Fatalf("no %q series found", metric)
	}
	for _, s := range series {
		if _, ok := s.Labels["dimension_ModelDeploymentName"]; !ok {
			t.Errorf("%q missing dimension_ModelDeploymentName label (got: %v)", metric, s.Labels)
		}
		if _, ok := s.Labels["dimension_ModelName"]; !ok {
			t.Errorf("%q missing dimension_ModelName label (got: %v)", metric, s.Labels)
		}
	}
}

// TestAIPerModelVolumeWeight verifies that per-deployment token emissions are differentiated
// by model volume-weight. A high-weight model (gpt-4o-mini, weight 6.0) must emit more prompt
// tokens than a low-weight model (o3, weight 1.0). This will fail with the pre-fix code
// because a single Noise draw is shared across all deployments (lockstep magnitudes).
func TestAIPerModelVolumeWeight(t *testing.T) {
	cfg := &cspazure.Config{SubSignals: []string{"ai"}}
	mc, _ := tick(t, cfg, defaultFx())
	const metric = "azure_microsoft_cognitiveservices_accounts_processed_prompt_tokens_total_count"

	// Collect prompt-token value per ModelName.
	byModel := map[string]float64{}
	for _, s := range mc.Find(metric) {
		mn := s.Labels["dimension_ModelName"]
		if mn != "" {
			byModel[mn] += s.Value
		}
	}
	if len(byModel) < 2 {
		t.Fatalf("expected at least 2 distinct ModelName series for %q, got %v", metric, byModel)
	}
	miniToks, hasMini := byModel["gpt-4o-mini"]
	o3Toks, hasO3 := byModel["o3"]
	if !hasMini || !hasO3 {
		t.Fatalf("expected both gpt-4o-mini and o3 in emission; got models: %v", byModel)
	}
	// gpt-4o-mini weight=6.0; o3 weight=1.0 → mini should emit markedly more tokens.
	if miniToks <= o3Toks {
		t.Errorf("per-model weight not applied: gpt-4o-mini tokens=%.0f must exceed o3 tokens=%.0f (weight ratio 6:1)",
			miniToks, o3Toks)
	}
}

// TestAIEmbeddingZeroCompletionTokens verifies that embedding models emit ~0 completion tokens.
// Embeddings have no output tokens in reality; the test checks the generated_completion_tokens
// value for the embed model is significantly lower than for a chat model.
func TestAIEmbeddingZeroCompletionTokens(t *testing.T) {
	cfg := &cspazure.Config{SubSignals: []string{"ai"}}
	mc, _ := tick(t, cfg, defaultFx())
	const metric = "azure_microsoft_cognitiveservices_accounts_generated_completion_tokens_total_count"

	byModel := map[string]float64{}
	for _, s := range mc.Find(metric) {
		mn := s.Labels["dimension_ModelName"]
		if mn != "" {
			byModel[mn] += s.Value
		}
	}
	embedToks, hasEmbed := byModel["text-embedding-3-small"]
	chatToks, hasChat := byModel["gpt-4o"]
	if !hasEmbed || !hasChat {
		t.Fatalf("expected both text-embedding-3-small and gpt-4o completion-token series; got: %v", byModel)
	}
	// Embed model completion tokens must be near-zero (≤ 1); chat model must be positive.
	if embedToks > 1 {
		t.Errorf("embedding completion tokens should be ~0, got %.2f", embedToks)
	}
	if chatToks <= 0 {
		t.Errorf("chat model completion tokens should be positive, got %.2f", chatToks)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func nameSet(mc *coretest.MetricCapture) map[string]bool {
	out := map[string]bool{}
	for _, n := range mc.Names() {
		out[n] = true
	}
	return out
}

func resourceIDs(mc *coretest.MetricCapture) map[string]bool {
	out := map[string]bool{}
	for _, s := range mc.All() {
		if rid, ok := s.Labels["resourceID"]; ok {
			out[rid] = true
		}
	}
	return out
}
