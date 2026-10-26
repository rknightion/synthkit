// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/construct/fleetmgmt"
	"github.com/rknightion/synthkit/internal/construct/sm"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ha"
	lease "github.com/rknightion/synthkit/internal/ha/kubernetes"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/smstate"
)

const startupKindsBlueprintYAML = `
name: startup-kinds
environments:
  - name: prod
    cloud: {provider: aws, account_id: "000000000001", region: us-east-1, vpc_id: vpc-test01}
features:
  fleet_management:
    enabled: true
    collectors_per_os: {linux: 2}
  synthetic_monitoring:
    enabled: true
    checks:
      - {name: api-health, target: "https://api.example/health", labels: {team: platform}}
`

func hasKind(resolved *blueprint.Resolved, kind string) bool {
	for _, instance := range resolved.Constructs {
		if instance.Kind == kind {
			return true
		}
	}
	return false
}

func TestApplyStartupDisabledKindsValidatesAgainstRegisteredKinds(t *testing.T) {
	bp := mustLoad(t, []byte(startupKindsBlueprintYAML))
	if err := applyStartupDisabledKinds(runner.Catalog(), []*blueprint.Resolved{bp}, nil); err != nil {
		t.Fatalf("empty list must be a no-op: %v", err)
	}
	if !hasKind(bp, sm.Kind) || !hasKind(bp, fleetmgmt.Kind) {
		t.Fatal("empty list changed the declared constructs")
	}
	err := applyStartupDisabledKinds(runner.Catalog(), []*blueprint.Resolved{bp}, []string{fleetmgmt.Kind, "ghost_kind"})
	if err == nil || !strings.Contains(err.Error(), "ghost_kind") || !strings.Contains(err.Error(), "STARTUP_DISABLED_KINDS") {
		t.Fatalf("unknown kind error = %v, want one naming STARTUP_DISABLED_KINDS and ghost_kind", err)
	}
	if !hasKind(bp, sm.Kind) {
		t.Fatal("a rejected list must not mutate the blueprint")
	}
}

