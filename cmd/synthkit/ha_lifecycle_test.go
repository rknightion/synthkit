// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/control"
	lease "github.com/rknightion/synthkit/internal/ha/kubernetes"
)

type leaseDocument struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name            string `json:"name"`
		Namespace       string `json:"namespace"`
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
	Spec struct {
		HolderIdentity       string `json:"holderIdentity"`
		LeaseDurationSeconds int32  `json:"leaseDurationSeconds"`
		AcquireTime          string `json:"acquireTime,omitempty"`
		RenewTime            string `json:"renewTime,omitempty"`
		LeaseTransitions     int32  `json:"leaseTransitions"`
	} `json:"spec"`
}
type namedLeaseRecorder struct {
	mu       sync.Mutex
	document leaseDocument
	updates  []string
	released chan struct{}
	once     sync.Once
}

func leaseMicroTimestamp(now time.Time) string {
	return now.UTC().Format("2006-01-02T15:04:05.000000Z07:00")
}
func TestHAFakeLeaseTimestampRetainsTrailingMicroseconds(t *testing.T) {
	now := time.Date(2026, 10, 8, 0, 0, 0, 763300000, time.UTC)
	wire := leaseMicroTimestamp(now)
	if wire != "2026-10-08T00:00:00.763300Z" {
		t.Fatal("fake Lease violated client-go MicroTime codec", wire)
	}
}

func newNamedLeaseRecorder(t *testing.T) (*namedLeaseRecorder, *httptest.Server) {
	t.Helper()
	a := &namedLeaseRecorder{released: make(chan struct{})}
	a.document.APIVersion = "coordination.k8s.io/v1"
	a.document.Kind = "Lease"
	a.document.Metadata.Name = "lease"
	a.document.Metadata.Namespace = "test"
	a.document.Metadata.ResourceVersion = "1"
	a.document.Spec.HolderIdentity = "other-process"
	a.document.Spec.LeaseDurationSeconds = 30
	a.document.Spec.RenewTime = leaseMicroTimestamp(time.Now())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/apis/coordination.k8s.io/v1/namespaces/test/leases/lease" {
			t.Errorf("state/unnamed API dependency: %s", r.URL.Path)
			w.WriteHeader(403)
			return
		}
		switch r.Method {
		case "GET":
			_ = json.NewEncoder(w).Encode(a.document)
		case "PUT":
			var d leaseDocument
			if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			if d.Metadata.ResourceVersion != a.document.Metadata.ResourceVersion {
				w.WriteHeader(409)
				_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Conflict","code":409}`))
				return
			}
			d.Metadata.ResourceVersion = fmt.Sprint(time.Now().UnixNano())
			a.document = d
			a.updates = append(a.updates, d.Spec.HolderIdentity)
			if d.Spec.HolderIdentity == "" {
				a.once.Do(func() { close(a.released) })
			}
			_ = json.NewEncoder(w).Encode(d)
		default:
			t.Errorf("forbidden Lease API method %s", r.Method)
			w.WriteHeader(403)
		}
	}))
	return a, srv
}
func (a *namedLeaseRecorder) allowAcquisition() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.document.Spec.HolderIdentity = ""
	a.document.Metadata.ResourceVersion = fmt.Sprint(time.Now().UnixNano())
}
func writeHAJSON(t *testing.T, path string, value any) {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}
func haGet(t *testing.T, url string) (int, []byte) {
	t.Helper()
	client := &http.Client{Timeout: 300 * time.Millisecond}
	resp, err := client.Get(url)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}
func awaitHA(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("HA local behavior was not observed within bound")
}

