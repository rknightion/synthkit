// SPDX-License-Identifier: AGPL-3.0-only

// Package matrix is the k3d capture lab's permutation-matrix support library: the
// per-permutation result record each parallel job writes, the capture-instance identity
// elision rule the k3d producer applies, and the single combined report the matrix run ends
// in. It is a TEST harness and is never on the synthetic-data path.
package matrix

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ResultVersion is the version of the per-permutation result record.
const ResultVersion = "synthkit.lab.permutation-result/v1alpha1"

// Outcome is what one permutation job ended in. The taxonomy exists because an empty corpus
// entry has two opposite causes and the combined report must never conflate them:
//
//   - OutcomeFailed     the harness could not complete its own steps, so this run observed
//     nothing and makes NO claim about the permutation.
//   - OutcomeEmpty      the harness completed every step, the collector deployed and reported
//     ready, and the receiver then recorded zero requests for the whole
//     capture window. That is a finding ABOUT the permutation.
//   - OutcomePartial    evidence arrived but the permutation's declared acceptance predicate
//     was not satisfied inside the window.
//   - OutcomeCaptured   the acceptance predicate was satisfied.
//   - OutcomeSkipped    the permutation was not selected for this run.
//
// Only OutcomeCaptured is success. Anything else keeps the run off a green verdict.
type Outcome string

const (
	OutcomeCaptured Outcome = "captured"
	OutcomePartial  Outcome = "partial"
	OutcomeEmpty    Outcome = "empty"
	OutcomeFailed   Outcome = "failed"
	OutcomeSkipped  Outcome = "skipped"
)

var knownOutcomes = map[Outcome]struct{}{
	OutcomeCaptured: {}, OutcomePartial: {}, OutcomeEmpty: {},
	OutcomeFailed: {}, OutcomeSkipped: {},
}

// Succeeded reports whether the outcome is the single success value.
func (o Outcome) Succeeded() bool { return o == OutcomeCaptured }

// Observed reports whether the harness got far enough for the run to say anything about the
// permutation itself. A failed or skipped job did not.
func (o Outcome) Observed() bool {
	return o == OutcomeCaptured || o == OutcomePartial || o == OutcomeEmpty
}

// Check is one acceptance predicate the permutation declared and the result of evaluating it.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Counts is the shape volume the receiver decoded for this permutation.
type Counts struct {
	Metrics int `json:"metrics"`
	Logs    int `json:"logs"`
	Traces  int `json:"traces"`
}

// Total is the sum of every decoded shape count.
func (c Counts) Total() int { return c.Metrics + c.Logs + c.Traces }

