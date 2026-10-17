// SPDX-License-Identifier: AGPL-3.0-only
package fleet_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	. "github.com/rknightion/synthkit/internal/fleet"
	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/sink/httpretry"
)

// fakeFM models the vendor Collector API contract: RegisterCollector creates, UnregisterCollector
// only marks a collector inactive (marked_inactive_at; it still reads present), DeleteCollector
// removes it, and GetCollector answers an absent ID with Connect not_found.
type fakeFM struct {
	mu         sync.Mutex
	registry   map[string]bool
	calls      []string
	unregister func(w http.ResponseWriter, id string) bool // true when the fake handled the response
	remove     func(w http.ResponseWriter, id string) bool // DeleteCollector hook, same convention
	inactive   map[string]bool
	get        func(w http.ResponseWriter, id string) bool
	auth       []string
}

func newFakeFM(t *testing.T, present ...string) (*fakeFM, *httptest.Server) {
	f := &fakeFM{registry: map[string]bool{}, inactive: map[string]bool{}}
	for _, id := range present {
		f.registry[id] = true
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		f.mu.Lock()
		defer f.mu.Unlock()
		user, pass, _ := r.BasicAuth()
		f.auth = append(f.auth, user+":"+pass)
		f.calls = append(f.calls, method+" "+body.ID)
		switch method {
		case "RegisterCollector":
			f.registry[body.ID] = true
		case "UnregisterCollector":
			if f.unregister != nil && f.unregister(w, body.ID) {
				return
			}
			if f.registry[body.ID] {
				f.inactive[body.ID] = true
			}
		case "DeleteCollector":
			if f.remove != nil && f.remove(w, body.ID) {
				return
			}
			if !f.registry[body.ID] {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":"not_found"}`))
				return
			}
			delete(f.registry, body.ID)
			delete(f.inactive, body.ID)
		case "GetCollector":
			if f.get != nil && f.get(w, body.ID) {
				return
			}
			if !f.registry[body.ID] {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":"not_found","message":"collector not found"}`))
				return
			}
			collector := map[string]any{"id": body.ID, "localAttributes": map[string]string{"secret_like": "attr-canary"}}
			if f.inactive[body.ID] {
				collector["markedInactiveAt"] = "2026-10-17T00:00:00Z"
			}
			_ = json.NewEncoder(w).Encode(collector)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeFM) snapshot() (map[string]bool, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reg := map[string]bool{}
	for k, v := range f.registry {
		reg[k] = v
	}
	return reg, append([]string(nil), f.calls...)
}

func ledgerIDs(s *memoryRegistered) []string {
	var out []string
	for id := range s.ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func outcomes(r *TeardownReceipt) map[string]TeardownEntry {
	out := map[string]TeardownEntry{}
	for _, e := range r.Collectors {
		out[e.ID] = e
	}
	return out
}

// Historical roster changes leave identities the current selection no longer names. The
// selected-roster teardown leaves them; owned teardown retires each by confirmed absence.
func TestOwnedTeardownRetiresHistoricalLedgerIdentities(t *testing.T) {
	ctx := context.Background()
	fm, srv := newFakeFM(t)
	store := &memoryRegistered{ids: map[string]bool{}, gate: ha.AlwaysLeader{}}
	cfg := Config{FMURL: srv.URL, StackID: "stack", Token: "token-canary", Store: store}
	if err := NewController(cfg).Reconcile(ctx, []Collector{{ID: "scaled-a"}, {ID: "scaled-b"}, {ID: "kept"}}); err != nil {
		t.Fatal(err)
	}
	// A scale-down whose unregistration failed retains the departed identities as intent.
	fm.mu.Lock()
	fm.unregister = func(w http.ResponseWriter, id string) bool { w.WriteHeader(503); return true }
	fm.mu.Unlock()
	if err := NewController(cfg).Reconcile(ctx, []Collector{{ID: "kept"}}); err == nil {
		t.Fatal("failed departure unregistration reported success")
	}
	fm.mu.Lock()
	fm.unregister = nil
	fm.mu.Unlock()
	// The selection then changes again; the selected-roster teardown removes only "kept".
	if err := NewController(cfg).Unregister(ctx, []Collector{{ID: "kept"}}); err != nil {
		t.Fatal(err)
	}
	if got := ledgerIDs(store); !reflect.DeepEqual(got, []string{"scaled-a", "scaled-b"}) {
		t.Fatalf("selected teardown ledger = %v, want the historical identities retained", got)
	}
	_, before := fm.snapshot()
	receipt, err := NewController(cfg).TeardownOwned(ctx, TeardownOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != TeardownComplete || receipt.Inventoried != 2 || receipt.Retired != 2 || receipt.Remaining != 0 || !receipt.InventoryVerified {
		t.Fatalf("receipt = %+v", receipt)
	}
	reg, calls := fm.snapshot()
	// The selected-roster teardown's UnregisterCollector only marked "kept" inactive; the owned
	// teardown deleted the historical identities.
	if !reflect.DeepEqual(reg, map[string]bool{"kept": true}) || !fm.inactive["kept"] || len(store.ids) != 0 {
		t.Fatalf("remote=%v inactive=%v ledger=%v", reg, fm.inactive, store.ids)
	}
	want := []string{"GetCollector scaled-a", "DeleteCollector scaled-a", "GetCollector scaled-a", "GetCollector scaled-b", "DeleteCollector scaled-b", "GetCollector scaled-b"}
	if got := calls[len(before):]; !reflect.DeepEqual(got, want) {
		t.Fatalf("teardown calls = %v, want %v", got, want)
	}
	for _, e := range receipt.Collectors {
		if e.Outcome != OutcomeConfirmedAbsent || e.Reason != ReasonAbsentAfterDelete || !e.Retired {
			t.Fatalf("entry %+v", e)
		}
	}
}

// Deletion authority comes only from the ledger: a remote collector that the ledger does not
// record is never touched, whatever its name.
func TestOwnedTeardownNeverDerivesAuthorityFromNames(t *testing.T) {
	fm, srv := newFakeFM(t, "owned", "owned-sibling", "foreign")
	store := &memoryRegistered{ids: map[string]bool{"owned": true}, gate: ha.AlwaysLeader{}}
	receipt, err := NewController(Config{FMURL: srv.URL, Token: "t", Store: store}).TeardownOwned(context.Background(), TeardownOptions{})
	if err != nil || receipt.Status != TeardownComplete {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	reg, calls := fm.snapshot()
	if !reg["owned-sibling"] || !reg["foreign"] || reg["owned"] {
		t.Fatalf("remote registry %v", reg)
	}
	for _, c := range calls {
		if !strings.HasSuffix(c, " owned") {
			t.Fatalf("request for unrecorded identity: %s", c)
		}
	}
}

// A lost delete response is resolved only by the following read.
func TestOwnedTeardownResponseLossAndRecovery(t *testing.T) {
	ctx := context.Background()
	t.Run("applied-then-confirmed", func(t *testing.T) {
		fm, srv := newFakeFM(t, "lost")
		fm.remove = func(w http.ResponseWriter, id string) bool {
			delete(fm.registry, id)
			hijackClose(t, w)
			return true
		}
		store := &memoryRegistered{ids: map[string]bool{"lost": true}, gate: ha.AlwaysLeader{}}
		receipt, err := NewController(Config{FMURL: srv.URL, Token: "t", Store: store}).TeardownOwned(ctx, TeardownOptions{})
		if err != nil || outcomes(receipt)["lost"].Outcome != OutcomeConfirmedAbsent || len(store.ids) != 0 {
			t.Fatalf("receipt=%+v err=%v ledger=%v", receipt, err, store.ids)
		}
	})
	t.Run("unknown-retained-then-recovered", func(t *testing.T) {
		fm, srv := newFakeFM(t, "lost")
		// The response is lost and the server did not apply it: the read still sees the collector.
		fm.remove = func(w http.ResponseWriter, id string) bool { hijackClose(t, w); return true }
		store := &memoryRegistered{ids: map[string]bool{"lost": true}, gate: ha.AlwaysLeader{}}
		receipt, err := NewController(Config{FMURL: srv.URL, Token: "t", Store: store}).TeardownOwned(ctx, TeardownOptions{})
		if !errors.Is(err, ErrTeardownIncomplete) || receipt.Status != TeardownRemaining {
			t.Fatalf("lost response certified: %+v %v", receipt, err)
		}
		e := outcomes(receipt)["lost"]
		if e.Outcome != OutcomeUnknown || e.Reason != ReasonPresentAfterDelete || e.Retired || !store.ids["lost"] || receipt.Remaining != 1 {
			t.Fatalf("entry=%+v ledger=%v", e, store.ids)
		}
		// Recovery: the next run reads the collector absent (the server applied it late) and
		// releases custody without another unregistration.
		fm.mu.Lock()
		delete(fm.registry, "lost")
		fm.remove = nil
		before := len(fm.calls)
		fm.mu.Unlock()
		receipt, err = NewController(Config{FMURL: srv.URL, Token: "t", Store: store}).TeardownOwned(ctx, TeardownOptions{})
		if err != nil || outcomes(receipt)["lost"].Reason != ReasonAbsentBeforeDelete || len(store.ids) != 0 {
			t.Fatalf("recovery receipt=%+v err=%v", receipt, err)
		}
		_, calls := fm.snapshot()
		if got := calls[before:]; !reflect.DeepEqual(got, []string{"GetCollector lost"}) {
			t.Fatalf("recovery calls %v", got)
		}
	})
	t.Run("read-failure-keeps-custody", func(t *testing.T) {
		fm, srv := newFakeFM(t, "kept")
		fm.get = func(w http.ResponseWriter, id string) bool { w.WriteHeader(500); return true }
		store := &memoryRegistered{ids: map[string]bool{"kept": true}, gate: ha.AlwaysLeader{}}
		receipt, err := NewController(Config{FMURL: srv.URL, Token: "t", Store: store}).TeardownOwned(ctx, TeardownOptions{})
		e := outcomes(receipt)["kept"]
		if !errors.Is(err, ErrTeardownIncomplete) || e.Outcome != OutcomeUnknown || e.Reason != ReasonReadFailed || e.HTTPStatus != 500 || !store.ids["kept"] {
			t.Fatalf("entry=%+v err=%v", e, err)
		}
		if _, calls := fm.snapshot(); !reflect.DeepEqual(calls, []string{"GetCollector kept"}) {
			t.Fatalf("unconfirmable identity was mutated: %v", calls)
		}
	})
}

// Only an exact Connect not_found is absence; a bare 404 (wrong path, proxy) is unknown.
func TestOwnedTeardownBare404IsNotAbsence(t *testing.T) {
	for name, body := range map[string]string{"html": "<html>not found</html>", "other-code": `{"code":"unimplemented"}`, "empty": ""} {
		t.Run(name, func(t *testing.T) {
			fm, srv := newFakeFM(t)
			fm.get = func(w http.ResponseWriter, id string) bool {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(body))
				return true
			}
			store := &memoryRegistered{ids: map[string]bool{"recorded": true}, gate: ha.AlwaysLeader{}}
			receipt, err := NewController(Config{FMURL: srv.URL, Token: "t", Store: store}).TeardownOwned(context.Background(), TeardownOptions{})
			if !errors.Is(err, ErrTeardownIncomplete) || !store.ids["recorded"] || outcomes(receipt)["recorded"].Outcome != OutcomeUnknown {
				t.Fatalf("bare 404 released custody: %+v %v %v", receipt, err, store.ids)
			}
		})
	}
}

// A definite rejection with the collector still present is reported as rejected, with the
// status but no body, and custody is retained.
func TestOwnedTeardownRejectedReceiptIsSecretFree(t *testing.T) {
	fm, srv := newFakeFM(t, "denied")
	fm.remove = func(w http.ResponseWriter, id string) bool {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"code":"permission_denied","message":"body-canary"}`))
		return true
	}
	store := &memoryRegistered{ids: map[string]bool{"denied": true}, gate: ha.AlwaysLeader{}}
	receipt, err := NewController(Config{FMURL: srv.URL, StackID: "stack-canary", Token: "token-canary", Store: store}).TeardownOwned(context.Background(), TeardownOptions{})
	if !errors.Is(err, ErrTeardownIncomplete) {
		t.Fatal(err)
	}
	e := outcomes(receipt)["denied"]
	if e.Outcome != OutcomeRejected || e.HTTPStatus != 403 || e.Retired || !store.ids["denied"] || receipt.Remaining != 1 {
		t.Fatalf("entry=%+v receipt=%+v", e, receipt)
	}
	data, _ := json.Marshal(receipt)
	for _, canary := range []string{"token-canary", "stack-canary", "body-canary", "attr-canary", srv.URL} {
		if strings.Contains(string(data), canary) {
			t.Fatalf("receipt leaked %q: %s", canary, data)
		}
	}
	if LifecycleErrorMessage(err) != ErrTeardownIncomplete.Error() {
		t.Fatalf("incomplete teardown diagnostic hidden: %q", LifecycleErrorMessage(err))
	}
}

// Interruption leaves the unprocessed identities as recoverable intent; a rerun completes.
func TestOwnedTeardownInterruptionAndRecovery(t *testing.T) {
	fm, srv := newFakeFM(t, "a", "b", "c")
	ctx, cancel := context.WithCancel(context.Background())
	fm.remove = func(w http.ResponseWriter, id string) bool { cancel(); return false }
	store := &memoryRegistered{ids: map[string]bool{"a": true, "b": true, "c": true}, gate: ha.AlwaysLeader{}}
	receipt, err := NewController(Config{FMURL: srv.URL, Token: "t", Store: store}).TeardownOwned(ctx, TeardownOptions{})
	if !errors.Is(err, ErrTeardownIncomplete) || receipt.Status != TeardownRemaining {
		t.Fatalf("interrupted teardown certified: %+v %v", receipt, err)
	}
	got := outcomes(receipt)
	if got["a"].Reason != ReasonInterrupted || got["a"].Retired || got["b"].Reason != ReasonNotAttempted || got["c"].Reason != ReasonNotAttempted || len(store.ids) != 3 || receipt.InventoryVerified {
		t.Fatalf("receipt=%+v ledger=%v", receipt, store.ids)
	}
	fm.mu.Lock()
	fm.remove = nil
	fm.mu.Unlock()
	receipt, err = NewController(Config{FMURL: srv.URL, Token: "t", Store: store}).TeardownOwned(context.Background(), TeardownOptions{})
	if err != nil || receipt.Status != TeardownComplete || len(store.ids) != 0 {
		t.Fatalf("recovery receipt=%+v err=%v", receipt, err)
	}
	if outcomes(receipt)["a"].Reason != ReasonAbsentBeforeDelete {
		t.Fatalf("applied interruption not confirmed by read: %+v", receipt)
	}
}

// addingStore is a concurrent writer: it records a new identity after the first release.
type addingStore struct {
	*memoryRegistered
	added bool
}

func (s *addingStore) Set(ctx context.Context, id string, present bool) error {
	err := s.memoryRegistered.Set(ctx, id, present)
	if !s.added {
		s.added = true
		s.ids["late-writer"] = true
	}
	return err
}

func TestOwnedTeardownConcurrentLedgerWriterRefusesCompletion(t *testing.T) {
	_, srv := newFakeFM(t, "a")
	store := &addingStore{memoryRegistered: &memoryRegistered{ids: map[string]bool{"a": true}, gate: ha.AlwaysLeader{}}}
	receipt, err := NewController(Config{FMURL: srv.URL, Token: "t", Store: store}).TeardownOwned(context.Background(), TeardownOptions{})
	if !errors.Is(err, ErrTeardownIncomplete) || receipt.Status != TeardownRemaining || receipt.Remaining != 1 {
		t.Fatalf("concurrent addition certified: %+v %v", receipt, err)
	}
	if e := outcomes(receipt)["late-writer"]; e.Reason != ReasonAddedDuringTeardown || !store.ids["late-writer"] {
		t.Fatalf("late identity %+v ledger %v", e, store.ids)
	}
}

// Revoked authority makes no Fleet request and changes no ledger entry.
func TestOwnedTeardownRevokedAuthorityAndRefusalsMakeNoRequests(t *testing.T) {
	fm, srv := newFakeFM(t, "a")
	gate := ha.NewGate() // never activated: a standby has no authority
	store := &memoryRegistered{ids: map[string]bool{"a": true}, gate: gate}
	if _, err := NewController(Config{FMURL: srv.URL, Token: "t", Store: store, Delivery: httpretry.Delivery{Gate: gate}}).TeardownOwned(context.Background(), TeardownOptions{}); !errors.Is(err, ha.ErrNotLeader) {
		t.Fatalf("standby teardown err=%v", err)
	}
	failing := &memoryRegistered{ids: map[string]bool{"a": true}, gate: ha.AlwaysLeader{}, fail: true}
	if r, err := NewController(Config{FMURL: srv.URL, Token: "t", Store: failing}).TeardownOwned(context.Background(), TeardownOptions{}); err == nil || r != nil {
		t.Fatal("unreadable ledger produced a receipt")
	}
	if r, err := NewController(Config{FMURL: srv.URL, Token: "t"}).TeardownOwned(context.Background(), TeardownOptions{}); !errors.Is(err, ErrTeardownNoLedger) || r != nil {
		t.Fatalf("no ledger err=%v", err)
	}
	if _, err := NewController(Config{FMURL: srv.URL, Token: "t", Store: store, DryRun: true}).TeardownOwned(context.Background(), TeardownOptions{}); err == nil {
		t.Fatal("dry-run teardown accepted")
	}
	if _, calls := fm.snapshot(); len(calls) != 0 || !store.ids["a"] {
		t.Fatalf("refused teardown sent %v", calls)
	}
}

func hijackClose(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}

// Leadership lost after the unregistration is sent: no read can confirm absence under the old
// authority, so custody stays with the ledger for the next leader.
func TestOwnedTeardownLeadershipLossMidRunRetainsCustody(t *testing.T) {
	fm, srv := newFakeFM(t, "a", "b")
	gate := ha.NewGate()
	_ = gate.Activate(context.Background(), func(context.Context) error { return nil })
	fm.remove = func(w http.ResponseWriter, id string) bool { gate.Revoke(); return false }
	store := &memoryRegistered{ids: map[string]bool{"a": true, "b": true}, gate: gate}
	receipt, err := NewController(Config{FMURL: srv.URL, Token: "t", Store: store, Delivery: httpretry.Delivery{Gate: gate}}).TeardownOwned(context.Background(), TeardownOptions{})
	if !errors.Is(err, ErrTeardownIncomplete) || receipt.Status != TeardownRemaining || receipt.Retired != 0 || receipt.Remaining != 2 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	if !store.ids["a"] || !store.ids["b"] {
		t.Fatalf("revoked leader released custody: %v", store.ids)
	}
	if _, calls := fm.snapshot(); !reflect.DeepEqual(calls, []string{"GetCollector a", "DeleteCollector a"}) {
		t.Fatalf("requests after revocation: %v", calls)
	}
}

// Inventory makes the in-memory fake a bound ledger: it models a written, destination-bound
// document (possibly emptied). Absent documents are modelled by absentLedger below.
func (s *memoryRegistered) Inventory(ctx context.Context) ([]string, bool, error) {
	ids, err := s.Load(ctx)
	return ids, err == nil, err
}

type absentLedger struct{}

func (absentLedger) Load(ctx context.Context) ([]string, error) { return nil, ctx.Err() }
func (absentLedger) Set(context.Context, string, bool) error    { return errors.New("unexpected write") }
func (absentLedger) Inventory(ctx context.Context) ([]string, bool, error) {
	return nil, false, ctx.Err()
}

type loadOnlyLedger struct{ absentLedger }

func (loadOnlyLedger) Inventory() {} // shadows the bound inventory: not a LedgerInventory

// M2: an absent ledger document is not an empty owned set; it certifies nothing.
func TestOwnedTeardownAbsentLedgerIsNotComplete(t *testing.T) {
	fm, srv := newFakeFM(t, "unrecorded")
	receipt, err := NewController(Config{FMURL: srv.URL, Token: "t", Store: absentLedger{}}).TeardownOwned(context.Background(), TeardownOptions{})
	if !errors.Is(err, ErrTeardownLedgerAbsent) || receipt == nil || receipt.Status != TeardownNoLedger || receipt.Inventoried != 0 || receipt.InventoryVerified {
		t.Fatalf("absent ledger certified: %+v %v", receipt, err)
	}
	if LifecycleErrorMessage(err) != ErrTeardownLedgerAbsent.Error() {
		t.Fatalf("diagnostic hidden: %q", LifecycleErrorMessage(err))
	}
	if _, err := NewController(Config{FMURL: srv.URL, Token: "t", Store: loadOnlyLedger{}}).TeardownOwned(context.Background(), TeardownOptions{}); !errors.Is(err, ErrTeardownNoLedger) {
		t.Fatalf("store without a bound inventory accepted: %v", err)
	}
	// An emptied bound ledger is a real, verified empty inventory.
	empty := &memoryRegistered{ids: map[string]bool{}, gate: ha.AlwaysLeader{}}
	receipt, err = NewController(Config{FMURL: srv.URL, Token: "t", Store: empty}).TeardownOwned(context.Background(), TeardownOptions{})
	if err != nil || receipt.Status != TeardownComplete || !receipt.InventoryVerified {
		t.Fatalf("emptied bound ledger: %+v %v", receipt, err)
	}
	if _, calls := fm.snapshot(); len(calls) != 0 {
		t.Fatalf("requests %v", calls)
	}
}

// m2: a redirect to another host is never followed, so another server cannot answer the
// confirmed-absence read.
func TestOwnedTeardownRefusesCrossHostRedirect(t *testing.T) {
	target, targetSrv := newFakeFM(t) // empty registry: would answer not_found
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetSrv.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)
	store := &memoryRegistered{ids: map[string]bool{"owned": true}, gate: ha.AlwaysLeader{}}
	receipt, err := NewController(Config{FMURL: source.URL, Token: "t", Store: store}).TeardownOwned(context.Background(), TeardownOptions{})
	if !errors.Is(err, ErrTeardownIncomplete) || !store.ids["owned"] {
		t.Fatalf("redirected read certified absence: %+v %v", receipt, err)
	}
	if e := outcomes(receipt)["owned"]; e.Outcome != OutcomeUnknown || e.HTTPStatus != http.StatusTemporaryRedirect {
		t.Fatalf("entry %+v", e)
	}
	if _, calls := target.snapshot(); len(calls) != 0 {
		t.Fatalf("redirect followed to another host: %v", calls)
	}
}

// M1: against the vendor contract an unregistered collector is only marked inactive and still
// reads present, so UnregisterCollector can never certify teardown. Owned teardown must delete.
func TestOwnedTeardownDeletesBecauseUnregisterOnlyMarksInactive(t *testing.T) {
	ctx := context.Background()
	fm, srv := newFakeFM(t, "owned")
	client := NewClient(srv.URL, "stack", "t")
	if err := client.UnregisterCollector(ctx, "owned"); err != nil {
		t.Fatal(err)
	}
	if presence, _, err := client.GetCollector(ctx, "owned"); err != nil || presence != PresencePresent {
		t.Fatalf("fake does not model unregister as inactive-only: %v %v", presence, err)
	}
	store := &memoryRegistered{ids: map[string]bool{"owned": true}, gate: ha.AlwaysLeader{}}
	receipt, err := NewController(Config{FMURL: srv.URL, Token: "t", Store: store}).TeardownOwned(ctx, TeardownOptions{})
	if err != nil || receipt.Status != TeardownComplete || outcomes(receipt)["owned"].Reason != ReasonAbsentAfterDelete || len(store.ids) != 0 {
		t.Fatalf("inactive collector not removed: %+v %v", receipt, err)
	}
	reg, calls := fm.snapshot()
	if len(reg) != 0 {
		t.Fatalf("remote %v", reg)
	}
	for _, c := range calls[1:] { // calls[0] is this test's own UnregisterCollector
		if strings.HasPrefix(c, "UnregisterCollector") {
			t.Fatalf("owned teardown relied on UnregisterCollector: %v", calls)
		}
	}
}
