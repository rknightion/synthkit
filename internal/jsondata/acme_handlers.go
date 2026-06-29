// SPDX-License-Identifier: AGPL-3.0-only

// acme_handlers.go — Infinity mock-API HTTP routes for the acme-ai-platform dashboard suite.
//
// Routes (all GET, side-effect-free — invariant I26):
//
//	GET /acme/golden_thread              — request/correlation_keys/hops for one sampled request
//	GET /v1/analytics/groups/metadata     — per-use_case analytics rows
//	GET /v1/analytics/groups/ai-models    — per-model analytics rows
//	GET /v1/configs                       — Portkey provider/model catalog
//	GET /v1/prompts                       — Portkey prompt registry
//	GET /api/v1/runs/query                — LangSmith eval runs (GET; real API is POST)
//	GET /api/v1/sessions                  — LangSmith projects with stats
//	GET /acme/aaef_kpis                   — AAEF executive KPIs
//	GET /acme/eval_scorecard              — per-gate eval scorecard
//
// Data sourcing: use_cases/models/providers/envs are the FIXED vocabulary from acme-ai-platform.yaml.
// Correlation keys for /acme/golden_thread are drawn from the most-recent Source request, exactly
// as goldenThreadHandler does for /golden_thread_sample.
//
// CONTENT STRIP (I23): no prompt/completion/message body fields anywhere.
package jsondata

import (
	"fmt"
	"net/http"
	"time"

	"github.com/rknightion/synthkit/internal/semconv"
)

// ── acme vocabulary (sourced from blueprints/acme-ai-platform.yaml) ──────────────────────────────

var (
	acmeUseCases = []string{"document_extraction", "code_assistant", "research_summarization", "customer_support"}
	acmeModels   = []string{"gpt-4o", "gpt-4.1", "claude-3.5-sonnet"}
	acmeEnvs     = []string{"PRD", "TST1", "TST2", "TRN", "DEV1", "DEV2", "BVE"}
	acmeProjects = []string{"contentgen-agents", "data-processing", "platform-assistant", "doc-intelligence"}

	// providerCatalog: Portkey virtual-key entries (slug, provider, region, status).
	// Providers: azure-openai, bedrock + gcp-vertex as a third common provider.
	providerCatalog = []struct {
		slug, provider, region, status string
	}{
		{"vk-azure-eu", "azure-openai", "eu-central-1", "healthy"},
		{"vk-bedrock-eu", "bedrock", "eu-central-1", "healthy"},
		{"vk-gcp-eu", "gcp-vertex", "europe-west3", "healthy"},
		{"vk-azure-us", "azure-openai", "us-east-1", "healthy"},
		{"vk-bedrock-us", "bedrock", "us-east-1", "degraded"},
	}

	// promptRegistry: slug→version→env mapping.
	promptRegistry = []struct {
		slug, status, envLabel string
		version                int
		lastApproved           string
	}{
		{"assist-v3", "active", "PRD", 3, "2026-05-28T10:00:00Z"},
		{"extract-v2", "active", "PRD", 2, "2026-05-15T09:30:00Z"},
		{"summarize-v1", "active", "PRD", 1, "2026-04-01T08:00:00Z"},
		{"codegen-v4", "active", "PRD", 4, "2026-06-01T11:00:00Z"},
		{"assist-v3-tst", "draft", "TST1", 3, "2026-06-10T14:00:00Z"},
		{"extract-v3-tst", "draft", "TST1", 3, "2026-06-12T09:00:00Z"},
	}

	// aaefContexts mirrors the use_case × period dimensions for AAEF KPIs.
	aaefContexts = []string{"document-intelligence", "code-generation", "research-assistant"}
	aaefPeriods  = []string{"Q2-2026", "Q1-2026"}

	// evalGates: the gates that appear in the scorecard.
	evalGates = []struct {
		gate, metric, op string
		threshold        float64
	}{
		{"faithfulness", "faithfulness_ratio", ">=", 0.85},
		{"completeness", "completeness_ratio", ">=", 0.995},
		{"env_consistency", "env_consistency_ratio", ">=", 1.0},
		{"schema_validity", "schema_validity_ratio", ">=", 0.995},
		{"passthrough_exactness", "passthrough_exactness_ratio", ">=", 0.999},
	}
)

