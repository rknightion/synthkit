// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/dashgen"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/telemetryspec"
	"github.com/rknightion/synthkit/internal/telemetryspec/profiles"
	"github.com/rknightion/synthkit/internal/workload/app"

	"github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
	"github.com/grafana/grafana-foundation-sdk/go/loki"
	"github.com/grafana/grafana-foundation-sdk/go/prometheus"

	"github.com/rknightion/synthkit/dashboard"
)

// Status colors (good / serious / critical) and the first two categorical slots, light and dark,
// from the validated reference palette. Status colors carry state only; series colors carry data.
const (
	colorGood     = "#0ca30c"
	colorSerious  = "#ec835a"
	colorCritical = "#d03b3b"
	colorNeutral  = "#6e6e6a"
)

var (
	seriesLatency = [2]string{"#2a78d6", "#3987e5"} // slot 1 blue: light, dark
	seriesErrors  = [2]string{"#eb6834", "#d95926"} // slot 2 orange: light, dark
)

// buildControlPlaneDashboard is the instructor console: status tiles, one card per scenario
// with Start/Stop buttons, live impact charts per affected service, and a collapsed row for
// fleet-wide controls. It requires -action-mode infinity so buttons run server-side.
func buildControlPlaneDashboard(o opts) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("control-plane", "Control Plane")
	if err != nil {
		return dashboard.Dashboard{}, err
	}
	d.Folder = o.folder
	d.Builder.TimeSettings(dashboardv2.NewTimeSettingsBuilder().From("now-3h").To("now").AutoRefresh("30s"))

	scenarios, err := loadScenarios(o.blueprints)
	if err != nil {
		return dashboard.Dashboard{}, err
	}
	impact, err := deriveImpact(scenarios)
	if err != nil {
		return dashboard.Dashboard{}, err
	}
	act := o.action()
	colored := func(title, path, body, color string) *dashboardv2.ActionBuilder {
		return dashboard.WithActionColor(act(title, path, body), color)
	}

	dashboard.AddPanel(&d, "header", dashboard.TextPanel("",
		"Start a scenario before its session and stop it afterwards: scenarios never stop on their own. "+
			"Buttons apply within seconds; the impact charts follow about a minute later."))

	dashboard.AddPanel(&d, "status-active", dashboard.StatusTile("Active scenarios",
		dashboard.InfinityTarget("A", "/control/state", o.dsName, "",
			dashboard.Col("active_scenarios", "Active scenarios", "string")),
		colorSerious,
		dashboard.TextMapping{Value: "[]", Text: "None", Color: colorGood}))
	dashboard.AddPanel(&d, "status-volume", dashboard.StatTile("Volume multiplier", "none",
		dashboard.InfinityTarget("A", "/control/state", o.dsName, "",
			dashboard.Col("volume_multiplier", "Volume", "number"))))
	dashboard.AddPanel(&d, "status-delivery", dashboard.StatusTile("Telemetry delivery",
		dashboard.InfinityTarget("A", "/control/readiness", o.dsName, "",
			dashboard.Col("live_ready", "Delivery", "string")),
		"",
		dashboard.TextMapping{Value: "true", Text: "Healthy", Color: colorGood},
		dashboard.TextMapping{Value: "false", Text: "Degraded", Color: colorCritical}))

	var cards []dashboard.Cell
	var services []string
	seen := map[string]bool{}
	for i, s := range scenarios {
		info := fmt.Sprintf("card-%d", i)
		buttons := fmt.Sprintf("buttons-%d", i)
		affects := make([]string, len(s.Targets))
		for j, t := range s.Targets {
			affects[j] = "`" + t + "`"
			key := s.Blueprint + "\x00" + t
			if !seen[key] {
				seen[key] = true
				services = append(services, key)
			}
		}
		body := `{"scenario":"` + s.id() + `"}`
		dashboard.AddPanel(&d, info, dashboard.TextPanel(s.Title, strings.Join([]string{
			s.Summary,
			"",
			"Affects: " + strings.Join(affects, ", "),
		}, "\n")))
		dashboard.AddPanel(&d, buttons, dashboard.ActionBoardPanel("", o.dsName,
			colored("Start "+s.Title, "/control/scenarios/activate", body, colorSerious),
			colored("Stop", "/control/scenarios/deactivate", body, colorGood)))
		cards = append(cards, dashboard.At(info, 16, 4), dashboard.At(buttons, 8, 4))
	}

	var charts []dashboard.Cell
	width := 24
	if n := 2 * len(services); n > 0 {
		width = max(24/n, 6)
	}
	for _, key := range services {
		bp, svc, _ := strings.Cut(key, "\x00")
		q := impact.queries[key]
		lat, errs := "latency-"+bp+"-"+svc, "errors-"+bp+"-"+svc
		if q.latency != "" {
			dashboard.AddPanel(&d, lat, dashboard.EChartsPanel(svc+": mean response time", "s", lineChartCode(seriesLatency, "ms"), promTarget(o.promUID, q.latency)))
			charts = append(charts, dashboard.At(lat, width, 8))
		}
		if q.errors != "" {
			dashboard.AddPanel(&d, errs, dashboard.EChartsPanel(svc+": error log events per minute", "short", lineChartCode(seriesErrors, "/min"), lokiTarget(o.lokiUID, q.errors)))
			charts = append(charts, dashboard.At(errs, width, 8))
		}
	}

	dashboard.AddPanel(&d, "fleet", dashboard.ActionBoardPanel("", o.dsName,
		colored("Stop all scenarios", "/control/scenarios", `{"active_scenarios":[]}`, colorGood),
		colored("Normal volume (1x)", "/control/load", `{"volume_multiplier":1}`, colorNeutral),
		colored("Peak volume (3x)", "/control/load", `{"volume_multiplier":3}`, colorSerious)))

	dashboard.WithRows(&d,
		dashboard.Section("Status", dashboard.At("header", 24, 2),
			dashboard.At("status-active", 12, 4), dashboard.At("status-volume", 6, 4), dashboard.At("status-delivery", 6, 4)),
		dashboard.Section("Scenarios", cards...),
		dashboard.Section("Live impact", charts...),
		dashboard.CollapsedSection("Fleet controls", dashboard.At("fleet", 24, 3)),
	)
	return d, nil
}

