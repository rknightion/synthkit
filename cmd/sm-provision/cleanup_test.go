// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/smstate"
	"github.com/rknightion/synthkit/internal/smstore"
)

// cleanupSM wraps the provisioning fake with the upstream DELETE shape. Every request is observed
// by the real handler; hooks run under mu and may change fixture facts.
type cleanupSM struct {
	fake       *fakeSM
	mu         sync.Mutex
	deletes    []string
	requests   int
	checkLists int
	failLists  bool
	auth       map[string]int
	onList     func(n int)
	onDelete   func(w http.ResponseWriter, r *http.Request, kind string, id int64) bool
}

func newCleanupSM() *cleanupSM { return &cleanupSM{fake: newFakeSM(), auth: map[string]int{}} }

var deletePath = regexp.MustCompile(`^/api/v1/(check|probe)/delete/([0-9]+)$`)

func (c *cleanupSM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests++
	c.auth[r.Header.Get("Authorization")]++
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/list") {
		if c.failLists {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/api/v1/check/list" {
			c.checkLists++
			if c.onList != nil {
				c.onList(c.checkLists)
			}
		}
	}
	if r.Method != http.MethodDelete {
		c.fake.ServeHTTP(w, r)
		return
	}
	match := deletePath.FindStringSubmatch(r.URL.Path)
	if match == nil {
		http.NotFound(w, r)
		return
	}
	id, _ := strconv.ParseInt(match[2], 10, 64)
	c.deletes = append(c.deletes, match[1]+"/"+match[2])
	if c.onDelete != nil && c.onDelete(w, r, match[1], id) {
		return
	}
	if !c.removeRemote(match[1], id) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	field := "checkId"
	if match[1] == "probe" {
		field = "probeId"
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"msg": match[1] + " deleted", field: id})
}

func (c *cleanupSM) removeRemote(kind string, id int64) bool {
	found := false
	c.fake.withState(func(f *fakeSM) {
		if kind == "check" {
			kept := f.checks[:0]
			for _, v := range f.checks {
				if v.ID == id {
					found = true
					continue
				}
				kept = append(kept, v)
			}
			f.checks = kept
			return
		}
		kept := f.probes[:0]
		for _, v := range f.probes {
			if v.ID == id {
				found = true
				continue
			}
			kept = append(kept, v)
		}
		f.probes = kept
	})
	return found
}

func (c *cleanupSM) observed() (deletes []string, requests int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.deletes...), c.requests
}

func (c *cleanupSM) set(fn func(*cleanupSM)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(c)
}

// TestCleanupFileProcessHelper is the real CLI entry point in a child process: os.Args and the
// environment are exactly what an operator's container supplies.
func TestCleanupFileProcessHelper(t *testing.T) {
	raw := os.Getenv("SYNTHKIT_SM_CLEANUP_ARGS")
	if raw == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		t.Fatal(err)
	}
	os.Args = append([]string{"sm-provision"}, args...)
	main()
	os.Exit(0)
}

type fileCleanup struct {
	sm       *cleanupSM
	server   *httptest.Server
	stateDir string
	snapshot smstate.Snapshot
	token    string
	probeID  int64
	checkID  int64
}

// provisionForCleanup runs a real ordinary apply so the ledger carries authoritative evidence.
func provisionForCleanup(t *testing.T, token string) *fileCleanup {
	t.Helper()
	sm := newCleanupSM()
	server := httptest.NewServer(sm)
	t.Cleanup(server.Close)
	stateDir, snapshot := testSnapshot(t, server.URL, token)
	if err := testProvisioner(server, stateDir, snapshot, true, false).execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	f := &fileCleanup{sm: sm, server: server, stateDir: stateDir, snapshot: snapshot, token: token}
	sm.fake.withState(func(fake *fakeSM) { f.probeID, f.checkID = fake.probes[0].ID, fake.checks[0].ID })
	sm.set(func(c *cleanupSM) { c.auth = map[string]int{} }) // the in-process fixture apply uses its own client
	return f
}

