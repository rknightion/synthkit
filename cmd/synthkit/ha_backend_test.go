// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
	lease "github.com/rknightion/synthkit/internal/ha/kubernetes"
)

// A process-edge double for the stdlib StateBackend seam. The production typed
// ConfigMap adapter is exercised separately in control/backend_kubernetes_test.go.
type processStateBackend struct {
	url    string
	gate   ha.LeaderGate
	client *http.Client
}

func (b processStateBackend) Load(ctx context.Context, k control.Key) (control.Snapshot, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", b.url+"/"+string(k), nil)
	resp, err := b.client.Do(req)
	if err != nil {
		return control.Snapshot{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return control.Snapshot{}, fmt.Errorf("named state read: %d", resp.StatusCode)
	}
	var s control.Snapshot
	err = json.NewDecoder(resp.Body).Decode(&s)
	return s, err
}
func (b processStateBackend) CompareAndSwap(ctx context.Context, k control.Key, r control.Revision, data []byte) (control.Revision, error) {
	var rev control.Revision
	err := b.gate.Do(ctx, ha.Mutation, func(c context.Context) error {
		body, _ := json.Marshal(control.Snapshot{Data: data, Revision: r})
		req, _ := http.NewRequestWithContext(c, "PUT", b.url+"/"+string(k), bytes.NewReader(body))
		resp, err := b.client.Do(req)
		if err != nil {
			return control.ErrOutcomeUnknown
		}
		defer resp.Body.Close()
		if resp.StatusCode == 409 {
			return control.ErrConflict
		}
		if resp.StatusCode != 200 {
			return fmt.Errorf("named state update: %d", resp.StatusCode)
		}
		var out control.Snapshot
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return control.ErrOutcomeUnknown
		}
		rev = out.Revision
		return nil
	})
	return rev, err
}
func TestFileBackendIgnoresUnusedKubernetesSettings(t *testing.T) {
	t.Setenv("HA_MODE", "off")
	t.Setenv("STATE_BACKEND", "file")
	t.Setenv("STATE_GIT_SOURCE_CONFIGMAPS", "not-json")
	t.Setenv("STATE_GIT_SOURCE_MAX_BYTES", "not-an-integer")
	t.Setenv("STATE_CAS_MAX_ATTEMPTS", "not-an-integer")
	if _, err := config.Load(filepath.Join(t.TempDir(), "absent.env")); err != nil {
		t.Fatalf("unused Kubernetes settings broke file mode: %v", err)
	}
}