// ── /acme/golden_thread ──────────────────────────────────────────────────────────────────────────

// acmeGoldenThreadHandler returns ONE response object with three root keys consumed by three
// separate Infinity table panels (root_selector=request, root_selector=correlation_keys,
// root_selector=hops). Data sourced from the most-recent ledger request (same as goldenThreadHandler),
// or synthesised from vocabulary when no requests are available yet.
func acmeGoldenThreadHandler(src Source) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		now := time.Now()

		// Find the most-recent request across all blueprints (first blueprint with data wins).
		var (
			correlationID  = "00000000-0000-4000-8000-000000000000"
			traceID        = "00000000000000000000000000000001"
			spanID         = "0000000000000001"
			portKeyTraceID = "00000000-0000-4000-8000-000000000001"
			model          = acmeModels[0]
			provider       = "azure-openai"
			env            = acmeEnvs[0]
			startedAt      = now.Add(-2 * time.Minute).UTC().Format(time.RFC3339)
		)
		for _, bp := range src.Blueprints() {
			reqs := src.Recent(bp, now, time.Hour)
			if len(reqs) == 0 {
				continue
			}
			rq := reqs[len(reqs)-1]
			correlationID = rq.CorrelationID
			traceID = rq.TraceID
			spanID = rq.SpanID
			portKeyTraceID = rq.PortkeyTraceID
			env = rq.Env
			if rq.Model != "" {
				model = rq.Model
			}
			if rq.Provider != "" {
				provider = rq.Provider
			}
			startedAt = rq.Start.UTC().Format(time.RFC3339)
			break
		}

		// root "request": one-element array — the Infinity root_selector="request" table reads it.
		request := []map[string]any{
			{
				"use_case":   acmeUseCases[0],
				"context":    "document-intelligence",
				"env":        env,
				"model":      model,
				"provider":   provider,
				"started_at": startedAt,
			},
		}

		// root "correlation_keys": one-element array — the key-set table reads it.
		correlationKeys := []map[string]any{
			{
				"correlation_id":       correlationID,
				"trace_id":             traceID,
				"portkey_trace_id":     portKeyTraceID,
				"span_id":              spanID,
				semconv.KeyTraceparent: "00-" + traceID + "-" + spanID + "-01",
			},
		}

		// root "hops": the 10-hop journey table.
		// Hop topology: browser → frontend → backend → workflow → agent → llm_gateway →
		//               bedrock → eval_log → export_log → langsmith
		// span_id values use the real spanID for hop-1; synthetic child IDs after.
		hops := []map[string]any{
			{
				"hop": 1, "service": "browser", "span_name": "user-action/POST /v1/assist",
				"signal": "RUM", "span_id": spanID[:8] + "0001",
				"parent_span_id": "", "keys": correlationID,
			},
			{
				"hop": 2, "service": "acme-frontend", "span_name": "POST /v1/assist",
				"signal": "trace", "span_id": spanID[:8] + "0002",
				"parent_span_id": spanID[:8] + "0001", "keys": traceID,
			},
			{
				"hop": 3, "service": "acme-backend", "span_name": "POST /v1/assist",
				"signal": "trace+logs+metrics", "span_id": spanID,
				"parent_span_id": spanID[:8] + "0002", "keys": correlationID + "," + traceID,
			},
			{
				"hop": 4, "service": "acme-backend/langgraph", "span_name": "workflow.run",
				"signal": "trace", "span_id": spanID[:8] + "0004",
				"parent_span_id": spanID, "keys": traceID,
			},
			{
				"hop": 5, "service": "acme-backend/agent", "span_name": "agent.invoke",
				"signal": "trace", "span_id": spanID[:8] + "0005",
				"parent_span_id": spanID[:8] + "0004", "keys": traceID,
			},
			{
				"hop": 6, "service": "portkey-gateway", "span_name": "llm.request/" + model,
				"signal": "trace", "span_id": spanID[:8] + "0006",
				"parent_span_id": spanID[:8] + "0005", "keys": portKeyTraceID,
			},
			{
				"hop": 7, "service": "bedrock/" + model, "span_name": "bedrock.invoke",
				"signal": "trace+CW-metrics", "span_id": spanID[:8] + "0007",
				"parent_span_id": spanID[:8] + "0006", "keys": traceID,
			},
			{
				"hop": 8, "service": "portkey-export-log", "span_name": "(e) 2b-export batch",
				"signal": "logs", "span_id": "",
				"parent_span_id": "", "keys": portKeyTraceID,
			},
			{
				"hop": 9, "service": "langsmith-runindex", "span_name": "(f-ii) run-index poll",
				"signal": "logs", "span_id": "",
				"parent_span_id": "", "keys": portKeyTraceID + "," + correlationID,
			},
			{
				"hop": 10, "service": "langsmith", "span_name": "eval-run",
				"signal": "Infinity/eval", "span_id": "",
				"parent_span_id": "", "keys": correlationID,
			},
		}

		writeJSON(w, map[string]any{
			"generated_at":     now.UTC().Format(time.RFC3339),
			"request":          request,
			"correlation_keys": correlationKeys,
			"hops":             hops,
		})
	}
}

