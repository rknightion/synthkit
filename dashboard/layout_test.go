// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"strings"
	"testing"
)

func TestWithTabs(t *testing.T) {
	d, err := NewDashboard("test-tabs", "Test Dashboard")
	if err != nil {
		t.Fatalf("NewDashboard: %v", err)
	}

	AddPanel(&d, "panel-a", TimeseriesPanel("Panel A", "short", PromTarget(`up`, "up")))
	AddPanel(&d, "panel-b", TimeseriesPanel("Panel B", "reqps", PromTarget(`http_requests_total`, "rps")))

	WithTabs(&d,
		Tab("Overview", "panel-a"),
		Tab("Traffic", "panel-b"),
	)

	out, err := Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	json := string(out)
	if !strings.Contains(json, "Overview") {
		t.Errorf("rendered JSON missing tab title %q", "Overview")
	}
	if !strings.Contains(json, "Traffic") {
		t.Errorf("rendered JSON missing tab title %q", "Traffic")
	}
}

func TestTabSpec(t *testing.T) {
	spec := Tab("My Tab", "p1", "p2", "p3")
	if spec.Title != "My Tab" {
		t.Errorf("Title = %q, want %q", spec.Title, "My Tab")
	}
	if len(spec.PanelIDs) != 3 {
		t.Errorf("len(PanelIDs) = %d, want 3", len(spec.PanelIDs))
	}
}

// Rich layout: a tab built from named Sections with variable-size Cells renders a RowsLayout whose
// rows carry the section titles and a GridLayout of explicitly-sized items.
func TestTabbedRowsLayout(t *testing.T) {
	d, _ := NewDashboard("rich", "Rich")
	AddPanel(&d, "k1", StatPanel("K1", PromTarget(`a`, "")))
	AddPanel(&d, "k2", StatPanel("K2", PromTarget(`b`, "")))
	AddPanel(&d, "big", TimeseriesPanel("Big", "short", PromTarget(`c`, "")))
	WithTabs(&d,
		Tabbed("Service",
			Section("KPIs", Tile("k1"), Tile("k2")),
			Section("Detail", Full("big")),
		),
	)
	out, err := Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	for _, want := range []string{"RowsLayout", "KPIs", "Detail", `"width": 4`, `"width": 24`} {
		if !strings.Contains(s, want) {
			t.Errorf("rich layout JSON missing %q", want)
		}
	}
}

// RepeatSection attaches a row-level repeat (mode=variable) so Grafana clones the row per value
// of the named template variable — the predecessor's row-repeat-by-variable pattern.
func TestRepeatSectionRowRepeat(t *testing.T) {
	d, _ := NewDashboard("rep", "Repeat")
	AddPanel(&d, "uc-cost", StatPanel("Cost", PromTarget(`a`, "")))
	WithTabs(&d,
		Tabbed("By use case",
			RepeatSection("Per use case", "use_case", Tile("uc-cost")),
		),
	)
	out, err := Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	// The row must carry a repeat with the variable name and mode "variable".
	for _, want := range []string{`"repeat"`, `"value": "use_case"`, `"mode": "variable"`} {
		if !strings.Contains(s, want) {
			t.Errorf("repeat row JSON missing %q", want)
		}
	}
	// A plain Section must NOT emit a repeat.
	d2, _ := NewDashboard("norep", "NoRepeat")
	AddPanel(&d2, "p", StatPanel("P", PromTarget(`a`, "")))
	WithTabs(&d2, Tabbed("T", Section("S", Tile("p"))))
	out2, _ := Render(d2)
	if strings.Contains(string(out2), `"repeat"`) {
		t.Errorf("plain Section should not emit a repeat option")
	}
}

// ConditionalSection attaches a data-presence conditional rendering group so the row is shown
// only when its panels return data. A plain Section must NOT emit any conditionalRendering field.
func TestConditionalSectionDataPresence(t *testing.T) {
	d, _ := NewDashboard("cond", "Conditional")
	AddPanel(&d, "storm", TimeseriesPanel("Storm", "short", PromTarget(`errors > 0`, "err")))
	WithTabs(&d,
		Tabbed("Degradation",
			ConditionalSection("Fallback Storm", Full("storm")),
		),
	)
	out, err := Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	// The row must carry a conditionalRendering group with a data condition.
	for _, want := range []string{`"conditionalRendering"`, `"ConditionalRenderingData"`, `"value": true`} {
		if !strings.Contains(s, want) {
			t.Errorf("conditional row JSON missing %q", want)
		}
	}
	// A plain Section must NOT emit any conditionalRendering field.
	d2, _ := NewDashboard("nocond", "NoConditional")
	AddPanel(&d2, "p", StatPanel("P", PromTarget(`a`, "")))
	WithTabs(&d2, Tabbed("T", Section("S", Tile("p"))))
	out2, _ := Render(d2)
	if strings.Contains(string(out2), `"conditionalRendering"`) {
		t.Errorf("plain Section should not emit a conditionalRendering field")
	}
}

// gridForCells wraps to a new line when a row of cells exceeds 24 columns.
func TestGridForCellsWraps(t *testing.T) {
	// three 12-wide cells: first two on row y=0, third wraps to the next line.
	cells := []Cell{Half("a"), Half("b"), Half("c")}
	g := gridForCells(cells)
	kind, err := g.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(kind.Spec.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(kind.Spec.Items))
	}
	// Item c must start at x=0 on a lower row than the first two.
	cY := kind.Spec.Items[2].Spec.Y
	if cY == 0 {
		t.Errorf("third 12-wide cell should wrap to a new row (y>0), got y=%d", cY)
	}
}
