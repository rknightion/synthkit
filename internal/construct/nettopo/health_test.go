// SPDX-License-Identifier: AGPL-3.0-only

package nettopo

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

var healthNow = time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

// makeHealthConstruct builds a standalone Construct with 3 devices, 2 edges,
// lldp+bgp protocols — the minimal fixture that exercises all always-on families.
func makeHealthConstruct() *Construct {
	return &Construct{
		rc: resolvedConfig{
			job:         "integrations/network-topology-exporter",
			instance:    "netobs-dc1:9100",
			role:        RoleStandalone,
			protocols:   []string{ProtoLLDP, ProtoBGP},
			protoSet:    map[string]bool{ProtoLLDP: true, ProtoBGP: true},
			sessionPool: false,
			oosCount:    0,
			otlpOutput:  false,
			spokes:      nil,
		},
		graph: Graph{
			Devices: []Device{
				{ID: "spine-01", Vendor: "arista", OSVersion: "4.36.0F", Site: "dc1"},
				{ID: "leaf-01", Vendor: "arista", OSVersion: "4.36.0F", Site: "dc1"},
				{ID: "leaf-02", Vendor: "cisco", OSVersion: "17.12.1", Site: "dc1"},
			},
			Edges: []Edge{
				{SrcDevice: "spine-01", SrcPort: "Ethernet1", DstDevice: "leaf-01", DstPort: "Ethernet1",
					Proto: ProtoLLDP, LinkKind: LinkEthernet, Direction: DirBidirectional},
				{SrcDevice: "spine-01", SrcPort: "Ethernet2", DstDevice: "leaf-02", DstPort: "GigabitEthernet0/1",
					Proto: ProtoLLDP, LinkKind: LinkEthernet, Direction: DirBidirectional},
			},
		},
		st: state.NewState(),
	}
}

// runHealthOnce calls buildHealth then collects the resulting series.
func runHealthOnce(c *Construct, now time.Time, eng *shape.Engine) []promrw.Series {
	c.buildHealth(now, eng)
	return c.st.Collect(now)
}

// hasName returns true if any series in batch has the given name.
func hasName(batch []promrw.Series, name string) bool {
	for _, s := range batch {
		if s.Name == name {
			return true
		}
	}
	return false
}

