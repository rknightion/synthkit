// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/construct/sm"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/smstate"
	"github.com/rknightion/synthkit/internal/smstore"
)

type smHandoff struct {
	Declared   bool
	Registered bool
}

func configuredSMStore(cfg *config.Config, backend control.StateBackend) (smstore.Store, error) {
	if cfg.StateBackend != "kubernetes" {
		return smstore.NewFileStore(smstate.RuntimeDir(cfg.SnapshotPath)), nil
	}
	if cfg.StateSMConfigMap == "" {
		return nil, nil
	}
	return smstore.NewBackendStore(backend, cfg.StateCASMaxAttempts, cfg.SMProvisionLeaseName)
}

// prepareSMHandoff binds activation to this boot's specification/target/schema,
// not its release. Publication is explicitly authorized by the composition root.
// Constructs receive only validated authoritative config-version strings.
func prepareSMHandoff(ctx context.Context, resolved []*blueprint.Resolved, cfg *config.Config, sourceVersion string, now time.Time, store smstore.Store, publish bool) (smHandoff, error) {
	return admitSM(ctx, resolved, nil, cfg, sourceVersion, now, store, bootPublication(publish))
}

// smPublication is how one admission writes the snapshot it validates against.
type smPublication int

const (
	smValidateOnly smPublication = iota // read and validate only (standby, restore)
	smPublishBoot                       // unconditional boot publication, including invalidation
	smPublishLive                       // runtime CAS: refused while provisioning is pending, skipped when unchanged
)

func bootPublication(publish bool) smPublication {
	if publish {
		return smPublishBoot
	}
	return smValidateOnly
}

// smZeroCheckInstance reports an SM construct with no checks, which the runner cannot build.
func smZeroCheckInstance(bp *blueprint.Resolved) bool {
	for _, instance := range bp.Constructs {
		if smCfg, ok := instance.Config.(*sm.Config); ok && instance.Kind == sm.Kind && len(smCfg.Checks) == 0 {
			return true
		}
	}
	return false
}

// smDeclaration is one blueprint's declared Synthetic Monitoring checks, retained before a handoff
// strips or configures its constructs, so a later admission computes the same snapshot.
type smDeclaration struct {
	Blueprint string
	Checks    []sm.CheckConfig
}

// declaredSM returns bp's declared SM checks in construct order; ok reports whether it declares SM.
func declaredSM(bp *blueprint.Resolved) (decl smDeclaration, ok bool, err error) {
	decl.Blueprint = bp.Name
	for _, instance := range bp.Constructs {
		if instance.Kind != sm.Kind {
			continue
		}
		smCfg, valid := instance.Config.(*sm.Config)
		if !valid {
			return decl, true, &smDeclarationError{errors.New("synthetic monitoring configuration is invalid")}
		}
		ok = true
		decl.Checks = append(decl.Checks, smCfg.Checks...)
	}
	return decl, ok, nil
}

// smProbeConflictError names a probe whose region or coordinates disagree across declarations.
type smProbeConflictError struct{ Probe string }

func (e *smProbeConflictError) Error() string {
	return fmt.Sprintf("synthetic monitoring probe %q has conflicting region or location declarations", e.Probe)
}

// smDeclarationError is an SM declaration this runtime cannot admit (an invalid check or snapshot,
// or SM declared where no SM state is configured), as opposed to unavailable SM state.
type smDeclarationError struct{ err error }

func (e *smDeclarationError) Error() string { return e.err.Error() }
func (e *smDeclarationError) Unwrap() error { return e.err }

func stripSM(bp *blueprint.Resolved) {
	kept := bp.Constructs[:0]
	for _, instance := range bp.Constructs {
		if instance.Kind != sm.Kind {
			kept = append(kept, instance)
		}
	}
	bp.Constructs = kept
}

