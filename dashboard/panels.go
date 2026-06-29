// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"fmt"
	"net/url"

	"github.com/grafana/grafana-foundation-sdk/go/cog"
	"github.com/grafana/grafana-foundation-sdk/go/common"
	"github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
	"github.com/grafana/grafana-foundation-sdk/go/gauge"
	"github.com/grafana/grafana-foundation-sdk/go/grafanapyroscope"
	"github.com/grafana/grafana-foundation-sdk/go/heatmap"
	"github.com/grafana/grafana-foundation-sdk/go/logs"
	"github.com/grafana/grafana-foundation-sdk/go/loki"
	"github.com/grafana/grafana-foundation-sdk/go/nodegraph"
	"github.com/grafana/grafana-foundation-sdk/go/prometheus"
	"github.com/grafana/grafana-foundation-sdk/go/stat"
	"github.com/grafana/grafana-foundation-sdk/go/statetimeline"
	"github.com/grafana/grafana-foundation-sdk/go/table"
	"github.com/grafana/grafana-foundation-sdk/go/tempo"
	"github.com/grafana/grafana-foundation-sdk/go/text"
	"github.com/grafana/grafana-foundation-sdk/go/timeseries"
)

// PromTarget builds a Prometheus v2 target with range=true (time-series default).
// The concrete return type is *dashboardv2.TargetBuilder; callers (Tasks 10/14) use this type.
func PromTarget(expr, legend string) *dashboardv2.TargetBuilder {
	return dashboardv2.NewTargetBuilder().
		RefId("A").
		Query(prometheus.NewQueryV2Builder().
			Expr(expr).
			LegendFormat(legend).
			Range(true))
}

// TimeseriesPanel builds a time-series panel with the given title, unit, and one or more
// Prometheus targets. Unit is a Grafana unit string (e.g. "short", "reqps", "ms").
func TimeseriesPanel(title, unit string, targets ...*dashboardv2.TargetBuilder) *dashboardv2.PanelBuilder {
	qg := dashboardv2.NewQueryGroupBuilder()
	for _, t := range targets {
		qg.Target(t)
	}
	return dashboardv2.NewPanelBuilder().
		Title(title).
		Visualization(timeseries.NewVisualizationV2Builder().Unit(unit)).
		Data(qg)
}

// StatPanel builds a single-stat panel with the given title and one Prometheus target. The reducer
// is lastNotNull: synthetic traffic follows a diurnal plateau, so at the overnight trough the final
// query bucket is often empty — a plain "last" would render 0/NaN even though the series has data.
// lastNotNull shows the most recent real value instead.
func StatPanel(title string, target *dashboardv2.TargetBuilder) *dashboardv2.PanelBuilder {
	return dashboardv2.NewPanelBuilder().
		Title(title).
		Visualization(stat.NewVisualizationV2Builder().
			ReduceOptions(common.NewReduceDataOptionsBuilder().Calcs([]string{"lastNotNull"}))).
		Data(dashboardv2.NewQueryGroupBuilder().Target(target))
}

// TablePanel builds a table panel with the given title and one Prometheus target.
func TablePanel(title string, target *dashboardv2.TargetBuilder) *dashboardv2.PanelBuilder {
	return dashboardv2.NewPanelBuilder().
		Title(title).
		Visualization(table.NewVisualizationV2Builder()).
		Data(dashboardv2.NewQueryGroupBuilder().Target(target))
}

// TextPanel builds a text panel with markdown content. It carries no data target.
func TextPanel(title, markdown string) *dashboardv2.PanelBuilder {
	return dashboardv2.NewPanelBuilder().
		Title(title).
		Visualization(text.NewVisualizationV2Builder().
			Mode(text.TextModeMarkdown).
			Content(markdown))
}

// Threshold is one absolute-value color step for a stat/gauge field (the colored-KPI-tile look).
// Value is the lower bound at which Color takes effect; the first step in a list is treated as the
// base (-Infinity). Color is a Grafana color name ("green","yellow","orange","red") or hex.
type Threshold struct {
	Value float64
	Color string
}

