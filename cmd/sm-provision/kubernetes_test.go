// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
	lease "github.com/rknightion/synthkit/internal/ha/kubernetes"
	"github.com/rknightion/synthkit/internal/smstate"
	"github.com/rknightion/synthkit/internal/smstore"
	"github.com/rknightion/synthkit/internal/smstore/smstoretest"
)

func syntheticToken(t *testing.T) string {
	t.Helper()
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
func kubeProvisionConfig(t *testing.T, smURL string) config.SMProvisionConfig {
	return config.SMProvisionConfig{SMURL: smURL, SMToken: syntheticToken(t), Apply: true, StateBackend: "kubernetes", SnapshotPath: "/dev/null/control.json", Namespace: "test", StateSMConfigMap: "sm", ProvisionLeaseName: "provision", EmitterLeaseName: "emitter", PodUID: "tool", LeaseDuration: 30 * time.Second, RenewDeadline: 15 * time.Second, RetryPeriod: 2 * time.Second, KubeRequestTimeout: 2 * time.Second, HTTPTimeout: 5 * time.Second, OperationTimeout: 8 * time.Second, FenceMargin: 2 * time.Second, ReleaseTimeout: 2 * time.Second, CASAttempts: 5}
}
func seedSM(t *testing.T, a *smstoretest.API, cfg config.SMProvisionConfig) smstate.Snapshot {
	t.Helper()
	p := smstate.ProbeSpec{Name: "private", Region: "EMEA", Key: smstate.ProbeKey("private", "EMEA"), Latitude: 50, Longitude: 8}
	c := smstate.CheckSpec{Job: "health", Target: "https://api.example/health", Key: smstate.CheckKey("health", "https://api.example/health"), Source: "sample", ProbeKey: p.Key, FrequencyMS: 60000, TimeoutMS: 3000}
	s, err := smstate.NewSnapshot([]smstate.ProbeSpec{p}, []smstate.CheckSpec{c}, cfg.SMURL, cfg.SMToken, version, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(smstore.Document{SchemaVersion: 1, ProvisionLeaseName: cfg.ProvisionLeaseName, Snapshot: &s})
	a.Set("sm", b)
	return s
}

type provisionProcess struct {
	cmd    *exec.Cmd
	done   chan error
	output bytes.Buffer
	cancel context.CancelFunc
}

func startProvision(t *testing.T, a *smstoretest.API, cfg config.SMProvisionConfig) *provisionProcess {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(cfg)
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	p := &provisionProcess{done: make(chan error, 1), cancel: cancel}
	p.cmd = exec.CommandContext(ctx, exe, "-test.run=^TestKubernetesProvisionProcessHelper$")
	p.cmd.Env = append(os.Environ(), "SYNTHKIT_SM_PROOF_CONFIG="+path, "SYNTHKIT_SM_PROOF_API="+a.Server.URL, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
	p.cmd.Stdout = &p.output
	p.cmd.Stderr = &p.output
	if err := p.cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	go func() { p.done <- p.cmd.Wait() }()
	t.Cleanup(cancel)
	return p
}
func waitProvision(t *testing.T, p *provisionProcess, wantOK bool) string {
	t.Helper()
	var err error
	select {
	case err = <-p.done:
	case <-time.After(11 * time.Second):
		p.cancel()
		t.Fatal("provision process did not join")
	}
	out := p.output.String()
	if (err == nil) != wantOK {
		t.Fatalf("CLI outcome %v expected success=%t\n%s", err, wantOK, out)
	}
	return out
}

type operationObserver struct {
	active   atomic.Int64
	released atomic.Bool
}
type observedTransport struct{ observer *operationObserver }

func (o observedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if o.observer.released.Load() {
		return nil, fmt.Errorf("SM operation after release")
	}
	o.observer.active.Add(1)
	res, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		o.observer.active.Add(-1)
		return nil, err
	}
	res.Body = &observedBody{ReadCloser: res.Body, observer: o.observer}
	return res, nil
}

type observedBody struct {
	io.ReadCloser
	observer *operationObserver
	once     sync.Once
}

func (b *observedBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { b.observer.active.Add(-1) })
	return err
}

type observedElection struct {
	provisionElection
	observer *operationObserver
}

func (e observedElection) Seal(ctx context.Context) error {
	if e.observer.active.Load() != 0 {
		return fmt.Errorf("unjoined real SM HTTP operation at seal")
	}
	return e.provisionElection.Seal(ctx)
}
func (e observedElection) Release(ctx context.Context) error {
	if e.observer.active.Load() != 0 {
		return fmt.Errorf("unjoined real SM HTTP operation at release")
	}
	e.observer.released.Store(true)
	err := e.provisionElection.Release(ctx)
	if err == nil {
		fmt.Println("joined-before-release=true")
	}
	return err
}
func TestKubernetesProvisionProcessHelper(t *testing.T) {
	path := os.Getenv("SYNTHKIT_SM_PROOF_CONFIG")
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
	endpoint := os.Getenv("SYNTHKIT_SM_PROOF_API")
	deps := productionProvisionDependencies()
	observer := &operationObserver{}
	deps.HTTPClient = &http.Client{Transport: observedTransport{observer: observer}}
	deps.Backend = func(g ha.LeaderGate) (control.StateBackend, error) {
		return control.NewKubernetesBackendForClient(control.KubernetesBackendOptions{Gate: g, Namespace: cfg.Namespace, Objects: map[control.Key]string{control.SMState: cfg.StateSMConfigMap}, RequestTimeout: cfg.KubeRequestTimeout, MaxDocumentBytes: control.MaxStateDocumentBytes}, endpoint, &http.Client{})
	}
	deps.Election = func(ctx context.Context, o lease.Options) (provisionElection, error) {
		e, err := lease.NewForClient(ctx, o, endpoint, &http.Client{})
		if err != nil {
			return nil, err
		}
		return observedElection{e, observer}, nil
	}
	if err := runWithDependencies(context.Background(), cfg, deps); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}
func awaitNamedLeaseRead(t *testing.T, a *smstoretest.API, after int) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		a.Mu.Lock()
		n := 0
		for _, r := range a.Requests {
			if strings.Contains(r, "GET /apis/coordination") {
				n++
			}
		}
		a.Mu.Unlock()
		if n > after {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("no real named Lease GET observed")
		case <-tick.C:
		}
	}
}
func TestKubernetesProvisionCLIUsesNamedStateWithoutDisk(t *testing.T) {
	a := smstoretest.New(t, "sm")
	defer a.Close()
	fake := newFakeSM()
	var mu sync.Mutex
	requests := 0
	sm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		if r.Method == "POST" {
			var d smstore.Document
			if json.Unmarshal(a.Data("sm"), &d) != nil || d.Provision.Pending == nil || d.Provision.Registration != nil {
				t.Error("POST before durable pending and activation removal")
			}
		}
		fake.ServeHTTP(w, r)
	}))
	defer sm.Close()
	cfg := kubeProvisionConfig(t, sm.URL)
	s := seedSM(t, a, cfg)
	p := startProvision(t, a, cfg)
	awaitNamedLeaseRead(t, a, 0)
	a.Mu.Lock()
	writes := a.StateWrites
	a.Mu.Unlock()
	mu.Lock()
	calls := requests
	mu.Unlock()
	if writes != 0 || calls != 0 {
		t.Fatalf("pre-lock activity state=%d SM=%d", writes, calls)
	}
	a.Permit()
	out := waitProvision(t, p, true)
	if !strings.Contains(out, "joined-before-release=true") {
		t.Fatal("no positively joined real HTTP operation proof")
	}
	var d smstore.Document
	if err := json.Unmarshal(a.Data("sm"), &d); err != nil {
		t.Fatal(err)
	}
	if d.Provision.Pending != nil || d.Provision.Ownership == nil || d.Provision.Registration == nil {
		t.Fatalf("missing final evidence %+v", d.Provision)
	}
	if err := smstate.ValidateRegistration(s, *d.Provision.Registration); err != nil {
		t.Fatal(err)
	}
	a.Mu.Lock()
	if a.Releases != 1 {
		t.Fatal("planned completion did not release")
	}
	t.Logf("named transcript=%v; complete resources=%d; pending=nil; %s", a.Requests, len(d.Provision.Registration.Resources), strings.TrimSpace(out))
	before := a.StateWrites
	a.Mu.Unlock()
	mu.Lock()
	posts := totalPosts(fake)
	mu.Unlock()
	cfg.Apply = false
	waitProvision(t, startProvision(t, a, cfg), true)
	a.Mu.Lock()
	if a.StateWrites != before {
		t.Error("ordinary preview wrote provision state")
	}
	a.Mu.Unlock()
	mu.Lock()
	if totalPosts(fake) != posts {
		t.Error("preview mutated SM")
	}
	mu.Unlock()
}
func TestKubernetesProvisionLeaseSerializesAndLossClosesAdmission(t *testing.T) {
	t.Run("serialize", func(t *testing.T) {
		a := smstoretest.New(t, "sm")
		defer a.Close()
		a.Permit()
		fake := newFakeSM()
		var mu sync.Mutex
		calls := 0
		received, allow := make(chan struct{}), make(chan struct{})
		sm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			calls++
			first := calls == 1
			mu.Unlock()
			if first {
				close(received)
				select {
				case <-allow:
				case <-r.Context().Done():
					return
				}
			}
			mu.Lock()
			defer mu.Unlock()
			fake.ServeHTTP(w, r)
		}))
		defer sm.Close()
		cfg := kubeProvisionConfig(t, sm.URL)
		seedSM(t, a, cfg)
		first := startProvision(t, a, cfg)
		select {
		case <-received:
		case <-time.After(5 * time.Second):
			t.Fatal("first process never reached real SM handler")
		}
		a.Mu.Lock()
		reads := 0
		for _, r := range a.Requests {
			if strings.Contains(r, "GET /apis/coordination") {
				reads++
			}
		}
		a.Mu.Unlock()
		second := startProvision(t, a, cfg)
		awaitNamedLeaseRead(t, a, reads)
		mu.Lock()
		if calls != 1 {
			t.Fatal("second process bypassed dedicated Lease", calls)
		}
		mu.Unlock()
		a.Mu.Lock()
		if a.StateWrites != 0 || a.Releases != 0 {
			t.Fatal("state write/release while inventory work blocked")
		}
		a.Mu.Unlock()
		close(allow)
		waitProvision(t, first, true)
		waitProvision(t, second, true)
		mu.Lock()
		defer mu.Unlock()
		if totalPosts(fake) != 2 {
			t.Fatal("serialized reconciliation duplicated creates", fake.posts)
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		if a.Releases != 2 {
			t.Fatal("both joined operations must release")
		}
		t.Logf("serialized CLI posts=%v; releases=%d; transcript=%v", fake.posts, a.Releases, a.Requests)
	})
	t.Run("loss", func(t *testing.T) {
		a := smstoretest.New(t, "sm")
		defer a.Close()
		a.Permit()
		observed := make(chan struct{})
		var posts int
		fake := newFakeSM()
		sm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" {
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					t.Error(err)
					return
				}
				posts++
				close(observed)
				<-r.Context().Done()
				return
			}
			fake.ServeHTTP(w, r)
		}))
		defer sm.Close()
		cfg := kubeProvisionConfig(t, sm.URL)
		// Loss is exercised before the typed client's initial burst is exhausted.
		// The 2s state-operation cap plus margin still fits strictly in the gap.
		cfg.LeaseDuration = 4 * time.Second
		cfg.RenewDeadline = 300 * time.Millisecond
		cfg.RetryPeriod = 50 * time.Millisecond
		cfg.KubeRequestTimeout = 100 * time.Millisecond
		cfg.HTTPTimeout = time.Second
		cfg.OperationTimeout = time.Second
		cfg.FenceMargin = 200 * time.Millisecond
		seedSM(t, a, cfg)
		p := startProvision(t, a, cfg)
		select {
		case <-observed:
		case <-time.After(3 * time.Second):
			t.Fatal("no real mutation receipt before loss")
		}
		a.Mu.Lock()
		a.LoseLease = true
		a.Mu.Unlock()
		out := waitProvision(t, p, false)
		var d smstore.Document
		json.Unmarshal(a.Data("sm"), &d)
		a.Mu.Lock()
		defer a.Mu.Unlock()
		if posts != 1 || a.Releases != 0 || d.Provision.Pending == nil || d.Provision.Registration != nil {
			t.Fatal("lease loss admitted further work, released or lost pending")
		}
		if strings.Contains(out, "apply complete") {
			t.Fatal("loss reported success")
		}
		t.Logf("real POST observed before revocation; posts=%d releases=0 retained pending=%s", posts, d.Provision.Pending.Kind)
	})
}