// buildSMSnapshot validates decls and returns their exact snapshot. declared reports whether any
// check is declared (an invalid check or a probe conflict counts); with none there is no snapshot.
func buildSMSnapshot(decls []smDeclaration, cfg *config.Config, sourceVersion string, now time.Time) (snapshot smstate.Snapshot, declared bool, err error) {
	var probes []smstate.ProbeSpec
	var checks []smstate.CheckSpec
	probeSeen := map[string]smstate.ProbeSpec{}
	for _, decl := range decls {
		for _, declared := range decl.Checks {
			if err := sm.ValidateCheck(declared); err != nil {
				return smstate.Snapshot{}, true, &smDeclarationError{fmt.Errorf("synthetic monitoring check in blueprint %q: %w", decl.Blueprint, err)}
			}
			spec := sm.ResolveSpec(declared)
			probeKey := smstate.ProbeKey(spec.Probe, spec.Region)
			probe := smstate.ProbeSpec{
				Key: probeKey, Name: spec.Probe, Region: spec.Region,
				Latitude: spec.Latitude, Longitude: spec.Longitude,
			}
			if prior, seen := probeSeen[spec.Probe]; seen {
				if prior != probe {
					return smstate.Snapshot{}, true, &smProbeConflictError{Probe: spec.Probe}
				}
			} else {
				probes = append(probes, probe)
				probeSeen[spec.Probe] = probe
			}
			labels := make([]smstate.Label, 0, len(spec.Labels))
			for key, value := range spec.Labels {
				labels = append(labels, smstate.Label{Name: key, Value: value})
			}
			sort.Slice(labels, func(i, j int) bool { return labels[i].Name < labels[j].Name })
			var dns *smstate.DNSSettings
			var tcp *smstate.TCPSettings
			if spec.DNS != nil {
				dns = &smstate.DNSSettings{Server: spec.DNS.Server}
			}
			if spec.TCP != nil {
				tcp = &smstate.TCPSettings{TLS: spec.TCP.TLS}
			}
			checks = append(checks, smstate.CheckSpec{
				Type: spec.Type, DNS: dns, TCP: tcp,
				Key: smstate.CheckKey(spec.Job, spec.Target), Source: decl.Blueprint,
				Job: spec.Job, Target: spec.Target, FrequencyMS: spec.FrequencyMs,
				TimeoutMS: spec.TimeoutMS, ProbeKey: probeKey, Labels: labels,
			})
		}
	}
	if len(checks) == 0 {
		return smstate.Snapshot{}, false, nil
	}
	snapshot, err = smstate.NewSnapshot(probes, checks, cfg.SMURL, cfg.SMToken, sourceVersion, now)
	if err != nil {
		return smstate.Snapshot{}, true, &smDeclarationError{err}
	}
	return snapshot, true, nil
}

// admitSM is the one fail-closed SM filter. The snapshot covers the declarations of targets plus
// extra (declarations that run beside targets but are not configured here); targets are stripped
// of SM unless the stored registration matches that exact snapshot, and are otherwise given it.
func admitSM(ctx context.Context, targets []*blueprint.Resolved, extra []smDeclaration, cfg *config.Config, sourceVersion string, now time.Time, store smstore.Store, publication smPublication) (smHandoff, error) {
	if cfg.HAMode == "lease" && cfg.StateBackend != "kubernetes" {
		handoff := smHandoff{}
		for _, bp := range targets {
			if _, ok, _ := declaredSM(bp); ok {
				handoff.Declared = true
			}
			stripSM(bp)
		}
		return handoff, nil // no artifact writes/deletes and no unregistered HA SM emission
	}
	decls := make([]smDeclaration, 0, len(targets)+len(extra))
	for _, bp := range targets {
		decl, _, err := declaredSM(bp)
		if err != nil {
			return smHandoff{}, err
		}
		decls = append(decls, decl)
	}
	decls = append(decls, extra...)
	snapshot, declared, err := buildSMSnapshot(decls, cfg, sourceVersion, now)
	if !declared {
		if publication == smPublishBoot && store != nil {
			if err := store.PublishSnapshot(ctx, nil); err != nil {
				return smHandoff{}, errors.New("synthetic monitoring snapshot could not be invalidated")
			}
		}
		return smHandoff{}, nil
	}
	if err != nil {
		return smHandoff{}, err
	}
	handoff := smHandoff{Declared: true}
	if store == nil {
		return handoff, &smDeclarationError{errors.New("synthetic monitoring requires STATE_SM_CONFIGMAP and a dedicated provisioning Lease with Kubernetes state")}
	}
	switch publication {
	case smPublishBoot:
		if err := store.PublishSnapshot(ctx, &snapshot); err != nil {
			return handoff, errors.New("synthetic monitoring snapshot could not be written")
		}
	case smPublishLive:
		if err := store.PublishSnapshotIfIdle(ctx, snapshot); errors.Is(err, smstore.ErrProvisionBusy) {
			return handoff, err
		} else if err != nil {
			return handoff, errors.New("synthetic monitoring snapshot could not be written")
		}
	}
	doc, err := store.Load(ctx)
	if err != nil {
		return handoff, errors.New("synthetic monitoring state is unavailable or invalid")
	}
	var registration smstate.Registration
	if doc.Provision.Registration != nil {
		registration = *doc.Provision.Registration
	}
	if doc.Provision.Pending != nil || smstate.ValidateRegistration(snapshot, registration) != nil {
		// Missing, corrupt, stale, or mismatched state is one fail-closed outcome: no SM emitter.
		for _, bp := range targets {
			stripSM(bp)
		}
		return handoff, nil
	}
	for _, bp := range targets {
		for i := range bp.Constructs {
			instance := &bp.Constructs[i]
			if instance.Kind != sm.Kind {
				continue
			}
			smCfg := instance.Config.(*sm.Config)
			smCfg.RequireRegistration = true
			smCfg.Registration = make(map[string]string, len(smCfg.Checks))
			for _, declared := range smCfg.Checks {
				spec := sm.ResolveSpec(declared)
				key := smstate.CheckKey(spec.Job, spec.Target)
				resource, ok := smstate.ResourceByKey(registration, "check", key)
				if !ok {
					return handoff, errors.New("synthetic monitoring registration is incomplete")
				}
				smCfg.Registration[sm.CheckKey(spec.Job, spec.Target)] = resource.ConfigVersion
			}
		}
	}
	handoff.Registered = true
	return handoff, nil
}

