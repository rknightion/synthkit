// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the actual composition root, not a duplicate of its configuration
// logic: the policy comes solely from an env file in an isolated dry-run CLI.
func TestSourcePolicyCLIEnvFile(t *testing.T) {
	for _, tc := range []struct {
		name      string
		allowlist string
		wantError bool
	}{
		{"malformed policy", "https://git.example", true},
		{"valid policy", "git.example,mirror.example", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			envPath := filepath.Join(dir, "fixture.env")
			text := "DRY_RUN=true\nBLUEPRINT_NAMES=\nSELFOBS_ENABLED=false\nGIT_SOURCE_HOST_ALLOWLIST=" + tc.allowlist + "\n" +
				"BLUEPRINTS=" + dir + "\nBLUEPRINT_DATA_DIR=" + filepath.Join(dir, "data") + "\nCONFIG_SNAPSHOT_PATH=" + filepath.Join(dir, "state.json") + "\n"
			if err := os.WriteFile(envPath, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "run", "./cmd/synthkit", "-once", "-env", envPath)
			cmd.Dir = filepath.Join("..", "..")
			// Keep build-tool settings, but no deployment credentials or runtime overrides.
			for _, key := range []string{"PATH", "HOME", "TMPDIR", "GOCACHE", "GOPATH"} {
				if value, ok := os.LookupEnv(key); ok {
					cmd.Env = append(cmd.Env, key+"="+value)
				}
			}
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("CLI timed out: %v\n%s", ctx.Err(), out)
			}
			if tc.wantError {
				if err == nil || !strings.Contains(string(out), "git source policy: GIT_SOURCE_HOST_ALLOWLIST") {
					t.Fatalf("malformed env-file policy not rejected by composition root: err=%v\n%s", err, out)
				}
			} else if err != nil || !strings.Contains(string(out), "mode DRY_RUN") {
				t.Fatalf("valid env-file policy did not start safely: err=%v\n%s", err, out)
			}
		})
	}
}

func policySource() Source {
	return Source{ID: "repo", Name: "Repo", Namespace: "team", URL: "https://git.example/repo.git", Ref: "refs/heads/main"}
}

func TestSourceTokenPolicy(t *testing.T) {
	for _, name := range []string{"CONTROL_TOKEN", "GC_TOKEN", "MY_REPO_TOKEN", "GIT_TOKEN_"} {
		t.Run(name, func(t *testing.T) {
			s := policySource()
			s.TokenEnvVar = name
			if err := ValidateSource(s); err == nil || !strings.Contains(err.Error(), "GIT_TOKEN") {
				t.Fatalf("unsafe token variable %q accepted or unclear error: %v", name, err)
			}
			c := NewNanogitClient(func(string) string { t.Fatal("unsafe credential lookup"); return "" })
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := c.HeadSHA(ctx, s.URL, s.Ref, name); err == nil || !strings.Contains(err.Error(), "GIT_TOKEN") {
				t.Fatalf("unsafe HeadSHA: %v", err)
			}
			if _, err := c.FetchYAML(ctx, s.URL, s.Ref, "", name); err == nil || !strings.Contains(err.Error(), "GIT_TOKEN") {
				t.Fatalf("unsafe FetchYAML: %v", err)
			}
		})
	}
	for _, name := range []string{"", "GIT_TOKEN", "GIT_TOKEN_REPO"} {
		s := policySource()
		s.TokenEnvVar = name
		if err := ValidateSource(s); err != nil {
			t.Fatalf("compatible variable %q rejected: %v", name, err)
		}
	}
}

