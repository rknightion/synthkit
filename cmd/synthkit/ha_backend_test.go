// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/construct/sm"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
	lease "github.com/rknightion/synthkit/internal/ha/kubernetes"
	"github.com/rknightion/synthkit/internal/optionallane"
	"github.com/rknightion/synthkit/internal/smstate"
	"github.com/rknightion/synthkit/internal/smstore"
	"github.com/rknightion/synthkit/internal/smstore/smstoretest"
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
	cfg.HTTPAddr = childHTTPAddr
	addrFile := filepath.Join(root, "http-addr")
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
	cmd.Env = append(os.Environ(), "SYNTHKIT_STATE_HELPER_CONFIG="+cfgPath, "SYNTHKIT_STATE_HELPER_API="+stateServer.URL, "SYNTHKIT_STATE_HELPER_LEASE="+leaseServer.URL, "SYNTHKIT_STATE_HELPER_PROOF="+proof, childHTTPAddrFileEnv+"="+addrFile, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	awaitChildControl(t, addrFile)
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
func TestHAKubernetesSMRegistrationReloadBeforeRealFirstTick(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := smstoretest.New(t, "control", "boot", "sm")
	defer a.Close()
	a.Permit()
	remote := smstoretest.NewRemote(t)
	defer remote.Close()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.StateBackend = "kubernetes"
	cfg.StateSMConfigMap = "sm"
	cfg.SMProvisionLeaseName = "provision"
	cfg.HANamespace = "test"
	cfg.StateControlConfigMap = "control"
	cfg.StateBootConfigMap = "boot"
	cfg.StateGitSourceConfigMaps = map[string]string{}
	cfg.SMURL = remote.Server.URL
	cfg.SMToken = smstoretest.Token(t)
	cfg.SnapshotPath = "/dev/null/control.json"
	cfg.StateCASMaxAttempts = 5
	backend, err := control.NewKubernetesBackendForClient(control.KubernetesBackendOptions{Gate: ha.AlwaysLeader{}, Namespace: "test", Objects: map[control.Key]string{control.SMState: "sm"}, RequestTimeout: 2 * time.Second, MaxDocumentBytes: control.MaxStateDocumentBytes}, a.Server.URL, a.Server.Client())
	if err != nil {
		t.Fatal(err)
	}
	store, err := configuredSMStore(cfg, backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareSMHandoff(context.Background(), []*blueprint.Resolved{smBlueprint(t)}, cfg, "release-a", time.Unix(100, 0), store, true); err != nil {
		t.Fatal(err)
	}
	provision := runFixtureProvision(t, cfg, a, "release-a")
	provision(true, true)
	registered := a.Data("sm")
	var d smstore.Document
	if err := json.Unmarshal(registered, &d); err != nil {
		t.Fatal(err)
	}
	d.Provision.Registration = nil
	unregistered, _ := json.Marshal(d)
	a.Set("sm", unregistered)
	a.ReplaceLease("emitter")
	cfg.HAMode = "lease"
	cfg.HALeaseName = "emitter"
	cfg.PodUID = "runtime"
	cfg.DryRun = true
	cfg.SelfObsEnabled = false
	cfg.FMURL = ""
	cfg.SigilEndpoint = ""
	cfg.FaroCollector = ""
	cfg.ProfilesURL = ""
	cfg.GitPollInterval = 0
	cfg.ControlToken = smstoretest.Token(t)
	cfg.BlueprintsDir = filepath.Join(root, "baked")
	cfg.BlueprintDataDir = filepath.Join(root, "absent-data")
	cfg.BlueprintNames = []string{"sm-test"}
	if err := os.Mkdir(cfg.BlueprintsDir, 0700); err != nil {
		t.Fatal(err)
	}
	fixture := []byte("name: sm-test\nhosts:\n  - {name: sample, os: linux}\nfeatures:\n  synthetic_monitoring:\n    enabled: true\n    checks:\n      - {name: api-health, target: 'https://api.example/health', labels: {team: platform}}\n")
	if err := os.WriteFile(filepath.Join(cfg.BlueprintsDir, "sm.yaml"), fixture, 0600); err != nil {
		t.Fatal(err)
	}
	cfg.HTTPAddr = childHTTPAddr
	addrFile := filepath.Join(root, "http-addr")
	path := filepath.Join(root, "config.json")
	writeHAJSON(t, path, cfg)
	proof := filepath.Join(root, "tick.json")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestHAKubernetesSMHelper$")
	cmd.Env = append(os.Environ(), "SYNTHKIT_STATE_HELPER_CONFIG="+path, "SYNTHKIT_STATE_HELPER_API="+a.Server.URL, "SYNTHKIT_STATE_HELPER_PROOF="+proof, childHTTPAddrFileEnv+"="+addrFile, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	a.Mu.Lock()
	initialWrites := a.StateWrites
	a.Mu.Unlock()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var standby optionallane.Disposition
	var boundAddr string
	awaitHA(t, func() bool {
		if boundAddr == "" {
			addr, ok := publishedHTTPAddr(addrFile)
			if !ok {
				return false
			}
			boundAddr = addr
		}
		req, _ := http.NewRequest("GET", "http://"+boundAddr+"/control/status", nil)
		req.SetBasicAuth("control", cfg.ControlToken)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer res.Body.Close()
		var body struct {
			Optional []optionallane.Disposition `json:"optional_lanes"`
		}
		if json.NewDecoder(res.Body).Decode(&body) != nil {
			return false
		}
		for _, d := range body.Optional {
			if d.Lane == optionallane.SyntheticMonitoring {
				standby = d
				return true
			}
		}
		return false
	})
	if standby.State == optionallane.Enabled {
		t.Fatal("standby advertised SM production")
	}
	a.Mu.Lock()
	writes := a.StateWrites
	a.Mu.Unlock()
	if writes != initialWrites {
		t.Fatal("standby SM/control/backend writes", writes-initialWrites)
	}
	// Parent changes only fixture state after the actual standby handler receipt.
	a.Set("sm", registered)
	a.Permit()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("acquisition process: %v %s", err, output.String())
		}
	case <-ctx.Done():
		t.Fatal("acquisition process did not join")
	}
	evidence, err := os.ReadFile(proof)
	if err != nil {
		t.Fatalf("no actual first tick proof: %v %s", err, output.String())
	}
	var observed struct {
		Registered    bool
		ConfigVersion string
		Metrics, Logs bool
		Optional      optionallane.Disposition
	}
	if err := json.Unmarshal(evidence, &observed); err != nil {
		t.Fatal(err)
	}
	if !observed.Registered || !observed.Metrics || !observed.Logs || observed.ConfigVersion != smstate.ConfigVersion(20) || observed.Optional.State != optionallane.Enabled || observed.Optional.Verification != optionallane.VerificationVerified {
		t.Fatalf("first tick did not activate authoritative registration: %s", evidence)
	}
	var final smstore.Document
	if err := json.Unmarshal(a.Data("sm"), &final); err != nil {
		t.Fatal(err)
	}
	if final.Snapshot.SourceVersion != version || final.Provision.Registration.SourceVersion != "release-a" {
		t.Fatal("release-independent acquisition provenance wrong")
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	if a.StateWrites <= writes {
		t.Fatal("acquisition did not publish")
	}
	t.Logf("authenticated standby partial; acquisition reread A registration; real first tick metrics/logs/config_version=%s; active optional=enabled/verified", observed.ConfigVersion)
}
func TestHAKubernetesSMRealRunnerEvidenceMatrix(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	remote := smstoretest.NewRemote(t)
	defer remote.Close()
	a := smstoretest.New(t, "sm", "control", "boot")
	defer a.Close()
	a.Permit()
	cfg, err := config.Load(filepath.Join(root, "absent.env"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.StateBackend, cfg.HAMode, cfg.HANamespace = "kubernetes", "lease", "test"
	cfg.StateSMConfigMap, cfg.SMProvisionLeaseName, cfg.StateControlConfigMap, cfg.StateBootConfigMap = "sm", "provision", "control", "boot"
	cfg.StateGitSourceConfigMaps = map[string]string{}
	cfg.StateCASMaxAttempts = 5
	cfg.SMURL, cfg.SMToken = remote.Server.URL, smstoretest.Token(t)
	cfg.SnapshotPath = "/dev/null/control.json"
	cfg.DryRun, cfg.SelfObsEnabled = true, false
	cfg.FMURL, cfg.ControlToken, cfg.SigilEndpoint, cfg.FaroCollector, cfg.ProfilesURL = "", "", "", "", ""
	cfg.BlueprintsDir, cfg.BlueprintDataDir = filepath.Join(root, "baked"), filepath.Join(root, "absent-data")
	cfg.BlueprintNames = []string{"sm-test"}
	if err := os.Mkdir(cfg.BlueprintsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.BlueprintsDir, "sm.yaml"), []byte(smRuntimeBlueprintYAML), 0600); err != nil {
		t.Fatal(err)
	}
	makeBackend := func(g ha.LeaderGate) control.StateBackend {
		t.Helper()
		backend, err := control.NewKubernetesBackendForClient(control.KubernetesBackendOptions{Gate: g, Namespace: "test", Objects: map[control.Key]string{control.SMState: "sm", control.Control: "control", control.BootManifest: "boot"}, RequestTimeout: 2 * time.Second, MaxDocumentBytes: control.MaxStateDocumentBytes}, a.Server.URL, a.Server.Client())
		if err != nil {
			t.Fatal(err)
		}
		return backend
	}
	store, err := configuredSMStore(cfg, makeBackend(ha.AlwaysLeader{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareSMHandoff(context.Background(), []*blueprint.Resolved{smBlueprint(t)}, cfg, version, time.Unix(100, 0), store, true); err != nil {
		t.Fatal(err)
	}
	runFixtureProvision(t, cfg, a, version)(true, true)
	original, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	persist := func(d smstore.Document, corruptName string) {
		data, _ := json.Marshal(d)
		a.Set("sm", corruptSMEvidenceJSON(data, corruptName))
	}
	drain := func(v *haView) {
		if v == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		v.runner.DrainQueues(ctx)
	}
	for _, tc := range smEvidenceNegatives() {
		t.Run(tc.name, func(t *testing.T) {
			persist(cloneSMDocument(t, original), "")
			gate := ha.NewGate()
			bound := ha.Bounded{Gate: gate, Timeout: 8 * time.Second, Margin: 2 * time.Second, Crash: func() { t.Error("unexpected HA bound crash") }}
			backend := makeBackend(gate)
			a.Mu.Lock()
			writes := a.StateWrites
			a.Mu.Unlock()
			standby, err := newHAView(context.Background(), cfg, gate, bound, false, backend)
			if err != nil {
				t.Fatal(err)
			}
			defer drain(standby)
			standby.prom.Capture, standby.loki.Capture = true, true
			if !standby.smState.Registered {
				t.Fatal("positive registered standby control absent")
			}
			if err := standby.runner.RunOnce(context.Background(), time.Unix(200, 0)); !errors.Is(err, ha.ErrNotLeader) {
				t.Fatalf("standby production admission=%v", err)
			}
			if len(standby.prom.Captured()) != 0 || len(standby.loki.Captured()) != 0 {
				t.Fatal("standby emitted")
			}
			a.Mu.Lock()
			after := a.StateWrites
			a.Mu.Unlock()
			if after != writes {
				t.Fatal("standby wrote named state")
			}
			bad := cloneSMDocument(t, original)
			tc.mutate(&bad)
			corruptName := ""
			if tc.corrupt {
				corruptName = tc.name
			}
			persist(bad, corruptName)
			// A fresh standby view observes the negative without writing. The old
			// registered view remains deliberately retained to prove acquisition rereads.
			negativeStandby, err := newHAView(context.Background(), cfg, gate, bound, false, backend)
			if tc.storageFailure {
				if err == nil {
					drain(negativeStandby)
					t.Fatal("invalid evidence reached standby runner construction")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer drain(negativeStandby)
				if negativeStandby.smState.Registered {
					t.Fatal("negative standby accepted registration")
				}
				if err := negativeStandby.runner.RunOnce(context.Background(), time.Unix(200, 0)); !errors.Is(err, ha.ErrNotLeader) {
					t.Fatalf("negative standby admission=%v", err)
				}
			}
			a.Mu.Lock()
			after = a.StateWrites
			a.Mu.Unlock()
			if after != writes {
				t.Fatal("negative standby wrote named state")
			}
			var acquired *haView
			err = gate.Activate(context.Background(), func(ctx context.Context) error {
				var err error
				acquired, err = newHAView(ctx, cfg, gate, bound, true, backend)
				return err
			})
			defer drain(acquired)
			if tc.storageFailure {
				if err == nil || acquired != nil {
					t.Fatal("invalid evidence reached acquired production runner")
				}
				t.Log("standby and acquisition rejected persisted invalid evidence before runner construction")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			acquired.prom.Capture, acquired.loki.Capture = true, true
			if err := acquired.runner.RunOnce(context.Background(), time.Unix(200, 0)); err != nil {
				t.Fatal(err)
			}
			if acquired.smState.Registered || len(acquired.prom.SeriesFor("sm_check_info")) != 0 || len(acquired.prom.SeriesFor("probe_success")) != 0 || len(acquired.prom.Captured()) == 0 {
				t.Fatal("acquisition used stale registered standby view or lost unrelated emission")
			}
			for _, stream := range acquired.loki.Captured() {
				if stream.Labels["source"] == "synthetic-monitoring-agent" {
					t.Fatal("acquired negative emitted SM logs")
				}
			}
			t.Log("standby zero writes/production; acquisition reread negative; real first tick zero SM with unrelated metrics")
		})
	}
}

func TestHAKubernetesSMHelper(t *testing.T) {
	path := os.Getenv("SYNTHKIT_STATE_HELPER_CONFIG")
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
	endpoint := os.Getenv("SYNTHKIT_STATE_HELPER_API")
	deps := productionHADependencies()
	recordBoundHTTPAddr(&deps)
	deps.StateBackend = func(g ha.LeaderGate) control.StateBackend {
		b, err := control.NewKubernetesBackendForClient(control.KubernetesBackendOptions{Gate: g, Namespace: "test", Objects: map[control.Key]string{control.Control: "control", control.BootManifest: "boot", control.SMState: "sm"}, RequestTimeout: 2 * time.Second, MaxDocumentBytes: control.MaxStateDocumentBytes}, endpoint, &http.Client{})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	deps.Election = func(ctx context.Context, o lease.Options) (leaseElection, error) {
		return lease.NewForClient(ctx, o, endpoint, &http.Client{})
	}
	deps.BeforeProduction = func(v *haView) {
		v.prom.Capture = true
		v.loki.Capture = true
		v.runner.SetTickObserver(func(ctx context.Context, bp, kind, name string, fn func(context.Context) error) error {
			if err := fn(ctx); err != nil {
				return err
			}
			if kind != sm.Kind {
				return nil
			}
			if err := v.runner.Flush(ctx); err != nil {
				return err
			}
			metrics := v.prom.SeriesFor("sm_check_info")
			if len(metrics) == 0 {
				t.Fatal("no actual SM metrics after real first tick/flush")
			}
			req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+boundHTTPAddr()+"/control/status", nil)
			req.SetBasicAuth("control", cfg.ControlToken)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer res.Body.Close()
			var body struct {
				Optional []optionallane.Disposition `json:"optional_lanes"`
			}
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				return err
			}
			var optional optionallane.Disposition
			for _, d := range body.Optional {
				if d.Lane == optionallane.SyntheticMonitoring {
					optional = d
				}
			}
			logs := false
			for _, stream := range v.loki.Captured() {
				if stream.Labels["source"] == "synthetic-monitoring-agent" {
					logs = true
				}
			}
			evidence, _ := json.Marshal(map[string]any{"Registered": v.smState.Registered, "ConfigVersion": metrics[0].Labels["config_version"], "Metrics": len(v.prom.SeriesFor("probe_success")) > 0, "Logs": logs, "Optional": optional})
			return os.WriteFile(os.Getenv("SYNTHKIT_STATE_HELPER_PROOF"), evidence, 0600)
		})
	}
	if err := runHALifecycle(&cfg, true, make(chan struct{}), deps); err != nil {
		t.Fatal(err)
	}
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
	recordBoundHTTPAddr(&deps)
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
