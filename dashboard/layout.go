// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
)

// TabSpec declares one tab. It has two modes:
//   - legacy: PanelIDs set → a uniform 2-col grid (Tab()).
//   - rich:   Rows set → a RowsLayout of named Sections, each a grid of variable-size Cells (Tabbed()).
type TabSpec struct {
	Title    string
	PanelIDs []string  // legacy uniform-grid mode
	Rows     []RowSpec // rich mode (takes precedence when non-empty)
}

// Tab builds a legacy uniform-grid TabSpec. Returns our TabSpec type, not the SDK's
// dashboardv2.Tab(...) builder — they live in separate packages so there is no shadowing.
func Tab(title string, panelIDs ...string) TabSpec {
	return TabSpec{Title: title, PanelIDs: panelIDs}
}

// Tabbed builds a rich TabSpec from one or more named Sections — the dense, varied layout the
// predecessor dashboards use (KPI tile strips, hero panels, full-width tables). Each Section becomes a
// titled row; Cells flow left-to-right in a 24-column grid, wrapping when a line fills.
func Tabbed(title string, rows ...RowSpec) TabSpec {
	return TabSpec{Title: title, Rows: rows}
}

// RowSpec is one named section within a rich tab: a title (rendered as the row header; "" for an
// untitled strip) and the ordered, variable-size Cells laid out in its grid. When Repeat is a
// template-variable name (no leading "$"), Grafana clones the whole row once per selected value of
// that variable, pinning the variable to a single value inside each clone — the predecessor's
// row-repeat-by-variable pattern (e.g. one stat row per use_case / env / provider).
// When ConditionalData is true the row is shown only when its panels return data (hidden when
// healthy / empty — the fallback-storm row pattern).
type RowSpec struct {
	Title           string
	Cells           []Cell
	Repeat          string // template-variable name to repeat this row by ("" = no repeat)
	ConditionalData bool   // when true, attach a data-presence conditional rendering group
}

// Section builds a RowSpec.
func Section(title string, cells ...Cell) RowSpec {
	return RowSpec{Title: title, Cells: cells}
}

// RepeatSection builds a RowSpec that Grafana clones once per value of the repeatVar template
// variable (mode=variable). repeatVar is the variable NAME without a leading "$" (e.g. "use_case").
// Inside each clone the variable is pinned to one value, so the cells' panel queries should
// reference that same variable (e.g. metadata_use_case=~"$use_case") to scope per-clone.
func RepeatSection(title, repeatVar string, cells ...Cell) RowSpec {
	return RowSpec{Title: title, Cells: cells, Repeat: repeatVar}
}

// ConditionalSection builds a RowSpec whose row carries a data-presence conditional rendering
// group: Grafana shows the row only when the panels inside it return data. The canonical use-case
// is a "fallback storm" degradation row that auto-hides when everything is healthy and auto-shows
// when a threshold-filtered query starts returning results.
func ConditionalSection(title string, cells ...Cell) RowSpec {
	return RowSpec{Title: title, Cells: cells, ConditionalData: true}
}

// Cell places one panel (by its AddPanel id) at a width W (1..24 grid columns) and height H (grid
// rows, ~30px each) within its section's grid.
type Cell struct {
	ID string
	W  int
	H  int
}

// Cell-size helpers — the common shapes, so templates read declaratively. Use At for a custom size.
func At(id string, w, h int) Cell { return Cell{ID: id, W: w, H: h} } // explicit
func Tile(id string) Cell         { return Cell{ID: id, W: 4, H: 4} } // KPI stat tile (6 across)
func Stat(id string) Cell         { return Cell{ID: id, W: 6, H: 5} } // wider stat (4 across)
func Third(id string) Cell        { return Cell{ID: id, W: 8, H: 8} } // 3 across
func Half(id string) Cell         { return Cell{ID: id, W: 12, H: 8} }
func TwoThirds(id string) Cell    { return Cell{ID: id, W: 16, H: 9} }
func Full(id string) Cell         { return Cell{ID: id, W: 24, H: 9} }  // full-width panel
func Tall(id string) Cell         { return Cell{ID: id, W: 24, H: 12} } // full-width tall (logs/tables)

