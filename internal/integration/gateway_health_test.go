// SPDX-License-Identifier: AGPL-3.0-only

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

const gatewayA = "10.90.1.10:12345"
const gatewayB = "10.90.1.11:12345"

var gatewayClock = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func gatewayFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "e2e", "fixtures", "ai-factory-gateway.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func gatewayRunner(t *testing.T, data []byte) (*runner.Runner, *coretest.MetricCapture) {
	t.Helper()
	reg := runner.Catalog()
	bp, err := blueprint.Load(data, reg)
	if err != nil {
		t.Fatal(err)
	}
	mc := &coretest.MetricCapture{}
	r := runner.New(runner.Sinks{Metrics: mc, Logs: &coretest.LogCapture{}}, reg, runner.Options{})
	if err := r.AddBlueprint(bp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r.DrainQueues(ctx)
	})
	return r, mc
}
func gatewayTick(t *testing.T, r *runner.Runner, mc *coretest.MetricCapture, now time.Time, mode, scope string) []promrw.Series {
	t.Helper()
	cs := control.DefaultState()
	if mode != "" {
		cs.Failures[mode] = control.FailureSetting{Enabled: true, Intensity: 1, Scope: scope}
	}
	r.ApplyControl(cs)
	start := len(mc.All())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := r.RunOnce(ctx, now); err != nil {
		t.Fatal(err)
	}
	var out []promrw.Series
	for _, s := range mc.All()[start:] {
		if s.Labels["instance"] == gatewayA || s.Labels["instance"] == gatewayB {
			out = append(out, s)
		}
	}
	return out
}
func gatewayFind(t *testing.T, ss []promrw.Series, name, member string, extra map[string]string) (float64, bool) {
	t.Helper()
	found := false
	v := 0.0
	for _, s := range ss {
		if s.Name != name || s.Labels["instance"] != member {
			continue
		}
		match := true
		for k, v := range extra {
			if s.Labels[k] != v {
				match = false
			}
		}
		if !match {
			continue
		}
		if found {
			t.Fatalf("duplicate %s %s %v", name, member, extra)
		}
		found = true
		v = s.Value
	}
	return v, found
}
func gatewayValue(t *testing.T, ss []promrw.Series, name, member string, extra map[string]string) float64 {
	t.Helper()
	v, ok := gatewayFind(t, ss, name, member, extra)
	if !ok {
		t.Fatalf("missing %s for %s %v", name, member, extra)
	}
	return v
}
func gatewaySeriesCount(r *runner.Runner) int64 {
	for _, bp := range r.Inventory().Blueprints {
		for _, c := range bp.Constructs {
			if c.Kind == "alloy_health" {
				return c.DistinctSeries
			}
		}
	}
	return 0
}

// Catches member-global silence, frozen/zero counters, lost health, and a sink-only
// filter applied after inventory instead of actual source-local publication omission.
func TestGatewaySourceGapPublicBoundary(t *testing.T) {
	r, mc := gatewayRunner(t, gatewayFixture(t))
	before := gatewayTick(t, r, mc, gatewayClock, "", "")
	during := gatewayTick(t, r, mc, gatewayClock.Add(time.Minute), "gateway_source_gap", "gateway-fixture-cluster")
	after := gatewayTick(t, r, mc, gatewayClock.Add(2*time.Minute), "", "")
	for _, name := range []string{"remotecfg_last_load_successful", "remotecfg_load_attempts_total", "remotecfg_load_failures_total"} {
		gatewayValue(t, before, name, gatewayA, nil)
		gatewayValue(t, before, name, gatewayB, nil)
		if _, ok := gatewayFind(t, during, name, gatewayA, nil); ok {
			t.Fatalf("victim data must be absent: %s", name)
		}
		gatewayValue(t, during, name, gatewayB, nil)
		gatewayValue(t, after, name, gatewayA, nil)
	}
	for _, ss := range [][]promrw.Series{before, during, after} {
		if gatewayValue(t, ss, "up", gatewayA, nil) != 1 || gatewayValue(t, ss, "alloy_component_controller_running_components", gatewayA, map[string]string{"health_type": "healthy"}) <= 0 {
			t.Fatal("gap must retain independent healthy process/component witnesses")
		}
		gatewayValue(t, ss, "cluster_node_peers", gatewayA, map[string]string{"state": "participant"})
		gatewayValue(t, ss, "otelcol_exporter_queue_capacity", gatewayA, map[string]string{"data_type": "metrics"})
	}
	if gatewayValue(t, after, "remotecfg_load_attempts_total", gatewayA, nil)-gatewayValue(t, before, "remotecfg_load_attempts_total", gatewayA, nil) != 2 {
		t.Fatal("source counter must resume cumulative polling, not freeze")
	}
	// New runner begins during gap: public inventory must count actual omission.
	g, gm := gatewayRunner(t, gatewayFixture(t))
	gapOnly := gatewayTick(t, g, gm, gatewayClock, "gateway_source_gap", "gateway-fixture-cluster")
	if gatewaySeriesCount(r)-gatewaySeriesCount(g) != 3 || len(before)-len(gapOnly) != 3 {
		t.Fatalf("public inventory/source writer mismatch: normal %d gap %d", gatewaySeriesCount(r), gatewaySeriesCount(g))
	}
	wrong, wm := gatewayRunner(t, gatewayFixture(t))
	ss := gatewayTick(t, wrong, wm, gatewayClock, "gateway_source_gap", "unrelated-cluster")
	gatewayValue(t, ss, "remotecfg_load_attempts_total", gatewayA, nil)
}