// ── /v1/analytics/groups/metadata ────────────────────────────────────────────────────────────────

// analyticsMetadataHandler returns per-use_case analytics aggregates.
// Root selector "data"; columns: group_key, requests, cost, total_tokens, latency_p99,
// error_rate, cache_hit_rate, rescued_requests.
func analyticsMetadataHandler(src Source) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Derive per-use_case volumes from live request counts (1h window, all blueprints).
		useCaseBase := analyticsBaseFromSource(src)

		type row struct {
			GroupKey        string  `json:"group_key"`
			Requests        int     `json:"requests"`
			Cost            float64 `json:"cost"`
			TotalTokens     int     `json:"total_tokens"`
			LatencyP99      float64 `json:"latency_p99"`
			ErrorRate       float64 `json:"error_rate"`
			CacheHitRate    float64 `json:"cache_hit_rate"`
			RescuedRequests int     `json:"rescued_requests"`
		}

		// Per-use_case statistics. Weights spread traffic realistically.
		ucWeights := []float64{0.45, 0.25, 0.20, 0.10}
		rows := make([]row, len(acmeUseCases))
		for i, uc := range acmeUseCases {
			vol := int(float64(useCaseBase) * ucWeights[i])
			if vol < 5 {
				vol = 5 + i*3
			}
			rows[i] = row{
				GroupKey:        uc,
				Requests:        vol,
				Cost:            roundF(0.0022*float64(vol) + float64(i)*0.15),
				TotalTokens:     vol * (2800 + i*400),
				LatencyP99:      roundF(1.8 + float64(i)*0.35),
				ErrorRate:       roundF(0.012 + float64(i)*0.004),
				CacheHitRate:    roundF(0.42 - float64(i)*0.05),
				RescuedRequests: i * 2,
			}
		}

		writeJSON(w, map[string]any{
			"generated_at": time.Now().UTC().Format(time.RFC3339),
			"data":         rows,
		})
	}
}

// ── /v1/analytics/groups/ai-models ───────────────────────────────────────────────────────────────

// analyticsModelsHandler returns per-model analytics aggregates.
// Same shape as analyticsMetadataHandler; group_key is the model name.
func analyticsModelsHandler(src Source) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		useCaseBase := analyticsBaseFromSource(src)

		type row struct {
			GroupKey        string  `json:"group_key"`
			Requests        int     `json:"requests"`
			Cost            float64 `json:"cost"`
			TotalTokens     int     `json:"total_tokens"`
			LatencyP99      float64 `json:"latency_p99"`
			ErrorRate       float64 `json:"error_rate"`
			CacheHitRate    float64 `json:"cache_hit_rate"`
			RescuedRequests int     `json:"rescued_requests"`
		}

		mWeights := []float64{0.50, 0.35, 0.15}
		rows := make([]row, len(acmeModels))
		for i, m := range acmeModels {
			vol := int(float64(useCaseBase) * mWeights[i])
			if vol < 5 {
				vol = 5 + i*2
			}
			// gpt-4.1 costs less per token; claude-3.5-sonnet is more expensive.
			costPerReq := []float64{0.0031, 0.0018, 0.0058}[i]
			rows[i] = row{
				GroupKey:        m,
				Requests:        vol,
				Cost:            roundF(costPerReq * float64(vol)),
				TotalTokens:     vol * (3200 - i*200),
				LatencyP99:      roundF(2.1 + float64(i)*0.5),
				ErrorRate:       roundF(0.011 + float64(i)*0.003),
				CacheHitRate:    roundF(0.38 + float64(i)*0.06),
				RescuedRequests: i,
			}
		}

		writeJSON(w, map[string]any{
			"generated_at": time.Now().UTC().Format(time.RFC3339),
			"data":         rows,
		})
	}
}

