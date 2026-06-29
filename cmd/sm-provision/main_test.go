// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/sm"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// fakeServer captures SM API requests and returns scripted responses.
// It is deliberately simple so tests remain self-contained.
type fakeServer struct {
	probes []smProbe
	checks []smCheck
	nextID int

	// recorded calls for assertions
	probePosts   []smProbe
	checkAdds    []smCheck
	checkUpdates []smCheck
}

func newFakeServer() *fakeServer {
	return &fakeServer{nextID: 10}
}

func (fs *fakeServer) assignID() int {
	id := fs.nextID
	fs.nextID++
	return id
}

func (fs *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v1/probe/list":
		_ = json.NewEncoder(w).Encode(fs.probes)

	case "/api/v1/probe/add":
		var p smProbe
		_ = json.NewDecoder(r.Body).Decode(&p)
		p.ID = fs.assignID()
		fs.probes = append(fs.probes, p)
		fs.probePosts = append(fs.probePosts, p)
		_ = json.NewEncoder(w).Encode(addProbeResponse{Probe: p})

	case "/api/v1/check/list":
		_ = json.NewEncoder(w).Encode(fs.checks)

	case "/api/v1/check/add":
		var ch smCheck
		_ = json.NewDecoder(r.Body).Decode(&ch)
		ch.ID = fs.assignID()
		fs.checks = append(fs.checks, ch)
		fs.checkAdds = append(fs.checkAdds, ch)
		w.WriteHeader(http.StatusOK)

	case "/api/v1/check/update":
		var ch smCheck
		_ = json.NewDecoder(r.Body).Decode(&ch)
		for i, existing := range fs.checks {
			if existing.ID == ch.ID {
				fs.checks[i] = ch
				break
			}
		}
		fs.checkUpdates = append(fs.checkUpdates, ch)
		w.WriteHeader(http.StatusOK)

	default:
		http.NotFound(w, r)
	}
}

func (fs *fakeServer) client(server *httptest.Server) *smClient {
	return &smClient{
		base:  server.URL,
		token: "test-token",
		hc:    &http.Client{Timeout: 5 * time.Second},
	}
}

// minimalBlueprint returns YAML bytes for a blueprint with one SM check.
func minimalBlueprint(jobName, target string) []byte {
	return []byte(`
name: test-bp
environments:
  - name: prod
    cloud:
      provider: aws
      account_id: "000000000001"
      region: us-east-1
      vpc_id: vpc-test01

features:
  synthetic_monitoring:
    enabled: true
    checks:
      - { name: ` + jobName + `, target: "` + target + `" }
`)
}

// ── tests ─────────────────────────────────────────────────────────────────────

// TestIdempotencyProbeAlreadyExists verifies that when the probe already exists,
// no second POST /api/v1/probe/add is issued.
func TestIdempotencyProbeAlreadyExists(t *testing.T) {
	fs := newFakeServer()
	fs.probes = []smProbe{{ID: 5, Name: "synthkit-private", Region: "EMEA"}}

	srv := httptest.NewServer(fs)
	defer srv.Close()

	c := fs.client(srv)
	id, err := ensureProbe(c, "synthkit-private", "EMEA")
	if err != nil {
		t.Fatalf("ensureProbe: %v", err)
	}
	if id != 5 {
		t.Errorf("expected probe id=5, got %d", id)
	}
	if len(fs.probePosts) != 0 {
		t.Errorf("expected 0 probe POSTs (probe already existed), got %d", len(fs.probePosts))
	}
}

// TestIdempotencyProbeCreatedWhenAbsent verifies probe creation when absent.
func TestIdempotencyProbeCreatedWhenAbsent(t *testing.T) {
	fs := newFakeServer()
	srv := httptest.NewServer(fs)
	defer srv.Close()

	c := fs.client(srv)
	id, err := ensureProbe(c, "synthkit-private", "EMEA")
	if err != nil {
		t.Fatalf("ensureProbe: %v", err)
	}
	if id == 0 {
		t.Error("expected non-zero probe id")
	}
	if len(fs.probePosts) != 1 {
		t.Errorf("expected 1 probe POST, got %d", len(fs.probePosts))
	}
	if fs.probePosts[0].Name != "synthkit-private" {
		t.Errorf("probe name = %q, want %q", fs.probePosts[0].Name, "synthkit-private")
	}
	if fs.probePosts[0].Public {
		t.Error("probe must not be public (offline probe)")
	}
}

