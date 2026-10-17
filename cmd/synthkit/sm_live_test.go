// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/construct/sm"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/smstate"
	"github.com/rknightion/synthkit/internal/smstore"
)

// liveSMYAML declares one check on the default probe, so it shares the probe with smBlueprint.
const liveSMYAML = `name: live-probe
environments:
  - name: prod
    weight: 1.0
features:
  synthetic_monitoring:
    enabled: true
    checks:
      - {name: live-probe-health, target: "https://live.example.com/health"}
`

// liveSMConflictYAML declares the default probe name at another location.
const liveSMConflictYAML = `name: live-moved
environments:
  - name: prod
    weight: 1.0
features:
  synthetic_monitoring:
    enabled: true
    checks:
      - {name: live-moved-health, target: "https://moved.example.com/health", latitude: 40.7128, longitude: -74.006}
`

const liveSMOtherYAML = `name: live-other
environments:
  - name: prod
    weight: 1.0
features:
  synthetic_monitoring:
    enabled: true
    checks:
      - {name: live-other-health, target: "https://other.example.com/health"}
`

func liveSMRecord(name, yaml string) control.LiveBlueprint {
	return control.LiveBlueprint{Name: "synthetic/" + name, Namespace: "synthetic", YAML: yaml}
}

// smLiveBoot is one composed process: the boot SM handoff, a runner whose LiveAdmit is that
// handoff's admission (as main.go wires it), and the control store holding the live set.
type smLiveBoot struct {
	t       *testing.T
	r       *runner.Runner
	store   *control.Store
	metrics *coretest.MetricCapture
	handoff smHandoff
}

func bootSMLive(t *testing.T, cfg *config.Config, statePath string, boot ...*blueprint.Resolved) *smLiveBoot {
	t.Helper()
	m, l := &coretest.MetricCapture{}, &coretest.LogCapture{}
	store := control.NewStore(statePath)
	reg := runner.Catalog()
	if err := applyStartupDisabledKinds(reg, boot, cfg.StartupDisabledKinds); err != nil {
		t.Fatal(err)
	}
	admit := newSMAdmission(cfg, "v1.2.3", smstore.NewFileStore(smstate.RuntimeDir(cfg.SnapshotPath)), true, smStartupDisabled(cfg), nil)
	handoff, err := admit.prepareBoot(context.Background(), boot, store.Snapshot().LiveBlueprints, reg, blueprint.RuntimeLimits{})
	if err != nil {
		t.Fatal(err)
	}
	r := runner.New(runner.Sinks{Metrics: m, Logs: l}, reg, runner.Options{LiveAdmit: admit.Admit})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		r.DrainQueues(ctx)
	})
	for _, bp := range boot {
		if err := r.AddBlueprint(bp); err != nil {
			t.Fatal(err)
		}
	}
	if err := seedStartupDisabledKinds(context.Background(), store, cfg.StartupDisabledKinds); err != nil {
		t.Fatal(err)
	}
	r.ApplyControl(store.Snapshot()) // restores the persisted live set through LiveAdmit
	return &smLiveBoot{t: t, r: r, store: store, metrics: m, handoff: handoff}
}

// apply is the runner side of POST /control/blueprints/live, persisting like the handler does.
func (b *smLiveBoot) apply(lb control.LiveBlueprint) error {
	return b.r.ApplyLiveBlueprint(lb, func() error {
		_, err := b.store.UpdateContext(context.Background(), func(s *control.State) { s.LiveBlueprints = append(s.LiveBlueprints, lb) })
		return err
	})
}

func (b *smLiveBoot) running(name string) bool {
	for _, res := range b.r.RunningResolved() {
		if res.Name == name {
			return true
		}
	}
	return false
}

// checkVersions ticks once and returns each emitting check's config_version from sm_check_info.
func (b *smLiveBoot) checkVersions() map[string]string {
	b.t.Helper()
	before := len(b.metrics.All())
	if err := b.r.RunOnce(context.Background(), time.Unix(200, 0)); err != nil {
		b.t.Fatal(err)
	}
	out := map[string]string{}
	for _, s := range b.metrics.All()[before:] {
		if s.Name == "sm_check_info" {
			out[s.Labels["job"]] = s.Labels["config_version"]
		}
	}
	return out
}

func smLiveConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &config.Config{SnapshotPath: filepath.Join(dir, "control-state.json"), SMURL: "https://sm.example", SMToken: "token"}, filepath.Join(dir, "control-state.json")
}

