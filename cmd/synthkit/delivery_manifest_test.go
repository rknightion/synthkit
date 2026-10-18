// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"compress/gzip"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/selfobs"
	metrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

const manifestTestYAML = `name: manifest
environments:
  - name: prod
    cloud: {provider: aws, account_id: "100000000003", region: eu-west-1, vpc_id: vpc-manifest}
    cluster:
      type: eks
      name: manifest-cluster
      node_groups: [{name: worker, instance_type: m6i.large, desired: 1}]
workloads:
  - type: web_service
    name: manifest-api
    runs_on: manifest-cluster
`

func manifestRunner(t *testing.T) *runner.Runner {
	t.Helper()
	r := runner.New(runner.Sinks{Metrics: &coretest.MetricCapture{}, Logs: &coretest.LogCapture{}, Traces: &coretest.TraceCapture{}}, runner.Catalog(), runner.Options{})
	res, err := blueprint.Load([]byte(manifestTestYAML), runner.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AddBlueprint(res); err != nil {
		t.Fatal(err)
	}
	return r
}

// Real role-scoped exports: the standby provider reports the running set's lanes as standby and
// the leader provider as expected, never dropping them, under one session ID.
func TestHADeliveryManifestStandbyAndLeaderExports(t *testing.T) {
	originalWriter := log.Writer()
	t.Cleanup(func() { log.SetOutput(originalWriter) })
	var mu sync.Mutex
	states := map[string]map[string]string{} // role -> sink -> state
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer w.WriteHeader(http.StatusOK)
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			defer gz.Close()
			reader = gz
		}
		body, err := io.ReadAll(reader)
		if err != nil || r.URL.Path != "/v1/metrics" {
			return
		}
		var req metrics.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for _, rm := range req.ResourceMetrics {
			role, session := "", ""
			for _, a := range rm.Resource.Attributes {
				switch a.Key {
				case "ha.role":
					role = a.Value.GetStringValue()
				case selfobs.AttrSessionID:
					session = a.Value.GetStringValue()
				}
			}
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					if m.Name != selfobs.MetricManifestLane {
						continue
					}
					if session != selfobs.SessionID(haTestIdentity) {
						t.Errorf("%s manifest exported under session %q", role, session)
					}
					for _, dp := range m.GetGauge().GetDataPoints() {
						attrs := map[string]string{}
						for _, a := range dp.Attributes {
							attrs[a.Key] = a.Value.GetStringValue()
						}
						if states[role] == nil {
							states[role] = map[string]string{}
						}
						states[role][attrs["sink"]] = attrs["state"]
					}
				}
			}
		}
	}))
	defer srv.Close()
	cfg := &config.Config{SelfObsEnabled: true, SelfOTLPEndpoint: srv.URL, SelfOTLPUser: "user", SelfOTLPPassword: "token", SelfObsMetricInterval: time.Hour}
	for _, role := range []ha.Role{ha.RoleStandby, ha.RoleLeader} {
		ops, err := startHAOperational(cfg, &haView{runner: manifestRunner(t)}, role, haTestIdentity)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err = ops.stop(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, sink := range []string{"promrw", "loki", "otlp"} {
		if states["standby"][sink] != string(runner.LaneStandby) || states["leader"][sink] != string(runner.LaneExpected) {
			t.Fatalf("role manifest states = %v, want %s standby/expected", states, sink)
		}
	}
}

func TestDeliveryManifestSourceDryRun(t *testing.T) {
	m := deliveryManifestSource(manifestRunner(t), false, true)()
	if len(m.Lanes) == 0 || len(m.Fingerprint) != 64 {
		t.Fatalf("dry-run manifest = %+v", m)
	}
	for _, lane := range m.Lanes {
		if lane.State != string(runner.LaneDryRun) || lane.FreshAfter <= 0 {
			t.Fatalf("dry-run lane = %+v, want dry_run with a bound", lane)
		}
	}
}

// The HA-off composition root exports the manifest: a one-shot run with real self-observability
// against a local OTLP receiver reports the selected blueprint's lanes as dry_run. Removing the
// main.go DeliveryManifest wiring fails this test.
func TestHAOffMainExportsDeliveryManifest(t *testing.T) {
	originalWriter := log.Writer()
	t.Cleanup(func() { log.SetOutput(originalWriter) })
	var mu sync.Mutex
	states := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer w.WriteHeader(http.StatusOK)
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				return
			}
			defer gz.Close()
			reader = gz
		}
		body, err := io.ReadAll(reader)
		if err != nil || r.URL.Path != "/v1/metrics" {
			return
		}
		var req metrics.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for _, rm := range req.ResourceMetrics {
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					if m.Name != selfobs.MetricManifestLane {
						continue
					}
					for _, dp := range m.GetGauge().GetDataPoints() {
						attrs := map[string]string{}
						for _, a := range dp.Attributes {
							attrs[a.Key] = a.Value.GetStringValue()
						}
						states[attrs["sink"]] = attrs["state"]
					}
				}
			}
		}
	}))
	defer srv.Close()
	root := t.TempDir()
	dir := filepath.Join(root, "blueprints")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(manifestTestYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"HA_MODE": "off", "DRY_RUN": "true", "STATE_BACKEND": "file",
		"BLUEPRINTS": dir, "BLUEPRINT_NAMES": "manifest", "BLUEPRINT_DATA_DIR": filepath.Join(root, "data"),
		"CONFIG_SNAPSHOT_PATH": filepath.Join(root, "state.json"),
		"SELFOBS_ENABLED":      "true", "GC_SELF_OTLP_ENDPOINT": srv.URL, "GC_SELF_OTLP_USER": "user",
		"GC_SELF_OTLP_PASSWORD": "token", "SELFOBS_METRIC_INTERVAL": "1h", "OTEL_RESOURCE_ATTRIBUTES": "",
	} {
		t.Setenv(k, v)
	}
	if err := runMode(true, false, false, ""); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, sink := range []string{"promrw", "loki", "otlp"} {
		if states[sink] != string(runner.LaneDryRun) {
			t.Fatalf("HA-off main manifest states = %v, want %s dry_run", states, sink)
		}
	}
}
