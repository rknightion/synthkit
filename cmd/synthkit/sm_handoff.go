// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/construct/sm"
	"github.com/rknightion/synthkit/internal/control"
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
	if cfg.HAMode == "lease" && cfg.StateBackend != "kubernetes" {
		handoff := smHandoff{}
		for _, bp := range resolved {
			kept := bp.Constructs[:0]
			for _, instance := range bp.Constructs {
				if instance.Kind == sm.Kind {
					handoff.Declared = true
					continue
				}
				kept = append(kept, instance)
			}
			bp.Constructs = kept
		}
		return handoff, nil // no artifact writes/deletes and no unregistered HA SM emission
	}
	var probes []smstate.ProbeSpec
	var checks []smstate.CheckSpec
	probeSeen := map[string]smstate.ProbeSpec{}
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
				if err := sm.ValidateLocation(declared); err != nil {
					return smHandoff{}, fmt.Errorf("synthetic monitoring location in blueprint %q: %w", bp.Name, err)
				}
				spec := sm.ResolveSpec(declared)
				probeKey := smstate.ProbeKey(spec.Probe, spec.Region)
				probe := smstate.ProbeSpec{
					Key: probeKey, Name: spec.Probe, Region: spec.Region,
					Latitude: spec.Latitude, Longitude: spec.Longitude,
				}
				if prior, seen := probeSeen[spec.Probe]; seen {
					if prior != probe {
						return smHandoff{}, fmt.Errorf("synthetic monitoring probe %q has conflicting region or location declarations", spec.Probe)
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
				checks = append(checks, smstate.CheckSpec{
					Key: smstate.CheckKey(spec.Job, spec.Target), Source: bp.Name,
					Job: spec.Job, Target: spec.Target, FrequencyMS: spec.FrequencyMs,
					TimeoutMS: 3000, ProbeKey: probeKey, Labels: labels,
				})
			}
		}
	}
	if len(checks) == 0 {
		if publish && store != nil {
			if err := store.PublishSnapshot(ctx, nil); err != nil {
				return smHandoff{}, errors.New("synthetic monitoring snapshot could not be invalidated")
			}
		}
		return smHandoff{}, nil
	}
	handoff := smHandoff{Declared: true}
	if store == nil {
		return handoff, errors.New("synthetic monitoring requires STATE_SM_CONFIGMAP and a dedicated provisioning Lease with Kubernetes state")
	}
	snapshot, err := smstate.NewSnapshot(probes, checks, cfg.SMURL, cfg.SMToken, sourceVersion, now)
	if err != nil {
		return handoff, err
	}
	if publish {
		if err := store.PublishSnapshot(ctx, &snapshot); err != nil {
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
