// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"gopkg.in/yaml.v3"
)

func automationYAML(reject, fail, recover float64) string {
	return fmt.Sprintf(`
traffic: {off_peak_rps: 0.001, peak_rps: 0.001}
services:
  - {name: controller, type: job, entry: true, namespace: platform-automation, runtime: go}
automation:
  name: node provisioning
  steps:
    - name: maintenance
      approval: {duration_ms: 1000, rejection_probability: %g}
    - name: provision-node
      http: {method: POST, url: "https://provisioner.example/v1/nodes", duration_ms: 100, max_attempts: 3, failure_probability: %g, retry_success_probability: %g}
    - name: verify-node
      http: {method: GET, url: "https://provisioner.example/v1/nodes/ready", duration_ms: 100, max_attempts: 1, failure_probability: 0, retry_success_probability: 1}
`, reject, fail, recover)
}

func automationBuild(t *testing.T, src string) core.Workload {
	t.Helper()
	reg := Registration()
	cfg := reg.NewConfig()
	dec := yaml.NewDecoder(strings.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		t.Fatalf("public YAML decode: %v", err)
	}
	w, err := reg.Build(cfg, core.Binding{Name: "automation-test", Env: coretest.Env()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return w
}

var automationCases = []struct {
	name                  string
	reject, fail, recover float64
	names                 []string
	codes                 []int
	duration              time.Duration
	terminal              otlp.StatusCode
}{
	{"approved", 0, 0, 1, []string{"node provisioning", "approval approved maintenance", "provision-node", "POST", "verify-node", "GET"}, []int{200, 200}, 1202 * time.Millisecond, otlp.StatusUnset},
	{"recovered", 0, 1, 1, []string{"node provisioning", "approval approved maintenance", "provision-node", "POST", "POST", "verify-node", "GET"}, []int{503, 200, 200}, 1302 * time.Millisecond, otlp.StatusUnset},
	{"exhausted", 0, 1, 0, []string{"node provisioning", "approval approved maintenance", "provision-node", "POST", "POST", "POST"}, []int{503, 503, 503}, 1302 * time.Millisecond, otlp.StatusError},
	{"rejected", 1, 0, 1, []string{"node provisioning", "approval rejected maintenance"}, nil, 1002 * time.Millisecond, otlp.StatusError},
}

// Catches unconditional retries, incoherent terminal status and execution after rejection/exhaustion.
func TestAutomationExecution(t *testing.T) {
	for _, tc := range automationCases {
		t.Run(tc.name, func(t *testing.T) {
			w := automationBuild(t, automationYAML(tc.reject, tc.fail, tc.recover))
			capture := &coretest.TraceCapture{}
			world := coretest.World(nil, nil, capture)
			now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
			batch := w.Minter().Mint(now, 1000, world.Shape)
			if len(batch) != 1 {
				t.Fatalf("minted %d, want one sparse request", len(batch))
			}
			r := batch[0]
			if r.Duration != tc.duration {
				t.Fatalf("duration %v want %v", r.Duration, tc.duration)
			}
			if len(r.Calls) != 0 {
				t.Fatal("automation must not fabricate graph edges")
			}
			if (r.Outcome != ledger.OutcomeSuccess) != (tc.terminal == otlp.StatusError) {
				t.Fatal("ledger outcome disagrees with execution")
			}
			if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
				t.Fatal(err)
			}
			if len(capture.Resources) != 1 {
				t.Fatalf("resources: %d", len(capture.Resources))
			}
			spans := capture.Resources[0].Spans
			var names []string
			var codes []int
			seen := map[string]bool{}
			parents := map[string]otlp.Span{}
			for _, s := range spans {
				parents[s.SpanID] = s
			}
			for _, s := range spans {
				names = append(names, s.Name)
				if s.TraceID != r.TraceID || seen[s.SpanID] {
					t.Fatal("invalid trace identity/duplicate span ID")
				}
				seen[s.SpanID] = true
				if !s.End.After(s.Start) {
					t.Fatal("nonpositive span duration")
				}
				if s.ParentID != "" {
					p, ok := parents[s.ParentID]
					if !ok || s.Start.Before(p.Start) || s.End.After(p.End) {
						t.Fatal("invalid parent/window")
					}
				}
				if s.Kind == otlp.KindClient {
					code, ok := s.Attrs["http.response.status_code"].(int)
					if !ok {
						t.Fatal("status must be integer")
					}
					codes = append(codes, code)
					if s.End.Sub(s.Start) != 100*time.Millisecond {
						t.Fatal("API duration")
					}
					if s.Attrs["server.address"] != "provisioner.example" || s.Attrs["server.port"] != 443 || s.Attrs["http.request.method"] != s.Name {
						t.Fatal("HTTP target/method attrs")
					}
					p := parents[s.ParentID]
					if p.Name != "provision-node" && p.Name != "verify-node" {
						t.Fatal("attempt must parent to task")
					}
					ordinal := 0
					for _, prior := range spans {
						if prior.SpanID == s.SpanID {
							break
						}
						if prior.ParentID == s.ParentID && prior.Kind == otlp.KindClient {
							ordinal++
							if prior.End.After(s.Start) {
								t.Fatal("attempt overlap")
							}
						}
					}
					if ordinal == 0 {
						if _, ok := s.Attrs["http.request.resend_count"]; ok {
							t.Fatal("first send has resend count")
						}
					} else if s.Attrs["http.request.resend_count"] != ordinal {
						t.Fatal("wrong resend ordinal")
					}
					if code == 503 {
						if s.Status != otlp.StatusError || s.Attrs["error.type"] != "503" {
							t.Fatal("failed attempt must remain ERROR")
						}
					} else {
						if s.Status != otlp.StatusUnset {
							t.Fatal("successful attempt status")
						}
						if _, ok := s.Attrs["error.type"]; ok {
							t.Fatal("success has error.type")
						}
					}
				}
				for key := range s.Attrs {
					if strings.Contains(key, "payload") || strings.Contains(key, "approval") || strings.HasPrefix(key, "gen_ai.") {
						t.Fatalf("unexpected metadata %s", key)
					}
				}
			}
			if !reflect.DeepEqual(names, tc.names) || !reflect.DeepEqual(codes, tc.codes) {
				t.Fatalf("execution names=%v codes=%v", names, codes)
			}
			if spans[0].Kind != otlp.KindInternal || spans[0].Status != tc.terminal || spans[0].End.Sub(spans[0].Start) != tc.duration {
				t.Fatal("root status/kind/timing")
			}
			if spans[1].Status != otlp.StatusUnset {
				t.Fatal("approval decision itself is successful")
			}
			if tc.name == "rejected" && spans[0].Attrs["error.type"] != "_OTHER" {
				t.Fatal("rejection convention")
			}
			if tc.name == "exhausted" && spans[2].Status != otlp.StatusError {
				t.Fatal("exhausted task status")
			}
			if tc.name == "recovered" && spans[2].Status != otlp.StatusUnset {
				t.Fatal("recovered task cannot retain error")
			}
			if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(spans, capture.Resources[1].Spans) {
				t.Fatal("repeat projection differs")
			}
		})
	}
}

func automationMetricKey(labels map[string]string) string {
	return labels["span_name"] + "|" + labels["span_kind"] + "|" + labels["status_code"]
}
func automationSpanKey(s otlp.Span) string {
	kind := "SPAN_KIND_INTERNAL"
	if s.Kind == otlp.KindClient {
		kind = "SPAN_KIND_CLIENT"
	}
	status := "STATUS_CODE_UNSET"
	if s.Status == otlp.StatusError {
		status = "STATUS_CODE_ERROR"
	}
	return s.Name + "|" + kind + "|" + status
}

// Catches missing low-volume child rows, divergent latency/status and duplicate observation on accelerated ticks.
func TestAutomationSpanMetricParity(t *testing.T) {
	for _, tc := range automationCases {
		t.Run(tc.name, func(t *testing.T) {
			w := automationBuild(t, automationYAML(tc.reject, tc.fail, tc.recover))
			metrics := &coretest.MetricCapture{}
			traces := &coretest.TraceCapture{}
			world := coretest.World(metrics, nil, traces)
			world.Ledger = ledger.New(world.Shape, 0, 0)
			world.Ledger.SetTickSeconds(1000)
			world.Ledger.AddMinter(w.Minter())
			now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
			var all []otlp.Span
			for generation := 1; generation <= 2; generation++ {
				batch := world.Ledger.Mint(now)
				if len(batch) != 1 {
					t.Fatalf("sparse ledger minted %d", len(batch))
				}
				if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
					t.Fatal(err)
				}
				all = append(all, traces.Resources[len(traces.Resources)-1].Spans...)
				if err := w.Tick(context.Background(), now, world); err != nil {
					t.Fatal(err)
				}
				expectedCalls := map[string]float64{}
				expectedSum := map[string]float64{}
				for _, s := range all {
					k := automationSpanKey(s)
					expectedCalls[k]++
					expectedSum[k] += s.End.Sub(s.Start).Seconds()
				}
				for repeat := 0; repeat < 3; repeat++ {
					latest := metrics.Batches[len(metrics.Batches)-1]
					calls := map[string]float64{}
					counts := map[string]float64{}
					sums := map[string]float64{}
					native := map[string]float64{}
					for _, s := range latest {
						if strings.HasPrefix(s.Name, "traces_service_graph_") {
							t.Fatal("fabricated remote servicegraph")
						}
						if !strings.HasPrefix(s.Name, "traces_spanmetrics_") {
							continue
						}
						for _, key := range []string{"request_id", "trace_id", "url.full", "server.address", "http.request.resend_count", "approval.outcome"} {
							if _, ok := s.Labels[key]; ok {
								t.Fatalf("high-card/operational label %s", key)
							}
						}
						k := automationMetricKey(s.Labels)
						switch s.Name {
						case "traces_spanmetrics_calls_total":
							calls[k] = s.Value
						case "traces_spanmetrics_latency_count":
							counts[k] = s.Value
						case "traces_spanmetrics_latency_sum":
							sums[k] = s.Value
						case "traces_spanmetrics_latency":
							if s.Native == nil {
								t.Fatal("missing native histogram")
							}
							native[k] = float64(s.Native.Count)
						}
					}
					if !reflect.DeepEqual(calls, expectedCalls) || !reflect.DeepEqual(counts, expectedCalls) || !reflect.DeepEqual(native, expectedCalls) {
						t.Fatalf("observations calls=%v counts=%v native=%v want=%v", calls, counts, native, expectedCalls)
					}
					for k, v := range expectedSum {
						if math.Abs(sums[k]-v) > 1e-9 {
							t.Fatalf("latency %s=%g want %g", k, sums[k], v)
						}
					}
					if err := w.Tick(context.Background(), now.Add(time.Duration(repeat)*time.Second), world); err != nil {
						t.Fatal(err)
					}
				}
				now = now.Add(10 * time.Second)
			}
		})
	}
	// Empty ledger and default-off must not invent observations.
	for _, enabled := range []bool{false, true} {
		w := automationBuild(t, automationYAML(0, 0, 1))
		mc := &coretest.MetricCapture{}
		world := coretest.World(mc, nil, nil)
		world.EmitSpanMetrics = enabled
		if !enabled {
			world.Ledger = ledger.New(world.Shape, 0, 0)
			world.Ledger.SetTickSeconds(1000)
			world.Ledger.AddMinter(w.Minter())
			world.Ledger.Mint(time.Now())
		}
		if err := w.Tick(context.Background(), time.Now(), world); err != nil {
			t.Fatal(err)
		}
		if len(mc.Find("traces_spanmetrics_calls_total")) != 0 {
			t.Fatal("empty/default-off observations")
		}
	}
}

