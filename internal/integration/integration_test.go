// SPDX-License-Identifier: AGPL-3.0-only

// Package integration is the definition-of-done gate (ARCHITECTURE §9, brief §10):
// the real blueprints load against the real catalog, run a full cycle, and the
// emitted estate is coherent and cross-joinable.
package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
)

// The full estate is built EXACTLY ONCE and the resulting read-only captures are shared by every
// test in this package. The estate is deterministic (fixed clock + seeded RNG, I12), so a single
// build is byte-equivalent to rebuilding it per-test — and one build already costs ~2.8 GB, so the
// old per-test rebuild (8 tests) stacked to a ~15 GB peak under GC lag and OOM-killed the CI runner
// (SIGTERM/143 in `go test ./...`). The capture readers (All/Names/Find) never mutate the recorded
// batches, so sharing them across sequential tests is safe.
var (
	estateOnce sync.Once
	estateMC   *coretest.MetricCapture
	estateLC   *coretest.LogCapture
	estateTC   *coretest.TraceCapture
	estateErr  error
)

func runAll(t *testing.T) (*coretest.MetricCapture, *coretest.LogCapture, *coretest.TraceCapture) {
	t.Helper()
	estateOnce.Do(func() {
		estateMC, estateLC, estateTC, estateErr = buildEstate()
	})
	if estateErr != nil {
		t.Fatalf("build estate: %v", estateErr)
	}
	return estateMC, estateLC, estateTC
}

// buildEstate loads every committed blueprint against the real catalog, opts each into span-metrics
// emission, and runs a few master ticks plus one full cycle. Returns an error (rather than failing a
// *testing.T) so it can run inside sync.Once and surface the failure to whichever test triggered it.
func buildEstate() (*coretest.MetricCapture, *coretest.LogCapture, *coretest.TraceCapture, error) {
	mc, lc, tc := &coretest.MetricCapture{}, &coretest.LogCapture{}, &coretest.TraceCapture{}
	reg := runner.Catalog()
	paths, err := filepath.Glob(filepath.Join("..", "..", "blueprints", "*.yaml"))
	if err != nil || len(paths) == 0 {
		return nil, nil, nil, fmt.Errorf("blueprints: %v (%d)", err, len(paths))
	}
	var resolved []*blueprint.Resolved
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, nil, err
		}
		res, err := blueprint.Load(data, reg)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %v", p, err)
		}
		resolved = append(resolved, res)
	}
	if err := blueprint.ValidateSet(resolved); err != nil {
		return nil, nil, nil, fmt.Errorf("ValidateSet: %v", err)
	}
	r := runner.New(runner.Sinks{Metrics: mc, Logs: lc, Traces: tc}, runner.Catalog(), runner.Options{})
	// Span-metrics emission is now a per-blueprint opt-in (default OFF, deferring to
	// metrics-generator/beyla). The full-estate inventory gate asserts the synthkit-emitted
	// spanmetrics/service-graph families, so opt every loaded blueprint in.
	cs := control.DefaultState()
	for _, res := range resolved {
		if err := r.AddBlueprint(res); err != nil {
			return nil, nil, nil, fmt.Errorf("AddBlueprint %q: %v", res.Name, err)
		}
		cs.SpanMetricsBlueprints = append(cs.SpanMetricsBlueprints, res.Name)
	}
	r.ApplyControl(cs)
	// Business hours on a weekday so traffic volume is non-trivial.
	now := time.Date(2026, 6, 17, 11, 0, 0, 0, time.UTC)
	// A few master ticks so the ledger mints a narrative sample, then one full cycle.
	ctx := context.Background()
	for i := range 6 {
		if err := r.MasterTick(ctx, now.Add(time.Duration(i*5)*time.Second)); err != nil {
			return nil, nil, nil, fmt.Errorf("MasterTick: %v", err)
		}
	}
	if err := r.RunOnce(ctx, now.Add(30*time.Second)); err != nil {
		return nil, nil, nil, fmt.Errorf("RunOnce: %v", err)
	}
	return mc, lc, tc, nil
}

