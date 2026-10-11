// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
)

func readinessHAView(t *testing.T, gate *ha.Gate) (*haView, string) {
	t.Helper()
	root := t.TempDir()
	baked := filepath.Join(root, "baked")
	if err := os.Mkdir(baked, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baked, "test.yaml"), []byte("name: test\nhosts:\n  - name: first\n    os: linux\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.HAMode, cfg.StateBackend = "lease", "file"
	cfg.BlueprintsDir, cfg.BlueprintDataDir = baked, filepath.Join(root, "data")
	cfg.BlueprintNames = []string{"test"}
	// This directory does not exist: standby must not create it or test writes.
	cfg.SnapshotPath = filepath.Join(root, "unwritten", "state.json")
	cfg.DryRun, cfg.SelfObsEnabled = false, false
	cfg.FMURL, cfg.ControlToken, cfg.SigilEndpoint, cfg.FaroCollector, cfg.ProfilesURL = "", "", "", "", ""
	v, err := newHAView(context.Background(), cfg, gate, ha.Bounded{Gate: gate})
	if err != nil {
		t.Fatal(err)
	}
	return v, cfg.SnapshotPath
}

func getReadinessProbe(t *testing.T, handler http.Handler, wantStatus int) control.ReadinessProbe {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/control/readiness", nil))
	if response.Code != wantStatus {
		t.Fatalf("readiness HTTP status=%d, want=%d: %s", response.Code, wantStatus, response.Body.String())
	}
	var probe control.ReadinessProbe
	if err := json.Unmarshal(response.Body.Bytes(), &probe); err != nil {
		t.Fatal(err)
	}
	return probe
}

func TestHAReadinessHandlerBootstrapAndLifecycle(t *testing.T) {
	gate := ha.NewGate()
	v, statePath := readinessHAView(t, gate)
	getReadinessProbe(t, v.handler, http.StatusServiceUnavailable)
	l := &haLifecycle{gate: gate}
	// Production installs this callback only after credential preflight succeeds.
	v.readinessFacts = l.readinessFacts
	probe := getReadinessProbe(t, v.handler, http.StatusOK)
	if !probe.Ready || probe.LiveReady || probe.PersistedState.Writable {
		t.Fatalf("standby probe must be operational but not live/writable: %+v", probe)
	}
	if _, err := os.Stat(filepath.Dir(statePath)); !os.IsNotExist(err) {
		t.Fatalf("standby created persisted-state directory: %v", err)
	}
	server := httptest.NewServer(v.handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := checkReadiness(ctx, server.Client(), server.URL+"/control/readiness"); err != nil {
		t.Fatalf("local healthcheck rejected ready standby: %v", err)
	}
	l.mu.Lock()
	l.activating, l.acquired = true, true
	l.mu.Unlock()
	getReadinessProbe(t, v.handler, http.StatusServiceUnavailable)
	if err := gate.Activate(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	l.activating = false
	l.mu.Unlock()
	// The leader has not probed state or pushed its declared metrics lane.
	probe = getReadinessProbe(t, v.handler, http.StatusServiceUnavailable)
	found := false
	for _, code := range probe.ReasonCodes {
		found = found || code == control.ReadinessDeliveryNotAttempted
	}
	if !found {
		t.Fatalf("leader's never-pushed lane did not hold readiness red: %+v", probe)
	}
	l.mu.Lock()
	l.terminating = true
	l.mu.Unlock()
	getReadinessProbe(t, v.handler, http.StatusServiceUnavailable)
	gate.Revoke()
	l.mu.Lock()
	l.terminating = false
	l.mu.Unlock()
	probe = getReadinessProbe(t, v.handler, http.StatusServiceUnavailable)
	if probe.Ready || probe.LiveReady {
		t.Fatalf("revoked leader became a ready standby: %+v", probe)
	}
}
