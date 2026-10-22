// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/construct/fleetmgmt"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/fleet"
	"github.com/rknightion/synthkit/internal/fleethook"
	"github.com/rknightion/synthkit/internal/ha"
	lease "github.com/rknightion/synthkit/internal/ha/kubernetes"
)

func TestFleetHALifecycleHelper(t *testing.T) {
	path := os.Getenv("FLEET_HA_HELPER_CONFIG")
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
	deps.Election = func(ctx context.Context, opts lease.Options) (leaseElection, error) {
		return lease.NewForClient(ctx, opts, os.Getenv("FLEET_HA_HELPER_LEASE"), &http.Client{Timeout: opts.RequestTimeout})
	}
	term := make(chan struct{})
	var once sync.Once
	if os.Getenv("FLEET_HA_HELPER_SIGNAL") == "1" {
		cfg.FMToken = os.Getenv("FLEET_HA_HELPER_TOKEN")
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM)
		defer signal.Stop(signals)
		go func() {
			<-signals
			once.Do(func() { close(term) })
		}()
	}
	if !cfg.FleetUnregister {
		deps.BeforeProduction = func(view *haView) {
			view.runner.SetFleetObserver(func(_ context.Context, event fleethook.Event) {
				if event.Op == fleethook.OpHeartbeat {
					once.Do(func() { close(term) })
				}
			})
		}
	}
	if err := runHALifecycle(&cfg, cfg.FleetUnregister, term, deps); err != nil {
		t.Fatal(err)
	}
}

