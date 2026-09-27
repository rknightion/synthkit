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

// activationActions builds one discrete fixed-body POST button per scenario (no ${__data}
// interpolation — that 400s), plus a trailing "Clear all incidents" button. Each scenario's id is
// baked into its own JSON body literal so the button targets that scenario deterministically.
func activationActions(scenarios []scenario, act actionFunc) []*dashboardv2.ActionBuilder {
	acts := make([]*dashboardv2.ActionBuilder, 0, len(scenarios)+1)
	for _, s := range scenarios {
		body := `{"active_scenarios":["` + s.id() + `"]}`
		acts = append(acts, act(s.Title, "/control/scenarios", body))
	}
	acts = append(acts, act("Clear all incidents", "/control/scenarios", `{"active_scenarios":[]}`))
	return acts
}

// actionFunc builds one fixed-body POST button for a control path.
type actionFunc func(title, path, body string) *dashboardv2.ActionBuilder

func (o opts) action() actionFunc {
	return func(title, path, body string) *dashboardv2.ActionBuilder {
		url := joinURL(o.writeBaseURL, path)
		if o.actionMode == actionModeInfinity {
			return dashboard.InfinityAction(title, o.dsUID, url, body)
		}
		return dashboard.FetchAction(title, url, body)
	}
}

// scenario is one enumerated incident, with the blueprint it came from. id = "<bpName>/<name>".
type scenario struct {
	Blueprint string
	Path      string
	Name      string
	Title     string
	Summary   string
	Targets   []string // distinct effect targets, in declaration order
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
			var targets []string
			seen := map[string]bool{}
			for _, e := range sc.Effects {
				if e.Target != "" && !seen[e.Target] {
					seen[e.Target] = true
					targets = append(targets, e.Target)
				}
			}
			out = append(out, scenario{Blueprint: res.Name, Path: filepath.Join(dir, e.Name()), Name: sc.Name, Title: title, Summary: sc.Summary, Targets: targets})
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
//  2. Load presets     — ACTION BOARD: fixed-body buttons POST /control/load (Idle/Normal/Peak/Stress)
//  3. Current state    — READ table: Infinity GET /control/state (live knobs, explicit columns)
//  4. Incidents        — READ table: Infinity GET /control/schema?audience=customer, root "scenarios"
//  5. Activate incident — ACTION BOARD: one discrete fixed-body button per enumerated scenario, plus Clear all
//
// Reads use RELATIVE paths (the Infinity datasource's Base URL prefixes them) so the dashboard is
// host/scheme-agnostic. Protected reads use Infinity datasource Basic auth. In fetch mode, writes
// use browser fetches and the browser's separate Basic challenge. In infinity mode, the datasource
// sends writes server-side with its stored credentials. No credentials are embedded in the JSON.
func buildControlDashboard(o opts) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("synthkit-customer-control", "synthkit — Customer Control")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	scenarios, err := loadScenarios(o.blueprints)
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	act := o.action()
	credentialNote := "- If an action prompts for credentials, use the synthkit control login only over this dashboard's trusted HTTPS origin."
	if o.actionMode == actionModeInfinity {
		credentialNote = "- Actions run through the Infinity datasource. Datasource query access permits authenticated control POSTs, so restrict datasource queries and dashboard access to operators."
	}

	// 1. Header — what this is + how to use it.
	dashboard.AddPanel(&d, "header", dashboard.TextPanel("", strings.Join([]string{
		"## synthkit — self-serve controls",
		"",
		"- **Load presets** scale the whole synthetic estate's volume coherently.",
		"- **Activate incident** fires a curated failure scenario (replaces any active incident); " +
			"**Clear all incidents** returns to steady state.",
		"- **Current state** / **Incidents** show the live picture. Changes apply within a few seconds.",
		credentialNote,
	}, "\n")))

	// 2. Load presets — VERIFIED action board (fixed-body buttons → /control/load).
	dashboard.AddPanel(&d, "volume-presets", dashboard.ActionBoardPanel("Load presets", o.dsName,
		act("Idle (0.2×)", "/control/load", `{"volume_multiplier":0.2}`),
		act("Normal (1×)", "/control/load", `{"volume_multiplier":1}`),
		act("Peak (3×)", "/control/load", `{"volume_multiplier":3}`),
		act("Stress (10×)", "/control/load", `{"volume_multiplier":10}`),
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
		activationActions(scenarios, act)...,
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
	u := strings.TrimSuffix(base, "/")
	if len(p) > 0 && p[0] != '/' {
		p = "/" + p
	}
	return u + p
}
