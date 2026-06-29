// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateWritesFiles(t *testing.T) {
	out := t.TempDir()
	if err := generate("../../blueprints/k8s-full-stack.yaml", out, "", ""); err != nil {
		t.Fatalf("generate: %v", err)
	}
	entries, _ := os.ReadDir(out)
	if len(entries) == 0 {
		t.Fatal("no dashboards written")
	}
	// the index is always written (filename is the blueprint name)
	if _, err := os.Stat(filepath.Join(out, "k8s-full-stack-index.json")); err != nil {
		t.Errorf("index not written: %v", err)
	}
	// the per-blueprint metrics dashboard is always written
	if _, err := os.Stat(filepath.Join(out, "k8s-full-stack-metrics.json")); err != nil {
		t.Errorf("metrics dashboard not written: %v", err)
	}
	// every file is valid GA v2 JSON
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(out, e.Name()))
		if !strings.Contains(string(b), "dashboard.grafana.app/v2") {
			t.Errorf("%s is not GA v2", e.Name())
		}
	}
}