// Catches queue loss disguised as retries, overflow counted as failed sends,
// unbalanced scrape ownership, and reload/credential faults without real observables.
func TestGatewayFailuresAndConservation(t *testing.T) {
	for _, mode := range []string{"gateway_instance_loss", "gateway_wan_outage", "gateway_queue_overflow", "gateway_config_reload_failure", "gateway_cloud_credential_expired"} {
		t.Run(mode, func(t *testing.T) {
			data := gatewayFixture(t)
			if mode == "gateway_queue_overflow" {
				data = []byte(strings.Replace(string(data), "queue_capacity: 1000", "queue_capacity: 2", 1))
			}
			r, mc := gatewayRunner(t, data)
			before := gatewayTick(t, r, mc, gatewayClock, "", "")
			fault := gatewayTick(t, r, mc, gatewayClock.Add(time.Minute), mode, "gateway-fixture-cluster")
			after := gatewayTick(t, r, mc, gatewayClock.Add(2*time.Minute), "", "")
			switch mode {
			case "gateway_instance_loss":
				if gatewayValue(t, fault, "up", gatewayA, nil) != 0 {
					t.Fatal("lost instance still up")
				}
				if _, ok := gatewayFind(t, fault, "remotecfg_load_attempts_total", gatewayA, nil); ok {
					t.Fatal("lost process still reports data")
				}
				if gatewayValue(t, fault, "prometheus_scrape_targets_gauge", gatewayB, nil) != 4 || gatewayValue(t, fault, "cluster_node_peers", gatewayB, map[string]string{"state": "participant"}) != 1 {
					t.Fatal("survivor must own pool targets and see one participant")
				}
				if gatewayValue(t, after, "prometheus_scrape_targets_gauge", gatewayB, nil) != 2 || gatewayValue(t, after, "prometheus_scrape_targets_moved_total", gatewayB, nil) != 2 {
					t.Fatal("recovery must return target ownership and count outbound moves")
				}
			case "gateway_config_reload_failure":
				if gatewayValue(t, fault, "remotecfg_last_load_successful", gatewayA, nil) != 0 || gatewayValue(t, fault, "remotecfg_load_failures_total", gatewayA, nil) <= gatewayValue(t, before, "remotecfg_load_failures_total", gatewayA, nil) {
					t.Fatal("reload fault missing")
				}
				if gatewayValue(t, fault, "remotecfg_last_load_successful", gatewayB, nil) != 1 || gatewayValue(t, after, "remotecfg_last_load_successful", gatewayA, nil) != 1 {
					t.Fatal("reload scope/recovery wrong")
				}
			default:
				q := map[string]string{"data_type": "traces"}
				queued := gatewayValue(t, fault, "otelcol_exporter_queue_size", gatewayA, q)
				if queued <= 0 || gatewayValue(t, fault, "otelcol_exporter_send_failed_spans_total", gatewayA, nil) <= gatewayValue(t, before, "otelcol_exporter_send_failed_spans_total", gatewayA, nil) {
					t.Fatal("blocked sends must grow retained queue and attempts")
				}
				if gatewayValue(t, after, "otelcol_exporter_queue_size", gatewayA, q) != 0 {
					t.Fatal("queue must drain after recovery")
				}
				sentDelta := gatewayValue(t, after, "otelcol_exporter_sent_spans_total", gatewayA, nil) - gatewayValue(t, before, "otelcol_exporter_sent_spans_total", gatewayA, nil)
				accepted := gatewayValue(t, after, "otelcol_receiver_accepted_spans_total", gatewayA, nil) - gatewayValue(t, before, "otelcol_receiver_accepted_spans_total", gatewayA, nil)
				dropped := gatewayValue(t, after, "otelcol_exporter_enqueue_failed_spans_total", gatewayA, nil) - gatewayValue(t, before, "otelcol_exporter_enqueue_failed_spans_total", gatewayA, nil)
				if sentDelta+dropped != accepted {
					t.Fatalf("request/item conservation: sent %v + drops %v != input %v", sentDelta, dropped, accepted)
				}
				if mode != "gateway_queue_overflow" && dropped != 0 {
					t.Fatal("inside-capacity outage lost data")
				}
				// Metrics intake includes pool scrape batches: overflow is witnessed there,
				// even when trace intake exactly fits the deliberately small fixture queue.
				if mode == "gateway_queue_overflow" && gatewayValue(t, fault, "otelcol_exporter_enqueue_failed_metric_points_total", gatewayA, nil) <= 0 {
					t.Fatal("full queue must reject metric items")
				}
			}
		})
	}
}

