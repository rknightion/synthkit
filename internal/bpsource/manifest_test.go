// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"path/filepath"
	"testing"
)

func TestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := Manifest{
		Blueprints: []ManifestEntry{{Name: "custom/a", Provenance: ProvUpload}},
		SourceSHAs: map[string]string{"src1": "abc"},
	}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	got := readManifest(dir)
	if len(got.Blueprints) != 1 || got.Blueprints[0].Name != "custom/a" || got.SourceSHAs["src1"] != "abc" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	_ = filepath.Join // keep import
}

func TestReadManifestAbsent(t *testing.T) {
	if got := readManifest(t.TempDir()); len(got.Blueprints) != 0 {
		t.Fatal("absent manifest should be empty")
	}
}

func TestDiffPending(t *testing.T) {
	boot := Manifest{
		Blueprints: []ManifestEntry{{Name: "custom/a"}, {Name: "git/b", SourceID: "s1"}},
		SourceSHAs: map[string]string{"s1": "old"},
	}
	staged := []ManifestEntry{{Name: "custom/a"}, {Name: "custom/c"}} // git/b removed, custom/c added
	latest := map[string]string{"s1": "new"}                          // s1 moved
	p := diffPending(boot, staged, latest)
	if !p.Restart {
		t.Fatal("expected Restart=true")
	}
	if len(p.Added) != 1 || p.Added[0] != "custom/c" {
		t.Fatalf("Added=%v", p.Added)
	}
	if len(p.Removed) != 1 || p.Removed[0] != "git/b" {
		t.Fatalf("Removed=%v", p.Removed)
	}
	if len(p.Changed) != 1 || p.Changed[0] != "s1" {
		t.Fatalf("Changed=%v", p.Changed)
	}
}
