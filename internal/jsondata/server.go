// SPDX-License-Identifier: AGPL-3.0-only

// Package jsondata builds the Infinity-over-hosted-JSON payloads that back the
// tabular Grafana Infinity datasource surfaces: request_correlation_sample, blueprints,
// recent requests, and the full acme-ai-platform dashboard suite.
//
// Routes (all GET, strictly side-effect-free — invariant I26):
//
//	GET /                                     route index
//	GET /healthz                              liveness
//	GET /request_correlation_sample                 most-recent request per blueprint with full correlation key-set
//	GET /requests?blueprint=X&window=15m      recent request narrative (capped at 500 rows)
//	GET /blueprints                           loaded blueprint names + workload counts
//
//	GET /acme/request_correlation                   request/correlation_keys/hops for acme-ai-platform dashboards
//	GET /v1/analytics/groups/metadata         per-use_case analytics aggregates (Portkey)
//	GET /v1/analytics/groups/ai-models        per-model analytics aggregates (Portkey)
//	GET /v1/configs                           Portkey provider/virtual-key catalog
//	GET /v1/prompts                           Portkey prompt registry
//	GET /api/v1/runs/query                    LangSmith eval runs (GET wrapper; real API is POST)
//	GET /api/v1/sessions                      LangSmith projects with stats
//	GET /acme/aaef_kpis                       AAEF executive KPI scores by context × period
//	GET /acme/eval_scorecard                  per-project × gate eval scorecard
//
// CONTENT STRIP (I23): no request/response body-content fields exist in any payload
// by construction. The regression test in content_strip_test.go walks every served
// JSON payload for the forbidden-key set.
//
// CORS (I26): the Origin and Access-Control-Request-Headers are echoed back — a fixed
// allow-list would break Grafana's x-grafana-device-id fetch.
package jsondata

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/rknightion/synthkit/internal/ledger"
)

// Source is the live coupling to the running generator. The runner adapter implements
// this; a fake is used in tests. The wiring pass wires a runner adapter and passes it
// to NewServer.
type Source interface {
	// Blueprints returns the names of all loaded blueprints.
	Blueprints() []string
	// Recent returns ledger requests for the given blueprint active within [now-window, now].
	Recent(blueprint string, now time.Time, window time.Duration) []*ledger.Request
	// WindowStats returns cap-independent aggregates (total minted + distinct workloads) for the
	// given blueprint over [now-window, now]. Count surfaces must read this, NOT len(Recent(...)),
	// which under-reports once the per-blueprint request ring is cap-trimmed at high mint rates.
	WindowStats(blueprint string, now time.Time, window time.Duration) ledger.WindowStats
}

// NewServer builds the Infinity JSON host. All routes are GET-only and side-effect-free.
// The returned handler is the composition root for this package — wrap it with ListenAndServe.
func NewServer(src Source) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", healthz)
	mux.HandleFunc("/blueprints", withMiddleware(blueprintsHandler(src)))
	mux.HandleFunc("/request_correlation_sample", withMiddleware(requestCorrelationHandler(src)))
	mux.HandleFunc("/requests", withMiddleware(requestsHandler(src)))

	// ── acme-ai-platform dashboard suite ────────────────────────────────────────────────────────
	mux.HandleFunc("/acme/request_correlation", withMiddleware(acmeRequestCorrelationHandler(src)))
	mux.HandleFunc("/v1/analytics/groups/metadata", withMiddleware(analyticsMetadataHandler(src)))
	mux.HandleFunc("/v1/analytics/groups/ai-models", withMiddleware(analyticsModelsHandler(src)))
	mux.HandleFunc("/v1/configs", withMiddleware(configsHandler()))
	mux.HandleFunc("/v1/prompts", withMiddleware(promptsHandler()))
	mux.HandleFunc("/api/v1/runs/query", withMiddleware(runsQueryHandler(src)))
	mux.HandleFunc("/api/v1/sessions", withMiddleware(sessionsHandler(src)))
	mux.HandleFunc("/acme/aaef_kpis", withMiddleware(aaefKPIsHandler()))
	mux.HandleFunc("/acme/eval_scorecard", withMiddleware(evalScorecardHandler()))

	mux.HandleFunc("/", withMiddleware(indexHandler()))

	return mux
}

// ── route handlers ───────────────────────────────────────────────────────────────────

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte("ok\n"))
}

func indexHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, map[string]any{
			"service": "synthkit Infinity JSON host",
			"now":     time.Now().UTC().Format(time.RFC3339),
			"routes": []string{
				"GET /",
				"GET /healthz",
				"GET /blueprints",
				"GET /request_correlation_sample",
				"GET /requests?blueprint=<name>&window=<duration>",
				"GET /acme/request_correlation",
				"GET /v1/analytics/groups/metadata",
				"GET /v1/analytics/groups/ai-models",
				"GET /v1/configs",
				"GET /v1/prompts",
				"GET /api/v1/runs/query",
				"GET /api/v1/sessions",
				"GET /acme/aaef_kpis",
				"GET /acme/eval_scorecard",
			},
		})
	}
}