// Catches modeled persistent queues being erased when a member becomes unavailable.
func TestGatewayQueueSurvivesMemberLoss(t *testing.T) {
	r, mc := gatewayRunner(t, gatewayFixture(t))
	before := gatewayTick(t, r, mc, gatewayClock, "", "")
	queued := gatewayTick(t, r, mc, gatewayClock.Add(time.Minute), "gateway_wan_outage", "gateway-fixture-cluster")
	gatewayTick(t, r, mc, gatewayClock.Add(2*time.Minute), "gateway_instance_loss", "gateway-fixture-cluster")
	after := gatewayTick(t, r, mc, gatewayClock.Add(3*time.Minute), "", "")
	if gatewayValue(t, queued, "otelcol_exporter_queue_size", gatewayA, map[string]string{"data_type": "traces"}) <= 0 {
		t.Fatal("persistence sequence never queued input")
	}
	sent := gatewayValue(t, after, "otelcol_exporter_sent_spans_total", gatewayA, nil) - gatewayValue(t, before, "otelcol_exporter_sent_spans_total", gatewayA, nil)
	accepted := gatewayValue(t, after, "otelcol_receiver_accepted_spans_total", gatewayA, nil) - gatewayValue(t, before, "otelcol_receiver_accepted_spans_total", gatewayA, nil)
	if sent != accepted || gatewayValue(t, after, "otelcol_exporter_queue_size", gatewayA, map[string]string{"data_type": "traces"}) != 0 {
		t.Fatal("member loss erased retained queue or recovery did not drain")
	}
}

// Catches role leakage: receive-only processes must not fabricate exporter queues,
// and forward-only processes must not report an OTLP receiver or scrape component.
func TestGatewaySeparateRoles(t *testing.T) {
	data := strings.Replace(string(gatewayFixture(t)), "roles: [otlp_receive, central_scrape, syslog, cloud_forward]", "roles: [otlp_receive, central_scrape, syslog]", 1)
	data = strings.Replace(data, "roles: [otlp_receive, central_scrape, syslog, cloud_forward]", "roles: [cloud_forward]", 1)
	r, mc := gatewayRunner(t, []byte(data))
	ss := gatewayTick(t, r, mc, gatewayClock, "", "")
	for _, s := range ss {
		if s.Labels["instance"] == gatewayA && strings.HasPrefix(s.Name, "otelcol_exporter_") {
			t.Fatal("receive-only member fabricated exporter")
		}
		if s.Labels["instance"] == gatewayB && (strings.HasPrefix(s.Name, "otelcol_receiver_") || strings.HasPrefix(s.Name, "prometheus_scrape_") || strings.HasPrefix(s.Name, "loki_source_syslog_")) {
			t.Fatal("forward-only member fabricated input role")
		}
	}
	if gatewayValue(t, ss, "otelcol_exporter_sent_spans_total", gatewayB, nil) != gatewayValue(t, ss, "otelcol_receiver_accepted_spans_total", gatewayA, nil) {
		t.Fatal("separate roles lost trace intake")
	}
}

// Catches permissive decoding and invalid identities/victims that could silently
// change which real process a source gap suppresses.
func TestGatewayStrictAdmission(t *testing.T) {
	original := string(gatewayFixture(t))
	for _, pair := range [][2]string{{"pool_size: 2", "pool_size: 3"}, {"source_victim: 0", "source_victim: 2"}, {"cloud_forward]", "unknown_role]"}, {"queue_capacity: 1000", "queue_capacity: 0"}, {"10.90.1.11:12345", "10.90.1.10:12345"}, {"requests_per_min: 2", "requests_per_min: -.inf"}, {"source_victim: 0", "source_victim: 0\n            unexpected_field: true"}} {
		data := []byte(strings.Replace(original, pair[0], pair[1], 1))
		bp, err := blueprint.Load(data, runner.Catalog())
		if err == nil {
			r := runner.New(runner.Sinks{}, runner.Catalog(), runner.Options{})
			err = r.AddBlueprint(bp)
		}
		if err == nil {
			t.Fatalf("invalid gateway accepted: %s -> %s", pair[0], pair[1])
		}
	}
}