func promTarget(uid, expr string) *dashboardv2.TargetBuilder {
	return dashboardv2.NewTargetBuilder().RefId("A").
		Query(prometheus.NewQueryV2Builder().Expr(expr).Range(true).
			Datasource(dashboardv2.NewDashboardv2DataQueryKindDatasourceBuilder().Name(uid)))
}

func lokiTarget(uid, expr string) *dashboardv2.TargetBuilder {
	return dashboardv2.NewTargetBuilder().RefId("A").
		Query(loki.NewQueryV2Builder().Expr(expr).
			Datasource(dashboardv2.NewDashboardv2DataQueryKindDatasourceBuilder().Name(uid)))
}

type impactQuery struct{ latency, errors string }
type impactSurface struct {
	queries   map[string]impactQuery
	manifests map[string]*dashboard.Manifest
}

// deriveImpact gates each choice twice: the node must declare the source and the dry-run
// manifest must contain its family and selector keys. The node-declared HTTP server
// histogram is the first usable latency source: it measures that service's requests.
// The gateway's HTTP request histogram has no service selector, while Derive opts
// spanmetrics into its inventory even though the live producer defaults them off.
// A node with no directly emitted, service-scoped latency family gets no panel.
func deriveImpact(scenarios []scenario) (impactSurface, error) {
	out := impactSurface{queries: map[string]impactQuery{}, manifests: map[string]*dashboard.Manifest{}}
	paths := map[string]string{}
	for _, s := range scenarios {
		paths[s.Blueprint] = s.Path
	}
	for bp, path := range paths {
		m, err := dashgen.Derive(path)
		if err != nil {
			return impactSurface{}, fmt.Errorf("derive %s: %w", path, err)
		}
		out.manifests[bp] = m
		data, err := os.ReadFile(path)
		if err != nil {
			return impactSurface{}, err
		}
		res, err := blueprint.Load(data, runner.Catalog())
		if err != nil {
			return impactSurface{}, err
		}
		for _, wi := range res.Workloads {
			cfg, ok := wi.Config.(*app.Config)
			if !ok {
				continue
			}
			for _, node := range cfg.Services {
				key := bp + "\x00" + node.Name
				q := out.queries[key]
				metrics := append([]string(nil), metricNames(node.Metrics)...)
				logSources := logNames(node.Logs)
				for _, pn := range node.Profiles {
					p, ok := profiles.Lookup(pn)
					if !ok {
						continue
					} // blueprint.Load already rejects unknown profiles.
					metrics = append(metrics, metricNames(p.Metrics)...)
					logSources = append(logSources, logNames(p.Logs)...)
				}
				if contains(metrics, "http_server_request_duration_seconds") {
					q.latency = histogramMean(m, "http_server_request_duration_seconds", node.Name)
				}
				for _, source := range logSources {
					for _, ls := range m.LogSources {
						if ls.Source != source || !contains(ls.StreamKeys, "service_name") || !contains(ls.StreamKeys, "level") || !contains(ls.StreamKeys, "source") {
							continue
						}
						labels := []string{fmt.Sprintf("service_name=%q", node.Name), fmt.Sprintf("source=%q", source), `level="error"`}
						if contains(ls.StreamKeys, "blueprint") {
							labels = append(labels, fmt.Sprintf("blueprint=%q", m.Label))
						}
						q.errors = "sum(count_over_time({" + strings.Join(labels, ",") + "}[1m]))"
						break
					}
					if q.errors != "" {
						break
					}
				}
				out.queries[key] = q
			}
		}
	}
	return out, nil
}

