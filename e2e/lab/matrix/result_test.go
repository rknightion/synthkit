// SPDX-License-Identifier: AGPL-3.0-only

package matrix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRejectsRecordsThatCouldBeMisread(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Result)
		want   string
	}{
		"unknown outcome": {
			mutate: func(r *Result) { r.Outcome = "green" },
			want:   "outcome",
		},
		"absent outcome": {
			mutate: func(r *Result) { r.Outcome = "" },
			want:   "outcome",
		},
		"failed without a reason": {
			mutate: func(r *Result) { r.Outcome = OutcomeFailed; r.FailureReason = "" },
			want:   "failure_reason",
		},
		"captured without a candidate": {
			mutate: func(r *Result) { r.Outcome = OutcomeCaptured; r.CandidatePath = "" },
			want:   "candidate_path",
		},
		"wrong version": {
			mutate: func(r *Result) { r.ResultVersion = "v0" },
			want:   "result_version",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			record := result("a", OutcomeCaptured)
			tc.mutate(&record)
			err := record.Validate()
			if err == nil {
				t.Fatalf("want an error mentioning %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestOutcomeObservedSeparatesHarnessFailureFromEvidence(t *testing.T) {
	for outcome, want := range map[Outcome]bool{
		OutcomeCaptured: true,
		OutcomePartial:  true,
		OutcomeEmpty:    true,
		OutcomeFailed:   false,
		OutcomeSkipped:  false,
	} {
		if got := outcome.Observed(); got != want {
			t.Errorf("%s.Observed() = %v, want %v", outcome, got, want)
		}
	}
	if OutcomeEmpty.Succeeded() {
		t.Error("an empty capture is not a success")
	}
}

func TestLoadResultsIsDeterministicAndRefusesAmbiguity(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"zulu", "alpha"} {
		if err := WriteResult(filepath.Join(dir, name+".json"), result(name, OutcomeCaptured)); err != nil {
			t.Fatal(err)
		}
	}
	records, err := LoadResults(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Permutation != "alpha" {
		t.Fatalf("results are not in permutation order: %+v", records)
	}

	duplicate := result("alpha", OutcomeCaptured)
	if err := WriteResult(filepath.Join(dir, "alpha-again.json"), duplicate); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadResults(dir); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want a duplicate-permutation error, got %v", err)
	}
}

func TestLoadResultsRefusesAnEmptyDirectoryRatherThanReportingAnEmptyMatrix(t *testing.T) {
	if _, err := LoadResults(t.TempDir()); err == nil {
		t.Fatal("an empty results directory must be an error, not an empty matrix")
	}
}

func TestLoadResultsRefusesAnUnreadableRecord(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadResults(dir); err == nil {
		t.Fatal("a result the report cannot read must not silently shrink the matrix")
	}
}
