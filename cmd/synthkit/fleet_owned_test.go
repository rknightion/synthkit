// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	"syscall"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/construct/fleetmgmt"
	"github.com/rknightion/synthkit/internal/fleet"
	"github.com/rknightion/synthkit/internal/ha"
	lease "github.com/rknightion/synthkit/internal/ha/kubernetes"
)

// ownedFM is a local Collector API fake following the vendor contract: UnregisterCollector only
// marks a collector inactive (it still reads present), DeleteCollector removes it, and
// GetCollector answers an absent ID with the Connect not_found error shape.
type ownedFM struct {
	mu        sync.Mutex
	registry  map[string]bool
	calls     []string
	remove    func(w http.ResponseWriter, id string) bool // DeleteCollector hook
	inactive  map[string]bool
	onRequest func(method string)
}

func newOwnedFM(t *testing.T, present ...string) (*ownedFM, *httptest.Server) {
	t.Helper()
	f := &ownedFM{registry: map[string]bool{}, inactive: map[string]bool{}}
	for _, id := range present {
		f.registry[id] = true
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		f.mu.Lock()
		f.calls = append(f.calls, method+" "+body.ID)
		hook := f.onRequest
		f.mu.Unlock()
		if hook != nil {
			hook(method)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch method {
		case "UnregisterCollector":
			if f.registry[body.ID] {
				f.inactive[body.ID] = true
			}
		case "DeleteCollector":
			if f.remove != nil && f.remove(w, body.ID) {
				return
			}
			if !f.registry[body.ID] {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":"not_found"}`))
				return
			}
			delete(f.registry, body.ID)
			delete(f.inactive, body.ID)
		case "GetCollector":
			if !f.registry[body.ID] {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":"not_found"}`))
				return
			}
			collector := map[string]any{"id": body.ID}
			if f.inactive[body.ID] {
				collector["markedInactiveAt"] = "2026-10-17T00:00:00Z"
			}
			_ = json.NewEncoder(w).Encode(collector)
			return
		case "RegisterCollector":
			f.registry[body.ID] = true
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *ownedFM) state() (map[string]bool, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reg := map[string]bool{}
	for k := range f.registry {
		reg[k] = true
	}
	return reg, append([]string(nil), f.calls...)
}

type ownedFixture struct {
	root, baked, envFile string
	cfg                  *config.Config
	selected             fleet.Collector
}

// newOwnedFixture writes a selected blueprint and an HA-off file-state env. The ledger is seeded
// through the production adapter for the same destination.
func newOwnedFixture(t *testing.T, fmURL, token string, ledger ...string) *ownedFixture {
	t.Helper()
	root := t.TempDir()
	baked := filepath.Join(root, "baked")
	if err := os.Mkdir(baked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baked, "selected.yaml"), []byte("name: selected\nhosts: [{name: selected-host, os: linux}]\nfeatures:\n  fleet_management:\n    enabled: true\n    collectors_per_os: {linux: 1}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(root, "control.json")
	envFile := filepath.Join(root, "test.env")
	env := fmt.Sprintf("HA_MODE=off\nSTATE_BACKEND=file\nDRY_RUN=false\nBLUEPRINTS=%s\nBLUEPRINT_NAMES=selected\nBLUEPRINT_DATA_DIR=%s\nCONFIG_SNAPSHOT_PATH=%s\nGC_FM_URL=%s\nGC_FM_STACK_ID=stack-a\nGC_FM_TOKEN=%s\n", baked, filepath.Join(root, "data"), snapshot, fmURL, token)
	if err := os.WriteFile(envFile, []byte(env), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{StateBackend: "file", SnapshotPath: snapshot, FMURL: fmURL, FMStackID: "stack-a", FMToken: token}
	store, err := configuredFleetStore(cfg, nil, ha.AlwaysLeader{})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ledger {
		if err := store.Set(context.Background(), id, true); err != nil {
			t.Fatal(err)
		}
	}
	return &ownedFixture{root: root, baked: baked, envFile: envFile, cfg: cfg, selected: fleetmgmt.Roster("selected:fleet_management", "", map[string]int{"linux": 1})[0]}
}

func (f *ownedFixture) ledgerPath() string { return f.cfg.SnapshotPath + ".fleet.json" }

func (f *ownedFixture) ledger(t *testing.T) []string {
	t.Helper()
	store, err := configuredFleetStore(f.cfg, nil, ha.AlwaysLeader{})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func runFleetCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestFleetUnregisterCLIHelper$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "FLEET_CLI_HELPER=1")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("CLI timed out: %s %s", out.String(), errOut.String())
	}
	return out.String(), errOut.String(), err
}

func parseOwnedReceipt(t *testing.T, stdout string) fleet.TeardownReceipt {
	t.Helper()
	var found []string
	for _, line := range strings.Split(stdout, "\n") {
		if rest, ok := strings.CutPrefix(line, "fleet teardown receipt: "); ok {
			found = append(found, rest)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one receipt line, got %d: %s", len(found), stdout)
	}
	var r fleet.TeardownReceipt
	dec := json.NewDecoder(strings.NewReader(found[0]))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		t.Fatal(err)
	}
	return r
}

// AC1/AC3: historical roster entries the current selection no longer names are inventoried from
// the ledger, retired by confirmed absence, and unrecorded remote collectors are never touched.
func TestFleetUnregisterOwnedCLIRetiresHistoricalLedger(t *testing.T) {
	token := fleetFakeToken(t)
	historical := []string{"selected-host-retired-scale-1", "previous-selection-collector"}
	fm, srv := newOwnedFM(t)
	fx := newOwnedFixture(t, srv.URL, token, historical...)
	fm.mu.Lock()
	for _, id := range append([]string{fx.selected.ID, "selected-host-unrecorded"}, historical...) {
		fm.registry[id] = true
	}
	fm.mu.Unlock()
	if err := (func() error {
		store, err := configuredFleetStore(fx.cfg, nil, ha.AlwaysLeader{})
		if err != nil {
			return err
		}
		return store.Set(context.Background(), fx.selected.ID, true)
	})(); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runFleetCLI(t, "-env", fx.envFile, "-fleet-unregister-owned")
	if err != nil {
		t.Fatalf("owned teardown exit: %v\n%s\n%s", err, stdout, stderr)
	}
	r := parseOwnedReceipt(t, stdout)
	wantIDs := append([]string{fx.selected.ID}, historical...)
	sort.Strings(wantIDs)
	var got []string
	for _, e := range r.Collectors {
		got = append(got, e.ID)
		if e.Outcome != fleet.OutcomeConfirmedAbsent || !e.Retired {
			t.Fatalf("entry %+v", e)
		}
	}
	if r.Status != fleet.TeardownComplete || r.Inventoried != 3 || r.Retired != 3 || r.Remaining != 0 || !r.InventoryVerified || !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("receipt %+v", r)
	}
	if ids := fx.ledger(t); len(ids) != 0 {
		t.Fatalf("ledger retained %v", ids)
	}
	reg, calls := fm.state()
	if !reflect.DeepEqual(reg, map[string]bool{"selected-host-unrecorded": true}) {
		t.Fatalf("remote registry %v: unrecorded collector touched or owned retained", reg)
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "RegisterCollector") || strings.HasPrefix(c, "GetConfig") || strings.HasPrefix(c, "UnregisterCollector") || strings.HasSuffix(c, "selected-host-unrecorded") {
			t.Fatalf("non-teardown or unrecorded request %q", c)
		}
	}
	for _, leak := range []string{token, "stack-a", srv.URL} {
		if strings.Contains(stdout, leak) || strings.Contains(stderr, leak) {
			t.Fatalf("receipt or log leaked %q", leak)
		}
	}
}

// AC2: refusals make no Fleet request, print no receipt and leave the ledger bytes unchanged.
func TestFleetUnregisterOwnedCLIRefusalsRetainIntent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, fx *ownedFixture) []string // returns CLI args
		want    string
	}{
		{"foreign-destination", func(t *testing.T, fx *ownedFixture) []string {
			other := *fx.cfg
			other.FMStackID = "stack-b"
			_ = os.Remove(fx.ledgerPath())
			store, err := configuredFleetStore(&other, nil, ha.AlwaysLeader{})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Set(context.Background(), "foreign-owned", true); err != nil {
				t.Fatal(err)
			}
			return []string{"-env", fx.envFile, "-fleet-unregister-owned"}
		}, "destination"},
		{"unbound-legacy", func(t *testing.T, fx *ownedFixture) []string {
			if err := os.WriteFile(fx.ledgerPath(), []byte(`{"version":1,"ids":["legacy-collector"]}`), 0600); err != nil {
				t.Fatal(err)
			}
			return []string{"-env", fx.envFile, "-fleet-unregister-owned"}
		}, "unbound"},
		{"kubernetes-state-without-lease", func(t *testing.T, fx *ownedFixture) []string {
			appendEnv(t, fx.envFile, "STATE_BACKEND=kubernetes\nHA_NAMESPACE=test\nSTATE_CONTROL_CONFIGMAP=control\nSTATE_BOOT_CONFIGMAP=boot\nSTATE_FLEET_CONFIGMAP=fleet\n")
			return []string{"-env", fx.envFile, "-fleet-unregister-owned"}
		}, "HA_MODE=lease"},
		{"dry-run", func(t *testing.T, fx *ownedFixture) []string {
			appendEnv(t, fx.envFile, "DRY_RUN=true\n")
			return []string{"-env", fx.envFile, "-fleet-unregister-owned"}
		}, "teardown failed"},
		{"combined-with-selected", func(t *testing.T, fx *ownedFixture) []string {
			return []string{"-env", fx.envFile, "-fleet-unregister-owned", "-fleet-unregister"}
		}, "cannot be combined"},
		{"missing-token", func(t *testing.T, fx *ownedFixture) []string {
			appendEnv(t, fx.envFile, "GC_FM_TOKEN=\n")
			return []string{"-env", fx.envFile, "-fleet-unregister-owned"}
		}, "teardown failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := fleetFakeToken(t)
			fm, srv := newOwnedFM(t, "owned-collector")
			fx := newOwnedFixture(t, srv.URL, token, "owned-collector")
			args := tc.prepare(t, fx)
			before, err := os.ReadFile(fx.ledgerPath())
			if err != nil {
				t.Fatal(err)
			}
			stdout, stderr, err := runFleetCLI(t, args...)
			if err == nil {
				t.Fatalf("refusal exited zero: %s %s", stdout, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("diagnostic %q missing: %s", tc.want, stderr)
			}
			if strings.Contains(stdout, "fleet teardown receipt") {
				t.Fatalf("refused run printed a receipt: %s", stdout)
			}
			after, err := os.ReadFile(fx.ledgerPath())
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("ledger changed: %v", err)
			}
			if _, calls := fm.state(); len(calls) != 0 {
				t.Fatalf("refusal sent Fleet requests %v", calls)
			}
			if strings.Contains(stdout+stderr, token) {
				t.Fatal("token leaked")
			}
		})
	}
}