// TestStartupDisabledSyntheticMonitoringPublishesNoProvisioningInput proves the SM half of AC2: with
// the kind listed at startup there is no construct and no snapshot for the provisioner to act on,
// so a fake SM endpoint sees zero requests.
func TestStartupDisabledSyntheticMonitoringPublishesNoProvisioningInput(t *testing.T) {
	var requests atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer fake.Close()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{SnapshotPath: filepath.Join(dir, "control-state.json"), SMURL: fake.URL, SMToken: "token"}
	bp := mustLoad(t, []byte(startupKindsBlueprintYAML))
	if err := applyStartupDisabledKinds(runner.Catalog(), []*blueprint.Resolved{bp}, []string{sm.Kind}); err != nil {
		t.Fatal(err)
	}
	handoff, err := fileSMHandoff([]*blueprint.Resolved{bp}, cfg, "v1", time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	if handoff.Declared || handoff.Registered || hasKind(bp, sm.Kind) {
		t.Fatalf("handoff = %+v constructs=%v, want no SM lane at all", handoff, bp.Constructs)
	}
	if !hasKind(bp, fleetmgmt.Kind) {
		t.Fatal("only the listed kind may be removed")
	}
	if _, err := smstate.ReadSnapshot(smstate.RuntimeDir(cfg.SnapshotPath)); err == nil {
		t.Fatal("a provisioning snapshot was published for a startup-disabled SM kind")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("fake SM endpoint received %d requests", got)
	}
}

func TestSeedStartupDisabledKindsUnionsAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-state.json")
	first := control.NewStore(path)
	first.Update(func(s *control.State) { s.DisabledKinds = []string{"persisted_kind"} })

	restarted := control.NewStore(path)
	if err := seedStartupDisabledKinds(context.Background(), restarted, []string{"startup_kind", "persisted_kind"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"persisted_kind", "startup_kind"}
	if got := restarted.Snapshot().DisabledKinds; !reflect.DeepEqual(got, want) {
		t.Fatalf("effective = %v, want %v", got, want)
	}
	if got := control.NewStore(path).Snapshot().DisabledKinds; !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted = %v, want %v", got, want)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wr
	done := make(chan string, 1)
	go func() { b, _ := io.ReadAll(rd); done <- string(b) }()
	func() {
		defer func() { os.Stdout = orig }()
		fn()
	}()
	wr.Close()
	return <-done
}

func startupKindsEnv(t *testing.T, startupKinds, smURL string) (snapshotPath string) {
	t.Helper()
	baked := t.TempDir()
	if err := os.WriteFile(filepath.Join(baked, "startup-kinds.yaml"), []byte(startupKindsBlueprintYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := filepath.EvalSymlinks(t.TempDir()) // the SM state directory rejects symlinked parents
	if err != nil {
		t.Fatal(err)
	}
	snapshotPath = filepath.Join(data, "control-state.json")
	for key, value := range map[string]string{
		"DRY_RUN":              "true",
		"BLUEPRINTS":           baked,
		"BLUEPRINT_DATA_DIR":   filepath.Join(data, "blueprints"),
		"BLUEPRINT_NAMES":      "startup-kinds",
		"CONFIG_SNAPSHOT_PATH": snapshotPath,
		"GC_SM_URL":            smURL,
		"GC_SM_TOKEN":          "startup-kinds-token",
	} {
		t.Setenv(key, value)
	}
	t.Setenv("STARTUP_DISABLED_KINDS", startupKinds)
	if startupKinds == "" {
		if err := os.Unsetenv("STARTUP_DISABLED_KINDS"); err != nil {
			t.Fatal(err)
		}
	}
	return snapshotPath
}

// TestStartupDisabledKindsFirstTickEmitsNone is AC1 through the real startup path: no POST
// /control/kinds is made, yet the first (and only, --once) tick pushes no series of the listed kinds.
func TestStartupDisabledKindsFirstTickEmitsNone(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), "absent.env")
	var smRequests atomic.Int32
	fakeSM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		smRequests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer fakeSM.Close()

	startupKindsEnv(t, "", fakeSM.URL)
	baseline := captureStdout(t, func() {
		if err := run(true, true, envPath); err != nil {
			t.Fatalf("baseline run: %v", err)
		}
	})
	if !strings.Contains(baseline, "alloy_") {
		t.Fatalf("baseline must emit alloy_* series so the test can observe their absence:\n%s", baseline)
	}

	snapshotPath := startupKindsEnv(t, "synthetic_monitoring, fleet_management", fakeSM.URL)
	if err := os.Remove(smstate.RuntimeDir(snapshotPath) + "/" + smstate.SnapshotFilename); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := run(true, true, envPath); err != nil {
			t.Fatalf("run with startup-disabled kinds: %v", err)
		}
	})
	if strings.Contains(out, "alloy_") {
		t.Fatalf("first tick emitted alloy_* series despite STARTUP_DISABLED_KINDS:\n%s", out)
	}
	if strings.Contains(out, "synthetic_monitoring_") || strings.Contains(out, "probe_success") {
		t.Fatalf("first tick emitted synthetic monitoring series:\n%s", out)
	}
	if _, err := smstate.ReadSnapshot(smstate.RuntimeDir(snapshotPath)); err == nil {
		t.Fatal("a provisioning snapshot was published for a startup-disabled SM kind")
	}
	if got := smRequests.Load(); got != 0 {
		t.Fatalf("fake SM endpoint received %d requests", got)
	}
	raw, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	var persisted control.State
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if want := []string{"synthetic_monitoring", "fleet_management"}; !reflect.DeepEqual(persisted.DisabledKinds, want) {
		t.Fatalf("persisted disabled_kinds = %v, want %v", persisted.DisabledKinds, want)
	}
}

