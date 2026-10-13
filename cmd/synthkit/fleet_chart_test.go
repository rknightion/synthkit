// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/construct/fleetmgmt"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/fleethook"
	"github.com/rknightion/synthkit/internal/ha"
	lease "github.com/rknightion/synthkit/internal/ha/kubernetes"
	"github.com/rknightion/synthkit/internal/smstore/smstoretest"
	"gopkg.in/yaml.v3"
)

func renderedFleetConfig(t *testing.T) *config.Config {
	t.Helper()
	chart, err := filepath.Abs("../../charts/synthkit")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "helm", "template", "synthkit-test", chart, "--namespace", "test", "--kube-version", "1.31.0", "-f", filepath.Join(chart, "ci-ha", "12-ha-fleet-values.yaml")).CombinedOutput()
	if err != nil {
		t.Fatalf("rendered Fleet runtime prerequisite: %v\n%s", err, out)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(out))
	for {
		var obj struct {
			Kind string
			Data map[string]string
		}
		if err := decoder.Decode(&obj); err != nil {
			if err != io.EOF {
				t.Fatal(err)
			}
			break
		}
		if obj.Kind == "ConfigMap" {
			for k, v := range obj.Data {
				t.Setenv(k, v)
			}
		}
	}
	t.Setenv("HA_NAMESPACE", "test")
	t.Setenv("POD_UID", "fixture-pod")
	cfg, err := config.Load("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StateFleetConfigMap != "synthkit-fleet" {
		t.Fatalf("rendered Fleet slot=%q", cfg.StateFleetConfigMap)
	}
	return cfg
}

