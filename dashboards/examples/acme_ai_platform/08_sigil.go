// SPDX-License-Identifier: AGPL-3.0-only

// Sigil is the Acme AI GenAI Semantics dashboard (acme_ai_platform blueprint, predecessor 08-sigil).
//
// "What you get from semconv conformance alone — BEFORE the proprietary Generation-Ingest SDK."
// Standard gen_ai.* OTLP spans (semconv v1.41.1) light up token economics, latency, TTFC/streaming
// metrics (Mimir) and the Tempo GenAI trace feed.
//
// Three tabs mirror the predecessor layout:
//   - Overview: KPI stats (token throughput, req rate, p95 duration, p95 TTFC, avg tok/req) +
//     token throughput by type + operation duration p50/p95/p99 + token by model.
//     NOTE: the streaming row carries the TTFC+chunk panel (panel-21) with both chunk histograms.
//   - By provider: provider/model breakdown (token rate, req rate, p95 per provider) +
//     req rate by provider + req rate by model (top 8) + tokens-per-request distribution +
//     per-model token & latency summary tables.
//   - Traces: GenAI OTLP trace feed — chat spans from Tempo (TraceQL tableType:traces).
//
// Scope: gen_ai_client_* is APP-scoped (carries blueprint + deployment_environment + service).
// Variables pin blueprint="$scenario" per the cheatsheet scope rule. Extra matchers include
// the predecessor variables: deployment_environment, gen_ai_provider_name.
//
// Per-model governance (cardinality):
//   - gen_ai_request_model is NOT a label on gen_ai_client_* METRICS (verified live:
//     count by(gen_ai_request_model) → absent). The model variable is sourced from
//     portkey_api_tokens_total{ai_model}, which IS the real per-model dimension.
//   - Per-model TOKENS/COST: portkey_api_tokens_total + portkey_api_cost_usd (SUBSTRATE-scoped
//     windowed-count GAUGEs keyed by ai_model, via acme.IntSel).
//   - Per-model REQUEST-RATE/LATENCY: traces_spanmetrics_calls_total + traces_spanmetrics_latency
//     keyed by span_name (= "chat <model>"), scoped by deployment_environment.
//     Because the two sources key on different labels (ai_model vs span_name), they cannot merge into
//     a single row-per-model table — two separate tables are provided instead.
//
// The predecessor's "By provider" tab row-repeat (repeat: {mode:variable, value:gen_ai_provider_name})
// is now rendered via dashboard.RepeatSection — Grafana clones the three-stat KPI row once per
// value of $gen_ai_provider_name.
package acme_ai_platform

import (
	"github.com/rknightion/synthkit/dashboard"
	acme "github.com/rknightion/synthkit/dashboards/examples/acme_ai"

	dashboardv2 "github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
)

