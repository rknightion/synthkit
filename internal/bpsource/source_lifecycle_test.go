// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/rknightion/synthkit/internal/runner"
)

func TestValidateSourceRejectsUnsafeOrAmbiguousConfiguration(t *testing.T) {
	valid := Source{
		ID:        "team-blueprints",
		Name:      "Team blueprints",
		Namespace: "team-a",
		URL:       "https://github.com/example/blueprints.git",
		Ref:       "refs/heads/main",
		Subpath:   "blueprints",
	}
	if err := ValidateSource(valid); err != nil {
		t.Fatalf("valid source rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Source)
		want   string
	}{
		{"ssh URL", func(s *Source) { s.URL = "git@github.com:example/blueprints.git" }, "HTTPS URL"},
		{"embedded credentials", func(s *Source) { s.URL = "https://token@example.com/repo.git" }, "HTTPS URL"},
		{"unsafe id", func(s *Source) { s.ID = "../other" }, "stable lowercase slug"},
		{"unsanitized namespace", func(s *Source) { s.Namespace = "Team A" }, "namespace must be"},
		{"missing ref", func(s *Source) { s.Ref = "" }, "ref is required"},
		{"unsafe ref", func(s *Source) { s.Ref = "refs/heads/../other" }, "ref is required"},
		{"path traversal", func(s *Source) { s.Subpath = "../blueprints" }, "clean relative path"},
		{"parent directory", func(s *Source) { s.Subpath = ".." }, "clean relative path"},
		{"invalid token variable", func(s *Source) { s.TokenEnvVar = "TOKEN-NAME" }, "environment-variable name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := valid
			tc.mutate(&source)
			err := ValidateSource(source)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateSource() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFetchedSourceIsStagedThenLoadedOnlyOnRestart(t *testing.T) {
	git := &fakeGit{
		head: map[string]string{key("https://example.com/team.git", "refs/heads/main"): "sha-fetched"},
		yaml: map[string]map[string][]byte{key("https://example.com/team.git", "refs/heads/main"): {
			"good.yaml":    []byte("name: good\nhosts: [{name: good-host, os: linux}]\n"),
			"bad.yaml":     []byte("name: \n"),
			"invalid.yaml": []byte("name: invalid\nunknown: true\n"),
		}},
	}
	cfg := &fakeConfig{}
	m := NewManager(Options{DataDir: t.TempDir(), BlueprintNames: []string{"*"}, Registry: runner.Catalog(), Git: git, Config: cfg, Now: func() int64 { return 99 }})
	source := Source{ID: "team-source", Name: "Team source", Namespace: "team-a", URL: "https://example.com/team.git", Ref: "refs/heads/main"}
	if err := m.UpsertSource(source); err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}
	if err := m.FetchNow(context.Background(), source.ID); err != nil {
		t.Fatalf("FetchNow: %v", err)
	}

	fetched := m.Sources()[0]
	if fetched.FetchedSHA != "sha-fetched" || fetched.FetchedFileCount != 3 || fetched.LastFetchMs != 99 {
		t.Fatalf("fetched state = %+v", fetched)
	}
	if got, want := fetched.EffectiveNames, []string{"team-a/good", "team-a/invalid"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("effective names = %v, want %v", got, want)
	}
	if !fetched.PendingRestart || fetched.LoadedSHA != "" {
		t.Fatalf("fetch must stage without loading: %+v", fetched)
	}
	if pending := m.Pending(); !pending.Restart || len(pending.Changed) != 1 || pending.Changed[0] != source.ID {
		t.Fatalf("pending after fetch = %+v, want a restart for %q", pending, source.ID)
	}

	// A restart consumes only the fetched on-disk snapshot. A failing remote must not affect it.
	git.err = errors.New("remote must not be contacted during Resolve")
	loaded, manifest, diags := m.Resolve(context.Background())
	if len(loaded) != 1 || loaded[0].Resolved.Name != "team-a/good" || manifest.SourceSHAs[source.ID] != "sha-fetched" {
		t.Fatalf("Resolve() loaded=%+v manifest=%+v", loaded, manifest)
	}
	if len(diags) != 2 || !strings.Contains(diags[0].Source, "team-source/bad.yaml") || !strings.Contains(diags[1].Source, "team-source/invalid.yaml") {
		t.Fatalf("Resolve() diags=%+v, want skipped bad.yaml and invalid.yaml", diags)
	}

	afterRestart := m.Sources()[0]
	if afterRestart.LoadedSHA != "sha-fetched" || afterRestart.PendingRestart {
		t.Fatalf("loaded state = %+v", afterRestart)
	}
	if len(afterRestart.LoadedNames) != 1 || afterRestart.LoadedNames[0] != "team-a/good" ||
		len(afterRestart.Skipped) != 2 || !strings.Contains(afterRestart.Skipped[0], "bad.yaml") || !strings.Contains(afterRestart.Skipped[1], "invalid.yaml") {
		t.Fatalf("restart result = %+v", afterRestart)
	}
	if pending := m.Pending(); pending.Restart {
		t.Fatalf("restart must clear source pending state: %+v", pending)
	}
}

func TestFetchNowDoesNotOverwriteConcurrentSourceConfiguration(t *testing.T) {
	source := Source{ID: "team-source", Name: "Team source", Namespace: "team-a", URL: "https://example.com/team.git", Ref: "refs/heads/main"}
	git := &blockingGit{
		head: map[string]string{key(source.URL, source.Ref): "sha-fetched"},
		yaml: map[string]map[string][]byte{key(source.URL, source.Ref): {
			"good.yaml": []byte("name: good\nhosts: [{name: good-host, os: linux}]\n"),
		}},
		headStarted: make(chan struct{}),
		releaseHead: make(chan struct{}),
	}
	cfg := &fakeConfig{list: []Source{source}}
	m := NewManager(Options{DataDir: t.TempDir(), Registry: runner.Catalog(), Git: git, Config: cfg, Now: func() int64 { return 99 }})

	fetched := make(chan error, 1)
	go func() { fetched <- m.FetchNow(context.Background(), source.ID) }()
	<-git.headStarted

	changed := source
	changed.Ref = "refs/heads/reconfigured"
	if err := m.UpsertSource(changed); err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}
	close(git.releaseHead)
	if err := <-fetched; err == nil || !strings.Contains(err.Error(), "configuration changed") {
		t.Fatalf("FetchNow() error = %v, want discarded result after concurrent reconfiguration", err)
	}

	got := m.Sources()[0]
	if got.Ref != changed.Ref || got.FetchedSHA != "" || got.ObservedSHA != "" {
		t.Fatalf("source after concurrent fetch = %+v, want retained configuration without stale fetch state", got)
	}
}

func TestPollSourcesDoesNotOverwriteConcurrentSourceConfiguration(t *testing.T) {
	source := Source{ID: "team-source", Name: "Team source", Namespace: "team-a", URL: "https://example.com/team.git", Ref: "refs/heads/main"}
	git := &blockingGit{
		head:        map[string]string{key(source.URL, source.Ref): "sha-observed"},
		headStarted: make(chan struct{}),
		releaseHead: make(chan struct{}),
	}
	cfg := &fakeConfig{list: []Source{source}}
	m := NewManager(Options{DataDir: t.TempDir(), Registry: runner.Catalog(), Git: git, Config: cfg})

	pollDone := make(chan struct{})
	go func() {
		m.PollSources(context.Background())
		close(pollDone)
	}()
	<-git.headStarted

	changed := source
	changed.Ref = "refs/heads/reconfigured"
	if err := m.UpsertSource(changed); err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}
	close(git.releaseHead)
	<-pollDone

	got := m.Sources()[0]
	if got.Ref != changed.Ref || got.ObservedSHA != "" {
		t.Fatalf("source after concurrent poll = %+v, want retained configuration without stale observation", got)
	}
}

func TestResolveRecordsDeselectedFetchedBlueprintsAsSkipped(t *testing.T) {
	source := Source{ID: "team-source", Name: "Team source", Namespace: "team-a", URL: "https://example.com/team.git", Ref: "refs/heads/main"}
	git := &fakeGit{
		head: map[string]string{key(source.URL, source.Ref): "sha-fetched"},
		yaml: map[string]map[string][]byte{key(source.URL, source.Ref): {
			"wanted.yaml": []byte("name: wanted\nhosts: [{name: wanted-host, os: linux}]\n"),
			"other.yaml":  []byte("name: other\nhosts: [{name: other-host, os: linux}]\n"),
		}},
	}
	cfg := &fakeConfig{}
	dataDir := t.TempDir()
	fetcher := NewManager(Options{DataDir: dataDir, Registry: runner.Catalog(), Git: git, Config: cfg})
	if err := fetcher.UpsertSource(source); err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}
	if err := fetcher.FetchNow(context.Background(), source.ID); err != nil {
		t.Fatalf("FetchNow: %v", err)
	}

	resolver := NewManager(Options{
		DataDir:        dataDir,
		BlueprintNames: []string{"team-a/wanted"},
		Registry:       runner.Catalog(),
		Config:         cfg,
	})
	loaded, _, diags := resolver.Resolve(context.Background())
	if len(loaded) != 1 || loaded[0].Resolved.Name != "team-a/wanted" {
		t.Fatalf("Resolve() loaded = %+v, want only the selected blueprint", loaded)
	}
	if len(diags) != 1 || diags[0].Stage != "selection" || !strings.Contains(diags[0].Source, "other.yaml") {
		t.Fatalf("Resolve() diagnostics = %+v, want deselected other.yaml", diags)
	}
	got := resolver.Sources()[0]
	if len(got.Skipped) != 1 || !strings.Contains(got.Skipped[0], "other.yaml") || !strings.Contains(got.Skipped[0], "deselected") {
		t.Fatalf("source skipped results = %+v, want deselected other.yaml", got.Skipped)
	}
}

