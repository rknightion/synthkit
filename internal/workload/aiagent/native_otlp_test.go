// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"context"
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

type aiNativeMetricCapture struct {
	resources []otlp.MetricResource
}

func (c *aiNativeMetricCapture) Write(_ context.Context, resources []otlp.MetricResource) error {
	c.resources = append(c.resources, resources...)
	return nil
}

func aiNativeConfig(enabled bool) *Config {
	agent := codingAgent()
	cfg := &Config{
		Resource: ResourceID{
			ServiceName:      "ai-runtime",
			ServiceNamespace: "agents",
			ServiceVersion:   "2.0.0",
			Job:              "agents/ai-runtime",
		},
		Agents: []AgentDecl{agent},
	}
	if enabled {
		cfg.OTel = &OTelObs{Metrics: true}
	}
	return cfg
}

func buildAINative(t *testing.T, enabled bool) *Workload {
	t.Helper()
	w, err := build(aiNativeConfig(enabled), core.Binding{
		Name:    "ai-fleet",
		Env:     coretest.Env(),
		Cluster: coretest.Cluster(),
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return w.(*Workload)
}

func aiNativeMetric(t *testing.T, resource otlp.MetricResource, name string) otlp.Metric {
	t.Helper()
	for _, metric := range resource.Metrics {
		if metric.Name == name {
			return metric
		}
	}
	t.Fatalf("native metric %q not found", name)
	return otlp.Metric{}
}

func populateAIMetrics(t *testing.T, w *Workload) {
	t.Helper()
	r := fixedReq("native-ai-conversation", 90*time.Second)
	r.Route = w.cfg.Agents[0].Name
	r.Provider = w.cfg.Agents[0].Provider
	r.Model = w.cfg.Agents[0].Models[0]
	world := &core.World{Shape: shape.New("", nil)}
	if err := w.ProjectBatch(context.Background(), time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC), world, []*ledger.Request{r}); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}
}

func TestAINativeSignalsFollowExplicitSwitch(t *testing.T) {
	off := buildAINative(t, false)
	if slices.Contains(off.Signals(), core.OTLPMetrics) {
		t.Fatal("absent otel.metrics switch must not declare native metrics")
	}

	on := buildAINative(t, true)
	if !slices.Contains(on.Signals(), core.OTLPMetrics) {
		t.Fatal("otel.metrics=true must declare native metrics")
	}

	cfg := aiNativeConfig(false)
	cfg.OTel = &OTelObs{Metrics: false}
	w, err := build(cfg, core.Binding{Name: "ai-fleet", Env: coretest.Env(), Cluster: coretest.Cluster()})
	if err != nil {
		t.Fatalf("build false switch: %v", err)
	}
	if slices.Contains(w.(*Workload).Signals(), core.OTLPMetrics) {
		t.Fatal("otel.metrics=false must not declare native metrics")
	}
}

func TestAINativeCoreFamiliesInvertDocumentedTranslation(t *testing.T) {
	w := buildAINative(t, true)
	populateAIMetrics(t, w)

	native := &aiNativeMetricCapture{}
	now := time.Date(2026, 8, 28, 10, 1, 0, 0, time.UTC)
	if err := w.Tick(context.Background(), now, &core.World{Shape: shape.New("", nil), OTLPMetrics: native}); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(native.resources) != 1 {
		t.Fatalf("native resources = %d, want one workload resource", len(native.resources))
	}
	resource := native.resources[0]
	if resource.Attrs["service.name"] != "ai-runtime" || resource.Attrs["service.namespace"] != "agents" {
		t.Fatalf("resource identity = %#v", resource.Attrs)
	}
	if resource.Attrs["deployment.environment.name"] != "prod" || resource.Attrs["k8s.cluster.name"] != "test-prod-use1" {
		t.Fatalf("binding resource identity = %#v", resource.Attrs)
	}
	if got := []string{resource.Metrics[0].Name, resource.Metrics[1].Name}; !reflect.DeepEqual(got, []string{metricTokenUsageOTLP, metricOpDurationOTLP}) {
		t.Fatalf("native metric names/order = %v, want [%s %s]", got, metricTokenUsageOTLP, metricOpDurationOTLP)
	}

	token := aiNativeMetric(t, resource, metricTokenUsageOTLP)
	if token.Kind != otlp.MetricHistogram || token.Unit != "{token}" || token.Temporality != otlp.TemporalityCumulative {
		t.Fatalf("token metric shape = %+v", token)
	}
	if len(token.Histograms) != 2 {
		t.Fatalf("token datapoints = %d, want input/output only", len(token.Histograms))
	}
	for _, point := range token.Histograms {
		if point.Start != now || point.Time != now {
			t.Errorf("token point times = start %s time %s, want %s", point.Start, point.Time, now)
		}
		if got := point.Attrs[nativeTokenTypeAttr]; got != "input" && got != "output" {
			t.Errorf("token type = %v, want input or output", got)
		}
		assertNoAISigilAttrs(t, point.Attrs)
	}

	duration := aiNativeMetric(t, resource, metricOpDurationOTLP)
	if duration.Kind != otlp.MetricHistogram || duration.Unit != "s" || duration.Temporality != otlp.TemporalityCumulative {
		t.Fatalf("duration metric shape = %+v", duration)
	}
	if len(duration.Histograms) == 0 {
		t.Fatal("duration metric has no datapoints")
	}
	for _, point := range duration.Histograms {
		if point.Start != now || point.Time != now {
			t.Errorf("duration point times = start %s time %s, want %s", point.Start, point.Time, now)
		}
		if point.Attrs["gen_ai.operation.name"] == nil || point.Attrs["gen_ai.provider.name"] == nil || point.Attrs["gen_ai.request.model"] == nil {
			t.Errorf("duration point missing core semconv attrs: %#v", point.Attrs)
		}
		assertNoAISigilAttrs(t, point.Attrs)
	}
}