func TestFleetHAGracefulRestartKeepsIDsAndExplicitTeardown(t *testing.T) {
	for _, teardown := range []bool{false, true} {
		t.Run(map[bool]string{false: "restart", true: "teardown"}[teardown], func(t *testing.T) {
			api, leaseServer := newNamedLeaseRecorder(t)
			defer leaseServer.Close()
			api.allowAcquisition()
			root := t.TempDir()
			baked := filepath.Join(root, "baked")
			if err := os.Mkdir(baked, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(baked, "selected.yaml"), []byte("name: selected\nhosts: [{name: selected-host, os: linux}]\nfeatures:\n  fleet_management:\n    enabled: true\n    collectors_per_os: {linux: 1}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var deleted []string
			registrations := 0
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				var body struct {
					ID string `json:"id"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if strings.HasSuffix(r.URL.Path, "/UnregisterCollector") {
					deleted = append(deleted, body.ID)
				}
				if strings.HasSuffix(r.URL.Path, "/RegisterCollector") {
					registrations++
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer remote.Close()
			cfg, err := config.Load(filepath.Join(root, "absent.env"))
			if err != nil {
				t.Fatal(err)
			}
			cfg.HAMode = "lease"
			cfg.StateBackend = "file"
			cfg.HANamespace = "test"
			cfg.HALeaseName = "lease"
			cfg.PodUID = "process"
			cfg.FleetUnregister = teardown
			cfg.DryRun = false
			cfg.SelfObsEnabled = false
			cfg.MasterTick = time.Hour
			cfg.HARetryPeriod = 50 * time.Millisecond
			cfg.HARenewDeadline = 300 * time.Millisecond
			cfg.HAKubeRequestTimeout = 100 * time.Millisecond
			cfg.HAReleaseTimeout = time.Second
			cfg.SendDrainDeadline = 2 * time.Second
			cfg.BlueprintsDir = baked
			cfg.BlueprintDataDir = filepath.Join(root, "data")
			cfg.BlueprintNames = []string{"selected"}
			cfg.SnapshotPath = filepath.Join(root, "control.json")
			cfg.FMURL = remote.URL
			cfg.FMToken = "test"
			cfg.FMStackID = "test"
			cfg.ControlToken = ""
			cfg.SigilEndpoint = ""
			cfg.FaroCollector = ""
			cfg.ProfilesURL = ""
			cfg.GitPollInterval = 0
			cfg.PromRWURL = remote.URL
			cfg.OTLPEndpoint = remote.URL
			cfg.LokiURL = remote.URL
			cfg.HTTPAddr = childHTTPAddr
			col := fleetmgmt.Roster("selected:fleet_management", "", map[string]int{"linux": 1})[0]
			store, err := configuredFleetStore(cfg, nil, ha.AlwaysLeader{})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Set(context.Background(), col.ID, true); err != nil {
				t.Fatal(err)
			}
			cfgPath := filepath.Join(root, "config.json")
			writeHAJSON(t, cfgPath, cfg)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFleetHALifecycleHelper$")
			cmd.Env = append(os.Environ(), "FLEET_HA_HELPER_CONFIG="+cfgPath, "FLEET_HA_HELPER_LEASE="+leaseServer.URL, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
			if out, err := cmd.CombinedOutput(); err != nil {
				mu.Lock()
				removed := append([]string(nil), deleted...)
				count := registrations
				mu.Unlock()
				t.Fatalf("HA lifecycle exit: %v; registered=%d unregistered=%v\n%s", err, count, removed, out)
			}
			ids, err := store.Load(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if teardown {
				if !reflect.DeepEqual(deleted, []string{col.ID}) || registrations != 0 || len(ids) != 0 {
					t.Fatalf("teardown deleted=%v registered=%d state=%v", deleted, registrations, ids)
				}
			} else {
				if len(deleted) != 0 || registrations != 1 || !reflect.DeepEqual(ids, []string{col.ID}) {
					t.Fatalf("restart deleted=%v registered=%d state=%v", deleted, registrations, ids)
				}
			}
			select {
			case <-api.released:
			default:
				t.Fatal("planned handoff did not release lease")
			}
		})
	}
}

func TestFleetUnregisterCLIHelper(t *testing.T) {
	if os.Getenv("FLEET_CLI_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{os.Args[0]}, os.Args[i+1:]...)
			break
		}
	}
	flag.CommandLine = flag.NewFlagSet("synthkit", flag.ExitOnError)
	main()
}

func TestFleetUnregisterCLISelectedRosterExitZero(t *testing.T) {
	root := t.TempDir()
	baked := filepath.Join(root, "baked")
	if err := os.Mkdir(baked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baked, "selected.yaml"), []byte("name: selected\nhosts:\n  - {name: selected-host, os: linux}\nfeatures:\n  fleet_management:\n    enabled: true\n    collectors_per_os: {linux: 2, darwin: 1}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baked, "other.yaml"), []byte("name: other\nhosts:\n  - {name: other-host, os: windows}\nfeatures:\n  fleet_management:\n    enabled: true\n    collectors_per_os: {windows: 3}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var deleted []string
	var otherCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var body struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if strings.HasSuffix(r.URL.Path, "/UnregisterCollector") {
			deleted = append(deleted, body.ID)
		} else {
			otherCalls++
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	envFile := filepath.Join(root, "test.env")
	env := fmt.Sprintf("HA_MODE=off\nSTATE_BACKEND=file\nDRY_RUN=false\nBLUEPRINTS=%s\nBLUEPRINT_NAMES=selected\nBLUEPRINT_DATA_DIR=%s\nCONFIG_SNAPSHOT_PATH=%s\nGC_FM_URL=%s\nGC_FM_STACK_ID=test\nGC_FM_TOKEN=test\n", baked, filepath.Join(root, "data"), filepath.Join(root, "control.json"), server.URL)
	if err := os.WriteFile(envFile, []byte(env), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFleetUnregisterCLIHelper$", "--", "-env", envFile, "-fleet-unregister")
	cmd.Env = append(os.Environ(), "FLEET_CLI_HELPER=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CLI did not exit zero: %v\n%s", err, out)
	}
	mu.Lock()
	defer mu.Unlock()
	sort.Strings(deleted)
	// IDs are sourced from the current public deterministic roster contract, not guessed.
	var want []string
	for _, col := range fleetmgmt.Roster("selected:fleet_management", "", map[string]int{"linux": 2, "darwin": 1}) {
		want = append(want, col.ID)
	}
	sort.Strings(want)
	if !reflect.DeepEqual(deleted, want) || otherCalls != 0 {
		t.Fatalf("CLI deleted %v, want %v; non-teardown calls=%d", deleted, want, otherCalls)
	}
}

func TestFleetUnregisterCLIRejectsDryRun(t *testing.T) {
	root := t.TempDir()
	baked := filepath.Join(root, "baked")
	if err := os.Mkdir(baked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baked, "selected.yaml"), []byte("name: selected\nhosts: [{name: selected-host, os: linux}]\nfeatures:\n  fleet_management:\n    enabled: true\n    collectors_per_os: {linux: 1}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(root, "test.env")
	content := fmt.Sprintf("HA_MODE=off\nSTATE_BACKEND=file\nDRY_RUN=true\nBLUEPRINTS=%s\nBLUEPRINT_NAMES=selected\nBLUEPRINT_DATA_DIR=%s\nCONFIG_SNAPSHOT_PATH=%s\nGC_FM_URL=http://fm.example\n", baked, filepath.Join(root, "data"), filepath.Join(root, "control.json"))
	if err := os.WriteFile(envFile, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFleetUnregisterCLIHelper$", "--", "-env", envFile, "-fleet-unregister")
	cmd.Env = append(os.Environ(), "FLEET_CLI_HELPER=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("dry-run falsely credited successful teardown: %s", out)
	}
	if ctx.Err() != nil {
		t.Fatal("dry-run rejection timed out")
	}
	if strings.Contains(string(out), "UnregisterCollector planned") {
		t.Fatalf("teardown work preceded dry-run rejection: %s", out)
	}
}

func TestFleetStateAdapterUsesGatedCASAndReplaysConflict(t *testing.T) {
	gate := ha.NewGate()
	var mu sync.Mutex
	snap := control.Snapshot{Revision: "1"}
	conflicts := 1
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/fleet-registered" {
			t.Errorf("unexpected key %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(snap)
			return
		}
		var request control.Snapshot
		_ = json.NewDecoder(r.Body).Decode(&request)
		if conflicts > 0 {
			conflicts--
			w.WriteHeader(409)
			return
		}
		if request.Revision != snap.Revision {
			w.WriteHeader(409)
			return
		}
		writes++
		snap = control.Snapshot{Data: request.Data, Revision: control.Revision(fmt.Sprint(writes + 1))}
		_ = json.NewEncoder(w).Encode(snap)
	}))
	defer server.Close()
	backend := processStateBackend{url: server.URL, gate: gate, client: server.Client()}
	store := &fleetStateAdapter{backend: backend, attempts: 5, destination: fleetDestination("http://fm.example", "stack")}
	if err := store.Set(context.Background(), "collector-a", true); !errors.Is(err, ha.ErrNotLeader) {
		t.Fatalf("standby write=%v", err)
	}
	_ = gate.Activate(context.Background(), func(context.Context) error { return nil })
	if err := store.Set(context.Background(), "collector-a", true); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), "collector-b", true); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), "collector-a", false); err != nil {
		t.Fatal(err)
	}
	ids, err := store.Load(context.Background())
	if err != nil || !reflect.DeepEqual(ids, []string{"collector-b"}) {
		t.Fatalf("roundtrip=%v %v", ids, err)
	}
	gate.Revoke()
	if err := store.Set(context.Background(), "collector-c", true); !errors.Is(err, ha.ErrNotLeader) {
		t.Fatalf("revoked write=%v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if writes != 3 {
		t.Fatalf("writes=%d", writes)
	}
}

func TestFleetFileStoreRestartAndCorruptionFailClosed(t *testing.T) {
	cfg := &config.Config{StateBackend: "file", FMURL: "http://fm.example", SnapshotPath: filepath.Join(t.TempDir(), "control.json")}
	first, err := configuredFleetStore(cfg, nil, ha.AlwaysLeader{})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Set(context.Background(), "collector", true); err != nil {
		t.Fatal(err)
	}
	second, err := configuredFleetStore(cfg, nil, ha.AlwaysLeader{})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := second.Load(context.Background())
	if err != nil || !reflect.DeepEqual(ids, []string{"collector"}) {
		t.Fatalf("restart state=%v %v", ids, err)
	}
	if err := os.WriteFile(cfg.SnapshotPath+".fleet.json", []byte(`{"version":99,"ids":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Load(context.Background()); err == nil {
		t.Fatal("corrupt state accepted")
	}
	cfg.DryRun = true
	dry, err := configuredFleetStore(cfg, nil, ha.AlwaysLeader{})
	if err != nil || dry != nil {
		t.Fatal("dry-run acquired a persistent store")
	}
}

// A named-document fake observes the real adapter's CAS boundary, not controller state.
type fleetMemoryBackend struct {
	snapshot     control.Snapshot
	writes       int
	conflictData []byte
}

func (s *fleetMemoryBackend) Load(ctx context.Context, key control.Key) (control.Snapshot, error) {
	if key != control.FleetRegistered {
		return control.Snapshot{}, errors.New("unexpected state key")
	}
	return control.Snapshot{Data: append([]byte(nil), s.snapshot.Data...), Revision: s.snapshot.Revision}, ctx.Err()
}
func (s *fleetMemoryBackend) CompareAndSwap(ctx context.Context, key control.Key, revision control.Revision, data []byte) (control.Revision, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if key != control.FleetRegistered {
		return "", errors.New("unexpected state key")
	}
	if s.conflictData != nil {
		s.snapshot = control.Snapshot{Data: s.conflictData, Revision: "conflict"}
		s.conflictData = nil
		return "", control.ErrConflict
	}
	if revision != s.snapshot.Revision {
		return "", control.ErrConflict
	}
	s.writes++
	s.snapshot = control.Snapshot{Data: append([]byte(nil), data...), Revision: control.Revision(fmt.Sprint(s.writes))}
	return s.snapshot.Revision, nil
}
func fleetFakeToken(t *testing.T) string {
	t.Helper()
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b[:])
}

func TestFleetLedgerDestinationBoundary(t *testing.T) {
	for _, backendKind := range []string{"file", "kubernetes"} {
		for _, change := range []string{"url", "stack"} {
			t.Run(backendKind+"/"+change, func(t *testing.T) {
				ctx := context.Background()
				var calls atomic.Int32
				remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					_, _ = w.Write([]byte(`{}`))
				}))
				defer remote.Close()
				cfg := &config.Config{StateBackend: backendKind, StateFleetConfigMap: "fleet", SnapshotPath: filepath.Join(t.TempDir(), "control.json"), FMURL: remote.URL, FMStackID: "stack-a", FMToken: fleetFakeToken(t)}
				backend := &fleetMemoryBackend{}
				store, err := configuredFleetStore(cfg, backend, ha.AlwaysLeader{})
				if err != nil {
					t.Fatal(err)
				}
				controller := fleet.NewController(fleet.Config{FMURL: cfg.FMURL, StackID: cfg.FMStackID, Token: cfg.FMToken, Store: store})
				roster := []fleet.Collector{{ID: "owned-collector"}}
				if err := controller.Reconcile(ctx, roster); err != nil {
					t.Fatal(err)
				}
				readDocument := func() []byte {
					if backendKind == "kubernetes" {
						return append([]byte(nil), backend.snapshot.Data...)
					}
					data, err := os.ReadFile(cfg.SnapshotPath + ".fleet.json")
					if err != nil {
						t.Fatal(err)
					}
					return data
				}
				before := readDocument()
				if bytes.Contains(before, []byte(cfg.FMToken)) {
					t.Fatal("credential persisted in ledger")
				}
				writes := backend.writes
				calls.Store(0)
				changed := *cfg
				changed.FMToken = fleetFakeToken(t)
				if change == "url" {
					changed.FMURL += "/different-destination"
				} else {
					changed.FMStackID = "stack-b"
				}
				next, err := configuredFleetStore(&changed, backend, ha.AlwaysLeader{})
				if err != nil {
					t.Fatal(err)
				}
				c := fleet.NewController(fleet.Config{FMURL: changed.FMURL, StackID: changed.FMStackID, Token: changed.FMToken, Store: next})
				for _, operation := range []struct {
					name string
					run  func() error
				}{
					{"empty-roster", func() error { return c.Reconcile(ctx, nil) }},
					{"register", func() error { return c.Reconcile(ctx, []fleet.Collector{{ID: "new-collector"}}) }},
					{"teardown", func() error { return c.Unregister(ctx, roster) }},
					{"CAS-remove", func() error { return next.Set(ctx, roster[0].ID, false) }},
					{"CAS-add", func() error { return next.Set(ctx, "new-collector", true) }},
				} {
					if err := operation.run(); err == nil || !strings.Contains(err.Error(), "destination") {
						t.Errorf("%s failed to visibly reject destination mismatch: %v", operation.name, err)
					}
				}
				if calls.Load() != 0 || backend.writes != writes || !bytes.Equal(before, readDocument()) {
					t.Fatal("destination mismatch sent FM HTTP or consumed old ledger intent")
				}
				// Token rotation is not a destination change and must not require ledger migration.
				rotated := *cfg
				rotated.FMToken = changed.FMToken
				rotatedStore, err := configuredFleetStore(&rotated, backend, ha.AlwaysLeader{})
				if err != nil {
					t.Fatal(err)
				}
				if err := fleet.NewController(fleet.Config{FMURL: rotated.FMURL, StackID: rotated.FMStackID, Token: rotated.FMToken, Store: rotatedStore}).Unregister(ctx, roster); err != nil {
					t.Fatal(err)
				}
				if calls.Load() != 1 {
					t.Fatalf("token rotation did not allow actual teardown: calls=%d", calls.Load())
				}
				if ids, err := store.Load(ctx); err != nil || len(ids) != 0 {
					t.Fatalf("original destination ledger=%v %v", ids, err)
				}
			})
		}
	}
}

func TestFleetLedgerUnboundIntentRejected(t *testing.T) {
	ctx := context.Background()
	backend := &fleetMemoryBackend{snapshot: control.Snapshot{Data: []byte(`{"version":1,"ids":["legacy-collector"]}`)}}
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = w.Write([]byte(`{}`)) }))
	defer remote.Close()
	cfg := &config.Config{StateBackend: "kubernetes", StateFleetConfigMap: "fleet", FMURL: remote.URL, FMStackID: "stack", FMToken: fleetFakeToken(t)}
	store, err := configuredFleetStore(cfg, backend, ha.AlwaysLeader{})
	if err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), backend.snapshot.Data...)
	c := fleet.NewController(fleet.Config{FMURL: cfg.FMURL, StackID: cfg.FMStackID, Token: cfg.FMToken, Store: store})
	if err := c.Reconcile(ctx, nil); err == nil {
		t.Error("nonempty unbound ledger silently assigned to current destination")
	}
	if err := c.Unregister(ctx, []fleet.Collector{{ID: "legacy-collector"}}); err == nil {
		t.Error("unbound teardown accepted")
	}
	if err := store.Set(ctx, "legacy-collector", false); err == nil {
		t.Error("unbound intent consumed by CAS")
	}
	if calls.Load() != 0 || backend.writes != 0 || !bytes.Equal(before, backend.snapshot.Data) {
		t.Fatal("unbound cleanup intent lost")
	}
	backend.snapshot.Data = []byte(`{"version":1,"ids":[]}`)
	if err := store.Set(ctx, "new-collector", true); err != nil {
		t.Fatalf("empty legacy ledger cannot initialize: %v", err)
	}
}