func TestHAGenericLeaseFileReloadAndPlannedRelease(t *testing.T) {
	exerciseHALeaseFileLifecycle(t, false)
}
func TestHAExpiredGlobalDrainDoesNotRelease(t *testing.T) { exerciseHALeaseFileLifecycle(t, true) }
func exerciseHALeaseFileLifecycle(t *testing.T, expire bool) {
	t.Helper()
	api, srv := newNamedLeaseRecorder(t)
	defer srv.Close()
	root := t.TempDir()
	baked := filepath.Join(root, "baked")
	data := filepath.Join(root, "data")
	source := filepath.Join(data, "git", "source")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(baked, 0755); err != nil {
		t.Fatal(err)
	}
	blob := filepath.Join(source, "test.yaml")
	if err := os.WriteFile(blob, []byte("name: test\nhosts:\n  - name: first\n    os: linux\n"), 0644); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "state.json")
	state := control.DefaultState()
	state.BlueprintSources = []control.SourceView{{ID: "source", Name: "source", Namespace: "staged", URL: "https://example.invalid/repository", Ref: "main", FetchedSHA: "before"}}
	writeHAJSON(t, statePath, state)
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.HAMode = "lease"
	cfg.StateBackend = "file"
	cfg.HANamespace = "test"
	cfg.HALeaseName = "lease"
	cfg.PodUID = "pod"
	cfg.DryRun = true
	cfg.SelfObsEnabled = false
	cfg.MasterTick = time.Hour
	cfg.HARetryPeriod = 50 * time.Millisecond
	cfg.HARenewDeadline = 300 * time.Millisecond
	cfg.HAKubeRequestTimeout = 100 * time.Millisecond
	cfg.HAReleaseTimeout = time.Second
	cfg.SendDrainDeadline = 2 * time.Second
	if expire {
		cfg.SendDrainDeadline = time.Nanosecond
	}
	cfg.BlueprintsDir = baked
	cfg.BlueprintDataDir = data
	cfg.BlueprintNames = []string{"staged/test"}
	cfg.SnapshotPath = statePath
	cfg.FMURL = ""
	cfg.ControlToken = ""
	cfg.SigilEndpoint = ""
	cfg.FaroCollector = ""
	cfg.ProfilesURL = ""
	cfg.GitPollInterval = 0
	listen, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg.HTTPAddr = listen.Addr().String()
	listen.Close()
	cfgPath := filepath.Join(root, "config.json")
	writeHAJSON(t, cfgPath, cfg)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestHAGenericLeaseFileHelper$")
	cmd.Env = append(os.Environ(), "SYNTHKIT_HA_CONFIG="+cfgPath, "SYNTHKIT_HA_API="+srv.URL, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
	logPath := filepath.Join(root, "process.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	t.Cleanup(func() {
		if t.Failed() {
			b, _ := os.ReadFile(logPath)
			t.Logf("bounded subprocess diagnostic:\n%s", b)
		}
	})
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	endpoint := "http://" + cfg.HTTPAddr
	awaitHA(t, func() bool { code, _ := haGet(t, endpoint+"/control/state"); return code == 200 })
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) == "" {
		t.Fatal("empty fixture")
	}
	if _, err := os.Stat(filepath.Join(data, ".boot-manifest.json")); !os.IsNotExist(err) {
		t.Fatal("standby manifest side effect", err)
	}
	fi, err := os.Stat(blob)
	if err != nil || fi.Mode().Perm() != 0644 {
		t.Fatal("standby chmodded fetched files", err)
	}
	client := &http.Client{Timeout: time.Second}
	req, _ := http.NewRequest("POST", endpoint+"/control/reset", strings.NewReader("{}"))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatal("standby HTTP mutation not rejected")
	}
	after, err := os.ReadFile(statePath)
	if err != nil || string(after) != string(before) {
		t.Fatal("standby persisted control state", err)
	}
	// Change both the authoritative local control snapshot and fetched blob while
	// standing by. No git host is contacted; the cached bytes are sufficient to boot.
	state.VolumeMultiplier = 7
	state.BlueprintSources[0].FetchedSHA = "after"
	writeHAJSON(t, statePath, state)
	if err := os.WriteFile(blob, []byte("name: test\nhosts:\n  - name: second\n    os: linux\n"), 0644); err != nil {
		t.Fatal(err)
	}
	api.allowAcquisition()
	awaitHA(t, func() bool {
		_, body := haGet(t, endpoint+"/control/state")
		var current control.State
		return json.Unmarshal(body, &current) == nil && current.VolumeMultiplier == 7 && len(current.BlueprintSources) == 1 && current.BlueprintSources[0].LoadedSHA == "after"
	})
	code, body := haGet(t, endpoint+"/control/inventory")
	if code != 200 || !strings.Contains(string(body), "second") {
		t.Fatalf("acquisition retained stale topology: %d %s", code, body)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-wait:
		if expire {
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 1 {
				t.Fatalf("expired global cap did not crash: %v", err)
			}
			select {
			case <-api.released:
				t.Fatal("expired global cap released lease")
			default:
			}
			t.Log("real lease+file process: SIGTERM cap expired; exit=1 with no release")
			return
		}
		if err != nil {
			output, _ := os.ReadFile(logPath)
			t.Fatalf("planned process exit: %v\n%s", err, output)
		}
	case <-ctx.Done():
		t.Fatal("planned shutdown did not exit")
	}
	select {
	case <-api.released:
	default:
		t.Fatal("no explicit Lease release after positive joins")
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	t.Logf("real lease+file process: standby no writes; acquired fresh control/source; joined production/delivery; explicit release; exit=0; lease_updates=%d", len(api.updates))
	if len(api.updates) == 0 || api.updates[len(api.updates)-1] != "" || api.document.Spec.LeaseDurationSeconds != 1 {
		t.Fatalf("release was overwritten by renewal: %v", api.updates)
	}
}

type diagnosticElection struct{ leaseElection }

func (e diagnosticElection) Seal(ctx context.Context) error {
	err := e.leaseElection.Seal(ctx)
	fmt.Fprintln(os.Stderr, "seal result:", err)
	return err
}
func (e diagnosticElection) Release(ctx context.Context) error {
	err := e.leaseElection.Release(ctx)
	fmt.Fprintln(os.Stderr, "release result:", err)
	return err
}

func TestHAGenericLeaseFileHelper(t *testing.T) {
	path := os.Getenv("SYNTHKIT_HA_CONFIG")
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	term := make(chan struct{})
	go func() { <-signals; close(term) }()
	deps := productionHADependencies()
	deps.Election = func(ctx context.Context, o lease.Options) (leaseElection, error) {
		e, err := lease.NewForClient(ctx, o, os.Getenv("SYNTHKIT_HA_API"), &http.Client{Timeout: o.RequestTimeout})
		return diagnosticElection{leaseElection: e}, err
	}
	if err := runHALifecycle(&cfg, false, term, deps); err != nil {
		t.Fatal(err)
	}
}
