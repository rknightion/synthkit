// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"errors"
	"sort"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/construct/sm"
	"github.com/rknightion/synthkit/internal/smstate"
)

type smHandoff struct {
	Declared   bool
	Registered bool
}

// prepareSMHandoff writes the exact current-boot snapshot and suppresses every SM construct unless
// a private registration artifact matches that snapshot, target fingerprint, and source version.
// It runs before runner construction; the construct receives only config-version strings.
func prepareSMHandoff(resolved []*blueprint.Resolved, cfg *config.Config, sourceVersion string, now time.Time) (smHandoff, error) {
	var probes []smstate.ProbeSpec
	var checks []smstate.CheckSpec
	probeSeen := map[string]bool{}
	for _, bp := range resolved {
		for _, instance := range bp.Constructs {
			if instance.Kind != sm.Kind {
				continue
			}
			smCfg, ok := instance.Config.(*sm.Config)
			if !ok {
				return smHandoff{}, errors.New("synthetic monitoring configuration is invalid")
			}
			for _, declared := range smCfg.Checks {
				spec := sm.ResolveSpec(declared)
				probeKey := smstate.ProbeKey(spec.Probe, spec.Region)
				if !probeSeen[probeKey] {
					probes = append(probes, smstate.ProbeSpec{
						Key: probeKey, Name: spec.Probe, Region: spec.Region,
						Latitude: sm.DefaultProbeLat, Longitude: sm.DefaultProbeLon,
					})
					probeSeen[probeKey] = true
				}
				labels := make([]smstate.Label, 0, len(spec.Labels))
				for key, value := range spec.Labels {
					labels = append(labels, smstate.Label{Name: key, Value: value})
				}
				sort.Slice(labels, func(i, j int) bool { return labels[i].Name < labels[j].Name })
				checks = append(checks, smstate.CheckSpec{
					Key: smstate.CheckKey(spec.Job, spec.Target), Source: bp.Name,
					Job: spec.Job, Target: spec.Target, FrequencyMS: spec.FrequencyMs,
					TimeoutMS: 3000, ProbeKey: probeKey, Labels: labels,
				})
			}
		}
	}
	if len(checks) == 0 {
		if err := smstate.RemoveSnapshot(smstate.RuntimeDir(cfg.SnapshotPath)); err != nil {
			return smHandoff{}, errors.New("synthetic monitoring snapshot could not be invalidated")
		}
		return smHandoff{}, nil
	}
	handoff := smHandoff{Declared: true}
	snapshot, err := smstate.NewSnapshot(probes, checks, cfg.SMURL, cfg.SMToken, sourceVersion, now)
	if err != nil {
		return handoff, err
	}
	runtimeDir := smstate.RuntimeDir(cfg.SnapshotPath)
	if err := smstate.WriteSnapshot(runtimeDir, snapshot); err != nil {
		return handoff, errors.New("synthetic monitoring snapshot could not be written")
	}
	registration, err := smstate.ReadRegistration(runtimeDir, snapshot)
	if err != nil {
		// Missing, corrupt, stale, or mismatched state is one fail-closed outcome: no SM emitter.
		for _, bp := range resolved {
			filtered := bp.Constructs[:0]
			for _, instance := range bp.Constructs {
				if instance.Kind != sm.Kind {
					filtered = append(filtered, instance)
				}
			}
			bp.Constructs = filtered
		}
		return handoff, nil
	}
	for _, bp := range resolved {
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
