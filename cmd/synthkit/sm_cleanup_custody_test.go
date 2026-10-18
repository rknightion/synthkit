// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"path/filepath"
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

// TestSMCleanupCustodyWriteNeverReactivatesEmission walks the emitter through SM turned off
// (startup-disabled or deselected), a cleanup-only custody write, and SM turned back on: the
// emitter publishes no snapshot while off, and once on again it stays suppressed until a later
// ordinary apply, because a custody write can never leave a registration behind.
func TestSMCleanupCustodyWriteNeverReactivatesEmission(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"file", "kubernetes"} {
		for _, disable := range []string{"startup-disabled", "deselected"} {
			t.Run(mode+"/"+disable, func(t *testing.T) {
				dir, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				cfg := &config.Config{StateBackend: mode, SnapshotPath: filepath.Join(dir, "control.json"), SMURL: "https://sm.example", SMToken: "token", StateCASMaxAttempts: 5}
				var store smstore.Store
				// provision runs one provisioner-side write with the authority each backend requires.
				provision := func(write func(smstore.Store) error) error { return write(store) }
				if mode == "kubernetes" {
					a := smstoretest.New(t, "sm")
					defer a.Close()
					a.Permit()
					cfg.StateSMConfigMap, cfg.SMProvisionLeaseName, cfg.HANamespace = "sm", "provision", "test"
					backend, err := control.NewKubernetesBackendForClient(control.KubernetesBackendOptions{Gate: ha.AlwaysLeader{}, Namespace: "test", Objects: map[control.Key]string{control.SMState: "sm"}, RequestTimeout: 2 * time.Second, MaxDocumentBytes: control.MaxStateDocumentBytes}, a.Server.URL, a.Server.Client())
					if err != nil {
						t.Fatal(err)
					}
					if store, err = configuredSMStore(cfg, backend); err != nil {
						t.Fatal(err)
					}
				} else {
					runtime := smstate.RuntimeDir(cfg.SnapshotPath)
					store = smstore.NewFileStore(runtime)
					provision = func(write func(smstore.Store) error) error {
						owned, release, err := smstore.AcquireFileStore(runtime)
						if err != nil {
							return err
						}
						return errors.Join(write(owned), release())
					}
				}

				// SM on: publish, record an applied registration, and emit.
				if _, err := prepareSMHandoff(ctx, []*blueprint.Resolved{smBlueprint(t)}, cfg, "v1", time.Unix(100, 0), store, true); err != nil {
					t.Fatal(err)
				}
				doc, err := store.Load(ctx)
				if err != nil || doc.Snapshot == nil {
					t.Fatalf("no published snapshot: %v", err)
				}
				s := *doc.Snapshot
				resources := []smstate.Resource{
					{Kind: "check", Key: s.Checks[0].Key, ID: 12, Modified: 20, ConfigVersion: smstate.ConfigVersion(20), Ownership: smstate.Owned, SpecHash: "check-spec"},
					{Kind: "probe", Key: s.Probes[0].Key, ID: 11, Modified: 10, Ownership: smstate.Owned, SpecHash: "probe-spec"},
				}
				if err := provision(func(w smstore.Store) error {
					return w.UpdateProvision(ctx, smstore.Binding(s), func(p *smstore.ProvisionState) error {
						p.Ownership = &smstate.OwnershipLedger{SchemaVersion: smstate.SchemaVersion, TargetFingerprint: s.TargetFingerprint, WrittenUnixMS: 1, Resources: resources}
						p.Registration = &smstate.Registration{SchemaVersion: smstate.ActivationSchemaVersion, SnapshotHash: s.SnapshotHash, TargetFingerprint: s.TargetFingerprint, SourceVersion: s.SourceVersion, WrittenUnixMS: 1, Resources: resources}
						return nil
					})
				}); err != nil {
					t.Fatal(err)
				}
				on := smBlueprint(t)
				if h, err := prepareSMHandoff(ctx, []*blueprint.Resolved{on}, cfg, "v1", time.Unix(150, 0), store, true); err != nil || !h.Registered || !hasSMConstruct(on) {
					t.Fatalf("applied SM did not activate: %+v %v", h, err)
				}

				// SM off: the boot publishes no snapshot and retains custody evidence.
				off := smBlueprint(t)
				switch disable {
				case "deselected":
					if _, err := prepareSMHandoff(ctx, nil, cfg, "v1", time.Unix(200, 0), store, true); err != nil {
						t.Fatal(err)
					}
				default:
					cfg.StartupDisabledKinds = []string{sm.Kind}
					reg := runner.Catalog()
					if err := applyStartupDisabledKinds(reg, []*blueprint.Resolved{off}, cfg.StartupDisabledKinds); err != nil {
						t.Fatal(err)
					}
					admit := newSMAdmission(cfg, "v1", store, true, smStartupDisabled(cfg), nil)
					if h, err := admit.prepareBoot(ctx, []*blueprint.Resolved{off}, nil, reg, blueprint.RuntimeLimits{}); err != nil || h.Registered || hasSMConstruct(off) {
						t.Fatalf("startup-disabled boot = %+v %v", h, err)
					}
					cfg.StartupDisabledKinds = nil
				}
				doc, err = store.Load(ctx)
				if err != nil || doc.Snapshot != nil || doc.Provision.Registration == nil || doc.Provision.Ownership == nil {
					t.Fatalf("SM off left snapshot=%v or lost custody: %v", doc.Snapshot, err)
				}

				// Cleanup-only custody write: every recorded resource leaves custody.
				if err := provision(func(w smstore.Store) error {
					return w.UpdateCustody(ctx, smstore.Custody(s.TargetFingerprint, doc.Provision), func(p *smstore.ProvisionState) error {
						p.Ownership.Resources, p.Registration = nil, nil
						return nil
					})
				}); err != nil {
					t.Fatal(err)
				}

				// SM on again: the snapshot returns, but nothing registers it, so SM stays suppressed.
				again := smBlueprint(t)
				h, err := prepareSMHandoff(ctx, []*blueprint.Resolved{again}, cfg, "v1", time.Unix(300, 0), store, true)
				if err != nil {
					t.Fatal(err)
				}
				doc, err = store.Load(ctx)
				if err != nil || doc.Snapshot == nil || doc.Provision.Registration != nil {
					t.Fatalf("re-enabled state = %+v %v", doc, err)
				}
				m, l := composedSMTick(t, again)
				if !h.Declared || hasSMConstruct(again) {
					t.Fatalf("re-enabled handoff = %+v", h)
				}
				assertSMSuppressed(t, h, m, l)
			})
		}
	}
}