// hasPrefixName returns true if any series in batch has a name starting with prefix.
func hasPrefixName(batch []promrw.Series, prefix string) bool {
	for _, s := range batch {
		if len(s.Name) >= len(prefix) && s.Name[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// findLabeled returns first series matching name and label key=val.
func findLabeled(batch []promrw.Series, name, key, val string) (promrw.Series, bool) {
	for _, s := range batch {
		if s.Name == name && s.Labels[key] == val {
			return s, true
		}
	}
	return promrw.Series{}, false
}

// countByName returns the number of series with the given name.
func countByName(batch []promrw.Series, name string) int {
	n := 0
	for _, s := range batch {
		if s.Name == name {
			n++
		}
	}
	return n
}

// ────────────────────────────────────────────────────────────────────────────
// Always-on: graph freshness
// ────────────────────────────────────────────────────────────────────────────

func TestBuildHealth_GraphStaleIsZero(t *testing.T) {
	c := makeHealthConstruct()
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	s, ok := findLabeled(series, "network_topology_graph_stale", "job", c.rc.job)
	if !ok {
		t.Fatal("network_topology_graph_stale not found")
	}
	if s.Value != 0 {
		t.Errorf("graph_stale = %v, want 0", s.Value)
	}
}

func TestBuildHealth_SnapshotLastWrittenTimestamp(t *testing.T) {
	c := makeHealthConstruct()
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	s, ok := findLabeled(series, "network_topology_snapshot_last_written_timestamp_seconds", "job", c.rc.job)
	if !ok {
		t.Fatal("snapshot_last_written_timestamp_seconds not found")
	}
	want := float64(healthNow.Unix())
	if s.Value != want {
		t.Errorf("snapshot_last_written_timestamp_seconds = %v, want %v", s.Value, want)
	}
}

func TestBuildHealth_SnapshotLoadedDevicesTotal(t *testing.T) {
	c := makeHealthConstruct()
	nDev := len(c.graph.Devices)
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	s, ok := findLabeled(series, "network_topology_snapshot_loaded_devices_total", "job", c.rc.job)
	if !ok {
		t.Fatal("snapshot_loaded_devices_total not found")
	}
	if s.Value != float64(nDev) {
		t.Errorf("snapshot_loaded_devices_total = %v, want %v", s.Value, nDev)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Always-on: discovery cycle health
// ────────────────────────────────────────────────────────────────────────────

func TestBuildHealth_DiscoveryDevicesTotal_Success(t *testing.T) {
	c := makeHealthConstruct()
	nDev := len(c.graph.Devices)
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	s, ok := findLabeled(series, "network_topology_discovery_devices_total", "status", "success")
	if !ok {
		t.Fatal("discovery_devices_total{status=success} not found")
	}
	if s.Value != float64(nDev) {
		t.Errorf("discovery_devices_total{status=success} = %v, want %v", s.Value, nDev)
	}
	if s.Labels["reason"] != "n/a" {
		t.Errorf("discovery_devices_total{status=success} reason = %q, want n/a", s.Labels["reason"])
	}
}

func TestBuildHealth_DiscoveryCycleDurationHistogram(t *testing.T) {
	c := makeHealthConstruct()
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	if !hasName(series, "network_topology_discovery_cycle_duration_seconds_bucket") {
		t.Error("network_topology_discovery_cycle_duration_seconds_bucket not found")
	}
	if !hasName(series, "network_topology_discovery_cycle_duration_seconds_sum") {
		t.Error("network_topology_discovery_cycle_duration_seconds_sum not found")
	}
	if !hasName(series, "network_topology_discovery_cycle_duration_seconds_count") {
		t.Error("network_topology_discovery_cycle_duration_seconds_count not found")
	}
}

func TestBuildHealth_ModuleDurationHistogram(t *testing.T) {
	c := makeHealthConstruct()
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	// One histogram per module (protocols + "snmp")
	if !hasPrefixName(series, "network_topology_discovery_module_duration_seconds_bucket") {
		t.Error("network_topology_discovery_module_duration_seconds_bucket not found")
	}
	// Each module should have its own series keyed by the "module" label.
	modules := append(append([]string{}, c.rc.protocols...), "snmp")
	for _, mod := range modules {
		if _, ok := findLabeled(series, "network_topology_discovery_module_duration_seconds_count", "module", mod); !ok {
			t.Errorf("discovery_module_duration_seconds_count missing for module=%q", mod)
		}
	}
}

func TestBuildHealth_WalkerOutcome_LLDPEdgesPresent(t *testing.T) {
	c := makeHealthConstruct()
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	// lldp is in protocols — walker_outcome_total{walker=lldp,outcome=edges} must exist.
	found := false
	for _, s := range series {
		if s.Name == "network_topology_walker_outcome_total" &&
			s.Labels["walker"] == ProtoLLDP && s.Labels["outcome"] == "edges" {
			found = true
			if s.Value <= 0 {
				t.Errorf("walker_outcome_total{walker=lldp,outcome=edges} = %v, want > 0", s.Value)
			}
			break
		}
	}
	if !found {
		t.Error("network_topology_walker_outcome_total{walker=lldp,outcome=edges} not found")
	}
}

func TestBuildHealth_BGPWalkerOutcome_BGPInProtocols(t *testing.T) {
	c := makeHealthConstruct()
	// bgp is in protocols — bgp_walker_outcome_total{outcome=edges} must appear.
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	if !hasName(series, "network_topology_bgp_walker_outcome_total") {
		t.Error("network_topology_bgp_walker_outcome_total not found (bgp in protocols)")
	}
	found := false
	for _, s := range series {
		if s.Name == "network_topology_bgp_walker_outcome_total" && s.Labels["outcome"] == "edges" {
			found = true
			break
		}
	}
	if !found {
		t.Error("network_topology_bgp_walker_outcome_total{outcome=edges} not found")
	}
}

func TestBuildHealth_BGPWalkerOutcome_BGPNotInProtocols(t *testing.T) {
	c := makeHealthConstruct()
	// Override to only lldp — no bgp → bgp_walker_outcome_total must be absent.
	c.rc.protocols = []string{ProtoLLDP}
	c.rc.protoSet = map[string]bool{ProtoLLDP: true}
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	if hasName(series, "network_topology_bgp_walker_outcome_total") {
		t.Error("network_topology_bgp_walker_outcome_total should be absent when bgp not in protocols")
	}
}

func TestBuildHealth_ModuleLastStatus(t *testing.T) {
	c := makeHealthConstruct()
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	modules := append(append([]string{}, c.rc.protocols...), "snmp")
	for _, mod := range modules {
		if _, ok := findLabeled(series, "network_topology_module_last_status", "module", mod); !ok {
			t.Errorf("module_last_status missing for module=%q", mod)
		}
	}
}

func TestBuildHealth_SNMPWalksTotal(t *testing.T) {
	c := makeHealthConstruct()
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	s, ok := findLabeled(series, "network_topology_snmp_walks_total", "status", "ok")
	if !ok {
		t.Fatal("snmp_walks_total{status=ok} not found")
	}
	if s.Value <= 0 {
		t.Errorf("snmp_walks_total{status=ok} = %v, want > 0", s.Value)
	}
}

func TestBuildHealth_CredentialTrialsTotal(t *testing.T) {
	c := makeHealthConstruct()
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	s, ok := findLabeled(series, "network_topology_credential_trials_total", "status", "ok")
	if !ok {
		t.Fatal("credential_trials_total{status=ok} not found")
	}
	if s.Value <= 0 {
		t.Errorf("credential_trials_total{status=ok} = %v, want > 0", s.Value)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Always-on: process self-observability
// ────────────────────────────────────────────────────────────────────────────

func TestBuildHealth_Goroutines(t *testing.T) {
	c := makeHealthConstruct()
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	s, ok := findLabeled(series, "network_topology_goroutines", "job", c.rc.job)
	if !ok {
		t.Fatal("network_topology_goroutines not found")
	}
	if s.Value <= 0 {
		t.Errorf("goroutines = %v, want > 0", s.Value)
	}
}

func TestBuildHealth_LastScrapeSamplesTotal(t *testing.T) {
	c := makeHealthConstruct()
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	s, ok := findLabeled(series, "network_topology_last_scrape_samples_total", "job", c.rc.job)
	if !ok {
		t.Fatal("network_topology_last_scrape_samples_total not found")
	}
	if s.Value <= 0 {
		t.Errorf("last_scrape_samples_total = %v, want > 0", s.Value)
	}
}

func TestBuildHealth_MetricsRenderDurationHistogram(t *testing.T) {
	c := makeHealthConstruct()
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	if !hasName(series, "network_topology_metrics_render_duration_seconds_bucket") {
		t.Error("metrics_render_duration_seconds_bucket not found")
	}
	if !hasName(series, "network_topology_metrics_render_duration_seconds_count") {
		t.Error("metrics_render_duration_seconds_count not found")
	}
}

func TestBuildHealth_LastScrapeDurationGauge(t *testing.T) {
	c := makeHealthConstruct()
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	if !hasName(series, "network_topology_last_scrape_duration_seconds") {
		t.Error("network_topology_last_scrape_duration_seconds not found")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Histogram contract: Observe-only, no double-counting
// ────────────────────────────────────────────────────────────────────────────

func TestBuildHealth_HistogramCount_EqualsObservations(t *testing.T) {
	c := makeHealthConstruct()
	// Call buildHealth once → expect exactly 1 observation per histogram.
	c.buildHealth(healthNow, shape.New("", nil))
	series := c.st.Collect(healthNow)

	// discovery_cycle_duration_seconds should have count=1 (one Observe per tick).
	s, ok := findLabeled(series, "network_topology_discovery_cycle_duration_seconds_count", "job", c.rc.job)
	if !ok {
		t.Fatal("discovery_cycle_duration_seconds_count not found")
	}
	if s.Value != 1 {
		t.Errorf("discovery_cycle_duration_seconds_count = %v, want 1 (1 Observe per tick)", s.Value)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Gating: SNMP session pool
// ────────────────────────────────────────────────────────────────────────────

func TestBuildHealth_SessionPool_Absent_WhenFalse(t *testing.T) {
	c := makeHealthConstruct()
	c.rc.sessionPool = false
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	if hasPrefixName(series, "network_topology_snmp_session_pool_") {
		t.Error("snmp_session_pool_* should be absent when sessionPool=false")
	}
}

func TestBuildHealth_SessionPool_Present_WhenTrue(t *testing.T) {
	c := makeHealthConstruct()
	c.rc.sessionPool = true
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	required := []string{
		"network_topology_snmp_session_pool_size",
		"network_topology_snmp_session_pool_hits_total",
		"network_topology_snmp_session_pool_misses_total",
	}
	for _, name := range required {
		if !hasName(series, name) {
			t.Errorf("missing %s when sessionPool=true", name)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Gating: federation hub
// ────────────────────────────────────────────────────────────────────────────

func TestBuildHealth_Hub_FederationSeriesPresent(t *testing.T) {
	c := makeHealthConstruct()
	c.rc.role = RoleHub
	c.rc.spokes = []string{"spoke-a", "spoke-b"}
	c.rc.oosCount = 2
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	// spoke_up for each spoke
	for _, sp := range c.rc.spokes {
		s, ok := findLabeled(series, "network_topology_federation_spoke_up", "spoke_id", sp)
		if !ok {
			t.Errorf("federation_spoke_up{spoke_id=%q} not found", sp)
			continue
		}
		if s.Value != 1 {
			t.Errorf("federation_spoke_up{spoke_id=%q} = %v, want 1", sp, s.Value)
		}
	}

	// spoke_last_push_timestamp per spoke
	for _, sp := range c.rc.spokes {
		s, ok := findLabeled(series, "network_topology_federation_spoke_last_push_timestamp_seconds", "spoke_id", sp)
		if !ok {
			t.Errorf("federation_spoke_last_push_timestamp_seconds{spoke_id=%q} not found", sp)
			continue
		}
		if s.Value != float64(healthNow.Unix()) {
			t.Errorf("federation_spoke_last_push_timestamp_seconds{spoke_id=%q} = %v, want %v",
				sp, s.Value, float64(healthNow.Unix()))
		}
	}

	// boundary_observation_info must appear (oosCount=2)
	if !hasName(series, "network_topology_boundary_observation_info") {
		t.Error("boundary_observation_info should appear when role=hub and oosCount>0")
	}
	bCount := countByName(series, "network_topology_boundary_observation_info")
	if bCount != 2 {
		t.Errorf("boundary_observation_info count = %d, want 2 (oosCount=2)", bCount)
	}
}

func TestBuildHealth_Hub_SpokeSeriesAbsent_WhenStandalone(t *testing.T) {
	c := makeHealthConstruct()
	c.rc.role = RoleStandalone
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	if hasName(series, "network_topology_federation_spoke_up") {
		t.Error("federation_spoke_up should be absent for standalone role")
	}
	if hasName(series, "network_topology_boundary_observation_info") {
		t.Error("boundary_observation_info should be absent for standalone role")
	}
}

func TestBuildHealth_Hub_BoundaryObsAbsent_WhenOOSZero(t *testing.T) {
	c := makeHealthConstruct()
	c.rc.role = RoleHub
	c.rc.spokes = []string{"spoke-a"}
	c.rc.oosCount = 0
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	if hasName(series, "network_topology_boundary_observation_info") {
		t.Error("boundary_observation_info should be absent when oosCount=0")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Gating: federation spoke
// ────────────────────────────────────────────────────────────────────────────

func TestBuildHealth_Spoke_SeriesPresent(t *testing.T) {
	c := makeHealthConstruct()
	c.rc.role = RoleSpoke
	c.rc.spokeID = "spoke-dc1"
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	required := []string{
		"network_topology_federation_spoke_push_failures_total",
		"network_topology_federation_spoke_push_last_success_unix",
		"network_topology_federation_spoke_push_queue_depth",
	}
	for _, name := range required {
		if !hasName(series, name) {
			t.Errorf("missing %s when role=spoke", name)
		}
	}

	// Hub series must be absent.
	if hasName(series, "network_topology_federation_spoke_up") {
		t.Error("federation_spoke_up should be absent for spoke role")
	}
}

func TestBuildHealth_Spoke_LastSuccessTimestamp(t *testing.T) {
	c := makeHealthConstruct()
	c.rc.role = RoleSpoke
	c.rc.spokeID = "spoke-dc1"
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	s, ok := findLabeled(series, "network_topology_federation_spoke_push_last_success_unix", "job", c.rc.job)
	if !ok {
		t.Fatal("federation_spoke_push_last_success_unix not found")
	}
	if s.Value != float64(healthNow.Unix()) {
		t.Errorf("federation_spoke_push_last_success_unix = %v, want %v", s.Value, float64(healthNow.Unix()))
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Gating: OTLP output
// ────────────────────────────────────────────────────────────────────────────

func TestBuildHealth_OTLPPush_Absent_WhenFalse(t *testing.T) {
	c := makeHealthConstruct()
	c.rc.otlpOutput = false
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	if hasName(series, "network_topology_otlp_push_total") {
		t.Error("otlp_push_total should be absent when otlpOutput=false")
	}
}

func TestBuildHealth_OTLPPush_Present_WhenTrue(t *testing.T) {
	c := makeHealthConstruct()
	c.rc.otlpOutput = true
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	s, ok := findLabeled(series, "network_topology_otlp_push_total", "status", "ok")
	if !ok {
		t.Fatal("otlp_push_total{status=ok} not found when otlpOutput=true")
	}
	if s.Value <= 0 {
		t.Errorf("otlp_push_total{status=ok} = %v, want > 0", s.Value)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Snapshot queue depth and drops always zero
// ────────────────────────────────────────────────────────────────────────────

func TestBuildHealth_SnapshotQueueDepthZero(t *testing.T) {
	c := makeHealthConstruct()
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	s, ok := findLabeled(series, "network_topology_snapshot_queue_depth", "job", c.rc.job)
	if !ok {
		t.Fatal("snapshot_queue_depth not found")
	}
	if s.Value != 0 {
		t.Errorf("snapshot_queue_depth = %v, want 0", s.Value)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Nil engine: must not panic (returns 1.0 for seriesVar)
// ────────────────────────────────────────────────────────────────────────────

func TestBuildHealth_NilEngine_NoPanic(t *testing.T) {
	c := makeHealthConstruct()
	// Must not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("buildHealth panicked with nil engine: %v", r)
		}
	}()
	runHealthOnce(c, healthNow, nil)
}

// ────────────────────────────────────────────────────────────────────────────
// Fault injection helpers
// ────────────────────────────────────────────────────────────────────────────

// shapeWithLiveFault returns a shape.Engine whose Live hook returns an active,
// full-intensity LiveFailure scoped to the given instance for the named mode.
// Unscoped if scope == "".
func shapeWithLiveFault(mode, scope string, intensity float64) *shape.Engine {
	eng := shape.New("", nil)
	eng.Live = func(m string) []shape.LiveFailure {
		if m != mode {
			return nil
		}
		return []shape.LiveFailure{{Enabled: true, Intensity: intensity, Scope: scope}}
	}
	return eng
}

// makeHubConstruct returns a Construct in hub role with two spokes.
func makeHubConstruct() *Construct {
	c := makeHealthConstruct()
	c.rc.role = RoleHub
	c.rc.spokes = []string{"spoke-a", "spoke-b"}
	c.rc.oosCount = 0
	return c
}

// makeSpokeConstruct returns a Construct in spoke role.
func makeSpokeConstruct() *Construct {
	c := makeHealthConstruct()
	c.rc.role = RoleSpoke
	c.rc.spokeID = "spoke-dc1"
	return c
}

// ────────────────────────────────────────────────────────────────────────────
// TestFault_Inert_NoFaultOnlySeries — with no active fault the fault-only
// families must be ABSENT (never emitted at all), not value-0.
// ────────────────────────────────────────────────────────────────────────────

func TestFault_Inert_NoFaultOnlySeries(t *testing.T) {
	c := makeHealthConstruct()
	series := runHealthOnce(c, healthNow, shape.New("", nil))

	faultOnly := []string{
		"network_topology_discovery_decode_issues_total",
		"network_topology_discovery_degraded_total",
		"network_topology_discovery_hard_fail_total",
	}
	for _, m := range faultOnly {
		if hasName(series, m) {
			t.Errorf("%s must be absent with no fault active (FAULT-ONLY invariant)", m)
		}
	}

	// walker error/drift outcomes must also be absent
	for _, s := range series {
		if s.Name == "network_topology_walker_outcome_total" {
			if s.Labels["outcome"] == "error" || s.Labels["outcome"] == "walker_drift" {
				t.Errorf("walker_outcome_total{outcome=%q} must be absent with no fault active", s.Labels["outcome"])
			}
		}
	}

	// No failed credential trials, no timeout snmp_walks
	for _, s := range series {
		if s.Name == "network_topology_credential_trials_total" && s.Labels["status"] == "failed" {
			t.Error("credential_trials_total{status=failed} must be absent with no fault active")
		}
		if s.Name == "network_topology_snmp_walks_total" && s.Labels["status"] == "timeout" {
			t.Error("snmp_walks_total{status=timeout} must be absent with no fault active")
		}
	}

	// No failed discovery devices
	for _, s := range series {
		if s.Name == "network_topology_discovery_devices_total" && s.Labels["status"] == "failed" {
			t.Error("discovery_devices_total{status=failed} must be absent with no fault active")
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// TestFault_DiscoverySlow_RaisesCycleAndStale
// ────────────────────────────────────────────────────────────────────────────

func TestFault_DiscoverySlow_RaisesCycleAndStale(t *testing.T) {
	instance := "netobs-dc1:9100"
	c := makeHealthConstruct()
	c.rc.instance = instance
	eng := shapeWithLiveFault("nettopo_discovery_slow", instance, 1.0)

	// First tick sets bootTime — call buildData to anchor cold-start.
	c.buildData(healthNow, eng)
	// Second tick (non-cold-start) is where we check health effects.
	t2 := healthNow.Add(60 * time.Second)
	series := runHealthOnce(c, t2, eng)

	// graph_stale must be 1 under discovery_slow
	s, ok := findLabeled(series, "network_topology_graph_stale", "job", c.rc.job)
	if !ok {
		t.Fatal("network_topology_graph_stale not found")
	}
	if s.Value != 1 {
		t.Errorf("graph_stale want 1 under nettopo_discovery_slow, got %v", s.Value)
	}

	// cycle_duration_seconds_sum should be markedly higher than baseline (FailFactor×10)
	// We just need it to be > 0 and the histogram present.
	if !hasName(series, "network_topology_discovery_cycle_duration_seconds_count") {
		t.Error("discovery_cycle_duration_seconds not emitted under discovery_slow")
	}

	// cycle_budget_skips_total must be present (incremented under slow mode)
	if !hasName(series, "network_topology_cycle_budget_skips_total") {
		t.Error("cycle_budget_skips_total not found under discovery_slow")
	}
	skip, ok := findLabeled(series, "network_topology_cycle_budget_skips_total", "job", c.rc.job)
	if !ok {
		t.Error("cycle_budget_skips_total not found")
	} else if skip.Value <= 0 {
		t.Errorf("cycle_budget_skips_total want >0 under discovery_slow, got %v", skip.Value)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// TestFault_WalkerDegraded_EmitsFaultOnlyFamilies
// ────────────────────────────────────────────────────────────────────────────

func TestFault_WalkerDegraded_EmitsFaultOnlyFamilies(t *testing.T) {
	instance := "netobs-dc1:9100"
	c := makeHealthConstruct()
	c.rc.instance = instance
	eng := shapeWithLiveFault("nettopo_walker_degraded", instance, 1.0)
	series := runHealthOnce(c, healthNow, eng)

	// Fault-only families must now be present
	for _, m := range []string{
		"network_topology_discovery_decode_issues_total",
		"network_topology_discovery_degraded_total",
		"network_topology_discovery_hard_fail_total",
	} {
		if !hasName(series, m) {
			t.Errorf("%s must be emitted under nettopo_walker_degraded", m)
		}
	}

	// walker_outcome_total must have error and/or walker_drift outcomes
	foundFaultOutcome := false
	for _, s := range series {
		if s.Name == "network_topology_walker_outcome_total" {
			if s.Labels["outcome"] == "error" || s.Labels["outcome"] == "walker_drift" {
				foundFaultOutcome = true
				if s.Value <= 0 {
					t.Errorf("walker_outcome_total{outcome=%q} want >0, got %v", s.Labels["outcome"], s.Value)
				}
			}
		}
	}
	if !foundFaultOutcome {
		t.Error("walker_outcome_total{outcome=error|walker_drift} not found under nettopo_walker_degraded")
	}

	// module_last_status must be ≥1 for at least one module
	foundDegraded := false
	for _, s := range series {
		if s.Name == "network_topology_module_last_status" && s.Value >= 1 {
			foundDegraded = true
			break
		}
	}
	if !foundDegraded {
		t.Error("module_last_status must be ≥1 for some module under nettopo_walker_degraded")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// TestFault_AuthFailures_EmitsFailedCredentials
// ────────────────────────────────────────────────────────────────────────────

func TestFault_AuthFailures_EmitsFailedCredentials(t *testing.T) {
	instance := "netobs-dc1:9100"
	c := makeHealthConstruct()
	c.rc.instance = instance
	eng := shapeWithLiveFault("nettopo_auth_failures", instance, 1.0)
	series := runHealthOnce(c, healthNow, eng)

	// credential_trials_total{status=failed} must appear
	s, ok := findLabeled(series, "network_topology_credential_trials_total", "status", "failed")
	if !ok {
		t.Error("credential_trials_total{status=failed} not found under nettopo_auth_failures")
	} else if s.Value <= 0 {
		t.Errorf("credential_trials_total{status=failed} want >0, got %v", s.Value)
	}

	// snmp_walks_total{status=timeout} must appear
	timeout, ok := findLabeled(series, "network_topology_snmp_walks_total", "status", "timeout")
	if !ok {
		t.Error("snmp_walks_total{status=timeout} not found under nettopo_auth_failures")
	} else if timeout.Value <= 0 {
		t.Errorf("snmp_walks_total{status=timeout} want >0, got %v", timeout.Value)
	}

	// discovery_devices_total{status=failed,reason=auth_failed} must appear
	authFailed := false
	for _, s := range series {
		if s.Name == "network_topology_discovery_devices_total" &&
			s.Labels["status"] == "failed" && s.Labels["reason"] == "auth_failed" {
			authFailed = true
			if s.Value <= 0 {
				t.Errorf("discovery_devices_total{status=failed,reason=auth_failed} want >0, got %v", s.Value)
			}
			break
		}
	}
	if !authFailed {
		t.Error("discovery_devices_total{status=failed,reason=auth_failed} not found under nettopo_auth_failures")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// TestFault_DevicesUnreachable_EmitsFailures
// ────────────────────────────────────────────────────────────────────────────

func TestFault_DevicesUnreachable_EmitsFailures(t *testing.T) {
	instance := "netobs-dc1:9100"
	c := makeHealthConstruct()
	c.rc.instance = instance
	eng := shapeWithLiveFault("nettopo_devices_unreachable", instance, 1.0)
	series := runHealthOnce(c, healthNow, eng)

	// discovery_devices_total{status=failed,reason=unreachable} must appear
	unreachable := false
	for _, s := range series {
		if s.Name == "network_topology_discovery_devices_total" &&
			s.Labels["status"] == "failed" && s.Labels["reason"] == "unreachable" {
			unreachable = true
			if s.Value <= 0 {
				t.Errorf("discovery_devices_total{status=failed,reason=unreachable} want >0, got %v", s.Value)
			}
			break
		}
	}
	if !unreachable {
		t.Error("discovery_devices_total{status=failed,reason=unreachable} not found under nettopo_devices_unreachable")
	}

	// snmp_walks_total{status=timeout} must appear
	if _, ok := findLabeled(series, "network_topology_snmp_walks_total", "status", "timeout"); !ok {
		t.Error("snmp_walks_total{status=timeout} not found under nettopo_devices_unreachable")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// TestFault_DevicesUnreachable_DropsDeviceSeries (device-side in nettopo.go)
// ────────────────────────────────────────────────────────────────────────────

func TestFault_DevicesUnreachable_DropsDeviceSeries(t *testing.T) {
	instance := "netobs-dc1:9100"
	// Build a multi-device construct to exercise subset omission.
	c := &Construct{
		rc: resolvedConfig{
			job:       "integrations/network-topology-exporter",
			instance:  instance,
			role:      RoleStandalone,
			protocols: []string{ProtoLLDP},
			protoSet:  map[string]bool{ProtoLLDP: true},
		},
		graph: Graph{
			Devices: []Device{
				{ID: "spine-01", Vendor: "arista", OSVersion: "4.36.0F", Site: "dc1",
					UptimeBaseSecs: UptimeBaseSeconds("spine-01", "")},
				{ID: "leaf-01", Vendor: "arista", OSVersion: "4.36.0F", Site: "dc1",
					UptimeBaseSecs: UptimeBaseSeconds("leaf-01", "")},
				{ID: "leaf-02", Vendor: "cisco", OSVersion: "17.12.1", Site: "dc1",
					UptimeBaseSecs: UptimeBaseSeconds("leaf-02", "")},
			},
			Edges: []Edge{},
		},
		st: state.NewState(),
	}
	eng := shapeWithLiveFault("nettopo_devices_unreachable", instance, 1.0)

	// First tick — cold-start + unreachable fault active.
	c.buildData(healthNow, eng)
	series := c.st.Collect(healthNow)

	// At intensity=1 all devices should be in the unreachable subset → no device_info.
	deviceInfoCount := len(findSeries(series, "network_topology_device_info"))
	if deviceInfoCount == len(c.graph.Devices) {
		t.Errorf("all %d device_info series present under full unreachable fault; expected some to be absent", len(c.graph.Devices))
	}
}

// ────────────────────────────────────────────────────────────────────────────
// TestFault_SpokeDown_HubMarksSpoke
// ────────────────────────────────────────────────────────────────────────────

func TestFault_SpokeDown_HubMarksSpoke(t *testing.T) {
	instance := "netobs-hub:9100"
	c := makeHubConstruct()
	c.rc.instance = instance
	eng := shapeWithLiveFault("nettopo_spoke_down", instance, 1.0)
	series := runHealthOnce(c, healthNow, eng)

	// At least one spoke must have spoke_up=0
	spokeDownFound := false
	for _, s := range series {
		if s.Name == "network_topology_federation_spoke_up" && s.Value == 0 {
			spokeDownFound = true
			break
		}
	}
	if !spokeDownFound {
		t.Error("federation_spoke_up=0 not found under nettopo_spoke_down on hub role")
	}

	// graph_updates_rejected_total must be present and > baseline
	// (baseline is 0-seeded, under fault it should increment)
	foundRejected := false
	for _, s := range series {
		if s.Name == "network_topology_graph_updates_rejected_total" && s.Value > 0 {
			foundRejected = true
			break
		}
	}
	if !foundRejected {
		t.Error("graph_updates_rejected_total must be >0 under nettopo_spoke_down on hub")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// TestFault_SpokeDown_SpokeSideEmitsFailures
// ────────────────────────────────────────────────────────────────────────────

func TestFault_SpokeDown_SpokeSideEmitsFailures(t *testing.T) {
	instance := "test-spoke-exporter:9100"
	c := makeSpokeConstruct()
	c.rc.instance = instance
	eng := shapeWithLiveFault("nettopo_spoke_down", instance, 1.0)
	series := runHealthOnce(c, healthNow, eng)

	// spoke role: federation_spoke_push_failures_total must increment (>0)
	s, ok := findLabeled(series, "network_topology_federation_spoke_push_failures_total", "job", c.rc.job)
	if !ok {
		t.Error("federation_spoke_push_failures_total not found under nettopo_spoke_down on spoke role")
	} else if s.Value <= 0 {
		t.Errorf("federation_spoke_push_failures_total want >0, got %v under nettopo_spoke_down", s.Value)
	}
}