// StatTile builds a KPI stat panel that colors its BACKGROUND by absolute thresholds — the dense,
// at-a-glance health-tile the predecessor overviews use. unit is a Grafana unit string. thresholds, if
// given, must be ascending by Value; the first step is the base. With no thresholds it is a plain
// colored-value tile. Reduces with lastNotNull (diurnal-trough robust) and draws a sparkline area.
func StatTile(title, unit string, target *dashboardv2.TargetBuilder, thresholds ...Threshold) *dashboardv2.PanelBuilder {
	viz := stat.NewVisualizationV2Builder().
		Unit(unit).
		ReduceOptions(common.NewReduceDataOptionsBuilder().Calcs([]string{"lastNotNull"})).
		GraphMode(common.BigValueGraphModeArea).
		ColorMode(common.BigValueColorModeBackground)
	// No thresholds → a single informational ("blue") base step. Without this the panel inherits
	// Grafana's DEFAULT thresholds (green base, red at 80), so any neutral count/volume tile above 80
	// renders alarm-red. A blue base reads as informational, distinct from the green/yellow/red of a
	// genuinely-thresholded health tile.
	if len(thresholds) == 0 {
		thresholds = []Threshold{{Value: 0, Color: "blue"}}
	}
	steps := make([]dashboardv2.Threshold, len(thresholds))
	for i, t := range thresholds {
		v := t.Value
		vp := &v
		if i == 0 {
			vp = nil // base step = -Infinity
		}
		steps[i] = dashboardv2.Threshold{Value: vp, Color: t.Color}
	}
	viz = viz.
		Thresholds(dashboardv2.NewThresholdsConfigBuilder().
			Mode(dashboardv2.ThresholdsModeAbsolute).
			Steps(steps)).
		ColorScheme(dashboardv2.NewFieldColorBuilder().Mode(dashboardv2.FieldColorModeIdThresholds))
	return dashboardv2.NewPanelBuilder().
		Title(title).
		Visualization(viz).
		Data(dashboardv2.NewQueryGroupBuilder().Target(target))
}

// LogsPanel builds a logs-visualization panel from a single Loki target, with the standard reading
// options the predecessor dashboards use (timestamps, wrapped + prettified messages, expandable details,
// newest-first). Pass a LokiTarget; a Prometheus target would render nothing useful here.
func LogsPanel(title string, target *dashboardv2.TargetBuilder) *dashboardv2.PanelBuilder {
	return dashboardv2.NewPanelBuilder().
		Title(title).
		Visualization(logs.NewVisualizationV2Builder().
			ShowTime(true).
			WrapLogMessage(true).
			PrettifyLogMessage(true).
			EnableLogDetails(true).
			DedupStrategy(common.LogsDedupStrategyNone).
			SortOrder(common.LogsSortOrderDescending)).
		Data(dashboardv2.NewQueryGroupBuilder().Target(target))
}

// GaugePanel builds a gauge-visualization panel (a radial gauge for a single reduced value, e.g. an
// eval score or a cache-hit ratio) from one target. Unit is a Grafana unit string.
func GaugePanel(title, unit string, target *dashboardv2.TargetBuilder) *dashboardv2.PanelBuilder {
	return dashboardv2.NewPanelBuilder().
		Title(title).
		Visualization(gauge.NewVisualizationV2Builder().Unit(unit)).
		Data(dashboardv2.NewQueryGroupBuilder().Target(target))
}

// HeatmapPanel builds a heatmap-visualization panel from one target — the latency-distribution view
// the gateway dashboards use. Calculate is OFF: the source query already yields bucketed series
// (e.g. a classic histogram's _bucket rate), so the panel must not re-bucket. Unit labels the y-axis.
func HeatmapPanel(title, unit string, target *dashboardv2.TargetBuilder) *dashboardv2.PanelBuilder {
	return dashboardv2.NewPanelBuilder().
		Title(title).
		Visualization(heatmap.NewVisualizationV2Builder().Unit(unit).Calculate(false)).
		Data(dashboardv2.NewQueryGroupBuilder().Target(target))
}

// NativeHistogramHeatmap builds a heatmap panel suited to a native (exponential) histogram source.
// Callers pass a target built from NativeHistogramRate (via PromTarget) — no _bucket suffix, no
// le label. Calculate(false) is correct for native histograms: Grafana auto-detects the exponential
// bucket layout from the series type and renders them as heatmap cells without client-side
// re-bucketing. Unit labels the y-axis (e.g. "ms", "s").
func NativeHistogramHeatmap(title, unit string, target *dashboardv2.TargetBuilder) *dashboardv2.PanelBuilder {
	return dashboardv2.NewPanelBuilder().
		Title(title).
		Visualization(heatmap.NewVisualizationV2Builder().Unit(unit).Calculate(false)).
		Data(dashboardv2.NewQueryGroupBuilder().Target(target))
}