func TestEstateEmitsAllDeclaredFamilies(t *testing.T) {
	mc, lc, tc := runAll(t)
	names := strings.Join(mc.Names(), "\n")
	for _, family := range []string{
		"aws_ec2_cpuutilization_average",         // EC2 CW
		"aws_natgateway_",                        // NAT GW on the VPC
		"aws_applicationelb_request_count_sum",   // ALB
		"aws_rds_cpuutilization_average",         // RDS
		"aws_elasticache_cache_hits_sum",         // ElastiCache
		"kube_pod_status_phase",                  // KSM
		"node_cpu_seconds_total",                 // node-exporter
		"container_cpu_usage_seconds_total",      // cAdvisor
		"kubelet_running_pods",                   // kubelet
		"alloy_build_info",                       // conformance + fleet
		"coredns_dns_requests_total",             // add-on
		"awscni_eni_allocated",                   // add-on
		"aws_ebs_csi_",                           // add-on
		"certmanager_certificate_ready_status",   // add-on (k8s-full-stack cluster)
		"cluster_autoscaler_nodes_count",         // add-on
		"kube_ingress_info",                      // ksm_ingress (acme-ai-eval declares it)
		"database_observability_connection_info", // dbo11y
		"pg_stat_statements_calls_total",         // dbo11y postgres
		"probe_success",                          // synthetic monitoring
		"cloudflare_zone_requests_total",         // cloudflare feature
		"traces_spanmetrics_calls_total",         // web_service RED
		"traces_service_graph_request_total",     // service graph
		"synthkit_content_leak_test",             // content sentinel (alloy_health addon)
	} {
		if !strings.Contains(names, family) {
			t.Errorf("expected family %q missing from emitted inventory", family)
		}
	}
	if len(lc.Streams) == 0 {
		t.Error("no log streams emitted")
	}
	if len(tc.Resources) == 0 {
		t.Error("no trace resources emitted")
	}
}

// TestNoDuplicateSeries asserts no two emitted metric series share an identical name + full
// label set in one push. Duplicates (Mimir rejects/corrupts them — out-of-order/duplicate samples)
// arise when a fanned construct (for_each_env) emits a family that does NOT carry the `env` label:
// every per-env instance then pushes the byte-identical series. This is the durable guard for the
// fan-out invariant "an env-scoped construct stamps env on EVERY series" (ARCHITECTURE §3).
func TestNoDuplicateSeries(t *testing.T) {
	mc, _, _ := runAll(t)
	seen := map[string]int{}
	for _, s := range mc.All() {
		keys := make([]string, 0, len(s.Labels))
		for k := range s.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString(s.Name)
		for _, k := range keys {
			b.WriteByte('|')
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteString(s.Labels[k])
		}
		seen[b.String()]++
	}
	dups := 0
	var examples []string
	for sig, n := range seen {
		if n > 1 {
			dups += n - 1
			if len(examples) < 15 {
				examples = append(examples, fmt.Sprintf("×%d %s", n, sig))
			}
		}
	}
	if dups > 0 {
		sort.Strings(examples)
		t.Fatalf("%d duplicate series (identical name+labels in one push — Mimir rejects/corrupts). Examples:\n%s",
			dups, strings.Join(examples, "\n"))
	}
}

func TestEC2NodeIdentityCorrelates(t *testing.T) {
	mc, _, _ := runAll(t)
	ec2IDs := map[string]bool{}
	for _, s := range mc.Find("aws_ec2_cpuutilization_average") {
		if id := s.Labels["dimension_InstanceId"]; id != "" {
			ec2IDs[id] = true
		}
	}
	if len(ec2IDs) == 0 {
		t.Fatal("no EC2 instance ids emitted")
	}
	// The same instance IDs must appear in the k8s substrate's node provider IDs.
	matched := 0
	for _, s := range mc.Find("kube_node_info") {
		pid := s.Labels["provider_id"]
		for id := range ec2IDs {
			if strings.HasSuffix(pid, id) {
				matched++
				break
			}
		}
	}
	if matched == 0 {
		t.Fatalf("no kube_node_info provider_id matches any aws_ec2 dimension_InstanceId (EC2↔EKS correlation broken); ec2 ids: %v", ec2IDs)
	}
}

