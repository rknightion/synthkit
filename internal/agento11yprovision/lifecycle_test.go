// SPDX-License-Identifier: AGPL-3.0-only

//go:build darwin || linux

package agento11yprovision

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// D50 deliberately changed the old distinguishability requirement. The exact
// old source/patch/plain+race RED are retained under codex's nested go.mod, not
// relabelled passing. These tests drive the actual C entry points, B public
// file methods and trusted TLS Client; no standalone fake absence predicate.
type engineTLSFixture struct {
	t               *testing.T
	mu              sync.Mutex
	server          *httptest.Server
	client          *Client
	store           *LockedState
	input           PlanInput
	evals           map[string]Evaluator
	extraEvaluators []Evaluator
	notCommitted    map[string]bool
	hooks           map[string]HookRule
	rules           map[string]Rule
	collections     map[string]Collection
	actions         map[string]RuleAction
	suite           *Suite
	version         *SuiteVersion
	cases           map[string]SuiteCase
	bindings        []SuiteBinding
	writes          []string
	calls           []string
	attempts        map[string]int
	committed       map[string]Coordinate
	fault           string
	dropped         bool
	queuedWork      bool
	wantSampleRate  float64 // Default zero retains the original fixture assertion.
	pause           *engineCommitPause
	disablePause    *engineCommitPause
	observer        *os.File
	observedRaw     []byte
	observedDoc     *Document
}
type engineCommitPause struct {
	entered, release chan struct{}
	late             chan error
}

