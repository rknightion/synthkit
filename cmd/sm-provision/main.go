// SPDX-License-Identifier: AGPL-3.0-only

// Command sm-provision reconciles the exact Synthetic Monitoring snapshot written by the
// version-matched synthkit emitter. It previews by default. Remote mutations require the explicit
// SM_PROVISION_APPLY=true gate; emitter DRY_RUN has no effect on this command.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/smstate"
)

var version = "dev"

type action string

const (
	actionCreate        action = "create"
	actionUpdate        action = "update"
	actionAdopt         action = "adopt"
	actionNoop          action = "noop"
	migrationPreviewTTL        = 15 * time.Minute
)

type probePlan struct {
	spec      smstate.ProbeSpec
	action    action
	remote    smProbe
	ownership smstate.Ownership
}

type checkPlan struct {
	spec      smstate.CheckSpec
	action    action
	remote    smCheck
	ownership smstate.Ownership
}

type plan struct {
	probes []probePlan
	checks []checkPlan
}

type provisioner struct {
	client              *smClient
	stateDir            string
	snapshot            smstate.Snapshot
	ownership           smstate.OwnershipLedger
	registration        smstate.Registration
	apply               bool
	adopt               bool
	migrate             bool
	migrationFrom       string
	migrationRetargeted bool
	now                 func() time.Time
	afterRetarget       func() error
	afterMutation       func(string) error
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sm-provision:", err)
		os.Exit(1)
	}
}

func run() error {
	apply, err := explicitBool("SM_PROVISION_APPLY")
	if err != nil {
		return err
	}
	adopt, err := explicitBool("SM_PROVISION_ADOPT_LEGACY")
	if err != nil {
		return err
	}
	migrate, err := explicitBool("SM_PROVISION_MIGRATE_TARGET")
	if err != nil {
		return err
	}
	if migrate && adopt {
		return errors.New("target migration and legacy adoption cannot be combined")
	}
	smURL, smToken := os.Getenv("GC_SM_URL"), os.Getenv("GC_SM_TOKEN")
	if smURL == "" || smToken == "" {
		return errors.New("GC_SM_URL and GC_SM_TOKEN are required")
	}
	snapshotPath := envDefault("CONFIG_SNAPSHOT_PATH", "./control-state.json")
	stateDir := smstate.RuntimeDir(snapshotPath)
	release, err := smstate.AcquireLock(stateDir)
	if err != nil {
		return errors.New("private state lock is unavailable")
	}
	defer func() { _ = release() }()
	if pendingStateExists(stateDir) {
		return errors.New("pending operation requires explicit operator review")
	}
	snapshot, err := smstate.ReadSnapshot(stateDir)
	if err != nil {
		return errors.New("private snapshot is unavailable or invalid")
	}
	if snapshot.TargetFingerprint != smstate.TargetFingerprint(smURL, smToken) {
		return errors.New("private snapshot target does not match configured target")
	}
	if snapshot.SourceVersion != version {
		return errors.New("provisioner version does not match private snapshot")
	}
	ownership := smstate.OwnershipLedger{
		SchemaVersion: smstate.SchemaVersion, TargetFingerprint: snapshot.TargetFingerprint,
	}
	migrationFrom := ""
	migrationRetargeted := false
	if current, readErr := smstate.ReadOwnershipState(stateDir); readErr == nil {
		if current.TargetFingerprint != snapshot.TargetFingerprint {
			if !migrate {
				return errors.New("ownership target migration requires SM_PROVISION_MIGRATE_TARGET=true")
			}
			migrationFrom = current.TargetFingerprint
		} else if marker, markerErr := smstate.ReadMigrationPreview(stateDir); markerErr == nil {
			if !migrate || marker.NewTargetFingerprint != snapshot.TargetFingerprint {
				return errors.New("target migration is incomplete; SM_PROVISION_MIGRATE_TARGET=true is required to resume")
			}
			migrationFrom = marker.OldTargetFingerprint
			migrationRetargeted = true
		} else if !errors.Is(markerErr, os.ErrNotExist) {
			return errors.New("private target migration state is invalid")
		} else if migrate {
			return errors.New("ownership target migration is not required")
		}
		ownership = current
	} else if errors.Is(readErr, os.ErrNotExist) {
		// One-time migration from the older snapshot-bound activation artifact. Its IDs remain
		// ownership evidence, but it never bypasses the emitter's exact snapshot validation.
		if legacy, legacyErr := smstate.OwnershipFromRegistration(stateDir, snapshot.TargetFingerprint); legacyErr == nil {
			ownership = legacy
		} else if !errors.Is(legacyErr, os.ErrNotExist) {
			return errors.New("private ownership state is invalid")
		}
	} else {
		return errors.New("private ownership state is invalid")
	}
	registration := smstate.Registration{
		SchemaVersion: smstate.SchemaVersion, SnapshotHash: snapshot.SnapshotHash,
		TargetFingerprint: snapshot.TargetFingerprint, SourceVersion: snapshot.SourceVersion,
	}
	p := &provisioner{
		client:   &smClient{base: strings.TrimRight(smURL, "/"), token: smToken, hc: &http.Client{Timeout: 30 * time.Second}},
		stateDir: stateDir, snapshot: snapshot, ownership: ownership, registration: registration,
		apply: apply, adopt: adopt, migrate: migrate, migrationFrom: migrationFrom,
		migrationRetargeted: migrationRetargeted, now: time.Now,
	}
	return p.execute(context.Background())
}

