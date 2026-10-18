// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/smstate"
	"github.com/rknightion/synthkit/internal/smstore"
)

// unpublishSnapshot is the emitter's own boot publication when no selected blueprint declares SM
// or synthetic_monitoring is startup-disabled: PublishSnapshot(nil) on the ordinary file store.
func unpublishSnapshot(t *testing.T, f *fileCleanup) {
	t.Helper()
	if err := smstore.NewFileStore(f.stateDir).PublishSnapshot(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(f.stateDir, smstate.SnapshotFilename)); !os.IsNotExist(err) {
		t.Fatalf("snapshot still published: %v", err)
	}
}

func TestCleanupWithoutPublishedSnapshotDeletesRecordedOwnedIDs(t *testing.T) {
	f := provisionForCleanup(t, "token")
	snapshotPlan := receiptOf(t, runCLI(t, f.env(false, "token"), true, "cleanup")).Plan
	unpublishSnapshot(t, f)
	before := privateFiles(t, f.stateDir)

	preview := receiptOf(t, runCLI(t, f.env(false, "token"), true, "cleanup"))
	if preview.Status != "planned" || preview.outcome("check", f.checkID) != "would_delete/" || preview.outcome("probe", f.probeID) != "would_delete/" {
		t.Fatalf("custody preview = %+v", preview)
	}
	if preview.Plan == snapshotPlan {
		t.Fatal("custody plan is not bound apart from the snapshot plan")
	}
	if deletes, _ := f.sm.observed(); len(deletes) != 0 || !sameFiles(before, privateFiles(t, f.stateDir)) {
		t.Fatalf("custody preview mutated: deletes=%v", deletes)
	}
	// A plan previewed against the published snapshot never authorises a custody-bound apply.
	if out := runCLI(t, f.env(true, "token"), false, "cleanup", "-confirm="+snapshotPlan); !strings.Contains(out, "does not match the current cleanup plan") {
		t.Fatalf("snapshot plan output:\n%s", out)
	}
	if deletes, _ := f.sm.observed(); len(deletes) != 0 || !sameFiles(before, privateFiles(t, f.stateDir)) {
		t.Fatal("mismatched plan mutated")
	}
	f.sm.set(func(c *cleanupSM) {
		c.onDelete = func(_ http.ResponseWriter, _ *http.Request, kind string, id int64) bool {
			journal, err := smstate.ReadJournal(f.stateDir)
			if err != nil {
				t.Errorf("DELETE %s/%d before durable intent: %v", kind, id, err)
				return false
			}
			if k, i := pendingDelete(t, journal); k != kind || i != id {
				t.Errorf("DELETE %s/%d under intent %s/%d", kind, id, k, i)
			}
			if _, err := os.Lstat(filepath.Join(f.stateDir, smstate.RegistrationFile)); !os.IsNotExist(err) {
				t.Error("DELETE while activation evidence remained")
			}
			return false
		}
	})
	applied := receiptOf(t, runCLI(t, f.env(true, "token"), true, "cleanup", "-confirm="+preview.Plan))
	if applied.Status != "complete" || applied.outcome("check", f.checkID) != "confirmed_absent/" || applied.outcome("probe", f.probeID) != "confirmed_absent/" {
		t.Fatalf("custody apply = %+v", applied)
	}
	deletes, _ := f.sm.observed()
	if want := []string{fmt.Sprintf("check/%d", f.checkID), fmt.Sprintf("probe/%d", f.probeID)}; strings.Join(deletes, ",") != strings.Join(want, ",") {
		t.Fatalf("deletes = %v, want %v", deletes, want)
	}
	d := loadDoc(t, f.stateDir)
	if d.Snapshot != nil || d.Provision.Pending != nil || d.Provision.Registration != nil || d.Provision.Ownership == nil ||
		d.Provision.Ownership.TargetFingerprint != f.snapshot.TargetFingerprint || len(d.Provision.Ownership.Resources) != 0 {
		t.Fatalf("final custody = %+v snapshot=%v", d.Provision, d.Snapshot)
	}
	if again := receiptOf(t, runCLI(t, f.env(false, "token"), true, "cleanup")); len(again.Resources) != 0 {
		t.Fatalf("released custody reappeared: %+v", again)
	}
}