func (f *fileCleanup) env(apply bool, token string) []string {
	env := []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GC_SM_") && !strings.HasPrefix(kv, "SM_PROVISION_") && !strings.HasPrefix(kv, "STATE_BACKEND=") && !strings.HasPrefix(kv, "CONFIG_SNAPSHOT_PATH=") {
			env = append(env, kv)
		}
	}
	return append(env, "GC_SM_URL="+f.server.URL, "GC_SM_TOKEN="+token, "SM_PROVISION_APPLY="+strconv.FormatBool(apply),
		"CONFIG_SNAPSHOT_PATH="+filepath.Join(filepath.Dir(f.stateDir), "control-state.json"))
}

func startCLI(t *testing.T, env []string, args ...string) *provisionProcess {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(append([]string{}, args...))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	p := &provisionProcess{done: make(chan error, 1), cancel: cancel}
	p.cmd = exec.CommandContext(ctx, exe, "-test.run=^TestCleanupFileProcessHelper$")
	p.cmd.Env = append(env, "SYNTHKIT_SM_CLEANUP_ARGS="+string(encoded))
	p.cmd.Stdout, p.cmd.Stderr = &p.output, &p.output
	if err := p.cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	go func() { p.done <- p.cmd.Wait() }()
	t.Cleanup(cancel)
	return p
}

func runCLI(t *testing.T, env []string, wantOK bool, args ...string) string {
	t.Helper()
	p := startCLI(t, env, args...)
	var err error
	select {
	case err = <-p.done:
	case <-time.After(16 * time.Second):
		p.cancel()
		t.Fatal("CLI process did not exit")
	}
	if (err == nil) != wantOK {
		t.Fatalf("CLI %v outcome %v, expected success=%t\n%s", args, err, wantOK, p.output.String())
	}
	return p.output.String()
}

type cleanupReceipt struct {
	Schema    string `json:"schema"`
	Mode      string `json:"mode"`
	Status    string `json:"status"`
	Plan      string `json:"plan"`
	Resources []struct {
		Kind          string `json:"kind"`
		ID            int64  `json:"id"`
		ConfigVersion string `json:"config_version"`
		Outcome       string `json:"outcome"`
		Reason        string `json:"reason"`
		HTTPStatus    int    `json:"http_status"`
	} `json:"resources"`
}

func receiptOf(t *testing.T, out string) cleanupReceipt {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "cleanup receipt: "); ok {
			var r cleanupReceipt
			if err := json.Unmarshal([]byte(rest), &r); err != nil {
				t.Fatalf("receipt is not JSON: %v\n%s", err, out)
			}
			return r
		}
	}
	t.Fatalf("no cleanup receipt in output:\n%s", out)
	return cleanupReceipt{}
}

func (r cleanupReceipt) outcome(kind string, id int64) string {
	for _, v := range r.Resources {
		if v.Kind == kind && v.ID == id {
			return v.Outcome + "/" + v.Reason
		}
	}
	return "missing"
}

// privateFiles captures every private state artifact for byte-identity assertions.
func privateFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(b)
	}
	return out
}

