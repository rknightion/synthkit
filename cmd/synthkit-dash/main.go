// SPDX-License-Identifier: AGPL-3.0-only

// Command synthkit-dash generates Grafana v2 dashboards for a blueprint's SYNTHETIC
// telemetry. It resolves the blueprint, derives the signal Manifest (internal/dashgen),
// runs the registered templates, and writes GA v2 JSON. Validate/push/snapshot stay gcx
// (see dashboards/CLAUDE.md). The synthetic-emit binary never imports this tree.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grafana/grafana-foundation-sdk/go/folderv1"
	"gopkg.in/yaml.v3"

	"github.com/rknightion/synthkit/dashboard"
	"github.com/rknightion/synthkit/internal/dashgen"
)

func main() {
	bp := flag.String("blueprint", "", "path to the blueprint YAML (required)")
	out := flag.String("out", "", "output directory for generated JSON (required)")
	integrations := flag.String("integrations", "", "optional integrations config YAML (thin index deep-links)")
	folder := flag.String("folder", "", "Grafana folder UID (default: <blueprint>-dashboards; the generated resource creates it)")
	datasources := datasourceFlags{}
	flag.Var(&datasources, "datasource", "explicit datasource mapping in group=name form (repeat for every query group)")
	verifyInventory := flag.String("verify-inventory", "", "generated panel inventory JSON to verify")
	observations := flag.String("observations", "", "read-only live query observations JSON (requires -verify-inventory)")
	flag.Parse()

	if *verifyInventory != "" || *observations != "" {
		if *verifyInventory == "" || *observations == "" {
			log.Fatal("synthkit-dash: -verify-inventory and -observations must be supplied together")
		}
		if err := verify(*verifyInventory, *observations); err != nil {
			log.Fatalf("synthkit-dash: %v", err)
		}
		return
	}
	if *bp == "" || *out == "" {
		log.Fatal("synthkit-dash: -blueprint and -out are required")
	}
	if err := generate(*bp, *out, *integrations, *folder, datasources); err != nil {
		log.Fatalf("synthkit-dash: %v", err)
	}
}

// datasourceFlags accepts explicit query-group-to-datasource-name mappings. Grafana
// dashboard v2 uses datasource names in its query references; requiring a mapping
// prevents same-type datasource resolution from silently selecting the wrong one.
type datasourceFlags map[string]string