// ── /v1/configs ───────────────────────────────────────────────────────────────────────────────────

// configsHandler returns the Portkey virtual-key / provider catalog.
// Root selector "providers"; columns: slug, provider, region, status.
func configsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		type row struct {
			Slug     string `json:"slug"`
			Provider string `json:"provider"`
			Region   string `json:"region"`
			Status   string `json:"status"`
		}
		rows := make([]row, len(providerCatalog))
		for i, p := range providerCatalog {
			rows[i] = row{Slug: p.slug, Provider: p.provider, Region: p.region, Status: p.status}
		}
		writeJSON(w, map[string]any{
			"generated_at": time.Now().UTC().Format(time.RFC3339),
			"providers":    rows,
		})
	}
}

// ── /v1/prompts ───────────────────────────────────────────────────────────────────────────────────

// promptsHandler returns the Portkey prompt registry.
// Root selector "data"; columns: slug, version, status, env_label, last_approved.
func promptsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		type row struct {
			Slug         string `json:"slug"`
			Version      int    `json:"version"`
			Status       string `json:"status"`
			EnvLabel     string `json:"env_label"`
			LastApproved string `json:"last_approved"`
		}
		rows := make([]row, len(promptRegistry))
		for i, p := range promptRegistry {
			rows[i] = row{
				Slug:         p.slug,
				Version:      p.version,
				Status:       p.status,
				EnvLabel:     p.envLabel,
				LastApproved: p.lastApproved,
			}
		}
		writeJSON(w, map[string]any{
			"generated_at": time.Now().UTC().Format(time.RFC3339),
			"data":         rows,
		})
	}
}

// ── /api/v1/runs/query ────────────────────────────────────────────────────────────────────────────

