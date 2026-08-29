// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestLoadContradictionExemptionsValidDocument(t *testing.T) {
	document := ContradictionExemptionDocument{
		Version: ContradictionExemptionsVersion,
		Exemptions: []ContradictionExemption{
			{
				ID:              "EX-001",
				Reason:          "The captured account has a deliberately smaller region set.",
				Area:            "cw",
				SourceKind:      "gcx_live_readback",
				Substrate:       "eks",
				FindingKind:     KindLabelValueContradiction,
				Field:           "labels.region",
				Signal:          "aws_applicationelb_request_count_sum",
				OnlyInSynth:     []string{"eu-west-1", "us-east-1"},
				ExpectedMatches: 1,
			},
		},
	}
	path := writeExemptionJSON(t, document)

	exemptions, err := LoadContradictionExemptions(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(exemptions) != 1 || exemptions[0].ID != "EX-001" {
		t.Fatalf("exemptions=%+v, want the decoded record", exemptions)
	}
	if err := ValidateContradictionExemptionDocument(document); err != nil {
		t.Fatal(err)
	}
}

func TestLoadContradictionExemptionsRejectsInvalidDocuments(t *testing.T) {
	base := ContradictionExemptionDocument{
		Version: ContradictionExemptionsVersion,
		Exemptions: []ContradictionExemption{{
			ID:              "EX-001",
			Reason:          "deliberate bounded difference",
			Area:            "cw",
			SourceKind:      "gcx_live_readback",
			Substrate:       "eks",
			FindingKind:     KindLabelValueContradiction,
			Field:           "labels.region",
			Signal:          "aws_request_total",
			OnlyInSynth:     []string{"eu-west-1", "us-east-1"},
			ExpectedMatches: 1,
		}},
	}

	cases := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{
			name: "missing version",
			mutate: func(raw map[string]any) {
				delete(raw, "version")
			},
			want: "version",
		},
		{
			name: "missing exemption list",
			mutate: func(raw map[string]any) {
				delete(raw, "exemptions")
			},
			want: "exemptions",
		},
		{
			name: "unknown top-level field",
			mutate: func(raw map[string]any) {
				raw["extra"] = true
			},
			want: "unknown field",
		},
		{
			name: "unknown exemption field",
			mutate: func(raw map[string]any) {
				raw["exemptions"].([]any)[0].(map[string]any)["extra"] = true
			},
			want: "unknown field",
		},
		{
			name: "duplicate id",
			mutate: func(raw map[string]any) {
				raw["exemptions"] = append(raw["exemptions"].([]any), raw["exemptions"].([]any)[0])
			},
			want: "duplicate id",
		},
		{
			name: "both signal selectors",
			mutate: func(raw map[string]any) {
				raw["exemptions"].([]any)[0].(map[string]any)["signal_prefix"] = "aws_"
			},
			want: "signal",
		},
		{
			name: "missing signal selector",
			mutate: func(raw map[string]any) {
				delete(raw["exemptions"].([]any)[0].(map[string]any), "signal")
			},
			want: "exactly one",
		},
		{
			name: "empty signal selector",
			mutate: func(raw map[string]any) {
				raw["exemptions"].([]any)[0].(map[string]any)["signal"] = ""
			},
			want: "must not be empty",
		},
		{
			name: "null signal selector",
			mutate: func(raw map[string]any) {
				raw["exemptions"].([]any)[0].(map[string]any)["signal"] = nil
			},
			want: "must not be empty",
		},
		{
			name: "empty signal prefix selector",
			mutate: func(raw map[string]any) {
				exemption := raw["exemptions"].([]any)[0].(map[string]any)
				delete(exemption, "signal")
				exemption["signal_prefix"] = ""
			},
			want: "must not be empty",
		},
		{
			name: "null signal prefix selector",
			mutate: func(raw map[string]any) {
				exemption := raw["exemptions"].([]any)[0].(map[string]any)
				delete(exemption, "signal")
				exemption["signal_prefix"] = nil
			},
			want: "must not be empty",
		},
		{
			name: "unsorted only in synth",
			mutate: func(raw map[string]any) {
				raw["exemptions"].([]any)[0].(map[string]any)["only_in_synth"] = []any{"us-east-1", "eu-west-1"}
			},
			want: "sorted",
		},
		{
			name: "duplicate only in synth",
			mutate: func(raw map[string]any) {
				raw["exemptions"].([]any)[0].(map[string]any)["only_in_synth"] = []any{"eu-west-1", "eu-west-1"}
			},
			want: "duplicate",
		},
		{
			name: "non-positive expected matches",
			mutate: func(raw map[string]any) {
				raw["exemptions"].([]any)[0].(map[string]any)["expected_matches"] = 0
			},
			want: "expected_matches",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			raw := marshalExemptionDocumentAsMap(t, base)
			test.mutate(raw)
			data, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			path := t.TempDir() + "/exemptions.json"
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadContradictionExemptions(path); err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("error=%v, want mention %q", err, test.want)
			}
		})
	}
}

