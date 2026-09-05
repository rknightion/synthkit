// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/inventory"
)

func TestRunPrintsReportLineCountAgainstBound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synthPath := filepath.Join(dir, "synth.json")
	corpusDir := filepath.Join(dir, "corpus")
	if err := os.Mkdir(corpusDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeJSONFile(t, synthPath, inventory.New())
	writeCorpusMetric(t, corpusDir, testMetric("report_size_total", "us-east-1"))
	writeExemptionsFile(t, corpusDir, nil)

	var output bytes.Buffer
	if err := run([]string{"-synth", synthPath, "-corpus", corpusDir}, &output); err != nil {
		t.Fatalf("run returned a report finding as an error: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("report did not include a size line:\n%s", output.String())
	}
	want := fmt.Sprintf("Report size: %d lines (bound: %d; size-only breaches are report-only).", len(lines)-1, reportLineBound)
	if !strings.Contains(output.String(), want) {
		t.Fatalf("report missing size diagnostic %q:\n%s", want, output.String())
	}
}

func TestRunTreatsReportSizeBreachAsReportOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synthPath := filepath.Join(dir, "synth.json")
	corpusDir := filepath.Join(dir, "corpus")
	if err := os.Mkdir(corpusDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeJSONFile(t, synthPath, inventory.New())
	writeCorpusMetric(t, corpusDir, testMetric("report_size_total", "us-east-1"))
	writeExemptionsFile(t, corpusDir, nil)

	var output bytes.Buffer
	if err := runWithReportLineBound([]string{"-synth", synthPath, "-corpus", corpusDir}, &output, 1); err != nil {
		t.Fatalf("size-only breach returned an error: %v", err)
	}
	if !strings.Contains(output.String(), "bound: 1; size-only breaches are report-only") {
		t.Fatalf("report did not disclose the report-only size breach:\n%s", output.String())
	}
}

func TestRunPrintsCoverageFindingsWithoutFailing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synthPath := filepath.Join(dir, "synth.json")
	corpusDir := filepath.Join(dir, "corpus")
	if err := os.Mkdir(corpusDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExemptionsFile(t, corpusDir, nil)

	synth := inventory.New()
	writeJSONFile(t, synthPath, synth)
	document := inventory.CorpusDocument{
		CorpusVersion: inventory.CorpusVersion,
		Area:          "k8s",
		Source: inventory.CorpusSource{
			Kind: "k3d_lab", Substrate: "k3s", Collector: "collector",
			CollectorRole:    inventory.CollectorRoleAudited,
			CollectorVersion: "1.0.0", CapturedOn: "2026-08-25",
		},
		Authority:     inventory.CorpusAuthority{Substrates: []string{"k3s"}},
		CaptureVolume: inventory.CaptureVolume{Runs: 1, ObservedContractCounts: []int{1}},
		Inventory:     inventory.New(),
	}
	document.Inventory.Metrics = []inventory.Metric{{
		Name: "reality_only_total", Transports: []string{inventory.TransportPrometheusRW1},
		InstrumentTypes: []string{inventory.InstrumentCounter}, Labels: []inventory.Attribute{},
	}}
	writeJSONFile(t, filepath.Join(corpusDir, "k3d-lab.json"), document)

	var output bytes.Buffer
	if err := run([]string{"-synth", synthPath, "-corpus", corpusDir}, &output); err != nil {
		t.Fatalf("run returned a report finding as an error: %v", err)
	}
	for _, wanted := range []string{"## Coverage gaps", "signals/k8s.md", "PENDING: confirm"} {
		if !strings.Contains(output.String(), wanted) {
			t.Fatalf("report missing %q:\n%s", wanted, output.String())
		}
	}
}

func TestRunFailsAfterPrintingUnexemptedContradiction(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synthPath := filepath.Join(dir, "synth.json")
	corpusDir := filepath.Join(dir, "corpus")
	if err := os.Mkdir(corpusDir, 0o755); err != nil {
		t.Fatal(err)
	}

	synth := inventory.New()
	synth.Metrics = []inventory.Metric{testMetricWithLabel("coredns_dns_responses_total", "rcode", "NOT_A_DNS_RCODE")}
	writeJSONFile(t, synthPath, synth)
	writeCorpusMetric(t, corpusDir, testMetricWithLabel("coredns_dns_responses_total", "rcode", "NOERROR"))
	writeExemptionsFile(t, corpusDir, nil)

	var output bytes.Buffer
	err := runWithReportLineBound([]string{"-synth", synthPath, "-corpus", corpusDir}, &output, 1)
	if err == nil || !strings.Contains(err.Error(), "1 unexempted contradiction") {
		t.Fatalf("run error = %v, want one unexempted contradiction", err)
	}
	for _, wanted := range []string{"## Contradictions", "## Coverage gaps", "signal `coredns_dns_responses_total`", "field `labels.rcode`"} {
		if !strings.Contains(output.String(), wanted) {
			t.Fatalf("report missing %q:\n%s", wanted, output.String())
		}
	}
}

func TestRunPrintsFullReportWhenExemptionDrifts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synthPath := filepath.Join(dir, "synth.json")
	corpusDir := filepath.Join(dir, "corpus")
	if err := os.Mkdir(corpusDir, 0o755); err != nil {
		t.Fatal(err)
	}

	synth := inventory.New()
	synth.Metrics = []inventory.Metric{testMetric("unrelated_total", "eu-west-1")}
	writeJSONFile(t, synthPath, synth)
	writeCorpusMetric(t, corpusDir, testMetric("unrelated_total", "us-east-1"))
	writeExemptionsFile(t, corpusDir, []inventory.ContradictionExemption{{
		ID:              "EX-STALE",
		Reason:          "deliberately stale test rule",
		Area:            "k8s",
		SourceKind:      "k3d_lab",
		Substrate:       "k3s",
		FindingKind:     inventory.KindLabelValueContradiction,
		Field:           "labels.region",
		Signal:          "stale_total",
		OnlyInSynth:     []string{"eu-west-1"},
		ExpectedMatches: 1,
	}})

	var output bytes.Buffer
	err := run([]string{"-synth", synthPath, "-corpus", corpusDir}, &output)
	if err == nil || !strings.Contains(err.Error(), `contradiction exemption "EX-STALE" expected_matches=1 but matched 0 findings`) {
		t.Fatalf("run error=%v, want stale exemption error", err)
	}
	for _, wanted := range []string{"## Contradictions", "signal `unrelated_total`", "field `labels.region`"} {
		if !strings.Contains(output.String(), wanted) {
			t.Fatalf("report missing %q while exemption drifted:\n%s", wanted, output.String())
		}
	}
}

