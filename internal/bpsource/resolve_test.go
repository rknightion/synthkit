// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
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

func TestHAReadOnlyScanRejectsSymlinkRootsAndYAML(t *testing.T) {
	for _, kind := range []string{"root", "custom", "yaml"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			outside := filepath.Join(root, "outside")
			if err := os.MkdirAll(filepath.Join(outside, "custom"), 0755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(outside, "custom", "sample__mini.yaml"), miniBlueprint)
			data := filepath.Join(root, "data")
			switch kind {
			case "root":
				if err := os.Symlink(outside, data); err != nil {
					t.Fatal(err)
				}
			case "custom":
				if err := os.Mkdir(data, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(outside, "custom"), filepath.Join(data, "custom")); err != nil {
					t.Fatal(err)
				}
			case "yaml":
				if err := os.MkdirAll(filepath.Join(data, "custom"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(outside, "custom", "sample__mini.yaml"), filepath.Join(data, "custom", "sample__mini.yaml")); err != nil {
					t.Fatal(err)
				}
			}
			mgr := NewManager(Options{Gate: ha.NewGate(), ReadOnly: true, BakedDir: filepath.Join(root, "baked"), BlueprintNames: []string{"*"}, DataDir: data, Registry: runner.Catalog()})
			loaded, _, diags := mgr.Resolve(context.Background())
			found := false
			for _, d := range diags {
				found = found || d.Severity == "error" && d.Stage == "secure"
			}
			if len(loaded) != 0 || !found {
				t.Fatalf("read-only scan followed %s symlink: loaded=%d diags=%v", kind, len(loaded), diags)
			}
		})
	}
}

func TestHAResolveIsReadOnlyAndPreparationUsesSameGate(t *testing.T) {
	root := t.TempDir()
	baked := filepath.Join(root, "baked")
	data := filepath.Join(root, "data")
	writeFile(t, filepath.Join(baked, "mini.yaml"), miniBlueprint)
	gate := ha.NewGate()
	store, err := control.NewHAStore(filepath.Join(root, "state.json"), gate)
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(Options{Gate: gate, ReadOnly: true, BakedDir: baked, BlueprintNames: []string{"mini"}, DataDir: data, Registry: runner.Catalog(), Config: NewStoreSourceConfig(store)})
	loaded, manifest, diags := mgr.Resolve(context.Background())
	if len(loaded) != 1 {
		t.Fatal("read-only load failed", diags)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatal("standby resolve created data/manifest directories", err)
	}
	if _, err := os.Stat(filepath.Join(root, "state.json")); !os.IsNotExist(err) {
		t.Fatal("standby persisted status", err)
	}
	if err := mgr.CommitResolved(context.Background(), manifest, loaded, diags); err != ha.ErrNotLeader {
		t.Fatal("standby commit admitted", err)
	}
	if err := gate.Activate(context.Background(), func(ctx context.Context) error { return mgr.CommitResolved(ctx, manifest, loaded, diags) }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, ".boot-manifest.json")); err != nil {
		t.Fatal("preparation did not persist manifest", err)
	}
}