// Catches unsafe URLs, invalid bounds and unsupported composition admitted at the YAML/Build boundary.
func TestAutomationValidation(t *testing.T) {
	base := automationYAML(0, 0, 1)
	cases := []struct{ name, src string }{
		{"conflicting forms", strings.Replace(base, "approval: {duration_ms: 1000, rejection_probability: 0}", "approval: {duration_ms: 1000, rejection_probability: 0}\n      http: {method: GET, url: https://target.example, duration_ms: 1}", 1)},
		{"probability", strings.Replace(base, "failure_probability: 0", "failure_probability: 2", 1)},
		{"nonfinite", strings.Replace(base, "rejection_probability: 0", "rejection_probability: .nan", 1)},
		{"attempt bound", strings.Replace(base, "max_attempts: 3", "max_attempts: 6", 1)},
		{"negative attempts", strings.Replace(base, "max_attempts: 3", "max_attempts: -1", 1)},
		{"credentials", strings.Replace(base, "https://provisioner.example/v1/nodes", "https://user:secret@provisioner.example/v1/nodes", 1)},
		{"query", strings.Replace(base, "https://provisioner.example/v1/nodes", "https://provisioner.example/v1/nodes?token=value", 1)},
		{"fragment", strings.Replace(base, "https://provisioner.example/v1/nodes", "https://provisioner.example/v1/nodes#value", 1)},
		{"port", strings.Replace(base, "https://provisioner.example/v1/nodes", "https://provisioner.example:99999/v1/nodes", 1)},
		{"method", strings.Replace(base, "method: POST", "method: INVALID", 1)},
		{"duration", strings.Replace(base, "duration_ms: 1000", "duration_ms: 0", 1)},
		{"duration overflow", strings.Replace(base, "duration_ms: 1000", "duration_ms: 9223372036854775807", 1)},
		{"over day", strings.Replace(base, "duration_ms: 1000", "duration_ms: 86400000", 1)},
		{"duplicate steps", strings.Replace(base, "name: verify-node", "name: provision-node", 1)},
		{"non-job", strings.Replace(base, "type: job", "type: web", 1)},
		{"graph composition", strings.Replace(base, "runtime: go", "runtime: go, calls: [controller]", 1)},
		{"traces disabled", strings.Replace(base, "runtime: go", "runtime: go, signals: {traces: false}", 1)},
		{"inline spans", strings.Replace(base, "runtime: go", "runtime: go, spans: [{name_template: forbidden}]", 1)},
		{"native metrics", base + "otel: {metrics: true}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Registration().NewConfig()
			dec := yaml.NewDecoder(strings.NewReader(tc.src))
			dec.KnownFields(true)
			if err := dec.Decode(cfg); err != nil {
				t.Fatalf("fixture decode: %v", err)
			}
			if _, err := Registration().Build(cfg, core.Binding{Name: "invalid"}); err == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
	// Zero max_attempts is the admitted single-attempt default.
	_ = automationBuild(t, strings.Replace(base, "max_attempts: 3", "max_attempts: 0", 1))
}

// Catches absent or span-level classifier and cross-resource inheritance.
func TestAutomationResourceRouting(t *testing.T) {
	capture := &coretest.TraceCapture{}
	world := coretest.World(nil, nil, capture)
	now := time.Now()
	platform := automationBuild(t, automationYAML(0, 0, 1))
	user := automationBuild(t, "traffic: {off_peak_rps: 0.001, peak_rps: 0.001}\nservices: [{name: application, type: web, entry: true, namespace: user-workloads}]\n")
	for _, w := range []core.Workload{platform, user} {
		batch := w.Minter().Mint(now, 1000, world.Shape)
		if len(batch) != 1 {
			t.Fatal("missing representative request")
		}
		if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
			t.Fatal(err)
		}
	}
	platforms, users := 0, 0
	for _, r := range capture.Resources {
		p := r.Attrs["service.namespace"] == "platform-automation"
		u := r.Attrs["service.namespace"] == "user-workloads"
		if p == u {
			t.Fatal("resource not exclusively routed")
		}
		if p {
			platforms++
		}
		if u {
			users++
		}
		for _, s := range r.Spans {
			if _, ok := s.Attrs["service.namespace"]; ok {
				t.Fatal("classifier incorrectly on span")
			}
		}
	}
	if platforms != 1 || users != 1 {
		t.Fatalf("routing platform=%d user=%d", platforms, users)
	}
}