// smStartupDisabled reports whether STARTUP_DISABLED_KINDS lists synthetic_monitoring.
func smStartupDisabled(cfg *config.Config) bool {
	return slices.Contains(cfg.StartupDisabledKinds, sm.Kind)
}

// smAdmission carries the boot SM handoff's inputs into live admission, so a live-applied or
// restored blueprint passes through the same filter against the same declared set the boot
// snapshot covered. The runner calls Admit under its live-mutation lock; mu guards it anyway.
type smAdmission struct {
	mu        sync.Mutex
	cfg       *config.Config
	version   string
	store     smstore.Store
	publish   bool
	stripKind bool                                    // STARTUP_DISABLED_KINDS lists synthetic_monitoring
	bound     func(func(context.Context) error) error // publication/load boundary (HA: leader-fenced)
	now       func() time.Time
	boot      []smDeclaration          // boot blueprints' declarations, captured before the handoff
	bootNames map[string]bool          // every boot blueprint, SM or not
	live      map[string]smDeclaration // last admitted declaration per live blueprint name
}

func newSMAdmission(cfg *config.Config, version string, store smstore.Store, publish, stripKind bool, bound func(func(context.Context) error) error) *smAdmission {
	if bound == nil {
		bound = func(fn func(context.Context) error) error { return fn(context.Background()) }
	}
	return &smAdmission{cfg: cfg, version: version, store: store, publish: publish, stripKind: stripKind, bound: bound, now: time.Now, bootNames: map[string]bool{}, live: map[string]smDeclaration{}}
}

// prepareBoot runs the boot handoff over accepted, after the startup kinds were applied. Its
// snapshot also covers the persisted live set's declarations, which the runner restores beside
// the boot set, so a registration provisioned for the running set still matches after a restart.
// A persisted record that no longer loads, reuses a boot name, or cannot join the set is left out
// for the restore to skip.
func (a *smAdmission) prepareBoot(ctx context.Context, accepted []*blueprint.Resolved, persisted []control.LiveBlueprint, reg *core.Registry, limits blueprint.RuntimeLimits) (smHandoff, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, bp := range accepted {
		a.bootNames[bp.Name] = true
		decl, ok, err := declaredSM(bp)
		if err != nil {
			return smHandoff{}, err
		}
		if ok {
			a.boot = append(a.boot, decl)
		}
	}
	var extra []smDeclaration
	if !a.stripKind {
		universe := append([]smDeclaration(nil), a.boot...)
		joined := append([]*blueprint.Resolved(nil), accepted...)
		for i, lb := range persisted {
			// The restore admits at most MaxLiveBlueprints records: every earlier record counts,
			// restored or skipped, so a later one is refused by the limit.
			if i >= control.MaxLiveBlueprints || a.bootNames[lb.Name] || lb.Namespace == "" {
				continue
			}
			res, err := blueprint.LoadNamespaced([]byte(lb.YAML), lb.Namespace, reg, limits)
			if err != nil || res.Name != lb.Name || blueprint.ValidateSet(append(joined, res)) != nil {
				continue // the restore skips it too (it no longer loads, or now collides)
			}
			decl, ok, err := declaredSM(res)
			if err != nil || smZeroCheckInstance(res) {
				continue // Admit rejects it, so the restore skips it
			}
			if !ok {
				joined = append(joined, res)
				continue
			}
			// A record that cannot join the set (invalid check, probe conflict, duplicate check)
			// stays out of the boot snapshot; its restore is then rejected by Admit and skipped.
			if _, _, err := buildSMSnapshot(append(universe, decl), a.cfg, a.version, a.now()); err != nil {
				continue
			}
			joined = append(joined, res)
			universe = append(universe, decl)
			a.live[lb.Name] = decl
			extra = append(extra, decl)
		}
	}
	return admitSM(ctx, accepted, extra, a.cfg, a.version, a.now(), a.store, bootPublication(a.publish))
}