func blueprintsHandler(src Source) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		now := time.Now()
		names := src.Blueprints()
		type row struct {
			Blueprint     string `json:"blueprint"`
			WorkloadCount int    `json:"workload_count"`
		}
		rows := make([]row, 0, len(names))
		for _, bp := range names {
			// Distinct workloads seen in the last hour (cap-independent: the rollup retains every
			// minute's workload set even when the request ring is cap-trimmed).
			st := src.WindowStats(bp, now, time.Hour)
			rows = append(rows, row{Blueprint: bp, WorkloadCount: len(st.Workloads)})
		}
		writeJSON(w, map[string]any{
			"generated_at": now.UTC().Format(time.RFC3339),
			"blueprints":   rows,
		})
	}
}

// maxRequestRows is the row cap for /requests (I26 — side-effect-free, bounded response).
const maxRequestRows = 500

func requestsHandler(src Source) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query()
		bp := q.Get("blueprint")
		window := parseDuration(q.Get("window"), 15*time.Minute)
		now := time.Now()

		reqs := src.Recent(bp, now, window)
		if len(reqs) > maxRequestRows {
			reqs = reqs[len(reqs)-maxRequestRows:]
		}

		type row struct {
			CorrelationID string `json:"correlation_id"`
			TraceID       string `json:"trace_id"`
			SpanID        string `json:"span_id"`
			SessionID     string `json:"session_id"`
			RequestID     string `json:"request_id"`
			Workload      string `json:"workload"`
			Env           string `json:"env"`
			Cluster       string `json:"cluster"`
			Route         string `json:"route"`
			Status        string `json:"status"`
			DurationMs    int64  `json:"duration_ms"`
			Calls         int    `json:"calls"`
		}
		rows := make([]row, 0, len(reqs))
		for _, rq := range reqs {
			rows = append(rows, row{
				CorrelationID: rq.CorrelationID,
				TraceID:       rq.TraceID,
				SpanID:        rq.SpanID,
				SessionID:     rq.SessionID,
				RequestID:     rq.RequestID,
				Workload:      rq.Workload,
				Env:           rq.Env,
				Cluster:       rq.Cluster,
				Route:         rq.Route,
				Status:        rq.Outcome.String(),
				DurationMs:    rq.Duration.Milliseconds(),
				Calls:         len(rq.Calls),
			})
		}
		writeJSON(w, map[string]any{
			"generated_at": now.UTC().Format(time.RFC3339),
			"blueprint":    bp,
			"window":       window.String(),
			"rows":         rows,
		})
	}
}

// requestCorrelationHandler returns the most recent request per blueprint with full correlation
// key-set + identity dims + per-signal "where to find it" hints.
func requestCorrelationHandler(src Source) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		now := time.Now()
		blueprints := src.Blueprints()

		type hint struct {
			Signal string `json:"signal"` // "metrics" | "traces" | "logs"
			Key    string `json:"key"`    // which correlation field to join on
			Where  string `json:"where"`  // human hint (e.g. "Mimir: workload label")
		}
		type sample struct {
			Blueprint     string `json:"blueprint"`
			CorrelationID string `json:"correlation_id"`
			TraceID       string `json:"trace_id"`
			SpanID        string `json:"span_id"`
			SessionID     string `json:"session_id"`
			RequestID     string `json:"request_id"`
			Workload      string `json:"workload"`
			Env           string `json:"env"`
			Cluster       string `json:"cluster"`
			Route         string `json:"route"`
			Status        string `json:"status"`
			DurationMs    int64  `json:"duration_ms"`
			Calls         int    `json:"calls"`
			StartedAt     string `json:"started_at"`
			Hints         []hint `json:"hints"`
		}
		var samples []sample
		for _, bp := range blueprints {
			reqs := src.Recent(bp, now, time.Hour)
			if len(reqs) == 0 {
				continue
			}
			// Most recent request (ring is time-ordered oldest→newest).
			rq := reqs[len(reqs)-1]
			samples = append(samples, sample{
				Blueprint:     bp,
				CorrelationID: rq.CorrelationID,
				TraceID:       rq.TraceID,
				SpanID:        rq.SpanID,
				SessionID:     rq.SessionID,
				RequestID:     rq.RequestID,
				Workload:      rq.Workload,
				Env:           rq.Env,
				Cluster:       rq.Cluster,
				Route:         rq.Route,
				Status:        rq.Outcome.String(),
				DurationMs:    rq.Duration.Milliseconds(),
				Calls:         len(rq.Calls),
				StartedAt:     rq.Start.UTC().Format(time.RFC3339),
				Hints: []hint{
					{"metrics", "workload", "Mimir: workload label → RED series"},
					{"traces", "trace_id", "Tempo: traceID search"},
					{"logs", "correlation_id", "Loki: {correlation_id=...}"},
				},
			})
		}
		writeJSON(w, map[string]any{
			"generated_at": now.UTC().Format(time.RFC3339),
			"samples":      samples,
		})
	}
}

// ── shared helpers ───────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		log.Printf("jsondata: encode error: %v", err)
	}
}

// parseDuration parses a Go duration string (e.g. "15m", "1h"). Returns def on empty/error.
func parseDuration(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return def
	}
	return d
}
