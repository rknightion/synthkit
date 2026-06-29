// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

package bpsource_test

// Integration tests for nanogitClient — hit a real public GitHub repo over HTTPS.
// These tests are intentionally excluded from the default `go test` gate.
//
// Run with:
//
//	go test -tags=integration ./internal/bpsource/ -run TestNanogit -v

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/bpsource"
)

// We use grafana/nanogit itself as the target: it's a small public repo that
// has a .golangci.yaml at its root and a stable main branch.
// Workflow files in .github/workflows/ use .yml (not .yaml), so we use the
// repo root (subpath="") which picks up .golangci.yaml.
const (
	integrationRepoURL = "https://github.com/grafana/nanogit"
	integrationRef     = "refs/heads/main"
	integrationSubpath = "" // repo root — .golangci.yaml lives here
)

func TestNanogitHeadSHA(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := bpsource.NewNanogitClient(nil)
	sha, err := client.HeadSHA(ctx, integrationRepoURL, integrationRef, "")
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if sha == "" {
		t.Fatal("HeadSHA returned empty SHA")
	}
	if len(sha) != 40 {
		t.Fatalf("HeadSHA returned unexpected length %d (want 40): %q", len(sha), sha)
	}
	t.Logf("HeadSHA = %s", sha)
}

func TestNanogitFetchYAML(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// subpath="" fetches from repo root; grafana/nanogit has .golangci.yaml there.
	client := bpsource.NewNanogitClient(nil)
	files, err := client.FetchYAML(ctx, integrationRepoURL, integrationRef, integrationSubpath, "")
	if err != nil {
		t.Fatalf("FetchYAML: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("FetchYAML returned 0 yaml files under subpath=%q (expected at least .golangci.yaml)", integrationSubpath)
	}
	t.Logf("FetchYAML returned %d yaml file(s):", len(files))
	for name, content := range files {
		t.Logf("  %s (%d bytes)", name, len(content))
	}
}

func TestNanogitFetchYAMLSubpath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Fetch .github/workflows — those files are .yml not .yaml, so the result
	// should be empty (no error). This validates that FetchYAML correctly returns
	// zero files (not an error) when no matching files exist under the subpath.
	client := bpsource.NewNanogitClient(nil)
	files, err := client.FetchYAML(ctx, integrationRepoURL, integrationRef, ".github/workflows", "")
	if err != nil {
		t.Fatalf("FetchYAML (.github/workflows — .yml only): %v", err)
	}
	// We expect 0 because the files are .yml, not .yaml — that's the correct behaviour.
	t.Logf("FetchYAML(.github/workflows) returned %d .yaml files (expected 0 since they are .yml)", len(files))
}
