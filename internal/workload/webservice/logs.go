// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/semconv"
	"github.com/rknightion/synthkit/internal/sink/loki"
)

// projectLogs emits app logs for the batch. ONE request-lifecycle line per request, plus
// an error-detail line on failures. Stream labels are low-cardinality only (I14); the
// high-card correlation keys ride in structured metadata. Lines are grouped into streams
// keyed by {env, service_name, level, source, cluster, job} — level is the only
// per-request-varying stream label and it is low-card (info|warn|error).
func (w *Workload) projectLogs(ctx context.Context, world *core.World, batch []*ledger.Request) error {
	// Group lines by level so each stream has a single low-card label set.
	byLevel := map[string]*loki.Stream{}
	streamFor := func(level string) *loki.Stream {
		st := byLevel[level]
		if st == nil {
			st = &loki.Stream{Labels: w.logStreamLabels(level)}
			byLevel[level] = st
		}
		return st
	}

	for _, r := range batch {
		ts := r.RenderStart().Add(r.Duration) // request completes at end of its window
		level := logLevel(r.Outcome)

		// 1) request-lifecycle line (one per request).
		body := lifecycleBody{
			Msg:       "request completed",
			Status:    r.Outcome.String(),
			Route:     r.Route,
			LatencyMs: r.Duration.Milliseconds(),
			Outcome:   r.Outcome.String(),
		}
		st := streamFor(level)
		st.Lines = append(st.Lines, loki.Line{T: ts, Body: jsonBody(body), Meta: w.logMeta(r)})

		// 2) error-detail line on failures.
		if r.Outcome != ledger.OutcomeSuccess {
			eb := errorBody{
				Msg:       "request failed",
				Status:    r.StatusCode,
				ErrorKind: r.ErrorKind,
				Outcome:   r.Outcome.String(),
			}
			est := streamFor(logLevel(r.Outcome))
			est.Lines = append(est.Lines, loki.Line{T: ts.Add(time.Microsecond), Body: jsonBody(eb), Meta: w.logMeta(r)})
		}
	}

	streams := make([]loki.Stream, 0, len(byLevel))
	for _, st := range byLevel {
		if len(st.Lines) > 0 {
			streams = append(streams, *st)
		}
	}

	// Per-hop failure lines: a failed "service" hop logs in the CALLEE's stream (its own
	// service_name), carrying the hop's own span_id so the callee log joins the callee SERVER
	// span (trace+span id). One stream per failed service hop; appended AFTER the level-keyed
	// streams because the service_name differs from the caller's. Request-level lines unchanged.
	for _, r := range batch {
		ts := r.RenderStart().Add(r.Duration)
		for i := range r.Calls {
			call := r.Calls[i]
			if call.Kind != "service" || !call.Failed {
				continue
			}
			hb := errorBody{Msg: "upstream call failed", Status: r.StatusCode, ErrorKind: r.ErrorKind, Outcome: r.Outcome.String()}
			streams = append(streams, loki.Stream{
				Labels: w.calleeStreamLabels(call.Target),
				Lines: []loki.Line{{
					T: ts.Add(2 * time.Microsecond), Body: jsonBody(hb), Meta: w.hopLogMeta(r, call),
				}},
			})
		}
	}

	if len(streams) == 0 {
		return nil
	}
	return world.Logs.Write(ctx, streams)
}