// ServiceMapTarget builds a Tempo "serviceMap" query target. The Tempo datasource renders this
// natively as a node graph derived from the traces_service_graph_* metrics (it queries the
// service-graph metrics behind the scenes — no client-side transformation needed). Pair with
// NodeGraphPanel. Requires the metrics-generator service-graph processor to be enabled (it is on
// a Grafana Cloud stack with metrics-generator — traces_service_graph_request_total is populated).
func ServiceMapTarget() *dashboardv2.TargetBuilder {
	return dashboardv2.NewTargetBuilder().
		RefId("A").
		Query(tempo.NewQueryV2Builder().QueryType("serviceMap"))
}

// NodeGraphPanel builds a node-graph (service-map) panel from one target — typically a
// ServiceMapTarget (Tempo serviceMap). It renders the estate topology as a directed node graph with
// per-edge request/error/duration stats. For a tabular alternative use a service-graph edge table
// (RateExpr over traces_service_graph_request_total grouped by client,server).
func NodeGraphPanel(title string, target *dashboardv2.TargetBuilder) *dashboardv2.PanelBuilder {
	return dashboardv2.NewPanelBuilder().
		Title(title).
		Visualization(nodegraph.NewVisualizationV2Builder()).
		Data(dashboardv2.NewQueryGroupBuilder().Target(target))
}

// StateTimelinePanel builds a state-timeline panel from one or more targets — a horizontal band per
// series showing discrete state over time (pipeline up/down, phase). Best with a small, discrete value set.
func StateTimelinePanel(title string, targets ...*dashboardv2.TargetBuilder) *dashboardv2.PanelBuilder {
	qg := dashboardv2.NewQueryGroupBuilder()
	for _, t := range targets {
		qg.Target(t)
	}
	return dashboardv2.NewPanelBuilder().
		Title(title).
		Visualization(statetimeline.NewVisualizationV2Builder()).
		Data(qg)
}

// TempoTableTarget builds a Tempo TraceQL target that returns a TABLE of traces (tableType:"traces")
// rather than a single trace view — the form a trace-list table panel needs. limit caps rows.
func TempoTableTarget(query string, limit int64) *dashboardv2.TargetBuilder {
	return dashboardv2.NewTargetBuilder().
		RefId("A").
		Query(tempo.NewQueryV2Builder().
			Query(query).
			QueryType("traceql").
			TableType(tempo.SearchTableTypeTraces).
			Limit(limit))
}

// TraceTablePanel builds a table panel listing traces (pair it with TempoTableTarget) and wires the
// traceID column to a "Open trace" data link that deep-links into Explore against the given Tempo
// datasource uid (on Grafana Cloud this is conventionally "grafanacloud-traces"). The link uses
// ${__value.raw} so each row opens its own trace.
func TraceTablePanel(title, tempoDSUID string, target *dashboardv2.TargetBuilder) *dashboardv2.PanelBuilder {
	exploreState := fmt.Sprintf(
		`{"datasource":%q,"queries":[{"refId":"A","queryType":"traceId","query":"${__value.raw}"}],"range":{"from":"now-6h","to":"now"}}`,
		tempoDSUID)
	link := map[string]any{
		"title":       "Open trace",
		"url":         "/explore?left=" + url.QueryEscape(exploreState),
		"targetBlank": true,
	}
	return dashboardv2.NewPanelBuilder().
		Title(title).
		Visualization(table.NewVisualizationV2Builder().
			ShowHeader(true).
			OverrideByName("traceID", []dashboardv2.DynamicConfigValue{
				{Id: "links", Value: []any{link}},
			})).
		Data(dashboardv2.NewQueryGroupBuilder().Target(target))
}

// AddPanel adds a built panel to the dashboard under the given reference id (the string key layout
// helpers use to position it). It also stamps a UNIQUE numeric panel id: Grafana keys panels by that
// id, and panels sharing the zero value collapse to one-per-tab in both the live UI and snapshots.
func AddPanel(d *Dashboard, id string, p *dashboardv2.PanelBuilder) {
	d.nextID++
	p.Id(float64(d.nextID))
	d.Builder.Panel(id, p)
}

// LokiDSUID is the Grafana Cloud convention uid for the logs datasource. Loki targets MUST pin it
// explicitly: a Grafana Cloud stack carries MULTIPLE loki datasources (logs, alert-state-history,
// usage-insights), so "resolve the default by type" is ambiguous and silently yields "No data".
// (Prometheus/Tempo each have a single datasource, so they resolve by type without a pin.)
const LokiDSUID = "grafanacloud-logs"

