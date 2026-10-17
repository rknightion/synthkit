// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/smstate"
	"github.com/rknightion/synthkit/internal/smstore"
)

const usageText = `Usage:
  sm-provision            preview (default) or, with SM_PROVISION_APPLY=true, apply the snapshot
  sm-provision cleanup    preview an ownership-bound cleanup of recorded owned resources
  SM_PROVISION_APPLY=true sm-provision cleanup -confirm=<plan>
                          delete exactly the previewed owned resources
  sm-provision help       print this help

Cleanup deletes only resources recorded as owned (created by synthkit) in the ownership ledger for
the configured target, by recorded API ID, after revalidating each remote revision and managed
specification. Adopted resources, probes still referenced by another check, and changed, foreign,
legacy-unbound or incompletely recorded resources are never deleted. Every delete first persists a
pending intent; an unknown outcome retains it, and the next cleanup run confirms absence or retries
the unchanged identity. A definite 4xx rejection also keeps the intent: fix the token permission and
rerun cleanup. Each planned run prints a secret-free JSON receipt line; a refused run prints none.
`

// cleanupRequest selects explicit cleanup. confirm is the plan hash a preview printed.
type cleanupRequest struct{ confirm string }

type invocation struct {
	help    bool
	cleanup *cleanupRequest
}

func parseInvocation(args []string) (invocation, error) {
	if len(args) == 0 {
		return invocation{}, nil
	}
	switch args[0] {
	case "help", "-h", "-help", "--help":
		if len(args) != 1 {
			return invocation{}, errors.New("unexpected arguments; run sm-provision help")
		}
		return invocation{help: true}, nil
	case "cleanup":
		fs := flag.NewFlagSet("cleanup", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		confirm := fs.String("confirm", "", "plan hash printed by a cleanup preview")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
			return invocation{}, errors.New("invalid cleanup arguments; run sm-provision help")
		}
		if *confirm != "" {
			if decoded, err := hex.DecodeString(*confirm); err != nil || len(decoded) != 32 || strings.ToLower(*confirm) != *confirm {
				return invocation{}, errors.New("cleanup -confirm must be the 64-character plan printed by a cleanup preview")
			}
		}
		return invocation{cleanup: &cleanupRequest{confirm: *confirm}}, nil
	default:
		return invocation{}, errors.New("unknown arguments; run sm-provision help")
	}
}

func validateCleanupMode(cfg config.SMProvisionConfig, req *cleanupRequest) error {
	if req == nil {
		return nil
	}
	if cfg.AdoptLegacy || cfg.MigrateTarget {
		return errors.New("cleanup cannot be combined with legacy adoption or target migration")
	}
	if cfg.Apply && req.confirm == "" {
		return errors.New("cleanup apply requires -confirm=<plan> from a current cleanup preview")
	}
	if !cfg.Apply && req.confirm != "" {
		return errors.New("cleanup -confirm requires SM_PROVISION_APPLY=true")
	}
	return nil
}

type cleanupAction string

const (
	cleanupDelete cleanupAction = "delete"
	cleanupAbsent cleanupAction = "absent"
	cleanupRetain cleanupAction = "retain"
)

type cleanupEntry struct {
	resource smstate.Resource
	action   cleanupAction
	reason   string
	pending  bool
}

type cleaner struct {
	client    *smClient
	store     smstore.Store
	ctx       context.Context
	snapshot  smstate.Snapshot
	target    string
	ownership smstate.OwnershipLedger
	pending   *smstate.DeleteIntent
	now       func() time.Time
	out       io.Writer
	rejected  int // HTTP status of a definite delete rejection, for the receipt
}

