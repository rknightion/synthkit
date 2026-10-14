// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/operationalerr"
	"github.com/rknightion/synthkit/internal/smstate"
	"github.com/rknightion/synthkit/internal/smstore"
)

// retainPendingStore simulates a failed pending-consumption write, after the
// actual file adapter has durably persisted authoritative complete registration.
// All earlier writes and all remote operations use the real provisioner/store.
type retainPendingStore struct{ smstore.Store }

func (s retainPendingStore) UpdateProvision(ctx context.Context, b smstore.SnapshotBinding, fn func(*smstore.ProvisionState) error) error {
	d, err := s.Store.Load(ctx)
	if err != nil {
		return err
	}
	hadPending := d.Provision.Pending != nil
	if err := fn(&d.Provision); err != nil {
		return err
	}
	if hadPending && d.Provision.Pending == nil && d.Snapshot != nil && d.Provision.Registration != nil && smstate.ValidateRegistration(*d.Snapshot, *d.Provision.Registration) == nil {
		return errors.New("fixture pending consumption failed")
	}
	return s.Store.UpdateProvision(ctx, b, fn)
}

func TestFileProvisionRetainsCompletePendingHelper(t *testing.T) {
	path := os.Getenv("SYNTHKIT_SM_PENDING_CONFIG")
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.SMProvisionConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	dir := smstate.RuntimeDir(cfg.SnapshotPath)
	store, release, err := smstore.AcquireFileStore(dir)
	if err != nil {
		fmt.Println("lock-contention=true")
		return
	}
	defer func() {
		if err := release(); err != nil {
			t.Error(err)
		}
	}()
	deps := productionProvisionDependencies()
	deps.client = &smClient{base: cfg.SMURL, token: cfg.SMToken, hc: &http.Client{Timeout: 5 * time.Second}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := executeStored(ctx, cfg, retainPendingStore{store}, dir, deps); err == nil || !strings.Contains(err.Error(), "pending operation") {
		t.Fatalf("expected failed pending consumption, got %v", err)
	}
	d, err := store.Load(ctx)
	if err != nil || d.Provision.Pending == nil || d.Provision.Registration == nil || smstate.ValidateRegistration(*d.Snapshot, *d.Provision.Registration) != nil {
		t.Fatalf("complete registration/pending not retained: %v", err)
	}
	fmt.Println("retained-complete-pending=true")
}

type fakeSM struct {
	mu               sync.Mutex
	probes           []smProbe
	checks           []smCheck
	posts            map[string]int
	nextID           int64
	nextModified     float64
	abortCheckCreate bool
	abortDone        chan struct{}
	rejectStatus     int
	responseBody     string
}

func newFakeSM() *fakeSM {
	return &fakeSM{posts: map[string]int{}, nextID: 100, nextModified: 1_725_000_000.1, abortDone: make(chan struct{})}
}

func (f *fakeSM) modified() float64 {
	f.nextModified += 0.001
	return f.nextModified
}

// withState joins real handler observations, including handlers whose response
// was lost. A child process exiting is not a happens-before edge in this process.
// Callbacks may inspect or configure fixture facts, but must not issue HTTP.
func (f *fakeSM) withState(fn func(*fakeSM)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeSM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rejectStatus != 0 {
		w.WriteHeader(f.rejectStatus)
		_, _ = w.Write([]byte(f.responseBody))
		return
	}
	if r.Method == http.MethodPost {
		f.posts[r.URL.Path]++
	}
	switch r.URL.Path {
	case "/api/v1/probe/list":
		_ = json.NewEncoder(w).Encode(f.probes)
	case "/api/v1/check/list":
		_ = json.NewEncoder(w).Encode(f.checks)
	case "/api/v1/probe/add":
		var probe smProbe
		_ = json.NewDecoder(r.Body).Decode(&probe)
		probe.ID, probe.Modified = f.nextID, f.modified()
		f.nextID++
		f.probes = append(f.probes, probe)
		_ = json.NewEncoder(w).Encode(addProbeResponse{Probe: probe, Token: []byte("never-persist")})
	case "/api/v1/probe/update":
		var probe smProbe
		_ = json.NewDecoder(r.Body).Decode(&probe)
		probe.Modified = f.modified()
		for index := range f.probes {
			if f.probes[index].ID == probe.ID {
				f.probes[index] = probe
			}
		}
		_ = json.NewEncoder(w).Encode(probe)
	case "/api/v1/check/add":
		var check smCheck
		_ = json.NewDecoder(r.Body).Decode(&check)
		check.ID, check.Modified = f.nextID, f.modified()
		f.nextID++
		f.checks = append(f.checks, check)
		if f.abortCheckCreate {
			close(f.abortDone)
			panic(http.ErrAbortHandler)
		}
		_ = json.NewEncoder(w).Encode(check)
	case "/api/v1/check/update":
		var check smCheck
		_ = json.NewDecoder(r.Body).Decode(&check)
		check.Modified = f.modified()
		for index := range f.checks {
			if f.checks[index].ID == check.ID {
				f.checks[index] = check
			}
		}
		_ = json.NewEncoder(w).Encode(check)
	default:
		http.NotFound(w, r)
	}
}

func testSnapshot(t *testing.T, target, token string) (string, smstate.Snapshot) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "runtime")
	probe := smstate.ProbeSpec{Key: smstate.ProbeKey("synthkit-private", "EMEA"), Name: "synthkit-private", Region: "EMEA", Latitude: 50.1109, Longitude: 8.6821}
	check := smstate.CheckSpec{
		Key: smstate.CheckKey("api-health", "https://api.example/health"), Source: "selected-source",
		Job: "api-health", Target: "https://api.example/health", FrequencyMS: 60000, TimeoutMS: 3000,
		ProbeKey: probe.Key, Labels: []smstate.Label{{Name: "team", Value: "platform"}},
	}
	snapshot, err := smstate.NewSnapshot([]smstate.ProbeSpec{probe}, []smstate.CheckSpec{check}, target, token, version, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := smstate.WriteSnapshot(dir, snapshot); err != nil {
		t.Fatal(err)
	}
	return dir, snapshot
}

