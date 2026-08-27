// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/inventory"
)

func TestRunPrintsCoverageFindingsWithoutFailing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synthPath := filepath.Join(dir, "synth.json")
	corpusDir := filepath.Join(dir, "corpus")
	if err := os.Mkdir(corpusDir, 0o755); err != nil {
		t.Fatal(err)
	}

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