// registerPublishedSnapshot stands in for a successful provisioner apply of the published snapshot.
func registerPublishedSnapshot(t *testing.T, cfg *config.Config) smstate.Snapshot {
	t.Helper()
	dir := smstate.RuntimeDir(cfg.SnapshotPath)
	snapshot, err := smstate.ReadSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	registration := smstate.Registration{
		SchemaVersion: smstate.ActivationSchemaVersion, SnapshotHash: snapshot.SnapshotHash,
		TargetFingerprint: snapshot.TargetFingerprint, SourceVersion: snapshot.SourceVersion,
	}
	for i, probe := range snapshot.Probes {
		registration.Resources = append(registration.Resources, smstate.Resource{Kind: "probe", Key: probe.Key, ID: int64(10 + i), Modified: 10, Ownership: smstate.Owned})
	}
	for i, check := range snapshot.Checks {
		modified := 1725000000 + float64(i)
		registration.Resources = append(registration.Resources, smstate.Resource{Kind: "check", Key: check.Key, ID: int64(100 + i), Modified: modified, ConfigVersion: smstate.ConfigVersion(modified), Ownership: smstate.Owned})
	}
	if err := smstate.WriteRegistration(dir, registration); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func publishedCheckJobs(t *testing.T, cfg *config.Config) []string {
	t.Helper()
	snapshot, err := smstate.ReadSnapshot(smstate.RuntimeDir(cfg.SnapshotPath))
	if err != nil {
		t.Fatal(err)
	}
	var jobs []string
	for _, check := range snapshot.Checks {
		jobs = append(jobs, check.Job)
	}
	return jobs
}

// A live SM blueprint goes through the boot handoff: it publishes the running set's snapshot and
// runs without SM until a registration matches that exact snapshot; after the provisioner registers
// it, the restart restores the live blueprint with injected registration, and a further live
// blueprint the registration does not cover is admitted stripped, exactly as boot strips.
func TestLiveSMAdmittedOnlyThroughBootHandoff(t *testing.T) {
	cfg, statePath := smLiveConfig(t)
	first := bootSMLive(t, cfg, statePath, smBlueprint(t))
	if !first.handoff.Declared || first.handoff.Registered {
		t.Fatalf("boot handoff without registration = %+v", first.handoff)
	}
	if err := first.apply(liveSMRecord("live-probe", liveSMYAML)); err != nil {
		t.Fatalf("live SM apply = %v, want admitted (stripped until registered)", err)
	}
	if !first.running("synthetic/live-probe") {
		t.Fatal("admitted live SM blueprint is not running")
	}
	if got := first.checkVersions(); len(got) != 0 {
		t.Fatalf("unregistered live SM emitted sm_check_info: %v", got)
	}
	if jobs := publishedCheckJobs(t, cfg); strings.Join(jobs, ",") != "api-health,live-probe-health" {
		t.Fatalf("published snapshot checks = %v, want the boot and live checks", jobs)
	}

	snapshot := registerPublishedSnapshot(t, cfg)
	restarted := bootSMLive(t, cfg, statePath, smBlueprint(t))
	if !restarted.handoff.Registered {
		t.Fatalf("boot handoff after registering the running set = %+v, want registered", restarted.handoff)
	}
	if !restarted.running("synthetic/live-probe") || len(restarted.r.LiveRestoreSkips()) != 0 {
		t.Fatalf("restore skipped the live SM blueprint: %+v", restarted.r.LiveRestoreSkips())
	}
	versions := restarted.checkVersions()
	for _, check := range snapshot.Checks {
		resource, _ := smstate.ResourceByKey(mustRegistration(t, cfg), "check", check.Key)
		if versions[check.Job] != resource.ConfigVersion {
			t.Fatalf("check %s config_version = %q, want injected %q (all=%v)", check.Job, versions[check.Job], resource.ConfigVersion, versions)
		}
	}

	if err := restarted.apply(liveSMRecord("live-other", liveSMOtherYAML)); err != nil {
		t.Fatalf("uncovered live SM apply = %v, want admitted stripped", err)
	}
	versions = restarted.checkVersions()
	if _, ok := versions["live-other-health"]; ok {
		t.Fatalf("live SM check the registration does not cover emitted: %v", versions)
	}
	if _, ok := versions["api-health"]; !ok {
		t.Fatalf("running boot SM stopped emitting after an uncovered live apply: %v", versions)
	}
	for _, res := range restarted.r.RunningResolved() {
		if res.Name == "synthetic/live-other" && hasSMConstruct(res) {
			t.Fatal("uncovered live SM construct was not stripped")
		}
	}
}

// Every persisted record is restored against the whole persisted set, so with two live SM
// blueprints the first restored sees the same snapshot the boot handoff published and registered.
func TestLiveSMRestoresWholePersistedSet(t *testing.T) {
	cfg, statePath := smLiveConfig(t)
	first := bootSMLive(t, cfg, statePath, smBlueprint(t))
	for _, lb := range []control.LiveBlueprint{liveSMRecord("live-probe", liveSMYAML), liveSMRecord("live-other", liveSMOtherYAML)} {
		if err := first.apply(lb); err != nil {
			t.Fatal(err)
		}
	}
	registerPublishedSnapshot(t, cfg)
	got := bootSMLive(t, cfg, statePath, smBlueprint(t)).checkVersions()
	for _, job := range []string{"api-health", "live-probe-health", "live-other-health"} {
		if got[job] == "" {
			t.Fatalf("restored set emitted %v, want %s registered", got, job)
		}
	}
}

func mustRegistration(t *testing.T, cfg *config.Config) smstate.Registration {
	t.Helper()
	doc, err := smstore.NewFileStore(smstate.RuntimeDir(cfg.SnapshotPath)).Load(context.Background())
	if err != nil || doc.Provision.Registration == nil {
		t.Fatalf("registration: %v", err)
	}
	return *doc.Provision.Registration
}

// A probe declared at another region or location than a running blueprint's (boot or live) is a
// 409 naming the probe, and leaves the running and persisted sets unchanged.
func TestLiveSMProbeConflictRejectedNamingProbe(t *testing.T) {
	for _, tc := range []struct {
		name string
		boot []*blueprint.Resolved
		live []control.LiveBlueprint
	}{
		{name: "boot", boot: []*blueprint.Resolved{smBlueprint(t)}},
		{name: "live", live: []control.LiveBlueprint{liveSMRecord("live-probe", liveSMYAML)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, statePath := smLiveConfig(t)
			b := bootSMLive(t, cfg, statePath, tc.boot...)
			for _, lb := range tc.live {
				if err := b.apply(lb); err != nil {
					t.Fatal(err)
				}
			}
			before := len(b.store.Snapshot().LiveBlueprints)
			err := b.apply(liveSMRecord("live-moved", liveSMConflictYAML))
			if !errors.Is(err, control.ErrLiveCollision) || !strings.Contains(err.Error(), `"`+sm.DefaultProbeName+`"`) {
				t.Fatalf("conflicting probe apply = %v, want ErrLiveCollision naming probe %q", err, sm.DefaultProbeName)
			}
			if b.running("synthetic/live-moved") || len(b.store.Snapshot().LiveBlueprints) != before {
				t.Fatal("rejected conflicting blueprint changed the running or persisted set")
			}
		})
	}
}

// The restore runs the persisted record through the same hook: one whose probe conflicts with the
// boot set is skipped with the probe named, and the boot handoff and its snapshot are unaffected.
func TestLiveSMRestoreConflictSkippedNamingProbe(t *testing.T) {
	cfg, statePath := smLiveConfig(t)
	if _, err := control.NewStore(statePath).UpdateContext(context.Background(), func(s *control.State) {
		s.LiveBlueprints = []control.LiveBlueprint{liveSMRecord("live-moved", liveSMConflictYAML)}
	}); err != nil {
		t.Fatal(err)
	}
	b := bootSMLive(t, cfg, statePath, smBlueprint(t))
	if !b.handoff.Declared {
		t.Fatalf("boot handoff = %+v", b.handoff)
	}
	skips := b.r.LiveRestoreSkips()
	if len(skips) != 1 || skips[0].Blueprint != "synthetic/live-moved" || !strings.Contains(skips[0].Reason, `"`+sm.DefaultProbeName+`"`) {
		t.Fatalf("restore skips = %+v, want the conflicting record skipped naming probe %q", skips, sm.DefaultProbeName)
	}
	if b.running("synthetic/live-moved") {
		t.Fatal("conflicting persisted record was restored")
	}
	if jobs := publishedCheckJobs(t, cfg); strings.Join(jobs, ",") != "api-health" {
		t.Fatalf("published snapshot checks = %v, want the boot check only", jobs)
	}
}

// HA lease mode without the kubernetes backend never emits SM, from boot or live blueprints, and
// writes no snapshot.
func TestLiveSMHALeaseFileBackendEmitsNoSM(t *testing.T) {
	cfg, statePath := smLiveConfig(t)
	cfg.HAMode, cfg.StateBackend = "lease", "file"
	b := bootSMLive(t, cfg, statePath, smBlueprint(t))
	if err := b.apply(liveSMRecord("live-probe", liveSMYAML)); err != nil {
		t.Fatalf("live SM apply in HA file mode = %v, want admitted stripped", err)
	}
	for _, res := range b.r.RunningResolved() {
		if hasSMConstruct(res) {
			t.Fatalf("blueprint %q kept SM in HA lease mode without kubernetes state", res.Name)
		}
	}
	if got := b.checkVersions(); len(got) != 0 {
		t.Fatalf("HA file mode emitted SM: %v", got)
	}
	if _, err := os.Lstat(filepath.Join(smstate.RuntimeDir(cfg.SnapshotPath), smstate.SnapshotFilename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("HA file mode wrote an SM snapshot: %v", err)
	}
}

// A startup-disabled synthetic_monitoring kind strips live and restored SM and publishes no
// snapshot, even when a registration would otherwise admit it.
func TestLiveSMStartupDisabledNeverEmits(t *testing.T) {
	cfg, statePath := smLiveConfig(t)
	first := bootSMLive(t, cfg, statePath, smBlueprint(t))
	if err := first.apply(liveSMRecord("live-probe", liveSMYAML)); err != nil {
		t.Fatal(err)
	}
	registerPublishedSnapshot(t, cfg)
	if got := bootSMLive(t, cfg, statePath, smBlueprint(t)).checkVersions(); len(got) != 2 {
		t.Fatalf("positive control: registered boot and live SM emitted %v, want both checks", got)
	}
	snapshotPath := filepath.Join(smstate.RuntimeDir(cfg.SnapshotPath), smstate.SnapshotFilename)

	cfg.StartupDisabledKinds = []string{sm.Kind}
	disabled := bootSMLive(t, cfg, statePath, smBlueprint(t))
	if !disabled.running("synthetic/live-probe") {
		t.Fatalf("restore skipped the live blueprint instead of stripping SM: %+v", disabled.r.LiveRestoreSkips())
	}
	if err := disabled.apply(liveSMRecord("live-other", liveSMOtherYAML)); err != nil {
		t.Fatal(err)
	}
	if got := disabled.checkVersions(); len(got) != 0 {
		t.Fatalf("startup-disabled synthetic_monitoring emitted: %v", got)
	}
	// The boot handoff invalidates the snapshot when no SM lane is selected; a live apply of a
	// startup-disabled kind must not publish one again.
	if _, err := os.Lstat(snapshotPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("startup-disabled live SM published a snapshot: %v", err)
	}
}

// The HA view wires the same admission: in lease mode on the file backend a live SM apply and a
// restored SM record are admitted with SM stripped and no snapshot write (no hook would reject the
// apply with 400 and skip the record).
func TestLiveSMHAViewWiresAdmission(t *testing.T) {
	gate := ha.AlwaysLeader{}
	cfg := startupKindsHAConfig(t)
	if _, err := control.NewStore(cfg.SnapshotPath).UpdateContext(context.Background(), func(s *control.State) {
		s.LiveBlueprints = []control.LiveBlueprint{liveSMRecord("live-probe", liveSMYAML)}
	}); err != nil {
		t.Fatal(err)
	}
	view, err := newHAView(context.Background(), cfg, gate, ha.Bounded{Gate: gate}, true)
	if err != nil {
		t.Fatal(err)
	}
	if skips := view.runner.LiveRestoreSkips(); len(skips) != 0 {
		t.Fatalf("HA restore skipped the SM record: %+v", skips)
	}
	if err := view.runner.ApplyLiveBlueprint(liveSMRecord("live-other", liveSMOtherYAML), nil); err != nil {
		t.Fatalf("HA live SM apply = %v, want admitted stripped", err)
	}
	names := map[string]bool{}
	for _, res := range view.runner.RunningResolved() {
		names[res.Name] = true
		if hasSMConstruct(res) {
			t.Fatalf("HA lease file mode kept SM on %q", res.Name)
		}
	}
	if !names["synthetic/live-probe"] || !names["synthetic/live-other"] {
		t.Fatalf("running set = %v, want both live blueprints", names)
	}
	if _, err := os.Lstat(filepath.Join(smstate.RuntimeDir(cfg.SnapshotPath), smstate.SnapshotFilename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("HA file mode wrote an SM snapshot: %v", err)
	}
}

const liveSMNoChecksYAML = `name: live-empty
environments:
  - name: prod
    weight: 1.0
features:
  synthetic_monitoring:
    enabled: true
    checks: []
`

// An SM construct with no checks cannot be built, so its live apply is a 400 that publishes
// nothing: the snapshot covering the running set survives.
func TestLiveSMZeroCheckApplyRejectedBeforePublish(t *testing.T) {
	cfg, statePath := smLiveConfig(t)
	b := bootSMLive(t, cfg, statePath, smBlueprint(t))
	snapshotPath := filepath.Join(smstate.RuntimeDir(cfg.SnapshotPath), smstate.SnapshotFilename)
	before, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	err = b.apply(liveSMRecord("live-empty", liveSMNoChecksYAML))
	if !errors.Is(err, control.ErrLiveInvalid) {
		t.Fatalf("zero-check SM apply = %v, want ErrLiveInvalid", err)
	}
	if after, err := os.ReadFile(snapshotPath); err != nil || string(after) != string(before) {
		t.Fatalf("zero-check SM apply changed the published snapshot (err=%v)", err)
	}
	if b.running("synthetic/live-empty") || len(b.store.Snapshot().LiveBlueprints) != 0 {
		t.Fatal("rejected zero-check apply changed the running or persisted set")
	}
}

// The non-HA composition root (run) wires the boot handoff over the persisted live set and the
// LiveAdmit hook: a persisted live SM record is restored (not skipped) and its check is in the
// published snapshot.
func TestRunWiresLiveSMAdmission(t *testing.T) {
	snapshotPath := startupKindsEnv(t, "", "https://sm.example")
	if _, err := control.NewStore(snapshotPath).UpdateContext(context.Background(), func(s *control.State) {
		s.LiveBlueprints = []control.LiveBlueprint{liveSMRecord("live-probe", liveSMYAML)}
	}); err != nil {
		t.Fatal(err)
	}
	var logs strings.Builder
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	if err := run(true, false, filepath.Join(t.TempDir(), "absent.env")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), `restored live blueprint "synthetic/live-probe"`) || strings.Contains(logs.String(), `skipping persisted live blueprint "synthetic/live-probe"`) {
		t.Fatalf("run did not restore the persisted live SM record through LiveAdmit:\n%s", logs.String())
	}
	cfg := &config.Config{SnapshotPath: snapshotPath}
	if jobs := publishedCheckJobs(t, cfg); strings.Join(jobs, ",") != "api-health,live-probe-health" {
		t.Fatalf("boot snapshot checks = %v, want the boot and persisted live checks", jobs)
	}
}

// The boot snapshot leaves out persisted records the restore will skip: an SM construct with no
// checks (a build failure) and any record past the live limit.
func TestLiveSMBootSnapshotOmitsRecordsRestoreSkips(t *testing.T) {
	cfg, statePath := smLiveConfig(t)
	records := []control.LiveBlueprint{liveSMRecord("live-empty", liveSMNoChecksYAML)}
	for i := 1; len(records) < control.MaxLiveBlueprints; i++ {
		name := fmt.Sprintf("plain-%02d", i)
		records = append(records, liveSMRecord(name, fmt.Sprintf("name: %s\nhosts:\n  - name: host-%02d\n    os: linux\n", name, i)))
	}
	records = append(records, liveSMRecord("live-probe", liveSMYAML)) // index 64: past the limit
	if _, err := control.NewStore(statePath).UpdateContext(context.Background(), func(s *control.State) { s.LiveBlueprints = records }); err != nil {
		t.Fatal(err)
	}
	b := bootSMLive(t, cfg, statePath, smBlueprint(t))
	skipped := map[string]bool{}
	for _, skip := range b.r.LiveRestoreSkips() {
		skipped[skip.Blueprint] = true
	}
	if !skipped["synthetic/live-empty"] || !skipped["synthetic/live-probe"] || len(skipped) != 2 {
		t.Fatalf("restore skips = %v, want the zero-check record and the record past the limit", skipped)
	}
	if jobs := publishedCheckJobs(t, cfg); strings.Join(jobs, ",") != "api-health" {
		t.Fatalf("boot snapshot checks = %v, want only the boot check", jobs)
	}
}
