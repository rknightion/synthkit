// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rknightion/synthkit/internal/runner"
)

func TestResolveMergesBuiltinCustomGit(t *testing.T) {
	data := t.TempDir()
	baked := t.TempDir()
	writeFile(t, filepath.Join(baked, "base.yaml"), `name: base
hosts: [{name: bh, os: linux}]
`)
	writeFile(t, filepath.Join(data, customDir, "up__u1.yaml"), `name: u1
hosts: [{name: uh, os: linux}]
`)
	git := &fakeGit{
		head: map[string]string{key("https://x/r", "refs/heads/main"): "sha1"},
		yaml: map[string]map[string][]byte{key("https://x/r", "refs/heads/main"): {
			"g1.yaml": []byte(`name: g1
hosts: [{name: gh, os: linux}]
`)}},
	}
	cfg := &fakeConfig{list: []Source{{ID: "s1", Namespace: "gns", URL: "https://x/r", Ref: "refs/heads/main"}}}
	m := NewManager(Options{BakedDir: baked, DataDir: data, Registry: runner.Catalog(), Git: git, Config: cfg, Now: func() int64 { return 1 }})
	loaded, man, diags := m.Resolve(context.Background())
	names := map[string]bool{}
	for _, l := range loaded {
		names[l.Resolved.Name] = true
	}
	if !names["base"] || !names["up/u1"] || !names["gns/g1"] {
		t.Fatalf("merged names wrong: %v (diags %+v)", names, diags)
	}
	if man.SourceSHAs["s1"] != "sha1" {
		t.Fatalf("manifest sha=%q", man.SourceSHAs["s1"])
	}
}

func TestStageUploadRejectsInvalid(t *testing.T) {
	m := NewManager(Options{DataDir: t.TempDir(), Registry: runner.Catalog(), Config: &fakeConfig{}})
	if err := m.StageUpload("x", "bad", []byte("name: \n:::")); err == nil {
		t.Fatal("expected reject for invalid YAML")
	}
}

func TestPendingDetectsStagedUpload(t *testing.T) {
	data := t.TempDir()
	m := NewManager(Options{DataDir: data, BakedDir: t.TempDir(), Registry: runner.Catalog(), Config: &fakeConfig{}, Now: func() int64 { return 1 }})
	// boot manifest empty (no Resolve yet) → staging an upload shows as pending
	if err := m.StageUpload("x", "u2", []byte(`name: u2
hosts: [{name: zz, os: linux}]
`)); err != nil {
		t.Fatal(err)
	}
	p := m.Pending()
	if !p.Restart {
		t.Fatalf("expected restart pending, got %+v", p)
	}
}

