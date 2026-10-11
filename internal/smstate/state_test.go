// SPDX-License-Identifier: AGPL-3.0-only

package smstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func privateTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func testSnapshot(t *testing.T) Snapshot {
	t.Helper()
	probe := ProbeSpec{Key: ProbeKey("synthkit-private", "EMEA"), Name: "synthkit-private", Region: "EMEA", Latitude: 50.1109, Longitude: 8.6821}
	check := CheckSpec{Key: CheckKey("api", "https://api.example/health"), Source: "test", Job: "api", Target: "https://api.example/health", FrequencyMS: 60000, TimeoutMS: 3000, ProbeKey: probe.Key}
	snapshot, err := NewSnapshot([]ProbeSpec{probe}, []CheckSpec{check}, "https://sm.example", "token", "v1.2.3", time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestSnapshotAndRegistrationRoundTrip(t *testing.T) {
	dir := privateTempDir(t)
	snapshot := testSnapshot(t)
	if err := WriteSnapshot(dir, snapshot); err != nil {
		t.Fatal(err)
	}
	read, err := ReadSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if read.SnapshotHash != snapshot.SnapshotHash {
		t.Fatalf("hash = %q, want %q", read.SnapshotHash, snapshot.SnapshotHash)
	}
	registration := Registration{
		SchemaVersion: ActivationSchemaVersion, SnapshotHash: snapshot.SnapshotHash,
		TargetFingerprint: snapshot.TargetFingerprint, SourceVersion: snapshot.SourceVersion,
		Resources: []Resource{
			{Kind: "probe", Key: snapshot.Probes[0].Key, ID: 4, Modified: 123, Ownership: Owned},
			{Kind: "check", Key: snapshot.Checks[0].Key, ID: 5, Modified: 123.456789, ConfigVersion: ConfigVersion(123.456789), Ownership: Owned},
		},
	}
	if err := WriteRegistration(dir, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRegistration(dir, snapshot); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{SnapshotFilename, RegistrationFile} {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 || !info.Mode().IsRegular() {
			t.Fatalf("%s mode/type = %v", name, info.Mode())
		}
	}
}

func TestRegistrationRejectsStaleIncompleteAndModifiedMismatch(t *testing.T) {
	snapshot := testSnapshot(t)
	base := Registration{SchemaVersion: ActivationSchemaVersion, SnapshotHash: snapshot.SnapshotHash, TargetFingerprint: snapshot.TargetFingerprint, SourceVersion: snapshot.SourceVersion, Resources: []Resource{
		{Kind: "probe", Key: snapshot.Probes[0].Key, ID: 4, Modified: 9, Ownership: Owned},
		{Kind: "check", Key: snapshot.Checks[0].Key, ID: 5, Modified: 10, ConfigVersion: ConfigVersion(10), Ownership: Owned},
	}}
	if err := ValidateRegistration(snapshot, base); err != nil {
		t.Fatal(err)
	}
	bad := base
	bad.SnapshotHash = "stale"
	if err := ValidateRegistration(snapshot, bad); err == nil {
		t.Fatal("stale registration accepted")
	}
	bad = base
	bad.Resources = bad.Resources[:1]
	if err := ValidateRegistration(snapshot, bad); err == nil {
		t.Fatal("incomplete registration accepted")
	}
	bad = base
	bad.Resources = append([]Resource(nil), base.Resources...)
	bad.Resources[1].ConfigVersion = "wrong"
	if err := ValidateRegistration(snapshot, bad); err == nil {
		t.Fatal("modified/config-version mismatch accepted")
	}
}

func TestPrivateFilesRejectSymlinksAndWrongModes(t *testing.T) {
	dir := privateTempDir(t)
	snapshot := testSnapshot(t)
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteSnapshot(link, snapshot); err == nil {
		t.Fatal("symlink runtime directory accepted")
	}
	if err := WriteSnapshot(target, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(target, SnapshotFilename), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSnapshot(target); err == nil {
		t.Fatal("non-private snapshot accepted")
	}
}

func TestLockAndJournalCrashBoundary(t *testing.T) {
	dir := privateTempDir(t)
	release, err := AcquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireLock(dir); err == nil {
		t.Fatal("concurrent lock accepted")
	}
	expected := json.RawMessage(`{"job":"api"}`)
	journal := Journal{SchemaVersion: SchemaVersion, Operation: "create", Kind: "check", Key: "a", SnapshotHash: "b", ExpectedHash: SpecHash(expected), ExpectedSpec: expected, StartedUnixMS: 1}
	if err := WriteJournal(dir, journal); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadJournal(dir); err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := RemoveJournal(dir); err != nil {
		t.Fatal(err)
	}
}

// TestJournalReplayHandoffPreservesExactExpectedSpec proves that a retained
// journal gives the provisioner an exact, hash-bound operation to inspect and
// replay after a credential or transport failure; it is not merely a marker.
func TestJournalReplayHandoffPreservesExactExpectedSpec(t *testing.T) {
	dir := privateTempDir(t)
	expected := json.RawMessage(`{"job":"api","target":"https://api.example/health"}`)
	journal := Journal{
		SchemaVersion: SchemaVersion,
		Operation:     "create",
		Kind:          "check",
		Key:           CheckKey("api", "https://api.example/health"),
		SnapshotHash:  "snapshot",
		ExpectedHash:  SpecHash(expected),
		ExpectedSpec:  expected,
		StartedUnixMS: 1,
	}
	if err := WriteJournal(dir, journal); err != nil {
		t.Fatal(err)
	}
	replayed, err := ReadJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	var expectedObject, replayedObject map[string]string
	if err := json.Unmarshal(expected, &expectedObject); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(replayed.ExpectedSpec, &replayedObject); err != nil {
		t.Fatal(err)
	}
	if replayed.Operation != journal.Operation || replayed.Kind != journal.Kind || replayed.Key != journal.Key || !reflect.DeepEqual(replayedObject, expectedObject) || replayed.ExpectedHash != SpecHash(expected) {
		t.Fatalf("replay handoff changed: %+v", replayed)
	}
	if _, err := os.Lstat(filepath.Join(dir, JournalFilename)); err != nil {
		t.Fatalf("replay journal was removed before provisioner acknowledgement: %v", err)
	}
}

func TestOwnershipLedgerIsSnapshotIndependentButTargetBound(t *testing.T) {
	dir := privateTempDir(t)
	snapshot := testSnapshot(t)
	ledger := OwnershipLedger{
		SchemaVersion: SchemaVersion, TargetFingerprint: snapshot.TargetFingerprint,
		Resources: []Resource{{Kind: "probe", Key: snapshot.Probes[0].Key, ID: 7, Modified: 12, Ownership: Owned}},
	}
	if err := WriteOwnership(dir, ledger); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOwnership(dir, snapshot.TargetFingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOwnership(dir, "different-target"); err == nil {
		t.Fatal("ownership crossed target fingerprint")
	}
	info, err := os.Lstat(filepath.Join(dir, OwnershipFile))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("ownership mode/type = %v, err=%v", info.Mode(), err)
	}
}

func TestOwnershipImportRejectsMismatchedRegistrationSchema(t *testing.T) {
	for name, schema := range map[string]int{"older": ActivationSchemaVersion - 1, "newer": ActivationSchemaVersion + 1} {
		t.Run(name, func(t *testing.T) {
			dir := privateTempDir(t)
			registration := Registration{
				SchemaVersion: schema, TargetFingerprint: "target", WrittenUnixMS: 1,
				Resources: []Resource{{Kind: "probe", Key: "probe", ID: 7, Modified: 12, Ownership: Owned}},
			}
			if err := writeJSON(filepath.Join(dir, RegistrationFile), registration); err != nil {
				t.Fatal(err)
			}
			if _, err := OwnershipFromRegistration(dir, "target"); err == nil || !strings.Contains(err.Error(), "schema mismatch") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestMigrationPreviewIsPrivateAndBindsBothTargetsAndResources(t *testing.T) {
	dir := privateTempDir(t)
	preview := MigrationPreview{
		SchemaVersion: SchemaVersion, OldTargetFingerprint: "old", NewTargetFingerprint: "new",
		SnapshotHash: "snapshot", SourceVersion: "version", PlanHash: "plan", WrittenUnixMS: 10,
		Resources: []MigrationResource{
			{Kind: "check", Key: "check", ID: 8, Modified: "11", Ownership: Adopted, SpecHash: "check-spec"},
			{Kind: "probe", Key: "probe", ID: 7, Modified: "10", Ownership: Owned, SpecHash: "probe-spec"},
		},
	}
	if err := WriteMigrationPreview(dir, preview); err != nil {
		t.Fatal(err)
	}
	got, err := ReadMigrationPreview(dir)
	if err != nil || got.OldTargetFingerprint != "old" || got.NewTargetFingerprint != "new" || len(got.Resources) != 2 {
		t.Fatalf("migration preview = %+v, err=%v", got, err)
	}
	info, err := os.Lstat(filepath.Join(dir, MigrationFile))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("migration preview mode/type = %v, err=%v", info.Mode(), err)
	}
	preview.NewTargetFingerprint = preview.OldTargetFingerprint
	if err := WriteMigrationPreview(dir, preview); err == nil {
		t.Fatal("migration preview accepted identical targets")
	}
}

func TestConfigVersionUsesModifiedSecondsTimesOneBillion(t *testing.T) {
	if got, want := ConfigVersion(1_725_000_000.123456), "1725000000123456000"; got != want {
		t.Fatalf("ConfigVersion = %s, want %s", got, want)
	}
}

func TestActivationHashIgnoresReleaseAndCreationTimeButBindsEverySpecField(t *testing.T) {
	a := testSnapshot(t)
	b, err := NewSnapshot(a.Probes, a.Checks, "https://sm.example", "token", "new-release", time.Unix(200, 0))
	if err != nil {
		t.Fatal(err)
	}
	if a.SnapshotHash != b.SnapshotHash || a.SourceVersion == b.SourceVersion {
		t.Fatal("release/creation time still bound activation")
	}
	nilChecks := append([]CheckSpec(nil), a.Checks...)
	nilChecks[0].Labels = nil
	emptyChecks := append([]CheckSpec(nil), a.Checks...)
	emptyChecks[0].Labels = []Label{}
	n, _ := NewSnapshot(a.Probes, nilChecks, "https://sm.example", "token", "a", time.Now())
	e, _ := NewSnapshot(a.Probes, emptyChecks, "https://sm.example", "token", "b", time.Now())
	if n.SnapshotHash != e.SnapshotHash {
		t.Fatal("nil/empty labels hash differently")
	}
	mutations := map[string]func(*Snapshot){
		"frequency": func(s *Snapshot) { s.Checks[0].FrequencyMS++ }, "timeout": func(s *Snapshot) { s.Checks[0].TimeoutMS++ },
		"labels": func(s *Snapshot) { s.Checks[0].Labels = []Label{{Name: "team", Value: "changed"}} },
		"source": func(s *Snapshot) { s.Checks[0].Source = "other" }, "latitude": func(s *Snapshot) { s.Probes[0].Latitude++ }, "longitude": func(s *Snapshot) { s.Probes[0].Longitude++ },
		"region-and-binding": func(s *Snapshot) {
			s.Probes[0].Region = "AMER"
			s.Probes[0].Key = ProbeKey(s.Probes[0].Name, "AMER")
			s.Checks[0].ProbeKey = s.Probes[0].Key
		},
		"probe-name-and-binding": func(s *Snapshot) {
			s.Probes[0].Name = "other"
			s.Probes[0].Key = ProbeKey("other", s.Probes[0].Region)
			s.Checks[0].ProbeKey = s.Probes[0].Key
		},
		"target": func(s *Snapshot) { s.TargetFingerprint = "other" }, "schema": func(s *Snapshot) { s.SchemaVersion++ },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			s := testSnapshot(t)
			mutate(&s)
			h, err := snapshotHash(s)
			if err != nil {
				t.Fatal(err)
			}
			if h == a.SnapshotHash {
				t.Fatal("bound field omitted from hash")
			}
		})
	}
}
func TestFirstActivationFormatRequiresExplicitApplyAndPreservesLegacyOwnership(t *testing.T) {
	dir := privateTempDir(t)
	s := testSnapshot(t)
	legacy := Registration{SchemaVersion: SchemaVersion, TargetFingerprint: s.TargetFingerprint, Resources: []Resource{{Kind: "probe", Key: s.Probes[0].Key, ID: 7, Modified: 10, Ownership: Owned}}}
	if err := writeJSON(filepath.Join(dir, LegacyRegistrationFile), legacy); err != nil {
		t.Fatal(err)
	}
	if err := WriteSnapshot(dir, s); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRegistration(dir, s); !os.IsNotExist(err) {
		t.Fatal("legacy v1 unexpectedly activated", err)
	}
	ledger, err := OwnershipFromRegistration(dir, s.TargetFingerprint)
	if err != nil || ledger.Resources[0].ID != 7 {
		t.Fatal("legacy ownership import lost", err)
	}
	if _, err := os.Stat(filepath.Join(dir, LegacyRegistrationFile)); err != nil {
		t.Fatal("legacy evidence erased", err)
	}
}

func TestSpecHashRejectsUnsupportedValue(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("SpecHash accepted an unsupported value")
		}
	}()
	_ = SpecHash(make(chan int))
}
