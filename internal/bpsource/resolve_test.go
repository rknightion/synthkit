// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rknightion/synthkit/internal/runner"
)

const miniBlueprint = `name: mini
hosts:
  - name: h1
    os: linux
`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanCustomNamespaces(t *testing.T) {
	data := t.TempDir()
	writeFile(t, filepath.Join(data, customDir, "team-a__mini.yaml"), miniBlueprint)
	m := &Manager{dataDir: data, reg: runner.Catalog(), cfg: &fakeConfig{}}
	got, diags := m.scanCustom()
	if len(got) != 1 {
		t.Fatalf("want 1 loaded, got %d (diags %+v)", len(got), diags)
	}
	if got[0].Resolved.Name != "team-a/mini" {
		t.Fatalf("name=%q want team-a/mini", got[0].Resolved.Name)
	}
	if got[0].Resolved.Label != "team-a/mini" {
		t.Fatalf("label=%q", got[0].Resolved.Label)
	}
	if got[0].Provenance != ProvUpload {
		t.Fatalf("prov=%v", got[0].Provenance)
	}
}

func TestScanCustomBadYAMLDegrades(t *testing.T) {
	data := t.TempDir()
	writeFile(t, filepath.Join(data, customDir, "x__broken.yaml"), "name: \nbad: [")
	m := &Manager{dataDir: data, reg: runner.Catalog(), cfg: &fakeConfig{}}
	got, diags := m.scanCustom()
	if len(got) != 0 {
		t.Fatal("broken blueprint should not load")
	}
	if len(diags) == 0 || diags[0].Severity != "error" {
		t.Fatal("expected error diag")
	}
}