// The simulated BACKEND observes the actual durable file independently of the
// controller's lifetime. It cannot borrow LockedState after a legitimate Close
// or contend with that actor's grant retirement. Atomic replacement can leave
// an opened descriptor on the old valid inode; never follow a link or read an
// unbounded file, and validate the existing foundation checksum/record codec.
func (f *engineTLSFixture) persistedDocument() (*Document, error) {
	fd, e := unix.Openat(int(f.observer.Fd()), "state.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(e, unix.ENOENT) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	file := os.NewFile(uintptr(fd), "fixture-state")
	defer file.Close()
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		return nil, e
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0777 != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink > 1 {
		return nil, ErrInvalid
	}
	limit := custodyStateLimit(f.input.Binding)
	raw, e := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if e != nil {
		return nil, e
	}
	if limit == 0 || len(raw) > limit {
		return nil, ErrCapacity
	}
	// Every observation still opens/stats/reads the actual file. Byte-identical
	// data includes the exact checksum, generation and binding already validated;
	// avoid repeatedly rebuilding the whole immutable history on each GET.
	if bytes.Equal(raw, f.observedRaw) && f.observedDoc != nil {
		if f.observedDoc.Binding != f.input.Binding {
			return nil, ErrBinding
		}
		return f.observedDoc, nil
	}
	d, e := custodyDocumentDecode(raw)
	if e != nil {
		return nil, e
	}
	if d.Binding != f.input.Binding {
		return nil, ErrBinding
	}
	f.observedRaw = append([]byte(nil), raw...)
	f.observedDoc = &d
	return &d, nil
}

func newEngineTLS(t *testing.T, suite bool) *engineTLSFixture {
	t.Helper()
	f := &engineTLSFixture{t: t, evals: map[string]Evaluator{}, hooks: map[string]HookRule{}, rules: map[string]Rule{}, collections: map[string]Collection{}, actions: map[string]RuleAction{}, cases: map[string]SuiteCase{}, bindings: []SuiteBinding{}, attempts: map[string]int{}, committed: map[string]Coordinate{}, notCommitted: map[string]bool{}}
	directory := proofDirectory(t)
	fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.observer = os.NewFile(uintptr(fd), "fixture-state-directory")
	t.Cleanup(func() {
		if e := f.observer.Close(); e != nil {
			t.Error(e)
		}
	})
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.serve(w, r)
		// Acknowledge only after the real handler returned and released its mutex.
		if r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/rules/rule-fresh") && f.disablePause != nil {
			f.disablePause.late <- nil
		}
	}))
	t.Cleanup(f.server.Close)
	var e error
	f.client, e = NewClient(ClientOptions{BaseURL: f.server.URL + "/sub", Token: "fixture-token", ExpectedTenantID: "tenant", HTTPClient: f.server.Client(), Timeout: time.Second})
	if e != nil {
		t.Fatal(e)
	}
	in := proofInput(t)
	in.Binding.SourceRevision = engineSourceRevision
	in.Binding.ContractRevision = "agento11yprovision-state-3264k-v2"
	in.Binding.Destination.GrafanaOrigin = f.server.URL
	a := in.Targets[0]
	spec := func(raw string) AuthoredSpec {
		s, e := NewAuthoredSpec([]byte(raw))
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	in.Resources = []PlannedResource{
		{Key: "e", Origin: a, Coordinate: Coordinate{Kind: KindEvaluator, ID: "eval-fresh", Version: "1"}, Spec: spec(`{"evaluator_id":"eval-fresh","version":"1","kind":"regex","description":" Fictional check ","config":{"pattern":" fiction ","target":"response","reject":false},"output_keys":[{"key":" passes ","type":"bool","pass_value":false}]}`), Namespaces: []string{"planner"}},
		{Key: "c", Origin: a, Coordinate: Coordinate{Kind: KindCollection, Unassigned: true}, Spec: spec(`{"name":" Fictional collection ","description":""}`), Namespaces: []string{"planner"}},
	}
	if suite {
		in.Resources = append(in.Resources,
			PlannedResource{Key: "s", Origin: a, Coordinate: Coordinate{Kind: KindSuite, ID: "suite-fresh"}, Spec: spec(`{"suite_id":"suite-fresh","name":" Fictional suite ","description":"","tags":[" one ","one"]}`), Namespaces: []string{"planner"}},
			PlannedResource{Key: "d", Origin: a, Coordinate: Coordinate{Kind: KindDraft, ParentID: "suite-fresh", Unassigned: true}, Spec: spec(`{"changelog":" First ","empty_draft":true}`), Dependencies: []string{"s"}, Namespaces: []string{"planner"}},
			PlannedResource{Key: "k", Origin: a, Coordinate: Coordinate{Kind: KindCase, ID: "case-fresh", ParentID: "suite-fresh"}, VersionFrom: "d", Spec: spec(`{"test_case_id":"case-fresh","input":{"text":"fiction","zero":0,"flag":false,"array":[],"object":{}},"expected":{},"metadata":{},"tags":[]}`), Dependencies: []string{"s", "d"}, Namespaces: []string{"planner"}},
			PlannedResource{Key: "b", Origin: a, Coordinate: Coordinate{Kind: KindBindings, ParentID: "suite-fresh"}, VersionFrom: "d", Spec: spec(`{"bindings":[{"binding_id":"binding-one","evaluator_id":"e","evaluator_version":"1","input_mappings":{"response":"test_case.input.text"}}]}`), Dependencies: []string{"s", "d", "e"}, Namespaces: []string{"planner"}},
			PlannedResource{Key: "p", Origin: a, Coordinate: Coordinate{Kind: KindPublication, ParentID: "suite-fresh"}, VersionFrom: "d", Spec: spec(`{"cases":[{"test_case_id":"case-fresh","input":{"text":"fiction","zero":0,"flag":false,"array":[],"object":{}},"expected":{},"metadata":{},"tags":[],"artifact_refs":[]}],"bindings":[{"binding_id":"binding-one","evaluator_id":"e","evaluator_version":"1","input_mappings":{"response":"test_case.input.text"}}]}`), Dependencies: []string{"s", "d", "e", "k", "b"}, Namespaces: []string{"planner"}},
		)
		in.Resources[3].Spec = spec(`{"changelog":" First "}`)
	}
	in.Resources = append(in.Resources,
		PlannedResource{Key: "r", Origin: a, Coordinate: Coordinate{Kind: KindRule, ID: "rule-fresh"}, Spec: spec(`{"rule_id":"rule-fresh","enabled":true,"selector":"user_visible_turn","match":{"agent_name":"Planner"},"sample_rate":0,"evaluator_ids":["e"],"execution_mode":"parallel","filterable_tag_keys":[],"alert_rule_uids":[]}`), Dependencies: []string{"e"}, Namespaces: []string{"planner"}},
		PlannedResource{Key: "h", Origin: a, Coordinate: Coordinate{Kind: KindHook, ID: "hook-fresh"}, Spec: spec(`{"rule_id":"hook-fresh","enabled":true,"phase":"preflight","priority":0,"selector":"all","match":{"agent_name":"Planner"},"evaluator_ids":["e"],"action_on_fail":"deny","short_circuit":false,"tool_filter":{"blocked_names":[]},"redact":{"patterns":[]}}`), Dependencies: []string{"e"}, Namespaces: []string{"planner"}},
		PlannedResource{Key: "a", Origin: a, Coordinate: Coordinate{Kind: KindAction, ParentID: "rule-fresh", Unassigned: true}, Spec: spec(`{"condition":{"kind":"all_evaluators_pass"},"action_config":{"kind":"add_to_collection","collection_ids":["c"]},"enabled":true}`), Dependencies: []string{"r", "c"}, Namespaces: []string{"planner"}},
	)
	in.Steps = nil
	for i, r := range in.Resources {
		op := OpCreate
		if r.Coordinate.Kind == KindBindings {
			op = OpInitialBindings
		}
		if r.Coordinate.Kind == KindPublication {
			op = OpPublish
		}
		in.Steps = append(in.Steps, PlannedStep{Number: uint32(i + 1), ResourceKey: r.Key, Operation: op})
	}
	f.input = in
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	f.store, e = AcquireState(context.Background(), Location{Directory: directory}, in.Binding)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.store.Load(ctx); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := f.store.Close(); e != nil {
			t.Error(e)
		}
	})
	return f
}
func fixtureCase(v CaseSpec) SuiteCase {
	tags := []string(nil)
	if v.Tags != nil {
		tags = append(tags, (*v.Tags)...)
	}
	var expected, metadata map[string]any
	if v.Expected != nil {
		expected = *v.Expected
	}
	if v.Metadata != nil {
		metadata = *v.Metadata
	}
	if len(expected) == 0 {
		expected = nil
	}
	if len(metadata) == 0 {
		metadata = nil
	}
	content := map[string]any{"name": "", "description": "", "category": "", "tags": tags, "input": v.Input, "expected": expected, "metadata": metadata}
	if v.Name != nil {
		content["name"] = strings.TrimSpace(*v.Name)
	}
	if v.Description != nil {
		content["description"] = strings.TrimSpace(*v.Description)
	}
	if v.Category != nil {
		content["category"] = strings.TrimSpace(*v.Category)
	}
	raw, _ := json.Marshal(content)
	sum := fmt.Sprintf("sha256:%x", sha256ForFixture(raw))
	outMeta := map[string]any{}
	for k, v := range metadata {
		outMeta[k] = v
	}
	outMeta["sigil.sanitization"] = map[string]any{"policy": "secrets-email.v2", "changed_paths": []any{}, "requires_review": false, "content_digest": sum}
	return SuiteCase{CaseSnapshot: CaseSnapshot{TestCaseID: v.TestCaseID, SuiteID: "suite-fresh", SuiteVersion: "v1", Name: content["name"].(string), Description: content["description"].(string), Category: content["category"].(string), Tags: tags, Input: v.Input, Expected: expected, Metadata: outMeta}, TenantID: "tenant"}
}
func (f *engineTLSFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/sub"+managementPrefix)
	f.calls = append(f.calls, key)
	if r.Header.Get("Authorization") != "Bearer fixture-token" {
		f.t.Error("missing bearer")
		http.Error(w, "auth", 401)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/sub"+managementPrefix+"/") {
		f.t.Error("wrong fixed base")
		http.NotFound(w, r)
		return
	}
	for _, h := range []string{"X-Scope-OrgID", "X-Forwarded-User", "X-Grafana-User"} {
		if r.Header.Get(h) != "" {
			f.t.Error("forged identity header")
		}
	}
	mutation := r.Method != http.MethodGet
	var originalIntent *Pending
	if mutation {
		doc, e := f.persistedDocument()
		if e != nil || doc == nil || doc.Pending == nil || doc.Pending.Phase != StepIntent || doc.Pending.Method != r.Method {
			f.t.Errorf("mutation before real durable pending: %s %v", key, e)
			http.Error(w, "pending", 409)
			return
		}
		pending := doc.Pending
		originalIntent = pending
		names := []string{}
		for _, claim := range doc.Reservations {
			names = append(names, claim.Namespace)
		}
		if fmt.Sprint(names) != "[planner source]" {
			f.t.Errorf("target reservation/provenance wrong: %v", names)
		}
		f.attempts[pending.AttemptID]++
		if f.attempts[pending.AttemptID] != 1 {
			f.t.Error("attempt replayed")
		}
		f.writes = append(f.writes, key)
		if f.fault == "collection-not-applied" && key == "POST /collections" {
			f.notCommitted[pending.AttemptID] = true // independent handler log, not inferred from HTTP503/absence
			http.Error(w, "independently logged refusal before commit", 503)
			return
		}
	} else if f.store != nil {
		doc, e := f.persistedDocument()
		if e != nil {
			f.t.Error(e)
		} else if doc != nil && doc.Pending != nil && doc.Pending.Phase == StepIntent && f.attempts[doc.Pending.AttemptID] == 0 {
			f.t.Error("hidden GET between durable intent and mutation")
		}
	}
	decode := func(v any) bool {
		d := json.NewDecoder(r.Body)
		d.UseNumber()
		d.DisallowUnknownFields()
		if e := d.Decode(v); e != nil {
			f.t.Error(e)
			http.Error(w, "body", 400)
			return false
		}
		return true
	}
	reply := func(v any) { resourceReply(w, v) }
	list := func(v []any) { page(w, v, "") }
	switch key {
	case "GET /evaluators":
		if f.fault == "partial" {
			reply(map[string]any{"items": []any{}})
			return
		}
		if f.fault == "404-alone" {
			http.Error(w, "inventory unavailable", 500)
			return
		}
		if f.fault == "capped" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"items":[],"next_cursor":"`+strings.Repeat("x", (1<<20)+1)+`"}`)
			return
		}
		items := []any{}
		for _, v := range f.evals {
			if f.fault == "list-config-drift" {
				v.Config = map[string]any{"pattern": "different"}
			}
			items = append(items, v)
		}
		for _, v := range f.extraEvaluators {
			items = append(items, v)
		}
		list(items)
	case "GET /hook-rules":
		items := []any{}
		for _, v := range f.hooks {
			items = append(items, v)
		}
		list(items)
	case "GET /rules":
		items := []any{}
		for _, v := range f.rules {
			items = append(items, v)
		}
		list(items)
	case "GET /collections":
		items := []any{}
		for _, v := range f.collections {
			items = append(items, v)
		}
		list(items)
	case "GET /test-suites":
		items := []any{}
		if f.suite != nil {
			copy := *f.suite
			copy.Versions = nil
			items = append(items, copy)
		}
		list(items)
	case "GET /rules/rule-fresh/actions":
		items := []any{}
		for _, v := range f.actions {
			if f.fault == "wrong-parent" {
				v.RuleID = "other-rule"
			}
			items = append(items, v)
		}
		reply(map[string]any{"items": items})
	case "GET /evaluators/eval-fresh":
		if f.fault == "non404" {
			http.Error(w, "forbidden exact route", 403)
			return
		}
		if f.fault == "contradictory-inventory" {
			http.NotFound(w, r)
			return
		}
		v, ok := f.evals["eval-fresh"]
		if !ok {
			if f.fault == "proxy" {
				http.NotFound(w, r)
				return
			}
			http.NotFound(w, r)
			return
		}
		if f.fault == "readback-drift" {
			v.Config = map[string]any{"pattern": "different"}
		}
		reply(v)
	case "GET /hook-rules/hook-fresh":
		v, ok := f.hooks["hook-fresh"]
		if !ok {
			http.NotFound(w, r)
			return
		}
		reply(v)
	case "GET /rules/rule-fresh":
		v, ok := f.rules["rule-fresh"]
		if !ok {
			http.NotFound(w, r)
			return
		}
		reply(v)
	case "GET /collections/11111111-1111-4111-8111-111111111111":
		v, ok := f.collections["11111111-1111-4111-8111-111111111111"]
		if !ok {
			http.NotFound(w, r)
			return
		}
		reply(v)
	case "GET /rules/rule-fresh/actions/ra_11111111111111111111111111111111":
		v, ok := f.actions["ra_11111111111111111111111111111111"]
		if !ok {
			http.NotFound(w, r)
			return
		}
		reply(v)
	case "GET /test-suites/suite-fresh":
		if f.suite == nil {
			http.NotFound(w, r)
			return
		}
		s := *f.suite
		if f.version != nil {
			s.Versions = []SuiteVersion{*f.version}
		}
		reply(s)
	case "GET /test-suites/suite-fresh/versions/v1/test-cases":
		if f.fault == "case-list-partial" && len(f.cases) > 0 {
			resourceReply(w, map[string]any{"items": []any{f.cases["case-fresh"]}})
			return
		}
		items := []any{}
		for _, v := range f.cases {
			if f.fault == "wrong-version" {
				v.SuiteVersion = "v2"
			}
			items = append(items, v)
		}
		list(items)
	case "GET /test-suites/suite-fresh/versions/v1/test-cases/case-fresh":
		v, ok := f.cases["case-fresh"]
		if !ok {
			http.NotFound(w, r)
			return
		}
		reply(v)
	case "GET /test-suites/suite-fresh/versions/v1/bindings":
		reply(map[string]any{"items": f.bindings})
	case "POST /evaluators":
		var v EvaluatorSpec
		if !decode(&v) {
			return
		}
		if f.fault == "heuristic" {
			want := map[string]any{"version": "v2", "root": map[string]any{"kind": "group", "operator": "and", "rules": []any{map[string]any{"kind": "rule", "type": "min_length", "value": json.Number("100")}, map[string]any{"kind": "rule", "type": "contains", "value": "fiction"}}}}
			if v.Kind != "heuristic" || !reflect.DeepEqual(v.Config, want) {
				f.t.Errorf("heuristic source normalization wrong: %#v", v.Config)
			}
		} else if v.Config["pattern"] != "fiction" || v.Config["reject"] != false || len(v.Config) != 2 {
			f.t.Error("regex normalization/false lost")
		}
		if v.Description != "Fictional check" || v.OutputKeys[0].Key != "passes" || v.OutputKeys[0].PassValue == nil || *v.OutputKeys[0].PassValue {
			f.t.Error("output/description/false lost")
		}
		v.OutputKeys[0].Enum = nil
		v.OutputKeys[0].PassMatch = nil
		if pause := f.pause; pause != nil {
			close(pause.entered)
			timer := time.NewTimer(4 * time.Second)
			defer timer.Stop()
			select {
			case <-pause.release:
			case <-timer.C:
				pause.late <- fmt.Errorf("test handler release bound exceeded")
				return
			}
			doc, e := f.persistedDocument()
			if e != nil {
				pause.late <- e
				return
			}
			if doc == nil || doc.Pending == nil || doc.Pending.Operation != OpCreate {
				pause.late <- fmt.Errorf("late backend cannot observe retained durable intent")
				return
			}
			pause.late <- nil
		}
		e := Evaluator{EvaluatorSpec: v, TenantID: "tenant"}
		f.evals[v.EvaluatorID] = e
		reply(e)
	case "POST /collections":
		var v CollectionSpec
		if !decode(&v) {
			return
		}
		c := Collection{TenantID: "tenant", CollectionID: "11111111-1111-4111-8111-111111111111", Name: strings.TrimSpace(v.Name), Description: strings.TrimSpace(*v.Description), CreatedBy: "fixture", UpdatedBy: "fixture"}
		f.collections[c.CollectionID] = c
		f.committed[originalIntent.AttemptID] = Coordinate{Kind: KindCollection, ID: c.CollectionID}
		if f.fault == "drop-collection" && !f.dropped {
			f.dropped = true
			conn, _, e := w.(http.Hijacker).Hijack()
			if e != nil {
				f.t.Error(e)
				return
			}
			conn.Close()
			return
		}
		reply(c)
	case "POST /test-suites":
		var v SuiteSpec
		if !decode(&v) {
			return
		}
		tags := []string{}
		for _, x := range *v.Tags {
			if !slicesContainsForFixture(tags, strings.TrimSpace(x)) {
				tags = append(tags, strings.TrimSpace(x))
			}
		}
		f.suite = &Suite{TenantID: "tenant", SuiteID: v.SuiteID, Name: strings.TrimSpace(v.Name), Description: strings.TrimSpace(*v.Description), Tags: tags}
		reply(f.suite)
	case "POST /test-suites/suite-fresh/versions":
		var v struct {
			Changelog string `json:"changelog"`
			Empty     bool   `json:"empty_draft"`
		}
		if !decode(&v) {
			return
		}
		if !v.Empty || f.version != nil {
			f.t.Error("draft not fresh empty")
		}
		f.version = &SuiteVersion{SuiteID: "suite-fresh", Version: "v1", Changelog: strings.TrimSpace(v.Changelog), Bindings: []SuiteBinding{}}
		reply(f.version)
	case "POST /test-suites/suite-fresh/versions/v1/test-cases":
		var v struct {
			CaseSpec
			CreateOnly bool `json:"create_only"`
		}
		if !decode(&v) {
			return
		}
		if !v.CreateOnly || f.version == nil || f.version.Published {
			f.t.Error("case not create only")
		}
		cs := fixtureCase(v.CaseSpec)
		f.cases[cs.TestCaseID] = cs
		if f.fault == "post-case-drift" {
			input := map[string]any{}
			for k, x := range cs.Input {
				input[k] = x
			}
			input["text"] = "different but still fictional"
			f.cases[cs.TestCaseID] = fixtureCase(CaseSpec{TestCaseID: cs.TestCaseID, Input: input})
		}
		f.version.TestCaseCount = len(f.cases)
		reply(cs)
	case "PUT /test-suites/suite-fresh/versions/v1/bindings":
		var v struct {
			Bindings []SuiteBinding `json:"bindings"`
		}
		if !decode(&v) {
			return
		}
		if f.version == nil || f.version.Published || len(f.bindings) != 0 || v.Bindings[0].EvaluatorID != "eval-fresh" {
			f.t.Error("binding substitution/phase wrong")
		}
		f.bindings = v.Bindings
		f.version.Bindings = v.Bindings
		reply(map[string]any{"items": v.Bindings})
	case "POST /test-suites/suite-fresh/versions/v1:publish":
		var v struct {
			Review PublicationReview `json:"reviewed_publication"`
		}
		if !decode(&v) {
			return
		}
		want := PublicationReview{Cases: []CaseSnapshot{{TestCaseID: "case-fresh", Input: map[string]any{"text": "fiction", "zero": json.Number("0"), "flag": false, "array": []any{}, "object": map[string]any{}}}}, Bindings: []SuiteBinding{{BindingID: "binding-one", EvaluatorID: "eval-fresh", EvaluatorVersion: "1", InputMappings: map[string]string{"response": "test_case.input.text"}}}}
		if f.fault == "case-json" {
			want.Cases[0].Input["nested"] = map[string]any{"name": nil, "NAME": false, "items": []any{nil, map[string]any{}, []any{}}}
			want.Cases[0].Expected = map[string]any{"nullable": nil}
			want.Cases[0].Metadata = map[string]any{"free": nil}
		}
		if !reflect.DeepEqual(v.Review, want) {
			f.t.Errorf("publication body differs from complete independent reviewed fixture: %#v", v.Review)
		}
		if len(v.Review.Cases) != len(f.cases) || len(v.Review.Bindings) != len(f.bindings) || f.version.Published {
			f.t.Error("publication not whole fresh checkpoint")
		}
		f.version.Published = true
		f.suite.LatestVersion = "v1"
		reply(f.version)
	case "POST /rules":
		var v RuleSpec
		if !decode(&v) {
			return
		}
		if v.EvaluatorIDs[0] != "eval-fresh" || *v.SampleRate != f.wantSampleRate {
			f.t.Error("rule substitution/sample rate wrong")
		}
		r := Rule{TenantID: "tenant", RuleID: v.RuleID, RuleSnapshot: RuleSnapshot{Enabled: *v.Enabled, Selector: v.Selector, Match: *v.Match, SampleRate: *v.SampleRate, EvaluatorIDs: v.EvaluatorIDs, ExecutionMode: v.ExecutionMode, AlertRuleUIDs: *v.AlertRuleUIDs, FilterableTagKeys: *v.FilterableTagKeys, MinIdleSeconds: v.MinIdleSeconds}, CreatedBy: "fixture", UpdatedBy: "fixture"}
		f.rules[r.RuleID] = r
		reply(r)
	case "POST /hook-rules", "PUT /hook-rules/hook-fresh":
		var v HookRuleSpec
		if !decode(&v) {
			return
		}
		if v.EvaluatorIDs[0] != "eval-fresh" || *v.Priority != 0 || *v.ShortCircuit {
			f.t.Error("hook substitution/false/zero wrong")
		}
		if r.Method == "PUT" && *v.Enabled {
			f.t.Error("cleanup enabled hook")
		}
		h := HookRule{TenantID: "tenant", RuleID: v.RuleID, Enabled: *v.Enabled, Phase: v.Phase, Priority: *v.Priority, Selector: v.Selector, Match: v.Match, EvaluatorIDs: v.EvaluatorIDs, ActionOnFail: v.ActionOnFail, ShortCircuit: *v.ShortCircuit, ToolFilter: v.ToolFilter, Redact: v.Redact}
		f.hooks[h.RuleID] = h
		reply(h)
	case "POST /rules/rule-fresh/actions":
		var v RuleActionSpec
		if !decode(&v) {
			return
		}
		if v.ActionConfig.CollectionIDs[0] != "11111111-1111-4111-8111-111111111111" {
			f.t.Error("generated collection reference not substituted")
		}
		a := RuleAction{TenantID: "tenant", RuleID: "rule-fresh", ActionID: "ra_11111111111111111111111111111111", Condition: v.Condition, ActionConfig: v.ActionConfig, Enabled: *v.Enabled, CreatedBy: "fixture", UpdatedBy: "fixture"}
		f.actions[a.ActionID] = a
		reply(a)
	case "PATCH /rules/rule-fresh":
		resourceBody(f.t, r, `{"enabled":false}`)
		if f.fault == "disable-refuse" {
			http.Error(w, "refused", 403)
			return
		}
		v := f.rules["rule-fresh"]
		if f.fault != "disable-lies" {
			v.Enabled = false
			if f.fault == "disable-wrong-config" {
				v.SampleRate = 0.75
			}
			f.rules[v.RuleID] = v
			f.committed[originalIntent.AttemptID] = originalIntent.Coordinate
		}
		if pause := f.disablePause; pause != nil {
			// Durable Pending was decoded above; commit and independent attempt
			// association precede this response-only barrier. DELETE stays armed.
			close(pause.entered)
			f.mu.Unlock()
			select {
			case <-pause.release:
			case <-time.After(5 * time.Second):
				f.t.Error("disable response barrier exceeded external bound")
			}
			f.mu.Lock()
		}
		if f.fault == "drop-disable" && !f.dropped {
			f.dropped = true
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				f.t.Error(err)
				return
			}
			conn.Close()
			return
		}
		reply(v)
	case "PATCH /rules/rule-fresh/actions/ra_11111111111111111111111111111111":
		resourceBody(f.t, r, `{"enabled":false}`)
		v := f.actions["ra_11111111111111111111111111111111"]
		v.Enabled = false
		f.actions[v.ActionID] = v
		reply(v)
	case "DELETE /rules/rule-fresh", "DELETE /hook-rules/hook-fresh", "DELETE /rules/rule-fresh/actions/ra_11111111111111111111111111111111":
		for _, v := range f.rules {
			if v.Enabled {
				f.t.Error("delete before WHOLE rule closure disabled")
			}
		}
		for _, v := range f.hooks {
			if v.Enabled {
				f.t.Error("delete before WHOLE hook closure disabled")
			}
		}
		for _, v := range f.actions {
			if v.Enabled {
				f.t.Error("delete before WHOLE action closure disabled")
			}
		}
		switch key {
		case "DELETE /rules/rule-fresh":
			if len(f.actions) != 0 {
				f.t.Error("parent deleted before dependent action")
			}
			delete(f.rules, "rule-fresh")
		case "DELETE /hook-rules/hook-fresh":
			delete(f.hooks, "hook-fresh")
		default:
			delete(f.actions, "ra_11111111111111111111111111111111")
		}
		f.committed[originalIntent.AttemptID] = originalIntent.Coordinate
		if f.fault == "drop-delete" && !f.dropped {
			f.dropped = true
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				f.t.Error(err)
				return
			}
			conn.Close()
			return
		}
		w.WriteHeader(204)
	default:
		f.t.Errorf("unsupported/unexpected route %s", key)
		http.NotFound(w, r)
	}
}
func engineApprovalRaw(t *testing.T, pv Preview, construction string, recovery any) []byte {
	t.Helper()
	raw := proofApprovalJSON(t, pv, "approval-"+fmt.Sprint(time.Now().UnixNano()), construction, time.Now().Add(-time.Second), time.Now().Add(time.Minute), recovery)
	var object, view map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || json.Unmarshal(pv.JSON(), &view) != nil {
		t.Fatal("approval fixture decode")
	}
	object["binding"] = view["binding"]
	raw, _ = json.Marshal(object)
	return raw
}
func engineApproval(t *testing.T, pv Preview, construction string, recovery any) Approval {
	t.Helper()
	a, e := DecodeApproval(engineApprovalRaw(t, pv, construction, recovery))
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func (f *engineTLSFixture) plan() enginePlan {
	p, e := prepareEngine(f.input)
	if e != nil {
		f.t.Fatal(e)
	}
	return p
}
func (f *engineTLSFixture) apply(p enginePlan) (Receipt, error) {
	return f.applyWithin(p, 4*time.Second)
}
func (f *engineTLSFixture) applyWithin(p enginePlan, budget time.Duration) (Receipt, error) {
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	pv, e := previewEngine(ctx, f.client, f.store, p, ModeApply)
	if e != nil {
		return Receipt{}, e
	}
	return applyEngine(ctx, f.client, f.store, p, pv, engineApproval(f.t, pv, "", nil))
}
func engineSnapshot(t *testing.T, f *engineTLSFixture) Snapshot {
	t.Helper()
	s, e := f.store.Load(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return s
}

// Complete six-record nonempty checkpoint and all-ten-family closures retain
// their original stronger admission RED in pre-v2-bc85. Exact contract v2 now
// deliberately enables them; no family/phase/evidence is omitted to pass.
func engineSuiteOnly(f *engineTLSFixture) {
	resources := []PlannedResource{}
	for _, r := range f.input.Resources {
		if r.Key != "c" && !isConsumer(r.Coordinate.Kind) {
			resources = append(resources, r)
		}
	}
	f.input.Resources = resources
	f.input.Steps = nil
	for i, r := range resources {
		op := OpCreate
		if r.Coordinate.Kind == KindBindings {
			op = OpInitialBindings
		}
		if r.Coordinate.Kind == KindPublication {
			op = OpPublish
		}
		f.input.Steps = append(f.input.Steps, PlannedStep{Number: uint32(i + 1), ResourceKey: r.Key, Operation: op})
	}
}

// Plain v2 all-ten apply measured 1.29s versus ~0.5s for five. Existing race
// overhead was ~6.5x; 20s per NEW broad invocation bounds that larger workload
// with headroom, not an SLA. Five-resource 4s, HTTP 1s and cancel/expiry stay fixed.
func TestEngineAllFamiliesApplyNoopCleanupTLS(t *testing.T) {
	for _, scenario := range []string{"standard", "proxy"} {
		t.Run(scenario, func(t *testing.T) {
			f := newEngineTLS(t, true)
			if scenario == "proxy" {
				f.fault = "proxy"
			}
			p := f.plan()
			receipt, e := f.applyWithin(p, 20*time.Second)
			if e != nil || receipt.Result != ReceiptApplyObserved || len(receipt.Members) != 10 || len(f.writes) != 10 {
				t.Fatalf("full ten-family fresh closure: %v %+v writes=%v", e, receipt, f.writes)
			}
			s := engineSnapshot(t, f)
			if s.Document.Pending != nil || f.version == nil || !f.version.Published || len(f.bindings) != 1 || len(f.cases) != 1 {
				t.Fatal("full checkpoint was not actually observed")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			pv, e := previewEngine(ctx, f.client, f.store, p, ModeApply)
			if e != nil {
				t.Fatal(e)
			}
			before := len(f.writes)
			noop, e := applyEngine(ctx, f.client, f.store, p, pv, engineApproval(t, pv, s.Document.Constructions[0].ID, nil))
			if e != nil || noop.Result != ReceiptApplyObserved || len(f.writes) != before {
				t.Fatalf("ten-family noop failed/mutated: %v", e)
			}
			cp := cleanupPlan(t, p, engineSnapshot(t, f))
			ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel2()
			pv, e = previewEngine(ctx2, f.client, f.store, cp, ModeTeardown)
			if e != nil {
				t.Fatal(e)
			}
			result, e := cleanupEngine(ctx2, f.client, f.store, cp, pv, engineApproval(t, pv, "", nil))
			if e == nil || result.Result != ReceiptIncomplete || len(result.Members) != 10 {
				t.Fatalf("full retained teardown not honest: %v %+v", e, result)
			}
			after := engineSnapshot(t, f)
			if after.Document.Pending != nil || len(f.evals) != 1 || len(f.collections) != 1 || f.suite == nil || len(f.rules) != 0 || len(f.hooks) != 0 || len(f.actions) != 0 {
				t.Fatal("dependent-first cleanup/retention actual backend state wrong")
			}
		})
	}
}

// A remote handler can outlive the canceled LOCAL sender and legitimately
// closed actor. It must observe durable disk state, not borrow actor lifetime.
func TestEngineDispatchRetirementAfterCancelOrExpiryTLS(t *testing.T) {
	for _, end := range []string{"cancel", "request-expiry"} {
		t.Run(end, func(t *testing.T) {
			f := newEngineTLS(t, false)
			p := f.plan()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			pv, e := previewEngine(ctx, f.client, f.store, p, ModeApply)
			if e != nil {
				t.Fatal(e)
			}
			approval := engineApproval(t, pv, "", nil)
			pause := &engineCommitPause{entered: make(chan struct{}), release: make(chan struct{}), late: make(chan error, 1)}
			f.pause = pause
			var once sync.Once
			release := func() { once.Do(func() { close(pause.release) }) }
			defer release()
			type result struct {
				receipt Receipt
				err     error
			}
			done := make(chan result, 1)
			go func() { r, e := applyEngine(ctx, f.client, f.store, p, pv, approval); done <- result{r, e} }()
			select {
			case <-pause.entered:
			case <-ctx.Done():
				t.Fatal("real mutation handler never observed durable pending")
			}
			if end == "cancel" {
				cancel()
			}
			var out result
			select {
			case out = <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("local synchronous sender did not return/join")
			}
			want := context.Canceled
			if end == "request-expiry" {
				want = context.DeadlineExceeded
			}
			if !errors.Is(out.err, want) {
				t.Fatalf("request end lost cause: %v", out.err)
			}
			s := engineSnapshot(t, f)
			if s.Document == nil || s.Document.Pending == nil || len(s.Document.Receipts) != 0 {
				t.Fatal("request end cleared pending or invented receipt")
			}
			if e = f.store.Close(); e != nil {
				t.Fatalf("owned local work/grant leaked after sender return: %v", e)
			}
			release()
			select {
			case e = <-pause.late:
				if e != nil {
					t.Fatalf("remote handler's disk observation depended on retired actor: %v", e)
				}
			case <-time.After(time.Second):
				t.Fatal("late handler did not join")
			}
			f.mu.Lock()
			writes := append([]string{}, f.writes...)
			committed := len(f.evals)
			f.mu.Unlock()
			if !reflect.DeepEqual(writes, []string{"POST /evaluators"}) || committed != 1 {
				t.Fatal("remote commit/retry observation wrong")
			}
		})
	}
}
func TestEngineFreshRoundtripTLS(t *testing.T) {
	for _, scenario := range []string{"normal", "proxy", "suite", "suite-proxy"} {
		t.Run(scenario, func(t *testing.T) {
			suite := strings.HasPrefix(scenario, "suite")
			f := newEngineTLS(t, suite)
			if suite {
				engineSuiteOnly(f)
			}
			if strings.Contains(scenario, "proxy") {
				f.fault = "proxy"
			}
			p := f.plan()
			budget := 4 * time.Second
			if suite {
				budget = 20 * time.Second
			}
			receipt, e := f.applyWithin(p, budget)
			if e != nil {
				t.Fatal(e)
			}
			if receipt.Result != ReceiptApplyObserved || len(receipt.Members) != len(f.input.Resources) {
				t.Fatalf("incomplete closure: %+v", receipt)
			}
			s := engineSnapshot(t, f)
			if s.Document.Pending != nil || len(s.Document.Receipts) != 1 || len(f.writes) != len(f.input.Resources) {
				t.Fatal("not exactly one attempt per complete resource")
			}
			if suite && (f.version == nil || !f.version.Published || len(f.bindings) != 1 || len(f.cases) != 1) {
				t.Fatal("checkpoint not actually published/read back")
			}
			before := len(f.writes)
			id := s.Document.Constructions[0].ID
			ctx, cancel := context.WithTimeout(context.Background(), budget)
			defer cancel()
			pv, e := previewEngine(ctx, f.client, f.store, p, ModeApply)
			if e != nil {
				t.Fatal(e)
			}
			noop, e := applyEngine(ctx, f.client, f.store, p, pv, engineApproval(t, pv, id, nil))
			if e != nil || noop.Result != ReceiptApplyObserved || len(f.writes) != before {
				t.Fatalf("unchanged noop mutated/refused: %v", e)
			}
		})
	}
}
func TestEngineSourceNormalizationAdmissionTLS(t *testing.T) {
	// These inputs survive the foundation's duplicate-key/canonical-object
	// checks but must not acquire a prepared plan, make HTTP calls or consume
	// authority. In particular, Go's case-insensitive DTO decoder is not the
	// pinned source's literal-key config contract.
	for _, tc := range []struct{ name, key, old, replacement string }{
		{"sample-precision", "r", `"sample_rate":0`, `"sample_rate":0.10000000000000001`},
		{"sample-upper-range-rounded", "r", `"sample_rate":0`, `"sample_rate":1.00000000000000001`},
		{"sample-underflow", "r", `"sample_rate":0`, `"sample_rate":1e-400`},
		{"sample-negative-underflow", "r", `"sample_rate":0`, `"sample_rate":-1e-400`},
		{"regex-case", "e", `"pattern":`, `"PATTERN":`},
		{"regex-semantic-alias", "e", `"pattern":" fiction "`, `"pattern":" fiction ","Pattern":" fiction "`},
		{"dto-case", "r", `"sample_rate":`, `"SAMPLE_RATE":`},
		{"dto-semantic-alias", "h", `"enabled":true`, `"enabled":true,"Enabled":true`},
		{"output-case", "e", `"pass_value":false`, `"PASS_VALUE":false`},
		{"output-null-bool", "e", `"pass_value":false`, `"pass_value":null`},
		{"output-null-enum", "e", `"pass_value":false`, `"pass_value":false,"enum":null`},
		{"output-null-constraint", "e", `"pass_value":false`, `"pass_value":false,"min":null`},
		{"output-null-description", "e", `"pass_value":false`, `"pass_value":false,"description":null`},
		{"filter-case", "h", `"blocked_names":[]`, `"BLOCKED_NAMES":[]`},
		{"filter-null-list", "h", `"blocked_names":[]`, `"blocked_names":null`},
		{"redact-null-list", "h", `"patterns":[]`, `"patterns":null`},
		{"redact-null-id", "h", `"patterns":[]`, `"patterns":[{"id":null,"regex":"fiction"}]`},
		{"redact-case", "h", `"patterns":[]`, `"patterns":[{"REGEX":"fiction"}]`},
		{"action-case", "a", `"kind":"all_evaluators_pass"`, `"KIND":"all_evaluators_pass"`},
		{"action-semantic-alias", "a", `"collection_ids":["c"]`, `"collection_ids":["c"],"Collection_ids":["c"]`},
		{"suite-null-tag-item", "s", `"tags":[" one ","one"]`, `"tags":["one",null]`},
		{"binding-case", "b", `"report_role"`, `"REPORT_ROLE"`},
		{"binding-null-role", "b", `"evaluator_version":"1"`, `"evaluator_version":"1","report_role":null`},
		{"publication-binding-null-role", "p", `"evaluator_version":"1"`, `"evaluator_version":"1","report_role":null`},
		{"publication-case-null-name", "p", `"test_case_id":"case-fresh"`, `"test_case_id":"case-fresh","name":null`},
		{"publication-case-null-tags", "p", `"tags":[]`, `"tags":null`},
		{"publication-case-alias", "p", `"test_case_id":"case-fresh"`, `"test_case_id":"case-fresh","TEST_CASE_ID":"case-fresh"`},
		{"case-null-tag-item", "k", `"tags":[]`, `"tags":[null]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEngineTLS(t, true)
			// report_role is optional in the baseline; add a valid spelling
			// before probing its source-invalid uppercase alias.
			for i, r := range f.input.Resources {
				if r.Key != tc.key {
					continue
				}
				raw := string(r.Spec.JSON())
				if tc.name == "binding-case" {
					raw = strings.Replace(raw, `"evaluator_version":"1"`, `"evaluator_version":"1","report_role":""`, 1)
				}
				if !strings.Contains(raw, tc.old) {
					t.Fatal("reproduction did not change authored input")
				}
				s, err := NewAuthoredSpec([]byte(strings.Replace(raw, tc.old, tc.replacement, 1)))
				if err != nil {
					t.Fatal(err)
				}
				f.input.Resources[i].Spec = s
			}
			p, err := prepareEngine(f.input)
			if err == nil || p.plan.Digest() != "" || p.specs != nil {
				t.Errorf("source-invalid %s admitted before first write: %v", tc.name, err)
			}
			if len(f.calls) != 0 || len(f.writes) != 0 || !engineSnapshot(t, f).Base.Pristine {
				t.Fatal("invalid authored input made calls or acquired authority")
			}
		})
	}
	for _, tc := range []struct{ name, old, replacement string }{
		{"heuristic-version-case", `"version":"v2"`, `"VERSION":"v2"`},
		{"heuristic-root-case", `"root":`, `"ROOT":`},
		{"heuristic-node-alias", `"operator":"and"`, `"operator":"and","Operator":"and"`},
		{"heuristic-leaf-case", `"value":100`, `"VALUE":100`},
		{"heuristic-leaf-null", `"value":100`, `"value":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEngineTLS(t, false)
			raw := `{"evaluator_id":"eval-fresh","version":"1","kind":"heuristic","config":{"version":"v2","root":{"kind":"group","operator":"and","rules":[{"kind":"rule","type":"min_length","value":100}]}},"output_keys":[{"key":"passes","type":"bool","pass_value":false}]}`
			s, err := NewAuthoredSpec([]byte(strings.Replace(raw, tc.old, tc.replacement, 1)))
			if err != nil {
				t.Fatal(err)
			}
			f.input.Resources[0].Spec = s
			if _, err = prepareEngine(f.input); err == nil {
				t.Errorf("source-invalid %s admitted before first write", tc.name)
			}
			if len(f.calls) != 0 || !engineSnapshot(t, f).Base.Pristine {
				t.Fatal("invalid heuristic made calls or acquired authority")
			}
		})
	}
	for _, bad := range []string{"duplicate-normalized-match", "bad-model-glob", "null-regex-target", "reserved-binding-name"} {
		t.Run(bad, func(t *testing.T) {
			f := newEngineTLS(t, bad == "reserved-binding-name")
			for i, r := range f.input.Resources {
				raw := string(r.Spec.JSON())
				switch bad {
				case "duplicate-normalized-match":
					if r.Key == "r" {
						raw = strings.Replace(raw, `"agent_name":"Planner"`, `"agent_name":"Planner","tags. region":"a","tags.region":"b"`, 1)
					}
				case "bad-model-glob":
					if r.Key == "r" {
						raw = strings.Replace(raw, `"agent_name":"Planner"`, `"agent_name":"Planner","model.name":"["`, 1)
					}
				case "null-regex-target":
					if r.Key == "e" {
						raw = strings.Replace(raw, `"target":"response"`, `"target":null`, 1)
					}
				case "reserved-binding-name":
					if r.Key == "b" || r.Key == "p" {
						raw = strings.ReplaceAll(raw, `"response":"test_case.input.text"`, `"input":"test_case.input.text"`)
					}
				}
				s, e := NewAuthoredSpec([]byte(raw))
				if e != nil {
					t.Fatal(e)
				}
				f.input.Resources[i].Spec = s
			}
			for repeat := 0; repeat < 20; repeat++ {
				if _, e := prepareEngine(f.input); e == nil {
					t.Fatalf("source-invalid %s admitted before first write", bad)
				}
			}
			if len(f.calls) != 0 {
				t.Fatal("authoring normalization made network calls")
			}
		})
	}
	// Positive controls use authored numbers whose decimal value survives the
	// effective wire representation; spelling need not survive, approved bytes
	// must. The TLS handler independently asserts the nonzero request value.
	for _, tc := range []struct {
		token string
		want  float64
	}{
		{"0.10", 0.1}, {"1e-1", 0.1}, {"1.000e0", 1},
		{"5e-324", 5e-324}, {"-0", 0}, {"0e999999999", 0},
	} {
		t.Run("lossless-sample/"+tc.token, func(t *testing.T) {
			f := newEngineTLS(t, false)
			f.wantSampleRate = tc.want
			var authored AuthoredSpec
			for i, r := range f.input.Resources {
				if r.Key == "r" {
					var err error
					authored, err = NewAuthoredSpec([]byte(strings.Replace(string(r.Spec.JSON()), `"sample_rate":0`, `"sample_rate":`+tc.token, 1)))
					if err != nil {
						t.Fatal(err)
					}
					f.input.Resources[i].Spec = authored
				}
			}
			p := f.plan()
			if *p.specs["r"].Rule.SampleRate != tc.want {
				t.Fatal("lossless sample rate changed during preparation")
			}
			receipt, err := f.apply(p)
			if err != nil || receipt.Result != ReceiptApplyObserved || f.rules["rule-fresh"].SampleRate != tc.want {
				t.Fatalf("lossless sample did not reach observed TLS receipt: %v", err)
			}
			for _, r := range engineSnapshot(t, f).Document.Resources {
				if r.Coordinate.Kind == KindRule && (!bytes.Equal(r.Authored.JSON(), authored.JSON()) || r.Authored.Digest() != authored.Digest()) {
					t.Fatal("approved sample-rate bytes/digest rewritten")
				}
			}
		})
	}
	t.Run("case-owned-json-and-optional-empty-containers", func(t *testing.T) {
		f := newEngineTLS(t, true)
		f.fault = "case-json"
		engineSuiteOnly(f)
		authored := map[string]AuthoredSpec{}
		for i, r := range f.input.Resources {
			if r.Key != "k" && r.Key != "p" {
				continue
			}
			raw := strings.Replace(string(r.Spec.JSON()), `"text":"fiction"`, `"text":"fiction","nested":{"name":null,"NAME":false,"items":[null,{},[]]}`, 1)
			raw = strings.Replace(raw, `"expected":{}`, `"expected":{"nullable":null}`, 1)
			raw = strings.Replace(raw, `"metadata":{}`, `"metadata":{"free":null}`, 1)
			s, err := NewAuthoredSpec([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			f.input.Resources[i].Spec = s
			authored[r.Key] = s
		}
		p := f.plan()
		// Same existing six-record workload budget; no caller/HTTP bound changes.
		receipt, err := f.applyWithin(p, 20*time.Second)
		if err != nil || receipt.Result != ReceiptApplyObserved || !f.version.Published {
			t.Fatalf("case-owned JSON was lost or refused: %v", err)
		}
		for _, r := range p.plan.input.Resources {
			if s, ok := authored[r.Key]; ok && (!bytes.Equal(r.Spec.JSON(), s.JSON()) || r.Spec.Digest() != s.Digest()) {
				t.Fatal("case/publication approved bytes changed")
			}
		}
	})
	t.Run("heuristic-v2-effective-tree", func(t *testing.T) {
		f := newEngineTLS(t, false)
		f.fault = "heuristic"
		for i, r := range f.input.Resources {
			if r.Key == "e" {
				raw := `{"evaluator_id":"eval-fresh","version":"1","kind":"heuristic","description":" Fictional check ","config":{"version":" v2 ","target":"response","root":{"kind":"group","operator":" and ","rules":[{"kind":"rule","type":"min_length","value":1e2},{"kind":"rule","type":"contains","value":" fiction "}]}},"output_keys":[{"key":" passes ","type":"bool","pass_value":false}]}`
				s, e := NewAuthoredSpec([]byte(raw))
				if e != nil {
					t.Fatal(e)
				}
				f.input.Resources[i].Spec = s
			}
		}
		p, e := prepareEngine(f.input)
		if e != nil {
			t.Fatal(e)
		}
		receipt, e := f.apply(p)
		if e != nil || receipt.Result != ReceiptApplyObserved {
			t.Fatalf("heuristic effective config did not roundtrip: %v", e)
		}
	})
}
func TestEngineContractRevisionIsExactBeforeCalls(t *testing.T) {
	f := newEngineTLS(t, false)
	p := f.plan()
	for _, revision := range []string{"", "contract-1", "agento11yprovision-state-3264k-v2-unknown"} {
		t.Run(revision, func(t *testing.T) {
			copy, e := NewPlan(f.input)
			if e != nil {
				t.Fatal(e)
			}
			in := copy.input
			in.Binding.ContractRevision = revision
			before := len(f.calls)
			if _, e := prepareEngine(in); !errors.Is(e, ErrBinding) {
				t.Fatalf("legacy/unknown contract revision not rejected before plan/network: %v", e)
			}
			if len(f.calls) != before {
				t.Fatal("revision probe made network calls")
			}
			if revision != "" {
				legacy, e := NewPlan(in)
				if e != nil {
					t.Fatal(e)
				}
				bad := enginePlan{plan: legacy, specs: p.specs}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if _, e = previewEngine(ctx, f.client, f.store, bad, ModeApply); !errors.Is(e, ErrBinding) || len(f.calls) != before {
					t.Fatalf("runtime contract crossing made calls/authority: %v", e)
				}
			}
		})
	}
}
func TestEngineApprovedAbsenceTruthTableTLS(t *testing.T) {
	for _, fault := range []string{"404-alone", "partial", "capped", "foreign", "contradictory-inventory", "non404", "paid"} {
		t.Run(fault, func(t *testing.T) {
			f := newEngineTLS(t, false)
			f.fault = fault
			if fault == "foreign" || fault == "contradictory-inventory" || fault == "paid" {
				v := Evaluator{TenantID: "tenant", EvaluatorSpec: EvaluatorSpec{EvaluatorID: "eval-fresh", Version: "old", Kind: "regex", Config: map[string]any{"pattern": "fiction"}, OutputKeys: []OutputKey{{Key: "passes", Type: "bool"}}}}
				if fault == "foreign" {
					v.TenantID = "foreign"
				}
				if fault == "paid" {
					v.Kind = "llm_judge"
				}
				f.evals[v.EvaluatorID] = v
			}
			_, e := f.apply(f.plan())
			if e == nil || len(f.writes) != 0 || !engineSnapshot(t, f).Base.Pristine {
				t.Fatalf("unverified absence granted write/custody: %v", e)
			}
		})
	}
	for _, revision := range []string{"", "source-1", "sigil-bc963fb09-engine-inventory404-v2"} {
		t.Run("revision/"+revision, func(t *testing.T) {
			f := newEngineTLS(t, false)
			in := f.input
			in.Binding.SourceRevision = revision
			if _, e := prepareEngine(in); e == nil || len(f.calls) != 0 {
				t.Fatal("unknown revision admitted")
			}
		})
	}
}
func TestEngineOwnedEvaluatorListConfigMustMatchExactTLS(t *testing.T) {
	f := newEngineTLS(t, false)
	p := f.plan()
	if _, e := f.apply(p); e != nil {
		t.Fatal(e)
	}
	f.fault = "list-config-drift"
	before := len(f.writes)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if _, e := previewEngine(ctx, f.client, f.store, p, ModeApply); e == nil || len(f.writes) != before {
		t.Fatal("same-coordinate list/exact configuration contradiction accepted")
	}
}
func TestEngineOwnedEvaluatorInventoryCannotHideOtherVersionTLS(t *testing.T) {
	f := newEngineTLS(t, false)
	p := f.plan()
	if _, e := f.apply(p); e != nil {
		t.Fatal(e)
	}
	other := f.evals["eval-fresh"]
	other.Version = "another-version"
	f.extraEvaluators = []Evaluator{other}
	before := len(f.writes)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if _, e := previewEngine(ctx, f.client, f.store, p, ModeApply); e == nil || len(f.writes) != before {
		t.Fatal("extra version under owned identity hidden behind unchanged GET-latest")
	}
}
func TestEnginePostCommitAmbiguityTLS(t *testing.T) {
	for _, fault := range []string{"drop-collection", "readback-drift"} {
		t.Run(fault, func(t *testing.T) {
			f := newEngineTLS(t, false)
			f.fault = fault
			p := f.plan()
			_, e := f.apply(p)
			if e == nil {
				t.Fatal("ambiguous mutation accepted")
			}
			s := engineSnapshot(t, f)
			if s.Document == nil || s.Document.Pending == nil || len(s.Document.Receipts) != 0 {
				t.Fatal("pending/real-only custody lost")
			}
			before := len(f.writes)
			_, e = f.apply(p)
			if e == nil || len(f.writes) != before {
				t.Fatal("normal apply retried ambiguous attempt")
			}
			for _, w := range f.writes {
				if strings.Contains(w, "/rules") || strings.Contains(w, "hook-rules") {
					t.Fatal("descendant before verified readback")
				}
			}
		})
	}
}

// The operator fixture obtains identity association from a separate backend
// commit log captured BEFORE the response was dropped, never from a sole name
// or list difference. Current exact/inventory hashes bind reviewed observation,
// not evidence of noncommit or attribution by themselves.
func engineRecoveryInput(t *testing.T, f *engineTLSFixture, p enginePlan) (Preview, map[string]any) {
	t.Helper()
	s := engineSnapshot(t, f)
	pending := s.Document.Pending
	if pending == nil {
		t.Fatal("no original attempt")
	}
	f.mu.Lock()
	co, ok := f.committed[pending.AttemptID]
	f.mu.Unlock()
	if !ok {
		t.Fatal("independent original-request association missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	pv, e := previewEngine(ctx, f.client, f.store, p, ModeRecover)
	if e != nil {
		t.Fatal(e)
	}
	inv, e := engineInventoryRead(ctx, f.client, p, s)
	if e != nil {
		t.Fatal(e)
	}
	o, _, e := engineReadExact(ctx, f.client, p, inv, co)
	if e != nil {
		t.Fatal(e)
	}
	association := sha256.Sum256([]byte(pending.AttemptID + "|" + string(pending.Operation) + "|" + co.ID + "|" + co.ParentID))
	coord := map[string]any{"kind": co.Kind, "id": co.ID, "parent_id": co.ParentID, "version": co.Version, "unassigned": false}
	rec := map[string]any{"attempt_id": pending.AttemptID, "original_input": pending.Input, "original_preview": pending.Preview, "original_approval": pending.OriginalApproval, "resource_key": pending.ResourceKey, "coordinate": coord, "operation": pending.Operation, "authored_hash": engineRecord(s, pending.ResourceKey).Authored.Digest(), "expected_managed_hash": o.ManagedHash, "exact_read": o.ExactRead, "inventory": o.Inventory, "attribution": []any{map[string]any{"id": "independently-reviewed-backend-original-request", "digest": fmt.Sprintf("%x", association)}}, "disposition": "observed"}
	return pv, rec
}
func TestEngineIndependentNoncommitOnlyNeverAttemptedSuffixTLS(t *testing.T) {
	f := newEngineTLS(t, false)
	f.fault = "collection-not-applied"
	p := f.plan()
	if _, e := f.apply(p); e == nil {
		t.Fatal("refused collection completed apply")
	}
	s := engineSnapshot(t, f)
	pending := s.Document.Pending
	if pending == nil || pending.ResourceKey != "c" {
		t.Fatal("original collection attempt not retained")
	}
	f.mu.Lock()
	rejected := f.notCommitted[pending.AttemptID]
	_, committed := f.committed[pending.AttemptID]
	f.mu.Unlock()
	if !rejected || committed {
		t.Fatal("no independent joined-handler noncommit evidence")
	}
	f.fault = ""
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	pv, e := previewEngine(ctx, f.client, f.store, p, ModeRecover)
	if e != nil {
		cancel()
		t.Fatal(e)
	}
	inv, e := engineInventoryRead(ctx, f.client, p, s)
	if e != nil {
		cancel()
		t.Fatal(e)
	}
	resolved, e := engineResolved(p, *planResource(p.plan.input, "c"), s)
	if e != nil {
		cancel()
		t.Fatal(e)
	}
	expected, e := engineDigest(p.plan.input.Binding, "managed", pending.Coordinate, resolved)
	if e != nil {
		cancel()
		t.Fatal(e)
	}
	association := sha256.Sum256([]byte("handler-confirmed-no-commit-and-returned|" + pending.AttemptID))
	// No exact GET exists for an unassigned server ID. The NotApplied approval
	// retains the original pre-dispatch observation digest; it is NOT a fabricated
	// absent entity. Independent handler evidence, not current inventory/HTTP503,
	// supplies the reviewed noncommit/no-inflight assertion.
	rec := map[string]any{"attempt_id": pending.AttemptID, "original_input": pending.Input, "original_preview": pending.Preview, "original_approval": pending.OriginalApproval, "resource_key": pending.ResourceKey, "coordinate": map[string]any{"kind": pending.Coordinate.Kind, "id": "", "parent_id": "", "version": "", "unassigned": true}, "operation": pending.Operation, "authored_hash": engineRecord(s, "c").Authored.Digest(), "expected_managed_hash": expected, "exact_read": pending.PreObservation, "inventory": inv.Digest, "attribution": []any{map[string]any{"id": "reviewed-joined-handler-noncommit", "digest": fmt.Sprintf("%x", association)}}, "disposition": "not_applied"}
	_, e = recoverEngine(ctx, f.client, f.store, p, pv, engineApproval(t, pv, pending.ConstructionID, rec))
	cancel()
	after := engineSnapshot(t, f)
	if e == nil || after.Document.Pending != nil || engineRecord(after, "c").Status != ResourceNotApplied || !engineRecord(after, "c").Coordinate.Unassigned {
		t.Fatal("noncommit lost reservation/tombstone")
	}
	before := len(f.writes)
	ctx, cancel = context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	pv, e = previewEngine(ctx, f.client, f.store, p, ModeRecover)
	if e != nil {
		t.Fatal(e)
	}
	_, e = recoverEngine(ctx, f.client, f.store, p, pv, engineApproval(t, pv, pending.ConstructionID, nil))
	if e == nil {
		t.Fatal("failed collection dependency made complete receipt")
	}
	suffix := f.writes[before:]
	if !reflect.DeepEqual(suffix, []string{"POST /rules", "POST /hook-rules"}) {
		t.Fatalf("only independent never-attempted work may proceed, not collection replay/dependent action: %v", suffix)
	}
	final := engineSnapshot(t, f)
	cons := engineConstruction(final, pending.ConstructionID)
	if cons.Complete || len(final.Document.Receipts) != 0 || final.Document.Pending != nil || engineRecord(final, "c").Status != ResourceNotApplied {
		t.Fatal("incomplete original construction silently completed/reset")
	}
}

// A teardown construction is not an action-DELETE attempt. An earlier rule
// DISABLE can commit while its response expires, with the action still owned.
func TestEngineTeardownExpiryBeforeFirstDeleteTLS(t *testing.T) {
	f := newEngineTLS(t, false)
	p := f.plan()
	if _, e := f.apply(p); e != nil {
		t.Fatal(e)
	}
	initial := engineSnapshot(t, f)
	action := engineRecord(initial, "a")
	if action == nil || action.Status != ResourceOwned || action.ManagedHash == "" || len(action.Acquisition) == 0 {
		t.Fatal("initial acquired action missing")
	}
	original := cleanupPlan(t, p, initial)
	f.fault = "drop-delete"
	pause := &engineCommitPause{entered: make(chan struct{}), release: make(chan struct{}), late: make(chan error, 1)}
	f.disablePause = pause
	var once sync.Once
	release := func() { once.Do(func() { close(pause.release) }) }
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	bound := time.NewTimer(5 * time.Second)
	defer bound.Stop()
	pv, e := previewEngine(ctx, f.client, f.store, original, ModeTeardown)
	if e != nil {
		t.Fatal(e)
	}
	approval := engineApproval(t, pv, "", nil)
	done := make(chan error, 1)
	go func() {
		_, err := cleanupEngine(ctx, f.client, f.store, original, pv, approval)
		done <- err
	}()
	select {
	case <-pause.entered:
	case <-bound.C:
		t.Fatal("real committed rule handler did not enter barrier within 5s")
	}
	select {
	case e = <-done:
	case <-bound.C:
		t.Fatal("local sender did not return within 5s")
	}
	callerErr := ctx.Err()
	release()
	select {
	case <-pause.late:
	case <-bound.C:
		t.Fatal("rule handler did not actually return within 5s")
	}
	if !errors.Is(e, context.DeadlineExceeded) || callerErr != nil {
		t.Fatalf("want existing HTTP 1s expiry, not caller 4s expiry: error=%v caller=%v", e, callerErr)
	}
	s := engineSnapshot(t, f)
	pending := s.Document.Pending
	if pending == nil || pending.Operation != OpDisable || pending.ResourceKey != "r" || pending.Coordinate != engineRecord(initial, "r").Coordinate || pending.Phase != StepIntent || pending.AttemptID == "" {
		t.Fatalf("earlier rule intent not retained: %+v", pending)
	}
	f.mu.Lock()
	co, associated := f.committed[pending.AttemptID]
	backendAction, actionPresent := f.actions[action.Coordinate.ID]
	ruleDisabled := !f.rules[pending.Coordinate.ID].Enabled
	dropped := f.dropped
	writes := append([]string(nil), f.writes...)
	f.mu.Unlock()
	if !associated || co != pending.Coordinate || !ruleDisabled || dropped || !actionPresent || !backendAction.Enabled {
		t.Fatalf("not the independent earlier-DISABLE counterexample: associated=%t coordinate=%+v disabled=%t dropped=%t action=%t enabled=%t", associated, co, ruleDisabled, dropped, actionPresent, backendAction.Enabled)
	}
	for _, w := range writes {
		if strings.HasPrefix(w, "DELETE ") {
			t.Fatalf("DELETE occurred before counterexample recovery: %s", w)
		}
	}
	deadline, _ := ctx.Deadline()
	t.Logf("HTTP=1s caller=4s callerErr=%v remaining=%s originalError=%v pendingOperation=%s key=%s coordinate=%+v phase=%s attempt=%s head=%+v actionStatus=%s hash=%s acquisition=%v", callerErr, time.Until(deadline), e, pending.Operation, pending.ResourceKey, pending.Coordinate, pending.Phase, pending.AttemptID, *s.Base.Head, engineRecord(s, "a").Status, action.ManagedHash, action.Acquisition)
	cons := engineConstruction(s, pending.ConstructionID)
	if cons == nil || cons.Mode != ModeTeardown || cons.Input != original.plan.Digest() {
		t.Fatal("original teardown construction lost")
	}
	pv, rec := engineRecoveryInput(t, f, original)
	recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer recoveryCancel()
	_, recoveryErr := recoverEngine(recoveryCtx, f.client, f.store, original, pv, engineApproval(t, pv, cons.ID, rec))
	after := engineSnapshot(t, f)
	settled := engineConstruction(after, cons.ID)
	if !errors.Is(recoveryErr, ErrConflict) || after.Document.Pending != nil || engineRecord(after, "r").Status != ResourceDisabled || settled == nil || settled.ID != cons.ID || settled.Input != cons.Input || settled.Mode != cons.Mode {
		t.Fatalf("exact rule recovery did not settle original attempt: %v pending=%+v", recoveryErr, after.Document.Pending)
	}
	if !reflect.DeepEqual(engineRecord(s, "a"), action) || !reflect.DeepEqual(engineRecord(after, "a"), action) {
		t.Fatal("earlier rule recovery changed action custody/hash/acquisition")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !reflect.DeepEqual(f.writes, writes) || f.dropped || !reflect.DeepEqual(f.actions[action.Coordinate.ID], backendAction) || f.attempts[pending.AttemptID] != 1 {
		t.Fatal("recovery replayed or deleted action")
	}
	t.Logf("recovered operation=%s key=%s attempt=%s head=%+v actionStatus=%s hash=%s acquisition=%v writes=%v", pending.Operation, pending.ResourceKey, pending.AttemptID, *after.Base.Head, engineRecord(after, "a").Status, engineRecord(after, "a").ManagedHash, engineRecord(after, "a").Acquisition, f.writes)
	// Only the exact rule DISABLE was settled. Construction mode must never
	// substitute for the pending operation when asserting an action tombstone.
}

func TestEngineLostResponseRecoveryRetainsOriginalConstructionTLS(t *testing.T) {
	for _, mode := range []Mode{ModeApply, ModeDeactivate, ModeTeardown} {
		t.Run(string(mode), func(t *testing.T) {
			f := newEngineTLS(t, false)
			p := f.plan()
			original := p
			var originalError error
			var acquiredAction *ResourceRecord
			if mode == ModeApply {
				f.fault = "drop-collection"
				if _, e := f.apply(p); e == nil {
					t.Fatal("lost create response accepted")
				}
			} else {
				if _, e := f.apply(p); e != nil {
					t.Fatal(e)
				}
				if mode == ModeDeactivate {
					original = engineConsumerCleanupPlan(t, p, engineSnapshot(t, f), mode)
					f.fault = "drop-disable"
				} else {
					acquired := engineSnapshot(t, f)
					acquiredAction = engineRecord(acquired, "a")
					if acquiredAction == nil || acquiredAction.Status != ResourceOwned || acquiredAction.ManagedHash == "" || len(acquiredAction.Acquisition) == 0 {
						t.Fatal("initial acquired action missing")
					}
					original = cleanupPlan(t, p, acquired)
					f.fault = "drop-delete"
				}
				ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				pv, e := previewEngine(ctx, f.client, f.store, original, mode)
				if e != nil {
					cancel()
					t.Fatal(e)
				}
				_, e = cleanupEngine(ctx, f.client, f.store, original, pv, engineApproval(t, pv, "", nil))
				originalError = e
				cancel()
				if e == nil {
					t.Fatal("lost cleanup response accepted")
				}
			}
			s := engineSnapshot(t, f)
			pending := s.Document.Pending
			if pending == nil {
				t.Fatal("lost response cleared pending")
			}
			if mode == ModeTeardown {
				f.mu.Lock()
				fired := f.dropped
				co, associated := f.committed[pending.AttemptID]
				_, present := f.actions[acquiredAction.Coordinate.ID]
				f.mu.Unlock()
				if !fired || pending.Operation != OpDelete || pending.Method != http.MethodDelete || pending.Phase != StepIntent || pending.ResourceKey != "a" || pending.Coordinate != acquiredAction.Coordinate || !associated || co != acquiredAction.Coordinate || present {
					t.Fatalf("lost action DELETE precondition not reached: error=%v fired=%t pending=%+v acquired=%+v associated=%t backendCoordinate=%+v present=%t", originalError, fired, *pending, acquiredAction.Coordinate, associated, co, present)
				}
				t.Logf("lost DELETE established: error=%v operation=%s key=%s coordinate=%+v phase=%s attempt=%s head=%+v status=%s hash=%s acquisition=%v backendAbsent=%t", originalError, pending.Operation, pending.ResourceKey, pending.Coordinate, pending.Phase, pending.AttemptID, *s.Base.Head, engineRecord(s, "a").Status, engineRecord(s, "a").ManagedHash, engineRecord(s, "a").Acquisition, !present)
			}
			cons := engineConstruction(s, pending.ConstructionID)
			if cons == nil || cons.Mode != mode || cons.Input != original.plan.Digest() {
				t.Fatal("original plan/mode/id lost")
			}
			id := cons.ID
			originalInput := cons.Input
			f.fault = ""
			before := len(f.writes)
			// Ordinary entry points cannot recast or replay an incomplete construction.
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			pv, _ := NewPreview(original.plan, mode, s.Base, proofDigest('9'))
			if mode == ModeApply {
				_, e := applyEngine(ctx, f.client, f.store, original, pv, engineApproval(t, pv, "", nil))
				if e == nil {
					t.Fatal("normal apply replayed pending")
				}
			} else {
				_, e := cleanupEngine(ctx, f.client, f.store, original, pv, engineApproval(t, pv, "", nil))
				if e == nil {
					t.Fatal("new cleanup recast pending")
				}
			}
			cancel()
			if len(f.writes) != before {
				t.Fatal("lost mutation replayed")
			}
			pv, rec := engineRecoveryInput(t, f, original)
			for _, bad := range []string{"attempt", "construction", "hash", "attribution", "parent-version"} {
				raw, _ := json.Marshal(rec)
				var changed map[string]any
				json.Unmarshal(raw, &changed)
				construction := id
				switch bad {
				case "attempt":
					changed["attempt_id"] = "different-attempt"
				case "construction":
					construction = "different-construction"
				case "hash":
					changed["expected_managed_hash"] = proofDigest('0')
				case "attribution":
					changed["attribution"] = []any{}
				case "parent-version":
					coordinate := changed["coordinate"].(map[string]any)
					coordinate["parent_id"] = "different-parent"
				}
				a, e := DecodeApproval(engineApprovalRaw(t, pv, construction, changed))
				if e == nil {
					ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
					_, e = recoverEngine(ctx, f.client, f.store, original, pv, a)
					cancel()
				}
				if e == nil || engineSnapshot(t, f).Document.Pending == nil || len(f.writes) != before {
					t.Fatalf("wrong recovery %s changed attempt/authority", bad)
				}
			}
			ctx, cancel = context.WithTimeout(context.Background(), 4*time.Second)
			_, e := recoverEngine(ctx, f.client, f.store, original, pv, engineApproval(t, pv, id, rec))
			cancel()
			after := engineSnapshot(t, f)
			if e == nil || after.Document.Pending != nil || len(f.writes) != before {
				t.Fatalf("exact original observation not settled without replay: %v", e)
			}
			settled := engineConstruction(after, id)
			if settled.Mode != mode || settled.Input != originalInput || settled.ID != id {
				t.Fatal("recovery rewrote original construction")
			}
			switch mode {
			case ModeApply:
				if engineRecord(after, "c").Status != ResourceOwned {
					t.Fatal("attributed generated ID not acquired")
				}
			case ModeDeactivate:
				if engineRecord(after, "r").Status != ResourceDisabled {
					t.Fatal("disable not observed")
				}
			case ModeTeardown:
				r := engineRecord(after, "a")
				if r.Status != ResourceRemoved || r.ManagedHash == "" || len(r.Acquisition) == 0 {
					t.Fatalf("delete tombstone/last-present acquisition lost: operation=%s key=%s status=%s hash=%s acquisition=%v", pending.Operation, pending.ResourceKey, r.Status, r.ManagedHash, r.Acquisition)
				}
				prior := engineRecord(s, "a")
				if r.ManagedHash != prior.ManagedHash || !reflect.DeepEqual(r.Acquisition, acquiredAction.Acquisition) || r.Coordinate != acquiredAction.Coordinate {
					t.Fatal("delete recovery changed last-present hash/original acquisition/coordinate")
				}
			}
			// A NEW exact recovery preview/approval can continue only never-dispatched
			// suffix steps of that SAME original construction, never the settled step.
			ctx, cancel = context.WithTimeout(context.Background(), 4*time.Second)
			pv2, e := previewEngine(ctx, f.client, f.store, original, ModeRecover)
			if e != nil {
				cancel()
				t.Fatal(e)
			}
			receipt, e := recoverEngine(ctx, f.client, f.store, original, pv2, engineApproval(t, pv2, id, nil))
			cancel()
			want := ReceiptApplyObserved
			if mode == ModeDeactivate {
				want = ReceiptDeactivated
			}
			if mode == ModeTeardown {
				want = ReceiptIncomplete
			}
			if receipt.Mode != mode || receipt.ConstructionID != id || receipt.Input != originalInput || receipt.Result != want {
				t.Fatalf("suffix recast original receipt: %v %+v", e, receipt)
			}
			if (mode == ModeTeardown && e == nil) || (mode != ModeTeardown && e != nil) {
				t.Fatalf("dishonest completion status for original mode: %v", e)
			}
			final := engineSnapshot(t, f)
			if final.Document.Pending != nil {
				t.Fatal("suffix left pending")
			}
			for attempt, n := range f.attempts {
				if n != 1 {
					t.Fatalf("original attempted step resent: %s=%d", attempt, n)
				}
			}
		})
	}
}
func cleanupPlan(t *testing.T, p enginePlan, s Snapshot) enginePlan {
	t.Helper()
	copy, e := NewPlan(p.plan.input)
	if e != nil {
		t.Fatal(e)
	}
	in := copy.input
	in.Mode = ModeTeardown
	in.Steps = nil
	for i := range in.Resources {
		rec := engineRecord(s, in.Resources[i].Key)
		in.Resources[i].Coordinate = rec.Coordinate
		in.Resources[i].VersionFrom = ""
	}
	add := func(key string, op Operation) {
		in.Steps = append(in.Steps, PlannedStep{Number: uint32(len(in.Steps) + 1), ResourceKey: key, Operation: op})
	}
	for _, r := range in.Resources {
		if isConsumer(r.Coordinate.Kind) {
			add(r.Key, OpDisable)
		}
	}
	for _, kind := range []ResourceKind{KindAction, KindHook, KindRule, KindCollection, KindEvaluator} {
		for _, r := range in.Resources {
			if r.Coordinate.Kind == kind {
				add(r.Key, OpDelete)
			}
		}
	}
	cp, e := prepareEngine(in)
	if e != nil {
		t.Fatal(e)
	}
	return cp
}

// This explicitly reviewed plan disables the ENTIRE five-resource baseline's
// historical consumer closure, then optionally deletes only its owned action.
// It is not a runtime rewrite of the separately retained full-teardown plan.
func engineConsumerCleanupPlan(t *testing.T, p enginePlan, s Snapshot, mode Mode) enginePlan {
	t.Helper()
	cp := cleanupPlan(t, p, s)
	in := cp.plan.input
	in.Mode = mode
	steps := []PlannedStep{}
	for _, step := range in.Steps {
		r := planResource(in, step.ResourceKey)
		if step.Operation == OpDisable || (mode == ModeTeardown && step.Operation == OpDelete && r.Coordinate.Kind == KindAction) {
			step.Number = uint32(len(steps) + 1)
			steps = append(steps, step)
		}
	}
	in.Steps = steps
	out, e := prepareEngine(in)
	if e != nil {
		t.Fatal(e)
	}
	expected, e := NewPlan(in)
	if e != nil || out.plan.Digest() != expected.Digest() {
		t.Fatal("cleanup input order/digest changed")
	}
	return out
}
func TestEngineCleanupContradictionNeverReadsAroundOrDeletesTLS(t *testing.T) {
	for _, fault := range []string{"disable-lies", "disable-refuse", "disable-wrong-config"} {
		t.Run(fault, func(t *testing.T) {
			f := newEngineTLS(t, false)
			p := f.plan()
			if _, e := f.apply(p); e != nil {
				t.Fatal(e)
			}
			cp := cleanupPlan(t, p, engineSnapshot(t, f))
			f.fault = fault
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			pv, e := previewEngine(ctx, f.client, f.store, cp, ModeTeardown)
			if e != nil {
				t.Fatal(e)
			}
			start := len(f.calls)
			before := len(f.writes)
			_, e = cleanupEngine(ctx, f.client, f.store, cp, pv, engineApproval(t, pv, "", nil))
			if e == nil {
				t.Fatal("contradictory/refused disable accepted")
			}
			f.mu.Lock()
			trace := append([]string{}, f.calls[start:]...)
			mutations := append([]string{}, f.writes[before:]...)
			f.mu.Unlock()
			if !reflect.DeepEqual(mutations, []string{"PATCH /rules/rule-fresh"}) {
				t.Fatalf("lying/refused/wrong-config handler not actually reached once: %v", mutations)
			}
			for _, call := range trace {
				if strings.HasPrefix(call, "DELETE ") {
					t.Fatalf("dependent DELETE after unverified consumer: %v", trace)
				}
			}
			if trace[len(trace)-1] != "PATCH /rules/rule-fresh" {
				t.Fatalf("read-around/retry after known typed response contradiction: %v", trace)
			}
			s := engineSnapshot(t, f)
			pending := s.Document.Pending
			if pending == nil || pending.Coordinate.Kind != KindRule || pending.Operation != OpDisable || len(s.Document.Receipts) != 1 || s.Document.Receipts[0].InvalidatedBy == "" {
				t.Fatal("pending/receipt invalidation lost")
			}
			if fault == "disable-wrong-config" && (pending.Phase != StepResponse || pending.Returned == nil || pending.Returned.ManagedHash == nil) {
				t.Fatal("attributable contradictory response evidence discarded")
			}
		})
	}
}
func TestEngineCleanupConsumerBarrierTLS(t *testing.T) {
	for _, scenario := range []string{"deactivate", "teardown-action", "lying-disable"} {
		t.Run(scenario, func(t *testing.T) {
			f := newEngineTLS(t, false)
			p := f.plan()
			if _, e := f.apply(p); e != nil {
				t.Fatal(e)
			}
			s := engineSnapshot(t, f)
			if len(s.Document.Resources) != 5 {
				t.Fatal("not the five-resource historical baseline")
			}
			mode := ModeTeardown
			if scenario == "deactivate" {
				mode = ModeDeactivate
			}
			cp := engineConsumerCleanupPlan(t, p, s, mode)
			// Validate a bad approved ordering rather than silently repairing it.
			if mode == ModeTeardown {
				badCopy, e := NewPlan(cp.plan.input)
				if e != nil {
					t.Fatal(e)
				}
				bad := badCopy.input
				last := len(bad.Steps) - 1
				bad.Steps[0], bad.Steps[last] = bad.Steps[last], bad.Steps[0]
				for i := range bad.Steps {
					bad.Steps[i].Number = uint32(i + 1)
				}
				before, _ := json.Marshal(bad.Steps)
				if _, e = prepareEngine(bad); e == nil {
					t.Fatal("delete-before-disable ordering admitted")
				}
				after, _ := json.Marshal(bad.Steps)
				if string(before) != string(after) {
					t.Fatal("invalid input was reordered")
				}
			}
			if scenario == "lying-disable" {
				f.fault = "disable-lies"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			pv, e := previewEngine(ctx, f.client, f.store, cp, mode)
			if e != nil {
				t.Fatal(e)
			}
			f.mu.Lock()
			requestStart := len(f.calls)
			writeStart := len(f.writes)
			f.mu.Unlock()
			receipt, e := cleanupEngine(ctx, f.client, f.store, cp, pv, engineApproval(t, pv, "", nil))
			f.mu.Lock()
			trace := append([]string{}, f.calls[requestStart:]...)
			mutations := append([]string{}, f.writes[writeStart:]...)
			f.mu.Unlock()
			t.Logf("scenario=%s error=%v actual_mutations=%v", scenario, e, mutations)
			if scenario == "lying-disable" {
				if e == nil {
					t.Fatal("lying disable accepted")
				}
				want := []string{"PATCH /rules/rule-fresh"}
				if !reflect.DeepEqual(mutations, want) {
					t.Fatalf("lying disable not actually reached exactly once: %v", mutations)
				}
				for _, call := range trace {
					if strings.HasPrefix(call, "DELETE ") {
						t.Fatalf("DELETE after lying disable: %v", trace)
					}
				}
				if trace[len(trace)-1] != "PATCH /rules/rule-fresh" {
					t.Fatalf("read-around/retry/descendant after contradictory response: %v", trace)
				}
				after := engineSnapshot(t, f)
				pending := after.Document.Pending
				if pending == nil || pending.Operation != OpDisable || pending.Coordinate.Kind != KindRule || pending.Method != "PATCH" || pending.Phase != StepIntent {
					t.Fatal("contradictory disable did not retain original pending intent")
				}
				if len(after.Document.Receipts) != 1 || after.Document.Receipts[0].InvalidatedBy == "" {
					t.Fatal("cleanup failed to invalidate original receipt")
				}
				return
			}
			want := []string{"PATCH /rules/rule-fresh", "PUT /hook-rules/hook-fresh", "PATCH /rules/rule-fresh/actions/ra_11111111111111111111111111111111"}
			if mode == ModeTeardown {
				want = append(want, "DELETE /rules/rule-fresh/actions/ra_11111111111111111111111111111111")
			}
			if !reflect.DeepEqual(mutations, want) {
				t.Fatalf("whole historical disable/order not observed: %v", mutations)
			}
			if mode == ModeDeactivate {
				if e != nil || receipt.Result != ReceiptDeactivated {
					t.Fatalf("deactivation failed: %v %+v", e, receipt)
				}
			} else if e == nil || receipt.Result != ReceiptIncomplete {
				t.Fatalf("retained teardown not honest: %v %+v", e, receipt)
			}
			if engineSnapshot(t, f).Document.Pending != nil {
				t.Fatal("verified cleanup left pending")
			}
		})
	}
}

// Read-only barrier proof against the ACTUAL five-resource historical custody.
// This does not substitute for the separate still-blocked lying-PATCH scenario.
func TestEngineHistoricalConsumerBarrierReadsEveryKindTLS(t *testing.T) {
	for _, enabled := range []string{"rule", "hook", "action", "none"} {
		t.Run(enabled, func(t *testing.T) {
			f := newEngineTLS(t, false)
			p := f.plan()
			if _, e := f.apply(p); e != nil {
				t.Fatal(e)
			}
			s := engineSnapshot(t, f)
			if len(s.Document.Resources) != 5 {
				t.Fatal("historical baseline was changed")
			}
			f.mu.Lock()
			for id, v := range f.rules {
				v.Enabled = enabled == "rule"
				f.rules[id] = v
			}
			for id, v := range f.hooks {
				v.Enabled = enabled == "hook"
				f.hooks[id] = v
			}
			for id, v := range f.actions {
				v.Enabled = enabled == "action"
				f.actions[id] = v
			}
			writes := len(f.writes)
			f.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			inv, e := engineInventoryRead(ctx, f.client, p, s)
			if e != nil {
				t.Fatal(e)
			}
			f.mu.Lock()
			start := len(f.calls)
			f.mu.Unlock()
			e = engineConsumerBarrier(ctx, f.client, p, s, inv)
			if enabled == "none" {
				if e != nil {
					t.Fatal(e)
				}
			} else if !errors.Is(e, ErrConflict) {
				t.Fatalf("enabled %s consumer failed to close barrier: %v", enabled, e)
			}
			expected := []string{"GET /rules/rule-fresh"}
			if enabled != "rule" {
				expected = append(expected, "GET /hook-rules/hook-fresh")
			}
			if enabled == "action" || enabled == "none" {
				expected = append(expected, "GET /rules/rule-fresh/actions/ra_11111111111111111111111111111111")
			}
			f.mu.Lock()
			actual := append([]string{}, f.calls[start:]...)
			afterWrites := len(f.writes)
			f.mu.Unlock()
			if !reflect.DeepEqual(actual, expected) || afterWrites != writes {
				t.Fatalf("whole historical rule/hook/action observation missing or mutated: %v", actual)
			}
		})
	}
}
func TestEngineCleanupWholeClosureAndRetentionTLS(t *testing.T) {
	for _, fault := range []string{"normal", "disable-lies", "suite-retained"} {
		t.Run(fault, func(t *testing.T) {
			f := newEngineTLS(t, fault == "suite-retained")
			if fault == "suite-retained" {
				engineSuiteOnly(f)
			}
			p := f.plan()
			if _, e := f.apply(p); e != nil {
				t.Fatal(e)
			}
			f.queuedWork = true
			f.fault = fault
			s := engineSnapshot(t, f)
			cp := cleanupPlan(t, p, s)
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			pv, e := previewEngine(ctx, f.client, f.store, cp, ModeTeardown)
			if e != nil {
				t.Fatal(e)
			}
			start := len(f.writes)
			requestStart := len(f.calls)
			receipt, e := cleanupEngine(ctx, f.client, f.store, cp, pv, engineApproval(t, pv, "", nil))
			if e == nil {
				t.Fatal("full teardown incorrectly successful")
			}
			deletes := 0
			for _, w := range f.writes[start:] {
				if strings.HasPrefix(w, "DELETE") {
					deletes++
				}
				if strings.Contains(w, "test-suites") || strings.HasPrefix(w, "DELETE /evaluators") || strings.HasPrefix(w, "DELETE /collections") {
					t.Fatal("deleted permanent/undrained dependency")
				}
			}
			if fault == "disable-lies" {
				pending := engineSnapshot(t, f).Document.Pending
				trace := append([]string{}, f.calls[requestStart:]...)
				if errors.Is(e, ErrCapacity) {
					t.Fatalf("lying disable NOT reached: admission error=%v cleanup_mutations=%v DELETE=%d pending=%t; retained blocked acceptance, not a crossed-delete observation", e, f.writes[start:], deletes, pending != nil)
				}
				if !reflect.DeepEqual(f.writes[start:], []string{"PATCH /rules/rule-fresh"}) {
					t.Fatalf("lying disable not observed exactly once: %v", f.writes[start:])
				}
				for _, call := range trace {
					if strings.HasPrefix(call, "DELETE ") {
						t.Fatalf("DELETE crossed lying-disable barrier: %v", trace)
					}
				}
				if deletes != 0 || pending == nil || pending.Operation != OpDisable || trace[len(trace)-1] != "PATCH /rules/rule-fresh" {
					t.Fatal("contradiction lost pending or performed read-around/retry/descendant")
				}
				return
			}
			wantDeletes := 3
			if fault == "suite-retained" {
				wantDeletes = 0
			}
			if receipt.Result != ReceiptIncomplete || deletes != wantDeletes || !f.queuedWork {
				t.Fatalf("not honest retained receipt: %v %+v", e, receipt)
			}
			f.queuedWork = false // outstanding work may finish AFTER disable/delete
			final := engineSnapshot(t, f)
			if final.Document.Pending != nil || len(final.Document.Receipts) != 2 || final.Document.Receipts[0].InvalidatedBy == "" {
				t.Fatal("cleanup receipt/history wrong")
			}
		})
	}
}
func TestEngineStaleAndWrongScopeRefuseTLS(t *testing.T) {
	f := newEngineTLS(t, false)
	p := f.plan()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	pv, e := previewEngine(ctx, f.client, f.store, p, ModeApply)
	if e != nil {
		t.Fatal(e)
	}
	approval := engineApproval(t, pv, "", nil)
	f.collections["unrelated"] = Collection{TenantID: "tenant", CollectionID: "unrelated", Name: "Other", CreatedBy: "fixture", UpdatedBy: "fixture"}
	if _, e = applyEngine(ctx, f.client, f.store, p, pv, approval); e == nil || len(f.writes) != 0 {
		t.Fatal("stale inventory approval accepted")
	}
	delete(f.collections, "unrelated")
	if _, e = f.apply(p); e != nil {
		t.Fatal(e)
	}
	for _, fault := range []string{"wrong-parent"} {
		f.fault = fault
		before := len(f.writes)
		if _, e = previewEngine(ctx, f.client, f.store, p, ModeApply); e == nil || len(f.writes) != before {
			t.Fatal("wrong scoped observation accepted")
		}
	}
	f.fault = ""
	wrong := p
	bad := p.plan.input
	bad.Binding.Destination.RuntimeTenant = "other"
	wrong, e = prepareEngine(bad)
	if e != nil {
		t.Fatal(e)
	}
	before := len(f.writes)
	if _, e = previewEngine(ctx, f.client, f.store, wrong, ModeApply); e == nil || len(f.writes) != before {
		t.Fatal("foreign binding admitted")
	}
}
func TestEngineSuiteContentAndCompletenessNotJustCountTLS(t *testing.T) {
	for _, fault := range []string{"post-case-drift", "case-list-partial"} {
		t.Run(fault, func(t *testing.T) {
			f := newEngineTLS(t, true)
			engineSuiteOnly(f)
			f.fault = fault
			p := f.plan()
			_, e := f.applyWithin(p, 20*time.Second)
			if e == nil {
				t.Fatal("same-ID wrong content/partial inventory accepted")
			}
			s := engineSnapshot(t, f)
			if s.Document.Pending == nil || s.Document.Pending.ResourceKey != "k" || len(s.Document.Receipts) != 0 {
				t.Fatal("case ambiguity lost pending/created receipt")
			}
			for _, call := range f.writes {
				if strings.HasPrefix(call, "PUT ") || strings.Contains(call, ":publish") {
					t.Fatal("binding/publication descendant before verified whole case content")
				}
			}
		})
	}
	t.Run("published-noop-content-drift", func(t *testing.T) {
		f := newEngineTLS(t, true)
		engineSuiteOnly(f)
		p := f.plan()
		if _, e := f.applyWithin(p, 20*time.Second); e != nil {
			t.Fatal(e)
		}
		old := f.cases["case-fresh"]
		input := map[string]any{}
		for k, v := range old.Input {
			input[k] = v
		}
		input["text"] = "changed reviewed content"
		f.cases["case-fresh"] = fixtureCase(CaseSpec{TestCaseID: "case-fresh", Input: input})
		before := len(f.writes)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, e := previewEngine(ctx, f.client, f.store, p, ModeApply); e == nil || len(f.writes) != before {
			t.Fatal("matching case count/IDs/publication flag hid content drift")
		}
	})
}
func TestEngineWrongCaseVersionRefusesBeforeWrites(t *testing.T) {
	f := newEngineTLS(t, true)
	engineSuiteOnly(f)
	p := f.plan()
	if _, e := f.apply(p); e != nil {
		t.Fatal(e)
	}
	f.fault = "wrong-version"
	before := len(f.writes)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if _, e := previewEngine(ctx, f.client, f.store, p, ModeApply); e == nil || len(f.writes) != before {
		t.Fatal("wrong case version admitted")
	}
}

// This isolates foundation admission from all C dispatch/projection logic. Six
// independently journaled identities are the frozen minimum for a nonempty
// published checkpoint: evaluator, suite, draft, case, bindings, publication.
func TestEngineMinimalNonemptySuiteFitsFoundationBudget(t *testing.T) {
	f := newEngineTLS(t, true)
	engineSuiteOnly(f)
	f.input.Declarations = f.input.Declarations[:1]
	f.input.Targets = f.input.Targets[:1]
	raw := map[string]string{
		"e": `{"evaluator_id":"eval-fresh","version":"1","kind":"regex","config":{"pattern":"fiction"},"output_keys":[{"key":"p","type":"bool"}]}`,
		"s": `{"suite_id":"suite-fresh","name":"s"}`,
		"d": `{}`,
		"k": `{"test_case_id":"case-fresh","input":{"text":"x"}}`,
		"b": `{"bindings":[{"binding_id":"b","evaluator_id":"e","evaluator_version":"1","input_mappings":{"response":"test_case.input.text"}}]}`,
		"p": `{"cases":[{"test_case_id":"case-fresh","input":{"text":"x"}}],"bindings":[{"binding_id":"b","evaluator_id":"e","evaluator_version":"1","input_mappings":{"response":"test_case.input.text"}}]}`,
	}
	bytes := 0
	for i, r := range f.input.Resources {
		s, e := NewAuthoredSpec([]byte(raw[r.Key]))
		if e != nil {
			t.Fatal(e)
		}
		f.input.Resources[i].Spec = s
		bytes += len(s.JSON())
	}
	p := f.plan()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	pv, e := previewEngine(ctx, f.client, f.store, p, ModeApply)
	if e != nil {
		t.Fatal(e)
	}
	approval := engineApproval(t, pv, "", nil)
	auth, e := f.store.Authorize(ctx, approval, pv, pv.inventory)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.store.Bootstrap(ctx, auth)
	if e != nil {
		after := engineSnapshot(t, f)
		if !after.Base.Pristine || len(f.writes) != 0 {
			t.Fatal("capacity failure lost pristine custody")
		}
		t.Fatalf("frozen foundation admission blocks minimal nonempty checkpoint before C dispatch: resources=%d steps=%d claims=%d authored_bytes=%d error=%v", len(f.input.Resources), len(f.input.Steps), len(f.input.Targets), bytes, e)
	}
}
func sha256ForFixture(raw []byte) [32]byte { return sha256.Sum256(raw) }
func slicesContainsForFixture(in []string, v string) bool {
	for _, x := range in {
		if x == v {
			return true
		}
	}
	return false
}