// projectAILogs emits the three correlated AI log streams (Spec 2b §4.3), reusing the
// per-hop request-correlation keys. Stream labels stay low-card ({env,service_name,level,source,
// cluster,job}); all high-card join keys (trace_id/span_id/correlation_id/portkey_trace_id/
// run_id) ride in structured metadata (I14 — the sink asserts). Bodies are content-free.
//   - source=portkey            one line per gateway/inference hop (the export schema).
//   - source=bedrock_invocation one line per model hop on the bedrock provider.
//   - source=langsmith-runs     one line per request with AI activity (the run-index).
func (w *Workload) projectAILogs(ctx context.Context, world *core.World, batch []*ledger.Request) error {
	type key struct{ source, level string }
	byKey := map[key]*loki.Stream{}
	streamFor := func(source, level string) *loki.Stream {
		k := key{source, level}
		st := byKey[k]
		if st == nil {
			st = &loki.Stream{Labels: w.aiLogStreamLabels(source, level)}
			byKey[k] = st
		}
		return st
	}

	for _, r := range batch {
		ts := r.RenderStart().Add(r.Duration)
		level := logLevel(r.Outcome)
		hasAI := false
		for i := range r.Calls {
			call := r.Calls[i]
			if call.AI == nil {
				continue
			}
			hasAI = true
			// Portkey export — gateway hops and any inference (model-bearing) hop.
			if call.Kind == fixture.KindLLMGateway || call.AI.Model != "" {
				in, out := synthTokens(call.SpanID)
				retryCount, fallback := synthRetryFallback(call.SpanID)
				body := portkeyExportBody{
					AIModel:            call.AI.Model,
					AIOrg:              call.AI.Provider,
					Cost:               float64(in+out) * 0.000002, // ≈ $2 / 1M tokens
					ReqUnits:           in,
					ResUnits:           out,
					ResponseStatusCode: r.StatusCode,
					RetryCount:         retryCount,
					Fallback:           fallback,
				}
				st := streamFor("portkey", level)
				st.Lines = append(st.Lines, loki.Line{T: ts.Add(3 * time.Microsecond), Body: jsonBody(body), Meta: w.aiHopMeta(r, call)})
			}
			// Bedrock invocation — model hops served by the bedrock provider.
			if call.AI.Model != "" && call.AI.Provider == "bedrock" {
				in, out := synthTokens(call.SpanID)
				body := bedrockInvocationBody{
					Operation:        "InvokeModel",
					ModelID:          call.AI.Model,
					InputTokenCount:  in,
					OutputTokenCount: out,
				}
				st := streamFor("bedrock_invocation", level)
				st.Lines = append(st.Lines, loki.Line{T: ts.Add(4 * time.Microsecond), Body: jsonBody(body), Meta: w.aiHopMeta(r, call)})
			}
		}
		// LangSmith run-index — once per request with AI activity.
		if hasAI {
			body := langsmithRunBody{Msg: "run indexed", AWSEnv: w.env}
			st := streamFor("langsmith-runs", level)
			st.Lines = append(st.Lines, loki.Line{T: ts.Add(5 * time.Microsecond), Body: jsonBody(body), Meta: w.langsmithRunMeta(r)})
		}
	}

	streams := make([]loki.Stream, 0, len(byKey))
	for _, st := range byKey {
		if len(st.Lines) > 0 {
			streams = append(streams, *st)
		}
	}
	if len(streams) == 0 {
		return nil
	}
	return world.Logs.Write(ctx, streams)
}

// aiLogStreamLabels are the low-card stream labels for an AI log source. source ∈
// {portkey, bedrock_invocation, langsmith-runs}.
func (w *Workload) aiLogStreamLabels(source, level string) map[string]string {
	return pruneEmpty(map[string]string{
		"env":          w.env,
		"service_name": w.name,
		"level":        level,
		"source":       source,
		"cluster":      w.cluster,
		"job":          w.job(),
	})
}

// aiHopMeta is the structured metadata for a per-hop AI log line: the hop's CLIENT span_id
// + the request-correlation join keys (incl portkey_trace_id + traceparent W3C context). High-card
// only — never labels (I14).
func (w *Workload) aiHopMeta(r *ledger.Request, call ledger.Call) map[string]string {
	return map[string]string{
		"trace_id":                 r.TraceID,
		"span_id":                  call.SpanID,
		semconv.FieldCorrelationID: r.CorrelationID,
		semconv.KeyPortkeyTraceID:  r.PortkeyTraceID,
		semconv.KeyTraceparent:     "00-" + r.TraceID + "-" + call.SpanID + "-01",
		"request_id":               r.RequestID,
		"session_id":               r.SessionID,
	}
}

// langsmithRunMeta is the run-index structured metadata: run_id + portkey_trace_id +
// correlation_id + traceparent (+ trace/span/request/session) — the keys a LangSmith run
// joins on. traceparent carries the W3C trace context for cross-system correlation (§4.1).
func (w *Workload) langsmithRunMeta(r *ledger.Request) map[string]string {
	return map[string]string{
		"trace_id":                 r.TraceID,
		"span_id":                  r.SpanID,
		"run_id":                   r.RunID,
		semconv.KeyPortkeyTraceID:  r.PortkeyTraceID,
		semconv.FieldCorrelationID: r.CorrelationID,
		semconv.KeyTraceparent:     "00-" + r.TraceID + "-" + r.SpanID + "-01",
		"request_id":               r.RequestID,
		"session_id":               r.SessionID,
	}
}

// portkeyExportBody is the content-free Portkey export-log body (signals/portkey.md
// [slug: portkey-logs]). ai_model (NOT model) and ai_org (the provider).
type portkeyExportBody struct {
	AIModel            string  `json:"ai_model"`
	AIOrg              string  `json:"ai_org"`
	Cost               float64 `json:"cost"`
	ReqUnits           int     `json:"req_units"`
	ResUnits           int     `json:"res_units"`
	ResponseStatusCode int     `json:"response_status_code"`
	RetryCount         int     `json:"retry_count"`
	Fallback           bool    `json:"fallback"`
}