func (p *provisioner) execute(ctx context.Context) error {
	remoteProbes, err := p.client.listProbes(ctx)
	if err != nil {
		return errors.New("remote probe inventory is unavailable")
	}
	remoteChecks, err := p.client.listChecks(ctx)
	if err != nil {
		return errors.New("remote check inventory is unavailable")
	}
	planned, err := p.buildPlan(remoteProbes, remoteChecks)
	if err != nil {
		return err
	}
	var migrationResources []smstate.MigrationResource
	if p.migrate {
		migrationResources, err = p.migrationEvidence(remoteProbes, remoteChecks)
		if err != nil {
			return err
		}
		if !migrationPlanIsNoop(planned) {
			return errors.New("target migration requires an unchanged fully registered resource set; reconcile configuration separately")
		}
	}
	printPlan(planned, p.apply)
	if !p.apply {
		if p.migrate {
			if p.migrationRetargeted {
				return errors.New("target migration is already retargeted; rerun with SM_PROVISION_APPLY=true to resume")
			}
			if err := smstate.WriteMigrationPreview(p.stateDir, p.migrationPreview(planned, migrationResources)); err != nil {
				return errors.New("target migration preview could not be persisted")
			}
			fmt.Println("target migration preview recorded; rerun with SM_PROVISION_APPLY=true and the same snapshot")
		}
		if p.adopt && adoptionCount(planned) > 0 {
			if err := smstate.WriteAdoptionPreview(p.stateDir, p.adoptionPreview(planned)); err != nil {
				return errors.New("legacy adoption preview could not be persisted")
			}
			fmt.Println("legacy adoption preview recorded; rerun with SM_PROVISION_APPLY=true and the same snapshot")
		}
		return nil
	}
	if p.migrate {
		preview, err := smstate.ReadMigrationPreview(p.stateDir)
		expected := p.migrationPreview(planned, migrationResources)
		if err == nil {
			expected.WrittenUnixMS = preview.WrittenUnixMS
		}
		age := p.now().Sub(time.UnixMilli(preview.WrittenUnixMS))
		stale := !p.migrationRetargeted && (age < 0 || age > migrationPreviewTTL)
		if err != nil || stale || !reflect.DeepEqual(preview, expected) {
			return errors.New("target migration requires a current matching explicit preview before apply")
		}
		if !p.migrationRetargeted {
			p.ownership.TargetFingerprint = p.snapshot.TargetFingerprint
			p.ownership.WrittenUnixMS = p.now().UnixMilli()
			if err := smstate.WriteOwnership(p.stateDir, p.ownership); err != nil {
				return errors.New("target migration ownership retarget could not be persisted")
			}
			p.migrationRetargeted = true
			if p.afterRetarget != nil {
				if err := p.afterRetarget(); err != nil {
					return errors.New("target migration is incomplete; rerun apply to resume")
				}
			}
		}
	}
	if adoptionCount(planned) > 0 {
		preview, err := smstate.ReadAdoptionPreview(p.stateDir)
		expected := p.adoptionPreview(planned)
		expected.WrittenUnixMS = preview.WrittenUnixMS
		if err != nil || preview != expected {
			return errors.New("legacy adoption requires a matching explicit preview before apply")
		}
	}
	probeIDs := make(map[string]int64, len(planned.probes))
	for _, item := range planned.probes {
		resource, err := p.applyProbe(ctx, item)
		if err != nil {
			return err
		}
		probeIDs[item.spec.Key] = resource.ID
	}
	for _, item := range planned.checks {
		if _, err := p.applyCheck(ctx, item, probeIDs); err != nil {
			return err
		}
	}
	if err := smstate.ValidateRegistration(p.snapshot, p.registration); err != nil {
		return errors.New("provisioning did not produce a complete registration")
	}
	if err := smstate.RemoveAdoptionPreview(p.stateDir); err != nil {
		return errors.New("legacy adoption preview could not be cleared")
	}
	if p.migrate {
		if err := smstate.RemoveMigrationPreview(p.stateDir); err != nil {
			return errors.New("target migration preview could not be cleared")
		}
	}
	fmt.Println("apply complete; restart synthkit to activate the matching registration")
	return nil
}

