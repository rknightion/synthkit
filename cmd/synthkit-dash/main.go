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
	namespace := flag.String("namespace", "", "optional UID namespace (1-12 of a-z, 0-9, _): derives every generated folder, dashboard, rule and link identity from (namespace, legacy UID) so independent installs of one blueprint cannot collide; absent = legacy UIDs")
	verifyInventory := flag.String("verify-inventory", "", "generated panel inventory JSON to verify")
	observations := flag.String("observations", "", "read-only live query observations JSON (requires -verify-inventory)")
	flag.Parse()

	ns, err := namespaceFromFlags(*namespace)
	if err != nil {
		log.Fatalf("synthkit-dash: %v", err)
	}
	if *verifyInventory != "" || *observations != "" {
		if ns != nil {
			log.Fatal("synthkit-dash: -namespace applies to generation only; verification reads the effective UIDs recorded in the inventory")
		}
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
	if ns != nil && *folder != "" {
		if _, err := ns.derive(*folder); err != nil {
			log.Fatalf("synthkit-dash: -folder: %v", err)
		}
	}
	if err := generateNamespaced(*bp, *out, *integrations, *folder, datasources, ns); err != nil {
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
	return generateNamespaced(bpPath, outDir, integPath, folder, datasources, nil)
}

// outputFile is one generated file, held in memory until every artifact has been built and
// checked, so a refusal never leaves partial output behind.
type outputFile struct {
	path string
	data []byte
}

// generateNamespaced builds every artifact first and writes them only once all of them
// succeeded. A nil ns is the legacy behaviour and produces byte-identical files.
func generateNamespaced(bpPath, outDir, integPath, folder string, datasources datasourceFlags, ns *uidNamespace) error {
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
	if folder == "" {
		folder = m.Blueprint + "-dashboards"
	}

	// Always emit the thin index, then the per-blueprint metrics dashboard, then each registered template.
	var dashboards []dashboard.Dashboard
	idx, err := dashboard.IndexDashboard(m, cfg)
	if err != nil {
		return err
	}
	dashboards = append(dashboards, idx)
	md, err := dashboard.MetricsDashboard(m)
	if err != nil {
		return fmt.Errorf("metrics dashboard: %w", err)
	}
	dashboards = append(dashboards, md)
	for _, tpl := range templates {
		d, terr := tpl(m)
		if terr != nil {
			return terr
		}
		dashboards = append(dashboards, d)
	}

	plan, err := newNamespacePlan(ns, folder, dashboards)
	if err != nil {
		return err
	}
	var files []outputFile
	folderFile, err := renderFolder(outDir, plan.folder, m.Blueprint, ns)
	if err != nil {
		return err
	}
	files = append(files, folderFile)
	dashFiles, panels, err := renderDashboards(outDir, dashboards, plan, datasources)
	if err != nil {
		return err
	}
	files = append(files, dashFiles...)

	inventoryName := m.Blueprint + "-panel-inventory.json"
	rulesName := m.Blueprint + "-rules.json"
	if ns != nil {
		inventoryName = m.Blueprint + "-" + ns.label() + "-panel-inventory.json"
		rulesName = m.Blueprint + "-" + ns.label() + "-rules.json"
	}
	inventory, err := json.MarshalIndent(dashgen.PanelInventory{Panels: panels}, "", "  ")
	if err != nil {
		return err
	}
	inventoryFile := outputFile{filepath.Join(outDir, inventoryName), inventory}

	// Recording/alert rules for blueprints that define them.
	var rulesFile *outputFile
	if ruleFn, ok := rulesCatalog()[m.Blueprint]; ok {
		if groups := ruleFn(m); len(groups) > 0 {
			if err := plan.applyRules(groups); err != nil {
				return err
			}
			rb, rerr := dashboard.RenderRules(m.Blueprint, groups)
			if rerr != nil {
				return fmt.Errorf("render rules: %w", rerr)
			}
			rulesFile = &outputFile{filepath.Join(outDir, rulesName), rb}
		}
	}

	// Everything is built and checked: now write.
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	for _, f := range append(files, inventoryFile) {
		if err := os.WriteFile(f.path, f.data, 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", f.path)
	}
	fmt.Printf("generated %d dashboard(s), %d panel(s)\n", len(dashboards), len(panels))
	if rulesFile != nil {
		if err := os.WriteFile(rulesFile.path, rulesFile.data, 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", rulesFile.path)
	}
	return nil
}

// namespaceFromFlags returns nil when -namespace was not passed. An explicitly empty value is
// refused rather than treated as absent, so an unset shell variable cannot silently select
// the colliding legacy UIDs.
func namespaceFromFlags(value string) (*uidNamespace, error) {
	set := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "namespace" {
			set = true
		}
	})
	if !set {
		return nil, nil
	}
	return newNamespace(value)
}

func renderFolder(dir, uid, blueprint string, ns *uidNamespace) (outputFile, error) {
	title := "Synthkit — " + blueprint
	if ns != nil {
		// Folder titles are unique within a parent folder, so two namespaces need distinct titles.
		title += " (" + ns.label() + ")"
	}
	manifest := folderv1.Manifest(uid, folderv1.NewFolderBuilder(title).
		Description("Generated synthkit dashboards for blueprint "+blueprint))
	resource, err := manifest.Build()
	if err != nil {
		return outputFile{}, err
	}
	data, err := json.MarshalIndent(resource, "", "  ")
	if err != nil {
		return outputFile{}, err
	}
	return outputFile{filepath.Join(dir, uid+"-folder.json"), data}, nil
}

// renderDashboards renders every dashboard under its effective UID and folder, rewrites the
// internal references (namespaced runs only), binds datasources and extracts the panel
// inventory from the final bytes, so the inventory always names the emitted resources.
func renderDashboards(dir string, dashboards []dashboard.Dashboard, plan *namespacePlan, datasources datasourceFlags) ([]outputFile, []dashgen.Panel, error) {
	rendered := make([][]byte, len(dashboards))
	names := make([]string, len(dashboards))
	for i, d := range dashboards {
		legacyUID := d.UID
		if plan.ns != nil {
			d.UID = plan.uids[legacyUID]
		}
		d.Folder = plan.folder
		js, err := dashboard.Render(d)
		if err != nil {
			return nil, nil, err
		}
		rendered[i], names[i] = js, d.UID
	}
	if plan.ns != nil {
		roots := make([]map[string]any, len(rendered))
		for i, js := range rendered {
			if err := json.Unmarshal(js, &roots[i]); err != nil {
				return nil, nil, fmt.Errorf("dashboard %q: %w", names[i], err)
			}
			if err := plan.collectNavTags(roots[i]); err != nil {
				return nil, nil, fmt.Errorf("dashboard %q: %w", names[i], err)
			}
		}
		plan.compileLinks()
		for i, root := range roots {
			plan.rewrite(root)
			js, err := reencode(root)
			if err != nil {
				return nil, nil, fmt.Errorf("dashboard %q: %w", names[i], err)
			}
			rendered[i] = js
		}
	}
	var files []outputFile
	var panels []dashgen.Panel
	for i, js := range rendered {
		js, err := bindDatasources(js, datasources)
		if err != nil {
			return nil, nil, fmt.Errorf("dashboard %q: %w", names[i], err)
		}
		files = append(files, outputFile{filepath.Join(dir, strings.ReplaceAll(names[i], "/", "-")+".json"), js})
		inventory, err := dashgen.InventoryFromDashboard(js)
		if err != nil {
			return nil, nil, err
		}
		panels = append(panels, inventory.Panels...)
	}
	return files, panels, nil
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