// Sigil builds the Acme AI GenAI Semantics (gen_ai semconv) dashboard for the acme_ai_platform blueprint.
func Sigil(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ws1-sigil", "Acme AI — GenAI Semantics (gen_ai semconv)")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ────────────────────────────────────────────────────────────────────────────────
	// scenario: hidden const; app selectors reference it as blueprint="$scenario".
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme_ai_platform"))

	// gen_ai_client_* is APP-scoped → keep blueprint pin in label_values queries.
	d.Builder.QueryVariable(dashboard.LabelValuesVar(
		"deployment_environment", "Environment",
		`label_values(gen_ai_client_token_usage_sum{blueprint="$scenario"}, deployment_environment_name)`))
	d.Builder.QueryVariable(dashboard.LabelValuesVar(
		"gen_ai_provider_name", "Provider",
		`label_values(gen_ai_client_token_usage_sum{blueprint="$scenario"}, gen_ai_provider_name)`))
	// Model variable: gen_ai_request_model is now a real label on gen_ai_client_token_usage_sum
	// (emitted with the OTEL semconv alignment). Source it directly from the app metric.
	d.Builder.QueryVariable(dashboard.LabelValuesVar(
		"gen_ai_request_model", "Model",
		`label_values(gen_ai_client_token_usage_sum{blueprint="$scenario"}, gen_ai_request_model)`))

	// siSel builds a selector for gen_ai_client_* (APP-scoped) with the predecessor filter vars.
	// gen_ai_client_* carries blueprint + deployment_environment_name + gen_ai_provider_name.
	// gen_ai_request_model is now also a label on gen_ai_client_token_usage_sum (OTEL semconv
	// alignment), but is not filtered here to keep the selector broad; the per-model tables below
	// use the $gen_ai_request_model var via specific per-model panel queries.
	siSel := func(extra string) string {
		base := `blueprint="$scenario",deployment_environment_name=~"$deployment_environment",` +
			`gen_ai_provider_name=~"$gen_ai_provider_name"`
		if extra != "" {
			base += "," + extra
		}
		return "{" + base + "}"
	}

	// siSelProvOnly is now identical to siSel (the gen_ai_request_model filter that previously
	// differentiated them has been removed — model is not a label on gen_ai_client_* metrics).
	// Kept as an alias to avoid churn at call sites; both produce the same selector.
	siSelProvOnly := siSel

	// ── Overview tab ─────────────────────────────────────────────────────────────────────────────

	// KPI stats row — panel-1, panel-2, panel-3, panel-4, panel-5
	// StatTile: colored background tiles for at-a-glance health.
	dashboard.AddPanel(&d, "sig-stat-tok",
		dashboard.StatTile("Token throughput (tok/s)", "short",
			dashboard.PromTarget(
				"sum(rate(gen_ai_client_token_usage_sum"+siSel("")+"[$__rate_interval]))",
				"Token throughput")))

	dashboard.AddPanel(&d, "sig-stat-req",
		dashboard.StatTile("Request rate (req/s)", "reqps",
			dashboard.PromTarget(
				"sum(rate(gen_ai_client_operation_duration_seconds_count"+siSel("")+"[$__rate_interval]))",
				"Request rate")))

	dashboard.AddPanel(&d, "sig-stat-p95",
		dashboard.StatTile("p95 operation duration (s)", "s",
			dashboard.PromTarget(
				dashboard.ClassicHistogramQuantile(0.95, "gen_ai_client_operation_duration_seconds", siSel(""), nil),
				"p95 duration"),
			dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 5, Color: "yellow"}, dashboard.Threshold{Value: 30, Color: "red"}))

	dashboard.AddPanel(&d, "sig-stat-ttfc",
		dashboard.StatTile("p95 TTFC (s)", "s",
			dashboard.PromTarget(
				dashboard.ClassicHistogramQuantile(0.95, "gen_ai_client_operation_time_to_first_chunk_seconds", siSel(""), nil),
				"TTFC p95"),
			dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 1, Color: "yellow"}, dashboard.Threshold{Value: 5, Color: "red"}))

	dashboard.AddPanel(&d, "sig-stat-avgtok",
		dashboard.StatTile("Avg tokens / request", "short",
			dashboard.PromTarget(
				"sum(rate(gen_ai_client_token_usage_sum"+siSel("")+"[$__rate_interval])) / "+
					"clamp_min(sum(rate(gen_ai_client_token_usage_count"+siSel("")+"[$__rate_interval])), 0.001)",
				"Avg tok/req")))

	// Tokens & duration row — panel-10 (token by type) + panel-20 (duration quantiles)
	dashboard.AddPanel(&d, "sig-tok-type",
		dashboard.TimeseriesPanel("Token throughput by type (input vs output)", "short",
			dashboard.PromTarget(
				"sum by (gen_ai_token_type)(rate(gen_ai_client_token_usage_sum"+siSel("")+"[$__rate_interval]))",
				"{{gen_ai_token_type}}")))

	dashboard.AddPanel(&d, "sig-opdur-quants",
		dashboard.TimeseriesPanel("Operation duration p50 / p95 / p99 (s)", "s",
			dashboard.PromTarget(
				dashboard.ClassicHistogramQuantile(0.50, "gen_ai_client_operation_duration_seconds", siSel(""), nil),
				"p50").RefId("A"),
			dashboard.PromTarget(
				dashboard.ClassicHistogramQuantile(0.95, "gen_ai_client_operation_duration_seconds", siSel(""), nil),
				"p95").RefId("B"),
			dashboard.PromTarget(
				dashboard.ClassicHistogramQuantile(0.99, "gen_ai_client_operation_duration_seconds", siSel(""), nil),
				"p99").RefId("C")))

	// Streaming & by-model row — panel-21 (streaming TTFC+chunk) + panel-11 (tok by model)
	dashboard.AddPanel(&d, "sig-streaming",
		dashboard.TimeseriesPanel("Streaming: TTFC p95 + time-per-chunk p95 (s)", "s",
			dashboard.PromTarget(
				dashboard.ClassicHistogramQuantile(0.95, "gen_ai_client_operation_time_to_first_chunk_seconds", siSel(""), nil),
				"TTFC p95").RefId("A"),
			dashboard.PromTarget(
				dashboard.ClassicHistogramQuantile(0.95, "gen_ai_client_operation_time_per_output_chunk_seconds", siSel(""), nil),
				"time-per-chunk p95").RefId("B")))

	dashboard.AddPanel(&d, "sig-tok-model",
		dashboard.TimeseriesPanel("Token throughput by model", "short",
			dashboard.PromTarget(
				"sum by (gen_ai_request_model)(rate(gen_ai_client_token_usage_sum"+siSel("")+"[$__rate_interval]))",
				"{{gen_ai_request_model}}")))

	// ── By provider tab ───────────────────────────────────────────────────────────────────────────
	//
	// The predecessor repeats a three-stat row (token throughput / req rate / p95) per provider using a
	// row-repeat (variable=gen_ai_provider_name). This is now wired via dashboard.RepeatSection so
	// Grafana clones the row once per $gen_ai_provider_name value; panels use siSelProvOnly (= siSel)
	// which scopes by gen_ai_provider_name=~"$gen_ai_provider_name" (resolves to one value per clone).

	dashboard.AddPanel(&d, "sig-prov-tok",
		dashboard.StatTile("Token throughput", "short",
			dashboard.PromTarget(
				"sum(rate(gen_ai_client_token_usage_sum"+siSelProvOnly("")+"[$__rate_interval]))",
				"Token throughput")))

	dashboard.AddPanel(&d, "sig-prov-req",
		dashboard.StatTile("Request rate", "reqps",
			dashboard.PromTarget(
				"sum(rate(gen_ai_client_operation_duration_seconds_count"+siSelProvOnly("")+"[$__rate_interval]))",
				"Request rate")))

	dashboard.AddPanel(&d, "sig-prov-p95",
		dashboard.StatTile("p95 duration", "s",
			dashboard.PromTarget(
				dashboard.ClassicHistogramQuantile(0.95, "gen_ai_client_operation_duration_seconds", siSelProvOnly(""), nil),
				"p95 duration"),
			dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 5, Color: "yellow"}, dashboard.Threshold{Value: 30, Color: "red"}))

	// Provider & model breakdown row — panel-30 (req rate by provider) + panel-31 (req rate by model top 8)
	dashboard.AddPanel(&d, "sig-rate-by-prov",
		dashboard.TimeseriesPanel("Request rate by provider", "reqps",
			dashboard.PromTarget(
				"sum by (gen_ai_provider_name)(rate(gen_ai_client_operation_duration_seconds_count"+siSel("")+"[$__rate_interval]))",
				"{{gen_ai_provider_name}}")))

	dashboard.AddPanel(&d, "sig-rate-by-model",
		dashboard.TimeseriesPanel("Request rate by model (top 8)", "reqps",
			dashboard.PromTarget(
				"topk(8, sum by (gen_ai_request_model)(rate(gen_ai_client_operation_duration_seconds_count"+siSel("")+"[$__rate_interval])))",
				"{{gen_ai_request_model}}")))

	// Tokens-per-request distribution — panel-40
	dashboard.AddPanel(&d, "sig-tokdist",
		dashboard.TimeseriesPanel("Tokens-per-request distribution (p50 / p95 / p99)", "short",
			dashboard.PromTarget(
				dashboard.ClassicHistogramQuantile(0.50, "gen_ai_client_token_usage", siSel(""), nil),
				"p50 tokens/req").RefId("A"),
			dashboard.PromTarget(
				dashboard.ClassicHistogramQuantile(0.95, "gen_ai_client_token_usage", siSel(""), nil),
				"p95 tokens/req").RefId("B"),
			dashboard.PromTarget(
				dashboard.ClassicHistogramQuantile(0.99, "gen_ai_client_token_usage", siSel(""), nil),
				"p99 tokens/req").RefId("C")))

	// Per-model tables — two separate tables because the token/cost source (portkey_api_*, keyed by
	// ai_model) and the request/latency source (traces_spanmetrics_*, keyed by span_name="chat <model>")
	// have different label shapes and cannot be merged into a single row-per-model table.
	//
	// (a) Portkey-sourced per-model token & cost table.
	// portkey_api_tokens_total and portkey_api_cost_usd are SUBSTRATE-scoped windowed-count GAUGEs —
	// do NOT rate() them; query the gauge value directly via sum by (ai_model).
	// Scope via acme.IntSel("") (env=~"$env"); no blueprint label on the substrate lane.
	dashboard.AddPanel(&d, "sig-model-table-portkey",
		dashboard.MergeTablePanel(
			"Per-model token & cost summary (Portkey analytics, windowed gauges)",
			[]*dashboardv2.TargetBuilder{
				dashboard.PromTableTarget(
					`sum by (ai_model)(portkey_api_tokens_total`+acme.IntSel("")+`)`,
					"A"),
				dashboard.PromTableTarget(
					`sum by (ai_model)(portkey_api_cost_usd`+acme.IntSel("")+`)`,
					"B"),
			},
			dashboard.OrganizeOptions{
				Exclude: []string{"Time"},
				Rename: map[string]string{
					"Value #A": "Tokens (window)",
					"Value #B": "Cost $ (window)",
				},
				Order: []string{"ai_model", "Tokens (window)", "Cost $ (window)"},
			},
		))

	// (b) Spanmetrics-sourced per-model request rate & p95 latency table.
	// traces_spanmetrics_calls_total and traces_spanmetrics_latency are substrate-scoped counters/
	// histograms keyed by span_name (= "chat <model>"). Scoped by deployment_environment_name variable.
	// No blueprint label — substrate path.
	dashboard.AddPanel(&d, "sig-model-table-spanmetrics",
		dashboard.MergeTablePanel(
			"Per-model request rate & p95 latency (spanmetrics, span_name=\"chat <model>\")",
			[]*dashboardv2.TargetBuilder{
				dashboard.PromTableTarget(
					`sum by (span_name)(rate(traces_spanmetrics_calls_total{span_name=~"chat .+",deployment_environment_name=~"$deployment_environment"}[$__rate_interval]))`,
					"A"),
				dashboard.PromTableTarget(
					`histogram_quantile(0.95, sum by (span_name,le)(rate(traces_spanmetrics_latency{span_name=~"chat .+",deployment_environment_name=~"$deployment_environment"}[$__rate_interval])))`,
					"B"),
			},
			dashboard.OrganizeOptions{
				Exclude: []string{"Time"},
				Rename: map[string]string{
					"Value #A": "Requests/s",
					"Value #B": "p95 (s)",
				},
				Order: []string{"span_name", "Requests/s", "p95 (s)"},
			},
		))

	// ── Traces tab ────────────────────────────────────────────────────────────────────────────────
	//
	// panel-50: GenAI OTLP trace feed — chat spans from Tempo (TraceQL tableType:traces).
	// The predecessor notes: "This same span stream activates Grafana AI Observability's pre-built GenAI
	// dashboards." Blueprint filter removed: Tempo TraceQL uses resource.blueprint, not gen_ai scope.
	dashboard.AddPanel(&d, "sig-traces",
		dashboard.TraceTablePanel(
			"GenAI OTLP trace feed — chat spans (Tempo, tableType:traces)",
			acme.DSTempo,
			dashboard.TempoTableTarget(`{ span.gen_ai.operation.name = "chat" }`, 20)))

	// ── Layout ────────────────────────────────────────────────────────────────────────────────────
	dashboard.WithTabs(&d,
		dashboard.Tabbed("Overview",
			dashboard.Section("Token & request KPIs",
				dashboard.Tile("sig-stat-tok"), dashboard.Tile("sig-stat-req"), dashboard.Tile("sig-stat-p95"),
				dashboard.Tile("sig-stat-ttfc"), dashboard.Tile("sig-stat-avgtok")),
			dashboard.Section("Tokens",
				dashboard.Half("sig-tok-type"), dashboard.Half("sig-opdur-quants")),
			dashboard.Section("Streaming & by model",
				dashboard.Half("sig-streaming"), dashboard.Half("sig-tok-model")),
		),
		dashboard.Tabbed("By provider",
			dashboard.RepeatSection("Provider-level KPIs — $gen_ai_provider_name", "gen_ai_provider_name",
				dashboard.Tile("sig-prov-tok"), dashboard.Tile("sig-prov-req"), dashboard.Tile("sig-prov-p95")),
			dashboard.Section("Provider & model breakdown",
				dashboard.Half("sig-rate-by-prov"), dashboard.Half("sig-rate-by-model"),
				dashboard.Full("sig-tokdist")),
			dashboard.Section("Per-model summary — tokens & cost (Portkey)",
				dashboard.Tall("sig-model-table-portkey")),
			dashboard.Section("Per-model summary — request rate & p95 latency (spanmetrics)",
				dashboard.Tall("sig-model-table-spanmetrics")),
		),
		dashboard.Tabbed("Traces",
			dashboard.Section("",
				dashboard.Tall("sig-traces")),
		),
	)
	return d, nil
}