// LokiTarget builds a Loki log query target (GA v2 QueryV2Builder), pinned to the Loki datasource
// (see LokiDSUID — leaving it unset renders blank against a multi-loki-datasource stack).
func LokiTarget(expr, legend string) *dashboardv2.TargetBuilder {
	return dashboardv2.NewTargetBuilder().
		RefId("A").
		Query(loki.NewQueryV2Builder().Expr(expr).LegendFormat(legend).
			Datasource(dashboardv2.NewDashboardv2DataQueryKindDatasourceBuilder().Name(LokiDSUID)))
}

// LokiInstantTarget builds a Loki INSTANT query target (pinned to the Loki datasource). Use it for
// aggregation TABLE panels (e.g. `sum by (k)(count_over_time(...[$__range]))`): an instant query
// collapses each group to a single current value (one row per group), whereas the range LokiTarget
// returns a matrix that a table renders as a noisy per-timestamp, per-series frame set.
func LokiInstantTarget(expr, legend string) *dashboardv2.TargetBuilder {
	return dashboardv2.NewTargetBuilder().
		RefId("A").
		Query(loki.NewQueryV2Builder().Expr(expr).LegendFormat(legend).
			Instant(true).
			Datasource(dashboardv2.NewDashboardv2DataQueryKindDatasourceBuilder().Name(LokiDSUID)))
}

// TempoTarget builds a Tempo trace (TraceQL) query target (GA v2 QueryV2Builder).
func TempoTarget(query string) *dashboardv2.TargetBuilder {
	return dashboardv2.NewTargetBuilder().
		RefId("A").
		Query(tempo.NewQueryV2Builder().Query(query))
}

// PyroscopeTarget builds a Grafana Pyroscope profile query target (GA v2 QueryV2Builder).
func PyroscopeTarget(profileType, labelSelector string) *dashboardv2.TargetBuilder {
	return dashboardv2.NewTargetBuilder().
		RefId("A").
		Query(grafanapyroscope.NewQueryV2Builder().
			ProfileTypeId(profileType).
			LabelSelector(labelSelector))
}

// ColLink is a data-link entry attached to an InfinityColumn. When rendered as a table-column
// field override the Grafana link object carries title, url, and targetBlank:true.
type ColLink struct {
	Title string
	URL   string
}

// InfinityColumn is one entry in the Infinity backend-parser `columns` list. A backend-parsed
// table renders BLANK without an explicit columns list, so every read table MUST pass one
// (browser click-verified against a Grafana Cloud stack / Grafana 13.1). Type is "string"/"number"/etc.
// Optional formatting fields (Unit, Decimals, ColorMode, Links) are applied as OverrideByName
// entries when InfinityTablePanel builds the table viz — they have no effect in bare InfinityTarget.
type InfinityColumn struct {
	Selector  string    // JSONPath-ish field selector within the (root-selected) document
	Text      string    // column header
	Type      string    // "string", "number", ...
	Unit      string    // Grafana unit string (e.g. "reqps", "ms"); "" = none
	Decimals  *int      // decimal places; nil = default
	ColorMode string    // "" | "color-text" | "color-background"
	Links     []ColLink // data-links appended to the column; nil = none
}

// Col is a terse InfinityColumn constructor (Selector, Text, Type only; formatting fields zero).
func Col(selector, text, typ string) InfinityColumn {
	return InfinityColumn{Selector: selector, Text: text, Type: typ}
}

// WithUnit returns a copy of c with Unit set.
func (c InfinityColumn) WithUnit(u string) InfinityColumn { c.Unit = u; return c }

// WithDecimals returns a copy of c with Decimals set.
func (c InfinityColumn) WithDecimals(d int) InfinityColumn { c.Decimals = &d; return c }

// WithColorMode returns a copy of c with ColorMode set ("color-text" or "color-background").
func (c InfinityColumn) WithColorMode(m string) InfinityColumn { c.ColorMode = m; return c }

// WithLink returns a copy of c with the given data-link appended to Links.
func (c InfinityColumn) WithLink(title, url string) InfinityColumn {
	c.Links = append(append([]ColLink(nil), c.Links...), ColLink{Title: title, URL: url})
	return c
}