func TestResolveReportsSourcePersistenceFailures(t *testing.T) {
	cfg := &failingSourceConfig{list: []Source{{
		ID:         "team-source",
		Namespace:  "team-a",
		FetchedSHA: "sha-fetched",
	}}, err: errors.New("state volume is read-only")}
	m := NewManager(Options{DataDir: t.TempDir(), Registry: runner.Catalog(), Config: cfg})
	_, _, diags := m.Resolve(context.Background())
	if len(diags) != 1 || diags[0].Source != "team-source" || diags[0].Stage != "persist" || !strings.Contains(diags[0].Detail, "read-only") {
		t.Fatalf("Resolve() diagnostics = %+v, want source persistence failure", diags)
	}
}

func TestSourcePersistenceOperationsAreRaceFree(t *testing.T) {
	source := Source{ID: "team-source", Name: "Team source", Namespace: "team-a", URL: "https://example.com/team.git", Ref: "refs/heads/main"}
	git := &fakeGit{
		head: map[string]string{key(source.URL, source.Ref): "sha-fetched"},
		yaml: map[string]map[string][]byte{key(source.URL, source.Ref): {
			"good.yaml": []byte("name: good\nhosts: [{name: good-host, os: linux}]\n"),
		}},
	}
	cfg := &fakeConfig{list: []Source{source}}
	m := NewManager(Options{DataDir: t.TempDir(), Registry: runner.Catalog(), Git: git, Config: cfg})

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				m.PollSources(context.Background())
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if err := m.UpsertSource(source); err != nil {
					t.Errorf("UpsertSource: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	if got := m.Sources(); len(got) != 1 || got[0].ID != source.ID {
		t.Fatalf("Sources() = %+v, want the configured source", got)
	}
}

type blockingGit struct {
	head        map[string]string
	yaml        map[string]map[string][]byte
	headStarted chan struct{}
	releaseHead chan struct{}
}

type failingSourceConfig struct {
	list []Source
	err  error
}

func (c *failingSourceConfig) Sources() []Source         { return c.list }
func (c *failingSourceConfig) UpsertSource(Source) error { return c.err }
func (c *failingSourceConfig) RemoveSource(string) error { return nil }

func (g *blockingGit) HeadSHA(_ context.Context, url, ref, _ string) (string, error) {
	close(g.headStarted)
	<-g.releaseHead
	return g.head[key(url, ref)], nil
}

func (g *blockingGit) FetchYAML(_ context.Context, url, ref, _, _ string) (map[string][]byte, error) {
	return g.yaml[key(url, ref)], nil
}