func (d *datasourceFlags) String() string {
	keys := make([]string, 0, len(*d))
	for group := range *d {
		keys = append(keys, group)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, group := range keys {
		pairs = append(pairs, group+"="+(*d)[group])
	}
	return strings.Join(pairs, ",")
}

func (d *datasourceFlags) Set(raw string) error {
	group, name, ok := strings.Cut(raw, "=")
	if !ok || group == "" || name == "" || strings.ContainsAny(group, " \t") {
		return fmt.Errorf("datasource must be group=name, got %q", raw)
	}
	if *d == nil {
		*d = datasourceFlags{}
	}
	if existing, exists := (*d)[group]; exists && existing != name {
		return fmt.Errorf("datasource group %q was specified more than once", group)
	}
	(*d)[group] = name
	return nil
}

func generate(bpPath, outDir, integPath, folder string, datasources datasourceFlags) error {
	m, err := dashgen.Derive(bpPath)
	if err != nil {
		return err
	}
	var cfg dashboard.IntegrationsConfig
	if integPath != "" {
		data, rerr := os.ReadFile(integPath)
		if rerr != nil {
			return rerr
		}
		if uerr := yaml.Unmarshal(data, &cfg); uerr != nil {
			return uerr
		}
	}

	templates := templateCatalog()[m.Blueprint]
	if len(templates) == 0 {
		log.Printf("synthkit-dash: no templates registered for blueprint %q — generating only the thin index", m.Blueprint)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if folder == "" {
		folder = m.Blueprint + "-dashboards"
	}
	if err := writeFolder(outDir, folder, m.Blueprint); err != nil {
		return err
	}
	var panels []dashgen.Panel

	// Always emit the thin index, then the per-blueprint metrics dashboard, then each registered template.
	idx, err := dashboard.IndexDashboard(m, cfg)
	if err != nil {
		return err
	}
	idxPanels, err := write(outDir, idx, folder, datasources)
	if err != nil {
		return err
	}
	panels = append(panels, idxPanels...)
	md, err := dashboard.MetricsDashboard(m)
	if err != nil {
		return fmt.Errorf("metrics dashboard: %w", err)
	}
	metricPanels, err := write(outDir, md, folder, datasources)
	if err != nil {
		return err
	}
	panels = append(panels, metricPanels...)
	for _, tpl := range templates {
		d, terr := tpl(m)
		if terr != nil {
			return terr
		}
		templatePanels, werr := write(outDir, d, folder, datasources)
		if werr != nil {
			return werr
		}
		panels = append(panels, templatePanels...)
	}
	inventoryPath := filepath.Join(outDir, m.Blueprint+"-panel-inventory.json")
	inventory, ierr := json.MarshalIndent(dashgen.PanelInventory{Panels: panels}, "", "  ")
	if ierr != nil {
		return ierr
	}
	if ierr := os.WriteFile(inventoryPath, inventory, 0o644); ierr != nil {
		return ierr
	}
	fmt.Printf("wrote %s\n", inventoryPath)
	fmt.Printf("generated %d dashboard(s), %d panel(s)\n", 2+len(templates), len(panels))

	// Emit recording/alert rules for blueprints that define them.
	if ruleFn, ok := rulesCatalog()[m.Blueprint]; ok {
		groups := ruleFn(m)
		if len(groups) > 0 {
			rb, rerr := dashboard.RenderRules(m.Blueprint, groups)
			if rerr != nil {
				return fmt.Errorf("render rules: %w", rerr)
			}
			rulesPath := filepath.Join(outDir, m.Blueprint+"-rules.json")
			if werr := os.WriteFile(rulesPath, rb, 0o644); werr != nil {
				return werr
			}
			fmt.Printf("wrote %s\n", rulesPath)
		}
	}
	return nil
}

func writeFolder(dir, uid, blueprint string) error {
	manifest := folderv1.Manifest(uid, folderv1.NewFolderBuilder("Synthkit — "+blueprint).
		Description("Generated synthkit dashboards for blueprint "+blueprint))
	resource, err := manifest.Build()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(resource, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, uid+"-folder.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", path)
	return nil
}

func write(dir string, d dashboard.Dashboard, folder string, datasources datasourceFlags) ([]dashgen.Panel, error) {
	d.Folder = folder
	js, err := dashboard.Render(d)
	if err != nil {
		return nil, err
	}
	js, err = bindDatasources(js, datasources)
	if err != nil {
		return nil, fmt.Errorf("dashboard %q: %w", d.UID, err)
	}
	name := strings.ReplaceAll(d.UID, "/", "-") + ".json"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, js, 0o644); err != nil {
		return nil, err
	}
	fmt.Printf("wrote %s\n", path)
	inventory, err := dashgen.InventoryFromDashboard(js)
	if err != nil {
		return nil, err
	}
	return inventory.Panels, nil
}

// bindDatasources stamps every emitted DataQuery with its explicitly supplied
// datasource name. It fails closed when an emitted query group has no mapping.
func bindDatasources(raw []byte, datasources datasourceFlags) ([]byte, error) {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	var missing []string
	var visit func(any)
	visit = func(v any) {
		switch node := v.(type) {
		case map[string]any:
			if node["kind"] == "DataQuery" {
				group, _ := node["group"].(string)
				name := datasources[group]
				if name == "" {
					missing = append(missing, group)
				} else {
					node["datasource"] = map[string]any{"name": name}
				}
			}
			for _, child := range node {
				visit(child)
			}
		case []any:
			for _, child := range node {
				visit(child)
			}
		}
	}
	visit(root)
	if len(missing) > 0 {
		sort.Strings(missing)
		missing = compact(missing)
		return nil, fmt.Errorf("missing explicit datasource mapping for query group(s): %s", strings.Join(missing, ", "))
	}
	return json.MarshalIndent(root, "", "  ")
}

func compact(in []string) []string {
	if len(in) < 2 {
		return in
	}
	out := in[:1]
	for _, item := range in[1:] {
		if item != out[len(out)-1] {
			out = append(out, item)
		}
	}
	return out
}

func verify(inventoryPath, observationsPath string) error {
	inventoryData, err := os.ReadFile(inventoryPath)
	if err != nil {
		return err
	}
	var inventory dashgen.PanelInventory
	if err := json.Unmarshal(inventoryData, &inventory); err != nil {
		return fmt.Errorf("decode panel inventory: %w", err)
	}
	observationsData, err := os.ReadFile(observationsPath)
	if err != nil {
		return err
	}
	var observations dashgen.ObservationFile
	if err := json.Unmarshal(observationsData, &observations); err != nil {
		return fmt.Errorf("decode observations: %w", err)
	}
	report, err := dashgen.Verify(inventory, observations)
	if err != nil {
		return err
	}
	output, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(output))
	return nil
}