func metricNames(specs []telemetryspec.MetricSpec) []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Name)
	}
	return out
}
func logNames(specs []telemetryspec.LogSpec) []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Source)
	}
	return out
}
func contains(xs []string, x string) bool { return slices.Contains(xs, x) }

func histogramMean(m *dashboard.Manifest, family, service string) string {
	sig, ok := m.Metric(family)
	if !ok || (sig.Instrument != dashboard.HistogramClassic && !sig.Dual) || !contains(sig.LabelKeys, "service") {
		return ""
	}
	labels := []string{fmt.Sprintf("service=%q", service)}
	if sig.Scope == dashboard.ScopeBlueprint {
		labels = append(labels, fmt.Sprintf("blueprint=%q", m.Label))
	}
	sel := "{" + strings.Join(labels, ",") + "}"
	return fmt.Sprintf("sum(rate(%s_sum%s[5m])) / sum(rate(%s_count%s[5m]))", family, sel, family, sel)
}

// lineChartCode is the Business Charts getOption body for one series: a thin line with a light
// area fill, a crosshair tooltip, a zero-based single y-axis and recessive grid, in the given
// categorical color stepped for the current theme. suffix "ms" converts seconds to milliseconds.
func lineChartCode(color [2]string, suffix string) string {
	format := `(v) => (v == null ? '-' : Math.round(v) + ' ` + suffix + `')`
	if suffix == "ms" {
		format = `(v) => (v == null ? '-' : Math.round(v * 1000) + ' ms')`
	}
	return strings.Join([]string{
		`const dark = context.grafana.theme.isDark;`,
		`const color = dark ? '` + color[1] + `' : '` + color[0] + `';`,
		`const ink = dark ? '#c3c2b7' : '#52514e';`,
		`const fmt = ` + format + `;`,
		`const frame = context.panel.data.series[0];`,
		`const time = frame && frame.fields.find((f) => f.type === 'time');`,
		`const value = frame && frame.fields.find((f) => f.type === 'number');`,
		`if (!time || !value) {`,
		`  return { title: { text: 'No data in this time range', left: 'center', top: 'middle', textStyle: { color: ink, fontSize: 13, fontWeight: 'normal' } } };`,
		`}`,
		`const t = Array.from(time.values);`,
		`const v = Array.from(value.values);`,
		`return {`,
		`  backgroundColor: 'transparent',`,
		`  grid: { left: 8, right: 16, top: 12, bottom: 4, containLabel: true },`,
		`  tooltip: { trigger: 'axis', axisPointer: { type: 'line' }, valueFormatter: fmt },`,
		`  xAxis: { type: 'time', axisLabel: { color: ink }, axisLine: { lineStyle: { color: ink, opacity: 0.3 } }, splitLine: { show: false } },`,
		`  yAxis: { type: 'value', min: 0, axisLabel: { color: ink, formatter: fmt }, splitLine: { lineStyle: { color: ink, opacity: 0.12 } } },`,
		`  series: [{ type: 'line', data: t.map((x, i) => [x, v[i]]), showSymbol: false, smooth: 0.3,`,
		`    lineStyle: { width: 2, color: color }, itemStyle: { color: color }, areaStyle: { color: color, opacity: 0.12 } }],`,
		`};`,
	}, "\n")
}
