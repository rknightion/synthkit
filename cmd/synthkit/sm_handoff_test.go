// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/construct/sm"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fleetstatus"
	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/optionallane"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/smstate"
	"github.com/rknightion/synthkit/internal/smstore"
	"github.com/rknightion/synthkit/internal/smstore/smstoretest"
)

func fileSMHandoff(resolved []*blueprint.Resolved, cfg *config.Config, release string, now time.Time) (smHandoff, error) {
	return prepareSMHandoff(context.Background(), resolved, cfg, release, now, smstore.NewFileStore(smstate.RuntimeDir(cfg.SnapshotPath)), true)
}

const smRuntimeBlueprintYAML = `
name: sm-test
environments:
  - name: prod
    cloud: {provider: aws, account_id: "000000000001", region: us-east-1, vpc_id: vpc-test01}
features:
  synthetic_monitoring:
    enabled: true
    checks:
      - {name: api-health, target: "https://api.example/health", labels: {team: platform}}
`

func smBlueprint(t *testing.T) *blueprint.Resolved {
	t.Helper()
	return mustLoad(t, []byte(smRuntimeBlueprintYAML))
}

func TestSMHandoffSuppressesUntilMatchingRegistrationThenInjects(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{SnapshotPath: filepath.Join(dir, "control-state.json"), SMURL: "https://sm.example"}
	first := smBlueprint(t)
	handoff, err := fileSMHandoff([]*blueprint.Resolved{first}, cfg, "v1.2.3", time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !handoff.Declared || handoff.Registered || hasSMConstruct(first) {
		t.Fatalf("unregistered handoff = %+v constructs=%v", handoff, first.Constructs)
	}
	runtimeDir := smstate.RuntimeDir(cfg.SnapshotPath)
	snapshot, err := smstate.ReadSnapshot(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	registration := smstate.Registration{
		SchemaVersion: smstate.ActivationSchemaVersion, SnapshotHash: snapshot.SnapshotHash,
		TargetFingerprint: snapshot.TargetFingerprint, SourceVersion: snapshot.SourceVersion,
		Resources: []smstate.Resource{
			{Kind: "probe", Key: snapshot.Probes[0].Key, ID: 11, Modified: 10, Ownership: smstate.Owned},
			{Kind: "check", Key: snapshot.Checks[0].Key, ID: 12, Modified: 1725000000.123456, ConfigVersion: smstate.ConfigVersion(1725000000.123456), Ownership: smstate.Owned},
		},
	}
	wantConfigVersion := smstate.ConfigVersion(1725000000.123456)
	if err := smstate.WriteRegistration(runtimeDir, registration); err != nil {
		t.Fatal(err)
	}
	second := smBlueprint(t)
	handoff, err = fileSMHandoff([]*blueprint.Resolved{second}, cfg, "v1.2.3", time.Unix(200, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !handoff.Registered || !hasSMConstruct(second) {
		t.Fatalf("registered handoff = %+v", handoff)
	}
	for _, instance := range second.Constructs {
		if instance.Kind == sm.Kind {
			got := instance.Config.(*sm.Config).Registration[sm.CheckKey("api-health", "https://api.example/health")]
			if got != wantConfigVersion {
				t.Fatalf("injected config version = %q", got)
			}
		}
	}
	info, err := os.Lstat(filepath.Join(runtimeDir, smstate.SnapshotFilename))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode = %v err=%v", info.Mode(), err)
	}
}

// runFixtureProvision invokes the real file CLI or the injected Kubernetes
// process edge with the real named typed backend and Lease adapter.
func runFixtureProvision(t *testing.T, cfg *config.Config, a *smstoretest.API, release string) func(bool, bool) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "provision")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	args := []string{"build", "-ldflags=-X main.version=" + release, "-o", executable, "../sm-provision"}
	if a != nil {
		args = []string{"test", "-c", "-ldflags=-X github.com/rknightion/synthkit/cmd/sm-provision.version=" + release, "-o", executable, "../sm-provision"}
	}
	if out, err := exec.CommandContext(ctx, "go", args...).CombinedOutput(); err != nil {
		t.Fatalf("build local CLI: %v %s", err, out)
	}
	return func(apply, wantOK bool) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, executable)
		cmd.Env = append(os.Environ(), "GC_SM_URL="+cfg.SMURL, "GC_SM_TOKEN="+cfg.SMToken, "CONFIG_SNAPSHOT_PATH="+cfg.SnapshotPath, "STATE_BACKEND=file", "SM_PROVISION_APPLY=false", "SM_PROVISION_ADOPT_LEGACY=false", "SM_PROVISION_MIGRATE_TARGET=false")
		if apply {
			cmd.Env = append(cmd.Env, "SM_PROVISION_APPLY=true")
		}
		if a != nil {
			pc := config.SMProvisionConfig{SMURL: cfg.SMURL, SMToken: cfg.SMToken, Apply: apply, StateBackend: "kubernetes", SnapshotPath: "/dev/null/control.json", Namespace: "test", StateSMConfigMap: "sm", ProvisionLeaseName: "provision", EmitterLeaseName: "emitter", PodUID: "fixture-tool", LeaseDuration: 30 * time.Second, RenewDeadline: 15 * time.Second, RetryPeriod: 2 * time.Second, KubeRequestTimeout: 2 * time.Second, HTTPTimeout: 5 * time.Second, OperationTimeout: 8 * time.Second, FenceMargin: 2 * time.Second, ReleaseTimeout: 2 * time.Second, CASAttempts: 5}
			data, _ := json.Marshal(pc)
			path := filepath.Join(dir, "config.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			cmd.Args = append(cmd.Args, "-test.run=^TestKubernetesProvisionProcessHelper$")
			cmd.Env = append(cmd.Env, "SYNTHKIT_SM_PROOF_CONFIG="+path, "SYNTHKIT_SM_PROOF_API="+a.Server.URL, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
		}
		out, err := cmd.CombinedOutput()
		if (err == nil) != wantOK {
			t.Fatalf("CLI outcome=%v expected=%t: %s", err, wantOK, out)
		}
	}
}
func composedSMTick(t *testing.T, bp *blueprint.Resolved) (*coretest.MetricCapture, *coretest.LogCapture) {
	t.Helper()
	m, l := &coretest.MetricCapture{}, &coretest.LogCapture{}
	r := runner.New(runner.Sinks{Metrics: m, Logs: l}, runner.Catalog(), runner.Options{})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		r.DrainQueues(ctx)
	}()
	if err := r.AddBlueprint(bp); err != nil {
		t.Fatal(err)
	}
	if err := r.RunOnce(context.Background(), time.Unix(200, 0)); err != nil {
		t.Fatal(err)
	}
	return m, l
}

