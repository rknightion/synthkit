// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
)

func TestTimeseriesPanelCarriesExpr(t *testing.T) {
	d, _ := NewDashboard("u", "T")
	AddPanel(&d, "p1", TimeseriesPanel("RPS", "reqps", PromTarget(`sum(rate(x[5m]))`, "rps")))
	out, err := Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(out), `sum(rate(x[5m]))`) {
		t.Errorf("rendered JSON missing the panel expr")
	}
}

// B1: Loki / Tempo / Pyroscope query targets.
func TestSignalSourceTargetsBuild(t *testing.T) {
	for name, b := range map[string]*dashboardv2.TargetBuilder{
		"loki":      LokiTarget(`{service="api"} |= "error"`, "errors"),
		"tempo":     TempoTarget(`{ duration > 1s }`),
		"pyroscope": PyroscopeTarget("process_cpu:cpu", `{service="api"}`),
	} {
		if _, err := b.Build(); err != nil {
			t.Errorf("%s target failed to build: %v", name, err)
		}
	}
}

// B2: Infinity read target (+ root selector + columns). A backend-parsed read table renders blank
// without an explicit columns list, so columns MUST be present.
func TestInfinityTargetBuildsBackendJSON(t *testing.T) {
	b := InfinityTarget("A", "http://host:8088/control/state", "synthkit (Infinity)", "",
		Col("volume_multiplier", "Volume ×", "number"))
	q, err := b.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	js, _ := json.Marshal(q)
	s := string(js)
	for _, want := range []string{
		"yesoreyeram-infinity-datasource", "/control/state", `"parser":"backend"`,
		`"source":"url"`, `"columns":`, `"selector":"volume_multiplier"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("infinity target JSON missing %q: %s", want, s)
		}
	}
	if strings.Contains(s, "root_selector") {
		t.Errorf("empty rootSelector must be omitted, got %s", s)
	}
	// With a root selector it must appear, alongside its columns.
	rb, _ := InfinityTarget("A", "http://host/x", "ds", "scenarios",
		Col("blueprint", "Blueprint", "string"), Col("name", "Name", "string")).Build()
	rjs := string(mustJSON(t, rb))
	for _, want := range []string{`"root_selector":"scenarios"`, `"selector":"blueprint"`, `"selector":"name"`} {
		if !strings.Contains(rjs, want) {
			t.Errorf("root_selector/columns missing %q: %s", want, rjs)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// FetchAction must build the VERIFIED write shape: type:"fetch", POST, fixed body, NO headers,
// confirmation "" and oneClick false.
func TestFetchActionVerifiedShape(t *testing.T) {
	a, err := FetchAction("Peak", "https://host/control/load", `{"volume_multiplier":3}`).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s := string(mustJSON(t, a))
	for _, want := range []string{`"type":"fetch"`, `"POST"`, "/control/load", `volume_multiplier`,
		`"confirmation":""`, `"oneClick":false`} {
		if !strings.Contains(s, want) {
			t.Errorf("fetch action JSON missing %q: %s", want, s)
		}
	}
	// Grafana can't set fetch headers — and we must never re-add the dead Content-Type call.
	if strings.Contains(s, "Content-Type") || strings.Contains(s, "headers") {
		t.Errorf("fetch action must carry NO headers: %s", s)
	}
	if strings.Contains(s, `"type":"infinity"`) {
		t.Errorf("fetch action must never be type:infinity: %s", s)
	}
}

// B3: ActionBoardPanel builds the VERIFIED firing widget — a one-row inline-data table whose
// Actions cell (cellOptions type "actions") holds type:"fetch" buttons with fixed bodies, header
// hidden, NO ${__data} interpolation and NO type:"infinity".
func TestActionBoardPanelFiringShape(t *testing.T) {
	p := ActionBoardPanel("Load presets", "synthkit (Infinity)",
		FetchAction("Peak", "https://host/control/load", `{"volume_multiplier":3}`),
		FetchAction("Idle", "https://host/control/load", `{"volume_multiplier":0.2}`),
	)
	built, err := p.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s := string(mustJSON(t, built))
	for _, want := range []string{
		`"POST"`, "/control/load", `volume_multiplier`, "Peak", "Idle",
		`"type":"fetch"`, `"type":"actions"`, // cellOptions.type:"actions"
		`"showHeader":false`,
		`"source":"inline"`, `[{\"_\":\" \"}]`, // one-row inline data
	} {
		if !strings.Contains(s, want) {
			t.Errorf("action board JSON missing %q: %s", want, s)
		}
	}
	if strings.Contains(s, `"type":"infinity"`) {
		t.Errorf("action board must not use type:infinity: %s", s)
	}
	if strings.Contains(s, "${__data") {
		t.Errorf("action board must not use ${__data} interpolation: %s", s)
	}
}

// StatPanel reduces with lastNotNull so the overnight trough's empty trailing bucket doesn't render
// as 0/NaN.
func TestStatPanelLastNotNull(t *testing.T) {
	built, err := StatPanel("RPS", PromTarget(`sum(rate(x[5m]))`, "rps")).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(string(mustJSON(t, built)), "lastNotNull") {
		t.Errorf("StatPanel should reduce with lastNotNull")
	}
}

// StatTile colors its background by absolute thresholds (the KPI health-tile look).
func TestStatTileThresholds(t *testing.T) {
	built, err := StatTile("Error %", "percent", PromTarget(`err`, ""),
		Threshold{0, "green"}, Threshold{1, "yellow"}, Threshold{5, "red"}).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s := string(mustJSON(t, built))
	for _, want := range []string{`"group":"stat"`, "background", "thresholds", `"color":"red"`, "lastNotNull"} {
		if !strings.Contains(s, want) {
			t.Errorf("StatTile missing %q: %s", want, s)
		}
	}
}

// B4: predecessor-parity viz panels — logs, gauge, heatmap, state-timeline. Each must build with the
// correct vizConfig kind and carry its data target's query.
func TestPredecessorVizPanelsBuild(t *testing.T) {
	cases := []struct {
		name      string
		panel     *dashboardv2.PanelBuilder
		wantKind  string
		wantQuery string
	}{
		// The SDK encodes the viz plugin id in vizConfig.group (vizConfig.kind is always "VizConfig").
		{"logs", LogsPanel("App logs", LokiTarget(`{source="app"}`, "")), `"group":"logs"`, `{source=\"app\"}`},
		{"gauge", GaugePanel("Score", "percentunit", PromTarget(`avg(score)`, "score")), `"group":"gauge"`, `avg(score)`},
		{"heatmap", HeatmapPanel("Latency dist", "ms", PromTarget(`sum(rate(x_bucket[5m]))`, "")), `"group":"heatmap"`, `x_bucket`},
		{"statetimeline", StateTimelinePanel("Pipeline state", PromTarget(`up`, "{{job}}")), `"group":"state-timeline"`, `up`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			built, err := c.panel.Build()
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			s := string(mustJSON(t, built))
			if !strings.Contains(s, c.wantKind) {
				t.Errorf("%s: missing vizConfig %s in %s", c.name, c.wantKind, s)
			}
			if !strings.Contains(s, c.wantQuery) {
				t.Errorf("%s: missing query %q in %s", c.name, c.wantQuery, s)
			}
		})
	}
}

// B4: logs panel must carry the standard reading options (time, wrap, prettify, details, desc sort).
func TestLogsPanelOptions(t *testing.T) {
	built, err := LogsPanel("Logs", LokiTarget(`{source="portkey"}`, "")).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s := string(mustJSON(t, built))
	for _, want := range []string{`"showTime":true`, `"wrapLogMessage":true`, `"prettifyLogMessage":true`,
		`"enableLogDetails":true`, `"sortOrder":"Descending"`} {
		if !strings.Contains(s, want) {
			t.Errorf("logs panel missing option %q: %s", want, s)
		}
	}
}

// B4: a traces table — TempoTableTarget sets tableType:"traces" + traceql + limit; TraceTablePanel
// adds the traceID → Explore data-link override pointing at the given Tempo datasource uid.
func TestTraceTablePanelShape(t *testing.T) {
	tgt := TempoTableTarget(`{resource.service.name="acme-backend"}`, 20)
	q, err := tgt.Build()
	if err != nil {
		t.Fatalf("target build: %v", err)
	}
	qs := string(mustJSON(t, q))
	for _, want := range []string{`"tableType":"traces"`, `"queryType":"traceql"`, `"limit":20`,
		`acme-backend`} {
		if !strings.Contains(qs, want) {
			t.Errorf("tempo table target missing %q: %s", want, qs)
		}
	}
	p, err := TraceTablePanel("Backend traces", "grafanacloud-traces", tgt).Build()
	if err != nil {
		t.Fatalf("panel build: %v", err)
	}
	ps := string(mustJSON(t, p))
	for _, want := range []string{`"group":"table"`, `"traceID"`, `"id":"links"`, "Open trace",
		"grafanacloud-traces", "queryType"} {
		if !strings.Contains(ps, want) {
			t.Errorf("trace table panel missing %q: %s", want, ps)
		}
	}
}

// F1: InfinityColumn fluent methods return independent copies (no mutation).
func TestInfinityColumnFluentMethods(t *testing.T) {
	base := Col("cost", "Cost", "number")
	got := base.WithUnit("currencyUSD").WithDecimals(2).WithColorMode("color-background").WithLink("Docs", "https://example.com/docs")

	// Original must be unmodified.
	if base.Unit != "" || base.Decimals != nil || base.ColorMode != "" || len(base.Links) != 0 {
		t.Errorf("WithX methods mutated original InfinityColumn: %+v", base)
	}
	if got.Unit != "currencyUSD" {
		t.Errorf("WithUnit: want currencyUSD, got %q", got.Unit)
	}
	if got.Decimals == nil || *got.Decimals != 2 {
		t.Errorf("WithDecimals: want 2, got %v", got.Decimals)
	}
	if got.ColorMode != "color-background" {
		t.Errorf("WithColorMode: want color-background, got %q", got.ColorMode)
	}
	if len(got.Links) != 1 || got.Links[0].Title != "Docs" || got.Links[0].URL != "https://example.com/docs" {
		t.Errorf("WithLink: want 1 link Docs/https://example.com/docs, got %+v", got.Links)
	}

	// WithLink appends — chain two links.
	got2 := got.WithLink("More", "https://example.com/more")
	if len(got2.Links) != 2 {
		t.Errorf("WithLink chain: want 2 links, got %d", len(got2.Links))
	}
	// got must still have 1 link.
	if len(got.Links) != 1 {
		t.Errorf("WithLink chain mutated previous copy: got %d links", len(got.Links))
	}
}

// F1: InfinityTablePanel carries Infinity target + table viz + OverrideByName for formatted cols.
func TestInfinityTablePanelOverrides(t *testing.T) {
	d := 2
	cols := []InfinityColumn{
		Col("name", "Name", "string"), // no formatting — no override
		Col("cost", "Cost", "number").WithUnit("currencyUSD").WithDecimals(d),
		Col("status", "Status", "string").WithColorMode("color-text"),
		Col("trace", "Trace", "string").WithLink("Open", "https://traces/{{.}}"),
	}
	p := InfinityTablePanel("Usage", "A", "http://host/usage", "synthkit (Infinity)", "", cols...)
	built, err := p.Build()
	if err != nil {
		t.Fatalf("InfinityTablePanel build: %v", err)
	}
	s := string(mustJSON(t, built))

	// Infinity target wired.
	for _, want := range []string{
		"yesoreyeram-infinity-datasource", "/usage", `"parser":"backend"`,
		`"selector":"name"`, `"selector":"cost"`, `"selector":"status"`, `"selector":"trace"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("InfinityTablePanel: missing target field %q in %s", want, s)
		}
	}

	// Table viz group.
	if !strings.Contains(s, `"group":"table"`) {
		t.Errorf("InfinityTablePanel: missing table viz group in %s", s)
	}

	// Field overrides: unit, decimals, cellOptions type, links — but NOT for plain "Name" col.
	for _, want := range []string{
		`"id":"unit"`, `"currencyUSD"`,
		`"id":"decimals"`,
		`"id":"custom.cellOptions"`, `"color-text"`,
		`"id":"links"`, `"targetBlank":true`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("InfinityTablePanel override: missing %q in %s", want, s)
		}
	}
}

// F2: InfinityTargetPOST emits method:"POST" and body in url_options.data.
func TestInfinityTargetPOST(t *testing.T) {
	body := `{"project":"my-proj","is_root":true}`
	b := InfinityTargetPOST("A", "https://api.smith.langchain.com/api/v1/runs/query",
		"LangSmith (Infinity)", "runs", body,
		Col("id", "Run ID", "string"),
		Col("name", "Name", "string"),
	)
	q, err := b.Build()
	if err != nil {
		t.Fatalf("InfinityTargetPOST build: %v", err)
	}
	s := string(mustJSON(t, q))

	for _, want := range []string{
		"yesoreyeram-infinity-datasource",
		"/api/v1/runs/query",
		`"method":"POST"`,
		`"data":`,
		`my-proj`,
		`"root_selector":"runs"`,
		`"selector":"id"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("InfinityTargetPOST: missing %q in %s", want, s)
		}
	}

	// GET default must NOT carry a data key.
	gq, _ := InfinityTarget("A", "http://host/x", "ds", "", Col("f", "F", "string")).Build()
	gs := string(mustJSON(t, gq))
	if strings.Contains(gs, `"data":`) {
		t.Errorf("InfinityTarget (GET default) must not carry url_options.data: %s", gs)
	}
	if !strings.Contains(gs, `"method":"GET"`) {
		t.Errorf("InfinityTarget (GET default) must carry method:GET: %s", gs)
	}
}

