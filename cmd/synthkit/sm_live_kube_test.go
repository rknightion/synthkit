// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/construct/sm"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/smstate"
	"github.com/rknightion/synthkit/internal/smstore"
	"github.com/rknightion/synthkit/internal/smstore/smstoretest"
)

// kubeSMEnv is the kubernetes-backend HA configuration of TestSMRealRunnerBoundSpecMatrix, with
// sm-test as the selected boot blueprint and a fake API serving the SM, control and boot objects.
type kubeSMEnv struct {
	cfg *config.Config
	api *smstoretest.API
}

func newKubeSMEnv(t *testing.T) *kubeSMEnv {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	remote := smstoretest.NewRemote(t)
	t.Cleanup(remote.Close)
	cfg, err := config.Load(filepath.Join(root, "absent.env"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.SnapshotPath, cfg.SMURL, cfg.SMToken = filepath.Join(root, "control.json"), remote.Server.URL, smstoretest.Token(t)
	cfg.DryRun, cfg.SelfObsEnabled = true, false
	cfg.FMURL, cfg.ControlToken, cfg.SigilEndpoint, cfg.FaroCollector, cfg.ProfilesURL = "", "", "", "", ""
	cfg.StateCASMaxAttempts = 5
	cfg.StateGitSourceConfigMaps = map[string]string{}
	cfg.BlueprintsDir, cfg.BlueprintDataDir = filepath.Join(root, "baked"), filepath.Join(root, "absent-data")
	if err := os.Mkdir(cfg.BlueprintsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg.StateBackend, cfg.HANamespace, cfg.StateSMConfigMap, cfg.SMProvisionLeaseName, cfg.StateControlConfigMap, cfg.StateBootConfigMap = "kubernetes", "test", "sm", "provision", "control", "boot"
	cfg.HAMode = "lease"
	cfg.BlueprintNames = []string{"sm-test"}
	if err := os.WriteFile(filepath.Join(cfg.BlueprintsDir, "sm.yaml"), []byte(smRuntimeBlueprintYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	a := smstoretest.New(t, "sm", "control", "boot")
	t.Cleanup(a.Close)
	a.Permit()
	return &kubeSMEnv{cfg: cfg, api: a}
}

func (e *kubeSMEnv) backend(t *testing.T, g ha.LeaderGate) control.StateBackend {
	t.Helper()
	b, err := control.NewKubernetesBackendForClient(control.KubernetesBackendOptions{Gate: g, Namespace: "test", Objects: map[control.Key]string{control.SMState: "sm", control.Control: "control", control.BootManifest: "boot"}, RequestTimeout: 2 * time.Second, MaxDocumentBytes: control.MaxStateDocumentBytes}, e.api.Server.URL, e.api.Server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (e *kubeSMEnv) smStore(t *testing.T) smstore.Store {
	t.Helper()
	store, err := configuredSMStore(e.cfg, e.backend(t, ha.AlwaysLeader{}))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// activate acquires leadership the way the HA loop does: the leader view is built, its SM handoff
// published and its persisted live set restored inside gate.Activate's prepare step.
func (e *kubeSMEnv) activate(t *testing.T) (*haView, *ha.Gate) {
	t.Helper()
	gate := ha.NewGate()
	backend := e.backend(t, gate)
	bound := ha.Bounded{Gate: gate, Timeout: 8 * time.Second, Margin: 2 * time.Second, Crash: func() { t.Error("unexpected crash") }}
	var v *haView
	if err := gate.Activate(context.Background(), func(ctx context.Context) error {
		var err error
		v, err = newHAView(ctx, e.cfg, gate, bound, true, backend)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return v, gate
}

// registerKube stands in for a completed sm-provision apply of the published snapshot.
func registerKube(t *testing.T, store smstore.Store) {
	t.Helper()
	doc, err := store.Load(context.Background())
	if err != nil || doc.Snapshot == nil {
		t.Fatalf("published snapshot: %v", err)
	}
	snapshot := *doc.Snapshot
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
	if err := store.UpdateProvision(context.Background(), smstore.Binding(snapshot), func(p *smstore.ProvisionState) error { p.Registration = &registration; return nil }); err != nil {
		t.Fatal(err)
	}
}

func registeredSM(res *blueprint.Resolved) bool {
	for _, instance := range res.Constructs {
		if instance.Kind == sm.Kind {
			return instance.Config.(*sm.Config).RequireRegistration
		}
	}
	return false
}

// On the kubernetes HA leader the restore runs inside gate.Activate's prepare step, before the
// gate admits leader mutations. It must not publish (the boot handoff already published the
// covering snapshot): it loads and validates, so persisted live SM records are restored, and once
// the provisioner registers the set the next activation restores them registered.
func TestLiveSMKubernetesLeaderRestore(t *testing.T) {
	env := newKubeSMEnv(t)
	seed, err := control.NewBackendStore(context.Background(), env.backend(t, ha.AlwaysLeader{}), ha.AlwaysLeader{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.UpdateContext(context.Background(), func(s *control.State) {
		s.LiveBlueprints = []control.LiveBlueprint{liveSMRecord("live-probe", liveSMYAML)}
	}); err != nil {
		t.Fatal(err)
	}
	first, _ := env.activate(t)
	if skips := first.runner.LiveRestoreSkips(); len(skips) != 0 {
		t.Fatalf("kubernetes HA leader skipped the persisted live SM record: %+v", skips)
	}
	if err := first.runner.ApplyLiveBlueprint(liveSMRecord("live-other", liveSMOtherYAML), func() error {
		_, err := first.store.UpdateContext(context.Background(), func(s *control.State) {
			s.LiveBlueprints = append(s.LiveBlueprints, liveSMRecord("live-other", liveSMOtherYAML))
		})
		return err
	}); err != nil {
		t.Fatalf("leader live SM apply = %v", err)
	}
	store := env.smStore(t)
	registerKube(t, store)

	second, _ := env.activate(t)
	if !second.smState.Registered {
		t.Fatalf("second activation handoff = %+v, want registered", second.smState)
	}
	if skips := second.runner.LiveRestoreSkips(); len(skips) != 0 {
		t.Fatalf("second activation skipped records: %+v", skips)
	}
	got := map[string]bool{}
	for _, res := range second.runner.RunningResolved() {
		got[res.Name] = registeredSM(res)
	}
	want := map[string]bool{"sm-test": true, "synthetic/live-probe": true, "synthetic/live-other": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registered SM per running blueprint = %v, want %v", got, want)
	}
}

// A live SM apply that changes the snapshot while a provisioner mutation is pending is refused
// with ErrLiveSMBusy and writes nothing, so the provisioner's bound writes still complete.
func TestLiveSMApplyRefusedWhileProvisionPending(t *testing.T) {
	env := newKubeSMEnv(t)
	store := env.smStore(t)
	boot := smBlueprint(t)
	admit := newSMAdmission(env.cfg, version, store, true, false, nil)
	if _, err := admit.prepareBoot(context.Background(), []*blueprint.Resolved{boot}, nil, runner.Catalog(), blueprint.RuntimeLimits{}); err != nil {
		t.Fatal(err)
	}
	doc, err := store.Load(context.Background())
	if err != nil || doc.Snapshot == nil {
		t.Fatalf("boot snapshot: %v", err)
	}
	binding := smstore.Binding(*doc.Snapshot)
	raw := json.RawMessage(`{"job":"api-health"}`)
	journal := smstate.Journal{SchemaVersion: smstate.SchemaVersion, Operation: "create", Kind: "check", Key: doc.Snapshot.Checks[0].Key, SnapshotHash: doc.Snapshot.SnapshotHash, ExpectedHash: smstate.SpecHash(raw), ExpectedSpec: raw, StartedUnixMS: 1}
	if err := store.UpdateProvision(context.Background(), binding, func(p *smstore.ProvisionState) error { p.Pending = &journal; return nil }); err != nil {
		t.Fatal(err)
	}
	before := env.api.Data("sm")

	candidate := mustLoadNS(t, []byte(liveSMYAML), "synthetic")
	err = admit.Admit(runner.LiveAdmitRequest{Beside: []string{"sm-test"}, Candidate: candidate})
	if !errors.Is(err, control.ErrLiveSMBusy) {
		t.Fatalf("live SM apply during a pending provision = %v, want ErrLiveSMBusy", err)
	}
	if after := env.api.Data("sm"); string(after) != string(before) {
		t.Fatal("refused live apply rewrote the SM state document")
	}
	// The provisioner's next bound write (here: completing the mutation) still sees its binding.
	if err := store.UpdateProvision(context.Background(), binding, func(p *smstore.ProvisionState) error {
		if p.Pending == nil || !reflect.DeepEqual(*p.Pending, journal) {
			t.Fatalf("pending journal changed: %+v", p.Pending)
		}
		p.Pending = nil
		return nil
	}); err != nil {
		t.Fatalf("provisioner completion after a refused live apply = %v", err)
	}
	// With nothing pending the same apply publishes; repeating it is a no-write.
	if err := admit.Admit(runner.LiveAdmitRequest{Beside: []string{"sm-test"}, Candidate: mustLoadNS(t, []byte(liveSMYAML), "synthetic")}); err != nil {
		t.Fatalf("live SM apply after the provisioner completed = %v", err)
	}
	published := env.api.Data("sm")
	if string(published) == string(before) {
		t.Fatal("idle live SM apply did not publish its snapshot")
	}
	if err := admit.Admit(runner.LiveAdmitRequest{Beside: []string{"sm-test"}, Candidate: mustLoadNS(t, []byte(liveSMYAML), "synthetic")}); err != nil {
		t.Fatal(err)
	}
	if string(env.api.Data("sm")) != string(published) {
		t.Fatal("an unchanged snapshot was written again")
	}
}
