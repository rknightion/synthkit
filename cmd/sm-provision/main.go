// SPDX-License-Identifier: AGPL-3.0-only

// Command sm-provision is the one-shot idempotent control-plane provisioner for
// synthkit's Synthetic Monitoring checks.
//
// # What it does
//
//  1. Reads every blueprint YAML from BLUEPRINTS dir.
//  2. Loads each one via blueprint.Load(data, runner.Catalog()).
//  3. Collects every synthetic_monitoring ConstructInstance from the Resolved set.
//  4. Registers ONE offline private probe (idempotent: list first, add only if absent).
//  5. For each check: add if absent by (job, target) key; update if present.
//     alertSensitivity is always "none" on the registered check (predecessor line 139).
//
// # Environment variables
//
//	GC_SM_URL    SM API base URL (required, e.g. https://synthetic-monitoring-api.grafana.net)
//	GC_SM_TOKEN  SM API bearer token (required)
//	BLUEPRINTS   directory of blueprint YAML files (default: ./blueprints)
//	PROBE_NAME   offline probe name (default: sm.DefaultProbeName)
//	PROBE_REGION probe region string (default: sm.DefaultProbeRegion)
//	DRY_RUN      if "true" (default), print planned operations and exit without POSTing
//
// # Exit codes
//
//	0  success (or DRY_RUN preview)
//	1  configuration / API error
package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/construct/sm"
	"github.com/rknightion/synthkit/internal/runner"
)

