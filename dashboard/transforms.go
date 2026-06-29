// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"github.com/grafana/grafana-foundation-sdk/go/cog"
	"github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
	"github.com/grafana/grafana-foundation-sdk/go/prometheus"
	"github.com/grafana/grafana-foundation-sdk/go/table"
)

// OrganizeOptions describes how the "organize" transformation should reshape table columns.
//   - Exclude: column names to hide (excludeByName map value → true).
//   - Rename:  display-name overrides; key=original column name, value=new display name.
//   - Order:   desired column order; each name maps to its 0-based index (indexByName).
//     Columns absent from Order are left in their natural position.
type OrganizeOptions struct {
	Exclude []string
	Rename  map[string]string
	Order   []string
}

// PromTableTarget builds a Prometheus instant-query target in "table" format.
// Use this with MergeTablePanel so each query contributes one column-set per label set
// (instant=true + format=table is the shape Grafana's merge transformation expects).
// refID should be unique per target within the same panel (e.g. "A", "B", "C"…).
func PromTableTarget(expr, refID string) *dashboardv2.TargetBuilder {
	return dashboardv2.NewTargetBuilder().
		RefId(refID).
		Query(prometheus.NewQueryV2Builder().
			Expr(expr).
			Format(prometheus.PromQueryFormatTable).
			Instant(true))
}

// MergeTablePanel builds a table panel that joins N instant Prometheus queries into a
// single row-per-label table via the Grafana "merge" + "organize" transformation chain.
//
// targets should be built with PromTableTarget (instant=true, format=table).
// organize controls column rename/hide/reorder on the merged result.
func MergeTablePanel(title string, targets []*dashboardv2.TargetBuilder, organize OrganizeOptions) *dashboardv2.PanelBuilder {
	qg := dashboardv2.NewQueryGroupBuilder()
	for _, t := range targets {
		qg.Target(t)
	}
	qg.Transformations([]cog.Builder[dashboardv2.TransformationKind]{
		mergeTransformation(),
		organizeTransformation(organize),
	})
	return dashboardv2.NewPanelBuilder().
		Title(title).
		Visualization(table.NewVisualizationV2Builder()).
		Data(qg)
}

// mergeTransformation returns the "merge" transformation (no options required).
func mergeTransformation() *dashboardv2.TransformationBuilder {
	return dashboardv2.NewTransformationBuilder().
		Group("merge").
		Options(map[string]any{})
}

// organizeTransformation converts OrganizeOptions into the Grafana "organize" transformation.
func organizeTransformation(opts OrganizeOptions) *dashboardv2.TransformationBuilder {
	// excludeByName: map column name → true
	excludeByName := make(map[string]bool, len(opts.Exclude))
	for _, col := range opts.Exclude {
		excludeByName[col] = true
	}

	// renameByName: pass through as-is (nil-safe)
	renameByName := opts.Rename
	if renameByName == nil {
		renameByName = map[string]string{}
	}

	// indexByName: assign 0-based index from the Order slice
	indexByName := make(map[string]int, len(opts.Order))
	for i, col := range opts.Order {
		indexByName[col] = i
	}

	options := map[string]any{
		"excludeByName": excludeByName,
		"renameByName":  renameByName,
		"indexByName":   indexByName,
	}
	return dashboardv2.NewTransformationBuilder().
		Group("organize").
		Options(options)
}