// F3: NativeHistogramHeatmap builds a heatmap viz with Calculate(false).
func TestNativeHistogramHeatmapBuilds(t *testing.T) {
	tgt := PromTarget(`sum(rate(traces_spanmetrics_latency{blueprint="initech"}[$__rate_interval]))`, "")
	p := NativeHistogramHeatmap("Latency dist", "ms", tgt)
	built, err := p.Build()
	if err != nil {
		t.Fatalf("NativeHistogramHeatmap build: %v", err)
	}
	s := string(mustJSON(t, built))
	if !strings.Contains(s, `"group":"heatmap"`) {
		t.Errorf("NativeHistogramHeatmap: missing heatmap group in %s", s)
	}
	if !strings.Contains(s, `traces_spanmetrics_latency`) {
		t.Errorf("NativeHistogramHeatmap: missing target expr in %s", s)
	}
	// calculate:false must be present (Grafana auto-detects exponential buckets; re-bucketing is wrong).
	if !strings.Contains(s, `"calculate":false`) {
		t.Errorf("NativeHistogramHeatmap: expected calculate:false in %s", s)
	}
}

// ActionButtonPanel is retained as an alias of the firing shape.
func TestActionButtonPanelAliasesBoard(t *testing.T) {
	p := ActionButtonPanel("Clear", "ds",
		FetchAction("Clear all", "https://host/control/scenarios", `{"active_scenarios":[]}`))
	built, err := p.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s := string(mustJSON(t, built))
	for _, want := range []string{`"type":"actions"`, `"type":"fetch"`, `"showHeader":false`} {
		if !strings.Contains(s, want) {
			t.Errorf("alias board JSON missing %q: %s", want, s)
		}
	}
}