func TestRunKeepsExemptedContradictionVisibleWithoutFailing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synthPath := filepath.Join(dir, "synth.json")
	corpusDir := filepath.Join(dir, "corpus")
	if err := os.Mkdir(corpusDir, 0o755); err != nil {
		t.Fatal(err)
	}

	synth := inventory.New()
	synth.Metrics = []inventory.Metric{testMetricWithLabel("coredns_dns_responses_total", "rcode", "NOT_A_DNS_RCODE")}
	writeJSONFile(t, synthPath, synth)
	writeCorpusMetric(t, corpusDir, testMetricWithLabel("coredns_dns_responses_total", "rcode", "NOERROR"))
	writeExemptionsFile(t, corpusDir, []inventory.ContradictionExemption{{
		ID:              "EX-TEST",
		Reason:          "bounded test exemption",
		Area:            "k8s",
		SourceKind:      "k3d_lab",
		Substrate:       "k3s",
		FindingKind:     inventory.KindLabelValueContradiction,
		Field:           "labels.rcode",
		Signal:          "coredns_dns_responses_total",
		OnlyInSynth:     []string{"NOT_A_DNS_RCODE"},
		ExpectedMatches: 1,
	}})

	var output bytes.Buffer
	if err := run([]string{"-synth", synthPath, "-corpus", corpusDir}, &output); err != nil {
		t.Fatalf("run returned an exempted contradiction or coverage gap as an error: %v", err)
	}
	for _, wanted := range []string{"[EXEMPTED: `EX-TEST`", "bounded test exemption", "only-in-reality=[NOERROR]"} {
		if !strings.Contains(output.String(), wanted) {
			t.Fatalf("report missing %q:\n%s", wanted, output.String())
		}
	}
}

func TestRunRejectsTrailingSynthJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synthPath := filepath.Join(dir, "synth.json")
	if err := os.WriteFile(synthPath, []byte("{}\n{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := run([]string{"-synth", synthPath, "-corpus", dir}, &output)
	if err == nil || !strings.Contains(err.Error(), "multiple JSON documents") {
		t.Fatalf("run error = %v, want multiple-document rejection", err)
	}
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func testMetric(name, region string) inventory.Metric {
	return testMetricWithLabel(name, "region", region)
}

func testMetricWithLabel(name, label, value string) inventory.Metric {
	return inventory.Metric{
		Name:            name,
		Transports:      []string{inventory.TransportPrometheusRW1},
		InstrumentTypes: []string{inventory.InstrumentCounter},
		Labels:          []inventory.Attribute{{Key: label, Values: []string{value}}},
	}
}

func writeCorpusMetric(t *testing.T, corpusDir string, metric inventory.Metric) {
	t.Helper()
	document := inventory.CorpusDocument{
		CorpusVersion: inventory.CorpusVersion,
		Area:          "k8s",
		Source: inventory.CorpusSource{
			Kind: "k3d_lab", Substrate: "k3s", Collector: "collector",
			CollectorRole:    inventory.CollectorRoleAudited,
			CollectorVersion: "1.0.0", CapturedOn: "2026-08-25",
		},
		Authority:     inventory.CorpusAuthority{Substrates: []string{"k3s"}},
		CaptureVolume: inventory.CaptureVolume{Runs: 1, ObservedContractCounts: []int{1}},
		Inventory:     inventory.New(),
	}
	document.Inventory.Metrics = []inventory.Metric{metric}
	writeJSONFile(t, filepath.Join(corpusDir, "k3d-lab.json"), document)
}

func writeExemptionsFile(t *testing.T, corpusDir string, exemptions []inventory.ContradictionExemption) {
	t.Helper()
	verdictsDir := filepath.Join(corpusDir, "verdicts")
	if err := os.Mkdir(verdictsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if exemptions == nil {
		exemptions = []inventory.ContradictionExemption{}
	}
	writeJSONFile(t, filepath.Join(verdictsDir, "contradiction-exemptions.json"), inventory.ContradictionExemptionDocument{
		Version:    inventory.ContradictionExemptionsVersion,
		Exemptions: exemptions,
	})
}
