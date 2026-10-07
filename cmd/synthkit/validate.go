// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/bpsource"
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/runner"
)

type validateDiagnostic struct {
	Severity string `json:"severity"`
	Source   string `json:"source"`
	Stage    string `json:"stage"`
	Detail   string `json:"detail"`
}

type validateBlueprint struct {
	Name        string              `json:"name"`
	Provenance  bpsource.Provenance `json:"provenance"`
	Cardinality int                 `json:"cardinality"`
	Estimated   bool                `json:"estimated"`
	Diagnostics []string            `json:"diagnostics"`
}

type validateReport struct {
	OK          bool                 `json:"ok"`
	Blueprints  []validateBlueprint  `json:"blueprints"`
	Diagnostics []validateDiagnostic `json:"diagnostics"`
}

// runValidate deliberately bypasses runtime startup, live credential validation,
// provisioning, profiling and control persistence. Sources are already-fetched local
// snapshots only; even DRY_RUN=false cannot enable a network sink here.
func runValidate(envPath string, out io.Writer) error {
	report := validateReport{OK: true, Blueprints: []validateBlueprint{}, Diagnostics: []validateDiagnostic{}}
	addError := func(source, stage string, err error) {
		report.OK = false
		report.Diagnostics = append(report.Diagnostics, validateDiagnostic{"error", source, stage, err.Error()})
	}
	cfg, err := config.Load(envPath)
	if err != nil {
		addError("config", "load", err)
	} else {
		// Read state explicitly: a corrupt source snapshot is a validation error rather
		// than silently falling back to a fresh-install set. No Store write methods run.
		var state control.State
		data, readErr := os.ReadFile(cfg.SnapshotPath)
		if readErr != nil && !os.IsNotExist(readErr) {
			addError("sources", "read", readErr)
		}
		if readErr == nil {
			if err := json.Unmarshal(data, &state); err != nil {
				addError("sources", "load", err)
			}
		}
		reg := runner.Catalog()
		mgr := bpsource.NewManager(bpsource.Options{
			BakedDir: cfg.BlueprintsDir, BlueprintNames: cfg.BlueprintNames, DataDir: cfg.BlueprintDataDir, Registry: reg,
			RuntimeLimits: blueprint.RuntimeLimits{MasterTick: cfg.MasterTick, MaxDPMPerSeries: cfg.MaxDPMPerSeries},
			Config:        bpsource.NewStoreSourceConfig(control.NewStore(cfg.SnapshotPath)),
		})
		loaded, diags := mgr.ResolveOffline()
		for _, d := range diags {
			report.Diagnostics = append(report.Diagnostics, validateDiagnostic{d.Severity, d.Source, d.Stage, d.Detail})
			if d.Severity == "error" {
				report.OK = false
			}
		}
		if err := mgr.SelectionError(); err != nil {
			addError("selection", "selection", err)
		}
		resolved := make([]*blueprint.Resolved, 0, len(loaded))
		for _, l := range loaded {
			resolved = append(resolved, l.Resolved)
		}
		if err := blueprint.ValidateSet(resolved); err != nil {
			addError("set", "validate", err)
		}
		for _, l := range loaded {
			warnings := append([]string{}, l.Resolved.Warnings...)
			count, estimated := bpsource.ProjectCardinality(reg, l.Resolved)
			if count < 0 {
				addError(l.Resolved.Name, "cardinality", fmt.Errorf("series projection unavailable"))
			}
			report.Blueprints = append(report.Blueprints, validateBlueprint{l.Resolved.Name, l.Provenance, count, estimated, warnings})
		}
		sort.Slice(report.Blueprints, func(i, j int) bool { return report.Blueprints[i].Name < report.Blueprints[j].Name })
	}
	if err := json.NewEncoder(out).Encode(report); err != nil {
		return fmt.Errorf("validate: write JSON: %w", err)
	}
	if !report.OK {
		return fmt.Errorf("validate: blueprint set failed (see JSON diagnostics)")
	}
	return nil
}
