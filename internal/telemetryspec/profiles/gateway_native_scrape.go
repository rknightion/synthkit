// SPDX-License-Identifier: AGPL-3.0-only

package profiles

// gateway_native_scrape: Portkey gateway /metrics scrape profile.
//
// Metric names sourced verbatim from signals/portkey.md [slug: portkey-gateway].
// 14 custom portkey_*/request_count/llm_* metrics + 4 Node.js runtime metrics.
//
// Label schema per signals/portkey.md:
//   Universal (all 14 custom): app, env, method, endpoint, code, provider, source, stream,
//   cacheStatus, model, config_name, api_key_name.
//   ⚠ cacheStatus ∈ {hit,miss,disabled,error} — NO "simple_hit"/"semantic_hit" on metrics.
//   ⚠ llm_token_sum + llm_cost_sum are GAUGEs (cumulative; delta() not rate()).
//   ⚠ request_count is a true Counter.
//
// Bucket arrays taken verbatim from signals/portkey.md where provided; "assumed" per SK-38
// where the signals doc marks them undocumented.

import "github.com/rknightion/synthkit/internal/telemetryspec"

// gnStr is a file-local string pointer helper (unique prefix "gn" for this file).
func gnStr(s string) *string { return &s }

// portkeyUniversalLabels returns the shared label map used on all 14 Portkey custom metrics.
// signals/portkey.md [slug: portkey-gateway]: universal label schema.
func portkeyUniversalLabels() map[string]telemetryspec.ValueModel {
	return map[string]telemetryspec.ValueModel{
		// app: the service/gateway name — constant per scrape target.
		"app": {ConstStr: gnStr("portkey-gateway")},
		// env: deployment environment.
		"env": {Enum: []telemetryspec.EnumEntry{
			{Value: "prod", Weight: 0.6},
			{Value: "staging", Weight: 0.3},
			{Value: "dev", Weight: 0.1},
		}},
		// method: HTTP method on gateway requests.
		"method": {Enum: []telemetryspec.EnumEntry{
			{Value: "POST", Weight: 0.95},
			{Value: "GET", Weight: 0.05},
		}},
		// endpoint: gateway endpoint path.
		"endpoint": {Enum: []telemetryspec.EnumEntry{
			{Value: "/v1/chat/completions", Weight: 0.70},
			{Value: "/v1/completions", Weight: 0.15},
			{Value: "/v1/embeddings", Weight: 0.10},
			{Value: "/v1/agents/invoke", Weight: 0.05},
		}},
		// code: HTTP response status code.
		"code": {Enum: []telemetryspec.EnumEntry{
			{Value: "200", Weight: 0.90},
			{Value: "429", Weight: 0.07},
			{Value: "500", Weight: 0.03},
		}},
		// provider: bare Portkey label (NOT gen_ai_provider_name OTel form).
		// signals/portkey.md: "provider" (bare Portkey label; OTel form is gen_ai_provider_name).
		"provider": {Enum: []telemetryspec.EnumEntry{
			{Value: "openai", Weight: 0.45},
			{Value: "anthropic", Weight: 0.30},
			{Value: "bedrock", Weight: 0.15},
			{Value: "azure-openai", Weight: 0.10},
		}},
		// source: request source identifier.
		"source": {ConstStr: gnStr("api")},
		// stream: whether the request uses streaming.
		"stream": {Enum: []telemetryspec.EnumEntry{
			{Value: "true", Weight: 0.40},
			{Value: "false", Weight: 0.60},
		}},
		// cacheStatus: ∈ {hit,miss,disabled,error} — signals/portkey.md ⚠ no semantic_hit on metrics.
		"cacheStatus": {Enum: []telemetryspec.EnumEntry{
			{Value: "miss", Weight: 0.65},
			{Value: "hit", Weight: 0.20},
			{Value: "disabled", Weight: 0.13},
			{Value: "error", Weight: 0.02},
		}},
		// model: opt-in (PROMETHEUS_INCLUDE_MODEL_LABEL); emitted ON per signals/portkey.md.
		"model": {Enum: []telemetryspec.EnumEntry{
			{Value: "gpt-4o", Weight: 0.35},
			{Value: "claude-3-5-sonnet", Weight: 0.30},
			{Value: "claude-3-7-sonnet", Weight: 0.15},
			{Value: "titan-text-premier", Weight: 0.10},
			{Value: "text-embedding-3-large", Weight: 0.10},
		}},
		// config_name: opt-in gateway config name.
		"config_name": {Enum: []telemetryspec.EnumEntry{
			{Value: "default", Weight: 0.70},
			{Value: "fallback-chain", Weight: 0.30},
		}},
		// api_key_name: opt-in API key name.
		"api_key_name": {Enum: []telemetryspec.EnumEntry{
			{Value: "primary", Weight: 0.80},
			{Value: "secondary", Weight: 0.20},
		}},
	}
}