func TestDBIdentityCorrelatesAcrossCloudAndDbo11y(t *testing.T) {
	mc, _, _ := runAll(t)
	rdsNames := map[string]bool{}
	for _, s := range mc.Find("aws_rds_cpuutilization_average") {
		rdsNames[s.Labels["dimension_DBInstanceIdentifier"]] = true
	}
	dboNames := map[string]bool{}
	for _, s := range mc.Find("database_observability_connection_info") {
		dboNames[s.Labels["db_instance_identifier"]] = true
	}
	if len(dboNames) == 0 || len(rdsNames) == 0 {
		t.Fatalf("rds=%v dbo=%v", rdsNames, dboNames)
	}
	for n := range dboNames {
		if !rdsNames[n] {
			t.Errorf("dbo11y instance %q has no matching aws_rds_* identity (cloud↔dbo11y join broken)", n)
		}
	}
}

func TestScopingBlueprintLabel(t *testing.T) {
	mc, _, _ := runAll(t)
	// Blueprint-scoped: every aws_ec2_* series carries the selector.
	for _, s := range mc.Find("aws_ec2_cpuutilization_average") {
		if s.Labels["blueprint"] == "" {
			t.Fatalf("aws_ec2 series missing blueprint label: %v", s.Labels)
		}
	}
	// Substrate: kube_* and dbo11y NEVER carry it (I21).
	for _, name := range []string{"kube_pod_status_phase", "database_observability_connection_info", "node_cpu_seconds_total"} {
		for _, s := range mc.Find(name) {
			if _, has := s.Labels["blueprint"]; has {
				t.Fatalf("substrate series %s carries a blueprint label: %v", name, s.Labels)
			}
		}
	}
}

func TestTwoBlueprintsSeparateCleanly(t *testing.T) {
	mc, _, _ := runAll(t)
	clusters := map[string]bool{}
	for _, s := range mc.Find("kube_node_info") {
		clusters[s.Labels["cluster"]] = true
	}
	// Many blueprints declare distinct EKS clusters (the for_each_env scenarios fan one cluster
	// per env, plus the standalone k8s-* showcases) — they MUST all separate by `cluster` with no
	// collisions. The full estate currently yields 30; assert a conservative floor.
	if len(clusters) < 20 {
		t.Fatalf("expected ≥20 distinct clusters across blueprints, got %d: %v", len(clusters), clusters)
	}
	// The EC2 CloudWatch lane is blueprint-scoped: each EC2-emitting blueprint stamps its own
	// selector. Several blueprints emit it (acme-ai-platform, acme-ai-platform-eval, aws-cw-estate,
	// acme-ai-eval, k8sfull, profiling-demo) — assert multiple distinct selectors plus the
	// stable named showcases.
	bps := map[string]bool{}
	for _, s := range mc.Find("aws_ec2_cpuutilization_average") {
		bps[s.Labels["blueprint"]] = true
	}
	if len(bps) < 3 {
		t.Fatalf("expected ≥3 distinct EC2-emitting blueprint selectors, got %v", bps)
	}
	if !bps["k8sfull"] || !bps["acme-ai-platform"] || !bps["aws-cw-estate"] {
		t.Fatalf("blueprint selectors on EC2 series: %v (want k8sfull AND acme-ai-platform AND aws-cw-estate)", bps)
	}
}

func TestGoldenThreadCorrelation(t *testing.T) {
	mc, lc, tc := runAll(t)
	_ = mc
	// Collect trace IDs from emitted spans.
	traceIDs := map[string]bool{}
	for _, res := range tc.Resources {
		for _, sp := range res.Spans {
			traceIDs[sp.TraceID] = true
		}
	}
	if len(traceIDs) == 0 {
		t.Fatal("no spans emitted")
	}
	// App log structured metadata must reference the SAME trace IDs.
	matched := false
	for _, st := range lc.Streams {
		if st.Labels["source"] != "app" {
			continue
		}
		for _, ln := range st.Lines {
			if tid := ln.Meta["trace_id"]; tid != "" && traceIDs[tid] {
				matched = true
			}
		}
	}
	if !matched {
		t.Fatal("no app log line's trace_id matches an emitted span trace ID (golden thread broken)")
	}
}