func TestFleetUnregisterHAInterruptedBeforeLeadership(t *testing.T) {
	for _, acquired := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-leadership", true: "during-unregister"}[acquired], func(t *testing.T) {
			api, leaseServer := newNamedLeaseRecorder(t)
			defer leaseServer.Close()
			if acquired {
				api.allowAcquisition()
			}
			observed := make(chan struct{})
			var once sync.Once
			// Forward the real named Lease read, and signal only after the holder was served.
			leaseProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				req, err := http.NewRequestWithContext(r.Context(), r.Method, leaseServer.URL+r.URL.Path, r.Body)
				if err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				resp, err := leaseServer.Client().Do(req)
				if err != nil {
					// The interruption under test cancels the elector's in-flight Lease reads; a
					// read the client itself abandoned is expected, not a proxy failure.
					if r.Context().Err() != nil {
						return
					}
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				defer resp.Body.Close()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(resp.StatusCode)
				_, _ = io.Copy(w, resp.Body)
				if !acquired && r.Method == http.MethodGet {
					once.Do(func() { close(observed) })
				}
			}))
			defer leaseProxy.Close()
			root := t.TempDir()
			baked := filepath.Join(root, "baked")
			if err := os.Mkdir(baked, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(baked, "selected.yaml"), []byte("name: selected\nhosts: [{name: selected-host, os: linux}]\nfeatures:\n  fleet_management:\n    enabled: true\n    collectors_per_os: {linux: 1}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			releaseResponse := make(chan struct{})
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if acquired {
					if !strings.HasSuffix(r.URL.Path, "/UnregisterCollector") {
						t.Error("teardown sent non-unregister request")
					}
					once.Do(func() { close(observed) })
					<-releaseResponse
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer remote.Close()
			defer close(releaseResponse)
			cfg, err := config.Load(filepath.Join(root, "absent.env"))
			if err != nil {
				t.Fatal(err)
			}
			cfg.HAMode, cfg.StateBackend, cfg.HANamespace, cfg.HALeaseName, cfg.PodUID = "lease", "file", "test", "lease", "process"
			cfg.FleetUnregister, cfg.DryRun, cfg.SelfObsEnabled = true, false, false
			cfg.HARetryPeriod, cfg.HARenewDeadline, cfg.HAKubeRequestTimeout = 50*time.Millisecond, 300*time.Millisecond, 100*time.Millisecond
			cfg.HAReleaseTimeout, cfg.SendDrainDeadline = time.Second, 2*time.Second
			cfg.BlueprintsDir, cfg.BlueprintDataDir, cfg.BlueprintNames = baked, filepath.Join(root, "data"), []string{"selected"}
			cfg.SnapshotPath = filepath.Join(root, "control.json")
			cfg.FMURL, cfg.FMStackID, cfg.FMToken = remote.URL, "stack", fleetFakeToken(t)
			cfg.ControlToken, cfg.SigilEndpoint, cfg.FaroCollector, cfg.ProfilesURL, cfg.GitPollInterval = "", "", "", "", 0
			cfg.PromRWURL, cfg.OTLPEndpoint, cfg.LokiURL = remote.URL, remote.URL, remote.URL
			cfg.HTTPAddr = "127.0.0.1:0"
			store, err := configuredFleetStore(cfg, nil, ha.AlwaysLeader{})
			if err != nil {
				t.Fatal(err)
			}
			col := fleetmgmt.Roster("selected:fleet_management", "", map[string]int{"linux": 1})[0]
			if err := store.Set(context.Background(), col.ID, true); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(cfg.SnapshotPath + ".fleet.json")
			if err != nil {
				t.Fatal(err)
			}
			cfgPath := filepath.Join(root, "config.json")
			publicCfg := *cfg
			publicCfg.FMToken = ""
			writeHAJSON(t, cfgPath, &publicCfg)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFleetHALifecycleHelper$")
			cmd.Env = append(os.Environ(), "FLEET_HA_HELPER_CONFIG="+cfgPath, "FLEET_HA_HELPER_LEASE="+leaseProxy.URL, "FLEET_HA_HELPER_SIGNAL=1", "FLEET_HA_HELPER_TOKEN="+cfg.FMToken, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-observed:
			case <-ctx.Done():
				_ = cmd.Wait()
				t.Fatalf("never observed real standby Lease read: %s", out.String())
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				_ = cmd.Wait()
				t.Fatal(err)
			}
			err = cmd.Wait()
			if ctx.Err() != nil {
				t.Fatalf("interrupted teardown did not finish: %s", out.String())
			}
			if err == nil {
				t.Errorf("interrupted teardown exited zero without successful unregister: %s", out.String())
			}
			after, readErr := os.ReadFile(cfg.SnapshotPath + ".fleet.json")
			wantCalls := int32(0)
			if acquired {
				wantCalls = 1
			}
			if readErr != nil || !bytes.Equal(before, after) || calls.Load() != wantCalls {
				t.Fatalf("interruption changed ledger or unexpected FM requests: err=%v calls=%d", readErr, calls.Load())
			}
			api.mu.Lock()
			defer api.mu.Unlock()
			if !acquired && len(api.updates) != 0 {
				t.Fatalf("standby altered or released Lease: %v", api.updates)
			}
		})
	}
}