func executeCleanup(ctx context.Context, cfg config.SMProvisionConfig, store smstore.Store, stateDir string, deps provisionDependencies) error {
	doc, err := store.Load(ctx)
	if err != nil {
		return errors.New("cleanup refused: SM state is unavailable or invalid")
	}
	if doc.Snapshot == nil {
		return errors.New("cleanup refused: the current private snapshot is unavailable to bind durable intent")
	}
	snapshot := *doc.Snapshot
	target := smstate.TargetFingerprint(cfg.SMURL, cfg.SMToken)
	if snapshot.TargetFingerprint != target {
		return errors.New("cleanup refused: private snapshot target does not match configured target; credentials are never substituted")
	}
	if snapshot.SourceVersion != version {
		return errors.New("cleanup refused: provisioner version does not match private snapshot")
	}
	if doc.Provision.Migration != nil {
		return errors.New("cleanup refused: target migration is incomplete")
	}
	if doc.Provision.Ownership == nil {
		if cfg.StateBackend == "file" {
			if _, err := smstate.OwnershipFromRegistration(stateDir, target); err == nil {
				return errors.New("cleanup refused: legacy registration has no bound ownership ledger; run an ordinary apply first")
			} else if !errors.Is(err, os.ErrNotExist) {
				return errors.New("cleanup refused: private ownership state is invalid or not bound to the configured target")
			}
		}
		return errors.New("cleanup refused: no ownership ledger is recorded; nothing is in cleanup custody")
	}
	ledger := *doc.Provision.Ownership
	if ledger.TargetFingerprint != target {
		return errors.New("cleanup refused: ownership target does not match the configured target; credentials are never substituted")
	}
	for _, resource := range ledger.Resources {
		if resource.SpecHash == "" {
			return errors.New("cleanup refused: legacy ownership has no bound specification; run an ordinary apply first")
		}
	}
	var pending *smstate.DeleteIntent
	if doc.Provision.Pending != nil {
		intent, err := smstate.DeleteIntentFromJournal(*doc.Provision.Pending)
		if err != nil {
			return errors.New("cleanup refused: pending provisioning operation requires explicit operator review")
		}
		if intent.TargetFingerprint != target {
			return errors.New("cleanup refused: pending delete intent is bound to another target")
		}
		pending = &intent
	}
	out := io.Writer(os.Stdout)
	c := &cleaner{client: deps.client, store: store, ctx: ctx, snapshot: snapshot, target: target, ownership: ledger, pending: pending, out: out,
		now: deps.Clock}
	return c.run(cfg.Apply, deps.cleanup.confirm)
}

func (c *cleaner) inventory() ([]smProbe, []smCheck, error) {
	probes, err := c.client.listProbes(c.ctx)
	if err != nil {
		return nil, nil, errors.New("remote probe inventory is unavailable")
	}
	checks, err := c.client.listChecks(c.ctx)
	if err != nil {
		return nil, nil, errors.New("remote check inventory is unavailable")
	}
	return probes, checks, nil
}

func (c *cleaner) run(apply bool, confirm string) error {
	probes, checks, err := c.inventory()
	if err != nil {
		return err
	}
	entries, err := c.plan(probes, checks)
	if err != nil {
		return err
	}
	planHash := c.planHash(entries)
	counts := map[cleanupAction]int{}
	for _, entry := range entries {
		counts[entry.action]++
	}
	mode := "preview"
	if apply {
		mode = "apply"
	}
	_, _ = fmt.Fprintf(c.out, "cleanup %s plan: delete=%d absent=%d retained=%d plan=%s\n", mode, counts[cleanupDelete], counts[cleanupAbsent], counts[cleanupRetain], planHash)
	outcomes := make([]string, len(entries))
	if !apply {
		for i, entry := range entries {
			outcomes[i] = map[cleanupAction]string{cleanupDelete: "would_delete", cleanupAbsent: "absent", cleanupRetain: "retained"}[entry.action]
		}
		c.receipt("preview", "planned", planHash, entries, outcomes)
		_, _ = fmt.Fprintln(c.out, "cleanup preview made no changes; rerun with SM_PROVISION_APPLY=true and -confirm="+planHash)
		return nil
	}
	if confirm != planHash {
		return errors.New("cleanup refused: -confirm does not match the current cleanup plan; preview again")
	}
	for i := range outcomes {
		outcomes[i] = "not_attempted"
		if entries[i].action == cleanupRetain {
			outcomes[i] = "retained"
		}
	}
	for i, entry := range entries {
		if entry.action == cleanupRetain {
			continue
		}
		outcome, err := c.remove(entry)
		outcomes[i] = outcome
		if err != nil {
			status := "unknown"
			switch outcome {
			case "not_attempted":
				status = "stopped"
			case "rejected":
				status = "rejected"
			}
			c.receipt("apply", status, planHash, entries, outcomes)
			return err
		}
	}
	status := "complete"
	if counts[cleanupRetain] > 0 {
		status = "remaining"
	}
	c.receipt("apply", status, planHash, entries, outcomes)
	return nil
}