// TestAppServiceGraphEmitted is the `app`-workload end-to-end gate (Spec 5 migration): the three
// scenario blueprints now declare their core flow as an `app` service graph, so the full estate must
// emit per-service families keyed by the app node identity, synthesize the service graph between app
// nodes (EmitSpanMetrics is opted in for every blueprint by runAll), and keep the golden thread
// across app nodes. Before the migration NO blueprint exercised `app` e2e (the review's flagged gap).
func TestAppServiceGraphEmitted(t *testing.T) {
	mc, lc, tc := runAll(t)

	// 1. Per-service SCRAPE family (scraped_http_server) carries the app node identity (service=node
	//    name) — the node-identity auto-stamp (B3), distinct from the metrics-generator spanmetrics.
	backendScrape := false
	for _, s := range mc.Find("http_server_request_duration_seconds_count") {
		if s.Labels["service"] == "acme-backend" {
			backendScrape = true
		}
	}
	if !backendScrape {
		t.Error("no http_server_request_duration_seconds for the app node service=acme-backend (scraped_http_server profile not emitting per-service)")
	}

	// 2. gen_ai_client metrics emit (composed onto an app node), keeping the AI families alive post-migration.
	if len(mc.Find("gen_ai_client_token_usage_bucket")) == 0 {
		t.Error("no gen_ai_client_token_usage emitted by the app estate (gen_ai_client profile not composed)")
	}

	// 3. Frontend node (rum_faro profile) emits a browser CLIENT span in the trace resources.
	//    2026-06-16: web-vital Prometheus gauges REMOVED (SK-56/57/58 resolved — vitals are
	//    Loki measurement log events via the Faro collector, not gauge series). The browser
	//    CLIENT span is the new gate: verify that acme-frontend appears as a trace resource.
	rumFrontendSpan := false
	for _, res := range tc.Resources {
		if svc, _ := res.Attrs["service.name"].(string); svc == "acme-frontend" {
			if len(res.Spans) > 0 {
				rumFrontendSpan = true
				break
			}
		}
	}
	if !rumFrontendSpan {
		t.Error("no spans emitted for service=acme-frontend (rum_faro frontend node not emitting browser CLIENT spans)")
	}

	// 4. The app's OWN service graph is synthesized between app nodes (EmitSpanMetrics opted in):
	//    a frontend→backend edge with the app node names on the client/server dimensions.
	feToBackend := false
	for _, s := range mc.Find("traces_service_graph_request_total") {
		if s.Labels["client"] == "acme-frontend" && s.Labels["server"] == "acme-backend" {
			feToBackend = true
		}
	}
	if !feToBackend {
		t.Error("no traces_service_graph edge acme-frontend→acme-backend (app graph not synthesized)")
	}

	// 5. App golden thread: a source=app log from an app node carries a trace_id that matches a span
	//    emitted by that same app service resource (continuous correlation through the app graph).
	appTraceIDs := map[string]bool{}
	for _, res := range tc.Resources {
		if svc, _ := res.Attrs["service.name"].(string); svc == "acme-backend" {
			for _, sp := range res.Spans {
				appTraceIDs[sp.TraceID] = true
			}
		}
	}
	matched := false
	for _, st := range lc.Streams {
		if st.Labels["source"] != "app" || st.Labels["service_name"] != "acme-backend" {
			continue
		}
		for _, ln := range st.Lines {
			if tid := ln.Meta["trace_id"]; tid != "" && appTraceIDs[tid] {
				matched = true
			}
		}
	}
	if !matched {
		t.Error("no acme-backend app log trace_id matches an acme-backend span (app golden thread broken)")
	}
}

func TestNoHighCardKeysInStreamLabels(t *testing.T) {
	_, lc, _ := runAll(t)
	for _, st := range lc.Streams {
		for _, k := range []string{"trace_id", "span_id", "request_id", "session_id", "correlation_id"} {
			if _, bad := st.Labels[k]; bad {
				t.Fatalf("high-card key %q in stream labels: %v", k, st.Labels)
			}
		}
	}
}