func sameFiles(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func loadDoc(t *testing.T, dir string) smstore.Document {
	t.Helper()
	d, err := smstore.NewFileStore(dir).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// pendingDelete reads the durable intent as stored, independent of the package decoder.
func pendingDelete(t *testing.T, journal smstate.Journal) (string, int64) {
	t.Helper()
	var intent struct {
		Kind string `json:"kind"`
		ID   int64  `json:"id"`
	}
	if journal.Operation != "delete" || json.Unmarshal(journal.ExpectedSpec, &intent) != nil || intent.Kind != journal.Kind {
		t.Fatalf("pending journal is not a delete intent: %+v", journal)
	}
	return intent.Kind, intent.ID
}

// writeDeleteIntent stores a raw pending delete journal for the recorded check, optionally bound
// to another target or offset from the recorded ID. It uses only the stored wire shape.
func writeDeleteIntent(t *testing.T, f *fileCleanup, target string, idOffset int64) {
	t.Helper()
	ledger, err := smstate.ReadOwnershipState(f.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	var check smstate.Resource
	for _, r := range ledger.Resources {
		if r.Kind == "check" {
			check = r
		}
	}
	spec, err := json.Marshal(struct {
		SchemaVersion     int    `json:"schema_version"`
		TargetFingerprint string `json:"target_fingerprint"`
		Kind              string `json:"kind"`
		Key               string `json:"key"`
		ID                int64  `json:"id"`
		Modified          string `json:"modified"`
		SpecHash          string `json:"spec_hash"`
	}{smstate.SchemaVersion, target, "check", check.Key, check.ID + idOffset, smstate.ConfigVersion(check.Modified), check.SpecHash})
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(spec)
	if err := smstate.WriteJournal(f.stateDir, smstate.Journal{SchemaVersion: smstate.SchemaVersion, Operation: "delete", Kind: "check", Key: check.Key,
		SnapshotHash: f.snapshot.SnapshotHash, ExpectedHash: smstate.SpecHash(raw), ExpectedSpec: raw, StartedUnixMS: 1}); err != nil {
		t.Fatal(err)
	}
}

const zeroPlan = "0000000000000000000000000000000000000000000000000000000000000000"

func TestCleanupPreviewThenConfirmedApplyDeletesOnlyOwnedIDs(t *testing.T) {
	canary := syntheticToken(t)
	f := provisionForCleanup(t, canary)
	foreignProbe := smProbe{ID: 900, Name: "synthkit-private", Region: "OTHER", Modified: 5}
	foreignCheck := desiredCheck(f.snapshot.Checks[0], 900)
	foreignCheck.ID, foreignCheck.Modified = 901, 6
	f.sm.fake.withState(func(fake *fakeSM) {
		fake.probes = append(fake.probes, foreignProbe)
		fake.checks = append(fake.checks, foreignCheck)
	})
	before := privateFiles(t, f.stateDir)

	out := runCLI(t, f.env(false, canary), true, "cleanup")
	preview := receiptOf(t, out)
	if preview.Mode != "preview" || preview.Status != "planned" || len(preview.Resources) != 2 ||
		preview.outcome("check", f.checkID) != "would_delete/" || preview.outcome("probe", f.probeID) != "would_delete/" {
		t.Fatalf("preview receipt = %+v", preview)
	}
	if deletes, _ := f.sm.observed(); len(deletes) != 0 || !sameFiles(before, privateFiles(t, f.stateDir)) {
		t.Fatalf("preview mutated: deletes=%v", deletes)
	}
	for _, leaked := range []string{canary, f.snapshot.TargetFingerprint, f.snapshot.Checks[0].Target, f.snapshot.Checks[0].Job} {
		if strings.Contains(out, leaked) {
			t.Fatalf("cleanup output leaked %q:\n%s", leaked, out)
		}
	}

	if out := runCLI(t, f.env(true, canary), false, "cleanup"); !strings.Contains(out, "-confirm") {
		t.Fatalf("unconfirmed apply output:\n%s", out)
	}
	if out := runCLI(t, f.env(true, canary), false, "cleanup", "-confirm="+zeroPlan); !strings.Contains(out, "does not match the current cleanup plan") {
		t.Fatalf("mismatched confirm output:\n%s", out)
	}
	if deletes, _ := f.sm.observed(); len(deletes) != 0 || !sameFiles(before, privateFiles(t, f.stateDir)) {
		t.Fatalf("unconfirmed cleanup mutated: deletes=%v", deletes)
	}

	out = runCLI(t, f.env(true, canary), true, "cleanup", "-confirm="+preview.Plan)
	applied := receiptOf(t, out)
	if applied.Status != "complete" || applied.outcome("check", f.checkID) != "confirmed_absent/" || applied.outcome("probe", f.probeID) != "confirmed_absent/" {
		t.Fatalf("apply receipt = %+v", applied)
	}
	deletes, _ := f.sm.observed()
	if want := []string{fmt.Sprintf("check/%d", f.checkID), fmt.Sprintf("probe/%d", f.probeID)}; strings.Join(deletes, ",") != strings.Join(want, ",") {
		t.Fatalf("deletes = %v, want %v", deletes, want)
	}
	f.sm.fake.withState(func(fake *fakeSM) {
		if len(fake.probes) != 1 || fake.probes[0].ID != 900 || len(fake.checks) != 1 || fake.checks[0].ID != 901 {
			t.Fatalf("foreign resources changed: %+v %+v", fake.probes, fake.checks)
		}
		if len(fake.posts) != 2 { // the provisioning creates only
			t.Fatalf("cleanup posted: %+v", fake.posts)
		}
	})
	f.sm.set(func(c *cleanupSM) {
		if len(c.auth) != 1 || c.auth["Bearer "+canary] == 0 {
			t.Fatal("cleanup used a credential other than the configured one")
		}
	})
	d := loadDoc(t, f.stateDir)
	if d.Provision.Pending != nil || d.Provision.Registration != nil || d.Provision.Ownership == nil || len(d.Provision.Ownership.Resources) != 0 {
		t.Fatalf("final custody = %+v", d.Provision)
	}
	if again := receiptOf(t, runCLI(t, f.env(false, canary), true, "cleanup")); len(again.Resources) != 0 {
		t.Fatalf("released custody reappeared: %+v", again)
	}
}

func TestCleanupRefusesForeignChangedLegacyAndIncompleteCustody(t *testing.T) {
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
		{name: "legacy registration without ledger", want: "legacy registration", mutate: func(t *testing.T, f *fileCleanup) {
			if err := os.Remove(filepath.Join(f.stateDir, smstate.OwnershipFile)); err != nil {
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
		{name: "credential substitution", want: "does not match configured target", token: "substituted-credential", mutate: func(*testing.T, *fileCleanup) {}},
		{name: "pending provisioning operation", want: "pending provisioning operation", mutate: func(t *testing.T, f *fileCleanup) {
			spec := json.RawMessage(`{"name":"x"}`)
			if err := smstate.WriteJournal(f.stateDir, smstate.Journal{SchemaVersion: smstate.SchemaVersion, Operation: "create", Kind: "check", Key: f.snapshot.Checks[0].Key,
				SnapshotHash: f.snapshot.SnapshotHash, ExpectedHash: smstate.SpecHash(spec), ExpectedSpec: spec, StartedUnixMS: 1}); err != nil {
				t.Fatal(err)
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
		{name: "no snapshot binding", want: "current private snapshot", mutate: func(t *testing.T, f *fileCleanup) {
			if err := smstate.RemoveSnapshot(f.stateDir); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := provisionForCleanup(t, "token")
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
				t.Fatalf("refused cleanup mutated: deletes=%v", deletes)
			}
			if tc.token != "" && requests != requestsBefore {
				t.Fatal("substituted credential reached the SM API")
			}
		})
	}
}

func TestCleanupRetainsAdoptedAndReferencedResources(t *testing.T) {
	t.Run("adopted check keeps its owned probe", func(t *testing.T) {
		f := provisionForCleanup(t, "token")
		ledger, err := smstate.ReadOwnershipState(f.stateDir)
		if err != nil {
			t.Fatal(err)
		}
		for i := range ledger.Resources {
			if ledger.Resources[i].Kind == "check" {
				ledger.Resources[i].Ownership = smstate.Adopted
			}
		}
		if err := smstate.WriteOwnership(f.stateDir, ledger); err != nil {
			t.Fatal(err)
		}
		preview := receiptOf(t, runCLI(t, f.env(false, "token"), true, "cleanup"))
		if preview.outcome("check", f.checkID) != "retained/adopted" || preview.outcome("probe", f.probeID) != "retained/referenced" {
			t.Fatalf("preview = %+v", preview)
		}
		applied := receiptOf(t, runCLI(t, f.env(true, "token"), true, "cleanup", "-confirm="+preview.Plan))
		if applied.Status != "remaining" {
			t.Fatalf("apply = %+v", applied)
		}
		if deletes, _ := f.sm.observed(); len(deletes) != 0 {
			t.Fatalf("retained resources deleted: %v", deletes)
		}
		after, err := smstate.ReadOwnershipState(f.stateDir)
		if err != nil || len(after.Resources) != 2 {
			t.Fatalf("retained custody lost: %+v %v", after, err)
		}
	})
	t.Run("foreign check references the owned probe", func(t *testing.T) {
		f := provisionForCleanup(t, "token")
		f.sm.fake.withState(func(fake *fakeSM) {
			foreign := desiredCheck(smstate.CheckSpec{Job: "other-team", Target: "https://other.example", FrequencyMS: 60000, TimeoutMS: 3000}, f.probeID)
			foreign.ID, foreign.Modified = 950, 9
			fake.checks = append(fake.checks, foreign)
		})
		preview := receiptOf(t, runCLI(t, f.env(false, "token"), true, "cleanup"))
		applied := receiptOf(t, runCLI(t, f.env(true, "token"), true, "cleanup", "-confirm="+preview.Plan))
		if applied.Status != "remaining" || applied.outcome("check", f.checkID) != "confirmed_absent/" || applied.outcome("probe", f.probeID) != "retained/referenced" {
			t.Fatalf("apply = %+v", applied)
		}
		if deletes, _ := f.sm.observed(); strings.Join(deletes, ",") != fmt.Sprintf("check/%d", f.checkID) {
			t.Fatalf("deletes = %v", deletes)
		}
		ledger, err := smstate.ReadOwnershipState(f.stateDir)
		if err != nil || len(ledger.Resources) != 1 || ledger.Resources[0].Kind != "probe" {
			t.Fatalf("probe custody = %+v %v", ledger, err)
		}
	})
}

func TestCleanupConcurrentModificationRefusesWithoutDeletion(t *testing.T) {
	t.Run("after preview", func(t *testing.T) {
		f := provisionForCleanup(t, "token")
		preview := receiptOf(t, runCLI(t, f.env(false, "token"), true, "cleanup"))
		f.sm.fake.withState(func(fake *fakeSM) {
			fake.checks[0].Frequency, fake.checks[0].Modified = 30000, fake.checks[0].Modified+1
		})
		before := privateFiles(t, f.stateDir)
		if out := runCLI(t, f.env(true, "token"), false, "cleanup", "-confirm="+preview.Plan); !strings.Contains(out, "revision or specification changed") {
			t.Fatalf("output:\n%s", out)
		}
		if deletes, _ := f.sm.observed(); len(deletes) != 0 || !sameFiles(before, privateFiles(t, f.stateDir)) {
			t.Fatalf("changed resource deleted or state written: %v", deletes)
		}
	})
	t.Run("between plan and delete", func(t *testing.T) {
		f := provisionForCleanup(t, "token")
		preview := receiptOf(t, runCLI(t, f.env(false, "token"), true, "cleanup"))
		f.sm.set(func(c *cleanupSM) {
			c.checkLists = 0
			c.onList = func(n int) {
				if n == 2 { // plan inventory is 1; the revalidation immediately before DELETE is 2
					c.fake.withState(func(fake *fakeSM) { fake.checks[0].Modified++ })
				}
			}
		})
		out := runCLI(t, f.env(true, "token"), false, "cleanup", "-confirm="+preview.Plan)
		receipt := receiptOf(t, out)
		if receipt.Status != "stopped" || receipt.outcome("check", f.checkID) != "not_attempted/" || receipt.outcome("probe", f.probeID) != "not_attempted/" {
			t.Fatalf("receipt = %+v\n%s", receipt, out)
		}
		if deletes, _ := f.sm.observed(); len(deletes) != 0 {
			t.Fatalf("concurrently changed resource deleted: %v", deletes)
		}
		d := loadDoc(t, f.stateDir)
		if d.Provision.Pending != nil || d.Provision.Ownership == nil || len(d.Provision.Ownership.Resources) != 2 {
			t.Fatalf("custody changed: %+v", d.Provision)
		}
	})
}

func TestCleanupLostDeleteResponse(t *testing.T) {
	t.Run("absence confirmed by inventory in the same run", func(t *testing.T) {
		f := provisionForCleanup(t, "token")
		preview := receiptOf(t, runCLI(t, f.env(false, "token"), true, "cleanup"))
		f.sm.set(func(c *cleanupSM) {
			c.onDelete = func(_ http.ResponseWriter, _ *http.Request, kind string, id int64) bool {
				if kind != "check" {
					return false
				}
				c.removeRemote(kind, id)
				panic(http.ErrAbortHandler)
			}
		})
		applied := receiptOf(t, runCLI(t, f.env(true, "token"), true, "cleanup", "-confirm="+preview.Plan))
		if applied.Status != "complete" || applied.outcome("check", f.checkID) != "confirmed_absent/" {
			t.Fatalf("receipt = %+v", applied)
		}
		if d := loadDoc(t, f.stateDir); d.Provision.Pending != nil || len(d.Provision.Ownership.Resources) != 0 {
			t.Fatalf("custody = %+v", d.Provision)
		}
	})
	for _, applied := range []bool{true, false} {
		t.Run(fmt.Sprintf("unknown outcome retains custody (server applied=%t)", applied), func(t *testing.T) {
			f := provisionForCleanup(t, "token")
			preview := receiptOf(t, runCLI(t, f.env(false, "token"), true, "cleanup"))
			f.sm.set(func(c *cleanupSM) {
				c.onDelete = func(_ http.ResponseWriter, _ *http.Request, kind string, id int64) bool {
					if kind != "check" || c.failLists {
						return false
					}
					if applied {
						c.removeRemote(kind, id)
					}
					c.failLists = true
					panic(http.ErrAbortHandler)
				}
			})
			out := runCLI(t, f.env(true, "token"), false, "cleanup", "-confirm="+preview.Plan)
			receipt := receiptOf(t, out)
			if receipt.Status != "unknown" || receipt.outcome("check", f.checkID) != "unknown/" || receipt.outcome("probe", f.probeID) != "not_attempted/" {
				t.Fatalf("receipt = %+v\n%s", receipt, out)
			}
			journal, err := smstate.ReadJournal(f.stateDir)
			if err != nil {
				t.Fatalf("pending intent missing: %v", err)
			}
			if kind, id := pendingDelete(t, journal); kind != "check" || id != f.checkID {
				t.Fatalf("pending intent = %s/%d", kind, id)
			}
			if _, err := os.Lstat(filepath.Join(f.stateDir, smstate.RegistrationFile)); !os.IsNotExist(err) {
				t.Fatal("activation retained while a delete was outstanding")
			}
			f.sm.set(func(c *cleanupSM) { c.failLists, c.onDelete = false, nil })
			// Ordinary provisioning never consumes cleanup custody.
			if out := runCLI(t, f.env(true, "token"), false); !strings.Contains(out, "pending operation") {
				t.Fatalf("ordinary apply output:\n%s", out)
			}
			f.sm.fake.withState(func(fake *fakeSM) {
				if fake.posts["/api/v1/check/add"] != 1 {
					t.Fatal("ordinary apply recreated a resource under cleanup custody")
				}
			})
			recovery := receiptOf(t, runCLI(t, f.env(false, "token"), true, "cleanup"))
			want := "would_delete/"
			if applied {
				want = "absent/"
			}
			if got := recovery.outcome("check", f.checkID); got != want {
				t.Fatalf("recovery preview check = %s, want %s", got, want)
			}
			final := receiptOf(t, runCLI(t, f.env(true, "token"), true, "cleanup", "-confirm="+recovery.Plan))
			if final.Status != "complete" {
				t.Fatalf("recovery = %+v", final)
			}
			deletes, _ := f.sm.observed()
			checkDeletes := 0
			for _, d := range deletes {
				if strings.HasPrefix(d, "check/") {
					checkDeletes++
				}
			}
			if wantDeletes := map[bool]int{true: 1, false: 2}[applied]; checkDeletes != wantDeletes {
				t.Fatalf("check deletes = %d, want %d (%v)", checkDeletes, wantDeletes, deletes)
			}
			if _, err := smstate.ReadJournal(f.stateDir); !os.IsNotExist(err) {
				t.Fatalf("recovered intent retained: %v", err)
			}
		})
	}
}

func TestCleanupKilledDuringDeleteKeepsRecoveryCustody(t *testing.T) {
	f := provisionForCleanup(t, "token")
	preview := receiptOf(t, runCLI(t, f.env(false, "token"), true, "cleanup"))
	reached := make(chan struct{})
	f.sm.set(func(c *cleanupSM) {
		c.onDelete = func(_ http.ResponseWriter, r *http.Request, kind string, id int64) bool {
			c.removeRemote(kind, id)
			close(reached)
			c.mu.Unlock() // let inventory continue while this handler waits for the dead client
			<-r.Context().Done()
			c.mu.Lock()
			return true
		}
	})
	p := startCLI(t, f.env(true, "token"), "cleanup", "-confirm="+preview.Plan)
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("no real DELETE observed")
	}
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-p.done
	f.sm.set(func(c *cleanupSM) { c.onDelete = nil })
	journal, err := smstate.ReadJournal(f.stateDir)
	if err != nil {
		t.Fatalf("killed cleanup lost its pending intent: %v", err)
	}
	if kind, id := pendingDelete(t, journal); kind != "check" || id != f.checkID {
		t.Fatalf("pending intent = %s/%d", kind, id)
	}
	// The provisioner lock is never guessed stale: the next run refuses before any SM request.
	_, requests := f.sm.observed()
	if out := runCLI(t, f.env(false, "token"), false, "cleanup"); !strings.Contains(out, "lock is unavailable") {
		t.Fatalf("output:\n%s", out)
	}
	if _, after := f.sm.observed(); after != requests {
		t.Fatal("cleanup reached SM without the provisioner lock")
	}
	if err := os.Remove(filepath.Join(f.stateDir, smstate.LockFilename)); err != nil { // explicit operator recovery
		t.Fatal(err)
	}
	recovery := receiptOf(t, runCLI(t, f.env(false, "token"), true, "cleanup"))
	if recovery.outcome("check", f.checkID) != "absent/" {
		t.Fatalf("recovery preview = %+v", recovery)
	}
	if final := receiptOf(t, runCLI(t, f.env(true, "token"), true, "cleanup", "-confirm="+recovery.Plan)); final.Status != "complete" {
		t.Fatalf("recovery = %+v", final)
	}
}

func TestCleanupDefersToEmitterHeldStateLock(t *testing.T) {
	f := provisionForCleanup(t, "token")
	release, err := smstate.AcquireLock(f.stateDir) // the emitter's publication/activation read holds this lock
	if err != nil {
		t.Fatal(err)
	}
	_, requests := f.sm.observed()
	before := privateFiles(t, f.stateDir)
	out := runCLI(t, f.env(true, "token"), false, "cleanup", "-confirm="+zeroPlan)
	if !strings.Contains(out, "lock is unavailable") {
		t.Fatalf("output:\n%s", out)
	}
	if _, after := f.sm.observed(); after != requests || !sameFiles(before, privateFiles(t, f.stateDir)) {
		t.Fatal("cleanup acted without provisioner authority")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestCLIHelpAndUnknownArgumentsMakeNoRequests(t *testing.T) {
	f := provisionForCleanup(t, "token")
	_, requests := f.sm.observed()
	before := privateFiles(t, f.stateDir)
	if out := runCLI(t, f.env(true, "token"), true, "help"); !strings.Contains(out, "sm-provision cleanup") || !strings.Contains(out, "-confirm=<plan>") {
		t.Fatalf("help output:\n%s", out)
	}
	for _, args := range [][]string{{"cleanp"}, {"cleanup", "extra"}, {"cleanup", "-confirm=ABC"}} {
		if out := runCLI(t, f.env(true, "token"), false, args...); !bytes.Contains([]byte(out), []byte("sm-provision help")) && !strings.Contains(out, "64-character") {
			t.Fatalf("%v output:\n%s", args, out)
		}
	}
	if _, after := f.sm.observed(); after != requests || !sameFiles(before, privateFiles(t, f.stateDir)) {
		t.Fatal("rejected arguments reached SM or state")
	}
}

func TestCleanupDefiniteDeleteRejectionKeepsCustodyAndRecovers(t *testing.T) {
	f := provisionForCleanup(t, "token")
	preview := receiptOf(t, runCLI(t, f.env(false, "token"), true, "cleanup"))
	const secretBody = `{"err":"forbidden","msg":"rejected-body-canary"}`
	f.sm.set(func(c *cleanupSM) {
		c.onDelete = func(w http.ResponseWriter, _ *http.Request, kind string, _ int64) bool {
			if kind != "check" {
				return false
			}
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(secretBody))
			return true
		}
	})
	out := runCLI(t, f.env(true, "token"), false, "cleanup", "-confirm="+preview.Plan)
	receipt := receiptOf(t, out)
	if receipt.Status != "rejected" || receipt.outcome("check", f.checkID) != "rejected/" || receipt.outcome("probe", f.probeID) != "not_attempted/" {
		t.Fatalf("receipt = %+v\n%s", receipt, out)
	}
	for _, r := range receipt.Resources {
		if r.Kind == "check" && r.HTTPStatus != http.StatusForbidden {
			t.Fatalf("rejected outcome status = %d", r.HTTPStatus)
		}
	}
	if strings.Contains(out, "rejected-body-canary") || strings.Contains(out, "forbidden\"") {
		t.Fatalf("response body reached output:\n%s", out)
	}
	journal, err := smstate.ReadJournal(f.stateDir)
	if err != nil {
		t.Fatalf("rejected delete released its intent: %v", err)
	}
	if kind, id := pendingDelete(t, journal); kind != "check" || id != f.checkID {
		t.Fatalf("pending intent = %s/%d", kind, id)
	}
	if out := runCLI(t, f.env(true, "token"), false); !strings.Contains(out, "pending operation") {
		t.Fatalf("ordinary apply was not blocked:\n%s", out)
	}
	f.sm.set(func(c *cleanupSM) { c.onDelete = nil }) // the operator fixes the token permission
	recovery := receiptOf(t, runCLI(t, f.env(false, "token"), true, "cleanup"))
	if recovery.outcome("check", f.checkID) != "would_delete/" {
		t.Fatalf("recovery preview = %+v", recovery)
	}
	if final := receiptOf(t, runCLI(t, f.env(true, "token"), true, "cleanup", "-confirm="+recovery.Plan)); final.Status != "complete" {
		t.Fatalf("recovery = %+v", final)
	}
	if _, err := smstate.ReadJournal(f.stateDir); !os.IsNotExist(err) {
		t.Fatalf("recovered intent retained: %v", err)
	}
}