// infinityQuery is a minimal cog.Builder[dashboardv2.DataQueryKind] for the Infinity datasource.
// The SDK ships no Infinity query builder at v0.0.18, so we build the DataQueryKind directly.
// Spec mirrors the Infinity backend-parser JSON shape. It serves both READ tables (source:"url",
// GET/POST the generator) and the action-board one-row INLINE table (source:"inline").
type infinityQuery struct {
	refID        string
	url          string // url source only
	inlineData   string // inline source only (e.g. `[{"_":" "}]`)
	source       string // "url" | "inline"
	dsName       string
	rootSelector string           // JSONPath-ish root to extract a sub-array (e.g. "scenarios"); "" = whole doc
	columns      []InfinityColumn // explicit columns — REQUIRED for backend parser to render
	method       string           // "GET" | "POST"; defaults to "GET" when empty
	body         string           // raw JSON body for POST requests; ignored for GET
}

func (q infinityQuery) Build() (dashboardv2.DataQueryKind, error) {
	name := q.dsName
	src := q.source
	if src == "" {
		src = "url"
	}
	spec := map[string]any{
		"refId":  q.refID,
		"type":   "json",
		"source": src,
		"format": "table",
		"parser": "backend",
	}
	switch src {
	case "inline":
		spec["data"] = q.inlineData
	default:
		spec["url"] = q.url
		method := q.method
		if method == "" {
			method = "GET"
		}
		urlOpts := map[string]any{"method": method}
		if method == "POST" && q.body != "" {
			urlOpts["data"] = q.body
		}
		spec["url_options"] = urlOpts
	}
	if q.rootSelector != "" {
		spec["root_selector"] = q.rootSelector
	}
	// columns is always present (empty list for the action board's placeholder row), matching the
	// predecessor's verified shape: a backend-parsed read table renders blank without it.
	cols := make([]map[string]any, 0, len(q.columns))
	for _, c := range q.columns {
		cols = append(cols, map[string]any{"selector": c.Selector, "text": c.Text, "type": c.Type})
	}
	spec["columns"] = cols
	return dashboardv2.DataQueryKind{
		Kind:    "Query",
		Group:   "yesoreyeram-infinity-datasource",
		Version: "v0",
		Datasource: &dashboardv2.Dashboardv2DataQueryKindDatasource{
			Name: &name,
		},
		Spec: spec,
	}, nil
}

// Ensure infinityQuery satisfies the interface at compile time.
var _ cog.Builder[dashboardv2.DataQueryKind] = infinityQuery{}

// InfinityTarget builds a READ target that GETs hosted JSON via the Infinity datasource (backend
// parser). dsName is the Infinity datasource NAME (matched by Grafana at render time). rootSelector
// optionally extracts a sub-array (e.g. "scenarios"); "" parses the whole doc. columns is the
// explicit column list — REQUIRED or the backend-parsed table renders blank.
func InfinityTarget(refID, url, dsName, rootSelector string, columns ...InfinityColumn) *dashboardv2.TargetBuilder {
	return dashboardv2.NewTargetBuilder().
		RefId(refID).
		Query(infinityQuery{refID: refID, url: url, source: "url", dsName: dsName, rootSelector: rootSelector, columns: columns})
}

// InfinityTargetPOST builds a POST target for Infinity datasource routes that require a JSON body
// (e.g. LangSmith /api/v1/runs/query). body is a raw JSON string sent as url_options.data.
// The Infinity backend parser receives it verbatim. All other parameters mirror InfinityTarget.
func InfinityTargetPOST(refID, url, dsName, rootSelector, body string, cols ...InfinityColumn) *dashboardv2.TargetBuilder {
	return dashboardv2.NewTargetBuilder().
		RefId(refID).
		Query(infinityQuery{
			refID:        refID,
			url:          url,
			source:       "url",
			dsName:       dsName,
			rootSelector: rootSelector,
			columns:      cols,
			method:       "POST",
			body:         body,
		})
}

// colOverrides returns the []DynamicConfigValue slice for all non-zero formatting fields on col,
// or nil if no overrides are needed. The returned slice is used with OverrideByName.
func colOverrides(col InfinityColumn) []dashboardv2.DynamicConfigValue {
	var props []dashboardv2.DynamicConfigValue
	if col.Unit != "" {
		props = append(props, dashboardv2.DynamicConfigValue{Id: "unit", Value: col.Unit})
	}
	if col.Decimals != nil {
		props = append(props, dashboardv2.DynamicConfigValue{Id: "decimals", Value: *col.Decimals})
	}
	if col.ColorMode != "" {
		props = append(props, dashboardv2.DynamicConfigValue{
			Id:    "custom.cellOptions",
			Value: map[string]any{"type": col.ColorMode},
		})
	}
	if len(col.Links) > 0 {
		links := make([]map[string]any, len(col.Links))
		for i, l := range col.Links {
			links[i] = map[string]any{"title": l.Title, "url": l.URL, "targetBlank": true}
		}
		props = append(props, dashboardv2.DynamicConfigValue{Id: "links", Value: links})
	}
	return props
}

