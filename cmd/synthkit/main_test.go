// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/bpsource"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/preflight"
	"github.com/rknightion/synthkit/internal/runner"
)

func TestRunPreflightReportsStaticAndNetworkResultsWithoutValues(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	values := map[string]string{
		"DRY_RUN":          "true",
		"GC_PROM_RW":       srv.URL + "/api/prom/push",
		"GC_PROM_USER":     "7123456",
		"GC_OTLP_ENDPOINT": srv.URL + "/otlp",
		"GC_OTLP_USER":     "7234567",
		"GC_LOKI":          srv.URL + "/loki/api/v1/push",
		"GC_LOKI_USER":     "7345678",
		"GC_TOKEN":         "command-test-token-should-never-appear",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
	var output strings.Builder

	err := runPreflight(context.Background(), filepath.Join(t.TempDir(), "absent.env"), &output,
		preflight.Options{Client: srv.Client(), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, expected := range []string{
		"prometheus: statically valid", "loki: statically valid", "otlp: statically valid",
		"prometheus: ready", "loki: ready", "otlp: ready",
	} {
		if !strings.Contains(got, expected) {
			t.Errorf("output missing %q:\n%s", expected, got)
		}
	}
	for key, value := range values {
		if strings.Contains(got, value) {
			t.Fatalf("output exposed %s value %q:\n%s", key, value, got)
		}
	}
	if strings.Contains(got, fmt.Sprint(http.StatusNoContent)) {
		t.Fatalf("output exposed unnecessary response details: %s", got)
	}
}

type emptyJSONSource struct{}

func (emptyJSONSource) Blueprints() []string                                      { return nil }
func (emptyJSONSource) Recent(string, time.Time, time.Duration) []*ledger.Request { return nil }
func (emptyJSONSource) WindowStats(string, time.Time, time.Duration) ledger.WindowStats {
	return ledger.WindowStats{}
}

func TestJSONHostProtectsDataButNotHealth(t *testing.T) {
	h := jsonHost(emptyJSONSource{}, "secret")

	for _, tc := range []struct {
		path string
		want int
	}{
		{path: "/healthz", want: http.StatusOK},
		{path: "/", want: http.StatusUnauthorized},
		{path: "/blueprints", want: http.StatusUnauthorized},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.want {
			t.Errorf("GET %s = %d, want %d", tc.path, rec.Code, tc.want)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/blueprints", nil)
	req.SetBasicAuth("control", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated GET /blueprints = %d, want 200", rec.Code)
	}
}

func TestJSONHostAllowsAuthenticatedCORSPreflight(t *testing.T) {
	h := jsonHost(emptyJSONSource{}, "secret")
	req := httptest.NewRequest(http.MethodOptions, "/blueprints", nil)
	req.Header.Set("Origin", "https://grafana.example.com")
	req.Header.Set("Access-Control-Request-Headers", "Authorization, x-grafana-device-id")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS /blueprints = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") {
		t.Fatalf("Access-Control-Allow-Headers = %q, want Authorization", got)
	}
}

func TestOrdinaryDryRunRemainsCredentialFreeAndOffline(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	baked := t.TempDir()
	if err := os.WriteFile(filepath.Join(baked, "minimal.yaml"), []byte("name: minimal\nhosts:\n  - name: h1\n    os: linux\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"DRY_RUN":              "true",
		"BLUEPRINTS":           baked,
		"BLUEPRINT_DATA_DIR":   t.TempDir(),
		"CONFIG_SNAPSHOT_PATH": filepath.Join(t.TempDir(), "control-state.json"),
		"GC_PROM_RW":           srv.URL + "/api/prom/push",
		"GC_PROM_USER":         "8123456",
		"GC_OTLP_ENDPOINT":     srv.URL + "/otlp",
		"GC_OTLP_USER":         "8234567",
		"GC_LOKI":              srv.URL + "/loki/api/v1/push",
		"GC_LOKI_USER":         "8345678",
		"GC_TOKEN":             "dry-run-token",
	} {
		t.Setenv(key, value)
	}

	if err := run(true, false, filepath.Join(t.TempDir(), "absent.env")); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("ordinary dry-run made %d network calls", got)
	}
}

func TestRunRejectsUnknownBlueprintSelectionBeforeRunnerStart(t *testing.T) {
	baked := t.TempDir()
	if err := os.WriteFile(filepath.Join(baked, "known.yaml"), []byte("name: known\nhosts:\n  - name: h1\n    os: linux\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	env := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(env, []byte(strings.Join([]string{
		"DRY_RUN=true",
		"BLUEPRINTS=" + baked,
		"BLUEPRINT_DATA_DIR=" + data,
		"CONFIG_SNAPSHOT_PATH=" + filepath.Join(data, "control-state.json"),
		"BLUEPRINT_NAMES=missing",
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := run(true, false, env)
	if err == nil {
		t.Fatal("run() must reject an unknown selected blueprint")
	}
	if !strings.Contains(err.Error(), "missing") || !strings.Contains(err.Error(), "known") {
		t.Fatalf("run() error = %v, want requested and available names", err)
	}
}

func TestIsLoopback(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8088", true},
		{"localhost:8088", true},
		{"[::1]:8088", true},
		{":8088", false}, // empty host = all interfaces, NOT loopback
		{"0.0.0.0:8088", false},
		{"10.0.0.1:8088", false},
		{"192.168.1.5:9090", false},
		{"not-an-addr", false}, // SplitHostPort fails
	}
	for _, c := range cases {
		if got := isLoopback(c.addr); got != c.want {
			t.Errorf("isLoopback(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

func TestValidateControlExposure(t *testing.T) {
	tests := []struct {
		name        string
		exposure    controlExposure
		inContainer bool
		servesHTTP  bool
		wantErr     string
	}{
		{name: "direct loopback remains open", exposure: controlExposure{HTTPAddr: "127.12.0.1:8088"}, servesHTTP: true},
		{name: "one-shot does not expose HTTP", exposure: controlExposure{HTTPAddr: "0.0.0.0:8088"}, servesHTTP: false},
		{name: "direct non-loopback needs token", exposure: controlExposure{HTTPAddr: "0.0.0.0:8088", Ack: "trusted-network"}, servesHTTP: true, wantErr: "CONTROL_TOKEN"},
		{name: "direct non-loopback needs acknowledgement", exposure: controlExposure{HTTPAddr: "10.0.0.8:8088", Token: "secret"}, servesHTTP: true, wantErr: "CONTROL_EXPOSURE_ACK"},
		{name: "trusted network accepted", exposure: controlExposure{HTTPAddr: "10.0.0.8:8088", Token: "secret", Ack: "trusted-network"}, servesHTTP: true},
		{name: "TLS proxy accepted", exposure: controlExposure{HTTPAddr: "example.internal:8088", Token: "secret", Ack: "tls-proxy"}, servesHTTP: true},
		{name: "invalid acknowledgement rejected on loopback", exposure: controlExposure{HTTPAddr: "localhost:8088", Ack: "yes"}, servesHTTP: true, wantErr: "trusted-network"},
		{name: "container uses host bind", exposure: controlExposure{HTTPAddr: "0.0.0.0:8088", HostBind: "127.0.0.1"}, inContainer: true, servesHTTP: true},
		{name: "container global publish needs acknowledgement", exposure: controlExposure{HTTPAddr: "0.0.0.0:8088", HostBind: "0.0.0.0", Token: "secret"}, inContainer: true, servesHTTP: true, wantErr: "CONTROL_EXPOSURE_ACK"},
		{name: "container missing effective bind fails closed", exposure: controlExposure{HTTPAddr: "0.0.0.0:8088"}, inContainer: true, servesHTTP: true, wantErr: "SYNTHKIT_BIND"},
		{name: "malformed direct bind rejected", exposure: controlExposure{HTTPAddr: "not-an-address"}, servesHTTP: true, wantErr: "JSON_HTTP_ADDR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateControlExposure(tt.exposure, tt.inContainer, tt.servesHTTP)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateControlExposure() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateControlExposure() error = %v, want text %q", err, tt.wantErr)
			}
		})
	}
}

// mustLoad is a test helper that loads a blueprint YAML or fails immediately.
func mustLoad(t *testing.T, yaml []byte) *blueprint.Resolved {
	t.Helper()
	reg := runner.Catalog()
	r, err := blueprint.Load(yaml, reg)
	if err != nil {
		t.Fatalf("blueprint.Load failed: %v", err)
	}
	return r
}

// mustLoadNS loads a namespaced blueprint or fails immediately.
func mustLoadNS(t *testing.T, yaml []byte, ns string) *blueprint.Resolved {
	t.Helper()
	reg := runner.Catalog()
	r, err := blueprint.LoadNamespaced(yaml, ns, reg)
	if err != nil {
		t.Fatalf("blueprint.LoadNamespaced(%q) failed: %v", ns, err)
	}
	return r
}

// TestFoldValidated covers the three required cases for foldValidated:
//
//  1. Built-in + custom that collides on a HOST name → custom is skipped (1 diag),
//     built-in is accepted, builtinErr==nil.
//  2. Two built-ins that collide → builtinErr != nil.
//  3. Built-in + non-colliding custom → both accepted, no skips.
func TestFoldValidated(t *testing.T) {
	// Two minimal blueprints declaring the SAME host name ("sharedhost") to force a collision.
	// The built-in uses name "bp-builtin"; the custom uses name "bp-custom" (different blueprint
	// names so only the host name is the collision trigger, not the blueprint/label name).
	builtinYAML := []byte("name: bp-builtin\nhosts:\n  - name: sharedhost\n    os: linux\n")
	customCollidingYAML := []byte("name: bp-custom\nhosts:\n  - name: sharedhost\n    os: linux\n")
	customOKYAML := []byte("name: bp-ok\nhosts:\n  - name: uniquehost\n    os: linux\n")
	// A second built-in with the same host name (for the two-built-ins collision case).
	builtin2YAML := []byte("name: bp-builtin2\nhosts:\n  - name: sharedhost\n    os: linux\n")

	t.Run("custom_collides_with_builtin_skipped", func(t *testing.T) {
		builtinRes := mustLoad(t, builtinYAML)
		// Custom namespaced so its blueprint name and label differ, but host name still collides.
		customRes := mustLoadNS(t, customCollidingYAML, "team-x")

		loaded := []bpsource.Loaded{
			{Resolved: builtinRes, Provenance: bpsource.ProvBuiltin},
			{Resolved: customRes, Provenance: bpsource.ProvUpload},
		}
		accepted, builtinErr, skipped := foldValidated(loaded)
		if builtinErr != nil {
			t.Fatalf("builtinErr should be nil, got: %v", builtinErr)
		}
		if len(accepted) != 1 || accepted[0].Name != "bp-builtin" {
			t.Fatalf("accepted = %v, want [bp-builtin]", resolvedNames(accepted))
		}
		if len(skipped) != 1 {
			t.Fatalf("len(skipped) = %d, want 1; skipped=%v", len(skipped), skipped)
		}
		d := skipped[0]
		if d.Severity != "error" {
			t.Errorf("skipped[0].Severity = %q, want error", d.Severity)
		}
		if d.Source != "team-x/bp-custom" {
			t.Errorf("skipped[0].Source = %q, want team-x/bp-custom", d.Source)
		}
		if d.Stage != "validate" {
			t.Errorf("skipped[0].Stage = %q, want validate", d.Stage)
		}
		if d.Detail == "" {
			t.Error("skipped[0].Detail is empty, want collision message")
		}
	})

	t.Run("two_builtins_collide_fatal", func(t *testing.T) {
		b1 := mustLoad(t, builtinYAML)
		b2 := mustLoad(t, builtin2YAML)

		loaded := []bpsource.Loaded{
			{Resolved: b1, Provenance: bpsource.ProvBuiltin},
			{Resolved: b2, Provenance: bpsource.ProvBuiltin},
		}
		_, builtinErr, _ := foldValidated(loaded)
		if builtinErr == nil {
			t.Fatal("builtinErr should be non-nil for two colliding built-ins")
		}
	})

	t.Run("builtin_plus_noncolliding_custom_both_accepted", func(t *testing.T) {
		builtinRes := mustLoad(t, builtinYAML)
		okRes := mustLoadNS(t, customOKYAML, "team-y")

		loaded := []bpsource.Loaded{
			{Resolved: builtinRes, Provenance: bpsource.ProvBuiltin},
			{Resolved: okRes, Provenance: bpsource.ProvGit},
		}
		accepted, builtinErr, skipped := foldValidated(loaded)
		if builtinErr != nil {
			t.Fatalf("builtinErr should be nil, got: %v", builtinErr)
		}
		if len(skipped) != 0 {
			t.Fatalf("expected no skips, got: %v", skipped)
		}
		if len(accepted) != 2 {
			t.Fatalf("accepted = %v, want 2 blueprints", resolvedNames(accepted))
		}
	})
}

// resolvedNames extracts blueprint names for test error messages.
func resolvedNames(rs []*blueprint.Resolved) []string {
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = r.Name
	}
	return names
}

func TestCheckReadinessRequiresHTTP200(t *testing.T) {
	status := http.StatusServiceUnavailable
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	defer srv.Close()
	if err := checkReadiness(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("503 readiness unexpectedly succeeded")
	}
	status = http.StatusOK
	if err := checkReadiness(context.Background(), srv.Client(), srv.URL); err != nil {
		t.Fatalf("200 readiness failed: %v", err)
	}
}

func TestReadinessEndpointUsesConfiguredPortAndLoopbackForWildcard(t *testing.T) {
	cases := map[string]string{
		"":               "http://127.0.0.1:8088/control/readiness",
		":9090":          "http://127.0.0.1:9090/control/readiness",
		"0.0.0.0:7070":   "http://127.0.0.1:7070/control/readiness",
		"[::]:6060":      "http://127.0.0.1:6060/control/readiness",
		"127.0.0.1:5050": "http://127.0.0.1:5050/control/readiness",
		"localhost:4040": "http://localhost:4040/control/readiness",
	}
	for addr, want := range cases {
		got, err := readinessEndpoint(addr)
		if err != nil {
			t.Fatalf("readinessEndpoint(%q): %v", addr, err)
		}
		if got != want {
			t.Errorf("readinessEndpoint(%q) = %q, want %q", addr, got, want)
		}
	}
	if _, err := readinessEndpoint("not-an-address"); err == nil {
		t.Fatal("invalid bind address unexpectedly accepted")
	}
}
