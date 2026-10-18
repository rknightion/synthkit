// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/optionallane"
)

// haParityBootYAML declares no optional lane, so any optional-lane fact in these tests can only
// come from a restored live blueprint.
const haParityBootYAML = `name: boot-plain
environments:
  - name: prod
    cloud: {provider: aws, account_id: "100000000004", region: eu-west-1, vpc_id: vpc-boot}
    cluster:
      type: eks
      name: boot-cluster
      node_groups: [{name: worker, instance_type: m6i.large, desired: 1}]
workloads:
  - type: web_service
    name: boot-api
    runs_on: boot-cluster
`

// haParityFleetYAML declares the fleet_management construct, an optional lane.
const haParityFleetYAML = `name: live-fleet
environments:
  - name: prod
features:
  fleet_management:
    enabled: true
    collectors_per_os: {linux: 1}
`

// haParityWarnYAML is valid but carries a daily incident window of 24h, which the shape engine
// skips with a warning.
const haParityWarnYAML = `name: live-warn
environments:
  - name: prod
    cloud: {provider: aws, account_id: "100000000005", region: eu-west-1, vpc_id: vpc-warn}
    cluster:
      type: eks
      name: warn-cluster
      node_groups: [{name: worker, instance_type: m6i.large, desired: 1}]
workloads:
  - type: web_service
    name: warn-api
    runs_on: warn-cluster
incidents:
  - {kind: pod_crashloop, target: warn-cluster, at: "10:00", for: 24h, intensity: 0.5}
`

// haParityEnv is a lease-mode file-backend configuration whose persisted state holds live
// blueprints: one that declares an optional lane, one that warns, and one that cannot be restored.
func haParityEnv(t *testing.T) *config.Config {
	t.Helper()
	cfg := startupKindsHAConfig(t)
	cfg.BlueprintNames = []string{"boot-plain"}
	if err := os.WriteFile(filepath.Join(cfg.BlueprintsDir, "fleet.yaml"), []byte(haParityBootYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewStore(cfg.SnapshotPath).UpdateContext(context.Background(), func(s *control.State) {
		s.LiveBlueprints = []control.LiveBlueprint{
			liveSMRecord("live-fleet", haParityFleetYAML),
			liveSMRecord("live-warn", haParityWarnYAML),
			liveSMRecord("live-broken", "name: live-broken\nenvironments: [not, a, mapping]\n"),
		}
	}); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func haParityGET(t *testing.T, v *haView, path string, into any) {
	t.Helper()
	rec := httptest.NewRecorder()
	v.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		t.Fatalf("GET %s body %q: %v", path, rec.Body.String(), err)
	}
}

func haParityActivate(t *testing.T, cfg *config.Config) *haView {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	gate := ha.NewGate()
	bound := ha.Bounded{Gate: gate, Timeout: 8 * time.Second, Margin: 2 * time.Second, Crash: func() { t.Error("unexpected crash") }}
	var leader *haView
	if err := gate.Activate(ctx, func(prep context.Context) error {
		var err error
		leader, err = newHAView(prep, cfg, gate, bound, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return leader
}

// A persisted live blueprint the HA leader's restore skipped, and the incident-schedule warnings
// of a restored one, reach GET /control/diagnostics (they reached only the logs before).
func TestHALeaderRestoreDiagnosticsReachControl(t *testing.T) {
	leader := haParityActivate(t, haParityEnv(t))
	var diags []control.Diagnostic
	haParityGET(t, leader, "/control/diagnostics", &diags)
	var skip, warn bool
	for _, d := range diags {
		switch {
		case d.Level == "error" && d.Category == "live" && d.Source == "synthetic/live-broken":
			skip = true
		case d.Level == "warning" && d.Category == "incident" && d.Source == "synthetic/live-warn":
			warn = true
		}
	}
	if !skip || !warn {
		t.Fatalf("diagnostics missing restore skip=%v shape warning=%v: %+v", skip, warn, diags)
	}
}

// Optional-lane dispositions follow the running set, so a lane declared only by a restored live
// blueprint is requested; the boot set alone declares none.
func TestHAOptionalLanesIncludeRestoredLiveBlueprints(t *testing.T) {
	leader := haParityActivate(t, haParityEnv(t))
	var status struct {
		Optional []optionallane.Disposition `json:"optional_lanes"`
	}
	haParityGET(t, leader, "/control/status", &status)
	for _, d := range status.Optional {
		if d.Lane == optionallane.FleetMetrics {
			if !d.Requested || d.Declaration != optionallane.GateSatisfied {
				t.Fatalf("fleet_metrics = %+v, want requested and declared by the restored live blueprint", d)
			}
			return
		}
	}
	t.Fatalf("no fleet_metrics disposition in %+v", status.Optional)
}

// A standby view builds its own read-only runner: it must not surface restore diagnostics, which
// belong to the process that holds leadership.
func TestHAStandbyViewReportsNoRestoreDiagnostics(t *testing.T) {
	cfg := haParityEnv(t)
	gate := ha.NewGate()
	standby, err := newHAView(context.Background(), cfg, gate, ha.Bounded{Gate: gate}, false)
	if err != nil {
		t.Fatal(err)
	}
	var diags []control.Diagnostic
	haParityGET(t, standby, "/control/diagnostics", &diags)
	for _, d := range diags {
		if strings.HasPrefix(d.Source, "synthetic/") {
			t.Fatalf("standby reported a restore diagnostic: %+v", d)
		}
	}
}