// WithTabs attaches a TabsLayout to the dashboard. Each TabSpec becomes one SDK tab whose
// content is a GridLayout with one GridItem per panel ID (Width 12, Height 8, flowing left-to-right).
//
// The name passed to dashboardv2.GridItem MUST match the key used in AddPanel so the layout
// engine can resolve the panel reference.
func WithTabs(d *Dashboard, tabs ...TabSpec) {
	tabsBuilder := dashboardv2.Tabs()
	for _, spec := range tabs {
		tab := dashboardv2.Tab(spec.Title)
		if len(spec.Rows) > 0 {
			tab = tab.RowsLayout(rowsFor(spec.Rows))
		} else {
			tab = tab.GridLayout(gridFor(spec.PanelIDs))
		}
		tabsBuilder.Tab(tab)
	}
	d.Builder.TabsLayout(tabsBuilder)
}

// rowsFor builds a RowsLayout: one titled row per Section, each holding a GridLayout of its
// variable-size Cells. This is the structure the predecessor uses (TabsTab → Rows → Row → Grid) and is
// what gives a tab its dense, sectioned, varied-size feel.
func rowsFor(sections []RowSpec) *dashboardv2.RowsBuilder {
	rows := dashboardv2.Rows()
	for _, s := range sections {
		row := dashboardv2.Row(s.Title).GridLayout(gridForCells(s.Cells))
		if s.Repeat != "" {
			// mode defaults to "variable" (RepeatModeVariable); Value is the bare var name.
			row = row.Repeat(dashboardv2.NewRowRepeatOptionsBuilder().Value(s.Repeat))
		}
		if s.ConditionalData {
			// Attach a data-presence conditional rendering group: show the row only when
			// its panels return data. The group holds a single ConditionalRenderingData
			// item with Value(true) = "render when data is present".
			dataItem := dashboardv2.ConditionalRenderingVariableKindOrConditionalRenderingDataKindOrConditionalRenderingTimeRangeSizeKind{
				ConditionalRenderingDataKind: dashboardv2.NewConditionalRenderingDataKind(),
			}
			dataItem.ConditionalRenderingDataKind.Spec.Value = true
			group := dashboardv2.NewConditionalRenderingGroupBuilder().
				Visibility(dashboardv2.ConditionalRenderingGroupSpecVisibilityShow).
				Condition(dashboardv2.ConditionalRenderingGroupSpecConditionAnd).
				Item(dataItem)
			row = row.ConditionalRendering(group)
		}
		rows.Row(row)
	}
	return rows
}

// gridForCells packs Cells left-to-right in a 24-column grid, wrapping to a new line when the next
// cell would overflow the row width. Each cell keeps its own width/height, so a section can mix a
// strip of small KPI tiles with a full-width chart below. Defaults: W=12, H=8 when unset/invalid.
func gridForCells(cells []Cell) *dashboardv2.GridBuilder {
	grid := dashboardv2.Grid()
	x, y, lineH := 0, 0, 0
	for _, c := range cells {
		w, h := c.W, c.H
		if w <= 0 || w > 24 {
			w = 12
		}
		if h <= 0 {
			h = 8
		}
		if x+w > 24 {
			x = 0
			y += lineH
			lineH = 0
		}
		grid.Item(
			dashboardv2.GridItem(c.ID).
				X(int64(x)).Y(int64(y)).
				Width(int64(w)).Height(int64(h)),
		)
		x += w
		if h > lineH {
			lineH = h
		}
	}
	return grid
}

// WithGrid attaches a flat GridLayout (no tabs) to the dashboard — for landing pages such
// as the thin index. The v2 schema REQUIRES exactly one layout; a dashboard with panels but
// no layout set fails validation, so every dashboard MUST call WithTabs or WithGrid.
func WithGrid(d *Dashboard, panelIDs ...string) {
	d.Builder.GridLayout(gridFor(panelIDs))
}

// gridFor builds a GridLayout placing each panel id in a two-column grid (Width 12,
// Height 8, flowing left-to-right). The id MUST match the key used in AddPanel so the
// layout engine can resolve the panel reference.
func gridFor(panelIDs []string) *dashboardv2.GridBuilder {
	grid := dashboardv2.Grid()
	for i, id := range panelIDs {
		col := int64((i % 2) * 12) // two columns of width 12
		row := int64((i / 2) * 8)
		grid.Item(
			dashboardv2.GridItem(id).
				X(col).Y(row).
				Width(12).Height(8),
		)
	}
	return grid
}
