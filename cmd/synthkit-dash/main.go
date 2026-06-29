// SPDX-License-Identifier: AGPL-3.0-only

// Command synthkit-dash generates Grafana v2 dashboards for a blueprint's SYNTHETIC
// telemetry. It resolves the blueprint, derives the signal Manifest (internal/dashgen),
// runs the registered templates, and writes GA v2 JSON. Validate/push/snapshot stay gcx
// (see dashboards/CLAUDE.md). The synthetic-emit binary never imports this tree.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/rknightion/synthkit/dashboard"
	"github.com/rknightion/synthkit/internal/dashgen"
)

func main() {
	bp := flag.String("blueprint", "", "path to the blueprint YAML (required)")
	out := flag.String("out", "", "output directory for generated JSON (required)")
	integrations := flag.String("integrations", "", "optional integrations config YAML (thin index deep-links)")
	folder := flag.String("folder", "", "optional Grafana folder UID to place every dashboard in (must already exist)")
	flag.Parse()

	if *bp == "" || *out == "" {
		log.Fatal("synthkit-dash: -blueprint and -out are required")
	}
	if err := generate(*bp, *out, *integrations, *folder); err != nil {
		log.Fatalf("synthkit-dash: %v", err)
	}
}

func generate(bpPath, outDir, integPath, folder string) error {
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

	// Always emit the thin index, then the per-blueprint metrics dashboard, then each registered template.
	idx, err := dashboard.IndexDashboard(m, cfg)
	if err != nil {
		return err
	}
	if err := write(outDir, idx, folder); err != nil {
		return err
	}
	md, err := dashboard.MetricsDashboard(m)
	if err != nil {
		return fmt.Errorf("metrics dashboard: %w", err)
	}
	if err := write(outDir, md, folder); err != nil {
		return err
	}
	for _, tpl := range templates {
		d, terr := tpl(m)
		if terr != nil {
			return terr
		}
		if werr := write(outDir, d, folder); werr != nil {
			return werr
		}
	}

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

func write(dir string, d dashboard.Dashboard, folder string) error {
	d.Folder = folder
	js, err := dashboard.Render(d)
	if err != nil {
		return err
	}
	name := strings.ReplaceAll(d.UID, "/", "-") + ".json"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, js, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", path)
	return nil
}