func (p *provisioner) migrationEvidence(remoteProbes []smProbe, remoteChecks []smCheck) ([]smstate.MigrationResource, error) {
	probesByID := make(map[int64]smProbe, len(remoteProbes))
	for _, remote := range remoteProbes {
		probesByID[remote.ID] = remote
	}
	checksByID := make(map[int64]smCheck, len(remoteChecks))
	for _, remote := range remoteChecks {
		checksByID[remote.ID] = remote
	}
	resources := make([]smstate.MigrationResource, 0, len(p.ownership.Resources))
	for _, owned := range p.ownership.Resources {
		var specHash string
		var modified float64
		switch owned.Kind {
		case "probe":
			remote, ok := probesByID[owned.ID]
			if !ok || smstate.ProbeKey(remote.Name, remote.Region) != owned.Key || remote.Modified <= 0 {
				return nil, errors.New("target migration owned remote probe is missing or changed")
			}
			specHash = smstate.SpecHash(managedProbeEvidence(remote))
			modified = remote.Modified
		case "check":
			remote, ok := checksByID[owned.ID]
			if !ok || smstate.CheckKey(remote.Job, remote.Target) != owned.Key || remote.Modified <= 0 {
				return nil, errors.New("target migration owned remote check is missing or changed")
			}
			specHash = smstate.SpecHash(managedCheckEvidence(remote))
			modified = remote.Modified
		default:
			return nil, errors.New("target migration ownership state is invalid")
		}
		if owned.SpecHash == "" || owned.SpecHash != specHash || smstate.ConfigVersion(owned.Modified) != smstate.ConfigVersion(modified) {
			return nil, errors.New("target migration owned remote resource revision or specification changed")
		}
		resources = append(resources, smstate.MigrationResource{
			Kind: owned.Kind, Key: owned.Key, ID: owned.ID,
			Modified: smstate.ConfigVersion(modified), Ownership: owned.Ownership, SpecHash: specHash,
		})
	}
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Kind == resources[j].Kind {
			return resources[i].Key < resources[j].Key
		}
		return resources[i].Kind < resources[j].Kind
	})
	return resources, nil
}

