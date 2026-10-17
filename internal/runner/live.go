// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"fmt"
	"log"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/construct/sm"
	"github.com/rknightion/synthkit/internal/control"
)

// LiveRestoreSkip is one persisted live-applied blueprint that was not restored at boot because it
// no longer loads or now collides with the boot set (BLUEPRINT_NAMES takes precedence). Its
// persisted record is kept so the operator can see it and remove it with DELETE.
type LiveRestoreSkip struct {
	Blueprint string
	Reason    string
}

// LiveRestoreSkips reports the persisted live blueprints the boot restore skipped.
func (r *Runner) LiveRestoreSkips() []LiveRestoreSkip {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()
	return append([]LiveRestoreSkip(nil), r.restoreSkips...)
}

// prepareLive loads lb under its sanitized namespace and validates it against the running set
// without publishing anything. Errors wrap control.ErrLiveInvalid or control.ErrLiveCollision.
// Caller holds liveMu, so no other live mutation can change the set it validated against.
func (r *Runner) prepareLive(lb control.LiveBlueprint) (*bpRuntime, []ShapeWarning, error) {
	if lb.Namespace == "" {
		return nil, nil, fmt.Errorf("%w: blueprint %q has no namespace", control.ErrLiveInvalid, lb.Name)
	}
	res, err := blueprint.LoadNamespaced([]byte(lb.YAML), lb.Namespace, r.reg, blueprint.RuntimeLimits{
		MasterTick: r.opts.MasterTick, MaxDPMPerSeries: r.opts.MaxDPMPerSeries,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", control.ErrLiveInvalid, err)
	}
	if res.Name != lb.Name {
		return nil, nil, fmt.Errorf("%w: blueprint YAML name resolves to %q, want %q", control.ErrLiveInvalid, res.Name, lb.Name)
	}
	// Synthetic Monitoring admission (check registration and probe handoff) happens once at
	// startup in the composition root and fails closed there; a live apply has no such step, so
	// it refuses SM outright rather than emit check series no registration matches.
	for _, ci := range res.Constructs {
		if ci.Kind == sm.Kind {
			return nil, nil, fmt.Errorf("%w: blueprint %q declares a %s construct %q; Synthetic Monitoring is admitted only for boot blueprints", control.ErrLiveInvalid, lb.Name, sm.Kind, ci.Name)
		}
	}
	set := r.blueprintSet()
	liveCount := len(r.restoreSkips)
	for _, bp := range set {
		if bp.live {
			liveCount++
		}
	}
	if liveCount >= control.MaxLiveBlueprints {
		return nil, nil, fmt.Errorf("%w: live blueprint limit reached (%d)", control.ErrLiveCollision, control.MaxLiveBlueprints)
	}
	running := make([]*blueprint.Resolved, 0, len(set)+1)
	for _, bp := range set {
		if bp.name == lb.Name {
			kind := "boot"
			if bp.live {
				kind = "live-applied"
			}
			return nil, nil, fmt.Errorf("%w: blueprint identity collision: blueprint %q is already running (%s)", control.ErrLiveCollision, lb.Name, kind)
		}
		if bp.res != nil {
			running = append(running, bp.res)
		}
	}
	if err := blueprint.ValidateSet(append(running, res)); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", control.ErrLiveCollision, err)
	}
	bp, warnings, err := r.buildBlueprint(res)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", control.ErrLiveInvalid, err)
	}
	bp.live, bp.owner = true, lb.Owner
	return bp, warnings, nil
}

// ApplyLiveBlueprint implements control.LiveBlueprintRuntime. It validates and builds lb against
// the running set, calls persist, and only then publishes it, so every rejection (and a persist
// failure) leaves the running set unchanged with no partial lanes. A Run in progress starts the
// new blueprint's loop immediately; it emits from its next master tick.
func (r *Runner) ApplyLiveBlueprint(lb control.LiveBlueprint, persist func() error) error {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()
	bp, warnings, err := r.prepareLive(lb)
	if err != nil {
		return err
	}
	if persist != nil {
		if err := persist(); err != nil {
			return err
		}
	}
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	if err := r.commitBlueprint(bp, warnings); err != nil {
		// Unreachable under liveMu (prepareLive rejected duplicates); kept loud, never silent.
		return fmt.Errorf("%w: %v", control.ErrLiveCollision, err)
	}
	r.dropRestoreSkip(lb.Name)
	log.Printf("runner: live blueprint %q applied", lb.Name)
	return nil
}

