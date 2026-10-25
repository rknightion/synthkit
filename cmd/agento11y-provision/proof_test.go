// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	p "github.com/rknightion/synthkit/internal/agento11yprovision"
	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/runner"
	"golang.org/x/sys/unix"
)

var proofBinary string

func TestMain(m *testing.M) {
	dir, e := os.MkdirTemp("", "agento11y-command-build-")
	if e != nil {
		panic(e)
	}
	proofBinary = filepath.Join(dir, "agento11y-provision")
	// Harness setup, not a command proof: a cold build cache (a separate race job,
	// a fresh CI runner) compiles the whole catalog. Every proof subprocess below
	// keeps its own 5s external bound.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	cmd := exec.CommandContext(ctx, "go", "build", "-o", proofBinary, ".")
	output, e := cmd.CombinedOutput()
	cancel()
	if e != nil {
		fmt.Fprintf(os.Stderr, "source binary build failed: %v %s\n", e, output)
		os.Remove(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.Remove(proofBinary)
	os.Remove(dir)
	os.Exit(code)
}
func fixtureHash(domain string, raw []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte("agento11yprovision/v1/"+domain+"\n"), raw...)))
}
func readObject(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(raw, &doc) != nil {
		t.Fatal("private object invalid")
	}
	return doc
}
func fieldString(t *testing.T, m map[string]json.RawMessage, key string) string {
	t.Helper()
	var s string
	if json.Unmarshal(m[key], &s) != nil {
		t.Fatalf("missing string %s", key)
	}
	return s
}

type commandProcess struct {
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	done     chan error
	out, err bytes.Buffer
	// Test-side diagnostics only: they change no bound, call or assertion.
	verb           string
	started, ended time.Time
	bound          error
}

// commandBound is the external bound every proof subprocess runs under.
const commandBound = 5 * time.Second

func (f *commandFixture) start(args ...string) *commandProcess {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), commandBound)
	pr := &commandProcess{cmd: exec.CommandContext(ctx, proofBinary, args...), cancel: cancel, done: make(chan error, 1)}
	// The bound runs from here, so the margin includes process start.
	pr.started = time.Now()
	if len(args) > 0 {
		pr.verb = args[0]
	}
	pr.cmd.Env = append(os.Environ(), "SSL_CERT_FILE="+f.ca, "SSL_CERT_DIR="+filepath.Dir(f.ca))
	pr.cmd.Stdout = &pr.out
	pr.cmd.Stderr = &pr.err
	if e := pr.cmd.Start(); e != nil {
		cancel()
		f.t.Fatal(e)
	}
	go func() {
		e := pr.cmd.Wait()
		pr.ended, pr.bound = time.Now(), ctx.Err()
		pr.done <- e
	}()
	return pr
}
func (f *commandFixture) finish(pr *commandProcess, want int) {
	f.t.Helper()
	e := <-pr.done
	pr.cancel()
	code := 0
	if e != nil {
		if ee, ok := e.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			f.t.Fatal(e)
		}
	}
	elapsed := pr.ended.Sub(pr.started)
	f.t.Logf("command-margin test=%s verb=%s bound=%s elapsed=%s margin=%.3f", f.t.Name(), pr.verb, commandBound, elapsed.Round(time.Millisecond), float64(commandBound-elapsed)/float64(commandBound))
	if code != want {
		ended := ""
		if errors.Is(pr.bound, context.DeadlineExceeded) {
			ended = fmt.Sprintf(" (ended by its %s external bound after %s, not the behaviour under test)", commandBound, elapsed.Round(time.Millisecond))
		}
		f.t.Fatalf("command %s exit=%d want=%d%s stdout=%s stderr=%s", pr.verb, code, want, ended, &pr.out, &pr.err)
	}
	combined := pr.out.String() + pr.err.String()
	for _, secret := range []string{f.dir, f.server.URL, "fixture-bearer-canary", "fictional-tenant", "Planner"} {
		if strings.Contains(combined, secret) {
			f.t.Fatal("private command output leaked")
		}
	}
}
func (f *commandFixture) run(want int, args ...string) {
	f.t.Helper()
	f.finish(f.start(args...), want)
}

type commandFixture struct {
	t                                  *testing.T
	mu                                 sync.Mutex
	dir, state, binding, bp, token, ca string
	server                             *httptest.Server
	evals                              map[string]p.Evaluator
	hooks                              map[string]p.HookRule
	rules                              map[string]p.Rule
	collections                        map[string]p.Collection
	actions                            map[string]p.RuleAction
	suites                             map[string]p.Suite
	versions                           map[string]p.SuiteVersion
	cases                              map[string]p.SuiteCase
	bindings                           map[string][]p.SuiteBinding
	calls, writes                      []string
	attempts                           map[string]int
	commits                            map[string]p.Coordinate
	// Network/process edge only: pause after a real durable-intent-backed commit.
	pauseRoute string
	entered    chan struct{}
	release    chan struct{}
	pauseUsed  bool
	// Network edge only: commit the backend mutation, then answer 502 as a
	// failing proxy would. The real handler state above is committed first.
	failRoute string
	failUsed  bool
	foreign   bool
	partial   bool
}

const collectionID = "11111111-1111-4111-8111-111111111111"
const actionID = "ra_11111111111111111111111111111111"
const proxyPath = "/sub/api/plugins/grafana-agento11y-app/resources/eval"