// Result is one permutation job's complete record. Every job writes one, on the success path
// and on every failure path, because a missing result file is itself indistinguishable from a
// job that never started.
type Result struct {
	ResultVersion string `json:"result_version"`
	Permutation   string `json:"permutation"`
	Title         string `json:"title"`
	Summary       string `json:"summary,omitempty"`
	Collector     string `json:"collector"`
	// CollectorVersion is the pinned version of whatever ships the telemetry in this
	// permutation, which is not always a Helm chart.
	CollectorVersion string   `json:"collector_version"`
	Substrate        string   `json:"substrate"`
	Cluster          string   `json:"cluster"`
	CorpusAreas      []string `json:"corpus_areas,omitempty"`
	// CaptureStatus is the permutation author's own claim about whether this permutation has
	// ever produced a curated corpus entry: "proven" or "unproven". It is provenance, not a
	// result, and the report prints it so an unproven permutation is never read as settled.
	CaptureStatus string `json:"capture_status,omitempty"`
	// Deviations records every place this permutation departs from the deployment it models.
	Deviations []string `json:"deviations,omitempty"`

	RunID           string `json:"run_id"`
	StartedAt       string `json:"started_at"`
	FinishedAt      string `json:"finished_at"`
	DurationSeconds int    `json:"duration_seconds"`

	Outcome Outcome `json:"outcome"`
	// Phase is the last phase the job entered. On a failure it names where the harness
	// stopped, which is the whole difference between "the lab broke" and "nothing was sent".
	Phase         string `json:"phase"`
	FailureReason string `json:"failure_reason,omitempty"`
	ExitCode      int    `json:"exit_code"`

	CaptureWindowSeconds int            `json:"capture_window_seconds"`
	Checks               []Check        `json:"checks,omitempty"`
	Receipts             map[string]int `json:"receipts,omitempty"`
	Counts               Counts         `json:"counts"`

	// CandidatePath is the normalized corpus candidate this job produced. It is always inside
	// this permutation's own output directory: no job writes to reality-corpus, so parallel
	// jobs cannot race each other's corpus entries. Promotion is a separate, deliberate step.
	CandidatePath   string   `json:"candidate_path,omitempty"`
	RawPath         string   `json:"raw_path,omitempty"`
	DiagnosticPaths []string `json:"diagnostic_paths,omitempty"`

	// TeardownConfirmed records whether the worker verified its own cluster was gone rather
	// than assuming the delete worked. A leaked cluster silently steals a concurrency slot from
	// the next run, so the report states it rather than leaving it to the operator to notice.
	TeardownConfirmed string `json:"teardown_confirmed,omitempty"`
	// InstrumentEvidence is whether any producer-declared instrument type reached the receiver.
	// It never changes the outcome: whether the pinned collector sends remote-write metadata at
	// all is one of the things this lab measures.
	InstrumentEvidence string `json:"instrument_evidence,omitempty"`
}

// Validate rejects a record that could be misread. An unknown or absent outcome is the
// dangerous case: it would otherwise default to the zero value and print as a blank row.
func (r Result) Validate() error {
	if r.ResultVersion != ResultVersion {
		return fmt.Errorf("result_version: got %q, want %q", r.ResultVersion, ResultVersion)
	}
	if strings.TrimSpace(r.Permutation) == "" {
		return errors.New("permutation: must not be empty")
	}
	if _, ok := knownOutcomes[r.Outcome]; !ok {
		return fmt.Errorf("outcome: unknown value %q", r.Outcome)
	}
	if r.Outcome == OutcomeFailed && strings.TrimSpace(r.FailureReason) == "" {
		return errors.New("failure_reason: a failed permutation must say why")
	}
	if r.Outcome == OutcomeCaptured && strings.TrimSpace(r.CandidatePath) == "" {
		return errors.New("candidate_path: a captured permutation must name its candidate")
	}
	return nil
}

// ReceiptTotal is the number of decoded producer requests across every protocol.
func (r Result) ReceiptTotal() int {
	total := 0
	for _, count := range r.Receipts {
		total += count
	}
	return total
}

// FailedChecks lists the declared acceptance checks that did not pass.
func (r Result) FailedChecks() []string {
	out := make([]string, 0, len(r.Checks))
	for _, check := range r.Checks {
		if check.Status != "PASS" {
			label := check.Name
			if check.Detail != "" {
				label += " (" + check.Detail + ")"
			}
			out = append(out, label)
		}
	}
	return out
}

// WriteResult writes one result record as stable indented JSON with a trailing newline.
func WriteResult(path string, result Result) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}

// LoadResults reads every result record directly below dir, in deterministic permutation
// order. A file that does not parse or does not validate is returned as an error rather than
// dropped: a result the report cannot read must not silently shrink the matrix.
func LoadResults(dir string) ([]Result, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("results directory %q: %w", dir, err)
	}
	results := make([]Result, 0, len(entries))
	seen := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var result Result
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if err := result.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if previous, exists := seen[result.Permutation]; exists {
			return nil, fmt.Errorf("%s: duplicate result for permutation %q; already read from %s", path, result.Permutation, previous)
		}
		seen[result.Permutation] = path
		results = append(results, result)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("results directory %q: no permutation result records found", dir)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Permutation < results[j].Permutation })
	return results, nil
}
