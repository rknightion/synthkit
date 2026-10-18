// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/construct/sm"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/smstate"
	"github.com/rknightion/synthkit/internal/smstore"
)

// liveSMChangedYAML is liveSMYAML with the same check (same key) at another frequency.
const liveSMChangedYAML = `name: live-probe
environments:
  - name: prod
    weight: 1.0
features:
  synthetic_monitoring:
    enabled: true
    checks:
      - {name: live-probe-health, target: "https://live.example.com/health", frequency: 120000}
`

// smProcess drives one backend's composed SM process: start is a restart (file) or a leader
// failover (kubernetes), and emitting reports, per running blueprint, whether its SM is admitted
// with registration (false when stripped).
type smProcess interface {
	start(t *testing.T) smHandoff
	apply(t *testing.T, lb control.LiveBlueprint) error
	remove(t *testing.T, name string)
	register(t *testing.T)
	pending(t *testing.T)
	emitting(t *testing.T) map[string]bool
}

func smProcesses() map[string]func(t *testing.T) smProcess {
	return map[string]func(t *testing.T) smProcess{
		"file": func(t *testing.T) smProcess {
			cfg, path := smLiveConfig(t)
			return &fileSMProcess{cfg: cfg, path: path}
		},
		"kubernetes": func(t *testing.T) smProcess { return &kubeSMProcess{env: newKubeSMEnv(t)} },
	}
}

// pendingJournal is a valid provisioner create journal for the published snapshot's first check.
func pendingJournal(s smstate.Snapshot) smstate.Journal {
	raw := json.RawMessage(`{"job":"api-health"}`)
	return smstate.Journal{SchemaVersion: smstate.SchemaVersion, Operation: "create", Kind: "check", Key: s.Checks[0].Key, SnapshotHash: s.SnapshotHash, ExpectedHash: smstate.SpecHash(raw), ExpectedSpec: raw, StartedUnixMS: 1}
}

type fileSMProcess struct {
	cfg  *config.Config
	path string
	b    *smLiveBoot
}

