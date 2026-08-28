// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"context"
	"sort"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/state"
)

const nativeTokenTypeAttr = "gen_ai.token.type"

// tickOTLPMetrics emits the two core gen_ai client instruments whose OTLP names are documented in
// signals/genai.md. The established promrw state is intentionally reused here: ProjectBatch is
// the single observation owner, and the native lane only changes the representation at export.
// Sigil-only extensions remain promrw-only and are filtered from the OTLP datapoint attributes.
func (w *Workload) tickOTLPMetrics(ctx context.Context, now time.Time, world *core.World) error {
	if world == nil || !w.cfg.otelMetricsEnabled() || world.OTLPMetrics == nil {
		return nil
	}

	points := w.st.CollectHistos()
	if len(points) == 0 {
		return nil
	}
	if w.otlpColdStart.IsZero() {
		w.otlpColdStart = now
	}

	metrics := make([]otlp.Metric, 0, 2)
	if tokenPoints := nativeAIHistogramPoints(points, metricTokenUsage, true, now, w.otlpColdStart); len(tokenPoints) > 0 {
		metrics = append(metrics, otlp.Metric{
			Name:        metricTokenUsageOTLP,
			Unit:        "{token}",
			Kind:        otlp.MetricHistogram,
			Temporality: otlp.TemporalityCumulative,
			Histograms:  tokenPoints,
		})
	}
	if durationPoints := nativeAIHistogramPoints(points, metricOpDuration, false, now, w.otlpColdStart); len(durationPoints) > 0 {
		metrics = append(metrics, otlp.Metric{
			Name:        metricOpDurationOTLP,
			Unit:        "s",
			Kind:        otlp.MetricHistogram,
			Temporality: otlp.TemporalityCumulative,
			Histograms:  durationPoints,
		})
	}
	if len(metrics) == 0 {
		return nil
	}

	firstAgent := AgentDecl{}
	if len(w.cfg.Agents) > 0 {
		firstAgent = w.cfg.Agents[0]
	}
	resourceReq := &ledger.Request{Env: w.env, Cluster: w.cluster}
	return world.OTLPMetrics.Write(ctx, []otlp.MetricResource{{
		Attrs:   resourceAttrs(w.cfg.Resource, firstAgent, resourceReq),
		Metrics: metrics,
	}})
}

// These are the semantic OTLP names obtained by inverting the documented gen_ai metric
// translation: dots become underscores in promrw, unit s becomes _seconds, and {token} has no
// Prometheus suffix. Keep the two spellings adjacent so a change cannot silently drift.
const (
	metricTokenUsageOTLP = "gen_ai.client.token.usage"
	metricOpDurationOTLP = "gen_ai.client.operation.duration"
)

func nativeAIHistogramPoints(points []state.HistoPoint, name string, token bool, now, start time.Time) []otlp.HistogramPoint {
	filtered := make([]state.HistoPoint, 0)
	for _, p := range points {
		if p.Name != name {
			continue
		}
		if _, ok := nativeAIAttrs(p.Labels, token); ok {
			filtered = append(filtered, p)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return state.LabelSig(filtered[i].Labels) < state.LabelSig(filtered[j].Labels)
	})
	out := make([]otlp.HistogramPoint, 0, len(filtered))
	for _, p := range filtered {
		attrs, _ := nativeAIAttrs(p.Labels, token)
		out = append(out, otlp.HistogramPoint{
			Attrs:        attrs,
			Start:        start,
			Time:         now,
			Count:        p.Count,
			Sum:          p.Sum,
			Bounds:       append([]float64(nil), p.Bounds...),
			BucketCounts: append([]uint64(nil), p.BucketCounts...),
		})
	}
	return out
}

// nativeAIAttrs inverts only the documented gen_ai metric labels. agent_name, agent_version,
// error_category, gen_ai_tool_name, and the cache/reasoning token categories are sigil extensions
// and must not cross into the core semconv OTLP instruments.
func nativeAIAttrs(labels map[string]string, token bool) (map[string]any, bool) {
	attrs := make(map[string]any, 5)
	copyLabel := func(promName, otlpName string) {
		if value := labels[promName]; value != "" {
			attrs[otlpName] = value
		}
	}
	copyLabel(labelOperationName, genai.AttrOperationName)
	copyLabel(labelProviderName, genai.AttrProviderName)
	copyLabel(labelRequestModel, genai.AttrRequestModel)
	copyLabel("server_address", "server.address")
	copyLabel("server_port", "server.port")

	if token {
		tokenType := labels[labelTokenType]
		if tokenType != genai.TokenTypeInput && tokenType != genai.TokenTypeOutput {
			return nil, false
		}
		attrs[nativeTokenTypeAttr] = tokenType
	} else {
		copyLabel(labelErrorType, genai.AttrErrorType)
	}
	return attrs, true
}