// plan admits only exact recorded owned identities. Every changed owned resource refuses the whole
// cleanup before any write; absence by recorded ID is observed fact, never a name/spec search.
func (c *cleaner) plan(probes []smProbe, checks []smCheck) ([]cleanupEntry, error) {
	probesByID := make(map[int64]smProbe, len(probes))
	for _, remote := range probes {
		probesByID[remote.ID] = remote
	}
	checksByID := make(map[int64]smCheck, len(checks))
	for _, remote := range checks {
		checksByID[remote.ID] = remote
	}
	var entries []cleanupEntry
	pendingRecorded := false
	for _, resource := range c.ownership.Resources {
		entry := cleanupEntry{resource: resource}
		if c.pending != nil && c.pending.Kind == resource.Kind && c.pending.Key == resource.Key {
			if c.pending.ID != resource.ID || c.pending.SpecHash != resource.SpecHash || c.pending.Modified != smstate.ConfigVersion(resource.Modified) {
				return nil, errors.New("cleanup refused: pending delete intent disagrees with recorded ownership; operator review required")
			}
			entry.pending, pendingRecorded = true, true
		}
		if resource.Ownership != smstate.Owned {
			if entry.pending {
				return nil, errors.New("cleanup refused: pending delete intent targets a resource synthkit did not create")
			}
			entry.action, entry.reason = cleanupRetain, "adopted"
			entries = append(entries, entry)
			continue
		}
		state, err := observe(resource, probesByID, checksByID)
		if err != nil {
			return nil, err
		}
		entry.action = state
		entries = append(entries, entry)
	}
	if c.pending != nil && !pendingRecorded {
		// The ledger removal committed before the pending clear: only observed absence completes it.
		if remotePresent(c.pending.Kind, c.pending.ID, probesByID, checksByID) {
			return nil, errors.New("cleanup refused: pending delete identity is no longer recorded but remains remote; operator review required")
		}
		entries = append(entries, cleanupEntry{resource: smstate.Resource{Kind: c.pending.Kind, Key: c.pending.Key, ID: c.pending.ID, Ownership: smstate.Owned, SpecHash: c.pending.SpecHash}, action: cleanupAbsent, pending: true})
	}
	deleting := map[int64]bool{}
	for _, entry := range entries {
		if entry.resource.Kind == "check" && entry.action == cleanupDelete {
			deleting[entry.resource.ID] = true
		}
	}
	for i := range entries {
		entry := &entries[i]
		if entry.resource.Kind != "probe" || entry.action != cleanupDelete {
			continue
		}
		for _, remote := range checks {
			if !deleting[remote.ID] && containsID(remote.Probes, entry.resource.ID) {
				if entry.pending {
					return nil, errors.New("cleanup refused: pending probe delete is referenced by another check; operator review required")
				}
				entry.action, entry.reason = cleanupRetain, "referenced"
				break
			}
		}
	}
	// Checks precede the probes they reference; within a kind the recorded pending identity is
	// resolved first so a second intent is never begun while one is outstanding.
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].resource.Kind != entries[j].resource.Kind {
			return entries[i].resource.Kind == "check"
		}
		if entries[i].pending != entries[j].pending {
			return entries[i].pending
		}
		return entries[i].resource.Key < entries[j].resource.Key
	})
	return entries, nil
}

func observe(resource smstate.Resource, probesByID map[int64]smProbe, checksByID map[int64]smCheck) (cleanupAction, error) {
	switch resource.Kind {
	case "probe":
		remote, ok := probesByID[resource.ID]
		if !ok {
			return cleanupAbsent, nil
		}
		if smstate.ProbeKey(remote.Name, remote.Region) != resource.Key || smstate.ConfigVersion(remote.Modified) != smstate.ConfigVersion(resource.Modified) ||
			smstate.SpecHash(managedProbeEvidence(remote)) != resource.SpecHash {
			return "", errors.New("cleanup refused: owned remote probe revision or specification changed")
		}
	case "check":
		remote, ok := checksByID[resource.ID]
		if !ok {
			return cleanupAbsent, nil
		}
		if smstate.CheckKey(remote.Job, remote.Target) != resource.Key || smstate.ConfigVersion(remote.Modified) != smstate.ConfigVersion(resource.Modified) ||
			smstate.SpecHash(managedCheckEvidence(remote)) != resource.SpecHash {
			return "", errors.New("cleanup refused: owned remote check revision or specification changed")
		}
	default:
		return "", errors.New("cleanup refused: ownership state is invalid")
	}
	return cleanupDelete, nil
}