func TestCleanupWithoutPublishedSnapshotRefusesForeignChangedAndUnboundCustody(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		token      string
		mutate     func(t *testing.T, f *fileCleanup)
	}{
		{name: "changed revision", want: "revision or specification changed", mutate: func(t *testing.T, f *fileCleanup) {
			f.sm.fake.withState(func(fake *fakeSM) { fake.checks[0].Modified++ })
		}},
		{name: "changed specification", want: "revision or specification changed", mutate: func(t *testing.T, f *fileCleanup) {
			f.sm.fake.withState(func(fake *fakeSM) { fake.checks[0].Frequency = 30000 })
		}},
		{name: "same ID now foreign identity", want: "revision or specification changed", mutate: func(t *testing.T, f *fileCleanup) {
			f.sm.fake.withState(func(fake *fakeSM) { fake.probes[0].Region = "OTHER" })
		}},
		{name: "ledger bound to another target", want: "ownership target does not match", mutate: func(t *testing.T, f *fileCleanup) {
			ledger, err := smstate.ReadOwnershipState(f.stateDir)
			if err != nil {
				t.Fatal(err)
			}
			ledger.TargetFingerprint = smstate.TargetFingerprint(f.server.URL, "previous-credential")
			if err := smstate.WriteOwnership(f.stateDir, ledger); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "credential substitution", want: "ownership target does not match", token: "substituted-credential", mutate: func(*testing.T, *fileCleanup) {}},
		{name: "legacy unbound specification", want: "legacy ownership", mutate: func(t *testing.T, f *fileCleanup) {
			ledger, err := smstate.ReadOwnershipState(f.stateDir)
			if err != nil {
				t.Fatal(err)
			}
			ledger.Resources[0].SpecHash = ""
			if err := smstate.WriteOwnership(f.stateDir, ledger); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "no recorded custody", want: "no ownership ledger", mutate: func(t *testing.T, f *fileCleanup) {
			for _, name := range []string{smstate.OwnershipFile, smstate.RegistrationFile} {
				if err := os.Remove(filepath.Join(f.stateDir, name)); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{name: "pending delete intent bound to another target", want: "bound to another target", mutate: func(t *testing.T, f *fileCleanup) {
			writeDeleteIntent(t, f, smstate.TargetFingerprint(f.server.URL, "previous-credential"), 0)
		}},
		{name: "pending delete intent disagrees with ledger", want: "disagrees with recorded ownership", mutate: func(t *testing.T, f *fileCleanup) {
			writeDeleteIntent(t, f, f.snapshot.TargetFingerprint, 1000)
		}},
		{name: "incomplete target migration", want: "target migration is incomplete", mutate: func(t *testing.T, f *fileCleanup) {
			if err := smstate.WriteMigrationPreview(f.stateDir, smstate.MigrationPreview{SchemaVersion: smstate.SchemaVersion, OldTargetFingerprint: "old", NewTargetFingerprint: f.snapshot.TargetFingerprint,
				SnapshotHash: f.snapshot.SnapshotHash, SourceVersion: version, PlanHash: "plan", WrittenUnixMS: 1,
				Resources: []smstate.MigrationResource{{Kind: "check", Key: "k", ID: 1, Modified: "1", Ownership: smstate.Owned, SpecHash: "s"}}}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := provisionForCleanup(t, "token")
			unpublishSnapshot(t, f)
			tc.mutate(t, f)
			token := f.token
			if tc.token != "" {
				token = tc.token
			}
			before := privateFiles(t, f.stateDir)
			_, requestsBefore := f.sm.observed()
			out := runCLI(t, f.env(false, token), false, "cleanup")
			if !strings.Contains(out, "cleanup refused: ") || !strings.Contains(out, tc.want) || strings.Contains(out, "cleanup receipt") {
				t.Fatalf("refusal output, want %q:\n%s", tc.want, out)
			}
			runCLI(t, f.env(true, token), false, "cleanup", "-confirm="+zeroPlan)
			deletes, requests := f.sm.observed()
			if len(deletes) != 0 || !sameFiles(before, privateFiles(t, f.stateDir)) {
				t.Fatalf("refused custody cleanup mutated: deletes=%v", deletes)
			}
			if tc.token != "" && requests != requestsBefore {
				t.Fatal("substituted credential reached the SM API")
			}
		})
	}
}

// An unknown outcome under custody binding keeps the intent; once SM is re-enabled and the
// emitter republishes its snapshot, the snapshot-bound cleanup recovers the same identity.
func TestCleanupCustodyIntentRecoversAfterSnapshotReturns(t *testing.T) {
	f := provisionForCleanup(t, "token")
	unpublishSnapshot(t, f)
	preview := receiptOf(t, runCLI(t, f.env(false, "token"), true, "cleanup"))
	f.sm.set(func(c *cleanupSM) {
		c.onDelete = func(_ http.ResponseWriter, _ *http.Request, kind string, id int64) bool {
			if kind != "check" || c.failLists {
				return false
			}
			c.removeRemote(kind, id)
			c.failLists = true
			panic(http.ErrAbortHandler)
		}
	})
	receipt := receiptOf(t, runCLI(t, f.env(true, "token"), false, "cleanup", "-confirm="+preview.Plan))
	if receipt.Status != "unknown" || receipt.outcome("check", f.checkID) != "unknown/" {
		t.Fatalf("receipt = %+v", receipt)
	}
	journal, err := smstate.ReadJournal(f.stateDir)
	if err != nil {
		t.Fatalf("pending intent missing: %v", err)
	}
	if kind, id := pendingDelete(t, journal); kind != "check" || id != f.checkID || journal.SnapshotHash == f.snapshot.SnapshotHash {
		t.Fatalf("custody intent = %s/%d bound to %s", kind, id, journal.SnapshotHash)
	}
	f.sm.set(func(c *cleanupSM) { c.failLists, c.onDelete = false, nil })
	if err := smstore.NewFileStore(f.stateDir).PublishSnapshot(context.Background(), &f.snapshot); err != nil {
		t.Fatal(err)
	}
	if out := runCLI(t, f.env(true, "token"), false); !strings.Contains(out, "pending operation") {
		t.Fatalf("ordinary apply consumed cleanup custody:\n%s", out)
	}
	recovery := receiptOf(t, runCLI(t, f.env(false, "token"), true, "cleanup"))
	if got := recovery.outcome("check", f.checkID); got != "absent/" {
		t.Fatalf("recovery preview check = %s", got)
	}
	final := receiptOf(t, runCLI(t, f.env(true, "token"), true, "cleanup", "-confirm="+recovery.Plan))
	if final.Status != "complete" {
		t.Fatalf("recovery = %+v", final)
	}
	if d := loadDoc(t, f.stateDir); d.Provision.Pending != nil || d.Provision.Registration != nil || len(d.Provision.Ownership.Resources) != 0 {
		t.Fatalf("recovered custody = %+v", d.Provision)
	}
}