func newCommandFixture(t *testing.T) *commandFixture {
	t.Helper()
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	f := &commandFixture{t: t, dir: dir, state: filepath.Join(dir, "state"), evals: map[string]p.Evaluator{}, hooks: map[string]p.HookRule{}, rules: map[string]p.Rule{}, collections: map[string]p.Collection{}, actions: map[string]p.RuleAction{}, suites: map[string]p.Suite{}, versions: map[string]p.SuiteVersion{}, cases: map[string]p.SuiteCase{}, bindings: map[string][]p.SuiteBinding{}, attempts: map[string]int{}, commits: map[string]p.Coordinate{}}
	if os.Mkdir(f.state, 0700) != nil {
		t.Fatal("state directory preparation")
	}
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	f.ca = privateFile(t, filepath.Join(dir, "ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.server.Certificate().Raw}))
	f.binding = privateFile(t, filepath.Join(dir, "binding"), fixtureBinding(t, f.server.URL))
	f.bp = privateFile(t, filepath.Join(dir, "blueprint"), []byte(strings.ReplaceAll(applicationBlueprint, "\n ", "\n")+managementYAML))
	f.token = privateFile(t, filepath.Join(dir, "token"), []byte("fixture-bearer-canary"))
	return f
}
func (f *commandFixture) base() []string {
	return []string{"--binding-file", f.binding, "--state-dir", f.state, "--token-file", f.token}
}
func (f *commandFixture) authored() []string {
	return []string{"--blueprint-file", f.bp, "--blueprint-namespace", ""}
}
func (f *commandFixture) preview(mode, id string) string {
	f.t.Helper()
	out := filepath.Join(f.dir, fmt.Sprintf("preview-%d", time.Now().UnixNano()))
	args := append([]string{"preview"}, f.base()...)
	args = append(args, "--out", out)
	if mode == "apply" {
		args = append(args, f.authored()...)
	} else {
		args = append(args, "--mode", mode, "--construction-id", id, "--expect-enrolled")
	}
	f.run(0, args...)
	st, e := os.Stat(out)
	if e != nil || st.Mode().Perm() != 0600 {
		f.t.Fatal("private preview mode")
	}
	return out
}
func (f *commandFixture) approved(preview, construction string, recovery any) string {
	f.t.Helper()
	raw, e := os.ReadFile(preview)
	if e != nil {
		f.t.Fatal(e)
	}
	return privateFile(f.t, filepath.Join(f.dir, fmt.Sprintf("approval-%d", time.Now().UnixNano())), fixtureApproval(f.t, raw, fmt.Sprintf("approval-%d", time.Now().UnixNano()), construction, recovery))
}
func (f *commandFixture) mutate(verb, construction, preview, approval string, want int) {
	f.t.Helper()
	args := append([]string{verb}, f.base()...)
	args = append(args, "--preview-file", preview, "--approval-file", approval)
	if verb == "apply" {
		args = append(args, f.authored()...)
	} else {
		args = append(args, "--construction-id", construction, "--expect-enrolled")
	}
	f.run(want, args...)
}
func (f *commandFixture) apply() {
	f.t.Helper()
	pv := f.preview("apply", "")
	f.mutate("apply", "", pv, f.approved(pv, "", nil), 0)
}
func (f *commandFixture) snapshot() p.Snapshot {
	f.t.Helper()
	raw, e := os.ReadFile(f.binding)
	if e != nil {
		f.t.Fatal(e)
	}
	b, e := p.DecodeBinding(raw)
	if e != nil {
		f.t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s, e := p.AcquireState(ctx, p.Location{Directory: f.state, ExpectEnrolled: true}, b)
	if e != nil {
		f.t.Fatal(e)
	}
	defer s.Close()
	snap, e := s.Load(ctx)
	if e != nil {
		f.t.Fatal(e)
	}
	return snap
}

// The network edge reads the real inode and complete bounded state before each
// mutation, independently of the command/LockedState lifetime. It does not mock
// the engine, authorization, intent, lock or journal protocol.
func (f *commandFixture) durableIntent(r *http.Request) map[string]json.RawMessage {
	f.t.Helper()
	path := filepath.Join(f.state, "state.json")
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		f.t.Errorf("mutation before durable state: %v", e)
		return nil
	}
	file := os.NewFile(uintptr(fd), "network-observer")
	defer file.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0777 != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
		f.t.Error("invalid durable authority inode")
		return nil
	}
	raw, e := io.ReadAll(io.LimitReader(file, 3342337))
	if e != nil || len(raw) > 3342336 {
		f.t.Error("state read bound")
		return nil
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(raw, &doc) != nil {
		f.t.Error("state decode")
		return nil
	}
	hash := fieldString(f.t, doc, "checksum")
	without := bytes.Replace(raw, []byte(`,"checksum":"`+hash+`"`), nil, 1)
	if bytes.Equal(raw, without) || fixtureHash("state", without) != hash {
		f.t.Error("durable state checksum")
		return nil
	}
	var pending map[string]json.RawMessage
	if json.Unmarshal(doc["pending"], &pending) != nil || pending == nil || fieldString(f.t, pending, "phase") != "intent" || fieldString(f.t, pending, "method") != r.Method {
		f.t.Error("mutation without actual durable Intent")
		return nil
	}
	var reservations []map[string]any
	if json.Unmarshal(doc["reservations"], &reservations) != nil || len(reservations) != 1 || reservations[0]["namespace"] != "planner" {
		f.t.Error("reservation not durable before exposure")
		return nil
	}
	attempt := fieldString(f.t, pending, "attempt_id")
	f.attempts[attempt]++
	if f.attempts[attempt] != 1 {
		f.t.Error("attempt replayed")
	}
	return pending
}

// locked runs fn holding the handler lock. Test-goroutine access to state the
// in-process TLS handlers share goes through it: a subprocess exit is no
// happens-before edge with those handler goroutines.
func (f *commandFixture) locked(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}
func (f *commandFixture) count(fn func() int) (n int) {
	f.locked(func() { n = fn() })
	return n
}
func (f *commandFixture) attemptsOnce() bool {
	once := true
	f.locked(func() {
		for _, n := range f.attempts {
			once = once && n == 1
		}
	})
	return once
}
func (f *commandFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	route := strings.TrimPrefix(r.URL.Path, proxyPath)
	key := r.Method + " " + route
	f.calls = append(f.calls, key)
	if key == f.failRoute && !f.failUsed {
		f.failUsed = true
		upstream := w
		w = httptest.NewRecorder()
		defer http.Error(upstream, "bad gateway", http.StatusBadGateway)
	}
	if !strings.HasPrefix(r.URL.Path, proxyPath+"/") || r.Header.Get("Authorization") != "Bearer fixture-bearer-canary" {
		f.t.Error("wrong fixed route/bearer")
		http.Error(w, "denied", 401)
		return
	}
	for _, k := range []string{"X-Scope-OrgID", "X-Grafana-User", "X-Forwarded-User"} {
		if r.Header.Get(k) != "" {
			f.t.Error("forged principal header")
		}
	}
	var pending map[string]json.RawMessage
	if r.Method != "GET" {
		pending = f.durableIntent(r)
		if pending == nil {
			http.Error(w, "intent", 409)
			return
		}
		f.writes = append(f.writes, key)
	}
	reply := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
	list := func(v any) { reply(map[string]any{"items": v, "next_cursor": ""}) }
	decode := func(v any) bool {
		d := json.NewDecoder(r.Body)
		d.UseNumber()
		d.DisallowUnknownFields()
		if e := d.Decode(v); e != nil {
			f.t.Errorf("invalid mutation body: %v", e)
			http.Error(w, "body", 400)
			return false
		}
		return true
	}
	switch {
	case key == "GET /evaluators":
		if f.partial {
			reply(map[string]any{"items": []any{}})
			return
		}
		v := []p.Evaluator{}
		for _, x := range f.evals {
			if f.foreign {
				x.TenantID = "foreign"
			}
			v = append(v, x)
		}
		list(v)
	case key == "GET /hook-rules":
		v := []p.HookRule{}
		for _, x := range f.hooks {
			v = append(v, x)
		}
		list(v)
	case key == "GET /rules":
		v := []p.Rule{}
		for _, x := range f.rules {
			v = append(v, x)
		}
		list(v)
	case key == "GET /collections":
		v := []p.Collection{}
		for _, x := range f.collections {
			v = append(v, x)
		}
		list(v)
	case key == "GET /test-suites":
		v := []p.Suite{}
		for _, x := range f.suites {
			x.Versions = nil
			v = append(v, x)
		}
		list(v)
	case key == "GET /rules/rule-fresh/actions":
		v := []p.RuleAction{}
		for _, x := range f.actions {
			v = append(v, x)
		}
		reply(map[string]any{"items": v})
	case r.Method == "GET":
		switch route {
		case "/evaluators/eval-fresh":
			if v, ok := f.evals["eval-fresh"]; ok {
				reply(v)
				return
			}
		case "/hook-rules/hook-fresh":
			if v, ok := f.hooks["hook-fresh"]; ok {
				reply(v)
				return
			}
		case "/rules/rule-fresh":
			if v, ok := f.rules["rule-fresh"]; ok {
				reply(v)
				return
			}
		case "/collections/" + collectionID:
			if v, ok := f.collections[collectionID]; ok {
				reply(v)
				return
			}
		case "/rules/rule-fresh/actions/" + actionID:
			if v, ok := f.actions[actionID]; ok {
				reply(v)
				return
			}
		default:
			if f.suiteRead(route, reply, list) {
				return
			}
		}
		http.NotFound(w, r)
	case key == "POST /evaluators":
		var v p.EvaluatorSpec
		if !decode(&v) {
			return
		}
		if v.Description != "Fictional check" || v.Config["pattern"] != "fiction" || v.Config["reject"] != false || v.OutputKeys[0].PassValue == nil || *v.OutputKeys[0].PassValue {
			f.t.Error("authored alias/false/normalization lost")
		}
		f.evals[v.EvaluatorID] = p.Evaluator{EvaluatorSpec: v, TenantID: "fictional-tenant"}
		reply(f.evals[v.EvaluatorID])
	case key == "POST /collections":
		var v p.CollectionSpec
		if !decode(&v) {
			return
		}
		f.collections[collectionID] = p.Collection{TenantID: "fictional-tenant", CollectionID: collectionID, Name: strings.TrimSpace(v.Name), Description: *v.Description, CreatedBy: "fixture", UpdatedBy: "fixture"}
		reply(f.collections[collectionID])
	case key == "POST /rules":
		var v p.RuleSpec
		if !decode(&v) {
			return
		}
		if !reflect.DeepEqual(v.EvaluatorIDs, []string{"eval-fresh"}) || *v.SampleRate != 0 {
			f.t.Error("rule refs/zero lost")
		}
		f.rules[v.RuleID] = p.Rule{TenantID: "fictional-tenant", RuleID: v.RuleID, CreatedBy: "fixture", UpdatedBy: "fixture", RuleSnapshot: p.RuleSnapshot{Enabled: *v.Enabled, Selector: v.Selector, Match: *v.Match, SampleRate: *v.SampleRate, EvaluatorIDs: v.EvaluatorIDs, ExecutionMode: v.ExecutionMode, AlertRuleUIDs: *v.AlertRuleUIDs, FilterableTagKeys: *v.FilterableTagKeys}}
		reply(f.rules[v.RuleID])
	case key == "POST /hook-rules" || key == "PUT /hook-rules/hook-fresh":
		var v p.HookRuleSpec
		if !decode(&v) {
			return
		}
		if *v.Priority != 0 || *v.ShortCircuit || v.ToolFilter == nil || !reflect.DeepEqual(v.ToolFilter.BlockedNames, []string{"fictional_blocked"}) || v.Redact == nil || !reflect.DeepEqual(v.Redact.Patterns, []p.HookRedactPattern{{ID: "redact-one", Regex: "fictional_secret"}}) || !reflect.DeepEqual(v.EvaluatorIDs, []string{"eval-fresh"}) {
			f.t.Error("hook aliases/false/zero lost")
		}
		f.hooks[v.RuleID] = p.HookRule{TenantID: "fictional-tenant", RuleID: v.RuleID, Enabled: *v.Enabled, Phase: v.Phase, Priority: *v.Priority, Selector: v.Selector, Match: v.Match, EvaluatorIDs: v.EvaluatorIDs, ActionOnFail: v.ActionOnFail, ShortCircuit: *v.ShortCircuit, ToolFilter: v.ToolFilter, Redact: v.Redact}
		reply(f.hooks[v.RuleID])
	case key == "POST /rules/rule-fresh/actions":
		var v p.RuleActionSpec
		if !decode(&v) {
			return
		}
		if !reflect.DeepEqual(v.ActionConfig.CollectionIDs, []string{collectionID}) {
			f.t.Error("collection acquisition substitution")
		}
		f.actions[actionID] = p.RuleAction{TenantID: "fictional-tenant", ActionID: actionID, RuleID: "rule-fresh", Condition: v.Condition, ActionConfig: v.ActionConfig, Enabled: *v.Enabled, CreatedBy: "fixture", UpdatedBy: "fixture"}
		reply(f.actions[actionID])
	case key == "PATCH /rules/rule-fresh" || key == "PATCH /rules/rule-fresh/actions/"+actionID:
		var v struct {
			Enabled bool `json:"enabled"`
		}
		if !decode(&v) {
			return
		}
		if v.Enabled {
			f.t.Error("cleanup reenabled consumer")
		}
		if route == "/rules/rule-fresh" {
			x := f.rules["rule-fresh"]
			x.Enabled = false
			f.rules[x.RuleID] = x
			reply(x)
		} else {
			x := f.actions[actionID]
			x.Enabled = false
			f.actions[actionID] = x
			reply(x)
		}
	case r.Method == "DELETE":
		for _, x := range f.rules {
			if x.Enabled {
				f.t.Error("delete before whole consumer disable")
			}
		}
		for _, x := range f.hooks {
			if x.Enabled {
				f.t.Error("delete before hook disable")
			}
		}
		for _, x := range f.actions {
			if x.Enabled {
				f.t.Error("delete before action disable")
			}
		}
		switch route {
		case "/rules/rule-fresh/actions/" + actionID:
			delete(f.actions, actionID)
		case "/hook-rules/hook-fresh":
			delete(f.hooks, "hook-fresh")
		case "/rules/rule-fresh":
			delete(f.rules, "rule-fresh")
		default:
			f.t.Error("unsupported/dependency/foreign DELETE")
		}
		w.WriteHeader(204)
	default:
		if !f.suiteWrite(key, r, decode, reply) {
			f.t.Errorf("unexpected management request %s", key)
			http.NotFound(w, r)
		}
	}
	if pending != nil {
		var co p.Coordinate
		var wire struct {
			Kind       p.ResourceKind `json:"kind"`
			ID         string         `json:"id"`
			Parent     string         `json:"parent_id"`
			Version    string         `json:"version"`
			Unassigned bool           `json:"unassigned"`
		}
		json.Unmarshal(pending["coordinate"], &wire)
		co = p.Coordinate{Kind: wire.Kind, ID: wire.ID, ParentID: wire.Parent, Version: wire.Version, Unassigned: wire.Unassigned}
		if co.Kind == p.KindCollection {
			co.ID = collectionID
			co.Unassigned = false
		}
		if co.Kind == p.KindAction {
			co.ID = actionID
			co.Unassigned = false
		}
		f.commits[fieldString(f.t, pending, "attempt_id")] = co
		if key == f.pauseRoute && !f.pauseUsed {
			f.pauseUsed = true
			close(f.entered)
			// Response bytes are buffered by net/http until return. The actual backend
			// state above is committed, and the process is killed before local result.
			timer := time.NewTimer(4 * time.Second)
			defer timer.Stop()
			select {
			case <-f.release:
			case <-timer.C:
				f.t.Error("handler release deadline")
			}
		}
	}
}

// suiteAuthoring authors one suite closure (five resources) per id. The
// management closure plus one suite is the ten-family, ten-resource maximum
// the custody teardown forecast admits (SKT-0153.03.04.02); two suites make
// fifteen resources, whose apply is refused before any request.
func suiteAuthoring(ids ...string) string {
	var out strings.Builder
	out.WriteString("   suites:\n")
	for _, id := range ids {
		fmt.Fprintf(&out, `   - key: s-%[1]s
     spec: {suite_id: suite-%[1]s, name: "Fictional %[1]s", description: "", tags: []}
     draft:
       key: d-%[1]s
       spec: {changelog: First}
       cases:
       - key: k-%[1]s
         spec: {test_case_id: case-%[1]s, name: Fiction, description: Reviewed, category: test, tags: [], input: {text: fiction, zero: 0, flag: false, integer: 9007199254740991, nullable: null, array: [], object: {}}, expected: {}, metadata: {}, artifact_refs: [], sanitization_reviewed: false, sanitization_content_digest: ""}
       bindings:
         key: b-%[1]s
         bindings: [{binding_id: binding-%[1]s, evaluator_id: e, evaluator_version: "1", input_mappings: {response: test_case.input.text}, report_role: diagnostic}]
       publication:
         key: p-%[1]s
         spec:
           cases: [{test_case_id: case-%[1]s, name: Fiction, description: Reviewed, category: test, tags: [], input: {text: fiction, zero: 0, flag: false, integer: 9007199254740991, nullable: null, array: [], object: {}}, expected: {}, metadata: {}, artifact_refs: []}]
           bindings: [{binding_id: binding-%[1]s, evaluator_id: e, evaluator_version: "1", input_mappings: {response: test_case.input.text}, report_role: diagnostic}]
`, id)
	}
	return out.String()
}
func (f *commandFixture) suiteRead(route string, reply, list func(any)) bool {
	parts := strings.Split(strings.TrimPrefix(route, "/"), "/")
	if len(parts) < 2 || parts[0] != "test-suites" {
		return false
	}
	id := parts[1]
	s, ok := f.suites[id]
	if !ok {
		return false
	}
	if len(parts) == 2 {
		if v, ok := f.versions[id]; ok {
			s.Versions = []p.SuiteVersion{v}
		}
		reply(s)
		return true
	}
	if len(parts) < 5 || parts[2] != "versions" || parts[3] != "v1" {
		return false
	}
	switch parts[4] {
	case "bindings":
		reply(map[string]any{"items": f.bindings[id]})
		return true
	case "test-cases":
		if len(parts) == 6 {
			if cs, ok := f.cases[id+"/"+parts[5]]; ok {
				reply(cs)
				return true
			}
			return false
		}
		all := []p.SuiteCase{}
		for key, cs := range f.cases {
			if strings.HasPrefix(key, id+"/") {
				all = append(all, cs)
			}
		}
		list(all)
		return true
	}
	return false
}
func (f *commandFixture) suiteWrite(key string, r *http.Request, decode func(any) bool, reply func(any)) bool {
	route := strings.TrimPrefix(r.URL.Path, proxyPath)
	parts := strings.Split(strings.TrimPrefix(route, "/"), "/")
	if len(parts) == 1 && key == "POST /test-suites" {
		var v p.SuiteSpec
		if !decode(&v) {
			return true
		}
		f.suites[v.SuiteID] = p.Suite{TenantID: "fictional-tenant", SuiteID: v.SuiteID, Name: v.Name, Description: *v.Description, Tags: *v.Tags}
		reply(f.suites[v.SuiteID])
		return true
	}
	if len(parts) < 3 || parts[0] != "test-suites" {
		return false
	}
	id := parts[1]
	if key == "POST /test-suites/"+id+"/versions" {
		var v struct {
			Changelog string `json:"changelog"`
			Empty     bool   `json:"empty_draft"`
		}
		if !decode(&v) {
			return true
		}
		if !v.Empty {
			f.t.Error("non-fresh draft request")
		}
		if _, exists := f.versions[id]; exists {
			f.t.Error("draft replay")
		}
		f.versions[id] = p.SuiteVersion{SuiteID: id, Version: "v1", Changelog: v.Changelog, Bindings: []p.SuiteBinding{}}
		f.bindings[id] = []p.SuiteBinding{}
		reply(f.versions[id])
		return true
	}
	if len(parts) == 4 && parts[2] == "versions" && parts[3] == "v1:publish" {
		var v struct {
			Review p.PublicationReview `json:"reviewed_publication"`
		}
		if !decode(&v) {
			return true
		}
		if len(v.Review.Cases) != 1 || len(v.Review.Bindings) != 1 || v.Review.Bindings[0].EvaluatorID != "eval-fresh" || v.Review.Bindings[0].EvaluatorVersion != "1" || v.Review.Cases[0].Input["integer"] != json.Number("9007199254740991") {
			f.t.Error("publication lost exact reviewed membership/number/evaluator substitution")
		}
		ver := f.versions[id]
		if ver.Published || ver.TestCaseCount != 1 || len(f.bindings[id]) != 1 {
			f.t.Error("publication not fresh whole checkpoint")
		}
		ver.Published = true
		f.versions[id] = ver
		s := f.suites[id]
		s.LatestVersion = "v1"
		f.suites[id] = s
		reply(ver)
		return true
	}
	if len(parts) != 5 || parts[2] != "versions" || parts[3] != "v1" {
		return false
	}
	if parts[4] == "bindings" && r.Method == "PUT" {
		var v struct {
			Bindings []p.SuiteBinding `json:"bindings"`
		}
		if !decode(&v) {
			return true
		}
		if len(f.bindings[id]) != 0 || len(v.Bindings) != 1 || v.Bindings[0].EvaluatorID != "eval-fresh" {
			f.t.Error("initial bindings replay or wrong exact evaluator")
		}
		f.bindings[id] = v.Bindings
		ver := f.versions[id]
		ver.Bindings = v.Bindings
		f.versions[id] = ver
		reply(map[string]any{"items": v.Bindings})
		return true
	}
	if parts[4] == "test-cases" && r.Method == "POST" {
		var v struct {
			p.CaseSpec
			CreateOnly bool `json:"create_only"`
		}
		if !decode(&v) {
			return true
		}
		if !v.CreateOnly {
			f.t.Error("case upsert")
		}
		if v.Input["integer"] != json.Number("9007199254740991") || v.Input["flag"] != false || v.Input["zero"] != json.Number("0") || v.Input["nullable"] != nil {
			f.t.Error("lossy case input")
		}
		var tags []string
		if v.Tags != nil && len(*v.Tags) > 0 {
			tags = *v.Tags
		}
		var expected, metadata map[string]any
		if v.Expected != nil && len(*v.Expected) > 0 {
			expected = *v.Expected
		}
		if v.Metadata != nil && len(*v.Metadata) > 0 {
			metadata = *v.Metadata
		}
		content := map[string]any{"name": *v.Name, "description": *v.Description, "category": *v.Category, "tags": tags, "input": v.Input, "expected": expected, "metadata": metadata}
		raw, _ := json.Marshal(content)
		evidence := map[string]any{"policy": "secrets-email.v2", "requires_review": false, "changed_paths": []any{}, "content_digest": fmt.Sprintf("sha256:%x", sha256.Sum256(raw))}
		observedMeta := map[string]any{"sigil.sanitization": evidence}
		for k, x := range metadata {
			observedMeta[k] = x
		}
		cs := p.SuiteCase{TenantID: "fictional-tenant", CaseSnapshot: p.CaseSnapshot{TestCaseID: v.TestCaseID, SuiteID: id, SuiteVersion: "v1", Name: *v.Name, Description: *v.Description, Category: *v.Category, Tags: tags, Input: v.Input, Expected: expected, Metadata: observedMeta}}
		f.cases[id+"/"+v.TestCaseID] = cs
		ver := f.versions[id]
		ver.TestCaseCount++
		f.versions[id] = ver
		reply(cs)
		return true
	}
	return false
}

func TestPublicCommandTLSWholeClosure(t *testing.T) {
	f := newCommandFixture(t)
	privateFile(t, f.bp, []byte(strings.ReplaceAll(applicationBlueprint, "\n ", "\n")+managementYAML+suiteAuthoring("one")))
	data, _ := os.ReadFile(f.bp)
	res, err := blueprint.Load(data, runner.Catalog())
	if err != nil {
		t.Fatalf("full authoring load: %v", err)
	}
	binding, _ := p.DecodeBinding(fixtureBinding(t, f.server.URL))
	if _, err = runner.RegistrationPlan(runner.Catalog(), []*blueprint.Resolved{res}, binding); err != nil {
		t.Fatalf("full authoring plan: %v", err)
	}
	// Offline metadata/plan cannot create even the stable state lock or hit TLS.
	for _, args := range [][]string{{"--help"}, {"--version"}, append([]string{"plan", "--binding-file", f.binding}, f.authored()...)} {
		f.run(0, args...)
	}
	if f.count(func() int { return len(f.calls) }) != 0 {
		t.Fatal("offline path performed management HTTP")
	}
	entries, e := os.ReadDir(f.state)
	if e != nil || len(entries) != 0 {
		t.Fatal("offline path wrote custody")
	}
	// Fifteen resources (two suites): the forecast teardown of the resulting
	// state would be refused, so the apply preview refuses before any request.
	privateFile(t, f.bp, []byte(strings.ReplaceAll(applicationBlueprint, "\n ", "\n")+managementYAML+suiteAuthoring("one", "two")))
	over := filepath.Join(f.dir, "over-capacity-preview")
	pr := f.start(append(append([]string{"preview"}, f.base()...), append(f.authored(), "--out", over)...)...)
	f.finish(pr, 2)
	if _, e := os.Stat(over); strings.TrimSpace(pr.err.String()) != `{"reason":"refused"}` || f.count(func() int { return len(f.calls) }) != 0 || !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("fifteen-resource apply not refused before requests: stderr=%s", &pr.err)
	}
	privateFile(t, f.bp, []byte(strings.ReplaceAll(applicationBlueprint, "\n ", "\n")+managementYAML+suiteAuthoring("one")))
	pv := f.preview("apply", "")
	entries, e = os.ReadDir(f.state)
	if e != nil || len(entries) != 1 || entries[0].Name() != "lock" {
		t.Fatal("preview enrolled custody")
	}
	// Missing/misbound approval, existing output and malformed aliases must refuse
	// without a mutation; the positive path below ensures a real command exists.
	bad := privateFile(t, filepath.Join(f.dir, "bad-approval"), []byte(`{}`))
	f.mutate("apply", "", pv, bad, 2)
	f.run(2, append(append([]string{"preview"}, f.base()...), append(f.authored(), "--out", pv)...)...)
	if f.count(func() int { return len(f.writes) }) != 0 {
		t.Fatal("unapproved mutation")
	}
	f.mutate("apply", "", pv, f.approved(pv, "", nil), 0)
	want := []string{"POST /evaluators", "POST /collections", "POST /test-suites", "POST /test-suites/suite-one/versions", "POST /test-suites/suite-one/versions/v1/test-cases", "PUT /test-suites/suite-one/versions/v1/bindings", "POST /test-suites/suite-one/versions/v1:publish", "POST /rules", "POST /hook-rules", "POST /rules/rule-fresh/actions"}
	var writes []string
	f.locked(func() { writes = append(writes, f.writes...) })
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("global full-set phases differ: %v", writes)
	}
	snap := f.snapshot()
	if snap.Document.Pending != nil || len(snap.Document.Receipts) != 1 || snap.Document.Receipts[0].Result != p.ReceiptApplyObserved || len(snap.Document.Receipts[0].Members) != 10 {
		t.Fatal("full observed receipt missing")
	}
	id := snap.Document.Constructions[0].ID
	pv = f.preview("teardown", id)
	f.mutate("teardown", id, pv, f.approved(pv, "", nil), 3)
	after := f.snapshot()
	last := after.Document.Receipts[len(after.Document.Receipts)-1]
	retained := false
	f.locked(func() {
		retained = len(f.suites) == 1 && len(f.evals) == 1 && len(f.collections) == 1 && len(f.rules) == 0 && len(f.actions) == 0 && len(f.hooks) == 0
	})
	if last.Result != p.ReceiptIncomplete || len(last.Members) != 10 || !retained {
		t.Fatal("cleanup dropped retained dependencies/checkpoints or claimed complete")
	}
	if !f.attemptsOnce() {
		t.Fatal("grant replay")
	}
}

