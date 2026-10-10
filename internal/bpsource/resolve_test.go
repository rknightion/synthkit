// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/runner"
)

type sourceMemoryDocuments struct {
	mu     sync.Mutex
	docs   map[control.Key]control.Snapshot
	writes int
}
type sourceMemoryBackend struct {
	docs *sourceMemoryDocuments
	gate ha.LeaderGate
}

func (b sourceMemoryBackend) Load(ctx context.Context, k control.Key) (control.Snapshot, error) {
	b.docs.mu.Lock()
	defer b.docs.mu.Unlock()
	snap, ok := b.docs.docs[k]
	if !ok {
		return snap, fmt.Errorf("missing precreated slot")
	}
	snap.Data = append([]byte(nil), snap.Data...)
	return snap, ctx.Err()
}
func (b sourceMemoryBackend) CompareAndSwap(ctx context.Context, k control.Key, r control.Revision, data []byte) (control.Revision, error) {
	var out control.Revision
	err := b.gate.Do(ctx, ha.Mutation, func(c context.Context) error {
		if err := c.Err(); err != nil {
			return err
		}
		b.docs.mu.Lock()
		defer b.docs.mu.Unlock()
		current, ok := b.docs.docs[k]
		if !ok {
			return fmt.Errorf("missing slot")
		}
		if current.Revision != r {
			return control.ErrConflict
		}
		out = control.Revision(string(r) + "x")
		b.docs.docs[k] = control.Snapshot{Data: append([]byte(nil), data...), Revision: out}
		b.docs.writes++
		return nil
	})
	return out, err
}

// Backend-only fake advertises immutable fetching; legacy file-mode fakes do not.
type immutableFakeGit struct{ *fakeGit }

func (g immutableFakeGit) FetchYAMLAtCommit(ctx context.Context, url, sha, subpath, tokenEnvVar string) (map[string][]byte, error) {
	for key, head := range g.head {
		if strings.HasPrefix(key, url+"@") && head == sha {
			return g.FetchYAML(ctx, url, strings.TrimPrefix(key, url+"@"), subpath, tokenEnvVar)
		}
	}
	return nil, fmt.Errorf("unknown commit")
}

func TestKubernetesSourceDocumentsResolveWithoutDisk(t *testing.T) {
	ctx := context.Background()
	key, _ := control.GitSourceKey("source")
	state := control.DefaultState()
	state.BlueprintSources = []control.SourceView{{ID: "source", Name: "source", Namespace: "cached", URL: "https://example.invalid/repository", Ref: "main"}}
	data, _ := json.Marshal(state)
	documents := &sourceMemoryDocuments{docs: map[control.Key]control.Snapshot{control.Control: {Data: data, Revision: "1"}, control.BootManifest: {Revision: "1"}, key: {Revision: "1"}}}
	backend := sourceMemoryBackend{docs: documents, gate: ha.AlwaysLeader{}}
	store, err := control.NewBackendStore(ctx, backend, ha.AlwaysLeader{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	const fetched = "1111111111111111111111111111111111111111"
	git := immutableFakeGit{&fakeGit{head: map[string]string{"https://example.invalid/repository@main": fetched}, yaml: map[string]map[string][]byte{"https://example.invalid/repository@main": {"mini.yaml": []byte(miniBlueprint)}}}}
	root := t.TempDir()
	absent := filepath.Join(root, "absent")
	opts := Options{Backend: backend, CASAttempts: 5, MaxDocumentBytes: 786432, Gate: ha.AlwaysLeader{}, ReadOnly: true, BlueprintNames: []string{"cached/mini"}, BakedDir: filepath.Join(root, "baked"), DataDir: absent, Registry: runner.Catalog(), Git: git, Config: NewStoreSourceConfig(store)}
	m := NewManager(opts)
	if err := m.LoadBackend(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.FetchNow(ctx, "source"); err != nil {
		t.Fatal(err)
	}
	loaded, manifest, diags := m.Resolve(ctx)
	if len(loaded) != 1 || len(diags) != 0 || manifest.SourceSHAs["source"] != fetched {
		t.Fatalf("resolve: %v %v %v", loaded, manifest, diags)
	}
	if err := m.CommitResolved(ctx, manifest, loaded, diags); err != nil {
		t.Fatal(err)
	}
	snap, _ := backend.Load(ctx, key)
	doc, err := decodeSourceDocument(snap.Data)
	if err != nil || doc.FetchedSHA != fetched || doc.FetchStatus.LoadedSHA != fetched || string(doc.Files["mini.yaml"]) != miniBlueprint {
		t.Fatal("source roundtrip", doc, err)
	}
	if len(store.Snapshot().BlueprintSources) == 0 || store.Snapshot().BlueprintSources[0].FetchedSHA != "" {
		t.Fatal("status torn into control document")
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatal("backend required disk", err)
	}
	gate := ha.NewGate()
	standby := sourceMemoryBackend{docs: documents, gate: gate}
	opts.Backend = standby
	opts.Gate = gate
	freshStore, err := control.NewBackendStore(ctx, standby, gate, 5)
	if err != nil {
		t.Fatal(err)
	}
	opts.Config = NewStoreSourceConfig(freshStore)
	opts.Git = &fakeGit{err: fmt.Errorf("git host unavailable")}
	reads := NewManager(opts)
	before := documents.writes
	if err := reads.LoadBackend(ctx); err != nil {
		t.Fatal(err)
	}
	got, man, ds := reads.Resolve(ctx)
	if len(got) != 1 || len(ds) != 0 || documents.writes != before {
		t.Fatal("standby/offline resolve", ds)
	}
	if err := reads.CommitResolved(ctx, man, got, ds); err != ha.ErrNotLeader || documents.writes != before {
		t.Fatal("standby write", err)
	}
	// A legacy client must not silently fall back to a second mutable-ref read.
	m.git = git.fakeGit
	before = documents.writes
	if err := m.FetchNow(ctx, "source"); err == nil || documents.writes != before {
		t.Fatal("backend accepted git client without immutable fetch capability", err)
	}
	m.git = git
	m.maxDocumentBytes = 64
	before = documents.writes
	if err := m.FetchNow(ctx, "source"); err == nil || documents.writes != before {
		t.Fatal("encoded cap not enforced")
	}
	if _, err := store.ResetContext(ctx); err != nil {
		t.Fatal(err)
	}
	opts.Gate = ha.AlwaysLeader{}
	opts.Backend = backend
	opts.Config = NewStoreSourceConfig(store)
	reset := NewManager(opts)
	if err := reset.LoadBackend(ctx); err != nil {
		t.Fatal(err)
	}
	got, _, _ = reset.Resolve(ctx)
	if len(got) != 0 || len(reset.Sources()) != 0 {
		t.Fatal("reset gave old source authority")
	}
}

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
