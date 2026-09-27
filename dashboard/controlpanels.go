// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"github.com/grafana/grafana-foundation-sdk/go/common"
	"github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
	"github.com/grafana/grafana-foundation-sdk/go/stat"
)

// EChartsPluginID is the Business Charts (Apache ECharts) panel plugin.
const EChartsPluginID = "volkovlabs-echarts-panel"

// echartsViz builds the Business Charts VizConfig directly; the SDK ships no builder for it.
type echartsViz struct {
	code string
	unit string
}

func (e echartsViz) Build() (dashboardv2.VizConfigKind, error) {
	unit := e.unit
	return dashboardv2.VizConfigKind{
		Kind:  "VizConfig",
		Group: EChartsPluginID,
		Spec: dashboardv2.VizConfigSpec{
			Options: map[string]any{
				"editorMode": "code",
				"renderer":   "canvas",
				"editor":     map[string]any{"format": "auto"},
				"getOption":  e.code,
			},
			FieldConfig: dashboardv2.FieldConfigSource{
				Defaults:  dashboardv2.FieldConfig{Unit: &unit},
				Overrides: []dashboardv2.Dashboardv2FieldConfigSourceOverrides{},
			},
		},
	}, nil
}

// EChartsPanel builds a Business Charts panel whose getOption code receives the panel's query
// frames as context.panel.data.series.
func EChartsPanel(title, unit, code string, target *dashboardv2.TargetBuilder) *dashboardv2.PanelBuilder {
	return dashboardv2.NewPanelBuilder().
		Title(title).
		Visualization(echartsViz{code: code, unit: unit}).
		Data(dashboardv2.NewQueryGroupBuilder().Target(target))
}

// TextMapping maps one exact string value to display text and a color.
type TextMapping struct {
	Value, Text, Color string
}

// StatusTile is a stat panel for a string value, colored by exact-value mappings with an optional
// regex fallback color for any other value (the value text is kept).
func StatusTile(title string, target *dashboardv2.TargetBuilder, fallbackColor string, maps ...TextMapping) *dashboardv2.PanelBuilder {
	options := map[string]dashboardv2.ValueMappingResult{}
	for i, m := range maps {
		text, color, idx := m.Text, m.Color, int32(i)
		options[m.Value] = dashboardv2.ValueMappingResult{Text: &text, Color: &color, Index: &idx}
	}
	mappings := []dashboardv2.ValueMapping{{ValueMap: &dashboardv2.ValueMap{Type: dashboardv2.MappingTypeValue, Options: options}}}
	if fallbackColor != "" {
		color, idx := fallbackColor, int32(len(maps))
		mappings = append(mappings, dashboardv2.ValueMapping{RegexMap: &dashboardv2.RegexMap{
			Type:    dashboardv2.MappingTypeRegex,
			Options: dashboardv2.Dashboardv2RegexMapOptions{Pattern: ".+", Result: dashboardv2.ValueMappingResult{Color: &color, Index: &idx}},
		}})
	}
	return dashboardv2.NewPanelBuilder().
		Title(title).
		Visualization(stat.NewVisualizationV2Builder().
			ReduceOptions(common.NewReduceDataOptionsBuilder().Calcs([]string{"lastNotNull"}).Fields("/.*/")).
			ColorMode(common.BigValueColorModeBackground).
			GraphMode(common.BigValueGraphModeNone).
			TextMode(common.BigValueTextModeValue).
			Mappings(mappings)).
		Data(dashboardv2.NewQueryGroupBuilder().Target(target))
}

// WithActionColor sets an action button's background color (hex).
func WithActionColor(a *dashboardv2.ActionBuilder, hex string) *dashboardv2.ActionBuilder {
	return a.Style(dashboardv2.NewDashboardv2ActionStyleBuilder().BackgroundColor(hex))
}