func TestResolveTightensExistingStagedBlueprintPaths(t *testing.T) {
	data := t.TempDir()
	customDirPath := filepath.Join(data, customDir)
	gitRoot := filepath.Join(data, gitDir)
	gitSource := filepath.Join(gitRoot, "s1")
	customFile := filepath.Join(customDirPath, "team__custom.yaml")
	gitFile := filepath.Join(gitSource, "git.yaml")
	writeFile(t, customFile, miniBlueprint)
	writeFile(t, gitFile, miniBlueprint)
	for _, path := range []string{data, customDirPath, gitRoot, gitSource} {
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m := NewManager(Options{
		BakedDir: t.TempDir(), DataDir: data, Registry: runner.Catalog(),
		Config: &fakeConfig{list: []Source{{ID: "s1", Namespace: "git", FetchedSHA: "sha1"}}},
	})
	m.Resolve(context.Background())
	for path, want := range map[string]os.FileMode{
		data: 0o700, customDirPath: 0o700, gitRoot: 0o700, gitSource: 0o700,
		customFile: 0o600, gitFile: 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("staged path %s mode = %o, want %o", path, got, want)
		}
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

func TestResolveFiltersBeforeBlueprintLoad(t *testing.T) {
	baked := t.TempDir()
	data := t.TempDir()
	writeFile(t, filepath.Join(baked, "selected.yaml"), miniBlueprint)
	writeFile(t, filepath.Join(baked, "unselected.yaml"), "name: unselected\nunknown: true\n")

	m := NewManager(Options{
		BakedDir:       baked,
		DataDir:        data,
		Registry:       runner.Catalog(),
		Config:         &fakeConfig{},
		BlueprintNames: []string{"mini"},
	})
	got, _, diags := m.Resolve(context.Background())
	if err := m.SelectionError(); err != nil {
		t.Fatalf("SelectionError() = %v", err)
	}
	if len(got) != 1 || got[0].Resolved.Name != "mini" {
		t.Fatalf("loaded = %+v, want only mini", got)
	}
	if len(diags) != 0 {
		t.Fatalf("unselected invalid blueprint must not be resolved: %v", diags)
	}
}

func TestResolveSelectsNothingByDefault(t *testing.T) {
	baked := t.TempDir()
	writeFile(t, filepath.Join(baked, "mini.yaml"), miniBlueprint)
	m := NewManager(Options{
		BakedDir: baked,
		DataDir:  t.TempDir(),
		Registry: runner.Catalog(),
		Config:   &fakeConfig{},
	})

	got, _, diags := m.Resolve(context.Background())
	if m.SelectionRequested() {
		t.Fatal("empty BLUEPRINT_NAMES must be an intentional no-selection setup")
	}
	if err := m.SelectionError(); err != nil {
		t.Fatalf("SelectionError() = %v, want nil for intentional no-selection", err)
	}
	if len(got) != 0 {
		t.Fatalf("loaded = %+v, want no blueprints", got)
	}
	if len(diags) != 0 {
		t.Fatalf("intentional no-selection produced diagnostics: %v", diags)
	}
}

func TestResolveWildcardSelectsAllBlueprints(t *testing.T) {
	baked := t.TempDir()
	writeFile(t, filepath.Join(baked, "mini.yaml"), miniBlueprint)
	writeFile(t, filepath.Join(baked, "other.yaml"), "name: other\nhosts:\n  - name: h2\n    os: linux\n")
	m := NewManager(Options{
		BakedDir:       baked,
		DataDir:        t.TempDir(),
		Registry:       runner.Catalog(),
		Config:         &fakeConfig{},
		BlueprintNames: []string{"*"},
	})

	got, _, diags := m.Resolve(context.Background())
	if !m.SelectionRequested() {
		t.Fatal("BLUEPRINT_NAMES=* must be an explicit selection")
	}
	if err := m.SelectionError(); err != nil {
		t.Fatalf("SelectionError() = %v, want nil for wildcard selection", err)
	}
	if len(got) != 2 {
		t.Fatalf("loaded = %+v, want the complete two-blueprint catalog", got)
	}
	if len(diags) != 0 {
		t.Fatalf("wildcard selection produced diagnostics: %v", diags)
	}
}

func TestResolveRejectsUnknownSelectedBlueprint(t *testing.T) {
	baked := t.TempDir()
	writeFile(t, filepath.Join(baked, "mini.yaml"), miniBlueprint)
	m := NewManager(Options{
		BakedDir:       baked,
		DataDir:        t.TempDir(),
		Registry:       runner.Catalog(),
		Config:         &fakeConfig{},
		BlueprintNames: []string{"missing"},
	})
	m.Resolve(context.Background())
	err := m.SelectionError()
	if err == nil {
		t.Fatal("unknown selected blueprint must fail")
	}
	if !strings.Contains(err.Error(), "missing") || !strings.Contains(err.Error(), "mini") {
		t.Fatalf("SelectionError() = %v, want requested and available names", err)
	}
}