func TestSourceHostPolicy(t *testing.T) {
	p, err := NewSourcePolicy(" Git.Example , other.example, ::1 ")
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"https://git.example/repo", "https://GIT.EXAMPLE:8443/repo", "https://[::1]/repo"} {
		s := policySource()
		s.URL = raw
		if err := p.ValidateSource(s); err != nil {
			t.Fatalf("allowed %q: %v", raw, err)
		}
	}
	for _, raw := range []string{"https://evil.example/repo", "https://git.example.evil.example/repo", "https://sub.git.example/repo", "https://git.example./repo"} {
		s := policySource()
		s.URL = raw
		if err := p.ValidateSource(s); err == nil || !strings.Contains(err.Error(), "GIT_SOURCE_HOST_ALLOWLIST") {
			t.Fatalf("disallowed %q: %v", raw, err)
		}
		c := NewNanogitClientWithPolicy(func(string) string { t.Fatal("disallowed credential lookup"); return "" }, p)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if _, err := c.HeadSHA(ctx, raw, s.Ref, "GIT_TOKEN_REPO"); err == nil || !strings.Contains(err.Error(), "GIT_SOURCE_HOST_ALLOWLIST") {
			t.Fatalf("HeadSHA %q: %v", raw, err)
		}
		if _, err := c.FetchYAML(ctx, raw, s.Ref, "", "GIT_TOKEN_REPO"); err == nil || !strings.Contains(err.Error(), "GIT_SOURCE_HOST_ALLOWLIST") {
			t.Fatalf("FetchYAML %q: %v", raw, err)
		}
		cancel()
	}
	cfg := &fakeConfig{}
	m := NewManager(Options{DataDir: t.TempDir(), Config: cfg, SourcePolicy: p})
	s := policySource()
	s.URL = "https://evil.example/repo"
	if err := m.UpsertSource(s); err == nil {
		t.Fatal("manager accepted disallowed host")
	}
	if len(cfg.Sources()) != 0 {
		t.Fatal("manager persisted disallowed source")
	}
	s.URL = "https://git.example/repo"
	if err := m.UpsertSource(s); err != nil {
		t.Fatalf("manager rejected allowed host: %v", err)
	}
}

func TestSourceHostPolicyRejectsMalformedAllowlist(t *testing.T) {
	for _, raw := range []string{"git.example,", ",git.example", "https://git.example", "git.example:443", "*.example", "git.example/path", "git.example?x", "git.example.", "-bad.example", "bad_.example"} {
		if _, err := NewSourcePolicy(raw); err == nil {
			t.Fatalf("malformed allowlist %q accepted", raw)
		}
	}
	for _, raw := range []string{"", "   "} {
		p, err := NewSourcePolicy(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.ValidateSource(policySource()); err != nil {
			t.Fatal(err)
		}
	}
}

// Exercise the real nanogit request path on local TLS servers only. The first
// server sees Basic auth; no redirect request may reach the second server.
func TestNanogitLocalTokenFallbackAndRedirectRefusal(t *testing.T) {
	var redirected atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer destination.Close()
	var requests atomic.Int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		user, password, ok := r.BasicAuth()
		if !ok || user != "git" || password != "fixture-token" {
			t.Errorf("default/named git token missing from real request")
		}
		http.Redirect(w, r, destination.URL+"/repo.git", http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	// Tests are deliberately non-parallel: temporarily trust only the local test TLS server.
	previous := http.DefaultTransport
	http.DefaultTransport = origin.Client().Transport
	defer func() { http.DefaultTransport = previous }()
	p, err := NewSourcePolicy("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "GIT_TOKEN", "GIT_TOKEN_REPO"} {
		lookedUp := false
		c := NewNanogitClientWithPolicy(func(got string) string {
			lookedUp = true
			if got != name {
				t.Errorf("lookup name = %q, want %q", got, name)
			}
			return "fixture-token"
		}, p)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := c.HeadSHA(ctx, origin.URL+"/repo.git", "refs/heads/main", name)
		cancel()
		if !lookedUp || err == nil || !strings.Contains(err.Error(), "redirects are not permitted") {
			t.Fatalf("real request fallback/redirect: lookup=%v err=%v", lookedUp, err)
		}
	}
	if requests.Load() == 0 {
		t.Fatal("real request path was not exercised")
	}
	if redirected.Load() != 0 {
		t.Fatal("redirect destination received a request")
	}
	// A disallowed persisted URL must never reach even the first server.
	p, err = NewSourcePolicy("git.example")
	if err != nil {
		t.Fatal(err)
	}
	before := requests.Load()
	c := NewNanogitClientWithPolicy(func(string) string { t.Fatal("disallowed credential lookup"); return "" }, p)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := c.FetchYAML(ctx, origin.URL+"/repo.git", "refs/heads/main", "", ""); err == nil || !strings.Contains(err.Error(), "GIT_SOURCE_HOST_ALLOWLIST") {
		t.Fatalf("disallowed local fetch: %v", err)
	}
	if requests.Load() != before {
		t.Fatal("disallowed fetch reached network")
	}
}