func TestStateBackendConfigAcceptsNamedSingleEmitter(t *testing.T) {
	t.Setenv("HA_MODE", "off")
	t.Setenv("STATE_BACKEND", "kubernetes")
	t.Setenv("HA_NAMESPACE", "test")
	t.Setenv("STATE_CONTROL_CONFIGMAP", "control")
	t.Setenv("STATE_BOOT_CONFIGMAP", "boot")
	t.Setenv("STATE_GIT_SOURCE_CONFIGMAPS", `{"source_key":"git-source"}`)
	cfg, err := config.Load(filepath.Join(t.TempDir(), "absent.env"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StateGitSourceMaxBytes != 786432 || cfg.StateCASMaxAttempts != 5 || cfg.StateGitSourceConfigMaps["source_key"] != "git-source" {
		t.Fatal("frozen defaults/mapping drift")
	}
	t.Setenv("STATE_GIT_SOURCE_MAX_BYTES", "786433")
	if _, err := config.Load(""); err == nil {
		t.Fatal("hard cap widened")
	}
	t.Setenv("STATE_GIT_SOURCE_MAX_BYTES", "786432")
	t.Setenv("STATE_CAS_MAX_ATTEMPTS", "0")
	if _, err := config.Load(""); err == nil {
		t.Fatal("invalid retry budget accepted")
	}
	t.Setenv("STATE_CAS_MAX_ATTEMPTS", "5")
	t.Setenv("STATE_GIT_SOURCE_CONFIGMAPS", `{"source":"control"}`)
	if _, err := config.Load(""); err == nil {
		t.Fatal("duplicate object mapping accepted")
	}
}

func TestHAKubernetesStateReloadBeforeRealFirstTick(t *testing.T) {
	election, leaseServer := newNamedLeaseRecorder(t)
	defer leaseServer.Close()
	root := t.TempDir()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.HAMode = "lease"
	cfg.StateBackend = "kubernetes"
	cfg.HANamespace = "test"
	cfg.HALeaseName = "lease"
	cfg.PodUID = "pod"
	cfg.DryRun = true
	cfg.SelfObsEnabled = false
	cfg.MasterTick = time.Second
	cfg.HARetryPeriod = 50 * time.Millisecond
	cfg.HARenewDeadline = 300 * time.Millisecond
	cfg.HAKubeRequestTimeout = 100 * time.Millisecond
	cfg.HAReleaseTimeout = time.Second
	cfg.SendDrainDeadline = 2 * time.Second
	cfg.BlueprintsDir = filepath.Join(root, "baked")
	cfg.BlueprintDataDir = filepath.Join(root, "absent-data")
	cfg.BlueprintNames = []string{"cached/mini"}
	cfg.SnapshotPath = filepath.Join(root, "unused.json")
	cfg.FMURL = ""
	cfg.ControlToken = ""
	cfg.SigilEndpoint = ""
	cfg.FaroCollector = ""
	cfg.ProfilesURL = ""
	cfg.GitPollInterval = 0
	cfg.StateCASMaxAttempts = 5
	cfg.StateGitSourceMaxBytes = 786432
	listen, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg.HTTPAddr = listen.Addr().String()
	listen.Close()
	state := control.DefaultState()
	source := control.SourceView{ID: "source", Name: "source", Namespace: "cached", URL: "https://example.invalid/repository", Ref: "main"}
	state.BlueprintSources = []control.SourceView{source}
	fields, _ := json.Marshal([]string{source.ID, source.URL, source.Ref, source.Subpath, source.Namespace, source.TokenEnvVar})
	sum := sha256.Sum256(fields)
	fingerprint := hex.EncodeToString(sum[:])
	docs := map[string]control.Snapshot{}
	var mu sync.Mutex
	writes := 0
	setDocs := func(volume float64, host, sha, rev string) {
		state.VolumeMultiplier = volume
		data, _ := json.Marshal(state)
		docs["control"] = control.Snapshot{Data: data, Revision: control.Revision(rev)}
		data, _ = json.Marshal(map[string]any{"schema_version": 1, "config_fingerprint": fingerprint, "fetched_sha": sha, "files": map[string][]byte{"mini.yaml": []byte("name: mini\nhosts:\n  - name: " + host + "\n    os: linux\n")}, "fetch_status": map[string]any{}, "sequence": 0, "receipt_floor_sequence": 0, "receipts": []any{}})
		docs["git-source/source"] = control.Snapshot{Data: data, Revision: control.Revision(rev)}
	}
	setDocs(1, "first", "before", "1")
	docs["boot-manifest"] = control.Snapshot{Revision: "1"}
	stateServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		key := strings.TrimPrefix(r.URL.Path, "/")
		current, ok := docs[key]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(current)
		case "PUT":
			var next control.Snapshot
			if json.NewDecoder(r.Body).Decode(&next) != nil {
				w.WriteHeader(400)
				return
			}
			if next.Revision != current.Revision {
				w.WriteHeader(409)
				return
			}
			next.Revision = control.Revision(string(current.Revision) + "x")
			docs[key] = next
			writes++
			json.NewEncoder(w).Encode(next)
		default:
			t.Errorf("forbidden API method %s", r.Method)
			w.WriteHeader(403)
		}
	}))
	defer stateServer.Close()
	cfgPath := filepath.Join(root, "config.json")
	writeHAJSON(t, cfgPath, cfg)
	proof := filepath.Join(root, "first-tick.json")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestHAKubernetesStateHelper$")
	cmd.Env = append(os.Environ(), "SYNTHKIT_STATE_HELPER_CONFIG="+cfgPath, "SYNTHKIT_STATE_HELPER_API="+stateServer.URL, "SYNTHKIT_STATE_HELPER_LEASE="+leaseServer.URL, "SYNTHKIT_STATE_HELPER_PROOF="+proof, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	endpoint := "http://" + cfg.HTTPAddr
	awaitHA(t, func() bool { status, _ := haGet(t, endpoint+"/control/state"); return status == 200 })
	mu.Lock()
	if writes != 0 {
		t.Fatal("standby state writes", writes)
	}
	setDocs(7, "second", "after", "2")
	mu.Unlock()
	if _, err := os.Stat(cfg.BlueprintDataDir); !os.IsNotExist(err) {
		t.Fatal("standby created staging directories")
	}
	election.allowAcquisition()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("real runtime: %v\n%s", err, output.String())
		}
	case <-ctx.Done():
		t.Fatal("runtime did not exit", output.String())
	}
	observed, err := os.ReadFile(proof)
	if err != nil {
		t.Fatalf("no real first tick: %v\n%s", err, output.String())
	}
	if !strings.Contains(string(observed), `"volume":7`) || !strings.Contains(string(observed), "second") {
		t.Fatal("first tick used stale state", string(observed))
	}
	if _, err := os.Stat(cfg.BlueprintDataDir); !os.IsNotExist(err) {
		t.Fatal("backend created authoritative disk staging")
	}
	mu.Lock()
	defer mu.Unlock()
	if writes < 3 {
		t.Fatal("startup control/manifest/load-result not committed", writes)
	}
	var doc struct {
		FetchStatus struct {
			LoadedSHA string `json:"loaded_sha"`
		} `json:"fetch_status"`
	}
	if err := json.Unmarshal(docs["git-source/source"].Data, &doc); err != nil || doc.FetchStatus.LoadedSHA != "after" {
		t.Fatal("new blob was not committed before production", doc, err)
	}
	t.Log("real Runtime: standby zero writes; acquisition reread control and git document; ApplyControl/topology checked in first real tick; joined shutdown")
}
func TestHAKubernetesStateHelper(t *testing.T) {
	path := os.Getenv("SYNTHKIT_STATE_HELPER_CONFIG")
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err = json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	deps := productionHADependencies()
	deps.StateBackend = func(gate ha.LeaderGate) control.StateBackend {
		return processStateBackend{url: os.Getenv("SYNTHKIT_STATE_HELPER_API"), gate: gate, client: &http.Client{Timeout: time.Second}}
	}
	deps.Election = func(ctx context.Context, o lease.Options) (leaseElection, error) {
		return lease.NewForClient(ctx, o, os.Getenv("SYNTHKIT_STATE_HELPER_LEASE"), &http.Client{Timeout: o.RequestTimeout})
	}
	deps.BeforeProduction = func(v *haView) {
		v.runner.SetTickObserver(func(ctx context.Context, bp, kind, name string, fn func(context.Context) error) error {
			proof, _ := json.Marshal(map[string]any{"volume": v.runner.VolumeMultiplier(), "inventory": v.runner.Inventory()})
			if v.store.Snapshot().VolumeMultiplier != 7 || v.runner.VolumeMultiplier() != 7 || !bytes.Contains(proof, []byte("second")) {
				t.Error("stale state/topology at first tick")
			}
			if err := os.WriteFile(os.Getenv("SYNTHKIT_STATE_HELPER_PROOF"), proof, 0600); err != nil {
				t.Error(err)
			}
			return fn(ctx)
		})
	}
	if err := runHALifecycle(&cfg, true, make(chan struct{}), deps); err != nil {
		t.Fatal(err)
	}
}