func TestFetchNowSkipsIfCached(t *testing.T) {
	data := t.TempDir()
	// Pre-populate the git dir with an existing file so skip-condition holds.
	gitDirPath := filepath.Join(data, gitDir, "s1")
	writeFile(t, filepath.Join(gitDirPath, "existing.yaml"), `name: existing
hosts: [{name: h1, os: linux}]
`)
	// Source has LastSHA matching HeadSHA → skip fetch.
	git := &fakeGit{
		head: map[string]string{key("https://x/r", "refs/heads/main"): "sha42"},
	}
	cfg := &fakeConfig{list: []Source{{ID: "s1", Namespace: "gns", URL: "https://x/r", Ref: "refs/heads/main", LastSHA: "sha42"}}}
	m := NewManager(Options{DataDir: data, Registry: runner.Catalog(), Git: git, Config: cfg, Now: func() int64 { return 1 }})
	if err := m.FetchNow(context.Background(), "s1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// latestSHAs must be seeded even on skip.
	m.mu.Lock()
	sha := m.latestSHAs["s1"]
	m.mu.Unlock()
	if sha != "sha42" {
		t.Fatalf("latestSHAs not seeded on skip, got %q", sha)
	}
}

func TestFetchNowClearsStaleFiles(t *testing.T) {
	data := t.TempDir()
	gitDirPath := filepath.Join(data, gitDir, "s1")
	// Write a stale file that should be removed on re-fetch.
	writeFile(t, filepath.Join(gitDirPath, "stale.yaml"), `name: stale
hosts: [{name: h1, os: linux}]
`)
	git := &fakeGit{
		head: map[string]string{key("https://x/r", "refs/heads/main"): "sha2"},
		yaml: map[string]map[string][]byte{key("https://x/r", "refs/heads/main"): {
			"fresh.yaml": []byte(`name: fresh
hosts: [{name: h2, os: linux}]
`)}},
	}
	// LastSHA differs → will fetch.
	cfg := &fakeConfig{list: []Source{{ID: "s1", Namespace: "gns", URL: "https://x/r", Ref: "refs/heads/main", LastSHA: "sha1"}}}
	m := NewManager(Options{DataDir: data, Registry: runner.Catalog(), Git: git, Config: cfg, Now: func() int64 { return 1 }})
	if err := m.FetchNow(context.Background(), "s1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// stale.yaml must be gone.
	entries, _ := filepath.Glob(filepath.Join(gitDirPath, "*.yaml"))
	for _, e := range entries {
		if filepath.Base(e) == "stale.yaml" {
			t.Fatal("stale.yaml should have been removed")
		}
	}
	// fresh.yaml must exist.
	found := false
	for _, e := range entries {
		if filepath.Base(e) == "fresh.yaml" {
			found = true
		}
	}
	if !found {
		t.Fatal("fresh.yaml not written")
	}
}

func TestFetchNowUnknownSourceErrors(t *testing.T) {
	m := NewManager(Options{DataDir: t.TempDir(), Registry: runner.Catalog(), Config: &fakeConfig{}, Git: &fakeGit{}})
	if err := m.FetchNow(context.Background(), "nonexistent"); err == nil {
		t.Fatal("expected error for unknown source")
	}
}

func TestStageUploadRejectsBadName(t *testing.T) {
	m := NewManager(Options{DataDir: t.TempDir(), Registry: runner.Catalog(), Config: &fakeConfig{}})
	// name with '/'
	err := m.StageUpload("ns", "bad/name", []byte(`name: ok
hosts: [{name: h1, os: linux}]
`))
	if err == nil {
		t.Fatal("expected rejection for name containing '/'")
	}
	// name with '__'
	err = m.StageUpload("ns", "bad__name", []byte(`name: ok
hosts: [{name: h1, os: linux}]
`))
	if err == nil {
		t.Fatal("expected rejection for name containing '__'")
	}
}

func TestRemoveUpload(t *testing.T) {
	data := t.TempDir()
	m := NewManager(Options{DataDir: data, Registry: runner.Catalog(), Config: &fakeConfig{}})
	if err := m.StageUpload("ns", "bp1", []byte(`name: bp1
hosts: [{name: h1, os: linux}]
`)); err != nil {
		t.Fatal(err)
	}
	staged := m.ListStaged()
	if len(staged) != 1 {
		t.Fatalf("expected 1 staged, got %d", len(staged))
	}
	if err := m.RemoveUpload("ns/bp1"); err != nil {
		t.Fatalf("RemoveUpload: %v", err)
	}
	staged = m.ListStaged()
	if len(staged) != 0 {
		t.Fatalf("expected 0 staged after remove, got %d", len(staged))
	}
}

func TestPendingNoGitIO(t *testing.T) {
	// Pending() must never call HeadSHA. Use a fakeGit that panics on any call.
	data := t.TempDir()
	panicGit := &panicOnCallGit{}
	m := NewManager(Options{DataDir: data, Registry: runner.Catalog(), Config: &fakeConfig{}, Git: panicGit, Now: func() int64 { return 1 }})
	// Should not panic.
	_ = m.Pending()
}

// panicOnCallGit panics if any method is called.
type panicOnCallGit struct{}

func (p *panicOnCallGit) HeadSHA(_ context.Context, _, _, _ string) (string, error) {
	panic("Pending() must not call HeadSHA")
}
func (p *panicOnCallGit) FetchYAML(_ context.Context, _, _, _, _ string) (map[string][]byte, error) {
	panic("Pending() must not call FetchYAML")
}

func TestBootManifest(t *testing.T) {
	m := NewManager(Options{DataDir: t.TempDir(), Registry: runner.Catalog(), Config: &fakeConfig{}})
	bm := m.BootManifest()
	// Fresh manager with no prior manifest on disk → empty blueprint list.
	if len(bm.Blueprints) != 0 {
		t.Fatalf("expected empty boot manifest, got %+v", bm)
	}
}

func TestResolveNilGitSkipsGitSources(t *testing.T) {
	data := t.TempDir()
	baked := t.TempDir()
	writeFile(t, filepath.Join(baked, "base.yaml"), `name: base
hosts: [{name: bh, os: linux}]
`)
	// Git=nil → no git fetch, no git scan, baked still loads.
	cfg := &fakeConfig{list: []Source{{ID: "s1", Namespace: "gns", URL: "https://x/r", Ref: "refs/heads/main"}}}
	m := NewManager(Options{BakedDir: baked, DataDir: data, Registry: runner.Catalog(), Config: cfg, Now: func() int64 { return 1 }})
	loaded, _, diags := m.Resolve(context.Background())
	if len(diags) != 0 {
		t.Fatalf("unexpected diags with nil git: %+v", diags)
	}
	names := map[string]bool{}
	for _, l := range loaded {
		names[l.Resolved.Name] = true
	}
	if !names["base"] {
		t.Fatalf("expected base to load, got %v", names)
	}
	if names["gns/g1"] {
		t.Fatal("git blueprint should not have loaded with nil git")
	}
}

func TestPollSources(t *testing.T) {
	git := &fakeGit{
		head: map[string]string{key("https://x/r", "refs/heads/main"): "pollsha"},
	}
	cfg := &fakeConfig{list: []Source{{ID: "s1", Namespace: "ns", URL: "https://x/r", Ref: "refs/heads/main"}}}
	m := NewManager(Options{DataDir: t.TempDir(), Registry: runner.Catalog(), Git: git, Config: cfg, Now: func() int64 { return 1 }})
	m.PollSources(context.Background())
	m.mu.Lock()
	sha := m.latestSHAs["s1"]
	m.mu.Unlock()
	if sha != "pollsha" {
		t.Fatalf("latestSHAs not updated by PollSources, got %q", sha)
	}
}

// TestPendingIgnoresBuiltins guards the regression where the boot manifest recorded built-in
// blueprints, which then showed up as phantom "removed" in Pending (ListStaged returns only
// custom/git), pinning the "restart to apply" banner permanently lit. With only built-ins
// loaded and nothing staged, Pending must report no changes.
func TestPendingIgnoresBuiltins(t *testing.T) {
	baked := t.TempDir()
	writeFile(t, filepath.Join(baked, "base.yaml"), miniBlueprint)
	m := NewManager(Options{
		BakedDir: baked,
		DataDir:  t.TempDir(),
		Registry: runner.Catalog(),
		Config:   &fakeConfig{},
		Now:      func() int64 { return 1 },
	})
	loaded, man, _ := m.Resolve(context.Background())
	if len(loaded) != 1 {
		t.Fatalf("want 1 builtin loaded, got %d", len(loaded))
	}
	if len(man.Blueprints) != 0 {
		t.Fatalf("boot manifest must NOT record built-ins, got %v", man.Blueprints)
	}
	p := m.Pending()
	if p.Restart || len(p.Removed) != 0 || len(p.Added) != 0 {
		t.Fatalf("built-ins must not be pending: %+v", p)
	}
}