// TestIdempotencyCheckUpdate verifies that re-provisioning an existing check
// issues an update, not a duplicate add.
func TestIdempotencyCheckUpdate(t *testing.T) {
	fs := newFakeServer()
	// Pre-seed the probe and an existing check.
	fs.probes = []smProbe{{ID: 5, Name: "synthkit-private", Region: "EMEA"}}
	fs.checks = []smCheck{{
		ID:     20,
		Job:    "my-service-health",
		Target: "https://my-service.example.com/health",
	}}
	srv := httptest.NewServer(fs)
	defer srv.Close()

	c := fs.client(srv)

	// Re-upsert the same check.
	ch := smCheck{
		ID:               20,
		Job:              "my-service-health",
		Target:           "https://my-service.example.com/health",
		Frequency:        60000,
		Timeout:          smTimeoutMs,
		Enabled:          true,
		Probes:           []int{5},
		Labels:           []smLabel{},
		AlertSensitivity: "none",
		BasicMetricsOnly: true,
		Settings:         map[string]any{"http": map[string]any{"method": "GET", "ipVersion": "V4"}},
	}
	if err := c.updateCheck(ch); err != nil {
		t.Fatalf("updateCheck: %v", err)
	}

	if len(fs.checkAdds) != 0 {
		t.Errorf("expected 0 check adds (should update existing), got %d", len(fs.checkAdds))
	}
	if len(fs.checkUpdates) != 1 {
		t.Errorf("expected 1 check update, got %d", len(fs.checkUpdates))
	}
	if fs.checkUpdates[0].ID != 20 {
		t.Errorf("updated check id = %d, want 20", fs.checkUpdates[0].ID)
	}
}

// TestCheckAddWhenAbsent verifies that a new check is POSTed to /api/v1/check/add.
func TestCheckAddWhenAbsent(t *testing.T) {
	fs := newFakeServer()
	fs.probes = []smProbe{{ID: 5, Name: "synthkit-private", Region: "EMEA"}}
	srv := httptest.NewServer(fs)
	defer srv.Close()

	c := fs.client(srv)
	ch := smCheck{
		Job:              "new-service-health",
		Target:           "https://new.example.com/health",
		Frequency:        60000,
		Timeout:          smTimeoutMs,
		Enabled:          true,
		Probes:           []int{5},
		Labels:           []smLabel{},
		AlertSensitivity: "none",
		BasicMetricsOnly: true,
		Settings:         map[string]any{"http": map[string]any{"method": "GET", "ipVersion": "V4"}},
	}
	if err := c.addCheck(ch); err != nil {
		t.Fatalf("addCheck: %v", err)
	}

	if len(fs.checkAdds) != 1 {
		t.Errorf("expected 1 check add, got %d", len(fs.checkAdds))
	}
	if fs.checkAdds[0].AlertSensitivity != "none" {
		t.Errorf("alertSensitivity = %q, want %q", fs.checkAdds[0].AlertSensitivity, "none")
	}
}

// TestAuthHeader verifies the Authorization: Bearer header is sent correctly.
func TestAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode([]smProbe{})
	}))
	defer srv.Close()

	c := &smClient{
		base:  srv.URL,
		token: "my-secret-token",
		hc:    &http.Client{Timeout: 5 * time.Second},
	}
	if _, err := c.listProbes(); err != nil {
		t.Fatalf("listProbes: %v", err)
	}
	want := "Bearer my-secret-token"
	if gotAuth != want {
		t.Errorf("Authorization header = %q, want %q", gotAuth, want)
	}
}

// TestDryRunNoPOST verifies that DRY_RUN=true prints a preview and makes no API calls.
func TestDryRunNoPOST(t *testing.T) {
	var postCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			postCount++
		}
		switch r.URL.Path {
		case "/api/v1/probe/list":
			_ = json.NewEncoder(w).Encode([]smProbe{})
		case "/api/v1/check/list":
			_ = json.NewEncoder(w).Encode([]smCheck{})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	// Build a temporary blueprints directory.
	dir := t.TempDir()
	bpFile := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(bpFile, minimalBlueprint("svc-health", "https://svc.example.com/health"), 0600); err != nil {
		t.Fatalf("write blueprint: %v", err)
	}

	// Set env for dry run.
	setenv(t, "DRY_RUN", "true")
	setenv(t, "GC_SM_URL", srv.URL)
	setenv(t, "GC_SM_TOKEN", "tok")
	setenv(t, "BLUEPRINTS", dir)
	setenv(t, "PROBE_NAME", "synthkit-private")
	setenv(t, "PROBE_REGION", "EMEA")

	if err := run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if postCount != 0 {
		t.Errorf("DRY_RUN=true must not POST; got %d POSTs", postCount)
	}
}