// Admit is the runner's Options.LiveAdmit. A startup-disabled SM kind strips the candidate's SM
// exactly as the boot set's; a candidate without SM changes nothing. Otherwise the boot handoff
// runs again with the candidate as its only target, over the boot declarations plus the live
// declarations of every blueprint beside it, and strips SM unless the stored registration matches
// that exact snapshot. A live apply on a publishing process first publishes the snapshot through
// the runtime CAS (refused while provisioning is pending; no write when unchanged). The restore
// never publishes: the boot handoff already published the snapshot covering the persisted set, so
// it only loads and validates, with no leader-fenced mutation. An SM construct without checks, an
// invalid check (400), a probe conflict (409, naming the probe), busy or unavailable SM state
// (503) reject the candidate before it changes anything else.
func (a *smAdmission) Admit(req runner.LiveAdmitRequest) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	candidate := req.Candidate
	decl, ok, err := declaredSM(candidate)
	if err != nil {
		return fmt.Errorf("%w: blueprint %q: %v", control.ErrLiveInvalid, candidate.Name, err)
	}
	if !ok {
		delete(a.live, candidate.Name)
		return nil
	}
	if a.stripKind {
		stripSM(candidate)
		log.Printf("runner: live blueprint %q: synthetic_monitoring is a startup-disabled kind; its constructs are not run", candidate.Name)
		return nil
	}
	if smZeroCheckInstance(candidate) {
		return fmt.Errorf("%w: blueprint %q: a %s construct declares no checks", control.ErrLiveInvalid, candidate.Name, sm.Kind)
	}
	extra := append([]smDeclaration(nil), a.boot...)
	seen := map[string]bool{candidate.Name: true}
	for _, name := range req.Beside {
		if seen[name] || a.bootNames[name] {
			continue
		}
		seen[name] = true
		if other, ok := a.live[name]; ok {
			extra = append(extra, other)
		}
	}
	publication, bound := smValidateOnly, func(fn func(context.Context) error) error { return fn(context.Background()) }
	if a.publish && !req.Restore {
		publication, bound = smPublishLive, a.bound
	}
	var handoff smHandoff
	err = bound(func(ctx context.Context) error {
		var err error
		handoff, err = admitSM(ctx, []*blueprint.Resolved{candidate}, extra, a.cfg, a.version, a.now(), a.store, publication)
		return err
	})
	var conflict *smProbeConflictError
	var invalid *smDeclarationError
	switch {
	case errors.As(err, &conflict):
		return fmt.Errorf("%w: blueprint %q: %v", control.ErrLiveCollision, candidate.Name, err)
	case errors.As(err, &invalid):
		return fmt.Errorf("%w: blueprint %q: %v", control.ErrLiveInvalid, candidate.Name, err)
	case errors.Is(err, smstore.ErrProvisionBusy):
		return fmt.Errorf("%w: blueprint %q: a provisioning mutation is pending; retry after sm-provision completes", control.ErrLiveSMBusy, candidate.Name)
	case errors.Is(err, ha.ErrNotLeader):
		return fmt.Errorf("live blueprint %q: %w", candidate.Name, err) // 503 not_leader
	case err != nil:
		return fmt.Errorf("%w: blueprint %q: %v", control.ErrLiveSMUnavailable, candidate.Name, err)
	}
	a.live[candidate.Name] = decl
	if handoff.Declared && !handoff.Registered {
		log.Printf("runner: live blueprint %q: Synthetic Monitoring suppressed until the provisioner registers the published snapshot", candidate.Name)
	}
	return nil
}