// smTimeoutMs is the registered check Timeout in milliseconds; matches
// sm.smTimeoutSeconds (3 s) converted to the integer millisecond form the SM API
// expects.
const smTimeoutMs = 3000

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	// ── env config ────────────────────────────────────────────────────────────
	smURL := os.Getenv("GC_SM_URL")
	smToken := os.Getenv("GC_SM_TOKEN")
	blueprintsDir := envDefault("BLUEPRINTS", "./blueprints")
	probeName := envDefault("PROBE_NAME", sm.DefaultProbeName)
	probeRegion := envDefault("PROBE_REGION", sm.DefaultProbeRegion)
	dryRun := envDefault("DRY_RUN", "true") == "true"

	if !dryRun && (smURL == "" || smToken == "") {
		return fmt.Errorf("GC_SM_URL and GC_SM_TOKEN are required when DRY_RUN=false")
	}

	// ── load blueprints and collect SM checks ─────────────────────────────────
	type checkSpec struct {
		job         string
		target      string
		frequencyMs int
	}
	var checks []checkSpec

	entries, err := os.ReadDir(blueprintsDir)
	if err != nil {
		return fmt.Errorf("read blueprints dir %q: %w", blueprintsDir, err)
	}
	reg := runner.Catalog()
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		path := filepath.Join(blueprintsDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read blueprint %q: %w", path, err)
		}
		resolved, err := blueprint.Load(data, reg)
		if err != nil {
			return fmt.Errorf("load blueprint %q: %w", path, err)
		}
		for _, ci := range resolved.Constructs {
			if ci.Kind != sm.Kind {
				continue
			}
			cfg, ok := ci.Config.(*sm.Config)
			if !ok {
				return fmt.Errorf("blueprint %q: synthetic_monitoring config type assertion failed (got %T)", path, ci.Config)
			}
			for _, ch := range cfg.Checks {
				target := ch.Target
				if target == "" {
					target = "https://" + ch.Name + ".example.com/health"
				}
				freq := ch.FrequencyMs
				if freq == 0 {
					freq = 60000
				}
				checks = append(checks, checkSpec{
					job:         ch.Name,
					target:      target,
					frequencyMs: freq,
				})
			}
		}
	}

	if len(checks) == 0 {
		fmt.Println("no synthetic_monitoring checks found in any blueprint; nothing to provision")
		return nil
	}

	// ── dry-run: print and exit ───────────────────────────────────────────────
	if dryRun {
		fmt.Printf("[DRY RUN] Would register offline probe %q (region=%s lat=%.4f lon=%.4f)\n",
			probeName, probeRegion, sm.DefaultProbeLat, sm.DefaultProbeLon)
		fmt.Printf("[DRY RUN] Would upsert %d check(s):\n", len(checks))
		for _, ch := range checks {
			fmt.Printf("  job=%q target=%q frequency=%dms alertSensitivity=none\n",
				ch.job, ch.target, ch.frequencyMs)
		}
		return nil
	}

	// ── live provisioning ─────────────────────────────────────────────────────
	client := &smClient{
		base:  smURL,
		token: smToken,
		hc:    &http.Client{Timeout: 30 * time.Second},
	}

	// Step 1: ensure the offline private probe (idempotent).
	probeID, err := ensureProbe(client, probeName, probeRegion)
	if err != nil {
		return fmt.Errorf("ensure probe: %w", err)
	}

	// Step 2: list existing checks; build (job, target) index.
	existing, err := client.listChecks()
	if err != nil {
		return fmt.Errorf("list checks: %w", err)
	}
	byKey := make(map[string]smCheck, len(existing))
	for _, ch := range existing {
		byKey[checkKey(ch.Job, ch.Target)] = ch
	}

	// Step 3: upsert each check.
	created, updated := 0, 0
	for _, spec := range checks {
		ch := smCheck{
			Job:       spec.job,
			Target:    spec.target,
			Frequency: spec.frequencyMs,
			Timeout:   smTimeoutMs,
			Enabled:   true,
			Probes:    []int{probeID},
			Labels:    []smLabel{},
			// "none" avoids the forbidden legacy-alerting path at registration.
			// The real alertSensitivity value is stamped on the sm_check_info metric
			// by the data-plane emitter — independent of this field (predecessor line 139).
			AlertSensitivity: "none",
			BasicMetricsOnly: true,
			Settings:         map[string]any{"http": map[string]any{"method": "GET", "ipVersion": "V4"}},
		}
		if prev, exists := byKey[checkKey(spec.job, spec.target)]; exists {
			ch.ID = prev.ID
			if err := client.updateCheck(ch); err != nil {
				return fmt.Errorf("update check job=%q target=%q: %w", spec.job, spec.target, err)
			}
			fmt.Printf("updated  check job=%q target=%q id=%d\n", spec.job, spec.target, prev.ID)
			updated++
		} else {
			if err := client.addCheck(ch); err != nil {
				return fmt.Errorf("add check job=%q target=%q: %w", spec.job, spec.target, err)
			}
			fmt.Printf("created  check job=%q target=%q\n", spec.job, spec.target)
			created++
		}
	}
	fmt.Printf("done: %d created, %d updated\n", created, updated)
	return nil
}

// ensureProbe lists existing probes and returns the ID of the probe named probeName,
// creating it if absent. Never creates a duplicate (predecessor lines 91–111).
func ensureProbe(c *smClient, probeName, probeRegion string) (int, error) {
	probes, err := c.listProbes()
	if err != nil {
		return 0, fmt.Errorf("list probes: %w", err)
	}
	for _, p := range probes {
		if p.Name == probeName {
			fmt.Printf("offline probe %q already exists id=%d\n", probeName, p.ID)
			return p.ID, nil
		}
	}
	// Probe absent — create it.
	created, err := c.addProbe(smProbe{
		Name:      probeName,
		Public:    false,
		Latitude:  sm.DefaultProbeLat,
		Longitude: sm.DefaultProbeLon,
		Region:    probeRegion,
	})
	if err != nil {
		return 0, fmt.Errorf("add probe %q: %w", probeName, err)
	}
	fmt.Printf("created offline probe %q id=%d\n", probeName, created.ID)
	return created.ID, nil
}

// checkKey is the idempotency key for a check (predecessor line 119).
func checkKey(job, target string) string { return job + "\x00" + target }

// envDefault returns the value of env var key, or def if unset or empty.
func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