func TestFleetLedgerConflictRechecksDestination(t *testing.T) {
	ctx := context.Background()
	backend := &fleetMemoryBackend{}
	cfg := &config.Config{StateBackend: "kubernetes", StateFleetConfigMap: "fleet", FMURL: "http://fm.example", FMStackID: "stack-a"}
	first, err := configuredFleetStore(cfg, backend, ha.AlwaysLeader{})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Set(ctx, "collector-a", true); err != nil {
		t.Fatal(err)
	}
	original := backend.snapshot
	cfg.FMStackID = "stack-b"
	second, err := configuredFleetStore(cfg, backend, ha.AlwaysLeader{})
	if err != nil {
		t.Fatal(err)
	}
	backend.snapshot = control.Snapshot{}
	if err := second.Set(ctx, "collector-b", true); err != nil {
		t.Fatal(err)
	}
	foreign := append([]byte(nil), backend.snapshot.Data...)
	backend.snapshot = original
	backend.conflictData = foreign
	writes := backend.writes
	if err := first.Set(ctx, "collector-a", false); !errors.Is(err, fleet.ErrLedgerDestinationMismatch) {
		t.Fatalf("CAS replay ignored changed destination: %v", err)
	}
	if backend.writes != writes || !bytes.Equal(foreign, backend.snapshot.Data) {
		t.Fatal("CAS conflict consumed other destination intent")
	}
	// Bound empty ledgers are not silently repurposed either.
	if err := second.Set(ctx, "collector-b", false); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Load(ctx); !errors.Is(err, fleet.ErrLedgerDestinationMismatch) {
		t.Fatalf("empty bound ledger was reassigned: %v", err)
	}
}

func TestFleetLedgerDiagnosticIsVisibleAndClosed(t *testing.T) {
	for _, err := range []error{fleet.ErrLedgerDestinationMismatch, fleet.ErrLedgerUnbound} {
		if fleet.LifecycleErrorMessage(fmt.Errorf("wrapped: %w", err)) != err.Error() {
			t.Fatal("ledger diagnostic hidden")
		}
	}
	if strings.Contains(fleet.LifecycleErrorMessage(errors.New("upstream-private-diagnostic")), "upstream-private-diagnostic") {
		t.Fatal("upstream diagnostic leaked")
	}
}