func assertNoAISigilAttrs(t *testing.T, attrs map[string]any) {
	t.Helper()
	for _, key := range []string{
		"agent_name", "agent_version", "gen_ai_tool_name", "error_category",
		"gen_ai_token_type", "gen_ai.cache_read", "gen_ai.cache_write", "gen_ai.reasoning",
	} {
		if _, ok := attrs[key]; ok {
			t.Errorf("sigil extension attr %q leaked into native datapoint: %#v", key, attrs)
		}
	}
}

func TestAINativeMapsErrorTypeAndOmitsCategory(t *testing.T) {
	w := buildAINative(t, true)
	accumulate(w.st, metricObs{
		agentName: "claude-code", operation: "chat", provider: "anthropic", model: "claude-sonnet-4-6",
		opDurationSec: 1.5, errorType: "timeout", errorCategory: "transient",
	})
	native := &aiNativeMetricCapture{}
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	if err := w.Tick(context.Background(), now, &core.World{Shape: shape.New("", nil), OTLPMetrics: native}); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(native.resources) != 1 {
		t.Fatalf("resources = %d, want one", len(native.resources))
	}
	duration := aiNativeMetric(t, native.resources[0], metricOpDurationOTLP)
	if len(duration.Histograms) != 1 {
		t.Fatalf("duration points = %d, want one", len(duration.Histograms))
	}
	attrs := duration.Histograms[0].Attrs
	if attrs["error.type"] != "timeout" {
		t.Errorf("error.type = %v, want timeout", attrs["error.type"])
	}
	if _, ok := attrs["error_category"]; ok {
		t.Error("error_category must remain promrw-only")
	}
}

func TestAINativeNilWriterIsSafe(t *testing.T) {
	w := buildAINative(t, true)
	populateAIMetrics(t, w)
	mc := &coretest.MetricCapture{}
	if err := w.Tick(context.Background(), time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC), &core.World{Shape: shape.New("", nil), Metrics: mc}); err != nil {
		t.Fatalf("Tick with nil native writer: %v", err)
	}
	if len(mc.All()) == 0 {
		t.Fatal("nil native writer must leave promrw lane operational")
	}
}

func TestAINativeSwitchLeavesPromrwPayloadUnchanged(t *testing.T) {
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	off := buildAINative(t, false)
	populateAIMetrics(t, off)
	offCapture := &coretest.MetricCapture{}
	if err := off.Tick(context.Background(), now, &core.World{Shape: shape.New("", nil), Metrics: offCapture}); err != nil {
		t.Fatalf("off Tick: %v", err)
	}

	on := buildAINative(t, true)
	populateAIMetrics(t, on)
	onCapture := &coretest.MetricCapture{}
	if err := on.Tick(context.Background(), now, &core.World{Shape: shape.New("", nil), Metrics: onCapture, OTLPMetrics: &aiNativeMetricCapture{}}); err != nil {
		t.Fatalf("on Tick: %v", err)
	}
	offSeries, onSeries := canonicalAISeries(offCapture.All()), canonicalAISeries(onCapture.All())
	if !reflect.DeepEqual(offSeries, onSeries) {
		t.Fatal("enabling native OTLP changed the Prometheus payload")
	}
}

func canonicalAISeries(in []promrw.Series) []promrw.Series {
	out := append([]promrw.Series(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		if sigI, sigJ := state.LabelSig(out[i].Labels), state.LabelSig(out[j].Labels); sigI != sigJ {
			return sigI < sigJ
		}
		if out[i].Value != out[j].Value {
			return out[i].Value < out[j].Value
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

var _ core.OTLPMetricWriter = (*aiNativeMetricCapture)(nil)