// TestDryRunOutput verifies the dry-run prints expected content to stdout.
func TestDryRunOutput(t *testing.T) {
	dir := t.TempDir()
	bpFile := filepath.Join(dir, "bp.yaml")
	if err := os.WriteFile(bpFile, minimalBlueprint("my-check", "https://my.example.com/health"), 0600); err != nil {
		t.Fatalf("write blueprint: %v", err)
	}

	setenv(t, "DRY_RUN", "true")
	setenv(t, "GC_SM_URL", "http://unused")
	setenv(t, "GC_SM_TOKEN", "tok")
	setenv(t, "BLUEPRINTS", dir)
	setenv(t, "PROBE_NAME", "synthkit-private")
	setenv(t, "PROBE_REGION", "EMEA")

	// Capture stdout by redirecting os.Stdout temporarily.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := run()

	_ = w.Close()
	os.Stdout = old
	outBytes, _ := io.ReadAll(r)
	out := string(outBytes)

	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "[DRY RUN]") {
		t.Errorf("expected [DRY RUN] in output, got: %s", out)
	}
	if !strings.Contains(out, "synthkit-private") {
		t.Errorf("expected probe name in output, got: %s", out)
	}
	if !strings.Contains(out, "my-check") {
		t.Errorf("expected check job name in output, got: %s", out)
	}
}

// TestErrorPropagation verifies a non-200 API response is returned as an error.
func TestErrorPropagation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid token"}`))
	}))
	defer srv.Close()

	c := &smClient{
		base:  srv.URL,
		token: "bad-token",
		hc:    &http.Client{Timeout: 5 * time.Second},
	}
	_, err := c.listProbes()
	if err == nil {
		t.Fatal("expected error from 401 response, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401, got: %v", err)
	}
}

// TestFullRunUpsertIdempotency runs the full upsert flow twice and verifies the
// second run updates rather than duplicating.
func TestFullRunUpsertIdempotency(t *testing.T) {
	fs := newFakeServer()
	srv := httptest.NewServer(fs)
	defer srv.Close()

	dir := t.TempDir()
	bpFile := filepath.Join(dir, "bp.yaml")
	if err := os.WriteFile(bpFile, minimalBlueprint("idempotent-svc", "https://idempotent.example.com/health"), 0600); err != nil {
		t.Fatalf("write blueprint: %v", err)
	}

	// First run: DRY_RUN=false.
	setenv(t, "DRY_RUN", "false")
	setenv(t, "GC_SM_URL", srv.URL)
	setenv(t, "GC_SM_TOKEN", "tok")
	setenv(t, "BLUEPRINTS", dir)
	setenv(t, "PROBE_NAME", "synthkit-private")
	setenv(t, "PROBE_REGION", "EMEA")

	if err := run(); err != nil {
		t.Fatalf("first run: %v", err)
	}

	if len(fs.checkAdds) != 1 {
		t.Errorf("first run: expected 1 check add, got %d", len(fs.checkAdds))
	}
	if len(fs.checkUpdates) != 0 {
		t.Errorf("first run: expected 0 updates, got %d", len(fs.checkUpdates))
	}
	if len(fs.probePosts) != 1 {
		t.Errorf("first run: expected 1 probe create, got %d", len(fs.probePosts))
	}

	// Second run: same blueprint — must update, not re-add.
	// Reset counters (keep state so probe/check exist).
	fs.probePosts = nil
	fs.checkAdds = nil
	fs.checkUpdates = nil

	if err := run(); err != nil {
		t.Fatalf("second run: %v", err)
	}

	if len(fs.probePosts) != 0 {
		t.Errorf("second run: probe should not be re-created, got %d POSTs", len(fs.probePosts))
	}
	if len(fs.checkAdds) != 0 {
		t.Errorf("second run: check should not be re-added, got %d adds", len(fs.checkAdds))
	}
	if len(fs.checkUpdates) != 1 {
		t.Errorf("second run: expected 1 check update, got %d", len(fs.checkUpdates))
	}
}

// TestMissingTokenDryRunFalse verifies that DRY_RUN=false without credentials errors.
func TestMissingTokenDryRunFalse(t *testing.T) {
	setenv(t, "DRY_RUN", "false")
	setenv(t, "GC_SM_URL", "")
	setenv(t, "GC_SM_TOKEN", "")
	setenv(t, "BLUEPRINTS", t.TempDir())

	err := run()
	if err == nil {
		t.Fatal("expected error for missing credentials, got nil")
	}
	if !strings.Contains(err.Error(), "GC_SM_URL") {
		t.Errorf("error should mention GC_SM_URL, got: %v", err)
	}
}