func fleetChartBackend(t *testing.T, cfg *config.Config, gate ha.LeaderGate, endpoint string) control.StateBackend {
	t.Helper()
	names := map[control.Key]string{control.Control: cfg.StateControlConfigMap, control.BootManifest: cfg.StateBootConfigMap, control.FleetRegistered: cfg.StateFleetConfigMap}
	for id, name := range cfg.StateGitSourceConfigMaps {
		key, err := control.GitSourceKey(id)
		if err != nil {
			t.Fatal(err)
		}
		names[key] = name
	}
	b, err := control.NewKubernetesBackendForClient(control.KubernetesBackendOptions{Gate: gate, Namespace: cfg.HANamespace, Objects: names, RequestTimeout: cfg.HAKubeRequestTimeout, MaxDocumentBytes: control.MaxStateDocumentBytes}, endpoint, &http.Client{Timeout: cfg.HAKubeRequestTimeout})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFleetRenderedHALifecycleHelper(t *testing.T) {
	path := os.Getenv("FLEET_CHART_CONFIG")
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	deps := productionHADependencies()
	deps.Preflight = func(context.Context, *config.Config) error { return nil }
	deps.StateBackend = func(gate ha.LeaderGate) control.StateBackend {
		b := fleetChartBackend(t, &cfg, gate, os.Getenv("FLEET_CHART_API"))
		// Match production's named precreation checks at this network-only seam.
		keys := []control.Key{control.Control, control.BootManifest, control.FleetRegistered}
		for id := range cfg.StateGitSourceConfigMaps {
			key, _ := control.GitSourceKey(id)
			keys = append(keys, key)
		}
		for _, key := range keys {
			if _, err := b.Load(context.Background(), key); err != nil {
				t.Fatalf("precreated state key %s unavailable: %v", key, err)
			}
		}
		return b
	}
	deps.Election = func(ctx context.Context, o lease.Options) (leaseElection, error) {
		return lease.NewForClient(ctx, o, os.Getenv("FLEET_CHART_API"), &http.Client{Timeout: o.RequestTimeout})
	}
	term := make(chan struct{})
	var once sync.Once
	heartbeats := 0
	deps.BeforeProduction = func(v *haView) {
		v.runner.SetFleetObserver(func(_ context.Context, event fleethook.Event) {
			if event.Op == fleethook.OpHeartbeat {
				heartbeats++
				if heartbeats == 2 {
					once.Do(func() { close(term) })
				}
			}
		})
	}
	if err := runHALifecycle(&cfg, false, term, deps); err != nil {
		t.Fatal(err)
	}
}

func TestFleetRenderedHAStartupAndHandoff(t *testing.T) {
	cfg := renderedFleetConfig(t)
	a := smstoretest.New(t, cfg.StateControlConfigMap, cfg.StateBootConfigMap, cfg.StateFleetConfigMap, cfg.StateGitSourceConfigMaps["sample_source"])
	defer a.Close()
	var mu sync.Mutex
	registrations, heartbeats, unregistrations := 0, 0, 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/RegisterCollector"):
			registrations++
		case strings.HasSuffix(r.URL.Path, "/GetConfig"):
			heartbeats++
		case strings.HasSuffix(r.URL.Path, "/UnregisterCollector"):
			unregistrations++
		default:
			t.Errorf("unexpected Fleet request: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer remote.Close()
	root := t.TempDir()
	cfg.BlueprintsDir = filepath.Join(root, "baked")
	if err := os.Mkdir(cfg.BlueprintsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.BlueprintsDir, "selected.yaml"), []byte("name: selected\nhosts: [{name: selected-host, os: linux}]\nfeatures:\n  fleet_management:\n    enabled: true\n    collectors_per_os: {linux: 2}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.BlueprintNames = []string{"selected"}
	cfg.BlueprintDataDir, cfg.SnapshotPath = filepath.Join(root, "absent-data"), filepath.Join(root, "unused.json")
	cfg.DryRun, cfg.SelfObsEnabled = false, false
	cfg.FMURL, cfg.FMStackID, cfg.FMToken = remote.URL, "test", fleetFakeToken(t)
	cfg.ControlToken, cfg.SigilEndpoint, cfg.FaroCollector, cfg.ProfilesURL = "", "", "", ""
	cfg.PromRWURL, cfg.OTLPEndpoint, cfg.LokiURL = "", "", ""
	cfg.MasterTick = time.Hour
	cfg.GitPollInterval = 0
	roster := fleetmgmt.Roster("selected:fleet_management", "", map[string]int{"linux": 2})
	col := roster[0]
	ids := []string{roster[0].ID, roster[1].ID}
	sort.Strings(ids)
	expected, err := json.Marshal(fleetStateDocument{Version: 2, Destination: fleetDestination(cfg.FMURL, cfg.FMStackID), IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := json.Marshal(fleetStateDocument{Version: 2, Destination: fleetDestination(cfg.FMURL, cfg.FMStackID), IDs: []string{col.ID}})
	if err != nil {
		t.Fatal(err)
	}
	a.Set(cfg.StateFleetConfigMap, initial)
	a.Mu.Lock()
	a.Maps[cfg.StateFleetConfigMap].BinaryData["unrelated"] = []byte("retained bytes")
	a.Mu.Unlock()
	for process := 0; process < 2; process++ {
		a.ReplaceLease(cfg.HALeaseName)
		a.Mu.Lock()
		baselineWrites := a.StateWrites
		a.Mu.Unlock()
		cfg.PodUID = []string{"first-pod", "next-pod"}[process]
		listen, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		cfg.HTTPAddr = listen.Addr().String()
		listen.Close()
		path := filepath.Join(root, "config.json")
		writeHAJSON(t, path, cfg)
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFleetRenderedHALifecycleHelper$")
		cmd.Env = append(os.Environ(), "FLEET_CHART_CONFIG="+path, "FLEET_CHART_API="+a.Server.URL, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
		var output bytes.Buffer
		cmd.Stdout, cmd.Stderr = &output, &output
		if err := cmd.Start(); err != nil {
			cancel()
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		awaitHA(t, func() bool { code, _ := haGet(t, "http://"+cfg.HTTPAddr+"/control/state"); return code == 200 })
		a.Mu.Lock()
		writes, releases := a.StateWrites, a.Releases
		a.Mu.Unlock()
		if writes != baselineWrites {
			t.Fatal("standby wrote state", writes)
		}
		mu.Lock()
		before := registrations + heartbeats + unregistrations
		mu.Unlock()
		if before != process*4 {
			t.Fatal("standby sent Fleet traffic", before)
		}
		a.Permit()
		select {
		case err := <-done:
			if err != nil {
				cancel()
				t.Fatalf("runtime handoff: %v\n%s", err, output.String())
			}
		case <-ctx.Done():
			cancel()
			t.Fatalf("runtime handoff timed out: %s", output.String())
		}
		cancel()
		a.Mu.Lock()
		if a.Releases != releases+1 {
			t.Error("runtime failed to release Lease")
		}
		if a.StateWrites <= writes {
			t.Error("runtime did not write authoritative state")
		}
		obj := a.Maps[cfg.StateFleetConfigMap]
		if obj.Data["unrelated"] != "preserved" || obj.Labels["preserved"] != "yes" || string(obj.BinaryData["unrelated"]) != "retained bytes" {
			t.Error("runtime changed unrelated state")
		}
		requests := append([]string(nil), a.Requests...)
		a.Mu.Unlock()
		if !bytes.Equal(a.Data(cfg.StateFleetConfigMap), expected) {
			t.Fatal("startup/handoff replaced existing Fleet ledger", string(a.Data(cfg.StateFleetConfigMap)))
		}
		for _, request := range requests {
			if !strings.HasPrefix(request, "GET ") && !strings.HasPrefix(request, "PUT ") {
				t.Fatal("unexpected API access", request)
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if registrations != 4 || heartbeats != 4 || unregistrations != 0 {
		t.Fatalf("startup/handoff Fleet registered=%d heartbeat=%d unregistered=%d", registrations, heartbeats, unregistrations)
	}
	t.Log("rendered named mapping: two real HA lifecycle processes, standby read-only, startup and successor heartbeats, joined Lease release, prior Fleet ID retained, new ID persisted to named slot, unrelated state preserved")
}

func TestFleetRenderedHAMissingStateFailsClosed(t *testing.T) {
	cfg := renderedFleetConfig(t)
	a := smstoretest.New(t, cfg.StateControlConfigMap, cfg.StateBootConfigMap)
	defer a.Close()
	requests := 0
	var mu sync.Mutex
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mu.Lock(); requests++; mu.Unlock(); w.WriteHeader(500) }))
	defer remote.Close()
	cfg.DryRun, cfg.FMURL, cfg.FMStackID, cfg.FMToken = false, remote.URL, "test", fleetFakeToken(t)
	path := filepath.Join(t.TempDir(), "config.json")
	writeHAJSON(t, path, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFleetRenderedHALifecycleHelper$")
	cmd.Env = append(os.Environ(), "FLEET_CHART_CONFIG="+path, "FLEET_CHART_API="+a.Server.URL, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || !bytes.Contains(out, []byte("precreated state key fleet-registered unavailable")) {
		t.Fatalf("startup did not reject missing named Fleet state: %v %s", err, out)
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	if a.StateWrites != 0 || a.LeaseWrites != 0 {
		t.Fatal("missing state startup wrote/acquired", a.StateWrites, a.LeaseWrites)
	}
	namedRead := false
	for _, req := range a.Requests {
		if !strings.HasPrefix(req, "GET /api/v1/namespaces/test/configmaps/") {
			t.Fatal("startup manufactured/discovered resources", req)
		}
		namedRead = namedRead || req == "GET /api/v1/namespaces/test/configmaps/"+cfg.StateFleetConfigMap
	}
	if !namedRead {
		t.Fatal("startup did not use rendered Fleet name")
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 0 {
		t.Fatal("missing state startup sent Fleet requests", requests)
	}
}