func TestStartupDisabledKindsUnknownKindFailsStartupNamingIt(t *testing.T) {
	startupKindsEnv(t, "fleet_management,not_a_kind", "")
	err := run(true, false, filepath.Join(t.TempDir(), "absent.env"))
	if err == nil || !strings.Contains(err.Error(), "not_a_kind") {
		t.Fatalf("run() = %v, want a startup error naming not_a_kind", err)
	}
	if strings.Contains(err.Error(), "fleet_management") {
		t.Fatalf("error must name only the unknown kind: %v", err)
	}
}

func startupKindsHAConfig(t *testing.T, kinds ...string) *config.Config {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	baked := filepath.Join(root, "baked")
	if err := os.Mkdir(baked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baked, "fleet.yaml"), []byte("name: fleet\nenvironments:\n  - name: prod\nfeatures:\n  fleet_management:\n    enabled: true\n    collectors_per_os: {linux: 1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.HAMode, cfg.StateBackend = "lease", "file"
	cfg.BlueprintsDir, cfg.BlueprintDataDir = baked, filepath.Join(root, "data")
	cfg.BlueprintNames = []string{"fleet"}
	cfg.SnapshotPath = filepath.Join(root, "state", "state.json")
	cfg.DryRun, cfg.SelfObsEnabled = true, false
	cfg.FMURL, cfg.ControlToken, cfg.SigilEndpoint, cfg.FaroCollector, cfg.ProfilesURL = "", "", "", "", ""
	cfg.StartupDisabledKinds = kinds
	return cfg
}

// TestHALeaderActivationSeedsStartupDisabledKinds: the lease-mode leader seeds the persisted state
// and applies it to its runner before production, and a standby view rejects an unknown kind.
func TestHALeaderActivationSeedsStartupDisabledKinds(t *testing.T) {
	gate := ha.AlwaysLeader{}
	cfg := startupKindsHAConfig(t, "fleet_management")
	view, err := newHAView(context.Background(), cfg, gate, ha.Bounded{Gate: gate}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := view.store.Snapshot().DisabledKinds; len(got) != 0 {
		t.Fatalf("standby view wrote or held the startup kinds: %v", got)
	}
	if err := applyActivatedControl(context.Background(), cfg, view); err != nil {
		t.Fatal(err)
	}
	if got := view.store.Snapshot().DisabledKinds; !reflect.DeepEqual(got, []string{"fleet_management"}) {
		t.Fatalf("leader effective disabled_kinds = %v", got)
	}
	enabled := map[string]bool{}
	for _, c := range view.runner.ControlSchema().Constructs {
		enabled[c.Kind] = c.Enabled
	}
	if enabled["fleet_management"] {
		t.Fatalf("runner still runs fleet_management after activation: %v", enabled)
	}

	bad := startupKindsHAConfig(t, "fleet_management", "no_such_kind")
	if _, err := newHAView(context.Background(), bad, gate, ha.Bounded{Gate: gate}, false); err == nil || !strings.Contains(err.Error(), "no_such_kind") {
		t.Fatalf("newHAView error = %v, want one naming no_such_kind", err)
	}

	teardown := startupKindsHAConfig(t, "no_such_kind")
	teardown.FleetUnregister = true
	if _, err := newHAView(context.Background(), teardown, gate, ha.Bounded{Gate: gate}, false); err != nil {
		t.Fatalf("fleet teardown must not depend on startup kinds: %v", err)
	}
}

// TestStartupDisabledKindsSurviveReplacementPosts: the list GET /control/state reports (including a
// stripped Synthetic Monitoring lane) can be re-posted, a narrower replacement is accepted, and SM
// stays absent for the rest of the run: no series and no provisioning input.
func TestStartupDisabledKindsSurviveReplacementPosts(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{SnapshotPath: filepath.Join(dir, "control-state.json"), SMURL: "https://sm.example", SMToken: "token"}
	kinds := []string{sm.Kind, fleetmgmt.Kind}
	bp := mustLoad(t, []byte(startupKindsBlueprintYAML))
	if err := applyStartupDisabledKinds(runner.Catalog(), []*blueprint.Resolved{bp}, kinds); err != nil {
		t.Fatal(err)
	}
	if _, err := fileSMHandoff([]*blueprint.Resolved{bp}, cfg, "v1", time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	store := control.NewStore(cfg.SnapshotPath)
	if err := seedStartupDisabledKinds(context.Background(), store, kinds); err != nil {
		t.Fatal(err)
	}
	capture := &coretest.MetricCapture{}
	r := runner.New(runner.Sinks{Metrics: capture}, runner.Catalog(), runner.Options{})
	if err := r.AddBlueprint(bp); err != nil {
		t.Fatal(err)
	}
	r.ApplyControl(store.Snapshot())
	h := control.NewHandler(store, r.ApplyControl, "", r)
	post := func(body string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/kinds", strings.NewReader(body)))
		return rec.Code
	}
	if code := post(`{"disabled_kinds":["synthetic_monitoring","fleet_management"]}`); code != 200 {
		t.Fatalf("re-posting the startup list = %d, want 200", code)
	}
	if code := post(`{"disabled_kinds":["fleet_management"]}`); code != 200 {
		t.Fatalf("narrower replacement = %d, want 200", code)
	}
	if code := post(`{"disabled_kinds":["not_a_kind"]}`); code != 400 {
		t.Fatalf("unknown kind = %d, want 400", code)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/control/schema", nil))
	if !strings.Contains(rec.Body.String(), `"synthetic_monitoring"`) {
		t.Fatalf("schema kinds omit the startup-disabled kind: %s", rec.Body)
	}
	if err := r.RunOnce(context.Background(), time.Unix(1_700_000_000, 0)); err != nil {
		t.Fatal(err)
	}
	for _, name := range capture.Names() {
		if strings.HasPrefix(name, "alloy_") {
			t.Fatalf("fleet series %s after the replacement disabled only fleet_management", name)
		}
		if strings.HasPrefix(name, "synthetic_monitoring_") || name == "probe_success" {
			t.Fatalf("SM series %s resurrected by a replacement that omits the stripped kind", name)
		}
	}
	if _, err := smstate.ReadSnapshot(smstate.RuntimeDir(cfg.SnapshotPath)); err == nil {
		t.Fatal("a provisioning snapshot exists after a replacement that omits synthetic_monitoring")
	}
}

// TestStartupDisabledKindsValidateAgainstRegisteredKindsNotBootSet: the setting is valid for any
// registered construct kind even when the boot set declares none of them (setup mode, a partial
// set), is seeded into the control state, and a typo still fails naming only the typo.
func TestStartupDisabledKindsValidateAgainstRegisteredKindsNotBootSet(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), "absent.env")
	both := "synthetic_monitoring,fleet_management"

	t.Run("setup mode", func(t *testing.T) {
		snapshotPath := startupKindsEnv(t, both, "")
		t.Setenv("BLUEPRINT_NAMES", "")
		if err := run(true, false, envPath); err != nil {
			t.Fatalf("setup mode with registered kinds must start: %v", err)
		}
		if got := control.NewStore(snapshotPath).Snapshot().DisabledKinds; !reflect.DeepEqual(got, []string{"synthetic_monitoring", "fleet_management"}) {
			t.Fatalf("seeded disabled_kinds = %v", got)
		}
	})

	t.Run("boot set declaring neither kind", func(t *testing.T) {
		snapshotPath := startupKindsEnv(t, both, "")
		baked := t.TempDir()
		if err := os.WriteFile(filepath.Join(baked, "plain.yaml"), []byte("name: plain\nhosts:\n  - name: h1\n    os: linux\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("BLUEPRINTS", baked)
		t.Setenv("BLUEPRINT_NAMES", "plain")
		if err := run(true, false, envPath); err != nil {
			t.Fatalf("boot set without SM or Fleet must start: %v", err)
		}
		if got := control.NewStore(snapshotPath).Snapshot().DisabledKinds; !reflect.DeepEqual(got, []string{"synthetic_monitoring", "fleet_management"}) {
			t.Fatalf("seeded disabled_kinds = %v", got)
		}
	})

	t.Run("typo still fails", func(t *testing.T) {
		startupKindsEnv(t, "fleet_managment", "")
		t.Setenv("BLUEPRINT_NAMES", "")
		err := run(true, false, envPath)
		if err == nil || !strings.Contains(err.Error(), `unknown construct kind "fleet_managment"`) {
			t.Fatalf("run() = %v, want the validateKinds error naming the typo", err)
		}
	})
}

// TestHAStartupKindsLifecycleHelper is the subprocess half of TestHAActivationAppliesStartupDisabledKinds:
// it runs the real lease lifecycle and records what the leader's runner holds just before production.
func TestHAStartupKindsLifecycleHelper(t *testing.T) {
	path := os.Getenv("STARTUP_KINDS_HA_CONFIG")
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
		return lease.NewForClient(ctx, opts, os.Getenv("STARTUP_KINDS_HA_LEASE"), &http.Client{Timeout: opts.RequestTimeout})
	}
	deps.BeforeProduction = func(view *haView) {
		enabled := map[string]bool{}
		for _, c := range view.runner.ControlSchema().Constructs {
			enabled[c.Kind] = c.Enabled
		}
		proof, _ := json.Marshal(map[string]any{"runner_enabled": enabled, "store_disabled_kinds": view.store.Snapshot().DisabledKinds})
		if err := os.WriteFile(os.Getenv("STARTUP_KINDS_HA_PROOF"), proof, 0o600); err != nil {
			t.Error(err)
		}
	}
	if err := runHALifecycle(&cfg, true, make(chan struct{}), deps); err != nil {
		t.Fatal(err)
	}
}

// TestHAActivationAppliesStartupDisabledKinds drives the lease-mode activation end to end: the
// runner the leader starts production with has the startup kinds disabled, and they are persisted.
// It fails if the activation commit stops going through applyActivatedControl.
func TestHAActivationAppliesStartupDisabledKinds(t *testing.T) {
	api, leaseServer := newNamedLeaseRecorder(t)
	defer leaseServer.Close()
	api.allowAcquisition()
	cfg := startupKindsHAConfig(t, "fleet_management")
	root := filepath.Dir(cfg.BlueprintsDir)
	cfg.HANamespace, cfg.HALeaseName, cfg.PodUID = "test", "lease", "process"
	cfg.MasterTick = time.Hour
	cfg.HARetryPeriod = 50 * time.Millisecond
	cfg.HARenewDeadline = fleetHARenewDeadline
	cfg.HAKubeRequestTimeout = fleetHAKubeRequestTimeout
	cfg.HAReleaseTimeout = time.Second
	cfg.SendDrainDeadline = 2 * time.Second
	cfg.GitPollInterval = 0
	cfg.HTTPAddr = childHTTPAddr
	cfgPath := filepath.Join(root, "config.json")
	writeHAJSON(t, cfgPath, cfg)
	proof := filepath.Join(root, "proof.json")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHAStartupKindsLifecycleHelper$")
	cmd.Env = append(os.Environ(), "STARTUP_KINDS_HA_CONFIG="+cfgPath, "STARTUP_KINDS_HA_LEASE="+leaseServer.URL, "STARTUP_KINDS_HA_PROOF="+proof, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("HA lifecycle: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(proof)
	if err != nil {
		t.Fatalf("leader never reached production: %v", err)
	}
	var got struct {
		RunnerEnabled      map[string]bool `json:"runner_enabled"`
		StoreDisabledKinds []string        `json:"store_disabled_kinds"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if enabled, ok := got.RunnerEnabled["fleet_management"]; !ok || enabled {
		t.Fatalf("leader runner fleet_management enabled=%v present=%v, want disabled: %s", enabled, ok, raw)
	}
	if !reflect.DeepEqual(got.StoreDisabledKinds, []string{"fleet_management"}) {
		t.Fatalf("leader store disabled_kinds = %v", got.StoreDisabledKinds)
	}
}
