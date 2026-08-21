// SPDX-License-Identifier: AGPL-3.0-only

// Package smstate owns the private, version-bound handoff between the synthkit process and the
// one-shot Synthetic Monitoring provisioner. It contains no API client and no construct imports.
package smstate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	SchemaVersion    = 1
	SnapshotFilename = "sm-snapshot-v1.json"
	RegistrationFile = "sm-registration-v1.json"
	OwnershipFile    = "sm-ownership-v1.json"
	AdoptionFile     = "sm-adoption-preview-v1.json"
	MigrationFile    = "sm-target-migration-preview-v1.json"
	JournalFilename  = "sm-pending-v1.json"
	LockFilename     = "sm-provision-v1.lock"
)

type Label struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type ProbeSpec struct {
	Key       string  `json:"key"`
	Name      string  `json:"name"`
	Region    string  `json:"region"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type CheckSpec struct {
	Key         string  `json:"key"`
	Source      string  `json:"source"`
	Job         string  `json:"job"`
	Target      string  `json:"target"`
	FrequencyMS int     `json:"frequency_ms"`
	TimeoutMS   int     `json:"timeout_ms"`
	ProbeKey    string  `json:"probe_key"`
	Labels      []Label `json:"labels"`
}

type Snapshot struct {
	SchemaVersion     int         `json:"schema_version"`
	CreatedUnixMS     int64       `json:"created_unix_ms"`
	SnapshotHash      string      `json:"snapshot_hash"`
	TargetFingerprint string      `json:"target_fingerprint"`
	SourceVersion     string      `json:"source_version"`
	Probes            []ProbeSpec `json:"probes"`
	Checks            []CheckSpec `json:"checks"`
}

type Ownership string

const (
	Owned   Ownership = "owned"
	Adopted Ownership = "adopted"
)

type Resource struct {
	Kind          string    `json:"kind"`
	Key           string    `json:"key"`
	ID            int64     `json:"id"`
	Modified      float64   `json:"modified"`
	ConfigVersion string    `json:"config_version"`
	Ownership     Ownership `json:"ownership"`
	SpecHash      string    `json:"spec_hash,omitempty"`
}

type Registration struct {
	SchemaVersion     int        `json:"schema_version"`
	SnapshotHash      string     `json:"snapshot_hash"`
	TargetFingerprint string     `json:"target_fingerprint"`
	SourceVersion     string     `json:"source_version"`
	WrittenUnixMS     int64      `json:"written_unix_ms"`
	Resources         []Resource `json:"resources"`
}

// OwnershipLedger is the durable remote-resource ownership record. Unlike Registration it is
// deliberately independent of one snapshot hash and source version, so a version-matched
// provisioner can reconcile a changed snapshot by recorded API ID. TargetFingerprint remains a
// hard tenant/credential boundary; changing it requires explicit operator migration or adoption.
type OwnershipLedger struct {
	SchemaVersion     int        `json:"schema_version"`
	TargetFingerprint string     `json:"target_fingerprint"`
	WrittenUnixMS     int64      `json:"written_unix_ms"`
	Resources         []Resource `json:"resources"`
}

// AdoptionPreview records the exact collision/adoption plan that an operator previewed. Apply
// accepts legacy adoption only when this private marker matches the current snapshot and plan.
type AdoptionPreview struct {
	SchemaVersion     int    `json:"schema_version"`
	SnapshotHash      string `json:"snapshot_hash"`
	TargetFingerprint string `json:"target_fingerprint"`
	SourceVersion     string `json:"source_version"`
	PlanHash          string `json:"plan_hash"`
	WrittenUnixMS     int64  `json:"written_unix_ms"`
}

// MigrationResource binds target migration approval to every resource in the existing ownership
// ledger as it was observed through the candidate target credentials. SpecHash covers only the
// managed remote specification; Modified binds the preview to the exact remote revision.
type MigrationResource struct {
	Kind      string    `json:"kind"`
	Key       string    `json:"key"`
	ID        int64     `json:"id"`
	Modified  string    `json:"modified"`
	Ownership Ownership `json:"ownership"`
	SpecHash  string    `json:"spec_hash"`
}

// MigrationPreview records the exact target transition and reconciliation plan an operator
// reviewed. Apply accepts it only for a short window and only while every remote resource and plan
// remains identical.
type MigrationPreview struct {
	SchemaVersion        int                 `json:"schema_version"`
	OldTargetFingerprint string              `json:"old_target_fingerprint"`
	NewTargetFingerprint string              `json:"new_target_fingerprint"`
	SnapshotHash         string              `json:"snapshot_hash"`
	SourceVersion        string              `json:"source_version"`
	PlanHash             string              `json:"plan_hash"`
	WrittenUnixMS        int64               `json:"written_unix_ms"`
	Resources            []MigrationResource `json:"resources"`
}

type Journal struct {
	SchemaVersion int             `json:"schema_version"`
	Operation     string          `json:"operation"`
	Kind          string          `json:"kind"`
	Key           string          `json:"key"`
	SnapshotHash  string          `json:"snapshot_hash"`
	ExpectedHash  string          `json:"expected_hash"`
	ExpectedSpec  json.RawMessage `json:"expected_spec"`
	StartedUnixMS int64           `json:"started_unix_ms"`
}

func CheckKey(job, target string) string  { return job + "\x00" + target }
func ProbeKey(name, region string) string { return name + "\x00" + region }

func TargetFingerprint(target, credential string) string {
	sum := sha256.Sum256([]byte("synthkit-sm-target-v1\x00" + target + "\x00" + credential))
	return hex.EncodeToString(sum[:])
}

func NewSnapshot(probes []ProbeSpec, checks []CheckSpec, target, credential, sourceVersion string, now time.Time) (Snapshot, error) {
	s := Snapshot{
		SchemaVersion: SchemaVersion, CreatedUnixMS: now.UnixMilli(),
		TargetFingerprint: TargetFingerprint(target, credential), SourceVersion: sourceVersion,
		Probes: append([]ProbeSpec(nil), probes...), Checks: append([]CheckSpec(nil), checks...),
	}
	normalizeSnapshot(&s)
	if err := validateSnapshotShape(s); err != nil {
		return Snapshot{}, err
	}
	hash, err := snapshotHash(s)
	if err != nil {
		return Snapshot{}, err
	}
	s.SnapshotHash = hash
	return s, nil
}

func RemoveSnapshot(dir string) error {
	err := os.Remove(filepath.Join(dir, SnapshotFilename))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func WriteSnapshot(dir string, snapshot Snapshot) error {
	normalizeSnapshot(&snapshot)
	if err := validateSnapshot(snapshot); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, SnapshotFilename), snapshot)
}

func ReadSnapshot(dir string) (Snapshot, error) {
	var snapshot Snapshot
	if err := readJSON(filepath.Join(dir, SnapshotFilename), &snapshot); err != nil {
		return Snapshot{}, err
	}
	normalizeSnapshot(&snapshot)
	if err := validateSnapshot(snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func WriteRegistration(dir string, registration Registration) error {
	sortResources(registration.Resources)
	if registration.SchemaVersion != SchemaVersion {
		return fmt.Errorf("smstate: registration schema mismatch")
	}
	return writeJSON(filepath.Join(dir, RegistrationFile), registration)
}

func WriteOwnership(dir string, ledger OwnershipLedger) error {
	sortResources(ledger.Resources)
	if err := validateOwnership(ledger); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, OwnershipFile), ledger)
}

func ReadOwnership(dir, targetFingerprint string) (OwnershipLedger, error) {
	ledger, err := ReadOwnershipState(dir)
	if err != nil {
		return OwnershipLedger{}, err
	}
	if ledger.TargetFingerprint != targetFingerprint {
		return OwnershipLedger{}, fmt.Errorf("smstate: ownership target mismatch")
	}
	return ledger, nil
}

// ReadOwnershipState reads and validates ownership without accepting it for a target. It exists so
// the provisioner can inspect an old target binding before an explicit preview-bound migration.
func ReadOwnershipState(dir string) (OwnershipLedger, error) {
	var ledger OwnershipLedger
	if err := readJSON(filepath.Join(dir, OwnershipFile), &ledger); err != nil {
		return OwnershipLedger{}, err
	}
	if err := validateOwnership(ledger); err != nil {
		return OwnershipLedger{}, err
	}
	return ledger, nil
}

// OwnershipFromRegistration imports recorded IDs from an older activation artifact without
// weakening the current snapshot activation check. It is used only when no durable ledger exists.
func OwnershipFromRegistration(dir, targetFingerprint string) (OwnershipLedger, error) {
	var registration Registration
	if err := readJSON(filepath.Join(dir, RegistrationFile), &registration); err != nil {
		return OwnershipLedger{}, err
	}
	if registration.SchemaVersion != SchemaVersion {
		return OwnershipLedger{}, fmt.Errorf("smstate: registration schema mismatch")
	}
	ledger := OwnershipLedger{
		SchemaVersion: SchemaVersion, TargetFingerprint: registration.TargetFingerprint,
		WrittenUnixMS: registration.WrittenUnixMS, Resources: registration.Resources,
	}
	if err := validateOwnership(ledger); err != nil {
		return OwnershipLedger{}, err
	}
	if ledger.TargetFingerprint != targetFingerprint {
		return OwnershipLedger{}, fmt.Errorf("smstate: ownership target mismatch")
	}
	return ledger, nil
}

func validateOwnership(ledger OwnershipLedger) error {
	if ledger.SchemaVersion != SchemaVersion || ledger.TargetFingerprint == "" {
		return fmt.Errorf("smstate: invalid ownership ledger")
	}
	seen := make(map[string]struct{}, len(ledger.Resources))
	for _, resource := range ledger.Resources {
		key := resource.Kind + "\x00" + resource.Key
		if (resource.Kind != "probe" && resource.Kind != "check") || resource.Key == "" || resource.ID <= 0 || resource.Modified <= 0 ||
			(resource.Ownership != Owned && resource.Ownership != Adopted) {
			return fmt.Errorf("smstate: invalid ownership resource")
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("smstate: duplicate ownership resource")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func WriteAdoptionPreview(dir string, preview AdoptionPreview) error {
	if err := validateAdoptionPreview(preview); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, AdoptionFile), preview)
}

func ReadAdoptionPreview(dir string) (AdoptionPreview, error) {
	var preview AdoptionPreview
	if err := readJSON(filepath.Join(dir, AdoptionFile), &preview); err != nil {
		return AdoptionPreview{}, err
	}
	if err := validateAdoptionPreview(preview); err != nil {
		return AdoptionPreview{}, err
	}
	return preview, nil
}

func RemoveAdoptionPreview(dir string) error {
	err := os.Remove(filepath.Join(dir, AdoptionFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func validateAdoptionPreview(preview AdoptionPreview) error {
	if preview.SchemaVersion != SchemaVersion || preview.SnapshotHash == "" || preview.TargetFingerprint == "" || preview.SourceVersion == "" || preview.PlanHash == "" {
		return fmt.Errorf("smstate: invalid adoption preview")
	}
	return nil
}

func WriteMigrationPreview(dir string, preview MigrationPreview) error {
	sort.Slice(preview.Resources, func(i, j int) bool {
		if preview.Resources[i].Kind == preview.Resources[j].Kind {
			return preview.Resources[i].Key < preview.Resources[j].Key
		}
		return preview.Resources[i].Kind < preview.Resources[j].Kind
	})
	if err := validateMigrationPreview(preview); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, MigrationFile), preview)
}

func ReadMigrationPreview(dir string) (MigrationPreview, error) {
	var preview MigrationPreview
	if err := readJSON(filepath.Join(dir, MigrationFile), &preview); err != nil {
		return MigrationPreview{}, err
	}
	if err := validateMigrationPreview(preview); err != nil {
		return MigrationPreview{}, err
	}
	return preview, nil
}

func RemoveMigrationPreview(dir string) error {
	err := os.Remove(filepath.Join(dir, MigrationFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func validateMigrationPreview(preview MigrationPreview) error {
	if preview.SchemaVersion != SchemaVersion || preview.OldTargetFingerprint == "" ||
		preview.NewTargetFingerprint == "" || preview.OldTargetFingerprint == preview.NewTargetFingerprint ||
		preview.SnapshotHash == "" || preview.SourceVersion == "" || preview.PlanHash == "" ||
		preview.WrittenUnixMS <= 0 || len(preview.Resources) == 0 {
		return fmt.Errorf("smstate: invalid target migration preview")
	}
	seen := make(map[string]struct{}, len(preview.Resources))
	for _, resource := range preview.Resources {
		key := resource.Kind + "\x00" + resource.Key
		if (resource.Kind != "probe" && resource.Kind != "check") || resource.Key == "" ||
			resource.ID <= 0 || resource.Modified == "" || resource.SpecHash == "" ||
			(resource.Ownership != Owned && resource.Ownership != Adopted) {
			return fmt.Errorf("smstate: invalid target migration resource")
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("smstate: duplicate target migration resource")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func ReadRegistration(dir string, snapshot Snapshot) (Registration, error) {
	registration, err := ReadRegistrationState(dir, snapshot)
	if err != nil {
		return Registration{}, err
	}
	if err := ValidateRegistration(snapshot, registration); err != nil {
		return Registration{}, err
	}
	return registration, nil
}

// ReadRegistrationState reads a snapshot-bound registration that may be incomplete while the
// one-shot provisioner is persisting successful resources one at a time. The emitter must use
// ReadRegistration, which additionally requires the complete expected resource set.
func ReadRegistrationState(dir string, snapshot Snapshot) (Registration, error) {
	var registration Registration
	if err := readJSON(filepath.Join(dir, RegistrationFile), &registration); err != nil {
		return Registration{}, err
	}
	if err := validateRegistrationResources(snapshot, registration, false); err != nil {
		return Registration{}, err
	}
	return registration, nil
}

func ValidateRegistration(snapshot Snapshot, registration Registration) error {
	return validateRegistrationResources(snapshot, registration, true)
}

func validateRegistrationResources(snapshot Snapshot, registration Registration, requireComplete bool) error {
	if registration.SchemaVersion != SchemaVersion || registration.SnapshotHash != snapshot.SnapshotHash ||
		registration.TargetFingerprint != snapshot.TargetFingerprint || registration.SourceVersion != snapshot.SourceVersion {
		return fmt.Errorf("smstate: stale or incompatible registration")
	}
	expected := make(map[string]string, len(snapshot.Probes)+len(snapshot.Checks))
	for _, probe := range snapshot.Probes {
		expected["probe\x00"+probe.Key] = "probe"
	}
	for _, check := range snapshot.Checks {
		expected["check\x00"+check.Key] = "check"
	}
	seen := make(map[string]struct{}, len(registration.Resources))
	for _, resource := range registration.Resources {
		key := resource.Kind + "\x00" + resource.Key
		if _, ok := expected[key]; !ok || resource.ID <= 0 || (resource.Ownership != Owned && resource.Ownership != Adopted) {
			return fmt.Errorf("smstate: invalid registration resource")
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("smstate: duplicate registration resource")
		}
		seen[key] = struct{}{}
		if resource.Modified <= 0 {
			return fmt.Errorf("smstate: invalid resource modified value")
		}
		if resource.Kind == "check" {
			if resource.ConfigVersion != ConfigVersion(resource.Modified) {
				return fmt.Errorf("smstate: invalid check modified/config-version binding")
			}
		}
	}
	if requireComplete && len(seen) != len(expected) {
		return fmt.Errorf("smstate: incomplete registration")
	}
	return nil
}

// ConfigVersion converts the SM API's Unix-seconds float timestamp to the nanosecond decimal
// join key documented by signals/sm.md (modified x 1e9).
func ConfigVersion(modified float64) string {
	return strconv.FormatInt(int64(modified*1_000_000_000), 10)
}

func ResourceByKey(registration Registration, kind, key string) (Resource, bool) {
	for _, resource := range registration.Resources {
		if resource.Kind == kind && resource.Key == key {
			return resource, true
		}
	}
	return Resource{}, false
}

func WriteJournal(dir string, journal Journal) error {
	if !validJournal(journal) {
		return fmt.Errorf("smstate: invalid pending journal")
	}
	return writeJSON(filepath.Join(dir, JournalFilename), journal)
}

func ReadJournal(dir string) (Journal, error) {
	var journal Journal
	if err := readJSON(filepath.Join(dir, JournalFilename), &journal); err != nil {
		return Journal{}, err
	}
	if !validJournal(journal) {
		return Journal{}, fmt.Errorf("smstate: invalid pending journal")
	}
	return journal, nil
}

func validJournal(journal Journal) bool {
	return journal.SchemaVersion == SchemaVersion && journal.Operation != "" && journal.Kind != "" && journal.Key != "" &&
		journal.SnapshotHash != "" && journal.ExpectedHash != "" && len(journal.ExpectedSpec) > 0 &&
		json.Valid(journal.ExpectedSpec) && SpecHash(json.RawMessage(journal.ExpectedSpec)) == journal.ExpectedHash
}

func RemoveJournal(dir string) error {
	err := os.Remove(filepath.Join(dir, JournalFilename))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// AcquireLock creates an exclusive owner-only lock. A pre-existing lock is never guessed stale;
// the operator must inspect and remove it explicitly after establishing no provisioner is active.
func AcquireLock(dir string) (func() error, error) {
	if err := ensurePrivateDir(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, LockFilename)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("smstate: acquire provision lock: %w", err)
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return func() error { return os.Remove(path) }, nil
}

func SpecHash(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		// All callers pass structs decoded from JSON or built from closed internal types. Reaching
		// this path is a programming error; hashing empty bytes would silently approve the wrong plan.
		panic("smstate: cannot hash unsupported value")
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func sortResources(resources []Resource) {
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Kind == resources[j].Kind {
			return resources[i].Key < resources[j].Key
		}
		return resources[i].Kind < resources[j].Kind
	})
}

func RuntimeDir(snapshotPath string) string {
	return filepath.Join(filepath.Dir(snapshotPath), "runtime")
}

func normalizeSnapshot(snapshot *Snapshot) {
	for i := range snapshot.Checks {
		sort.Slice(snapshot.Checks[i].Labels, func(a, b int) bool {
			if snapshot.Checks[i].Labels[a].Name == snapshot.Checks[i].Labels[b].Name {
				return snapshot.Checks[i].Labels[a].Value < snapshot.Checks[i].Labels[b].Value
			}
			return snapshot.Checks[i].Labels[a].Name < snapshot.Checks[i].Labels[b].Name
		})
	}
	sort.Slice(snapshot.Probes, func(i, j int) bool { return snapshot.Probes[i].Key < snapshot.Probes[j].Key })
	sort.Slice(snapshot.Checks, func(i, j int) bool { return snapshot.Checks[i].Key < snapshot.Checks[j].Key })
}

func validateSnapshot(snapshot Snapshot) error {
	if err := validateSnapshotShape(snapshot); err != nil {
		return err
	}
	hash, err := snapshotHash(snapshot)
	if err != nil {
		return err
	}
	if snapshot.SnapshotHash == "" || snapshot.SnapshotHash != hash {
		return fmt.Errorf("smstate: snapshot hash mismatch")
	}
	return nil
}

func validateSnapshotShape(snapshot Snapshot) error {
	if snapshot.SchemaVersion != SchemaVersion || snapshot.TargetFingerprint == "" || snapshot.SourceVersion == "" || len(snapshot.Checks) == 0 || len(snapshot.Probes) == 0 {
		return fmt.Errorf("smstate: incomplete snapshot")
	}
	probes := make(map[string]struct{}, len(snapshot.Probes))
	for _, probe := range snapshot.Probes {
		if probe.Key == "" || probe.Name == "" || probe.Region == "" || probe.Key != ProbeKey(probe.Name, probe.Region) {
			return fmt.Errorf("smstate: invalid probe specification")
		}
		if _, duplicate := probes[probe.Key]; duplicate {
			return fmt.Errorf("smstate: duplicate probe specification")
		}
		probes[probe.Key] = struct{}{}
	}
	checks := make(map[string]struct{}, len(snapshot.Checks))
	for _, check := range snapshot.Checks {
		if check.Key == "" || check.Job == "" || check.Target == "" || check.FrequencyMS <= 0 || check.TimeoutMS <= 0 || check.Key != CheckKey(check.Job, check.Target) {
			return fmt.Errorf("smstate: invalid check specification")
		}
		if _, ok := probes[check.ProbeKey]; !ok {
			return fmt.Errorf("smstate: check references unknown probe")
		}
		if _, duplicate := checks[check.Key]; duplicate {
			return fmt.Errorf("smstate: duplicate check specification")
		}
		checks[check.Key] = struct{}{}
	}
	return nil
}

func snapshotHash(snapshot Snapshot) (string, error) {
	copy := snapshot
	copy.CreatedUnixMS = 0
	copy.SnapshotHash = ""
	b, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func readJSON(path string, out any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return fmt.Errorf("smstate: %q must be a mode-0600 regular file", filepath.Base(path))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("smstate: decode %q: %w", filepath.Base(path), err)
	}
	return nil
}

func writeJSON(path string, value any) error {
	dir := filepath.Dir(path)
	if err := ensurePrivateDir(dir); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("smstate: %q is not a regular file", filepath.Base(path))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(dir, ".smstate-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	d, err := os.Open(dir)
	if err == nil {
		err = d.Sync()
		_ = d.Close()
	}
	return err
}

func ensurePrivateDir(path string) error {
	// This pre-scan rejects symlinks in stable paths, but Lstat plus MkdirAll cannot eliminate a
	// concurrent component-replacement race. An absolute guarantee would require walking directory
	// descriptors with openat-style O_NOFOLLOW handling, which Go's portable os API does not expose.
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(abs), string(filepath.Separator)), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("smstate: path component is a symlink")
		}
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("smstate: runtime path is not a real directory")
	}
	return os.Chmod(abs, 0o700)
}