// Digests below construct REVIEW INPUTS from the independent network edge's
// committed coordinate/configuration and fresh private inventory, using the
// frozen engine hash domain/typed projections. They do not resolve custody or
// synthesize a receipt; the public command must independently verify them.
func reviewedEngineDigest(binding string, domain string, co p.Coordinate, value any) string {
	raw, _ := json.Marshal(struct {
		Binding    string
		Coordinate p.Coordinate
		Value      any
	}{binding, co, value})
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte("agento11yprovision/engine/v1/"+domain+"\n"), raw...)))
}
func (f *commandFixture) recoveryReview(pv string, snap p.Snapshot) map[string]any {
	f.t.Helper()
	pending := snap.Document.Pending
	view := readObject(f.t, pv)
	bindingHash := fixtureHash("binding", view["binding"])
	inv := fieldString(f.t, view, "inventory")
	f.mu.Lock()
	defer f.mu.Unlock()
	co, ok := f.commits[pending.AttemptID]
	if !ok {
		f.t.Fatal("independent handler attribution missing")
	}
	managed := ""
	presence := p.PresencePresent
	switch co.Kind {
	case p.KindCollection:
		x := f.collections[co.ID]
		description := x.Description
		managed = reviewedEngineDigest(bindingHash, "managed", co, struct {
			Collection *p.CollectionSpec `json:"collection,omitempty"`
		}{&p.CollectionSpec{Name: x.Name, Description: &description}})
	case p.KindAction:
		if pending.Operation == p.OpDelete {
			presence = p.PresenceAbsent
		} else {
			x := f.actions[co.ID]
			managed = reviewedEngineDigest(bindingHash, "managed", co, struct {
				Action *p.RuleActionSpec `json:"action,omitempty"`
			}{&p.RuleActionSpec{Condition: x.Condition, ActionConfig: x.ActionConfig, Enabled: &x.Enabled}})
		}
	default:
		f.t.Fatal("unimplemented independent fixture projection")
	}
	exact := reviewedEngineDigest(bindingHash, "exact", co, struct {
		Presence  p.Presence
		Managed   string
		Inventory string
	}{presence, managed, inv})
	attribution := fixtureHash("fixture-handler-commit", []byte(pending.AttemptID+"/"+co.ID+"/"+string(pending.Operation)))
	var authored p.Digest
	for _, r := range snap.Document.Resources {
		if r.Key == pending.ResourceKey {
			authored = r.Authored.Digest()
		}
	}
	return map[string]any{"attempt_id": pending.AttemptID, "original_input": pending.Input, "original_preview": pending.Preview, "original_approval": pending.OriginalApproval, "resource_key": pending.ResourceKey, "coordinate": map[string]any{"kind": co.Kind, "id": co.ID, "parent_id": co.ParentID, "version": co.Version, "unassigned": co.Unassigned}, "operation": pending.Operation, "authored_hash": authored, "expected_managed_hash": managed, "exact_read": exact, "inventory": inv, "attribution": []any{map[string]string{"id": "independent-handler-commit", "digest": attribution}}, "disposition": "observed"}
}
func TestPublicRefusalsNoMutation(t *testing.T) {
	f := newCommandFixture(t)
	full := strings.ReplaceAll(applicationBlueprint, "\n ", "\n") + managementYAML + suiteAuthoring("one")
	privateFile(t, f.bp, []byte(full))
	f.run(0, append([]string{"plan", "--binding-file", f.binding}, f.authored()...)...)
	for name, source := range map[string]string{
		"alias-conflict": strings.Replace(full, "blocked_tools: [fictional_blocked]", "blocked_tools: [fictional_blocked], tool_filter: {blocked_names: []}", 1),
		"local-ref":      strings.Replace(full, "evaluator_ids: [e]", "evaluator_ids: [remote-id]", 1),
		"parent":         strings.Replace(full, "parent_rule: r", "parent_rule: remote-id", 1),
		"version":        strings.Replace(full, "evaluator_version: \"1\"", "evaluator_version: \"2\"", 1),
		"lossy-integer":  strings.ReplaceAll(full, "9007199254740991", "9007199254740993"),
		"lossy-rate":     strings.Replace(full, "sample_rate: 0", "sample_rate: 0.10000000000000001", 1),
		"unknown":        strings.Replace(full, "priority: 0", "priority: 0, unknown: true", 1),
		"duplicate":      strings.Replace(full, "priority: 0", "priority: 0, priority: 1", 1),
		"case-alias":     strings.Replace(full, "priority: 0", "Priority: 0", 1),
		"anchor":         strings.Replace(full, "spec: {evaluator_id:", "spec: &bad {evaluator_id:", 1),
	} {
		t.Run(name, func(t *testing.T) {
			privateFile(t, f.bp, []byte(source))
			f.run(2, append([]string{"plan", "--binding-file", f.binding}, f.authored()...)...)
		})
	}
	privateFile(t, f.bp, []byte(full))
	original, _ := os.ReadFile(f.binding)
	for _, revision := range []string{"unknown", "agento11yprovision-state-3264k-v2-extra"} {
		privateFile(t, f.binding, bytes.Replace(original, []byte("agento11yprovision-state-3264k-v2"), []byte(revision), 1))
		out := filepath.Join(f.dir, "prior-preview")
		f.run(2, append(append([]string{"preview"}, f.base()...), append(f.authored(), "--out", out)...)...)
	}
	privateFile(t, f.binding, original)
	if f.count(func() int { return len(f.calls) }) != 0 {
		t.Fatal("offline invalid authoring/revision reached network")
	}
	pv := f.preview("apply", "")
	raw, _ := os.ReadFile(pv)
	for _, fault := range []string{"expired", "runtime-evidence", "bound-input"} {
		approved := fixtureApproval(t, raw, "approval-"+fault, "", nil)
		var view map[string]any
		json.Unmarshal(approved, &view)
		switch fault {
		case "expired":
			view["issued_ms"] = time.Now().Add(-2 * time.Minute).UnixMilli()
			view["expires_ms"] = time.Now().Add(-time.Minute).UnixMilli()
		case "runtime-evidence":
			view["assertions"].(map[string]any)["runtime_evidence"] = []any{}
		case "bound-input":
			view["input"] = strings.Repeat("b", 64)
		}
		altered, _ := json.Marshal(view)
		approval := privateFile(t, filepath.Join(f.dir, "bad-"+fault), altered)
		f.mutate("apply", "", pv, approval, 2)
	}
	// Complete inventory is mandatory; no current absence/non-404/partial
	// observation substitutes for it, even when all operator assertions are true.
	f.mu.Lock()
	f.partial = true
	f.mu.Unlock()
	f.mutate("apply", "", pv, f.approved(pv, "", nil), 2)
	f.mu.Lock()
	f.partial = false
	f.mu.Unlock()
	if f.count(func() int { return len(f.writes) }) != 0 {
		t.Fatal("unapproved/incomplete-inventory mutation")
	}
	privateFile(t, f.token, []byte("fixture-bearer-canary\n"))
	f.run(2, append(append([]string{"preview"}, f.base()...), append(f.authored(), "--out", filepath.Join(f.dir, "bad-token-preview"))...)...)
	privateFile(t, f.token, []byte("fixture-bearer-canary"))
	// Trust failure is an actual built-process TLS handshake, never insecure
	// fallback. No new command trust/env surface is introduced by the fixture.
	privateFile(t, f.ca, []byte("not a certificate"))
	f.run(2, append(append([]string{"preview"}, f.base()...), append(f.authored(), "--out", filepath.Join(f.dir, "untrusted-preview"))...)...)
	if f.count(func() int { return len(f.writes) }) != 0 {
		t.Fatal("TLS failure mutated")
	}
}

