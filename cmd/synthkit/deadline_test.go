// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"github.com/rknightion/synthkit/internal/preflight"
	"io"
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

func TestOneShotDeadlineCLI(t *testing.T) {
	root, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "synthkit")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/synthkit")
	build.Dir = root
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("build: %v %s", e, out)
	}
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	future := time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	for _, tc := range []struct {
		name string
		args []string
		want string
		ok   bool
	}{
		{"omitted-validation", []string{"-validate"}, `"ok":true`, true},
		{"future-validation", []string{"-validate", "-one-shot-deadline", future}, `"ok":true`, true},
		{"expired-validation", []string{"-validate", "-one-shot-deadline", past}, "one-shot deadline has expired", false},
		{"expired-preflight", []string{"-preflight", "-one-shot-deadline", past}, "one-shot deadline has expired", false},
		{"malformed", []string{"-validate", "-one-shot-deadline", "private-fixture-value"}, "invalid one-shot deadline", false},
		{"incompatible-mode", []string{"-version", "-one-shot-deadline", future}, "one-shot deadline requires exactly one", false},
		{"empty-explicit", []string{"-validate", "-one-shot-deadline", ""}, "invalid one-shot deadline", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append(append([]string(nil), tc.args...), "-env", filepath.Join(dir, "absent.env"))
			c := exec.CommandContext(ctx, binary, args...)
			c.Dir = dir
			c.Env = append(os.Environ(), "BLUEPRINTS="+filepath.Join(root, "blueprints"), "BLUEPRINT_NAMES=otlp-native", "HA_MODE=off", "DRY_RUN=false", "GC_PROM_RW="+server.URL, "GC_LOKI="+server.URL, "GC_OTLP_ENDPOINT="+server.URL)
			out, e := c.CombinedOutput()
			if (e == nil) != tc.ok || !strings.Contains(string(out), tc.want) {
				t.Fatalf("CLI deadline result: err=%v output=%s", e, out)
			}
			if strings.Contains(string(out), "private-fixture-value") {
				t.Fatal("invalid deadline echoed input")
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatal("refused or offline invocations called receiver", requests.Load())
	}

}

func TestOneShotDeadlineCancelsActualPreflight(t *testing.T) {
	var calls atomic.Int64
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		calls.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	defer close(release)
	for key, value := range map[string]string{"GC_PROM_RW": server.URL + "/api/prom/push", "GC_PROM_USER": "1", "GC_LOKI": server.URL + "/loki/api/v1/push", "GC_LOKI_USER": "2", "GC_OTLP_ENDPOINT": server.URL + "/otlp", "GC_OTLP_USER": "3", "GC_TOKEN": "deadline-probe-fixture", "HA_MODE": "off"} {
		t.Setenv(key, value)
	}
	ctx, cancel, e := oneShotContext(time.Now().Add(500*time.Millisecond).UTC().Format(time.RFC3339Nano), true, true)
	if e != nil {
		t.Fatal(e)
	}
	defer cancel()
	begin := time.Now()
	e = runPreflight(ctx, filepath.Join(t.TempDir(), "absent.env"), io.Discard, preflight.Options{Client: server.Client(), Timeout: 3 * time.Second})
	if e == nil || time.Since(begin) > 2*time.Second || calls.Load() != 1 {
		t.Fatal("absolute deadline did not bound real preflight requests", e, time.Since(begin), calls.Load())
	}
}

// The subprocess executes the real main/flags/config/preflight path. Only its
// network trust root is injected at the test edge; production has no test hook.
func TestOneShotDeadlineMainProcess(t *testing.T) {
	if os.Getenv("SYNTHKIT_DEADLINE_TEST_HELPER") == "1" {
		raw, e := os.ReadFile(os.Getenv("SYNTHKIT_DEADLINE_TEST_CA"))
		if e != nil {
			os.Exit(12)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(raw) {
			os.Exit(13)
		}
		http.DefaultClient = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
		flag.CommandLine = flag.NewFlagSet("synthkit", flag.ExitOnError)
		os.Args = []string{"synthkit", "-preflight", "-env", "/dev/null", "-one-shot-deadline", os.Getenv("SYNTHKIT_DEADLINE_TEST_AT")}
		main()
		os.Exit(0)
	}
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	for _, blocked := range []bool{false, true} {
		name := "healthy"
		if blocked {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int64
			release := make(chan struct{})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				calls.Add(1)
				if blocked {
					select {
					case <-release:
					case <-r.Context().Done():
					}
				}
				w.WriteHeader(204)
			}))
			defer server.Close()
			defer close(release)
			ca := filepath.Join(t.TempDir(), "fixture-ca.pem")
			if e = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); e != nil {
				t.Fatal(e)
			}
			budget := time.Minute
			if blocked {
				budget = 3 * time.Second
			}
			deadline := time.Now().Add(budget).UTC().Format(time.RFC3339Nano)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestOneShotDeadlineMainProcess$")
			cmd.Env = append(os.Environ(), "SYNTHKIT_DEADLINE_TEST_HELPER=1", "SYNTHKIT_DEADLINE_TEST_CA="+ca, "SYNTHKIT_DEADLINE_TEST_AT="+deadline, "HA_MODE=off", "SELFOBS_ENABLED=false", "GC_PROM_RW="+server.URL+"/api/prom/push", "GC_PROM_USER=1", "GC_LOKI="+server.URL+"/loki/api/v1/push", "GC_LOKI_USER=2", "GC_OTLP_ENDPOINT="+server.URL+"/otlp", "GC_OTLP_USER=3", "GC_TOKEN=deadline-process-fixture")
			begin := time.Now()
			output, e := cmd.CombinedOutput()
			if blocked {
				if e == nil || !strings.Contains(string(output), "one-shot deadline exceeded") || time.Since(begin) > 6*time.Second || calls.Load() != 1 {
					t.Fatalf("real main ignored absolute preflight budget: err=%v elapsed=%s calls=%d output=%s", e, time.Since(begin), calls.Load(), output)
				}
			} else if e != nil || calls.Load() != 3 {
				t.Fatalf("real main positive receiver control failed: %v calls=%d %s", e, calls.Load(), output)
			}
		})
	}
}