func appendEnv(t *testing.T, path, extra string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(extra); err != nil {
		t.Fatal(err)
	}
}

// AC2/AC3: an ambiguous or rejected outcome exits non-zero with a secret-free receipt naming the
// remaining obligations; a later run with Fleet Management healthy completes from the ledger.
func TestFleetUnregisterOwnedCLIRemainingReceiptAndRecovery(t *testing.T) {
	token := fleetFakeToken(t)
	fm, srv := newOwnedFM(t, "denied", "lost", "already-gone-recorded")
	fm.mu.Lock()
	delete(fm.registry, "already-gone-recorded")
	fm.remove = func(w http.ResponseWriter, id string) bool {
		switch id {
		case "denied":
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"code":"permission_denied","message":"body-canary"}`))
			return true
		case "lost":
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return true
		}
		return false
	}
	fm.mu.Unlock()
	fx := newOwnedFixture(t, srv.URL, token, "denied", "lost", "already-gone-recorded")
	stdout, stderr, err := runFleetCLI(t, "-env", fx.envFile, "-fleet-unregister-owned")
	if err == nil {
		t.Fatalf("remaining obligations exited zero: %s", stdout)
	}
	if !strings.Contains(stderr, fleet.ErrTeardownIncomplete.Error()) {
		t.Fatalf("incomplete diagnostic missing: %s", stderr)
	}
	r := parseOwnedReceipt(t, stdout)
	byID := map[string]fleet.TeardownEntry{}
	for _, e := range r.Collectors {
		byID[e.ID] = e
	}
	if r.Status != fleet.TeardownRemaining || r.Remaining != 2 || r.Retired != 1 ||
		byID["denied"].Outcome != fleet.OutcomeRejected || byID["denied"].HTTPStatus != 403 ||
		byID["lost"].Outcome != fleet.OutcomeUnknown || byID["already-gone-recorded"].Reason != fleet.ReasonAbsentBeforeDelete {
		t.Fatalf("receipt %+v", r)
	}
	for _, leak := range []string{token, "stack-a", srv.URL, "body-canary"} {
		if strings.Contains(stdout+stderr, leak) {
			t.Fatalf("leaked %q", leak)
		}
	}
	if ids := fx.ledger(t); !reflect.DeepEqual(ids, []string{"denied", "lost"}) {
		t.Fatalf("ledger %v", ids)
	}
	fm.mu.Lock()
	fm.remove = nil
	fm.mu.Unlock()
	stdout, stderr, err = runFleetCLI(t, "-env", fx.envFile, "-fleet-unregister-owned")
	if err != nil {
		t.Fatalf("recovery: %v %s %s", err, stdout, stderr)
	}
	if r := parseOwnedReceipt(t, stdout); r.Status != fleet.TeardownComplete || r.Retired != 2 {
		t.Fatalf("recovery receipt %+v", r)
	}
	if ids := fx.ledger(t); len(ids) != 0 {
		t.Fatalf("ledger %v", ids)
	}
}

// The HA helper runs the production lifecycle with the owned teardown dependency, as the
// lease branch of runFleetUnregisterOwned composes it.
func TestFleetOwnedHALifecycleHelper(t *testing.T) {
	path := os.Getenv("FLEET_OWNED_HA_HELPER_CONFIG")
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
	cfg.FMToken = os.Getenv("FLEET_OWNED_HA_HELPER_TOKEN")
	cfg.FleetUnregister = true
	deps := productionHADependencies()
	deps.Preflight = func(context.Context, *config.Config) error { return nil }
	deps.Election = func(ctx context.Context, opts lease.Options) (leaseElection, error) {
		return lease.NewForClient(ctx, opts, os.Getenv("FLEET_OWNED_HA_HELPER_LEASE"), &http.Client{Timeout: opts.RequestTimeout})
	}
	deps.FleetTeardown = ownedFleetTeardown(os.Stdout)
	term := make(chan struct{})
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() { <-signals; close(term) }()
	if err := runHALifecycle(&cfg, true, term, deps); err != nil {
		t.Fatal(err)
	}
}

// ownedHASlowLeaseLatency delays every Lease response during the recovery run, standing in for a
// loaded host. It sits above the 400ms request timeout this test once used and far below the
// production-sized fleetHAKubeRequestTimeout.
const ownedHASlowLeaseLatency = 450 * time.Millisecond

// AC2/AC3: lease mode runs only as the elected leader. While another emitter holds the Lease the
// teardown makes no Fleet request and keeps the ledger; an interruption during a remote call keeps
// intent; once authority is acquired a rerun retires the historical identities and releases.
func TestFleetUnregisterOwnedHAAuthorityInterruptionAndRecovery(t *testing.T) {
	token := fleetFakeToken(t)
	historical := []string{"historical-a", "historical-b"}
	fm, srv := newOwnedFM(t, historical...)
	api, leaseServer := newNamedLeaseRecorder(t)
	defer leaseServer.Close()
	fx := newOwnedFixture(t, srv.URL, token, historical...)
	cfg, err := config.Load(filepath.Join(fx.root, "absent.env"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.HAMode, cfg.StateBackend, cfg.HANamespace, cfg.HALeaseName, cfg.PodUID = "lease", "file", "test", "lease", "process"
	cfg.DryRun, cfg.SelfObsEnabled = false, false
	cfg.MasterTick = time.Hour
	// Leadership lasts seconds here (paced teardown). client-go's default client-side limiter
	// (5 QPS, burst 10) would throttle 50ms renewals while holding the renewal write slot, starving
	// Seal; production renews every 2s. The retry period stays at 250ms to keep renewals under that
	// limit. The request timeout and renew deadline are production-sized, not figures only an idle
	// host meets: every lookup and renewal is one bounded Lease request, and a loaded runner that
	// answers one late would otherwise fail the startup lookup or cost the leader its Lease
	// mid-teardown. Step 3 injects ownedHASlowLeaseLatency to prove that tolerance.
	cfg.HARetryPeriod, cfg.HARenewDeadline, cfg.HAKubeRequestTimeout = 250*time.Millisecond, fleetHARenewDeadline, fleetHAKubeRequestTimeout
	cfg.HAReleaseTimeout, cfg.SendDrainDeadline, cfg.HAFlushTimeout = 5*time.Second, 10*time.Second, 8*time.Second
	cfg.BlueprintsDir, cfg.BlueprintDataDir, cfg.BlueprintNames = fx.baked, filepath.Join(fx.root, "data"), []string{"selected"}
	cfg.SnapshotPath = fx.cfg.SnapshotPath
	cfg.FMURL, cfg.FMStackID, cfg.FMToken = srv.URL, "stack-a", ""
	cfg.ControlToken, cfg.SigilEndpoint, cfg.FaroCollector, cfg.ProfilesURL, cfg.GitPollInterval = "", "", "", "", 0
	cfg.PromRWURL, cfg.OTLPEndpoint, cfg.LokiURL = srv.URL, srv.URL, srv.URL
	cfg.HTTPAddr = "127.0.0.1:0"
	cfgPath := filepath.Join(fx.root, "config.json")
	writeHAJSON(t, cfgPath, cfg)
	// Forward to the named Lease fake and signal once a held Lease has been served.
	leaseRead := make(chan struct{})
	var readOnce sync.Once
	leaseProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, err := http.NewRequestWithContext(r.Context(), r.Method, leaseServer.URL+r.URL.Path, r.Body)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		req.Header = r.Header.Clone()
		resp, err := leaseServer.Client().Do(req)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		if r.Method == http.MethodGet {
			readOnce.Do(func() { close(leaseRead) })
		}
	}))
	defer leaseProxy.Close()
	start := func(leaseURL string) (*exec.Cmd, *bytes.Buffer, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFleetOwnedHALifecycleHelper$")
		cmd.Env = append(os.Environ(), "FLEET_OWNED_HA_HELPER_CONFIG="+cfgPath, "FLEET_OWNED_HA_HELPER_LEASE="+leaseURL, "FLEET_OWNED_HA_HELPER_TOKEN="+token, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd, &out, cancel
	}
	before, err := os.ReadFile(fx.ledgerPath())
	if err != nil {
		t.Fatal(err)
	}

	// 1. Another emitter holds the Lease: standby makes no Fleet request, then SIGTERM refuses.
	cmd, out, cancel := start(leaseProxy.URL)
	select {
	case <-leaseRead:
	case <-time.After(15 * time.Second):
		t.Fatalf("never observed a standby Lease read: %s", out.String())
	}
	time.Sleep(750 * time.Millisecond) // several standby retry periods against the held Lease
	_ = cmd.Process.Signal(syscall.SIGTERM)
	err = cmd.Wait()
	cancel()
	if err == nil {
		t.Fatalf("standby teardown exited zero: %s", out.String())
	}
	if _, calls := fm.state(); len(calls) != 0 {
		t.Fatalf("standby sent Fleet requests %v", calls)
	}
	if after, _ := os.ReadFile(fx.ledgerPath()); !bytes.Equal(before, after) {
		t.Fatal("standby changed the ledger")
	}
	api.mu.Lock()
	updates := len(api.updates)
	api.mu.Unlock()
	if updates != 0 {
		t.Fatal("standby altered the held Lease")
	}

	// 2. Leader interrupted while the first DeleteCollector is in flight: intent is kept.
	api.allowAcquisition()
	inFlight := make(chan struct{})
	releaseInFlight := make(chan struct{})
	var once sync.Once
	fm.mu.Lock()
	fm.onRequest = func(method string) {
		if method == "DeleteCollector" {
			once.Do(func() { close(inFlight) })
			<-releaseInFlight
		}
	}
	fm.mu.Unlock()
	cmd, out, cancel = start(leaseServer.URL)
	select {
	case <-inFlight:
	case <-time.After(15 * time.Second):
		t.Fatalf("leader never reached deletion: %s", out.String())
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	time.Sleep(100 * time.Millisecond)
	close(releaseInFlight)
	err = cmd.Wait()
	cancel()
	if err == nil {
		t.Fatalf("interrupted teardown exited zero: %s", out.String())
	}
	if ids := fx.ledger(t); !reflect.DeepEqual(ids, historical) {
		t.Fatalf("interruption consumed intent: %v\n%s", ids, out.String())
	}
	if !strings.Contains(out.String(), `"status":"remaining"`) {
		t.Fatalf("interrupted leader printed no remaining receipt: %s", out.String())
	}

	// 3. Recovery as leader: every historical identity is confirmed absent and released.
	fm.mu.Lock()
	fm.onRequest = nil
	fm.mu.Unlock()
	api.allowAcquisition()
	api.setLatency(ownedHASlowLeaseLatency)
	cmd, out, cancel = start(leaseProxy.URL)
	err = cmd.Wait()
	cancel()
	if err != nil {
		t.Fatalf("recovery exit %v: %s", err, out.String())
	}
	r := parseOwnedReceipt(t, out.String())
	if r.Status != fleet.TeardownComplete || r.Retired != 2 {
		t.Fatalf("recovery receipt %+v", r)
	}
	if ids := fx.ledger(t); len(ids) != 0 {
		t.Fatalf("ledger %v", ids)
	}
	if reg, _ := fm.state(); len(reg) != 0 {
		t.Fatalf("remote %v", reg)
	}
	if strings.Contains(out.String(), token) {
		t.Fatal("token leaked")
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if n := len(api.updates); n == 0 || api.updates[n-1] != "" {
		t.Fatalf("leader did not release the Lease: %v", api.updates)
	}
}

// M2: a missing file ledger, or an empty unbound legacy document, reports no_ledger and exits
// nonzero; only an emptied version-2 ledger bound to the destination certifies complete.
func TestFleetUnregisterOwnedCLIAbsentLedgerVersusEmptiedLedger(t *testing.T) {
	for _, tc := range []struct {
		name     string
		prepare  func(t *testing.T, fx *ownedFixture)
		complete bool
	}{
		{"missing-file", func(t *testing.T, fx *ownedFixture) {
			if _, err := os.Stat(fx.ledgerPath()); !os.IsNotExist(err) {
				t.Fatalf("fixture ledger exists: %v", err)
			}
		}, false},
		{"empty-legacy-v1", func(t *testing.T, fx *ownedFixture) {
			if err := os.WriteFile(fx.ledgerPath(), []byte(`{"version":1,"ids":[]}`), 0600); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"emptied-v2", func(t *testing.T, fx *ownedFixture) {
			store, err := configuredFleetStore(fx.cfg, nil, ha.AlwaysLeader{})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Set(context.Background(), "retired-earlier", true); err != nil {
				t.Fatal(err)
			}
			if err := store.Set(context.Background(), "retired-earlier", false); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(fx.ledgerPath())
			if err != nil || !strings.Contains(string(data), `"version":2`) || !strings.Contains(string(data), `"destination":"`) {
				t.Fatalf("emptied ledger is not a bound v2 document: %s %v", data, err)
			}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fm, srv := newOwnedFM(t, "unrecorded")
			fx := newOwnedFixture(t, srv.URL, fleetFakeToken(t))
			tc.prepare(t, fx)
			stdout, stderr, err := runFleetCLI(t, "-env", fx.envFile, "-fleet-unregister-owned")
			r := parseOwnedReceipt(t, stdout)
			if tc.complete {
				if err != nil || r.Status != fleet.TeardownComplete || !r.InventoryVerified || r.Inventoried != 0 {
					t.Fatalf("emptied bound ledger: %v %+v %s", err, r, stderr)
				}
			} else {
				if err == nil || r.Status != fleet.TeardownNoLedger || r.InventoryVerified {
					t.Fatalf("absent ledger certified: %v %+v", err, r)
				}
				if !strings.Contains(stderr, fleet.ErrTeardownLedgerAbsent.Error()) {
					t.Fatalf("no_ledger diagnostic missing: %s", stderr)
				}
			}
			if _, calls := fm.state(); len(calls) != 0 {
				t.Fatalf("Fleet requests %v", calls)
			}
		})
	}
}