// A proxy 502 after the backend committed a durable-Intent-backed create is an
// error after custody changed: the command must exit 4 (outcome uncertain,
// preview next), never 2, and must not resend.
func TestPublicLostResponseAfterCommitExitsUnknown(t *testing.T) {
	f := newCommandFixture(t)
	pv := f.preview("apply", "")
	approval := f.approved(pv, "", nil)
	f.locked(func() { f.failRoute = "POST /collections" })
	f.mutate("apply", "", pv, approval, 4)
	committed := false
	f.locked(func() {
		posts := 0
		for _, w := range f.writes {
			if w == "POST /collections" {
				posts++
			}
		}
		committed = f.failUsed && len(f.collections) == 1 && posts == 1
	})
	if !committed {
		t.Fatal("502 edge did not follow a real single committed create")
	}
	if !f.attemptsOnce() {
		t.Fatal("lost response resent")
	}
	snap := f.snapshot()
	if snap.Document == nil {
		t.Fatal("pristine custody unchanged after a committed create")
	}
}

func TestPublicCompetingProcessesAndEnrollment(t *testing.T) {
	f := newCommandFixture(t)
	pv := f.preview("apply", "")
	approval := f.approved(pv, "", nil)
	f.locked(func() {
		f.pauseRoute = "POST /collections"
		f.entered = make(chan struct{})
		f.release = make(chan struct{})
	})
	args := append([]string{"apply"}, f.base()...)
	args = append(args, f.authored()...)
	args = append(args, "--preview-file", pv, "--approval-file", approval)
	first := f.start(args...)
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	select {
	case <-f.entered:
	case <-timer.C:
		first.cancel()
		<-first.done
		t.Fatal("first handler commit not observed")
	}
	second := f.start(args...)
	// Real kernel lock must remain owned while the handler has committed but the
	// original process has not received its response. The second public process
	// uses the very same physical UID/directory/lock and cannot bypass authority.
	lock, e := os.OpenFile(filepath.Join(f.state, "lock"), os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	if e = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); e == nil {
		unix.Flock(int(lock.Fd()), unix.LOCK_UN)
		lock.Close()
		t.Fatal("command released lock around live request")
	}
	lock.Close()
	close(f.release)
	f.finish(first, 0)
	f.finish(second, 2)
	if f.count(func() int { return len(f.writes) }) != 5 {
		t.Fatal("competing process duplicated mutation")
	}
	if !f.attemptsOnce() {
		t.Fatal("competing attempt replay")
	}
	// Retained expectation is not permission to initialize a different directory.
	alternate := filepath.Join(f.dir, "alternate")
	if os.Mkdir(alternate, 0700) != nil {
		t.Fatal("alternate directory")
	}
	originalState := f.state
	f.state = alternate
	out := filepath.Join(f.dir, "missing-enrolled-preview")
	f.run(2, append(append([]string{"preview"}, f.base()...), append(f.authored(), "--expect-enrolled", "--out", out)...)...)
	f.state = originalState
	snap := f.snapshot()
	id := snap.Document.Constructions[0].ID
	// Foreign destination never retargets existing local custody.
	binding, _ := os.ReadFile(f.binding)
	privateFile(t, f.binding, bytes.Replace(binding, []byte("fictional-runtime"), []byte("other-runtime"), 1))
	args = append([]string{"preview"}, f.base()...)
	args = append(args, "--mode", "teardown", "--construction-id", id, "--expect-enrolled", "--out", filepath.Join(f.dir, "foreign-preview"))
	f.run(2, args...)
	privateFile(t, f.binding, binding)
}