// RemoveLiveBlueprint implements control.LiveBlueprintRuntime. A running live-applied blueprint,
// one the boot restore skipped, or any name the caller found in the persisted live set (recorded,
// e.g. orphaned by an ambiguous persist) is removable; boot blueprints return ErrLiveNotFound.
// persist runs first; then the blueprint leaves the running set and its loop is joined before
// return, so no later tick, inventory read or fleet reconcile sees its lanes or cumulative state.
func (r *Runner) RemoveLiveBlueprint(name string, recorded bool, persist func(forgetKnobs bool) error) error {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()
	var target *bpRuntime
	bootTwin := false
	for _, bp := range r.blueprintSet() {
		if bp.name == name && bp.live {
			target = bp
		}
		if bp.name == name && !bp.live {
			bootTwin = true
		}
	}
	skipped := false
	for _, s := range r.restoreSkips {
		if s.Blueprint == name {
			skipped = true
		}
	}
	if name == "" || (target == nil && !skipped && !recorded) {
		return fmt.Errorf("%w: %q", control.ErrLiveNotFound, name)
	}
	if persist != nil {
		if err := persist(!bootTwin); err != nil {
			return err
		}
	}
	r.dropRestoreSkip(name)
	if target == nil {
		return nil
	}
	r.bpMu.Lock()
	next := make([]*bpRuntime, 0, len(r.bps))
	for _, bp := range r.bps {
		if bp != target {
			next = append(next, bp)
		}
	}
	r.bps = next
	stop, done := target.stop, target.done
	r.bpMu.Unlock()
	if stop != nil {
		stop()
		<-done
	}
	// Its shape warnings leave with it, so repeated apply and remove cannot grow the list.
	r.shapeMu.Lock()
	kept := make([]ShapeWarning, 0, len(r.shapeWarnings))
	for _, w := range r.shapeWarnings {
		if w.Blueprint != name {
			kept = append(kept, w)
		}
	}
	r.shapeWarnings = kept
	r.shapeMu.Unlock()
	log.Printf("runner: live blueprint %q removed", name)
	return nil
}

func (r *Runner) dropRestoreSkip(name string) {
	kept := r.restoreSkips[:0]
	for _, s := range r.restoreSkips {
		if s.Blueprint != name {
			kept = append(kept, s)
		}
	}
	r.restoreSkips = append([]LiveRestoreSkip(nil), kept...)
}

// restoreLiveLocked re-applies the persisted live set after the boot set, in persisted order.
// Caller holds liveMu and applyMu (ApplyControl's first call).
func (r *Runner) restoreLiveLocked(entries []control.LiveBlueprint) {
	for _, lb := range entries {
		bp, warnings, err := r.prepareLive(lb)
		if err == nil {
			err = r.commitBlueprint(bp, warnings)
		}
		if err != nil {
			log.Printf("ERROR runner: skipping persisted live blueprint %q at restore: %v", lb.Name, err)
			r.restoreSkips = append(r.restoreSkips, LiveRestoreSkip{Blueprint: lb.Name, Reason: err.Error()})
			continue
		}
		log.Printf("runner: restored live blueprint %q", lb.Name)
	}
}

// RunningResolved returns the resolved declarations of every running blueprint (boot, restored and
// live-applied), for composition-root facts that must track the current set.
func (r *Runner) RunningResolved() []*blueprint.Resolved {
	set := r.blueprintSet()
	out := make([]*blueprint.Resolved, 0, len(set))
	for _, bp := range set {
		if bp.res != nil {
			out = append(out, bp.res)
		}
	}
	return out
}