func testProvisioner(server *httptest.Server, stateDir string, snapshot smstate.Snapshot, apply, adopt bool) *provisioner {
	return &provisioner{
		client: &smClient{base: server.URL, token: "token", hc: server.Client()}, stateDir: stateDir,
		ownership: smstate.OwnershipLedger{
			SchemaVersion: smstate.SchemaVersion, TargetFingerprint: snapshot.TargetFingerprint,
		},
		snapshot: snapshot, registration: smstate.Registration{
			SchemaVersion: smstate.ActivationSchemaVersion, SnapshotHash: snapshot.SnapshotHash,
			TargetFingerprint: snapshot.TargetFingerprint, SourceVersion: snapshot.SourceVersion,
		}, apply: apply, adopt: adopt, now: func() time.Time { return time.Unix(200, 0) },
	}
}

func totalPosts(fake *fakeSM) int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	total := 0
	for _, count := range fake.posts {
		total += count
	}
	return total
}

func migrationFixture(t *testing.T, fake *fakeSM, server *httptest.Server) (string, smstate.Snapshot, smstate.OwnershipLedger) {
	t.Helper()
	stateDir, oldSnapshot := testSnapshot(t, server.URL, "old-token")
	probe := desiredProbe(oldSnapshot.Probes[0])
	probe.ID, probe.Modified = 71, 70
	check := desiredCheck(oldSnapshot.Checks[0], probe.ID)
	check.ID, check.Modified = 72, 71
	fake.withState(func(f *fakeSM) { f.probes, f.checks = []smProbe{probe}, []smCheck{check} })
	ledger := smstate.OwnershipLedger{
		SchemaVersion: smstate.SchemaVersion, TargetFingerprint: oldSnapshot.TargetFingerprint,
		Resources: []smstate.Resource{
			{Kind: "probe", Key: oldSnapshot.Probes[0].Key, ID: probe.ID, Modified: probe.Modified, Ownership: smstate.Owned, SpecHash: smstate.SpecHash(managedProbeEvidence(probe))},
			{Kind: "check", Key: oldSnapshot.Checks[0].Key, ID: check.ID, Modified: check.Modified, ConfigVersion: smstate.ConfigVersion(check.Modified), Ownership: smstate.Owned, SpecHash: smstate.SpecHash(managedCheckEvidence(check))},
		},
	}
	if err := smstate.WriteOwnership(stateDir, ledger); err != nil {
		t.Fatal(err)
	}
	newSnapshot, err := smstate.NewSnapshot(oldSnapshot.Probes, oldSnapshot.Checks, server.URL, "new-token", version, time.Unix(101, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := smstate.WriteSnapshot(stateDir, newSnapshot); err != nil {
		t.Fatal(err)
	}
	return stateDir, newSnapshot, ledger
}

func migrationProvisioner(server *httptest.Server, stateDir string, snapshot smstate.Snapshot, ledger smstate.OwnershipLedger, apply bool) *provisioner {
	p := testProvisioner(server, stateDir, snapshot, apply, false)
	p.ownership = ledger
	p.migrate = true
	p.migrationFrom = ledger.TargetFingerprint
	return p
}

func TestPreviewMakesNoRemoteOrRegistrationWrites(t *testing.T) {
	fake := newFakeSM()
	server := httptest.NewServer(fake)
	defer server.Close()
	stateDir, snapshot := testSnapshot(t, server.URL, "token")
	p := testProvisioner(server, stateDir, snapshot, false, false)
	if err := p.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if totalPosts(fake) != 0 {
		t.Fatalf("preview made %d remote writes", totalPosts(fake))
	}
	if _, err := os.Lstat(filepath.Join(stateDir, smstate.RegistrationFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("registration unexpectedly exists: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(stateDir, smstate.JournalFilename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal unexpectedly exists: %v", err)
	}
}

func TestApplyCreatesAndPersistsAuthoritativeModified(t *testing.T) {
	fake := newFakeSM()
	server := httptest.NewServer(fake)
	defer server.Close()
	stateDir, snapshot := testSnapshot(t, server.URL, "token")
	p := testProvisioner(server, stateDir, snapshot, true, false)
	if err := p.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	registration, err := smstate.ReadRegistration(stateDir, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	check, ok := smstate.ResourceByKey(registration, "check", snapshot.Checks[0].Key)
	fake.withState(func(f *fakeSM) {
		if !ok || check.ID != f.checks[0].ID || check.ConfigVersion != smstate.ConfigVersion(f.checks[0].Modified) {
			t.Fatalf("check registration = %+v", check)
		}
	})
	if _, err := os.Lstat(filepath.Join(stateDir, smstate.JournalFilename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal retained after success: %v", err)
	}
}

func TestForeignCollisionFailsBeforeAnyWrite(t *testing.T) {
	fake := newFakeSM()
	fake.probes = []smProbe{{ID: 7, Name: "synthkit-private", Region: "OTHER", Modified: 10}}
	server := httptest.NewServer(fake)
	defer server.Close()
	stateDir, snapshot := testSnapshot(t, server.URL, "token")
	err := testProvisioner(server, stateDir, snapshot, true, false).execute(context.Background())
	if err == nil || !strings.Contains(err.Error(), "foreign remote probe collision") {
		t.Fatalf("error = %v", err)
	}
	if totalPosts(fake) != 0 {
		t.Fatalf("collision made %d writes", totalPosts(fake))
	}
}

func TestOwnedResourcesNoopThenUpdateByRecordedID(t *testing.T) {
	fake := newFakeSM()
	server := httptest.NewServer(fake)
	defer server.Close()
	stateDir, snapshot := testSnapshot(t, server.URL, "token")
	probe := desiredProbe(snapshot.Probes[0])
	probe.ID, probe.Modified = 11, 20
	check := desiredCheck(snapshot.Checks[0], probe.ID)
	check.ID, check.Modified = 12, 21
	fake.withState(func(f *fakeSM) { f.probes, f.checks = []smProbe{probe}, []smCheck{check} })
	registration := smstate.Registration{
		SchemaVersion: smstate.SchemaVersion, SnapshotHash: snapshot.SnapshotHash, TargetFingerprint: snapshot.TargetFingerprint, SourceVersion: snapshot.SourceVersion,
		Resources: []smstate.Resource{
			{Kind: "probe", Key: snapshot.Probes[0].Key, ID: probe.ID, Modified: probe.Modified, Ownership: smstate.Owned},
			{Kind: "check", Key: snapshot.Checks[0].Key, ID: check.ID, Modified: check.Modified, ConfigVersion: smstate.ConfigVersion(check.Modified), Ownership: smstate.Owned},
		},
	}
	p := testProvisioner(server, stateDir, snapshot, true, false)
	p.ownership = smstate.OwnershipLedger{SchemaVersion: smstate.SchemaVersion, TargetFingerprint: snapshot.TargetFingerprint, Resources: registration.Resources}
	if err := p.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if totalPosts(fake) != 0 {
		t.Fatalf("unchanged apply made %d writes", totalPosts(fake))
	}
	fake.withState(func(f *fakeSM) { f.checks[0].Frequency = 30000 })
	if err := p.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.withState(func(f *fakeSM) {
		if f.posts["/api/v1/check/update"] != 1 || f.posts["/api/v1/check/add"] != 0 {
			t.Fatalf("posts = %+v", f.posts)
		}
		if f.checks[0].ID != check.ID || f.checks[0].Frequency != snapshot.Checks[0].FrequencyMS {
			t.Fatalf("updated check = %+v", f.checks[0])
		}
	})
}

func TestSameCheckIgnoresServerDefaultsButComparesManagedHTTPSettings(t *testing.T) {
	spec := smstate.CheckSpec{Job: "job", Target: "https://example.test", FrequencyMS: 60000, TimeoutMS: 3000}
	desired := desiredCheck(spec, 7)
	remote := desired
	remote.Settings = map[string]any{
		"http":          map[string]any{"method": "GET", "ipVersion": "V4", "followRedirects": true},
		"serverDefault": float64(1),
	}
	if !sameCheck(remote, desired) {
		t.Fatal("server-defaulted settings forced an update")
	}
	remote.Settings["http"].(map[string]any)["method"] = "POST"
	if sameCheck(remote, desired) {
		t.Fatal("managed HTTP setting drift was ignored")
	}
}

func TestExplicitLegacyAdoptionRequiresOneExactMatch(t *testing.T) {
	fake := newFakeSM()
	server := httptest.NewServer(fake)
	defer server.Close()
	stateDir, snapshot := testSnapshot(t, server.URL, "token")
	probe := desiredProbe(snapshot.Probes[0])
	probe.ID, probe.Modified = 31, 30
	check := desiredCheck(snapshot.Checks[0], probe.ID)
	check.ID, check.Modified = 32, 31
	fake.withState(func(f *fakeSM) { f.probes, f.checks = []smProbe{probe}, []smCheck{check} })
	preview := testProvisioner(server, stateDir, snapshot, false, true)
	if err := preview.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := testProvisioner(server, stateDir, snapshot, true, true)
	if err := p.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if totalPosts(fake) != 0 {
		t.Fatalf("adoption made %d remote writes", totalPosts(fake))
	}
	registration, err := smstate.ReadRegistration(stateDir, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, resource := range registration.Resources {
		if resource.Ownership != smstate.Adopted {
			t.Fatalf("resource not adopted: %+v", resource)
		}
	}
}

func TestAmbiguousCreateRetainsJournalAndForeignMatchIsNeverClaimed(t *testing.T) {
	fake := newFakeSM()
	server := httptest.NewServer(fake)
	defer server.Close()
	stateDir, snapshot := testSnapshot(t, server.URL, "token")
	probe := desiredProbe(snapshot.Probes[0])
	probe.ID, probe.Modified = 41, 40
	fake.withState(func(f *fakeSM) { f.probes = []smProbe{probe} })
	partial := smstate.Registration{
		SchemaVersion: smstate.SchemaVersion, SnapshotHash: snapshot.SnapshotHash, TargetFingerprint: snapshot.TargetFingerprint, SourceVersion: snapshot.SourceVersion,
		Resources: []smstate.Resource{{Kind: "probe", Key: snapshot.Probes[0].Key, ID: probe.ID, Modified: probe.Modified, Ownership: smstate.Owned}},
	}
	fake.withState(func(f *fakeSM) { f.abortCheckCreate = true })
	p := testProvisioner(server, stateDir, snapshot, true, false)
	p.ownership = smstate.OwnershipLedger{SchemaVersion: smstate.SchemaVersion, TargetFingerprint: snapshot.TargetFingerprint, Resources: partial.Resources}
	err := p.execute(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %v", err)
	}
	if _, err := smstate.ReadJournal(stateDir); err != nil {
		t.Fatalf("pending journal missing: %v", err)
	}
	select {
	case <-fake.abortDone:
	case <-time.After(3 * time.Second):
		t.Fatal("no real check mutation recorded before response loss")
	}
	fake.withState(func(f *fakeSM) {
		if len(f.checks) != 1 {
			t.Fatalf("remote create did not occur: %d", len(f.checks))
		}
	})
	if err := smstate.RemoveJournal(stateDir); err != nil {
		t.Fatal(err)
	}
	fake.withState(func(f *fakeSM) { f.abortCheckCreate = false })
	p2 := testProvisioner(server, stateDir, snapshot, true, false)
	p2.ownership = smstate.OwnershipLedger{SchemaVersion: smstate.SchemaVersion, TargetFingerprint: snapshot.TargetFingerprint, Resources: partial.Resources}
	err = p2.execute(context.Background())
	if err == nil || !strings.Contains(err.Error(), "foreign remote check collision") {
		t.Fatalf("error = %v", err)
	}
	fake.withState(func(f *fakeSM) {
		if f.posts["/api/v1/check/update"] != 0 {
			t.Fatal("foreign check was updated")
		}
	})
}

func TestLegacyAdoptionApplyWithoutMatchingPreviewFailsClosed(t *testing.T) {
	fake := newFakeSM()
	server := httptest.NewServer(fake)
	defer server.Close()
	stateDir, snapshot := testSnapshot(t, server.URL, "token")
	probe := desiredProbe(snapshot.Probes[0])
	probe.ID, probe.Modified = 51, 50
	check := desiredCheck(snapshot.Checks[0], probe.ID)
	check.ID, check.Modified = 52, 51
	fake.withState(func(f *fakeSM) { f.probes, f.checks = []smProbe{probe}, []smCheck{check} })
	err := testProvisioner(server, stateDir, snapshot, true, true).execute(context.Background())
	if err == nil || !strings.Contains(err.Error(), "matching explicit preview") {
		t.Fatalf("error = %v", err)
	}
	if totalPosts(fake) != 0 {
		t.Fatalf("unpreviewed adoption made %d writes", totalPosts(fake))
	}
}

func TestOwnershipSurvivesSnapshotAndSourceVersionEvolution(t *testing.T) {
	fake := newFakeSM()
	server := httptest.NewServer(fake)
	defer server.Close()
	stateDir, oldSnapshot := testSnapshot(t, server.URL, "token")
	probe := desiredProbe(oldSnapshot.Probes[0])
	probe.ID, probe.Modified = 61, 60
	check := desiredCheck(oldSnapshot.Checks[0], probe.ID)
	check.ID, check.Modified = 62, 61
	fake.withState(func(f *fakeSM) { f.probes, f.checks = []smProbe{probe}, []smCheck{check} })
	ledger := smstate.OwnershipLedger{
		SchemaVersion: smstate.SchemaVersion, TargetFingerprint: oldSnapshot.TargetFingerprint,
		Resources: []smstate.Resource{
			{Kind: "probe", Key: oldSnapshot.Probes[0].Key, ID: probe.ID, Modified: probe.Modified, Ownership: smstate.Owned},
			{Kind: "check", Key: oldSnapshot.Checks[0].Key, ID: check.ID, Modified: check.Modified, ConfigVersion: smstate.ConfigVersion(check.Modified), Ownership: smstate.Owned},
		},
	}
	if err := smstate.WriteOwnership(stateDir, ledger); err != nil {
		t.Fatal(err)
	}
	changedChecks := append([]smstate.CheckSpec(nil), oldSnapshot.Checks...)
	changedChecks[0].FrequencyMS = 30000
	newSnapshot, err := smstate.NewSnapshot(oldSnapshot.Probes, changedChecks, server.URL, "token", "dev-next", time.Unix(101, 0))
	if err != nil {
		t.Fatal(err)
	}
	// The store now checks the actual current emitter snapshot on every write.
	if err := smstate.WriteSnapshot(stateDir, newSnapshot); err != nil {
		t.Fatal(err)
	}
	p := testProvisioner(server, stateDir, newSnapshot, true, false)
	p.ownership = ledger
	if err := p.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.withState(func(f *fakeSM) {
		if f.posts["/api/v1/check/update"] != 1 || f.posts["/api/v1/check/add"] != 0 {
			t.Fatalf("snapshot evolution did not update recorded ID: %+v", f.posts)
		}
		if f.checks[0].ID != check.ID || f.checks[0].Frequency != 30000 {
			t.Fatalf("updated check = %+v", f.checks[0])
		}
	})
}

func TestTargetMigrationRequiresFlagBeforeOwnershipCanBeRead(t *testing.T) {
	fake := newFakeSM()
	server := httptest.NewServer(fake)
	defer server.Close()
	stateDir, _, _ := migrationFixture(t, fake, server)
	t.Setenv("GC_SM_URL", server.URL)
	t.Setenv("GC_SM_TOKEN", "new-token")
	t.Setenv("CONFIG_SNAPSHOT_PATH", filepath.Join(filepath.Dir(stateDir), "control-state.json"))
	t.Setenv("SM_PROVISION_APPLY", "false")
	t.Setenv("SM_PROVISION_ADOPT_LEGACY", "false")
	t.Setenv("SM_PROVISION_MIGRATE_TARGET", "false")
	err := run()
	if err == nil || !strings.Contains(err.Error(), "SM_PROVISION_MIGRATE_TARGET=true") {
		t.Fatalf("error = %v", err)
	}
	if totalPosts(fake) != 0 {
		t.Fatalf("unapproved migration made %d writes", totalPosts(fake))
	}
}

func TestTargetMigrationApplyRequiresPreviewAndRetargetsOnlyOnApprovedApply(t *testing.T) {
	fake := newFakeSM()
	server := httptest.NewServer(fake)
	defer server.Close()
	stateDir, snapshot, ledger := migrationFixture(t, fake, server)

	applyWithoutPreview := migrationProvisioner(server, stateDir, snapshot, ledger, true)
	if err := applyWithoutPreview.execute(context.Background()); err == nil || !strings.Contains(err.Error(), "matching explicit preview") {
		t.Fatalf("unpreviewed migration error = %v", err)
	}
	before, err := smstate.ReadOwnershipState(stateDir)
	if err != nil || before.TargetFingerprint != ledger.TargetFingerprint {
		t.Fatalf("ownership changed before preview: %+v, err=%v", before, err)
	}

	preview := migrationProvisioner(server, stateDir, snapshot, ledger, false)
	if err := preview.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	afterPreview, err := smstate.ReadOwnershipState(stateDir)
	if err != nil || afterPreview.TargetFingerprint != ledger.TargetFingerprint {
		t.Fatalf("preview retargeted ownership: %+v, err=%v", afterPreview, err)
	}
	marker, err := smstate.ReadMigrationPreview(stateDir)
	if err != nil || len(marker.Resources) != len(ledger.Resources) {
		t.Fatalf("migration marker = %+v, err=%v", marker, err)
	}

	apply := migrationProvisioner(server, stateDir, snapshot, ledger, true)
	if err := apply.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	afterApply, err := smstate.ReadOwnership(stateDir, snapshot.TargetFingerprint)
	if err != nil || afterApply.TargetFingerprint != snapshot.TargetFingerprint {
		t.Fatalf("ownership was not retargeted: %+v, err=%v", afterApply, err)
	}
	if totalPosts(fake) != 0 {
		t.Fatalf("no-op migration made %d remote writes", totalPosts(fake))
	}
	if _, err := os.Lstat(filepath.Join(stateDir, smstate.MigrationFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("migration marker retained: %v", err)
	}
}

func TestTargetMigrationFailsWhenRecordedResourceIsMissing(t *testing.T) {
	fake := newFakeSM()
	server := httptest.NewServer(fake)
	defer server.Close()
	stateDir, snapshot, ledger := migrationFixture(t, fake, server)
	fake.withState(func(f *fakeSM) { f.checks = nil })
	err := migrationProvisioner(server, stateDir, snapshot, ledger, false).execute(context.Background())
	if err == nil || !strings.Contains(err.Error(), "owned remote check is missing or changed") {
		t.Fatalf("error = %v", err)
	}
	if _, readErr := os.Lstat(filepath.Join(stateDir, smstate.MigrationFile)); !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("failed migration wrote marker: %v", readErr)
	}
}

func TestTargetMigrationPreviewRejectsPreexistingRevisionOrSpecDrift(t *testing.T) {
	for _, tc := range []struct {
		name  string
		drift func(*fakeSM)
	}{
		{name: "specification only", drift: func(fake *fakeSM) { fake.checks[0].Frequency = 30000 }},
		{name: "revision only", drift: func(fake *fakeSM) { fake.checks[0].Modified++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeSM()
			server := httptest.NewServer(fake)
			defer server.Close()
			stateDir, snapshot, ledger := migrationFixture(t, fake, server)
			fake.withState(tc.drift)
			err := migrationProvisioner(server, stateDir, snapshot, ledger, false).execute(context.Background())
			if err == nil || !strings.Contains(err.Error(), "revision or specification changed") {
				t.Fatalf("error = %v", err)
			}
			if _, readErr := os.Lstat(filepath.Join(stateDir, smstate.MigrationFile)); !errors.Is(readErr, os.ErrNotExist) {
				t.Fatalf("drifted migration wrote marker: %v", readErr)
			}
		})
	}
}

func TestTargetMigrationRejectsCombinedConfigurationChange(t *testing.T) {
	fake := newFakeSM()
	server := httptest.NewServer(fake)
	defer server.Close()
	stateDir, snapshot, ledger := migrationFixture(t, fake, server)
	changedChecks := append([]smstate.CheckSpec(nil), snapshot.Checks...)
	changedChecks[0].FrequencyMS = 30000
	changed, err := smstate.NewSnapshot(snapshot.Probes, changedChecks, server.URL, "new-token", version, time.Unix(102, 0))
	if err != nil {
		t.Fatal(err)
	}
	err = migrationProvisioner(server, stateDir, changed, ledger, false).execute(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unchanged fully registered resource set") {
		t.Fatalf("error = %v", err)
	}
	if totalPosts(fake) != 0 {
		t.Fatalf("combined migration/configuration change made %d writes", totalPosts(fake))
	}
}

func TestTargetMigrationRejectsStalePreview(t *testing.T) {
	fake := newFakeSM()
	server := httptest.NewServer(fake)
	defer server.Close()
	stateDir, snapshot, ledger := migrationFixture(t, fake, server)
	preview := migrationProvisioner(server, stateDir, snapshot, ledger, false)
	preview.now = func() time.Time { return time.Unix(1_000, 0) }
	if err := preview.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	apply := migrationProvisioner(server, stateDir, snapshot, ledger, true)
	apply.now = func() time.Time { return time.Unix(1_000, 0).Add(migrationPreviewTTL + time.Millisecond) }
	err := apply.execute(context.Background())
	if err == nil || !strings.Contains(err.Error(), "current matching explicit preview") {
		t.Fatalf("stale preview error = %v", err)
	}
	current, readErr := smstate.ReadOwnershipState(stateDir)
	if readErr != nil || current.TargetFingerprint != ledger.TargetFingerprint {
		t.Fatalf("stale preview retargeted ownership: %+v, err=%v", current, readErr)
	}
}

func TestTargetMigrationRejectsRemoteSpecChangeAfterPreview(t *testing.T) {
	fake := newFakeSM()
	server := httptest.NewServer(fake)
	defer server.Close()
	stateDir, snapshot, ledger := migrationFixture(t, fake, server)
	preview := migrationProvisioner(server, stateDir, snapshot, ledger, false)
	if err := preview.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.withState(func(f *fakeSM) {
		f.checks[0].Frequency = 30000
		f.checks[0].Modified++
	})
	apply := migrationProvisioner(server, stateDir, snapshot, ledger, true)
	err := apply.execute(context.Background())
	if err == nil || !strings.Contains(err.Error(), "revision or specification changed") {
		t.Fatalf("changed preview error = %v", err)
	}
	current, readErr := smstate.ReadOwnershipState(stateDir)
	if readErr != nil || current.TargetFingerprint != ledger.TargetFingerprint {
		t.Fatalf("changed preview retargeted ownership: %+v, err=%v", current, readErr)
	}
	if totalPosts(fake) != 0 {
		t.Fatalf("changed preview made %d writes", totalPosts(fake))
	}
}

func TestTargetMigrationRetargetInterruptionRequiresAndSupportsResume(t *testing.T) {
	fake := newFakeSM()
	server := httptest.NewServer(fake)
	defer server.Close()
	stateDir, snapshot, ledger := migrationFixture(t, fake, server)
	preview := migrationProvisioner(server, stateDir, snapshot, ledger, false)
	if err := preview.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	interrupted := migrationProvisioner(server, stateDir, snapshot, ledger, true)
	interrupted.afterRetarget = func() error { return errors.New("simulated interruption") }
	if err := interrupted.execute(context.Background()); err == nil || !strings.Contains(err.Error(), "resume") {
		t.Fatalf("interruption error = %v", err)
	}
	retargeted, err := smstate.ReadOwnership(stateDir, snapshot.TargetFingerprint)
	if err != nil {
		t.Fatalf("retarget was not durable: %v", err)
	}
	if _, err := smstate.ReadMigrationPreview(stateDir); err != nil {
		t.Fatalf("resume marker missing: %v", err)
	}
	resume := migrationProvisioner(server, stateDir, snapshot, retargeted, true)
	resume.migrationFrom = ledger.TargetFingerprint
	resume.migrationRetargeted = true
	if err := resume.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(stateDir, smstate.MigrationFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("resume did not consume marker: %v", err)
	}
}

func TestCrashAfterAPISuccessLeavesJournalWithoutClaimingResource(t *testing.T) {
	fake := newFakeSM()
	server := httptest.NewServer(fake)
	defer server.Close()
	stateDir, snapshot := testSnapshot(t, server.URL, "token")
	p := testProvisioner(server, stateDir, snapshot, true, false)
	p.afterMutation = func(string) error { return errors.New("simulated crash") }
	if err := p.execute(context.Background()); err == nil {
		t.Fatal("expected simulated crash")
	}
	if _, err := smstate.ReadJournal(stateDir); err != nil {
		t.Fatalf("journal missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(stateDir, smstate.RegistrationFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("resource was claimed: %v", err)
	}
}

func TestAPIErrorNeverReflectsResponseOrCredentialCanaries(t *testing.T) {
	fake := newFakeSM()
	fake.rejectStatus = http.StatusUnauthorized
	canary := `raw-secret raw%2Dsecret Basic cmF3LXNlY3JldA== Bearer raw-secret {"token":"raw-secret"} &quot;raw-secret&quot;`
	fake.responseBody = canary
	server := httptest.NewServer(fake)
	defer server.Close()
	client := &smClient{base: server.URL, token: "raw-secret", hc: server.Client()}
	_, err := client.listProbes(context.Background())
	if operationalerr.CodeOf(err) != operationalerr.CodeAuthentication {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "raw-secret") || strings.Contains(err.Error(), "cmF3") {
		t.Fatalf("error leaked canary: %v", err)
	}
}

func TestMutationGatewayFailureIsAmbiguous(t *testing.T) {
	fake := newFakeSM()
	fake.rejectStatus = http.StatusServiceUnavailable
	server := httptest.NewServer(fake)
	defer server.Close()
	client := &smClient{base: server.URL, token: "token", hc: server.Client()}
	_, err := client.addProbe(context.Background(), smProbe{Name: "probe"})
	if err == nil || !ambiguousAPIError(err) {
		t.Fatalf("mutation 503 was not ambiguous: %v", err)
	}
	if got := pendingMutationError(err).Error(); !strings.Contains(got, "ambiguous") {
		t.Fatalf("pending error = %q", got)
	}
}

func TestMutationIncompleteSuccessResponseIsAmbiguous(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer server.Close()
	client := &smClient{base: server.URL, token: "token", hc: server.Client()}
	_, err := client.addProbe(context.Background(), smProbe{Name: "probe"})
	if err == nil || !ambiguousAPIError(err) {
		t.Fatalf("incomplete 2xx response was not ambiguous: %v", err)
	}
}

func TestRunUsesExplicitApplyGateNotEmitterDryRun(t *testing.T) {
	fake := newFakeSM()
	server := httptest.NewServer(fake)
	defer server.Close()
	stateDir, _ := testSnapshot(t, server.URL, "token")
	t.Setenv("GC_SM_URL", server.URL)
	t.Setenv("GC_SM_TOKEN", "token")
	t.Setenv("CONFIG_SNAPSHOT_PATH", filepath.Join(filepath.Dir(stateDir), "control-state.json"))
	t.Setenv("DRY_RUN", "false")
	t.Setenv("SM_PROVISION_APPLY", "")
	t.Setenv("SM_PROVISION_ADOPT_LEGACY", "")
	if err := run(); err != nil {
		t.Fatal(err)
	}
	if totalPosts(fake) != 0 {
		t.Fatalf("DRY_RUN=false caused %d remote writes", totalPosts(fake))
	}
}

func TestApplyRegistersSnapshotCoordinates(t *testing.T) {
	for _, location := range [][2]float64{{0, 0}, {40.7128, -74.0060}, {50.1109, 8.6821}} {
		fake := newFakeSM()
		server := httptest.NewServer(fake)
		stateDir, snapshot := testSnapshot(t, server.URL, "token")
		snapshot.Probes[0].Latitude, snapshot.Probes[0].Longitude = location[0], location[1]
		var err error
		snapshot, err = smstate.NewSnapshot(snapshot.Probes, snapshot.Checks, server.URL, "token", version, time.Unix(100, 0))
		if err != nil {
			t.Fatal(err)
		}
		if err := smstate.WriteSnapshot(stateDir, snapshot); err != nil {
			t.Fatal(err)
		}
		p := testProvisioner(server, stateDir, snapshot, true, false)
		if err := p.execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		fake.withState(func(f *fakeSM) {
			if len(f.probes) != 1 || f.probes[0].Latitude != location[0] || f.probes[0].Longitude != location[1] {
				t.Fatalf("registered probes = %+v, want coordinates %v", f.probes, location)
			}
		})
		server.Close()
	}
}