func TestPublicOriginalModeLostResponses(t *testing.T) {
	for _, scenario := range []struct{ name, verb, route string }{{"generated-create", "apply", "POST /collections"}, {"original-disable", "deactivate", "PATCH /rules/rule-fresh/actions/" + actionID}, {"original-delete", "teardown", "DELETE /rules/rule-fresh/actions/" + actionID}} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newCommandFixture(t)
			var id string
			if scenario.verb != "apply" {
				f.apply()
				id = f.snapshot().Document.Constructions[0].ID
			}
			pv := f.preview(scenario.verb, id)
			approval := f.approved(pv, "", nil)
			f.locked(func() {
				f.pauseRoute = scenario.route
				f.entered = make(chan struct{})
				f.release = make(chan struct{})
			})
			args := append([]string{scenario.verb}, f.base()...)
			args = append(args, "--preview-file", pv, "--approval-file", approval)
			if scenario.verb == "apply" {
				args = append(args, f.authored()...)
			} else {
				args = append(args, "--construction-id", id, "--expect-enrolled")
			}
			pr := f.start(args...)
			timer := time.NewTimer(4 * time.Second)
			defer timer.Stop()
			select {
			case <-f.entered:
			case <-timer.C:
				pr.cancel()
				<-pr.done
				t.Fatal("real commit handler not reached")
			}
			// Kill only after the handler independently observed durable Intent AND
			// committed the backend mutation; sender entry or a sleep is not a receipt.
			if e := pr.cmd.Process.Kill(); e != nil {
				t.Fatal(e)
			}
			f.finish(pr, -1)
			close(f.release)
			snap := f.snapshot()
			pending := snap.Document.Pending
			if pending == nil || pending.Operation == "" || pending.Phase != p.StepIntent {
				t.Fatal("hard exit lost pending evidence")
			}
			var original p.Construction
			for _, c := range snap.Document.Constructions {
				if c.ID == pending.ConstructionID {
					original = c
				}
			}
			if original.Mode != p.Mode(scenario.verb) || original.Input != pending.Input {
				t.Fatal("pending recast original mode/input")
			}
			// Ordinary restart cannot replay; no fresh cleanup may replace its pending
			// construction, and a sole generated-ID candidate is not attribution.
			before := f.count(func() int { return len(f.writes) })
			// With an operation pending, a refused mutation exits 4 (preview next),
			// never 2: exit 2 claims unchanged custody with nothing pending.
			if scenario.verb == "apply" {
				f.mutate("apply", "", pv, approval, 4)
			} else {
				args := append([]string{"preview"}, f.base()...)
				args = append(args, "--mode", scenario.verb, "--construction-id", id, "--expect-enrolled", "--out", filepath.Join(f.dir, "fresh-cleanup"))
				f.run(2, args...)
			}
			recoverPV := f.preview("recover", pending.ConstructionID)
			f.mutate("recover", pending.ConstructionID, recoverPV, f.approved(recoverPV, pending.ConstructionID, nil), 4)
			still := f.snapshot()
			if still.Document.Pending == nil {
				t.Fatal("current inventory/sole candidate cleared pending")
			}
			review := f.recoveryReview(recoverPV, snap)
			// The attributed settle durably resolves the pending operation and then
			// returns a conflict: custody changed, so exit 4 with preview as the
			// next step, never the exit 2 of an unchanged refusal.
			f.mutate("recover", pending.ConstructionID, recoverPV, f.approved(recoverPV, pending.ConstructionID, review), 4)
			settled := f.snapshot()
			if settled.Document.Pending != nil {
				t.Fatal("independently attributed original pending did not settle")
			}
			var current p.Construction
			for _, c := range settled.Document.Constructions {
				if c.ID == original.ID {
					current = c
				}
			}
			if current.ID != original.ID || current.Mode != original.Mode || current.Input != original.Input || !reflect.DeepEqual(current.Plan, original.Plan) || current.Complete {
				t.Fatal("settled recovery became completion/recast/success")
			}
			if f.count(func() int { return len(f.writes) }) != before {
				t.Fatal("recover resent original mutation")
			}
			// A suffix needs another current preview/approval, preserving the ORIGINAL
			// construction plan, ID and mode. Teardown retains dependencies honestly.
			suffixPV := f.preview("recover", original.ID)
			want := 0
			if scenario.verb == "teardown" {
				want = 3
			}
			f.mutate("recover", original.ID, suffixPV, f.approved(suffixPV, original.ID, nil), want)
			final := f.snapshot()
			if final.Document.Pending != nil {
				t.Fatal("suffix left pending")
			}
			if !f.attemptsOnce() {
				t.Fatal("attempt replayed")
			}
		})
	}
}