func init() {
	register(telemetryspec.Profile{
		Name: "gateway_native_scrape",
		Metrics: []telemetryspec.MetricSpec{
			// --- true Counter ---

			// request_count: total gateway request count.
			// signals/portkey.md [slug: portkey-gateway]: {root: request_count, type: counter}.
			{
				Name:       "request_count",
				Instrument: telemetryspec.InstrumentCounter,
				Unit:       "requests",
				Scope:      telemetryspec.ScopeSubstrate,
				Labels:     portkeyUniversalLabels(),
				Value:      telemetryspec.ValueModel{Shape: &telemetryspec.ShapeModel{Base: 10}},
			},

			// --- Histograms ---

			// http_request_duration_seconds: end-to-end HTTP request duration.
			// signals/portkey.md [slug: portkey-gateway]: histogram, buckets verbatim from signals.
			{
				Name:       "http_request_duration_seconds",
				Instrument: telemetryspec.InstrumentHistogram,
				Unit:       "seconds",
				Scope:      telemetryspec.ScopeSubstrate,
				Labels:     portkeyUniversalLabels(),
				Value:      telemetryspec.ValueModel{Normal: &telemetryspec.Normal{Mean: 2.0, Stddev: 1.5}},
				Buckets:    []float64{0.1, 1, 1.5, 2, 3, 5, 7, 10, 15, 20, 30, 45, 60, 90, 120, 240, 500, 1000, 3000},
				LEStyle:    telemetryspec.LEStyleDotZero,
			},

			// llm_request_duration_milliseconds: LLM provider round-trip latency.
			// signals/portkey.md [slug: portkey-gateway]: histogram, buckets verbatim from signals.
			{
				Name:       "llm_request_duration_milliseconds",
				Instrument: telemetryspec.InstrumentHistogram,
				Unit:       "milliseconds",
				Scope:      telemetryspec.ScopeSubstrate,
				Labels:     portkeyUniversalLabels(),
				Value:      telemetryspec.ValueModel{Normal: &telemetryspec.Normal{Mean: 800, Stddev: 600}},
				Buckets:    []float64{0.1, 1, 2, 5, 10, 30, 50, 75, 100, 150, 200, 350, 500, 1000, 2500, 5000, 10000, 50000, 100000, 300000, 500000, 10000000},
				LEStyle:    telemetryspec.LEStyleDotZero,
			},

			// portkey_request_duration_milliseconds: complete gateway processing time.
			// signals/portkey.md [slug: portkey-gateway]: histogram, buckets undocumented (SK-38) → assumed.
			{
				Name:       "portkey_request_duration_milliseconds",
				Instrument: telemetryspec.InstrumentHistogram,
				Unit:       "milliseconds",
				Scope:      telemetryspec.ScopeSubstrate,
				Labels:     portkeyUniversalLabels(),
				Value:      telemetryspec.ValueModel{Normal: &telemetryspec.Normal{Mean: 900, Stddev: 700}},
				// ASSUMED buckets (SK-38) — signals doc marks them undocumented.
				Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000},
				LEStyle: telemetryspec.LEStyleDotZero,
			},

			// portkey_processing_time_excluding_last_byte_ms: gateway overhead excl. streaming final byte.
			// signals/portkey.md [slug: portkey-gateway]: v: ok, buckets not specified → assumed.
			{
				Name:       "portkey_processing_time_excluding_last_byte_ms",
				Instrument: telemetryspec.InstrumentHistogram,
				Unit:       "milliseconds",
				Scope:      telemetryspec.ScopeSubstrate,
				Labels:     portkeyUniversalLabels(),
				Value:      telemetryspec.ValueModel{Normal: &telemetryspec.Normal{Mean: 50, Stddev: 30}},
				// ASSUMED buckets (no documented bucket set in signals/portkey.md).
				Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000},
				LEStyle: telemetryspec.LEStyleDotZero,
			},

			// llm_last_byte_diff_duration_milliseconds: first→final byte gap; TTFT proxy.
			// signals/portkey.md [slug: portkey-gateway]: v: ok, buckets not specified → assumed.
			{
				Name:       "llm_last_byte_diff_duration_milliseconds",
				Instrument: telemetryspec.InstrumentHistogram,
				Unit:       "milliseconds",
				Scope:      telemetryspec.ScopeSubstrate,
				Labels:     portkeyUniversalLabels(),
				Value:      telemetryspec.ValueModel{Normal: &telemetryspec.Normal{Mean: 400, Stddev: 300}},
				// ASSUMED buckets.
				Buckets: []float64{10, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000},
				LEStyle: telemetryspec.LEStyleDotZero,
			},

			// authentication_duration_milliseconds: auth check latency.
			// signals/portkey.md [slug: portkey-gateway]: v: ok, buckets not specified → assumed.
			{
				Name:       "authentication_duration_milliseconds",
				Instrument: telemetryspec.InstrumentHistogram,
				Unit:       "milliseconds",
				Scope:      telemetryspec.ScopeSubstrate,
				Labels:     portkeyUniversalLabels(),
				Value:      telemetryspec.ValueModel{Normal: &telemetryspec.Normal{Mean: 5, Stddev: 3}},
				// ASSUMED buckets.
				Buckets: []float64{0.5, 1, 2, 5, 10, 25, 50, 100, 250, 500},
				LEStyle: telemetryspec.LEStyleDotZero,
			},

			// api_key_rate_limit_check_duration_milliseconds: rate-limit check latency.
			// signals/portkey.md [slug: portkey-gateway]: v: ok, buckets not specified → assumed.
			{
				Name:       "api_key_rate_limit_check_duration_milliseconds",
				Instrument: telemetryspec.InstrumentHistogram,
				Unit:       "milliseconds",
				Scope:      telemetryspec.ScopeSubstrate,
				Labels:     portkeyUniversalLabels(),
				Value:      telemetryspec.ValueModel{Normal: &telemetryspec.Normal{Mean: 3, Stddev: 2}},
				// ASSUMED buckets.
				Buckets: []float64{0.5, 1, 2, 5, 10, 25, 50, 100, 250, 500},
				LEStyle: telemetryspec.LEStyleDotZero,
			},

			// pre_request_processing_duration_milliseconds: pre-request middleware latency.
			// signals/portkey.md [slug: portkey-gateway]: v: ok, buckets not specified → assumed.
			{
				Name:       "pre_request_processing_duration_milliseconds",
				Instrument: telemetryspec.InstrumentHistogram,
				Unit:       "milliseconds",
				Scope:      telemetryspec.ScopeSubstrate,
				Labels:     portkeyUniversalLabels(),
				Value:      telemetryspec.ValueModel{Normal: &telemetryspec.Normal{Mean: 8, Stddev: 5}},
				// ASSUMED buckets.
				Buckets: []float64{0.5, 1, 2, 5, 10, 25, 50, 100, 250, 500},
				LEStyle: telemetryspec.LEStyleDotZero,
			},

			// post_request_processing_duration_milliseconds: post-request middleware latency.
			// signals/portkey.md [slug: portkey-gateway]: v: ok, buckets not specified → assumed.
			{
				Name:       "post_request_processing_duration_milliseconds",
				Instrument: telemetryspec.InstrumentHistogram,
				Unit:       "milliseconds",
				Scope:      telemetryspec.ScopeSubstrate,
				Labels:     portkeyUniversalLabels(),
				Value:      telemetryspec.ValueModel{Normal: &telemetryspec.Normal{Mean: 6, Stddev: 4}},
				// ASSUMED buckets.
				Buckets: []float64{0.5, 1, 2, 5, 10, 25, 50, 100, 250, 500},
				LEStyle: telemetryspec.LEStyleDotZero,
			},

			// llm_cache_processing_duration_milliseconds: cache lookup/store latency.
			// signals/portkey.md [slug: portkey-gateway]: v: ok, buckets not specified → assumed.
			{
				Name:       "llm_cache_processing_duration_milliseconds",
				Instrument: telemetryspec.InstrumentHistogram,
				Unit:       "milliseconds",
				Scope:      telemetryspec.ScopeSubstrate,
				Labels:     portkeyUniversalLabels(),
				Value:      telemetryspec.ValueModel{Normal: &telemetryspec.Normal{Mean: 12, Stddev: 8}},
				// ASSUMED buckets.
				Buckets: []float64{0.5, 1, 2, 5, 10, 25, 50, 100, 250, 500, 1000},
				LEStyle: telemetryspec.LEStyleDotZero,
			},

			// grpc_req_conversion_duration_milliseconds: gRPC↔HTTP conversion latency.
			// signals/portkey.md [slug: portkey-gateway]: buckets verbatim from signals.
			{
				Name:       "grpc_req_conversion_duration_milliseconds",
				Instrument: telemetryspec.InstrumentHistogram,
				Unit:       "milliseconds",
				Scope:      telemetryspec.ScopeSubstrate,
				Labels:     portkeyUniversalLabels(),
				Value:      telemetryspec.ValueModel{Normal: &telemetryspec.Normal{Mean: 2, Stddev: 1}},
				Buckets:    []float64{0.01, 0.1, 0.5, 1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000},
				LEStyle:    telemetryspec.LEStyleDotZero,
			},

			// --- GAUGEs (cumulative — delta() not rate()) ---

			// llm_token_sum: cumulative token count GAUGE.
			// signals/portkey.md [slug: portkey-gateway]: ⚠ GAUGE (cumulative; delta() not rate()).
			{
				Name:       "llm_token_sum",
				Instrument: telemetryspec.InstrumentGauge,
				Unit:       "tokens",
				Scope:      telemetryspec.ScopeSubstrate,
				Labels:     portkeyUniversalLabels(),
				Value:      telemetryspec.ValueModel{Shape: &telemetryspec.ShapeModel{Base: 50000}},
			},

			// llm_cost_sum: cumulative USD cost GAUGE.
			// signals/portkey.md [slug: portkey-gateway]: ⚠ GAUGE (cumulative; delta() not rate()).
			{
				Name:       "llm_cost_sum",
				Instrument: telemetryspec.InstrumentGauge,
				Unit:       "usd",
				Scope:      telemetryspec.ScopeSubstrate,
				Labels:     portkeyUniversalLabels(),
				Value:      telemetryspec.ValueModel{Shape: &telemetryspec.ShapeModel{Base: 0.10}},
			},

			// --- Node.js runtime metrics (plausible subset, signals/portkey.md) ---

			// node_process_cpu_user_seconds_total: CPU user time counter.
			// signals/portkey.md: "node_process_cpu_user_seconds_total (counter)".
			{
				Name:       "node_process_cpu_user_seconds_total",
				Instrument: telemetryspec.InstrumentCounter,
				Unit:       "seconds",
				Scope:      telemetryspec.ScopeSubstrate,
				Labels: map[string]telemetryspec.ValueModel{
					"app": {ConstStr: gnStr("portkey-gateway")},
					"env": {Enum: []telemetryspec.EnumEntry{
						{Value: "prod", Weight: 0.6},
						{Value: "staging", Weight: 0.3},
						{Value: "dev", Weight: 0.1},
					}},
				},
				Value: telemetryspec.ValueModel{Shape: &telemetryspec.ShapeModel{Base: 0.05}},
			},

			// node_process_resident_memory_bytes: process RSS gauge.
			// signals/portkey.md: "node_process_resident_memory_bytes (gauge)".
			{
				Name:       "node_process_resident_memory_bytes",
				Instrument: telemetryspec.InstrumentGauge,
				Unit:       "bytes",
				Scope:      telemetryspec.ScopeSubstrate,
				Labels: map[string]telemetryspec.ValueModel{
					"app": {ConstStr: gnStr("portkey-gateway")},
					"env": {Enum: []telemetryspec.EnumEntry{
						{Value: "prod", Weight: 0.6},
						{Value: "staging", Weight: 0.3},
						{Value: "dev", Weight: 0.1},
					}},
				},
				Value: telemetryspec.ValueModel{Normal: &telemetryspec.Normal{Mean: 256 * 1024 * 1024, Stddev: 32 * 1024 * 1024}},
			},

			// node_eventloop_lag_seconds: Node.js event-loop lag gauge.
			// signals/portkey.md: "node_eventloop_lag_seconds (gauge)".
			{
				Name:       "node_eventloop_lag_seconds",
				Instrument: telemetryspec.InstrumentGauge,
				Unit:       "seconds",
				Scope:      telemetryspec.ScopeSubstrate,
				Labels: map[string]telemetryspec.ValueModel{
					"app": {ConstStr: gnStr("portkey-gateway")},
					"env": {Enum: []telemetryspec.EnumEntry{
						{Value: "prod", Weight: 0.6},
						{Value: "staging", Weight: 0.3},
						{Value: "dev", Weight: 0.1},
					}},
				},
				Value: telemetryspec.ValueModel{FloatRange: &telemetryspec.FloatRange{Min: 0.0001, Max: 0.05}},
			},

			// node_gc_duration_seconds: GC pause histogram.
			// signals/portkey.md: "node_gc_duration_seconds (histogram)".
			{
				Name:       "node_gc_duration_seconds",
				Instrument: telemetryspec.InstrumentHistogram,
				Unit:       "seconds",
				Scope:      telemetryspec.ScopeSubstrate,
				Labels: map[string]telemetryspec.ValueModel{
					"app": {ConstStr: gnStr("portkey-gateway")},
					"env": {Enum: []telemetryspec.EnumEntry{
						{Value: "prod", Weight: 0.6},
						{Value: "staging", Weight: 0.3},
						{Value: "dev", Weight: 0.1},
					}},
					// gc_type: GC kind. PENDING: cantfind SK-64 — unverified label key (prom-client's
					// default nodejs_gc_duration_seconds uses `kind`; `gc_type` is unconfirmed).
					"gc_type": {Enum: []telemetryspec.EnumEntry{
						{Value: "minor", Weight: 0.80},
						{Value: "major", Weight: 0.20},
					}},
				},
				Value: telemetryspec.ValueModel{Normal: &telemetryspec.Normal{Mean: 0.005, Stddev: 0.003}},
				// ASSUMED buckets for Node GC pauses (ms-range).
				Buckets: []float64{0.001, 0.002, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
				LEStyle: telemetryspec.LEStyleDotZero,
			},
		},
	})
}
