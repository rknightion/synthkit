// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/runner"
)

// Exercise the production nanogit adapter against native Git's smart HTTP server.
// The branch moves after the HEAD reply is captured, before that reply reaches
// the client. Resolving the mutable ref a second time therefore fetches B, not A.
func TestNanogitBackendMovingRefKeepsSnapshotBound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	repo := filepath.Join(root, "fixture.git")
	git := func(input string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
			"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
			"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("", "init", "--bare", "--object-format=sha1", repo)
	git("", "-C", repo, "config", "uploadpack.allowFilter", "true")
	git("", "-C", repo, "config", "uploadpack.allowAnySHA1InWant", "true")
	commit := func(yaml, parent string) string {
		blob := git(yaml, "-C", repo, "hash-object", "-w", "--stdin")
		tree := git("100644 blob "+blob+"\tmini.yaml\n", "-C", repo, "mktree")
		args := []string{"-C", repo, "commit-tree", tree}
		if parent != "" {
			args = append(args, "-p", parent)
		}
		return git("fixture\n", args...)
	}
	bytesA := miniBlueprint
	bytesB := strings.ReplaceAll(miniBlueprint, "h1", "h2")
	shaA := commit(bytesA, "")
	shaB := commit(bytesB, shaA)
	git("", "-C", repo, "update-ref", "refs/heads/main", shaA)
	backendPath := filepath.Join(git("", "--exec-path"), "git-http-backend")
	backendHTTP := &cgi.Handler{Path: backendPath, Dir: root, Env: []string{
		"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "GIT_PROTOCOL=version=2",
	}}
	var move, moved atomic.Bool
	var refReplies atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		reply := httptest.NewRecorder()
		backendHTTP.ServeHTTP(reply, r)
		if bytes.Contains(body, []byte("command=ls-refs")) {
			refReplies.Add(1)
			if move.CompareAndSwap(true, false) {
				cmd := exec.CommandContext(ctx, "git", "-C", repo, "update-ref", "refs/heads/main", shaB, shaA)
				if out, err := cmd.CombinedOutput(); err != nil {
					http.Error(w, fmt.Sprintf("advance ref: %v: %s", err, out), http.StatusInternalServerError)
					return
				}
				moved.Store(true)
			}
		}
		for k, values := range reply.Header() {
			w.Header()[k] = values
		}
		w.WriteHeader(reply.Code)
		_, _ = w.Write(reply.Body.Bytes())
	}))
	defer server.Close()
	// The production client uses the default transport. Trust only this test's
	// TLS server without adding a production transport or client-factory seam.
	oldTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })

	key, _ := control.GitSourceKey("fixture")
	state := control.DefaultState()
	state.BlueprintSources = []control.SourceView{{ID: "fixture", Name: "fixture", Namespace: "cached", URL: server.URL + "/fixture.git", Ref: "refs/heads/main"}}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	documents := &sourceMemoryDocuments{docs: map[control.Key]control.Snapshot{
		control.Control: {Data: data, Revision: "1"}, control.BootManifest: {Revision: "1"}, key: {Revision: "1"},
	}}
	backend := sourceMemoryBackend{docs: documents, gate: ha.AlwaysLeader{}}
	store, err := control.NewBackendStore(ctx, backend, ha.AlwaysLeader{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Backend: backend, CASAttempts: 5, MaxDocumentBytes: 786432,
		Gate: ha.AlwaysLeader{}, ReadOnly: true, BlueprintNames: []string{"cached/mini"},
		BakedDir: filepath.Join(root, "baked"), DataDir: filepath.Join(root, "absent"),
		Registry: runner.Catalog(), Git: NewNanogitClient(nil), Config: NewStoreSourceConfig(store)}
	load := func() *Manager {
		t.Helper()
		m := NewManager(opts)
		if err := m.LoadBackend(ctx); err != nil {
			t.Fatal(err)
		}
		return m
	}
	resolve := func(m *Manager, sha string) {
		t.Helper()
		loaded, manifest, diags := m.Resolve(ctx)
		if len(loaded) != 1 || len(diags) != 0 || manifest.SourceSHAs["fixture"] != sha {
			t.Fatalf("resolve: loaded=%v manifest=%v diagnostics=%v", loaded, manifest, diags)
		}
		if err := m.CommitResolved(ctx, manifest, loaded, diags); err != nil {
			t.Fatal(err)
		}
	}
	assertSnapshot := func(m *Manager, fetched, loaded, yaml string, pending bool) {
		t.Helper()
		snap, err := backend.Load(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := decodeSourceDocument(snap.Data)
		if err != nil {
			t.Fatal(err)
		}
		if doc.FetchedSHA != fetched || doc.FetchStatus.LoadedSHA != loaded || string(doc.Files["mini.yaml"]) != yaml {
			t.Fatalf("cached bytes/SHA receipt mismatch: fetched=%s loaded=%s bytes=%q; want fetched=%s loaded=%s bytes=%q", doc.FetchedSHA, doc.FetchStatus.LoadedSHA, doc.Files["mini.yaml"], fetched, loaded, yaml)
		}
		sources := m.Sources()
		if len(sources) != 1 || sources[0].PendingRestart != pending || doc.ConfigFingerprint != sourceFingerprint(sources[0]) {
			t.Fatalf("source binding/restart status mismatch: document=%+v sources=%+v", doc, sources)
		}
	}
	m := load()
	if err := m.FetchNow(ctx, "fixture"); err != nil {
		t.Fatal(err)
	}
	resolve(m, shaA)
	assertSnapshot(m, shaA, shaA, bytesA, false)

	before := refReplies.Load()
	move.Store(true)
	if err := m.FetchNow(ctx, "fixture"); err != nil {
		t.Fatal(err)
	}
	if !moved.Load() || git("", "-C", repo, "rev-parse", "refs/heads/main") != shaB {
		t.Fatal("fixture did not advance the mutable branch")
	}
	assertSnapshot(m, shaA, shaA, bytesA, false)
	if got := refReplies.Load() - before; got != 1 {
		t.Fatalf("snapshot resolved mutable branch %d times; want exactly one", got)
	}
	restarted := load()
	resolve(restarted, shaA)
	assertSnapshot(restarted, shaA, shaA, bytesA, false)

	if err := restarted.FetchNow(ctx, "fixture"); err != nil {
		t.Fatal(err)
	}
	assertSnapshot(restarted, shaB, shaA, bytesB, true)
	restarted = load()
	resolve(restarted, shaB)
	assertSnapshot(restarted, shaB, shaB, bytesB, false)

	// The legacy file-mode method still follows its mutable ref, not a SHA.
	files, err := opts.Git.FetchYAML(ctx, state.BlueprintSources[0].URL, "refs/heads/main", "", "")
	if err != nil || string(files["mini.yaml"]) != bytesB {
		t.Fatalf("legacy ref fetch changed: files=%v err=%v", files, err)
	}
}