// runsQueryHandler returns LangSmith eval runs. The real API uses POST with a JSON select body;
// the Infinity datasource targets are GET-only so this route accepts GET (I26).
// Root selector "runs"; nested extra.metadata.* and feedback_stats.*.avg fields are real JSON
// objects (not dot-separated strings) — Infinity walks nested objects.
func runsQueryHandler(src Source) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		now := time.Now()

		// Pull the most-recent correlation IDs to make the run rows joinable to real trace data.
		var correlationIDs []string
		var traceIDs []string
		var portKeyTraceIDs []string
		for _, bp := range src.Blueprints() {
			reqs := src.Recent(bp, now, time.Hour)
			for _, rq := range reqs {
				correlationIDs = append(correlationIDs, rq.CorrelationID)
				traceIDs = append(traceIDs, rq.TraceID)
				portKeyTraceIDs = append(portKeyTraceIDs, rq.PortkeyTraceID)
				if len(correlationIDs) >= 8 {
					break
				}
			}
			if len(correlationIDs) >= 8 {
				break
			}
		}
		// Pad with deterministic synthetic IDs if Source has no requests yet.
		for len(correlationIDs) < 8 {
			n := len(correlationIDs)
			correlationIDs = append(correlationIDs, fmt.Sprintf("synth-corr-%04d", n))
			traceIDs = append(traceIDs, fmt.Sprintf("synth-trace-%032d", n))
			portKeyTraceIDs = append(portKeyTraceIDs, fmt.Sprintf("synth-ptk-%04d", n))
		}

		type runMetadata struct {
			OtelTraceID    string `json:"otel_trace_id"`
			CorrelationID  string `json:"correlation_id"`
			PortkeyTraceID string `json:"portkey_trace_id"`
			UseCase        string `json:"use_case"`
		}
		type runExtra struct {
			Metadata runMetadata `json:"metadata"`
		}
		type feedbackScore struct {
			Avg float64 `json:"avg"`
		}
		type feedbackStats struct {
			Faithfulness feedbackScore `json:"faithfulness"`
			Completeness feedbackScore `json:"completeness"`
		}
		type run struct {
			ID            string        `json:"id"`
			Name          string        `json:"name"`
			RunType       string        `json:"run_type"`
			Status        string        `json:"status"`
			StartTime     string        `json:"start_time"`
			TotalTokens   int           `json:"total_tokens"`
			TotalCost     string        `json:"total_cost"`
			Extra         runExtra      `json:"extra"`
			FeedbackStats feedbackStats `json:"feedback_stats"`
		}

		runTypes := []string{"chain", "llm", "tool", "chain", "llm", "chain", "llm", "chain"}
		projects := acmeProjects
		runs := make([]run, 8)
		for i := range runs {
			faithfulness := roundF(0.91 + float64(i%3)*0.02)
			completeness := roundF(0.996 + float64(i%2)*0.001)
			runs[i] = run{
				ID:          fmt.Sprintf("run-%04d", i+1),
				Name:        projects[i%len(projects)],
				RunType:     runTypes[i],
				Status:      pickStatus(i),
				StartTime:   now.Add(-time.Duration(i+1) * 8 * time.Minute).UTC().Format(time.RFC3339),
				TotalTokens: 2400 + i*350,
				TotalCost:   fmt.Sprintf("$%.4f", 0.0045+float64(i)*0.0008),
				Extra: runExtra{Metadata: runMetadata{
					OtelTraceID:    traceIDs[i],
					CorrelationID:  correlationIDs[i],
					PortkeyTraceID: portKeyTraceIDs[i],
					UseCase:        acmeUseCases[i%len(acmeUseCases)],
				}},
				FeedbackStats: feedbackStats{
					Faithfulness: feedbackScore{Avg: faithfulness},
					Completeness: feedbackScore{Avg: completeness},
				},
			}
		}

		writeJSON(w, map[string]any{
			"generated_at": now.UTC().Format(time.RFC3339),
			"runs":         runs,
		})
	}
}

// ── /api/v1/sessions ─────────────────────────────────────────────────────────────────────────────

// sessionsHandler returns LangSmith projects with aggregated stats.
// Root selector "sessions"; nested stats.* and stats.feedback_stats.faithfulness.avg.
func sessionsHandler(src Source) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		now := time.Now()

		// Base volume from live requests (approximation: total requests in last hour).
		base := analyticsBaseFromSource(src)
		if base < 20 {
			base = 20
		}

		type feedbackScore struct {
			Avg float64 `json:"avg"`
		}
		type sessionFeedback struct {
			Faithfulness feedbackScore `json:"faithfulness"`
		}
		type stats struct {
			RunCount      int             `json:"run_count"`
			LatencyP50    float64         `json:"latency_p50"`
			LatencyP99    float64         `json:"latency_p99"`
			ErrorRate     float64         `json:"error_rate"`
			TotalTokens   int             `json:"total_tokens"`
			TotalCost     string          `json:"total_cost"`
			FeedbackStats sessionFeedback `json:"feedback_stats"`
		}
		type session struct {
			Name  string `json:"name"`
			Stats stats  `json:"stats"`
		}

		sessions := make([]session, len(acmeProjects))
		for i, proj := range acmeProjects {
			vol := base/len(acmeProjects) + i*3
			sessions[i] = session{
				Name: proj,
				Stats: stats{
					RunCount:    vol,
					LatencyP50:  roundF(800 + float64(i)*120),
					LatencyP99:  roundF(2100 + float64(i)*300),
					ErrorRate:   roundF(0.013 + float64(i)*0.004),
					TotalTokens: vol * (2600 + i*200),
					TotalCost:   fmt.Sprintf("$%.2f", 0.08*float64(vol)+float64(i)*0.12),
					FeedbackStats: sessionFeedback{
						Faithfulness: feedbackScore{Avg: roundF(0.92 + float64(i)*0.01)},
					},
				},
			}
		}

		writeJSON(w, map[string]any{
			"generated_at": now.UTC().Format(time.RFC3339),
			"sessions":     sessions,
		})
	}
}