func TestKubernetesProvisionLostSMResponseRetainsPending(t *testing.T) {
	a := smstoretest.New(t, "sm")
	defer a.Close()
	a.Permit()
	fake := newFakeSM()
	fake.abortCheckCreate = true
	sm := httptest.NewServer(fake)
	defer sm.Close()
	cfg := kubeProvisionConfig(t, sm.URL)
	seedSM(t, a, cfg)
	out := waitProvision(t, startProvision(t, a, cfg), false)
	var d smstore.Document
	json.Unmarshal(a.Data("sm"), &d)
	if !strings.Contains(out, "ambiguous") || fake.posts["/api/v1/check/add"] != 1 || d.Provision.Pending == nil || d.Provision.Registration != nil {
		t.Fatal("ambiguous SM result replayed or activated")
	}
	waitProvision(t, startProvision(t, a, cfg), false)
	if fake.posts["/api/v1/check/add"] != 1 {
		t.Fatal("pending did not block next process")
	}
}

func TestKubernetesProvisionRequiresMatchingSnapshotVersion(t *testing.T) {
	a := smstoretest.New(t, "sm")
	defer a.Close()
	calls := 0
	sm := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer sm.Close()
	cfg := kubeProvisionConfig(t, sm.URL)
	s := seedSM(t, a, cfg)
	next, err := smstate.NewSnapshot(s.Probes, s.Checks, cfg.SMURL, cfg.SMToken, "new-release", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if next.SnapshotHash != s.SnapshotHash {
		t.Fatal("release hash changed")
	}
	data, _ := json.Marshal(smstore.Document{SchemaVersion: 1, ProvisionLeaseName: "provision", Snapshot: &next})
	a.Set("sm", data)
	a.Permit()
	out := waitProvision(t, startProvision(t, a, cfg), false)
	if !strings.Contains(out, "version does not match") || calls != 0 {
		t.Fatalf("old tool touched SM or wrong failure: calls=%d %s", calls, out)
	}
}
func TestKubernetesProvisionNamedStateFailuresCloseBeforeMutation(t *testing.T) {
	for _, scenario := range []string{"missing-state", "malformed-lease", "forbidden-lease", "corrupt-state", "wrong-lock", "conflicts"} {
		t.Run(scenario, func(t *testing.T) {
			a := smstoretest.New(t, "sm")
			defer a.Close()
			fake := newFakeSM()
			sm := httptest.NewServer(fake)
			defer sm.Close()
			cfg := kubeProvisionConfig(t, sm.URL)
			seedSM(t, a, cfg)
			a.Permit()
			switch scenario {
			case "missing-state":
				a.Mu.Lock()
				delete(a.Maps, "sm")
				a.Mu.Unlock()
			case "malformed-lease":
				a.Mu.Lock()
				a.Lease.ResourceVersion = ""
				a.Mu.Unlock()
			case "forbidden-lease":
				a.Mu.Lock()
				a.LoseLease = true
				a.Mu.Unlock()
			case "corrupt-state":
				a.Set("sm", []byte(`{"schema_version":1,"pending":"broken"}`))
			case "wrong-lock":
				var d smstore.Document
				json.Unmarshal(a.Data("sm"), &d)
				d.ProvisionLeaseName = "other"
				data, _ := json.Marshal(d)
				a.Set("sm", data)
			case "conflicts":
				a.Mu.Lock()
				a.ConflictState = true
				a.Mu.Unlock()
			}
			out := waitProvision(t, startProvision(t, a, cfg), false)
			if totalPosts(fake) != 0 || strings.Contains(out, "apply complete") {
				t.Fatal("invalid named state mutated SM/reported completion")
			}
			a.Mu.Lock()
			defer a.Mu.Unlock()
			if a.StateWrites != 0 {
				t.Fatal("invalid state wrote document")
			}
			puts := 0
			for _, req := range a.Requests {
				if strings.HasPrefix(req, "PUT /api/v1/") {
					puts++
				}
			}
			if puts > cfg.CASAttempts {
				t.Fatal("conflict attempts escaped cap", puts)
			}
		})
	}
}
func TestKubernetesProvisionLostOwnershipResponseRetainsJournalAfterRealPOST(t *testing.T) {
	a := smstoretest.New(t, "sm")
	defer a.Close()
	fake := newFakeSM()
	sm := httptest.NewServer(fake)
	defer sm.Close()
	cfg := kubeProvisionConfig(t, sm.URL)
	seedSM(t, a, cfg)
	a.Permit()
	a.Mu.Lock()
	a.LoseStateAtWrite = 2
	a.Mu.Unlock()
	waitProvision(t, startProvision(t, a, cfg), false)
	var d smstore.Document
	if err := json.Unmarshal(a.Data("sm"), &d); err != nil {
		t.Fatal(err)
	}
	if d.Provision.Pending == nil || d.Provision.Ownership == nil || totalPosts(fake) != 1 {
		t.Fatal("post-success uncertain persistence lost evidence or replayed SM")
	}
	waitProvision(t, startProvision(t, a, cfg), false)
	if totalPosts(fake) != 1 {
		t.Fatal("pending evidence did not block next invocation")
	}
	t.Log("one real POST; ownership CAS committed then socket lost; pending retained and later CLI blocked")
}
func TestKubernetesProvisionLostStateResponseNeverReplaysAPI(t *testing.T) {
	a := smstoretest.New(t, "sm")
	defer a.Close()
	fake := newFakeSM()
	sm := httptest.NewServer(fake)
	defer sm.Close()
	cfg := kubeProvisionConfig(t, sm.URL)
	seedSM(t, a, cfg)
	a.Permit()
	a.Mu.Lock()
	a.LoseStateResponse = true
	a.Mu.Unlock()
	waitProvision(t, startProvision(t, a, cfg), false)
	var d smstore.Document
	json.Unmarshal(a.Data("sm"), &d)
	if d.Provision.Pending == nil || totalPosts(fake) != 0 {
		t.Fatal("uncertain pending CAS replayed mutation or lost evidence")
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	if a.StateWrites != 1 {
		t.Fatal("uncertain CAS replayed", a.StateWrites)
	}
	t.Logf("committed pending=%s; SM posts=0; named transcript=%v", d.Provision.Pending.Kind, a.Requests)
}