func TestApplyContradictionExemptionsMarksExactFindings(t *testing.T) {
	findings := []ScopedFinding{
		contradictionFinding("aws_request_total", []string{"a", "b"}, []string{"b"}),
		{
			Area:      "cw",
			Source:    CorpusSource{Kind: "gcx_live_readback"},
			Substrate: "eks",
			Finding: Finding{
				Kind:          KindLabelValueContradiction,
				Disposition:   DispositionCoverageGap,
				Signal:        "aws_request_total",
				Field:         "labels.region",
				SynthValues:   []string{"a", "b"},
				RealityValues: []string{"a", "b", "c"},
			},
		},
	}
	exemptions := []ContradictionExemption{{
		ID:              "EX-001",
		Reason:          "The account capture is not the complete region universe.",
		Area:            "cw",
		SourceKind:      "gcx_live_readback",
		Substrate:       "eks",
		FindingKind:     KindLabelValueContradiction,
		Field:           "labels.region",
		Signal:          "aws_request_total",
		OnlyInSynth:     []string{"a"},
		ExpectedMatches: 1,
	}}

	if err := ApplyContradictionExemptions(findings, exemptions); err != nil {
		t.Fatal(err)
	}
	if got := findings[0]; got.ExemptionID != "EX-001" || got.ExemptionReason != exemptions[0].Reason {
		t.Fatalf("marked finding=%+v, want exemption metadata", got)
	}
	if findings[0].Finding.Disposition != DispositionContradiction {
		t.Fatalf("exemption reclassified finding: %+v", findings[0].Finding)
	}
	if findings[1].ExemptionID != "" {
		t.Fatalf("coverage gap was exempted: %+v", findings[1])
	}
}

func TestApplyContradictionExemptionsRejectsStaleAndOverlappingRulesAtomically(t *testing.T) {
	baseFinding := contradictionFinding("aws_request_total", []string{"a", "b"}, []string{"b"})
	baseRule := ContradictionExemption{
		ID:              "EX-001",
		Reason:          "bounded difference",
		Area:            "cw",
		SourceKind:      "gcx_live_readback",
		Substrate:       "eks",
		FindingKind:     KindLabelValueContradiction,
		Field:           "labels.region",
		SignalPrefix:    "aws_",
		OnlyInSynth:     []string{"a"},
		ExpectedMatches: 1,
	}

	cases := []struct {
		name  string
		rules []ContradictionExemption
		want  string
	}{
		{
			name: "stale count",
			rules: []ContradictionExemption{func() ContradictionExemption {
				rule := baseRule
				rule.ExpectedMatches = 2
				return rule
			}()},
			want: "expected_matches",
		},
		{
			name: "overlap",
			rules: []ContradictionExemption{
				baseRule,
				func() ContradictionExemption {
					rule := baseRule
					rule.ID = "EX-002"
					rule.Signal = "aws_request_total"
					rule.SignalPrefix = ""
					return rule
				}(),
			},
			want: "multiple",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			findings := []ScopedFinding{baseFinding}
			if err := ApplyContradictionExemptions(findings, test.rules); err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("error=%v, want mention %q", err, test.want)
			}
			if findings[0].ExemptionID != "" {
				t.Fatalf("failed application partially marked finding: %+v", findings[0])
			}
		})
	}
}

func TestCountUnexemptedContradictions(t *testing.T) {
	findings := []ScopedFinding{
		contradictionFinding("a", []string{"x"}, nil),
		func() ScopedFinding {
			finding := contradictionFinding("b", []string{"x"}, nil)
			finding.ExemptionID = "EX-001"
			finding.ExemptionReason = "documented"
			return finding
		}(),
		{
			Finding: Finding{Disposition: DispositionCoverageGap},
		},
	}
	if got := CountUnexemptedContradictions(findings); got != 1 {
		t.Fatalf("count=%d, want one unexempted contradiction", got)
	}
}

func TestWriteFindingsReportShowsExemptionMetadata(t *testing.T) {
	finding := contradictionFinding("aws_request_total", []string{"a"}, nil)
	finding.ExemptionID = "EX-001"
	finding.ExemptionReason = "The selected capture is intentionally bounded."
	var report bytes.Buffer
	if err := WriteFindingsReport(&report, []ScopedFinding{finding}); err != nil {
		t.Fatal(err)
	}
	output := report.String()
	for _, want := range []string{
		"unexempted contradictions fail",
		"exemptions remain visible",
		"coverage gaps are report-only",
		"EX-001",
		"The selected capture is intentionally bounded.",
	} {
		if !strings.Contains(output, want) && !strings.Contains(strings.ToLower(output), strings.ToLower(want)) {
			t.Fatalf("report missing %q:\n%s", want, output)
		}
	}
	if !strings.Contains(reportSection(output, "## Contradictions", "## Coverage gaps"), "EX-001") {
		t.Fatalf("exemption not retained under contradictions:\n%s", output)
	}
}

func contradictionFinding(signal string, synthValues, realityValues []string) ScopedFinding {
	return ScopedFinding{
		Area:      "cw",
		Source:    CorpusSource{Kind: "gcx_live_readback"},
		Substrate: "eks",
		Finding: Finding{
			Kind:          KindLabelValueContradiction,
			Disposition:   DispositionContradiction,
			Signal:        signal,
			Field:         "labels.region",
			SynthValues:   synthValues,
			RealityValues: realityValues,
		},
	}
}

func writeExemptionJSON(t *testing.T, document ContradictionExemptionDocument) string {
	t.Helper()
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/exemptions.json"
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func marshalExemptionDocumentAsMap(t *testing.T, document ContradictionExemptionDocument) map[string]any {
	t.Helper()
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}
