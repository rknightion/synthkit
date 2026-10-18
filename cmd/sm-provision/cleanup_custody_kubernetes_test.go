// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/smstate"
	"github.com/rknightion/synthkit/internal/smstore/smstoretest"
)

// unpublishNamed is the emitter's boot PublishSnapshot(nil) on the aggregate SM document, which
// replaces only the snapshot and retains every provision field.
func unpublishNamed(t *testing.T, a *smstoretest.API) {
	t.Helper()
	d := namedDoc(t, a)
	d.Snapshot = nil
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	a.Set("sm", b)
}

// provisionKubeUnpublished applies through the real diskless CLI, then unpublishes the snapshot
// and returns the custody-bound cleanup preview plan.
func provisionKubeUnpublished(t *testing.T) (*smstoretest.API, *cleanupSM, config.SMProvisionConfig, string) {
	t.Helper()
	a, cs, cfg, snapshotPlan := provisionKube(t)
	unpublishNamed(t, a)
	cfg.Apply = false
	preview := receiptOf(t, waitProvision(t, startKubeCleanup(t, a, cfg, "cleanup"), true))
	if len(preview.Resources) != 2 || preview.Plan == snapshotPlan {
		t.Fatalf("custody preview = %+v", preview)
	}
	cfg.Apply = true
	return a, cs, cfg, preview.Plan
}

func TestKubernetesCleanupWithoutPublishedSnapshotKeepsDurableIntent(t *testing.T) {
	a, cs, cfg, plan := provisionKubeUnpublished(t)
	intents := 0
	cs.set(func(c *cleanupSM) {
		c.onDelete = func(_ http.ResponseWriter, _ *http.Request, kind string, id int64) bool {
			d := namedDoc(t, a)
			if d.Snapshot != nil || d.Provision.Registration != nil || d.Provision.Pending == nil || d.Provision.Pending.Operation != smstate.DeleteOperation {
				t.Errorf("DELETE %s/%d before durable custody intent", kind, id)
				return false
			}
			intents++
			return false
		}
	})
	out := waitProvision(t, startKubeCleanup(t, a, cfg, "cleanup", "-confirm="+plan), true)
	receipt := receiptOf(t, out)
	observed := 0
	cs.set(func(*cleanupSM) { observed = intents })
	if receipt.Status != "complete" || observed != 2 || !strings.Contains(out, "joined-before-release=true") {
		t.Fatalf("receipt=%+v intents=%d\n%s", receipt, observed, out)
	}
	d := namedDoc(t, a)
	if d.Snapshot != nil || d.Provision.Pending != nil || d.Provision.Registration != nil || d.Provision.Ownership == nil || len(d.Provision.Ownership.Resources) != 0 {
		t.Fatalf("final custody = %+v", d)
	}
}

func TestKubernetesCleanupWithoutPublishedSnapshotRefusesForeignAndChangedCustody(t *testing.T) {
	// One applied fixture serves every case: each mutation is undone before the next.
	a, cs, cfg, plan := provisionKubeUnpublished(t)
	for _, tc := range []struct {
		name, want string
		mutate     func(t *testing.T) (undo func())
	}{
		{name: "ledger bound to another target", want: "ownership target does not match", mutate: func(t *testing.T) func() {
			original := a.Data("sm")
			d := namedDoc(t, a)
			d.Provision.Ownership.TargetFingerprint = smstate.TargetFingerprint("https://sm.example", "previous-credential")
			b, _ := json.Marshal(d)
			a.Set("sm", b)
			return func() { a.Set("sm", original) }
		}},
		{name: "changed revision", want: "revision or specification changed", mutate: func(t *testing.T) func() {
			cs.fake.withState(func(fake *fakeSM) { fake.checks[0].Modified++ })
			return func() { cs.fake.withState(func(fake *fakeSM) { fake.checks[0].Modified-- }) }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			undo := tc.mutate(t)
			defer undo()
			before := a.Data("sm")
			out := waitProvision(t, startKubeCleanup(t, a, cfg, "cleanup", "-confirm="+plan), false)
			if !strings.Contains(out, "cleanup refused: ") || !strings.Contains(out, tc.want) {
				t.Fatalf("refusal output, want %q:\n%s", tc.want, out)
			}
			if deletes, _ := cs.observed(); len(deletes) != 0 || !bytes.Equal(before, a.Data("sm")) {
				t.Fatalf("refused custody cleanup mutated: deletes=%v", deletes)
			}
		})
	}
}

// A snapshot the emitter publishes mid-run (SM re-enabled) ends custody binding before any DELETE.
func TestKubernetesCleanupCustodyStopsWhenSnapshotIsPublished(t *testing.T) {
	a, cs, cfg, plan := provisionKubeUnpublished(t)
	cs.set(func(c *cleanupSM) {
		c.checkLists = 0
		c.onList = func(n int) {
			if n != 2 { // publish between the plan inventory and the pre-delete revalidation
				return
			}
			d := namedDoc(t, a)
			s := seedSnapshot(t, cfg.SMURL, cfg.SMToken)
			d.Snapshot = &s
			b, _ := json.Marshal(d)
			a.Set("sm", b)
		}
	})
	out := waitProvision(t, startKubeCleanup(t, a, cfg, "cleanup", "-confirm="+plan), false)
	if !strings.Contains(out, "no deletion was attempted") {
		t.Fatalf("output:\n%s", out)
	}
	if deletes, _ := cs.observed(); len(deletes) != 0 {
		t.Fatalf("deleted across a published snapshot: %v", deletes)
	}
	d := namedDoc(t, a)
	if d.Snapshot == nil || d.Provision.Pending != nil || len(d.Provision.Ownership.Resources) != 2 {
		t.Fatalf("custody changed: %+v", d)
	}
}

func seedSnapshot(t *testing.T, url, token string) smstate.Snapshot {
	t.Helper()
	p := smstate.ProbeSpec{Name: "private", Region: "EMEA", Key: smstate.ProbeKey("private", "EMEA"), Latitude: 50, Longitude: 8}
	c := smstate.CheckSpec{Job: "health", Target: "https://api.example/health", Key: smstate.CheckKey("health", "https://api.example/health"), Source: "sample", ProbeKey: p.Key, FrequencyMS: 60000, TimeoutMS: 3000}
	s, err := smstate.NewSnapshot([]smstate.ProbeSpec{p}, []smstate.CheckSpec{c}, url, token, version, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return s
}
