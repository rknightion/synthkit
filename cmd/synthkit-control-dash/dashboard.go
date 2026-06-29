// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grafana/grafana-foundation-sdk/go/dashboardv2"

	"github.com/rknightion/synthkit/dashboard"
	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/runner"
)

// activationActions builds one discrete FIXED-body fetch button per scenario (no ${__data}
// interpolation — that 400s), plus a trailing "Clear all incidents" button. Each scenario's id is
// baked into its own JSON body literal so the button fires deterministically.
func activationActions(scenarios []scenario, postURL string) []*dashboardv2.ActionBuilder {
	acts := make([]*dashboardv2.ActionBuilder, 0, len(scenarios)+1)
	for _, s := range scenarios {
		body := `{"active_scenarios":["` + s.id() + `"]}`
		acts = append(acts, dashboard.FetchAction(s.Title, postURL, body))
	}
	acts = append(acts, dashboard.FetchAction("Clear all incidents", postURL, `{"active_scenarios":[]}`))
	return acts
}

// scenario is one enumerated incident, with the blueprint it came from. id = "<bpName>/<name>".
type scenario struct {
	Blueprint string
	Name      string
	Title     string
}

func (s scenario) id() string { return s.Blueprint + "/" + s.Name }

// loadScenarios walks every *.yaml in dir, resolves it against the runner catalog, and enumerates
// its scenarios as "<bpName>/<name>" ids with a display title. Results are sorted (blueprint, name)
// for deterministic output. A blueprint that fails to load is fatal — a control dashboard built
// against a stale/broken blueprint set would silently mis-fire.
func loadScenarios(dir string) ([]scenario, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read blueprints dir %q: %w", dir, err)
	}
	var out []scenario
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), rerr)
		}
		res, lerr := blueprint.Load(data, runner.Catalog())
		if lerr != nil {
			return nil, fmt.Errorf("load %s: %w", e.Name(), lerr)
		}
		for _, sc := range res.Scenarios {
			title := sc.Title
			if title == "" {
				title = sc.Name
			}
			out = append(out, scenario{Blueprint: res.Name, Name: sc.Name, Title: title})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Blueprint != out[j].Blueprint {
			return out[i].Blueprint < out[j].Blueprint
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// buildControlDashboard assembles the customer-control dashboard from the enumerated blueprints.
// Panels:
//  1. Header           — markdown intro / how to use it
//  2. Load presets     — ACTION BOARD: fixed-body fetch buttons POST /control/load (Idle/Normal/Peak/Stress)
//  3. Current state    — READ table: Infinity GET /control/state (live knobs, explicit columns)
//  4. Incidents        — READ table: Infinity GET /control/schema?audience=customer, root "scenarios"
//  5. Activate incident — ACTION BOARD: one discrete fixed-body button per enumerated scenario, plus Clear all
//
// Reads use RELATIVE paths (the Infinity datasource's Base URL prefixes them) so the dashboard is
// host/scheme-agnostic. Writes are ABSOLUTE browser fetches (--write-base-url, HTTPS). GET routes
// are open; POST routes require HTTP Basic auth the browser challenges natively — no embedded creds.
func buildControlDashboard(o opts) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("synthkit-customer-control", "synthkit — Customer Control")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	scenarios, err := loadScenarios(o.blueprints)
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	writeURL := func(p string) string { return joinURL(o.writeBaseURL, p) }

	// 1. Header — what this is + how to use it.
	dashboard.AddPanel(&d, "header", dashboard.TextPanel("", strings.Join([]string{
		"## synthkit — self-serve controls",
		"",
		"- **Load presets** scale the whole synthetic estate's volume coherently.",
		"- **Activate incident** fires a curated failure scenario (replaces any active incident); " +
			"**Clear all incidents** returns to steady state.",
		"- **Current state** / **Incidents** show the live picture. Changes apply within a few seconds.",
	}, "\n")))

	// 2. Load presets — VERIFIED action board (fixed-body fetch buttons → /control/load).
	dashboard.AddPanel(&d, "volume-presets", dashboard.ActionBoardPanel("Load presets", o.dsName,
		dashboard.FetchAction("Idle (0.2×)", writeURL("/control/load"), `{"volume_multiplier":0.2}`),
		dashboard.FetchAction("Normal (1×)", writeURL("/control/load"), `{"volume_multiplier":1}`),
		dashboard.FetchAction("Peak (3×)", writeURL("/control/load"), `{"volume_multiplier":3}`),
		dashboard.FetchAction("Stress (10×)", writeURL("/control/load"), `{"volume_multiplier":10}`),
	))

	// 3. Current effective state (READ; relative — datasource Base URL prefixes it; explicit columns).
	dashboard.AddPanel(&d, "current-state", dashboard.TablePanel("Current state",
		dashboard.InfinityTarget("A", "/control/state", o.dsName, "",
			dashboard.Col("volume_multiplier", "Volume ×", "number"),
			dashboard.Col("peak_rps_per_env", "Peak RPS/env", "number"),
			dashboard.Col("platform_call_fraction", "Platform fraction", "number"),
			dashboard.Col("rum_multiplier", "RUM ×", "number"),
			dashboard.Col("series_cap", "Series cap", "number"),
		)))

	// 4. Incident catalogue (READ; the "scenarios" array from the customer-projected schema).
	dashboard.AddPanel(&d, "scenarios", dashboard.TablePanel("Incidents",
		dashboard.InfinityTarget("A", "/control/schema?audience=customer", o.dsName, "scenarios",
			dashboard.Col("blueprint", "Blueprint", "string"),
			dashboard.Col("name", "Name", "string"),
			dashboard.Col("title", "Title", "string"),
			dashboard.Col("active", "Active", "string"),
		)))

	// 5. Activate incident — VERIFIED action board: ONE discrete fixed-body button per enumerated
	//    scenario (no ${__data} interpolation), plus a "Clear all incidents" button.
	dashboard.AddPanel(&d, "scenario-activate", dashboard.ActionBoardPanel("Activate incident", o.dsName,
		activationActions(scenarios, writeURL("/control/scenarios"))...,
	))

	dashboard.WithGrid(&d, "header", "volume-presets", "current-state", "scenarios", "scenario-activate")
	return d, nil
}

// joinURL concatenates a base URL and a path, preserving any query string on path.
// Paths like "/control/schema?audience=customer" pass through unchanged.
func joinURL(base, p string) string {
	if base == "" {
		return p
	}
	u := strings.TrimRight(base, "/")
	if len(p) > 0 && p[0] != '/' {
		p = "/" + p
	}
	return u + p
}