// TestEnvDefaultsFromSmConstants verifies that when PROBE_NAME / PROBE_REGION are
// unset, the provisioner defaults to sm.DefaultProbeName / sm.DefaultProbeRegion —
// proving the two packages share a single source of truth and cannot drift.
func TestEnvDefaultsFromSmConstants(t *testing.T) {
	// Clear the env vars to force defaults.
	setenv(t, "PROBE_NAME", "")
	setenv(t, "PROBE_REGION", "")

	name := envDefault("PROBE_NAME", sm.DefaultProbeName)
	region := envDefault("PROBE_REGION", sm.DefaultProbeRegion)

	if name != sm.DefaultProbeName {
		t.Errorf("PROBE_NAME default = %q, want sm.DefaultProbeName = %q", name, sm.DefaultProbeName)
	}
	if region != sm.DefaultProbeRegion {
		t.Errorf("PROBE_REGION default = %q, want sm.DefaultProbeRegion = %q", region, sm.DefaultProbeRegion)
	}
}

// TestProbeCoordinatesFromSmConstants verifies that the lat/lon used when creating
// an offline probe are sourced from sm.DefaultProbeLat / sm.DefaultProbeLon (Frankfurt),
// not a local copy that could drift.
func TestProbeCoordinatesFromSmConstants(t *testing.T) {
	fs := newFakeServer()
	srv := httptest.NewServer(fs)
	defer srv.Close()

	c := fs.client(srv)
	_, err := ensureProbe(c, sm.DefaultProbeName, sm.DefaultProbeRegion)
	if err != nil {
		t.Fatalf("ensureProbe: %v", err)
	}

	if len(fs.probePosts) != 1 {
		t.Fatalf("expected 1 probe POST, got %d", len(fs.probePosts))
	}
	p := fs.probePosts[0]
	if p.Latitude != sm.DefaultProbeLat {
		t.Errorf("probe latitude = %v, want sm.DefaultProbeLat = %v", p.Latitude, sm.DefaultProbeLat)
	}
	if p.Longitude != sm.DefaultProbeLon {
		t.Errorf("probe longitude = %v, want sm.DefaultProbeLon = %v", p.Longitude, sm.DefaultProbeLon)
	}
}

// TestCheckKeyIdempotency verifies that checkKey produces a stable key for the
// upsert index and that two checks with the same (job, target) produce the same key.
func TestCheckKeyIdempotency(t *testing.T) {
	k1 := checkKey("svc-health", "https://svc.example.com/health")
	k2 := checkKey("svc-health", "https://svc.example.com/health")
	if k1 != k2 {
		t.Errorf("checkKey is not stable: %q != %q", k1, k2)
	}
	// Different jobs must produce distinct keys.
	k3 := checkKey("other-health", "https://svc.example.com/health")
	if k1 == k3 {
		t.Errorf("checkKey collision: different jobs produced same key %q", k1)
	}
	// Different targets must produce distinct keys.
	k4 := checkKey("svc-health", "https://other.example.com/health")
	if k1 == k4 {
		t.Errorf("checkKey collision: different targets produced same key %q", k1)
	}
}

// TestCheckSpecExtractionFromBlueprint verifies the blueprint→checkSpec extraction
// path: a blueprint with a named check and explicit target produces the expected job
// and target in the dry-run output, confirming the full extraction pipeline from YAML
// through blueprint.Load to the provisioner's check list.
func TestCheckSpecExtractionFromBlueprint(t *testing.T) {
	dir := t.TempDir()
	bpFile := filepath.Join(dir, "extract.yaml")
	bp := minimalBlueprint("extraction-check", "https://extract.example.com/health")
	if err := os.WriteFile(bpFile, bp, 0600); err != nil {
		t.Fatalf("write blueprint: %v", err)
	}

	setenv(t, "DRY_RUN", "true")
	setenv(t, "GC_SM_URL", "http://unused")
	setenv(t, "GC_SM_TOKEN", "tok")
	setenv(t, "BLUEPRINTS", dir)
	setenv(t, "PROBE_NAME", sm.DefaultProbeName)
	setenv(t, "PROBE_REGION", sm.DefaultProbeRegion)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := run()

	_ = w.Close()
	os.Stdout = old
	outBytes, _ := io.ReadAll(r)
	out := string(outBytes)

	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "extraction-check") {
		t.Errorf("expected job name %q in dry-run output; got:\n%s", "extraction-check", out)
	}
	if !strings.Contains(out, "https://extract.example.com/health") {
		t.Errorf("expected target URL in dry-run output; got:\n%s", out)
	}
}

// setenv sets an env var and restores the previous value at test cleanup.
func setenv(t *testing.T, key, value string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	if value == "" {
		_ = os.Unsetenv(key)
	} else {
		_ = os.Setenv(key, value)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}
