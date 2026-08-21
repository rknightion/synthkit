// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/construct/sm"
	"github.com/rknightion/synthkit/internal/smstate"
)

func smBlueprint(t *testing.T) *blueprint.Resolved {
	t.Helper()
	return mustLoad(t, []byte(`
name: sm-test
environments:
  - name: prod
    cloud: {provider: aws, account_id: "000000000001", region: us-east-1, vpc_id: vpc-test01}
features:
  synthetic_monitoring:
    enabled: true
    checks:
      - {name: api-health, target: "https://api.example/health", labels: {team: platform}}
`))
}

func TestSMHandoffSuppressesUntilMatchingRegistrationThenInjects(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{SnapshotPath: filepath.Join(dir, "control-state.json"), SMURL: "https://sm.example"}
	first := smBlueprint(t)
	handoff, err := prepareSMHandoff([]*blueprint.Resolved{first}, cfg, "v1.2.3", time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !handoff.Declared || handoff.Registered || hasSMConstruct(first) {
		t.Fatalf("unregistered handoff = %+v constructs=%v", handoff, first.Constructs)
	}
	runtimeDir := smstate.RuntimeDir(cfg.SnapshotPath)
	snapshot, err := smstate.ReadSnapshot(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	registration := smstate.Registration{
		SchemaVersion: smstate.SchemaVersion, SnapshotHash: snapshot.SnapshotHash,
		TargetFingerprint: snapshot.TargetFingerprint, SourceVersion: snapshot.SourceVersion,
		Resources: []smstate.Resource{
			{Kind: "probe", Key: snapshot.Probes[0].Key, ID: 11, Modified: 10, Ownership: smstate.Owned},
			{Kind: "check", Key: snapshot.Checks[0].Key, ID: 12, Modified: 1725000000.123456, ConfigVersion: smstate.ConfigVersion(1725000000.123456), Ownership: smstate.Owned},
		},
	}
	wantConfigVersion := smstate.ConfigVersion(1725000000.123456)
	if err := smstate.WriteRegistration(runtimeDir, registration); err != nil {
		t.Fatal(err)
	}
	second := smBlueprint(t)
	handoff, err = prepareSMHandoff([]*blueprint.Resolved{second}, cfg, "v1.2.3", time.Unix(200, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !handoff.Registered || !hasSMConstruct(second) {
		t.Fatalf("registered handoff = %+v", handoff)
	}
	for _, instance := range second.Constructs {
		if instance.Kind == sm.Kind {
			got := instance.Config.(*sm.Config).Registration[sm.CheckKey("api-health", "https://api.example/health")]
			if got != wantConfigVersion {
				t.Fatalf("injected config version = %q", got)
			}
		}
	}
	info, err := os.Lstat(filepath.Join(runtimeDir, smstate.SnapshotFilename))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode = %v err=%v", info.Mode(), err)
	}
}

func hasSMConstruct(resolved *blueprint.Resolved) bool {
	for _, instance := range resolved.Constructs {
		if instance.Kind == sm.Kind {
			return true
		}
	}
	return false
}

func TestSMHandoffInvalidatesPriorSnapshotWhenNoSMLaneIsSelected(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{SnapshotPath: filepath.Join(dir, "control-state.json"), SMURL: "https://sm.example", SMToken: "token"}
	selected := smBlueprint(t)
	if _, err := prepareSMHandoff([]*blueprint.Resolved{selected}, cfg, "v1.2.3", time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(smstate.RuntimeDir(cfg.SnapshotPath), smstate.SnapshotFilename)
	if _, err := os.Lstat(path); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareSMHandoff(nil, cfg, "v1.2.3", time.Unix(200, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("prior snapshot remained reusable: %v", err)
	}
}