func managedProbeEvidence(remote smProbe) any {
	return struct {
		Name      string  `json:"name"`
		Public    bool    `json:"public"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Region    string  `json:"region"`
	}{remote.Name, remote.Public, remote.Latitude, remote.Longitude, remote.Region}
}

func managedCheckEvidence(remote smCheck) any {
	labels := append([]smLabel(nil), remote.Labels...)
	probes := append([]int64(nil), remote.Probes...)
	sort.Slice(labels, func(i, j int) bool {
		if labels[i].Name == labels[j].Name {
			return labels[i].Value < labels[j].Value
		}
		return labels[i].Name < labels[j].Name
	})
	sort.Slice(probes, func(i, j int) bool { return probes[i] < probes[j] })
	return struct {
		Job              string            `json:"job"`
		Target           string            `json:"target"`
		Frequency        int               `json:"frequency"`
		Timeout          int               `json:"timeout"`
		Enabled          bool              `json:"enabled"`
		Probes           []int64           `json:"probes"`
		Labels           []smLabel         `json:"labels"`
		AlertSensitivity string            `json:"alert_sensitivity"`
		BasicMetricsOnly bool              `json:"basic_metrics_only"`
		HTTP             map[string]string `json:"http"`
	}{
		remote.Job, remote.Target, remote.Frequency, remote.Timeout, remote.Enabled, probes, labels,
		remote.AlertSensitivity, remote.BasicMetricsOnly, managedHTTPSettings(remote.Settings),
	}
}

func (p *provisioner) buildPlan(remoteProbes []smProbe, remoteChecks []smCheck) (plan, error) {
	resources := registrationResources(p.ownership.Resources)
	probesByID := make(map[int64]smProbe, len(remoteProbes))
	probesByName := make(map[string][]smProbe)
	for _, remote := range remoteProbes {
		probesByID[remote.ID] = remote
		probesByName[remote.Name] = append(probesByName[remote.Name], remote)
	}
	checksByID := make(map[int64]smCheck, len(remoteChecks))
	checksByKey := make(map[string][]smCheck)
	for _, remote := range remoteChecks {
		checksByID[remote.ID] = remote
		checksByKey[smstate.CheckKey(remote.Job, remote.Target)] = append(checksByKey[smstate.CheckKey(remote.Job, remote.Target)], remote)
	}

	result := plan{}
	probeIDs := make(map[string]int64, len(p.snapshot.Probes))
	for _, spec := range p.snapshot.Probes {
		resource, owned := resources["probe\x00"+spec.Key]
		if owned {
			remote, ok := probesByID[resource.ID]
			if !ok || smstate.ProbeKey(remote.Name, remote.Region) != spec.Key {
				return plan{}, errors.New("owned remote probe is missing or changed")
			}
			act := actionUpdate
			if sameProbe(remote, desiredProbe(spec)) {
				act = actionNoop
			}
			result.probes = append(result.probes, probePlan{spec: spec, action: act, remote: remote, ownership: resource.Ownership})
			probeIDs[spec.Key] = remote.ID
			continue
		}
		candidates := probesByName[spec.Name]
		if len(candidates) > 0 {
			if len(candidates) == 1 && p.adopt && sameProbe(candidates[0], desiredProbe(spec)) && candidates[0].ID > 0 && candidates[0].Modified > 0 {
				result.probes = append(result.probes, probePlan{spec: spec, action: actionAdopt, remote: candidates[0], ownership: smstate.Adopted})
				probeIDs[spec.Key] = candidates[0].ID
				continue
			}
			return plan{}, errors.New("foreign remote probe collision")
		}
		result.probes = append(result.probes, probePlan{spec: spec, action: actionCreate, ownership: smstate.Owned})
	}

	for _, spec := range p.snapshot.Checks {
		resource, owned := resources["check\x00"+spec.Key]
		probeID := probeIDs[spec.ProbeKey]
		if owned {
			remote, ok := checksByID[resource.ID]
			if !ok || smstate.CheckKey(remote.Job, remote.Target) != spec.Key || probeID == 0 {
				return plan{}, errors.New("owned remote check is missing or changed")
			}
			act := actionUpdate
			if sameCheck(remote, desiredCheck(spec, probeID)) {
				act = actionNoop
			}
			result.checks = append(result.checks, checkPlan{spec: spec, action: act, remote: remote, ownership: resource.Ownership})
			continue
		}
		candidates := checksByKey[spec.Key]
		if len(candidates) > 0 {
			if len(candidates) == 1 && p.adopt && probeID > 0 && sameCheck(candidates[0], desiredCheck(spec, probeID)) && candidates[0].ID > 0 && candidates[0].Modified > 0 {
				result.checks = append(result.checks, checkPlan{spec: spec, action: actionAdopt, remote: candidates[0], ownership: smstate.Adopted})
				continue
			}
			return plan{}, errors.New("foreign remote check collision")
		}
		result.checks = append(result.checks, checkPlan{spec: spec, action: actionCreate, ownership: smstate.Owned})
	}
	return result, nil
}

func (p *provisioner) applyProbe(ctx context.Context, item probePlan) (smstate.Resource, error) {
	if item.action == actionAdopt || item.action == actionNoop {
		return p.persistResource("probe", item.spec.Key, item.remote.ID, item.remote.Modified, item.ownership, smstate.SpecHash(managedProbeEvidence(item.remote)))
	}
	desired := desiredProbe(item.spec)
	operation := string(item.action)
	if item.action == actionUpdate {
		desired.ID = item.remote.ID
	}
	if err := p.beginMutation(operation, "probe", item.spec.Key, desired); err != nil {
		return smstate.Resource{}, err
	}
	var remote smProbe
	var err error
	if item.action == actionCreate {
		remote, err = p.client.addProbe(ctx, desired)
	} else {
		remote, err = p.client.updateProbe(ctx, desired)
	}
	if err != nil || remote.ID <= 0 || remote.Modified <= 0 || !sameProbe(remote, desired) {
		return smstate.Resource{}, pendingMutationError(err)
	}
	if p.afterMutation != nil {
		if err := p.afterMutation("probe_api_success"); err != nil {
			return smstate.Resource{}, errors.New("pending operation requires explicit operator review")
		}
	}
	resource, err := p.persistResource("probe", item.spec.Key, remote.ID, remote.Modified, item.ownership, smstate.SpecHash(managedProbeEvidence(remote)))
	if err != nil {
		return smstate.Resource{}, err
	}
	if err := smstate.RemoveJournal(p.stateDir); err != nil {
		return smstate.Resource{}, errors.New("pending operation requires explicit operator review")
	}
	return resource, nil
}

func (p *provisioner) applyCheck(ctx context.Context, item checkPlan, probeIDs map[string]int64) (smstate.Resource, error) {
	probeID := probeIDs[item.spec.ProbeKey]
	if probeID == 0 {
		return smstate.Resource{}, errors.New("check probe registration is incomplete")
	}
	if item.action == actionAdopt || item.action == actionNoop {
		return p.persistResource("check", item.spec.Key, item.remote.ID, item.remote.Modified, item.ownership, smstate.SpecHash(managedCheckEvidence(item.remote)))
	}
	desired := desiredCheck(item.spec, probeID)
	operation := string(item.action)
	if item.action == actionUpdate {
		desired.ID = item.remote.ID
	}
	if err := p.beginMutation(operation, "check", item.spec.Key, desired); err != nil {
		return smstate.Resource{}, err
	}
	var remote smCheck
	var err error
	if item.action == actionCreate {
		remote, err = p.client.addCheck(ctx, desired)
	} else {
		remote, err = p.client.updateCheck(ctx, desired)
	}
	if err != nil || remote.ID <= 0 || remote.Modified <= 0 || !sameCheck(remote, desired) {
		return smstate.Resource{}, pendingMutationError(err)
	}
	if p.afterMutation != nil {
		if err := p.afterMutation("check_api_success"); err != nil {
			return smstate.Resource{}, errors.New("pending operation requires explicit operator review")
		}
	}
	resource, err := p.persistResource("check", item.spec.Key, remote.ID, remote.Modified, item.ownership, smstate.SpecHash(managedCheckEvidence(remote)))
	if err != nil {
		return smstate.Resource{}, err
	}
	if err := smstate.RemoveJournal(p.stateDir); err != nil {
		return smstate.Resource{}, errors.New("pending operation requires explicit operator review")
	}
	return resource, nil
}

func (p *provisioner) beginMutation(operation, kind, key string, desired any) error {
	expected, err := json.Marshal(desired)
	if err != nil {
		return errors.New("pending journal specification could not be encoded")
	}
	journal := smstate.Journal{
		SchemaVersion: smstate.SchemaVersion, Operation: operation, Kind: kind, Key: key,
		SnapshotHash: p.snapshot.SnapshotHash, ExpectedHash: smstate.SpecHash(desired), ExpectedSpec: expected,
		StartedUnixMS: p.now().UnixMilli(),
	}
	if err := smstate.WriteJournal(p.stateDir, journal); err != nil {
		return errors.New("pending journal could not be persisted")
	}
	return nil
}

func (p *provisioner) persistResource(kind, key string, id int64, modified float64, ownership smstate.Ownership, specHash string) (smstate.Resource, error) {
	if id <= 0 || modified <= 0 || specHash == "" {
		return smstate.Resource{}, errors.New("remote resource identity is invalid")
	}
	resource := smstate.Resource{Kind: kind, Key: key, ID: id, Modified: modified, Ownership: ownership, SpecHash: specHash}
	if kind == "check" {
		resource.ConfigVersion = smstate.ConfigVersion(modified)
	}
	found := false
	for index := range p.ownership.Resources {
		if p.ownership.Resources[index].Kind == kind && p.ownership.Resources[index].Key == key {
			p.ownership.Resources[index] = resource
			found = true
			break
		}
	}
	if !found {
		p.ownership.Resources = append(p.ownership.Resources, resource)
	}
	p.ownership.WrittenUnixMS = p.now().UnixMilli()
	if err := smstate.WriteOwnership(p.stateDir, p.ownership); err != nil {
		return smstate.Resource{}, errors.New("private ownership could not be persisted")
	}
	found = false
	for index := range p.registration.Resources {
		if p.registration.Resources[index].Kind == kind && p.registration.Resources[index].Key == key {
			p.registration.Resources[index] = resource
			found = true
			break
		}
	}
	if !found {
		p.registration.Resources = append(p.registration.Resources, resource)
	}
	p.registration.WrittenUnixMS = p.now().UnixMilli()
	if err := smstate.WriteRegistration(p.stateDir, p.registration); err != nil {
		return smstate.Resource{}, errors.New("private registration could not be persisted")
	}
	return resource, nil
}

func desiredProbe(spec smstate.ProbeSpec) smProbe {
	return smProbe{Name: spec.Name, Region: spec.Region, Latitude: spec.Latitude, Longitude: spec.Longitude, Public: false}
}

func desiredCheck(spec smstate.CheckSpec, probeID int64) smCheck {
	labels := make([]smLabel, 0, len(spec.Labels))
	for _, label := range spec.Labels {
		labels = append(labels, smLabel{Name: label.Name, Value: label.Value})
	}
	sort.Slice(labels, func(i, j int) bool {
		if labels[i].Name == labels[j].Name {
			return labels[i].Value < labels[j].Value
		}
		return labels[i].Name < labels[j].Name
	})
	return smCheck{
		Job: spec.Job, Target: spec.Target, Frequency: spec.FrequencyMS, Timeout: spec.TimeoutMS,
		Enabled: true, Probes: []int64{probeID}, Labels: labels, AlertSensitivity: "none",
		BasicMetricsOnly: true, Settings: map[string]any{"http": map[string]any{"method": "GET", "ipVersion": "V4"}},
	}
}

func sameProbe(remote, desired smProbe) bool {
	return remote.Name == desired.Name && remote.Region == desired.Region && !remote.Public &&
		float32(remote.Latitude) == float32(desired.Latitude) && float32(remote.Longitude) == float32(desired.Longitude)
}

func sameCheck(remote, desired smCheck) bool {
	remote.ID, remote.Modified = 0, 0
	desired.ID, desired.Modified = 0, 0
	sort.Slice(remote.Labels, func(i, j int) bool {
		if remote.Labels[i].Name == remote.Labels[j].Name {
			return remote.Labels[i].Value < remote.Labels[j].Value
		}
		return remote.Labels[i].Name < remote.Labels[j].Name
	})
	sort.Slice(desired.Labels, func(i, j int) bool {
		if desired.Labels[i].Name == desired.Labels[j].Name {
			return desired.Labels[i].Value < desired.Labels[j].Value
		}
		return desired.Labels[i].Name < desired.Labels[j].Name
	})
	sort.Slice(remote.Probes, func(i, j int) bool { return remote.Probes[i] < remote.Probes[j] })
	sort.Slice(desired.Probes, func(i, j int) bool { return desired.Probes[i] < desired.Probes[j] })
	remoteHTTP := managedHTTPSettings(remote.Settings)
	desiredHTTP := managedHTTPSettings(desired.Settings)
	return remote.Job == desired.Job && remote.Target == desired.Target && remote.Frequency == desired.Frequency &&
		remote.Timeout == desired.Timeout && remote.Enabled == desired.Enabled &&
		remote.AlertSensitivity == desired.AlertSensitivity && remote.BasicMetricsOnly == desired.BasicMetricsOnly &&
		reflect.DeepEqual(remote.Labels, desired.Labels) && reflect.DeepEqual(remote.Probes, desired.Probes) &&
		reflect.DeepEqual(remoteHTTP, desiredHTTP)
}

func managedHTTPSettings(settings map[string]any) map[string]string {
	encoded, err := json.Marshal(settings["http"])
	if err != nil {
		return nil
	}
	var normalized map[string]any
	if json.Unmarshal(encoded, &normalized) != nil {
		return nil
	}
	return map[string]string{
		"method":    fmt.Sprint(normalized["method"]),
		"ipVersion": fmt.Sprint(normalized["ipVersion"]),
	}
}

func registrationResources(resources []smstate.Resource) map[string]smstate.Resource {
	out := make(map[string]smstate.Resource, len(resources))
	for _, resource := range resources {
		out[resource.Kind+"\x00"+resource.Key] = resource
	}
	return out
}

func adoptionCount(planned plan) int {
	count := 0
	for _, item := range planned.probes {
		if item.action == actionAdopt {
			count++
		}
	}
	for _, item := range planned.checks {
		if item.action == actionAdopt {
			count++
		}
	}
	return count
}

func migrationPlanIsNoop(planned plan) bool {
	if len(planned.probes) == 0 || len(planned.checks) == 0 {
		return false
	}
	for _, item := range planned.probes {
		if item.action != actionNoop {
			return false
		}
	}
	for _, item := range planned.checks {
		if item.action != actionNoop {
			return false
		}
	}
	return true
}

func (p *provisioner) adoptionPreview(planned plan) smstate.AdoptionPreview {
	type adoptedResource struct {
		Kind     string `json:"kind"`
		Key      string `json:"key"`
		ID       int64  `json:"id"`
		Modified string `json:"modified"`
		SpecHash string `json:"spec_hash"`
	}
	resources := make([]adoptedResource, 0, adoptionCount(planned))
	for _, item := range planned.probes {
		if item.action == actionAdopt {
			resources = append(resources, adoptedResource{"probe", item.spec.Key, item.remote.ID, smstate.ConfigVersion(item.remote.Modified), smstate.SpecHash(desiredProbe(item.spec))})
		}
	}
	probeIDs := make(map[string]int64, len(planned.probes))
	for _, item := range planned.probes {
		probeIDs[item.spec.Key] = item.remote.ID
	}
	for _, item := range planned.checks {
		if item.action == actionAdopt {
			resources = append(resources, adoptedResource{"check", item.spec.Key, item.remote.ID, smstate.ConfigVersion(item.remote.Modified), smstate.SpecHash(desiredCheck(item.spec, probeIDs[item.spec.ProbeKey]))})
		}
	}
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Kind == resources[j].Kind {
			return resources[i].Key < resources[j].Key
		}
		return resources[i].Kind < resources[j].Kind
	})
	return smstate.AdoptionPreview{
		SchemaVersion: smstate.SchemaVersion, SnapshotHash: p.snapshot.SnapshotHash,
		TargetFingerprint: p.snapshot.TargetFingerprint, SourceVersion: p.snapshot.SourceVersion,
		PlanHash: smstate.SpecHash(resources), WrittenUnixMS: p.now().UnixMilli(),
	}
}

func (p *provisioner) migrationPreview(planned plan, resources []smstate.MigrationResource) smstate.MigrationPreview {
	type planEntry struct {
		Kind            string `json:"kind"`
		Key             string `json:"key"`
		Action          action `json:"action"`
		RemoteID        int64  `json:"remote_id"`
		RemoteModified  string `json:"remote_modified"`
		DesiredSpecHash string `json:"desired_spec_hash"`
	}
	entries := make([]planEntry, 0, len(planned.probes)+len(planned.checks))
	for _, item := range planned.probes {
		entries = append(entries, planEntry{
			Kind: "probe", Key: item.spec.Key, Action: item.action, RemoteID: item.remote.ID,
			RemoteModified: smstate.ConfigVersion(item.remote.Modified), DesiredSpecHash: smstate.SpecHash(item.spec),
		})
	}
	for _, item := range planned.checks {
		entries = append(entries, planEntry{
			Kind: "check", Key: item.spec.Key, Action: item.action, RemoteID: item.remote.ID,
			RemoteModified: smstate.ConfigVersion(item.remote.Modified), DesiredSpecHash: smstate.SpecHash(item.spec),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Kind == entries[j].Kind {
			return entries[i].Key < entries[j].Key
		}
		return entries[i].Kind < entries[j].Kind
	})
	return smstate.MigrationPreview{
		SchemaVersion: smstate.SchemaVersion, OldTargetFingerprint: p.migrationFrom,
		NewTargetFingerprint: p.snapshot.TargetFingerprint, SnapshotHash: p.snapshot.SnapshotHash,
		SourceVersion: p.snapshot.SourceVersion, PlanHash: smstate.SpecHash(entries),
		WrittenUnixMS: p.now().UnixMilli(), Resources: append([]smstate.MigrationResource(nil), resources...),
	}
}

func printPlan(planned plan, apply bool) {
	counts := map[action]int{}
	for _, item := range planned.probes {
		counts[item.action]++
	}
	for _, item := range planned.checks {
		counts[item.action]++
	}
	mode := "preview"
	if apply {
		mode = "apply"
	}
	fmt.Printf("%s plan: create=%d update=%d adopt=%d unchanged=%d\n", mode, counts[actionCreate], counts[actionUpdate], counts[actionAdopt], counts[actionNoop])
}

func pendingMutationError(err error) error {
	if ambiguousAPIError(err) {
		return errors.New("remote mutation outcome is ambiguous; pending operation requires explicit operator review")
	}
	return errors.New("remote mutation did not complete; pending operation requires explicit operator review")
}

func pendingStateExists(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, smstate.JournalFilename))
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

func explicitBool(name string) (bool, error) {
	value := os.Getenv(name)
	switch value {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, fmt.Errorf("%s must be exactly true or false", name)
	}
}

func envDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