// Both modes provision at A through fake SM HTTP, then reboot the real composed
// runner at B, with no provisioner invocation between those two boots.
func TestSMHandoffReleaseBumpRetainsAuthoritativeEmission(t *testing.T) {
	for _, mode := range []string{"file", "kubernetes"} {
		t.Run(mode, func(t *testing.T) {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			remote := smstoretest.NewRemote(t)
			defer remote.Close()
			cfg := &config.Config{StateBackend: mode, SnapshotPath: filepath.Join(dir, "control.json"), SMURL: remote.Server.URL, SMToken: smstoretest.Token(t), StateCASMaxAttempts: 5}
			var a *smstoretest.API
			var store smstore.Store
			if mode == "kubernetes" {
				a = smstoretest.New(t, "sm")
				defer a.Close()
				a.Permit()
				cfg.StateSMConfigMap = "sm"
				cfg.SMProvisionLeaseName = "provision"
				cfg.HANamespace = "test"
				backend, err := control.NewKubernetesBackendForClient(control.KubernetesBackendOptions{Gate: ha.AlwaysLeader{}, Namespace: "test", Objects: map[control.Key]string{control.SMState: "sm"}, RequestTimeout: 2 * time.Second, MaxDocumentBytes: control.MaxStateDocumentBytes}, a.Server.URL, a.Server.Client())
				if err != nil {
					t.Fatal(err)
				}
				store, err = configuredSMStore(cfg, backend)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				store = smstore.NewFileStore(smstate.RuntimeDir(cfg.SnapshotPath))
			}
			if _, err := prepareSMHandoff(context.Background(), []*blueprint.Resolved{smBlueprint(t)}, cfg, "release-a", time.Unix(100, 0), store, true); err != nil {
				t.Fatal(err)
			}
			provision := runFixtureProvision(t, cfg, a, "release-a")
			provision(true, true)
			original, err := store.Load(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var inventory []string
			for _, release := range []string{"release-a", "release-b"} {
				bp := smBlueprint(t)
				h, err := prepareSMHandoff(context.Background(), []*blueprint.Resolved{bp}, cfg, release, time.Unix(200, 0), store, true)
				if err != nil {
					t.Fatal(err)
				}
				m, l := composedSMTick(t, bp)
				if !h.Registered || len(m.Find("sm_check_info")) == 0 || len(m.Find("probe_success")) == 0 || len(l.Streams) == 0 {
					t.Fatalf("release %s did not emit SM handoff=%+v", release, h)
				}
				for _, series := range m.Find("sm_check_info") {
					if series.Labels["config_version"] != smstate.ConfigVersion(20) || series.Labels["blueprint"] != "" {
						t.Fatalf("wrong authoritative labels: %v", series.Labels)
					}
				}
				if inventory == nil {
					inventory = m.LabelKeys("sm_check_info")
				} else if !reflect.DeepEqual(inventory, m.LabelKeys("sm_check_info")) {
					t.Fatal("release changed label inventory")
				}
				facts := collectOptionalRuntimeFacts([]*blueprint.Resolved{bp}, nil, cfg, h)
				for _, d := range optionalRuntimeDispositions(cfg, facts, nil, fleetstatus.FleetStat{}, 0, false, false) {
					if d.Lane == optionallane.SyntheticMonitoring && (d.State != optionallane.Enabled || d.Verification != optionallane.VerificationVerified) {
						t.Fatalf("active optional SM disposition=%+v", d)
					}
				}
			}
			next, err := store.Load(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if next.Snapshot.SourceVersion != "release-b" || next.Snapshot.SnapshotHash != original.Snapshot.SnapshotHash || next.Provision.Registration.SourceVersion != "release-a" {
				t.Fatal("activation provenance/hash not preserved")
			}
			remote.Mu.Lock()
			posts := remote.Posts
			remote.Mu.Unlock()
			provision(true, false)
			remote.Mu.Lock()
			defer remote.Mu.Unlock()
			if remote.Posts != posts {
				t.Fatal("old version tool mutated SM after release B")
			}
			t.Logf("%s release B emitted authoritative config_version=%s with registration from A; old A CLI rejected B snapshot", mode, smstate.ConfigVersion(20))
		})
	}
}

func TestSMHandoffChangedSameKeySpecsSuppressUntilReprovision(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	remote := smstoretest.NewRemote(t)
	defer remote.Close()
	cfg := &config.Config{StateBackend: "file", SnapshotPath: filepath.Join(dir, "control.json"), SMURL: remote.Server.URL, SMToken: smstoretest.Token(t)}
	store := smstore.NewFileStore(smstate.RuntimeDir(cfg.SnapshotPath))
	prepare := func(bp *blueprint.Resolved) (smHandoff, error) {
		return prepareSMHandoff(context.Background(), []*blueprint.Resolved{bp}, cfg, "release-a", time.Unix(200, 0), store, true)
	}
	if _, err := prepare(smBlueprint(t)); err != nil {
		t.Fatal(err)
	}
	provision := runFixtureProvision(t, cfg, nil, "release-a")
	provision(true, true)
	bp := smBlueprint(t)
	h, err := prepare(bp)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := composedSMTick(t, bp)
	if !h.Registered || len(m.Find("sm_check_info")) == 0 {
		t.Fatal("positive emission control failed")
	}
	original, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*blueprint.Resolved){
		"frequency": func(b *blueprint.Resolved) {
			for _, c := range b.Constructs {
				if c.Kind == sm.Kind {
					c.Config.(*sm.Config).Checks[0].FrequencyMs = 90000
				}
			}
		},
		"labels": func(b *blueprint.Resolved) {
			for _, c := range b.Constructs {
				if c.Kind == sm.Kind {
					c.Config.(*sm.Config).Checks[0].Labels["team"] = "other"
				}
			}
		},
		"probe": func(b *blueprint.Resolved) {
			for _, c := range b.Constructs {
				if c.Kind == sm.Kind {
					c.Config.(*sm.Config).Checks[0].Probe = "other"
				}
			}
		},
		"region": func(b *blueprint.Resolved) {
			for _, c := range b.Constructs {
				if c.Kind == sm.Kind {
					c.Config.(*sm.Config).Checks[0].Region = "AMER"
				}
			}
		},
		"source": func(b *blueprint.Resolved) { b.Name = "other-source" },
		"addition": func(b *blueprint.Resolved) {
			for _, c := range b.Constructs {
				if c.Kind == sm.Kind {
					x := c.Config.(*sm.Config)
					next := x.Checks[0]
					next.Name = "other-health"
					x.Checks = append(x.Checks, next)
				}
			}
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			bp := smBlueprint(t)
			mutate(bp)
			h, err := prepare(bp)
			if err != nil {
				t.Fatal(err)
			}
			m, l := composedSMTick(t, bp)
			if h.Registered || len(m.Find("sm_check_info")) != 0 || len(m.Find("probe_success")) != 0 {
				t.Fatal("changed same-key spec emitted")
			}
			if len(m.All()) == 0 {
				t.Fatal("unrelated positive emission control missing")
			}
			for _, stream := range l.Streams {
				if stream.Labels["source"] == "synthetic-monitoring-agent" {
					t.Fatal("changed spec emitted SM log")
				}
			}
		})
	}
	// Root-fixed timeout/coordinates also remain part of the binding: a prior
	// registration for any different value cannot activate this boot's specs.
	for name, mutate := range map[string]func(*smstate.Snapshot){"timeout": func(s *smstate.Snapshot) { s.Checks[0].TimeoutMS++ }, "latitude": func(s *smstate.Snapshot) { s.Probes[0].Latitude++ }, "longitude": func(s *smstate.Snapshot) { s.Probes[0].Longitude++ }} {
		t.Run(name, func(t *testing.T) {
			data, _ := json.Marshal(original.Snapshot)
			var prior smstate.Snapshot
			json.Unmarshal(data, &prior)
			mutate(&prior)
			prior, err = smstate.NewSnapshot(prior.Probes, prior.Checks, cfg.SMURL, cfg.SMToken, "release-a", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			reg := *original.Provision.Registration
			reg.SnapshotHash = prior.SnapshotHash
			if err := smstate.WriteRegistration(smstate.RuntimeDir(cfg.SnapshotPath), reg); err != nil {
				t.Fatal(err)
			}
			bp := smBlueprint(t)
			h, err := prepare(bp)
			if err != nil {
				t.Fatal(err)
			}
			m, _ := composedSMTick(t, bp)
			if h.Registered || len(m.Find("sm_check_info")) != 0 || len(m.All()) == 0 {
				t.Fatal("fixed field omitted from binding")
			}
		})
	}
	if err := smstate.WriteRegistration(smstate.RuntimeDir(cfg.SnapshotPath), *original.Provision.Registration); err != nil {
		t.Fatal(err)
	}
	changed := smBlueprint(t)
	mutations["frequency"](changed)
	h, err = prepare(changed)
	if err != nil || h.Registered {
		t.Fatal("changed boot activated", err)
	}
	// Keep this already-built suppressed runner across apply: no hot activation.
	capture := &coretest.MetricCapture{}
	oldRunner := runner.New(runner.Sinks{Metrics: capture}, runner.Catalog(), runner.Options{})
	if err := oldRunner.AddBlueprint(changed); err != nil {
		t.Fatal(err)
	}
	provision(false, true)
	fresh := smBlueprint(t)
	mutations["frequency"](fresh)
	h, err = prepare(fresh)
	if err != nil || h.Registered {
		t.Fatal("preview activated", err)
	}
	provision(true, true)
	if err := oldRunner.RunOnce(context.Background(), time.Unix(300, 0)); err != nil {
		t.Fatal(err)
	}
	if len(capture.Find("sm_check_info")) != 0 {
		t.Fatal("apply hot-activated prior process")
	}
	restarted := smBlueprint(t)
	mutations["frequency"](restarted)
	h, err = prepare(restarted)
	if err != nil {
		t.Fatal(err)
	}
	m, _ = composedSMTick(t, restarted)
	if !h.Registered || len(m.Find("sm_check_info")) == 0 || m.Find("sm_check_info")[0].Labels["config_version"] != smstate.ConfigVersion(40) {
		t.Fatal("explicit apply/restart did not activate authoritative new revision")
	}
}

func TestSMHandoffFirstFormatUpgradeRequiresExplicitApplyRestart(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	remote := smstoretest.NewRemote(t)
	defer remote.Close()
	cfg := &config.Config{StateBackend: "file", SnapshotPath: filepath.Join(dir, "control.json"), SMURL: remote.Server.URL, SMToken: smstoretest.Token(t)}
	if _, err := fileSMHandoff([]*blueprint.Resolved{smBlueprint(t)}, cfg, "release-a", time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	provision := runFixtureProvision(t, cfg, nil, "release-a")
	provision(true, true)
	runtimeDir := smstate.RuntimeDir(cfg.SnapshotPath)
	store := smstore.NewFileStore(runtimeDir)
	d, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	legacy := *d.Provision.Registration
	legacy.SchemaVersion = smstate.SchemaVersion
	legacy.SnapshotHash = "legacy-format-hash"
	evidence, _ := json.Marshal(legacy)
	legacyPath := filepath.Join(runtimeDir, smstate.LegacyRegistrationFile)
	if err := os.WriteFile(legacyPath, evidence, 0600); err != nil {
		t.Fatal(err)
	}
	if err := smstate.RemoveRegistration(runtimeDir); err != nil {
		t.Fatal(err)
	}
	bp := smBlueprint(t)
	h, err := fileSMHandoff([]*blueprint.Resolved{bp}, cfg, "release-a", time.Unix(200, 0))
	if err != nil {
		t.Fatal(err)
	}
	m, _ := composedSMTick(t, bp)
	if h.Registered || len(m.Find("sm_check_info")) != 0 {
		t.Fatal("v1 registration silently activated v2")
	}
	provision(false, true)
	bp = smBlueprint(t)
	h, err = fileSMHandoff([]*blueprint.Resolved{bp}, cfg, "release-a", time.Unix(200, 0))
	if err != nil || h.Registered {
		t.Fatal("preview migrated activation", err)
	}
	remote.Mu.Lock()
	before := remote.Posts
	remote.Mu.Unlock()
	provision(true, true)
	remote.Mu.Lock()
	if remote.Posts != before {
		t.Fatal("unchanged-spec upgrade made remote mutation")
	}
	remote.Mu.Unlock()
	bp = smBlueprint(t)
	h, err = fileSMHandoff([]*blueprint.Resolved{bp}, cfg, "release-a", time.Unix(300, 0))
	if err != nil {
		t.Fatal(err)
	}
	m, _ = composedSMTick(t, bp)
	if !h.Registered || len(m.Find("sm_check_info")) == 0 {
		t.Fatal("explicit upgrade apply/restart did not activate")
	}
	retained, err := os.ReadFile(legacyPath)
	if err != nil || string(retained) != string(evidence) {
		t.Fatal("legacy registration evidence changed", err)
	}
}

func TestSMHandoffPendingAndCorruptEvidenceNeverActivates(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{SnapshotPath: filepath.Join(dir, "control.json"), SMURL: "https://sm.example"}
	prepare := func(bp *blueprint.Resolved) (smHandoff, error) {
		return fileSMHandoff([]*blueprint.Resolved{bp}, cfg, "a", time.Unix(100, 0))
	}
	if _, err := prepare(smBlueprint(t)); err != nil {
		t.Fatal(err)
	}
	s, err := smstate.ReadSnapshot(smstate.RuntimeDir(cfg.SnapshotPath))
	if err != nil {
		t.Fatal(err)
	}
	reg := smstate.Registration{SchemaVersion: smstate.ActivationSchemaVersion, SnapshotHash: s.SnapshotHash, TargetFingerprint: s.TargetFingerprint, SourceVersion: "a", Resources: []smstate.Resource{{Kind: "probe", Key: s.Probes[0].Key, ID: 1, Modified: 10, Ownership: smstate.Owned}, {Kind: "check", Key: s.Checks[0].Key, ID: 2, Modified: 20, ConfigVersion: smstate.ConfigVersion(20), Ownership: smstate.Owned}}}
	if err := smstate.WriteRegistration(smstate.RuntimeDir(cfg.SnapshotPath), reg); err != nil {
		t.Fatal(err)
	}
	positive := smBlueprint(t)
	h, err := prepare(positive)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := composedSMTick(t, positive)
	if !h.Registered || len(m.Find("probe_success")) == 0 {
		t.Fatal("positive control absent")
	}
	expected := json.RawMessage(`{"job":"health"}`)
	journal := smstate.Journal{SchemaVersion: 1, Operation: "update", Kind: "check", Key: s.Checks[0].Key, SnapshotHash: s.SnapshotHash, ExpectedHash: smstate.SpecHash(expected), ExpectedSpec: expected, StartedUnixMS: 1}
	if err := smstate.WriteJournal(smstate.RuntimeDir(cfg.SnapshotPath), journal); err != nil {
		t.Fatal(err)
	}
	bp := smBlueprint(t)
	h, err = prepare(bp)
	if err != nil {
		t.Fatal(err)
	}
	m, _ = composedSMTick(t, bp)
	if h.Registered || len(m.Find("probe_success")) != 0 || len(m.All()) == 0 {
		t.Fatal("pending registration activated")
	}
	if err := os.WriteFile(filepath.Join(smstate.RuntimeDir(cfg.SnapshotPath), smstate.JournalFilename), []byte(`{"broken":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepare(smBlueprint(t)); err == nil {
		t.Fatal("corrupt evidence treated as absence")
	}
}

type smEvidenceNegative struct {
	name           string
	mutate         func(*smstore.Document)
	storageFailure bool
	corrupt        bool
}

func smEvidenceResource(d *smstore.Document, kind string) *smstate.Resource {
	for i := range d.Provision.Registration.Resources {
		if d.Provision.Registration.Resources[i].Kind == kind {
			return &d.Provision.Registration.Resources[i]
		}
	}
	panic("positive fixture lacks expected resource kind")
}

// These are persisted evidence shapes, not mocked activation results. The same
// cases run through HA-off handoff and real HA standby/acquisition construction.
func smEvidenceNegatives() []smEvidenceNegative {
	return []smEvidenceNegative{
		{"missing", func(d *smstore.Document) { d.Provision.Registration = nil }, false, false},
		{"empty", func(d *smstore.Document) { d.Provision.Registration.Resources = nil }, false, false},
		{"probe-only", func(d *smstore.Document) {
			d.Provision.Registration.Resources = []smstate.Resource{*smEvidenceResource(d, "probe")}
		}, false, false},
		{"check-only", func(d *smstore.Document) {
			d.Provision.Registration.Resources = []smstate.Resource{*smEvidenceResource(d, "check")}
		}, false, false},
		{"duplicate-probe", func(d *smstore.Document) {
			r := d.Provision.Registration
			r.Resources = append(r.Resources, *smEvidenceResource(d, "probe"))
		}, true, false},
		{"duplicate-check", func(d *smstore.Document) {
			r := d.Provision.Registration
			r.Resources = append(r.Resources, *smEvidenceResource(d, "check"))
		}, true, false},
		{"foreign-probe", func(d *smstore.Document) { smEvidenceResource(d, "probe").Key = "foreign-probe" }, false, false},
		{"foreign-check", func(d *smstore.Document) { smEvidenceResource(d, "check").Key = "foreign-check" }, false, false},
		{"foreign-extra", func(d *smstore.Document) {
			r := d.Provision.Registration
			next := *smEvidenceResource(d, "check")
			next.Key = "foreign-extra"
			r.Resources = append(r.Resources, next)
		}, false, false},
		{"bad-kind", func(d *smstore.Document) { smEvidenceResource(d, "check").Kind = "foreign-kind" }, true, false},
		{"missing-id", func(d *smstore.Document) { smEvidenceResource(d, "check").ID = 0 }, true, false},
		{"negative-id", func(d *smstore.Document) { smEvidenceResource(d, "probe").ID = -1 }, true, false},
		{"bad-ownership", func(d *smstore.Document) { smEvidenceResource(d, "check").Ownership = "foreign" }, true, false},
		{"missing-probe-modified", func(d *smstore.Document) { smEvidenceResource(d, "probe").Modified = 0 }, true, false},
		{"missing-check-modified", func(d *smstore.Document) { smEvidenceResource(d, "check").Modified = 0 }, true, false},
		{"negative-modified", func(d *smstore.Document) { smEvidenceResource(d, "check").Modified = -1 }, true, false},
		{"missing-config-version", func(d *smstore.Document) { smEvidenceResource(d, "check").ConfigVersion = "" }, true, false},
		{"bad-config-version", func(d *smstore.Document) {
			smEvidenceResource(d, "check").ConfigVersion = smstate.ConfigVersion(21)
		}, true, false},
		{"missing-provenance", func(d *smstore.Document) { d.Provision.Registration.SourceVersion = "" }, true, false},
		{"missing-hash", func(d *smstore.Document) { d.Provision.Registration.SnapshotHash = "" }, true, false},
		{"stale-hash", func(d *smstore.Document) { d.Provision.Registration.SnapshotHash = "stale-spec" }, false, false},
		{"missing-target", func(d *smstore.Document) { d.Provision.Registration.TargetFingerprint = "" }, true, false},
		{"foreign-target", func(d *smstore.Document) { d.Provision.Registration.TargetFingerprint = "foreign-target" }, false, false},
		{"schema-v1", func(d *smstore.Document) { d.Provision.Registration.SchemaVersion = 1 }, true, false},
		{"schema-future", func(d *smstore.Document) { d.Provision.Registration.SchemaVersion = 3 }, true, false},
		{"pending-complete", func(d *smstore.Document) {
			raw := json.RawMessage(`{"job":"health"}`)
			d.Provision.Pending = &smstate.Journal{SchemaVersion: 1, Operation: "update", Kind: "check", Key: d.Snapshot.Checks[0].Key, SnapshotHash: d.Snapshot.SnapshotHash, ExpectedHash: smstate.SpecHash(raw), ExpectedSpec: raw, StartedUnixMS: 1}
		}, false, false},
		{"modified-overflow", func(*smstore.Document) {}, true, true},
		{"modified-nonnumeric", func(*smstore.Document) {}, true, true},
		{"modified-null", func(*smstore.Document) {}, true, true},
		{"corrupt-registration", func(*smstore.Document) {}, true, true},
	}
}

func corruptSMEvidenceJSON(data []byte, name string) []byte {
	if name == "" {
		return data
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		panic(err)
	}
	// An aggregate mutation changes registration only, preserving valid ownership,
	// snapshot and lock evidence. Private registration has no outer envelope.
	if provision, ok := fields["provision"]; ok {
		var p map[string]json.RawMessage
		if err := json.Unmarshal(provision, &p); err != nil {
			panic(err)
		}
		p["registration"] = corruptSMEvidenceJSON(p["registration"], name)
		fields["provision"], _ = json.Marshal(p)
		out, err := json.Marshal(fields)
		if err != nil {
			panic(err)
		}
		return out
	}
	if name == "corrupt-registration" {
		return []byte(`{"broken":true}`)
	}
	var resources []map[string]json.RawMessage
	if err := json.Unmarshal(fields["resources"], &resources); err != nil {
		panic(err)
	}
	found := false
	for _, r := range resources {
		if string(r["kind"]) != `"check"` {
			continue
		}
		switch name {
		case "modified-overflow":
			r["modified"] = json.RawMessage(`1e999`)
		case "modified-nonnumeric":
			r["modified"] = json.RawMessage(`"NaN"`)
		case "modified-null":
			r["modified"] = json.RawMessage(`null`)
		default:
			panic("unknown corrupt evidence fixture")
		}
		found = true
	}
	if !found {
		panic("positive evidence lacks check")
	}
	fields["resources"], _ = json.Marshal(resources)
	out, err := json.Marshal(fields)
	if err != nil {
		panic(err)
	}
	return out
}

func cloneSMDocument(t *testing.T, d smstore.Document) smstore.Document {
	t.Helper()
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var out smstore.Document
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSMHandoffRealRunnerEvidenceMatrix(t *testing.T) {
	for _, mode := range []string{"file", "kubernetes"} {
		t.Run(mode, func(t *testing.T) {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			remote := smstoretest.NewRemote(t)
			defer remote.Close()
			cfg := &config.Config{StateBackend: mode, SnapshotPath: filepath.Join(dir, "control.json"), SMURL: remote.Server.URL, SMToken: smstoretest.Token(t), StateCASMaxAttempts: 5}
			var a *smstoretest.API
			var store smstore.Store
			if mode == "file" {
				store = smstore.NewFileStore(smstate.RuntimeDir(cfg.SnapshotPath))
			} else {
				a = smstoretest.New(t, "sm")
				defer a.Close()
				a.Permit()
				cfg.StateSMConfigMap, cfg.SMProvisionLeaseName, cfg.HANamespace = "sm", "provision", "test"
				backend, err := control.NewKubernetesBackendForClient(control.KubernetesBackendOptions{Gate: ha.AlwaysLeader{}, Namespace: "test", Objects: map[control.Key]string{control.SMState: "sm"}, RequestTimeout: 2 * time.Second, MaxDocumentBytes: control.MaxStateDocumentBytes}, a.Server.URL, a.Server.Client())
				if err != nil {
					t.Fatal(err)
				}
				store, err = configuredSMStore(cfg, backend)
				if err != nil {
					t.Fatal(err)
				}
			}
			prepare := func(bp *blueprint.Resolved) (smHandoff, error) {
				return prepareSMHandoff(context.Background(), []*blueprint.Resolved{bp}, cfg, "release-a", time.Unix(200, 0), store, true)
			}
			if _, err := prepare(smBlueprint(t)); err != nil {
				t.Fatal(err)
			}
			runFixtureProvision(t, cfg, a, "release-a")(true, true)
			original, err := store.Load(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			persist := func(d smstore.Document, corruptName string) {
				t.Helper()
				if mode == "file" {
					path := filepath.Join(smstate.RuntimeDir(cfg.SnapshotPath), smstate.RegistrationFile)
					if d.Provision.Registration == nil {
						if err := smstate.RemoveRegistration(smstate.RuntimeDir(cfg.SnapshotPath)); err != nil {
							t.Fatal(err)
						}
					} else {
						data, _ := json.Marshal(d.Provision.Registration)
						data = corruptSMEvidenceJSON(data, corruptName)
						if err := os.WriteFile(path, data, 0600); err != nil {
							t.Fatal(err)
						}
					}
					if d.Provision.Pending == nil {
						if err := smstate.RemoveJournal(smstate.RuntimeDir(cfg.SnapshotPath)); err != nil {
							t.Fatal(err)
						}
					} else if err := smstate.WriteJournal(smstate.RuntimeDir(cfg.SnapshotPath), *d.Provision.Pending); err != nil {
						t.Fatal(err)
					}
				} else {
					data, _ := json.Marshal(d)
					data = corruptSMEvidenceJSON(data, corruptName)
					a.Set("sm", data)
				}
			}
			for _, tc := range smEvidenceNegatives() {
				t.Run(tc.name, func(t *testing.T) {
					persist(cloneSMDocument(t, original), "")
					positive := smBlueprint(t)
					h, err := prepare(positive)
					if err != nil {
						t.Fatal(err)
					}
					m, l := composedSMTick(t, positive)
					if !h.Registered || len(m.Find("sm_check_info")) == 0 || len(m.Find("probe_success")) == 0 || len(l.Streams) == 0 {
						t.Fatal("positive actual runner control absent")
					}
					bad := cloneSMDocument(t, original)
					tc.mutate(&bad)
					corruptName := ""
					if tc.corrupt {
						corruptName = tc.name
					}
					persist(bad, corruptName)
					bp := smBlueprint(t)
					h, err = prepare(bp)
					if tc.storageFailure {
						if err == nil {
							t.Fatal("invalid evidence reached runner construction")
						}
						t.Log("real root handoff rejected invalid persisted evidence before runner construction")
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					m, l = composedSMTick(t, bp)
					assertSMSuppressed(t, h, m, l)
					facts := collectOptionalRuntimeFacts([]*blueprint.Resolved{bp}, nil, cfg, h)
					for _, d := range optionalRuntimeDispositions(cfg, facts, nil, fleetstatus.FleetStat{}, 0, false, false) {
						if d.Lane == optionallane.SyntheticMonitoring && d.State == optionallane.Enabled {
							t.Fatal("suppressed runtime advertised enabled SM")
						}
					}
					t.Log("real composed runner: zero SM metrics/logs; unrelated emission retained")
				})
			}
		})
	}
}

func TestSMRealRunnerBoundSpecMatrix(t *testing.T) {
	const second = "      - {name: secondary-health, target: 'https://secondary.example/health', probe: secondary, region: AMER}\n"
	const third = "      - {name: third-health, target: 'https://third.example/health'}\n"
	baseline := smRuntimeBlueprintYAML + second + third
	type mutation struct {
		name   string
		yaml   func(string) string
		target string
		prior  func(*smstate.Snapshot)
	}
	mutations := []mutation{
		{"check-removal", func(s string) string { return strings.Replace(s, third, "", 1) }, "", nil},
		{"probe-removal", func(s string) string { return strings.Replace(s, second, "", 1) }, "", nil},
		{"frequency", func(s string) string {
			return strings.Replace(s, "labels: {team: platform}", "frequency: 90000, labels: {team: platform}", 1)
		}, "", nil},
		{"labels", func(s string) string { return strings.Replace(s, "team: platform", "team: other", 1) }, "", nil},
		{"probe", func(s string) string {
			return strings.Replace(s, "labels: {team: platform}", "probe: other, labels: {team: platform}", 1)
		}, "", nil},
		{"region", func(s string) string {
			return strings.Replace(s, "labels: {team: platform}", "region: AMER, labels: {team: platform}", 1)
		}, "", nil},
		{"source", func(s string) string { return strings.Replace(s, "name: sm-test", "name: other-source", 1) }, "", nil},
		{"addition", func(s string) string {
			return s + "      - {name: additional-health, target: 'https://additional.example/health'}\n"
		}, "", nil},
		{"target-credential", nil, "credential", nil},
		{"target-url", nil, "url", nil},
		{"timeout", nil, "", func(s *smstate.Snapshot) { s.Checks[0].TimeoutMS++ }},
		{"latitude", nil, "", func(s *smstate.Snapshot) { s.Probes[0].Latitude++ }},
		{"longitude", nil, "", func(s *smstate.Snapshot) { s.Probes[0].Longitude++ }},
	}
	for _, mode := range []string{"file", "kubernetes", "kubernetes-acquisition"} {
		t.Run(mode, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			remote := smstoretest.NewRemote(t)
			defer remote.Close()
			cfg, err := config.Load(filepath.Join(root, "absent.env"))
			if err != nil {
				t.Fatal(err)
			}
			cfg.StateBackend, cfg.HAMode = "file", "off"
			cfg.SnapshotPath, cfg.SMURL, cfg.SMToken = filepath.Join(root, "control.json"), remote.Server.URL, smstoretest.Token(t)
			cfg.DryRun, cfg.SelfObsEnabled = true, false
			cfg.FMURL, cfg.ControlToken, cfg.SigilEndpoint, cfg.FaroCollector, cfg.ProfilesURL = "", "", "", "", ""
			cfg.StateCASMaxAttempts = 5
			cfg.StateGitSourceConfigMaps = map[string]string{}
			cfg.BlueprintsDir, cfg.BlueprintDataDir = filepath.Join(root, "baked"), filepath.Join(root, "absent-data")
			if err := os.Mkdir(cfg.BlueprintsDir, 0700); err != nil {
				t.Fatal(err)
			}
			var a *smstoretest.API
			var store smstore.Store
			makeBackend := func(g ha.LeaderGate) control.StateBackend {
				t.Helper()
				b, err := control.NewKubernetesBackendForClient(control.KubernetesBackendOptions{Gate: g, Namespace: "test", Objects: map[control.Key]string{control.SMState: "sm", control.Control: "control", control.BootManifest: "boot"}, RequestTimeout: 2 * time.Second, MaxDocumentBytes: control.MaxStateDocumentBytes}, a.Server.URL, a.Server.Client())
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			if mode == "file" {
				store = smstore.NewFileStore(smstate.RuntimeDir(cfg.SnapshotPath))
			} else {
				cfg.StateBackend, cfg.HANamespace, cfg.StateSMConfigMap, cfg.SMProvisionLeaseName, cfg.StateControlConfigMap, cfg.StateBootConfigMap = "kubernetes", "test", "sm", "provision", "control", "boot"
				a = smstoretest.New(t, "sm", "control", "boot")
				defer a.Close()
				a.Permit()
				store, err = configuredSMStore(cfg, makeBackend(ha.AlwaysLeader{}))
				if err != nil {
					t.Fatal(err)
				}
			}
			base := *cfg
			seed := mustLoad(t, []byte(baseline))
			if _, err := prepareSMHandoff(context.Background(), []*blueprint.Resolved{seed}, cfg, version, time.Unix(100, 0), store, true); err != nil {
				t.Fatal(err)
			}
			runFixtureProvision(t, cfg, a, version)(true, true)
			original, err := store.Load(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			persist := func(d smstore.Document) {
				t.Helper()
				if mode == "file" {
					if err := smstate.WriteRegistration(smstate.RuntimeDir(cfg.SnapshotPath), *d.Provision.Registration); err != nil {
						t.Fatal(err)
					}
				} else {
					data, _ := json.Marshal(d)
					a.Set("sm", data)
				}
			}
			observe := func(yaml string) (bool, bool, bool, int) {
				t.Helper()
				bp := mustLoad(t, []byte(yaml))
				if mode != "kubernetes-acquisition" {
					h, err := prepareSMHandoff(context.Background(), []*blueprint.Resolved{bp}, cfg, version, time.Unix(200, 0), store, true)
					if err != nil {
						t.Fatal(err)
					}
					m, l := composedSMTick(t, bp)
					logs := false
					for _, stream := range l.Streams {
						if stream.Labels["source"] == "synthetic-monitoring-agent" {
							logs = true
						}
					}
					return h.Registered, len(m.Find("sm_check_info")) > 0 || len(m.Find("probe_success")) > 0, logs, len(m.All())
				}
				cfg.HAMode = "lease"
				cfg.BlueprintNames = []string{bp.Name}
				if err := os.WriteFile(filepath.Join(cfg.BlueprintsDir, "sm.yaml"), []byte(yaml), 0600); err != nil {
					t.Fatal(err)
				}
				gate := ha.NewGate()
				backend := makeBackend(gate)
				bound := ha.Bounded{Gate: gate, Timeout: 8 * time.Second, Margin: 2 * time.Second, Crash: func() { t.Error("unexpected HA bound crash") }}
				a.Mu.Lock()
				writes := a.StateWrites
				a.Mu.Unlock()
				standby, err := newHAView(context.Background(), cfg, gate, bound, false, backend)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					standby.runner.DrainQueues(ctx)
				}()
				if err := standby.runner.RunOnce(context.Background(), time.Unix(200, 0)); !errors.Is(err, ha.ErrNotLeader) {
					t.Fatalf("standby production admission=%v", err)
				}
				a.Mu.Lock()
				after := a.StateWrites
				a.Mu.Unlock()
				if after != writes {
					t.Fatal("standby wrote state")
				}
				var v *haView
				if err := gate.Activate(context.Background(), func(ctx context.Context) error {
					var err error
					v, err = newHAView(ctx, cfg, gate, bound, true, backend)
					return err
				}); err != nil {
					t.Fatal(err)
				}
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					v.runner.DrainQueues(ctx)
				}()
				v.prom.Capture, v.loki.Capture = true, true
				if err := v.runner.RunOnce(context.Background(), time.Unix(200, 0)); err != nil {
					t.Fatal(err)
				}
				logs := false
				for _, stream := range v.loki.Captured() {
					if stream.Labels["source"] == "synthetic-monitoring-agent" {
						logs = true
					}
				}
				return v.smState.Registered, len(v.prom.SeriesFor("sm_check_info")) > 0 || len(v.prom.SeriesFor("probe_success")) > 0, logs, len(v.prom.Captured())
			}
			for _, tc := range mutations {
				t.Run(tc.name, func(t *testing.T) {
					*cfg = base
					persist(cloneSMDocument(t, original))
					registered, metrics, logs, count := observe(baseline)
					if !registered || !metrics || !logs || count == 0 {
						t.Fatal("positive real runner control absent")
					}
					yaml := baseline
					if tc.yaml != nil {
						yaml = tc.yaml(yaml)
					}
					switch tc.target {
					case "credential":
						cfg.SMToken = smstoretest.Token(t)
					case "url":
						cfg.SMURL += "/rotated"
					}
					if tc.prior != nil {
						d := cloneSMDocument(t, original)
						prior := *cloneSMDocument(t, original).Snapshot
						tc.prior(&prior)
						prior, err = smstate.NewSnapshot(prior.Probes, prior.Checks, cfg.SMURL, cfg.SMToken, version, time.Unix(100, 0))
						if err != nil {
							t.Fatal(err)
						}
						d.Provision.Registration.SnapshotHash = prior.SnapshotHash
						persist(d)
					}
					registered, metrics, logs, count = observe(yaml)
					if registered || metrics || logs || count == 0 {
						t.Fatal("changed bound spec emitted SM or lost unrelated control")
					}
					t.Log("real composed runner bound-spec negative: zero SM metrics/logs; unrelated emission retained")
				})
			}
		})
	}
}

// fileReadCheckpoint is a cancellation-check observer: its third Err call is
// after the real file adapter sampled Pending (publication and Load each check
// cancellation first). No store result is manufactured or echoed by this seam.
type fileReadCheckpoint struct {
	context.Context
	calls        int
	afterPending func()
}

func (c *fileReadCheckpoint) Err() error {
	c.calls++
	if c.calls == 3 {
		c.afterPending()
	}
	return c.Context.Err()
}

func TestSMFileConcurrentApplyCannotTearActivation(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	remote := smstoretest.NewRemote(t)
	defer remote.Close()
	cfg := &config.Config{StateBackend: "file", SnapshotPath: filepath.Join(dir, "control.json"), SMURL: remote.Server.URL, SMToken: smstoretest.Token(t)}
	store := smstore.NewFileStore(smstate.RuntimeDir(cfg.SnapshotPath))
	prepare := func(ctx context.Context, bp *blueprint.Resolved) (smHandoff, error) {
		return prepareSMHandoff(ctx, []*blueprint.Resolved{bp}, cfg, "release-a", time.Unix(200, 0), store, true)
	}
	if _, err := prepare(context.Background(), smBlueprint(t)); err != nil {
		t.Fatal(err)
	}
	runFixtureProvision(t, cfg, nil, "release-a")(true, true)
	positive := smBlueprint(t)
	h, err := prepare(context.Background(), positive)
	if err != nil {
		t.Fatal(err)
	}
	m, l := composedSMTick(t, positive)
	if !h.Registered || len(m.Find("probe_success")) == 0 || len(l.Streams) == 0 {
		t.Fatal("positive runtime emission control absent")
	}
	// Remote drift, not a changed local spec: hashes still match after update.
	remote.Mu.Lock()
	remote.Checks[0]["frequency"] = 90000
	posts := remote.Posts
	remote.Mu.Unlock()
	exe := filepath.Join(dir, "pending-provision")
	buildCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(buildCtx, "go", "test", "-c", "-ldflags=-X github.com/rknightion/synthkit/cmd/sm-provision.version=release-a", "-o", exe, "../sm-provision").CombinedOutput(); err != nil {
		t.Fatalf("build pending helper: %v %s", err, out)
	}
	pc := config.SMProvisionConfig{StateBackend: "file", SnapshotPath: cfg.SnapshotPath, SMURL: cfg.SMURL, SMToken: cfg.SMToken, Apply: true}
	data, _ := json.Marshal(pc)
	path := filepath.Join(dir, "pending-config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	apply := func() string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, "-test.run=^TestFileProvisionRetainsCompletePendingHelper$")
		cmd.Env = append(os.Environ(), "SYNTHKIT_SM_PENDING_CONFIG="+path, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("pending helper: %v %s", err, out)
		}
		return string(out)
	}
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	checkpoint := &fileReadCheckpoint{Context: ctx}
	var transcript string
	checkpoint.afterPending = func() { transcript = apply() }
	bp := smBlueprint(t)
	h, err = prepare(checkpoint, bp)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.calls < 3 || transcript == "" {
		t.Fatal("real Pending read checkpoint not reached")
	}
	m, l = composedSMTick(t, bp)
	if strings.Contains(transcript, "retained-complete-pending=true") {
		if h.Registered || len(m.Find("probe_success")) != 0 {
			t.Fatal("torn file read activated new complete registration despite durable pending")
		}
	} else if strings.Contains(transcript, "lock-contention=true") {
		// Corrected reader owns O_EXCL while sampling: the concurrent CLI cannot
		// start, and the pre-apply complete registration remains a positive control.
		if !h.Registered || len(m.Find("probe_success")) == 0 {
			t.Fatal("consistent pre-apply registration did not emit")
		}
		transcript = apply()
	} else {
		t.Fatalf("unrecognized real apply outcome: %s", transcript)
	}
	if !strings.Contains(transcript, "retained-complete-pending=true") {
		t.Fatal("actual interrupted apply not observed")
	}
	d, err := store.Load(context.Background())
	if err != nil || d.Provision.Pending == nil || d.Provision.Registration == nil || smstate.ValidateRegistration(*d.Snapshot, *d.Provision.Registration) != nil {
		t.Fatalf("durable complete pending proof missing: %v", err)
	}
	remote.Mu.Lock()
	if remote.Posts != posts+1 {
		t.Errorf("actual check update receipts=%d want=%d", remote.Posts, posts+1)
	}
	remote.Mu.Unlock()
	bp = smBlueprint(t)
	h, err = prepare(context.Background(), bp)
	if err != nil {
		t.Fatal(err)
	}
	m, l = composedSMTick(t, bp)
	assertSMSuppressed(t, h, m, l)
	t.Log("synchronized actual file handoff/apply: O_EXCL contention; authoritative update persisted with pending; composed runner suppressed; unrelated emission retained")
}

func assertSMSuppressed(t *testing.T, h smHandoff, m *coretest.MetricCapture, l *coretest.LogCapture) {
	t.Helper()
	if h.Registered || len(m.Find("sm_check_info")) != 0 || len(m.Find("probe_success")) != 0 || len(m.All()) == 0 {
		t.Fatalf("SM suppression/unrelated control failed: handoff=%+v", h)
	}
	for _, stream := range l.Streams {
		if stream.Labels["source"] == "synthetic-monitoring-agent" {
			t.Fatal("suppressed SM emitted log")
		}
	}
}

func hasSMConstruct(resolved *blueprint.Resolved) bool {
	for _, instance := range resolved.Constructs {
		if instance.Kind == sm.Kind {
			return true
		}
	}
	return false
}

func TestSMHandoffInvalidatesPriorSnapshotWhenNoSMLaneIsSelected(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{SnapshotPath: filepath.Join(dir, "control-state.json"), SMURL: "https://sm.example", SMToken: "token"}
	selected := smBlueprint(t)
	if _, err := fileSMHandoff([]*blueprint.Resolved{selected}, cfg, "v1.2.3", time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(smstate.RuntimeDir(cfg.SnapshotPath), smstate.SnapshotFilename)
	if _, err := os.Lstat(path); err != nil {
		t.Fatal(err)
	}
	if _, err := fileSMHandoff(nil, cfg, "v1.2.3", time.Unix(200, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("prior snapshot remained reusable: %v", err)
	}
}