func (p *fileSMProcess) start(t *testing.T) smHandoff {
	p.b = bootSMLive(t, p.cfg, p.path, smBlueprint(t))
	return p.b.handoff
}
func (p *fileSMProcess) apply(_ *testing.T, lb control.LiveBlueprint) error { return p.b.apply(lb) }
func (p *fileSMProcess) remove(t *testing.T, name string) {
	t.Helper()
	if err := p.b.r.RemoveLiveBlueprint(name, true, func(bool) error {
		_, err := p.b.store.UpdateContext(context.Background(), func(s *control.State) { s.LiveBlueprints = withoutLive(s.LiveBlueprints, name) })
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
func (p *fileSMProcess) register(t *testing.T) { registerPublishedSnapshot(t, p.cfg) }
func (p *fileSMProcess) pending(t *testing.T) {
	t.Helper()
	dir := smstate.RuntimeDir(p.cfg.SnapshotPath)
	snapshot, err := smstate.ReadSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := smstate.WriteJournal(dir, pendingJournal(snapshot)); err != nil {
		t.Fatal(err)
	}
}

// emitting ticks once: every admitted check must emit sm_check_info with the registration's
// config_version, and no check of a stripped blueprint may emit.
func (p *fileSMProcess) emitting(t *testing.T) map[string]bool {
	t.Helper()
	versions := p.b.checkVersions()
	var registration *smstate.Registration
	out := map[string]bool{}
	for _, res := range p.b.r.RunningResolved() {
		out[res.Name] = registeredSM(res)
		for _, instance := range res.Constructs {
			if instance.Kind != sm.Kind {
				continue
			}
			if !registeredSM(res) {
				t.Fatalf("blueprint %q runs SM without registration", res.Name)
			}
			if registration == nil {
				r := mustRegistration(t, p.cfg)
				registration = &r
			}
			for _, check := range instance.Config.(*sm.Config).Checks {
				spec := sm.ResolveSpec(check)
				resource, _ := smstate.ResourceByKey(*registration, "check", smstate.CheckKey(spec.Job, spec.Target))
				if got, ok := versions[spec.Job]; !ok || got != resource.ConfigVersion {
					t.Fatalf("admitted check %s emitted config_version %q, want registered %q", spec.Job, got, resource.ConfigVersion)
				}
				delete(versions, spec.Job)
			}
		}
	}
	if len(versions) != 0 {
		t.Fatalf("checks emitted without an admitted SM construct: %v", versions)
	}
	return out
}

func withoutLive(set []control.LiveBlueprint, name string) []control.LiveBlueprint {
	kept := set[:0]
	for _, lb := range set {
		if lb.Name != name {
			kept = append(kept, lb)
		}
	}
	return kept
}

type kubeSMProcess struct {
	env *kubeSMEnv
	v   *haView
}

func (p *kubeSMProcess) start(t *testing.T) smHandoff {
	p.v, _ = p.env.activate(t)
	return p.v.smState
}
func (p *kubeSMProcess) apply(_ *testing.T, lb control.LiveBlueprint) error {
	return p.v.runner.ApplyLiveBlueprint(lb, func() error {
		_, err := p.v.store.UpdateContext(context.Background(), func(s *control.State) { s.LiveBlueprints = append(s.LiveBlueprints, lb) })
		return err
	})
}
func (p *kubeSMProcess) remove(t *testing.T, name string) {
	t.Helper()
	if err := p.v.runner.RemoveLiveBlueprint(name, true, func(bool) error {
		_, err := p.v.store.UpdateContext(context.Background(), func(s *control.State) { s.LiveBlueprints = withoutLive(s.LiveBlueprints, name) })
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
func (p *kubeSMProcess) register(t *testing.T) { registerKube(t, p.env.smStore(t)) }
func (p *kubeSMProcess) pending(t *testing.T) {
	t.Helper()
	store := p.env.smStore(t)
	doc, err := store.Load(context.Background())
	if err != nil || doc.Snapshot == nil {
		t.Fatalf("published snapshot: %v", err)
	}
	journal := pendingJournal(*doc.Snapshot)
	if err := store.UpdateProvision(context.Background(), smstore.Binding(*doc.Snapshot), func(s *smstore.ProvisionState) error { s.Pending = &journal; return nil }); err != nil {
		t.Fatal(err)
	}
}
func (p *kubeSMProcess) emitting(*testing.T) map[string]bool {
	out := map[string]bool{}
	for _, res := range p.v.runner.RunningResolved() {
		out[res.Name] = registeredSM(res)
	}
	return out
}

func wantEmitting(t *testing.T, p smProcess, step string, want map[string]bool) {
	t.Helper()
	if got := p.emitting(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: registered SM per running blueprint = %v, want %v", step, got, want)
	}
}

// A live SM apply the registration does not cover strips only that blueprint: across restarts
// (file) and leader failovers (kubernetes) every blueprint whose registered checks are unchanged
// keeps emitting. A probe conflict is still rejected, and a pending journal still strips all SM.
func TestLiveSMPerBlueprintApplyKeepsOthersRegistered(t *testing.T) {
	for name, setup := range smProcesses() {
		t.Run(name, func(t *testing.T) {
			p := setup(t)
			p.start(t)
			if err := p.apply(t, liveSMRecord("live-probe", liveSMYAML)); err != nil {
				t.Fatal(err)
			}
			p.register(t)
			p.start(t)
			wantEmitting(t, p, "registered set", map[string]bool{"sm-test": true, "synthetic/live-probe": true})

			if err := p.apply(t, liveSMRecord("live-other", liveSMOtherYAML)); err != nil {
				t.Fatalf("uncovered live SM apply = %v, want admitted stripped", err)
			}
			wantEmitting(t, p, "after the uncovered apply", map[string]bool{"sm-test": true, "synthetic/live-probe": true, "synthetic/live-other": false})
			for i := range 2 {
				// The lane is not verified while a covered declaration (live-other) is unregistered.
				if h := p.start(t); !h.Declared || h.Registered {
					t.Fatalf("start %d after the uncovered apply: boot handoff %+v, want declared, not all registered", i+1, h)
				}
				wantEmitting(t, p, "restart after the uncovered apply", map[string]bool{"sm-test": true, "synthetic/live-probe": true, "synthetic/live-other": false})
			}

			err := p.apply(t, liveSMRecord("live-moved", liveSMConflictYAML))
			if !errors.Is(err, control.ErrLiveCollision) || !strings.Contains(err.Error(), `"`+sm.DefaultProbeName+`"`) {
				t.Fatalf("conflicting probe apply = %v, want ErrLiveCollision naming probe %q", err, sm.DefaultProbeName)
			}
			p.start(t)
			wantEmitting(t, p, "restart after the rejected conflict", map[string]bool{"sm-test": true, "synthetic/live-probe": true, "synthetic/live-other": false})

			p.pending(t)
			if h := p.start(t); h.Registered {
				t.Fatalf("boot handoff with a pending journal = %+v, want unregistered", h)
			}
			wantEmitting(t, p, "pending journal", map[string]bool{"sm-test": false, "synthetic/live-probe": false, "synthetic/live-other": false})
		})
	}
}

// A DELETE of a live SM blueprint leaves the others registered across restart and failover; a
// re-apply of it with a changed check never emits that check, while the others keep emitting.
func TestLiveSMPerBlueprintDeleteAndChange(t *testing.T) {
	for name, setup := range smProcesses() {
		t.Run(name, func(t *testing.T) {
			p := setup(t)
			p.start(t)
			if err := p.apply(t, liveSMRecord("live-probe", liveSMYAML)); err != nil {
				t.Fatal(err)
			}
			p.register(t)
			p.start(t)
			wantEmitting(t, p, "registered set", map[string]bool{"sm-test": true, "synthetic/live-probe": true})

			p.remove(t, "synthetic/live-probe")
			for range 2 {
				if h := p.start(t); !h.Declared || !h.Registered {
					t.Fatalf("restart after the DELETE: boot handoff %+v, want registered", h)
				}
				wantEmitting(t, p, "restart after the DELETE", map[string]bool{"sm-test": true})
			}

			if err := p.apply(t, liveSMRecord("live-probe", liveSMChangedYAML)); err != nil {
				t.Fatalf("changed live SM apply = %v, want admitted stripped", err)
			}
			wantEmitting(t, p, "after the changed apply", map[string]bool{"sm-test": true, "synthetic/live-probe": false})
			p.start(t)
			wantEmitting(t, p, "restart after the changed apply", map[string]bool{"sm-test": true, "synthetic/live-probe": false})
		})
	}
}

// Rotating the SM target or credential strips every blueprint, even one whose checks are
// unchanged and registered in a retained snapshot: the registration belongs to another target.
func TestLiveSMPerBlueprintTargetRotationStripsAll(t *testing.T) {
	cfg, path := smLiveConfig(t)
	p := &fileSMProcess{cfg: cfg, path: path}
	p.start(t)
	if err := p.apply(t, liveSMRecord("live-probe", liveSMYAML)); err != nil {
		t.Fatal(err)
	}
	p.register(t)
	p.start(t)
	if err := p.apply(t, liveSMRecord("live-other", liveSMOtherYAML)); err != nil {
		t.Fatal(err)
	}
	p.start(t)
	wantEmitting(t, p, "retained registration", map[string]bool{"sm-test": true, "synthetic/live-probe": true, "synthetic/live-other": false})
	cfg.SMToken = "rotated"
	p.start(t)
	wantEmitting(t, p, "rotated credential", map[string]bool{"sm-test": false, "synthetic/live-probe": false, "synthetic/live-other": false})
}