// InfinityTablePanel builds a combined Infinity-target + table-visualization panel. It is the
// preferred constructor when per-column formatting (unit, decimals, color-mode, data-links) is
// needed: cols are used BOTH as the Infinity extraction column list AND as sources for
// OverrideByName field overrides on the table viz. The override name matches col.Text (the column
// header the Infinity backend parser uses). Columns with no formatting fields set produce no
// override. Existing InfinityTarget / TablePanel callers are unaffected.
func InfinityTablePanel(title, refID, url, dsName, rootSelector string, cols ...InfinityColumn) *dashboardv2.PanelBuilder {
	viz := table.NewVisualizationV2Builder()
	for _, col := range cols {
		if props := colOverrides(col); len(props) > 0 {
			viz = viz.OverrideByName(col.Text, props)
		}
	}
	target := InfinityTarget(refID, url, dsName, rootSelector, cols...)
	return dashboardv2.NewPanelBuilder().
		Title(title).
		Visualization(viz).
		Data(dashboardv2.NewQueryGroupBuilder().Target(target))
}

// infinityInlineTarget builds the one-row INLINE-DATA Infinity target the action board hangs its
// fetch buttons on. The single `_` field's single cell becomes the Actions cell — the buttons
// render there regardless of generator reachability (no network query).
func infinityInlineTarget(dsName string) *dashboardv2.TargetBuilder {
	return dashboardv2.NewTargetBuilder().
		RefId("A").
		Query(infinityQuery{refID: "A", source: "inline", inlineData: `[{"_":" "}]`, dsName: dsName})
}

// FetchAction builds one native fetch action — the VERIFIED write button: a CLIENT-SIDE browser
// POST of a FIXED JSON body to the absolute (HTTPS) generator URL. confirmation:"", oneClick:false
// (the proven firing shape). NO request headers are set — Grafana cannot set headers on fetch
// actions, so the body goes as text/plain and the control plane decodes JSON content-type-agnostically.
// ${var}/${__data.fields...} interpolation is UNRELIABLE (400) — pass ONLY discrete fixed bodies.
func FetchAction(title, url, jsonBody string) *dashboardv2.ActionBuilder {
	return dashboardv2.NewActionBuilder().
		Title(title).
		Type(dashboardv2.ActionTypeFetch).
		Confirmation("").
		OneClick(false).
		Fetch(dashboardv2.NewFetchOptionsBuilder().
			Method(dashboardv2.HttpRequestMethodPOST).
			Url(url).
			Body(jsonBody))
}

// ActionBoardPanel builds the VERIFIED firing widget: a one-row INLINE-DATA table whose single
// Actions cell holds N type:"fetch" actions with fixed JSON bodies. The shape (browser
// click-verified on Grafana 13.1):
//   - fieldConfig.defaults.actions = [the fetch actions]   (via table .Actions)
//   - fieldConfig.defaults.custom.cellOptions.type:"actions" (via .CellOptions actions)
//   - options.showHeader:false                              (the button labels ARE the UI)
//
// type:"infinity" actions and "auto"+oneClick cells DO NOT fire — never use them.
func ActionBoardPanel(title, dsName string, actions ...*dashboardv2.ActionBuilder) *dashboardv2.PanelBuilder {
	builders := make([]cog.Builder[dashboardv2.Action], len(actions))
	for i, a := range actions {
		builders[i] = a
	}
	cellOpts := common.TableCellOptions{TableActionsCellOptions: common.NewTableActionsCellOptions()}
	return dashboardv2.NewPanelBuilder().
		Title(title).
		Visualization(table.NewVisualizationV2Builder().
			Actions(builders).
			CellOptions(cellOpts).
			ShowHeader(false)).
		Data(dashboardv2.NewQueryGroupBuilder().Target(infinityInlineTarget(dsName)))
}

// ActionButtonPanel is retained as an alias of the verified firing shape so existing callers keep
// compiling — it now builds the inline-table Actions-cell board (NOT the dead text-panel variant).
// Prefer ActionBoardPanel directly.
func ActionButtonPanel(title, dsName string, actions ...*dashboardv2.ActionBuilder) *dashboardv2.PanelBuilder {
	return ActionBoardPanel(title, dsName, actions...)
}