// ── /acme/aaef_kpis ──────────────────────────────────────────────────────────────────────────────

// aaefKPIsHandler returns AAEF executive KPI scores by context × period.
// Root selector "kpis"; columns: context, period, tue_score, mcr_score, spi_score, css_score.
func aaefKPIsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		type kpiRow struct {
			Context  string  `json:"context"`
			Period   string  `json:"period"`
			TUEScore float64 `json:"tue_score"`
			MCRScore float64 `json:"mcr_score"`
			SPIScore float64 `json:"spi_score"`
			CSSScore float64 `json:"css_score"`
		}

		var rows []kpiRow
		// Base scores for Q2 and slight degradation for Q1.
		baseScores := [][4]float64{
			{0.96, 0.94, 0.91, 0.89}, // document-intelligence
			{0.93, 0.91, 0.88, 0.87}, // code-generation
			{0.90, 0.88, 0.86, 0.85}, // research-assistant
		}
		qOffset := [2]float64{0.0, -0.015} // Q2, Q1
		for i, ctx := range aaefContexts {
			for j, period := range aaefPeriods {
				s := baseScores[i]
				d := qOffset[j]
				rows = append(rows, kpiRow{
					Context:  ctx,
					Period:   period,
					TUEScore: roundF(s[0] + d),
					MCRScore: roundF(s[1] + d),
					SPIScore: roundF(s[2] + d),
					CSSScore: roundF(s[3] + d),
				})
			}
		}

		writeJSON(w, map[string]any{
			"generated_at": time.Now().UTC().Format(time.RFC3339),
			"kpis":         rows,
		})
	}
}

// ── /acme/eval_scorecard ─────────────────────────────────────────────────────────────────────────

// evalScorecardHandler returns per-project × gate scorecard rows.
// Root selector "scorecard"; columns: project, env, use_case, value, op, threshold, gate, status, metric.
func evalScorecardHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		type scorecardRow struct {
			Project   string  `json:"project"`
			Env       string  `json:"env"`
			UseCase   string  `json:"use_case"`
			Value     float64 `json:"value"`
			Op        string  `json:"op"`
			Threshold float64 `json:"threshold"`
			Gate      string  `json:"gate"`
			Status    string  `json:"status"`
			Metric    string  `json:"metric"`
		}

		// Two envs × 4 projects × 5 gates = 40 rows (capped to keep the table useful).
		envs := []string{"PRD", "TST1"}
		var rows []scorecardRow
		for _, proj := range acmeProjects {
			uc := acmeUseCases[len(rows)/10%len(acmeUseCases)]
			for _, env := range envs {
				for k, gate := range evalGates {
					// Slightly degrade TST1 to show some FAIL rows.
					val := gate.threshold + 0.02 - float64(k)*0.005
					if env == "TST1" {
						val -= 0.025
					}
					status := "pass"
					if val < gate.threshold {
						status = "fail"
					}
					rows = append(rows, scorecardRow{
						Project:   proj,
						Env:       env,
						UseCase:   uc,
						Value:     roundF(val),
						Op:        gate.op,
						Threshold: gate.threshold,
						Gate:      gate.gate,
						Status:    status,
						Metric:    gate.metric,
					})
				}
			}
		}

		writeJSON(w, map[string]any{
			"generated_at": time.Now().UTC().Format(time.RFC3339),
			"scorecard":    rows,
		})
	}
}

// ── shared helpers ────────────────────────────────────────────────────────────────────────────────

// analyticsBaseFromSource returns a rough total request count across all blueprints in the last
// hour, used to scale analytics row volumes to live traffic. Falls back to 120 when no data.
func analyticsBaseFromSource(src Source) int {
	now := time.Now()
	total := 0
	for _, bp := range src.Blueprints() {
		total += src.WindowStats(bp, now, time.Hour).Count
	}
	if total == 0 {
		return 120
	}
	return total
}

// roundF rounds to 3 decimal places.
func roundF(v float64) float64 {
	return float64(int(v*1000+0.5)) / 1000
}

// pickStatus returns "success" for most rows, "error" for occasional ones (realistic ~3%).
func pickStatus(i int) string {
	if i%17 == 0 {
		return "error"
	}
	return "success"
}