// bedrockInvocationBody is the content-free Bedrock invocation-log body (signals/bedrock.md
// [slug: bedrock-logs]). Token COUNTS only — bodies (inputBodyJson/outputBodyJson) stripped.
// The predecessor schema also carries requestMetadata.use_case (per-use-case attribution), but
// use_case is a CUSTOMER/blueprint value the generic workload does not hold — the blueprint
// layer supplies use-case metadata (Spec 3), it is not invented here.
type bedrockInvocationBody struct {
	Operation        string `json:"operation"`
	ModelID          string `json:"modelId"`
	InputTokenCount  int    `json:"input_inputTokenCount"`
	OutputTokenCount int    `json:"output_outputTokenCount"`
}

// langsmithRunBody is the content-free LangSmith run-index body (the run's join keys ride in
// structured metadata; inputs/outputs/messages are NEVER emitted).
type langsmithRunBody struct {
	Msg    string `json:"msg"`
	AWSEnv string `json:"aws_env"`
}

// calleeStreamLabels are the low-card stream labels for a service-hop callee's log stream.
func (w *Workload) calleeStreamLabels(callee string) map[string]string {
	return map[string]string{
		"env":          w.env,
		"service_name": callee,
		"level":        "error",
		"source":       "app",
		"cluster":      w.cluster,
		"job":          w.namespace + "/" + callee,
	}
}

// hopLogMeta is the structured metadata for a per-hop callee log line: the hop's CLIENT
// span_id (so the callee log joins the callee SERVER span via trace+span id) + correlation.
func (w *Workload) hopLogMeta(r *ledger.Request, call ledger.Call) map[string]string {
	return map[string]string{
		"trace_id":                 r.TraceID,
		"span_id":                  call.SpanID,
		semconv.FieldCorrelationID: r.CorrelationID,
		"request_id":               r.RequestID,
		"session_id":               r.SessionID,
	}
}

// logStreamLabels are the low-card Loki stream labels (the index key). source="app".
// service_name (NOT service) is the app-tier standardization so one trace-to-logs mapping
// service.name→service_name covers every stream (extract §3.1).
func (w *Workload) logStreamLabels(level string) map[string]string {
	return map[string]string{
		"env":          w.env,
		"service_name": w.name,
		"level":        level,
		"source":       "app",
		"cluster":      w.cluster,
		"job":          w.job(),
	}
}

// logMeta is the high-card structured metadata (NEVER stream labels — I14). Only ledger
// IDs (I9). span_id is the backend SERVER span id.
func (w *Workload) logMeta(r *ledger.Request) map[string]string {
	return map[string]string{
		"trace_id":                 r.TraceID,
		"span_id":                  r.SpanID,
		semconv.FieldCorrelationID: r.CorrelationID,
		"request_id":               r.RequestID,
		"session_id":               r.SessionID,
	}
}

// lifecycleBody is the per-request completion log body (content-stripped — no row data).
type lifecycleBody struct {
	Msg       string `json:"msg"`
	Status    string `json:"status"`
	Route     string `json:"route"`
	LatencyMs int64  `json:"latency_ms"`
	Outcome   string `json:"outcome"`
}

// errorBody is the failure-detail log body.
type errorBody struct {
	Msg       string `json:"msg"`
	Status    int    `json:"status"`
	ErrorKind string `json:"error_kind"`
	Outcome   string `json:"outcome"`
}

// synthRetryFallback derives deterministic retry_count and fallback values from a span ID.
// It uses chars 4-7 of the hex span ID (offset from synthTokens which uses chars 0-3) so the
// two fields don't correlate with token counts. Distribution matches signals/portkey.md:
//   - retry_count: 0 with P≈0.90; 1/2/3 share the remaining ~10% evenly.
//   - fallback: true with P≈0.03, seeded independently from chars 8-11.
func synthRetryFallback(spanID string) (retryCount int, fallback bool) {
	retrySeed := 0
	if len(spanID) >= 8 {
		if v, err := strconv.ParseInt(spanID[4:8], 16, 0); err == nil {
			retrySeed = int(v)
		}
	}
	// 0–65535; 0–58981 (90%) → retry 0; remainder split into three ~3.33% bands.
	r := retrySeed % 100
	switch {
	case r < 90:
		retryCount = 0
	case r < 93:
		retryCount = 1
	case r < 97:
		retryCount = 2
	default:
		retryCount = 3
	}

	fbSeed := 0
	if len(spanID) >= 12 {
		if v, err := strconv.ParseInt(spanID[8:12], 16, 0); err == nil {
			fbSeed = int(v)
		}
	}
	fallback = (fbSeed % 100) < 3
	return
}

func jsonBody(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// logLevel maps an outcome to the low-card level stream label.
func logLevel(o ledger.Outcome) string {
	switch o {
	case ledger.OutcomeClientError:
		return "warn"
	case ledger.OutcomeServerError, ledger.OutcomeThrottled:
		return "error"
	default:
		return "info"
	}
}