func remotePresent(kind string, id int64, probesByID map[int64]smProbe, checksByID map[int64]smCheck) bool {
	if kind == "probe" {
		_, ok := probesByID[id]
		return ok
	}
	_, ok := checksByID[id]
	return ok
}

func containsID(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// remove deletes one exact identity. It returns the receipt outcome. Pending intent is durable and
// activation is removed before the DELETE; custody is released only on observed absence.
func (c *cleaner) remove(entry cleanupEntry) (string, error) {
	resource := entry.resource
	if entry.action == cleanupAbsent {
		if err := c.release(resource); err != nil {
			return "unknown", err
		}
		return "confirmed_absent", nil
	}
	probes, checks, err := c.inventory()
	if err != nil {
		return "not_attempted", errors.New("cleanup stopped before deletion: " + err.Error() + "; remaining custody retained")
	}
	probesByID := make(map[int64]smProbe, len(probes))
	for _, remote := range probes {
		probesByID[remote.ID] = remote
	}
	checksByID := make(map[int64]smCheck, len(checks))
	for _, remote := range checks {
		checksByID[remote.ID] = remote
	}
	state, err := observe(resource, probesByID, checksByID)
	if err != nil {
		return "not_attempted", errors.New("cleanup stopped: owned remote " + resource.Kind + " changed before deletion; remaining custody retained")
	}
	if state == cleanupAbsent {
		if err := c.release(resource); err != nil {
			return "unknown", err
		}
		return "confirmed_absent", nil
	}
	if resource.Kind == "probe" {
		for _, remote := range checks {
			if containsID(remote.Probes, resource.ID) {
				return "not_attempted", errors.New("cleanup stopped: owned probe became referenced by another check; remaining custody retained")
			}
		}
	}
	intent := smstate.DeleteIntent{SchemaVersion: smstate.SchemaVersion, TargetFingerprint: c.target, Kind: resource.Kind, Key: resource.Key,
		ID: resource.ID, Modified: smstate.ConfigVersion(resource.Modified), SpecHash: resource.SpecHash}
	journal, err := smstate.NewDeleteJournal(intent, c.snapshot.SnapshotHash, c.now())
	if err != nil {
		return "not_attempted", errors.New("cleanup pending intent is invalid")
	}
	if err := c.store.UpdateProvision(c.ctx, smstore.Binding(c.snapshot), func(s *smstore.ProvisionState) error {
		if s.Pending != nil {
			current, err := smstate.DeleteIntentFromJournal(*s.Pending)
			if err != nil || current != intent {
				return errors.New("another pending operation exists")
			}
		}
		s.Pending, s.Registration = &journal, nil
		return nil
	}); err != nil {
		return "not_attempted", errors.New("cleanup pending intent could not be persisted; no deletion was attempted")
	}
	deleteErr := c.client.deleteResource(c.ctx, resource.Kind, resource.ID)
	// Only an inventory observation proves absence; a delete response alone never releases custody.
	probes, checks, err = c.inventory()
	if err != nil {
		return "unknown", errors.New("cleanup outcome is unknown; pending delete intent retained for recovery")
	}
	if remotePresent(resource.Kind, resource.ID, mapProbes(probes), mapChecks(checks)) {
		if status := definiteRejection(deleteErr); status != 0 {
			c.rejected = status
			return "rejected", fmt.Errorf("remote delete was rejected with HTTP %d and the resource is still present; fix the SM token permission and rerun cleanup (pending delete intent retained)", status)
		}
		return "unknown", errors.New("cleanup outcome is unknown; pending delete intent retained for recovery")
	}
	if err := c.release(resource); err != nil {
		return "unknown", err
	}
	return "confirmed_absent", nil
}

// release records observed absence: the identity leaves ownership custody and its pending intent is
// cleared in the same state update. Activation stays removed.
func (c *cleaner) release(resource smstate.Resource) error {
	err := c.store.UpdateProvision(c.ctx, smstore.Binding(c.snapshot), func(s *smstore.ProvisionState) error {
		if s.Ownership == nil || s.Ownership.TargetFingerprint != c.target {
			return errors.New("ownership changed")
		}
		ledger := *s.Ownership
		kept := make([]smstate.Resource, 0, len(ledger.Resources))
		for _, recorded := range ledger.Resources {
			if recorded.Kind == resource.Kind && recorded.Key == resource.Key {
				if recorded.ID != resource.ID || recorded.SpecHash != resource.SpecHash || recorded.Ownership != smstate.Owned {
					return errors.New("ownership changed")
				}
				continue
			}
			kept = append(kept, recorded)
		}
		ledger.Resources = kept
		ledger.WrittenUnixMS = c.now().UnixMilli()
		s.Ownership, s.Registration = &ledger, nil
		if s.Pending != nil {
			current, err := smstate.DeleteIntentFromJournal(*s.Pending)
			if err != nil {
				return errors.New("another pending operation exists")
			}
			if current.Kind == resource.Kind && current.Key == resource.Key && current.ID == resource.ID {
				s.Pending = nil
			}
		}
		return nil
	})
	if err != nil {
		return errors.New("confirmed absence could not be recorded; pending delete intent retained for recovery")
	}
	return nil
}

func mapProbes(probes []smProbe) map[int64]smProbe {
	out := make(map[int64]smProbe, len(probes))
	for _, remote := range probes {
		out[remote.ID] = remote
	}
	return out
}

func mapChecks(checks []smCheck) map[int64]smCheck {
	out := make(map[int64]smCheck, len(checks))
	for _, remote := range checks {
		out[remote.ID] = remote
	}
	return out
}

func (c *cleaner) planHash(entries []cleanupEntry) string {
	type planEntry struct {
		Kind      string            `json:"kind"`
		Key       string            `json:"key"`
		ID        int64             `json:"id"`
		Modified  string            `json:"modified"`
		SpecHash  string            `json:"spec_hash"`
		Ownership smstate.Ownership `json:"ownership"`
		Action    cleanupAction     `json:"action"`
		Reason    string            `json:"reason"`
		Pending   bool              `json:"pending"`
	}
	projected := make([]planEntry, 0, len(entries))
	for _, entry := range entries {
		projected = append(projected, planEntry{entry.resource.Kind, entry.resource.Key, entry.resource.ID, smstate.ConfigVersion(entry.resource.Modified),
			entry.resource.SpecHash, entry.resource.Ownership, entry.action, entry.reason, entry.pending})
	}
	return smstate.SpecHash(struct {
		Schema            string      `json:"schema"`
		TargetFingerprint string      `json:"target_fingerprint"`
		SnapshotHash      string      `json:"snapshot_hash"`
		Entries           []planEntry `json:"entries"`
	}{"synthkit-sm-cleanup-plan-v1", c.target, c.snapshot.SnapshotHash, projected})
}

// receipt prints one secret-free JSON line: kinds, API IDs, revisions and outcomes only. It omits
// keys and check targets, which can carry operator data, and every credential-derived value.
func (c *cleaner) receipt(mode, status, planHash string, entries []cleanupEntry, outcomes []string) {
	type entry struct {
		Kind          string `json:"kind"`
		ID            int64  `json:"id"`
		ConfigVersion string `json:"config_version,omitempty"`
		Outcome       string `json:"outcome"`
		Reason        string `json:"reason,omitempty"`
		HTTPStatus    int    `json:"http_status,omitempty"`
	}
	resources := make([]entry, 0, len(entries))
	for i, e := range entries {
		version := ""
		if e.resource.Modified > 0 {
			version = smstate.ConfigVersion(e.resource.Modified)
		}
		status := 0
		if outcomes[i] == "rejected" {
			status = c.rejected
		}
		resources = append(resources, entry{e.resource.Kind, e.resource.ID, version, outcomes[i], e.reason, status})
	}
	b, _ := json.Marshal(struct {
		Schema    string  `json:"schema"`
		Mode      string  `json:"mode"`
		Status    string  `json:"status"`
		Plan      string  `json:"plan"`
		Resources []entry `json:"resources"`
	}{"synthkit-sm-cleanup-receipt-v1", mode, status, planHash, resources})
	_, _ = fmt.Fprintln(c.out, "cleanup receipt: "+string(b))
}
