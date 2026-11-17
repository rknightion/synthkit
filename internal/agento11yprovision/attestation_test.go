// SPDX-License-Identifier: AGPL-3.0-only

package agento11yprovision

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
)

func proofDigest(c byte) Digest { return Digest(strings.Repeat(string(c), 64)) }
func proofBinding() Binding {
	return Binding{"deployment", "state", "contract-1", "source-1", Destination{"https://example.invalid:443", "/sub", "org", managementPrefix, "tenant", "https://runtime.invalid/ingest", "tenant"}}
}
func proofInput(t *testing.T) PlanInput {
	t.Helper()
	spec, err := NewAuthoredSpec([]byte(`{"pattern":"fictional","number":1e2,"array":[],"object":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	a := Origin{"blueprint", "planner", 0}
	b := Origin{"blueprint", "source", 0}
	in := PlanInput{Binding: proofBinding(), Mode: ModeApply,
		Declarations: []DeclarationInput{
			{a, core.AgentDeclaration{Name: "Planner", Service: core.AgentServiceIdentity{Name: "planner"}, ProducerMode: core.AgentProducerReal}},
			{b, core.AgentDeclaration{Name: "Source", Service: core.AgentServiceIdentity{Name: "source"}, ProducerMode: core.AgentProducerReal}},
			{Origin{"other", "synthetic", 0}, core.AgentDeclaration{Name: "Context", Service: core.AgentServiceIdentity{Name: "context"}, ProducerMode: core.AgentProducerSynthetic}},
			{Origin{"other", "real", 0}, core.AgentDeclaration{Name: "Other", Service: core.AgentServiceIdentity{Name: "other"}, ProducerMode: core.AgentProducerReal}},
		}, Targets: []Origin{a, b}}
	in.Resources = []PlannedResource{
		{Key: "e", Origin: a, Coordinate: Coordinate{Kind: KindEvaluator, ID: "e-1", Version: "v1"}, Spec: spec, Namespaces: []string{"planner"}},
		{Key: "c", Origin: a, Coordinate: Coordinate{Kind: KindCollection, Unassigned: true}, Spec: spec, Namespaces: []string{"planner"}},
		{Key: "r", Origin: a, Coordinate: Coordinate{Kind: KindRule, ID: "r-1"}, Spec: spec, Dependencies: []string{"e"}, Namespaces: []string{"planner"}},
		{Key: "a", Origin: a, Coordinate: Coordinate{Kind: KindAction, ParentID: "r-1", Unassigned: true}, Spec: spec, Dependencies: []string{"r", "c"}, Namespaces: []string{"planner"}},
	}
	for i, r := range in.Resources {
		in.Steps = append(in.Steps, PlannedStep{uint32(i + 1), r.Key, OpCreate})
	}
	return in
}

// This independent operator document builder deliberately does not use the
// production wire mirrors: it pins the private approval boundary's actual keys.
func proofApprovalJSON(t *testing.T, p Preview, id, construction string, issued, expires time.Time, recovery any) []byte {
	t.Helper()
	b := proofBinding()
	d := b.Destination
	var pv map[string]json.RawMessage
	if err := json.Unmarshal(p.JSON(), &pv); err != nil {
		t.Fatal(err)
	}
	// Preserve the public preview's integer head, including near-overflow CAS
	// controls; a map[string]any roundtrip would silently coerce it to float64.
	base := pv["base"]
	var mode string
	var names []string
	var inventory string
	json.Unmarshal(pv["mode"], &mode)
	json.Unmarshal(pv["namespaces"], &names)
	json.Unmarshal(pv["inventory"], &inventory)
	ev := []any{map[string]any{"id": "reviewed-evidence", "digest": proofDigest('e')}}
	assertions := map[string]any{"authorized_operator": true, "effective_destination": true, "all_affected_emitters_stopped": true, "keep_stopped_until_receipt_accepted": true, "exclusive_namespace": true, "outstanding_work_may_finish": true, "non_atomic_initial_publication": true, "permanent_records_may_remain": true, "no_synthetic_reentry": true, "destination_evidence": ev, "runtime_evidence": ev, "stop_evidence": ev, "namespace_evidence": ev, "foreign": []any{}}
	obj := map[string]any{"schema": 1, "approval_id": id, "operator": "operator", "construction_id": construction, "issued_ms": issued.UnixMilli(), "expires_ms": expires.UnixMilli(), "binding": map[string]any{"deployment_id": b.DeploymentID, "state_id": b.StateID, "contract_revision": b.ContractRevision, "source_revision": b.SourceRevision, "destination": map[string]any{"grafana_origin": d.GrafanaOrigin, "deployment_path": d.DeploymentPath, "grafana_org": d.GrafanaOrg, "plugin_route": d.PluginRoute, "effective_tenant": d.EffectiveTenant, "runtime_endpoint": d.RuntimeEndpoint, "runtime_tenant": d.RuntimeTenant}}, "input": p.plan.Digest(), "preview": p.Digest(), "inventory": inventory, "base": base, "mode": mode, "namespaces": names, "assertions": assertions, "recovery": recovery}
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestFoundationPlanTargetsAndAuthoredObjects(t *testing.T) {
	in := proofInput(t)
	p, err := NewPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Declarations[0].Declaration.Name = "changed"
	in.Resources[0].Namespaces[0] = "context"
	pv, err := NewPreview(p, ModeApply, Base{Pristine: true}, proofDigest('1'))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Namespaces []string `json:"namespaces"`
	}
	if err = json.Unmarshal(pv.JSON(), &v); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(v.Namespaces) != "[planner source]" {
		t.Fatalf("target leak/omission: %v", v.Namespaces)
	}
	cases := map[string]func(*PlanInput){
		"synthetic target":           func(i *PlanInput) { i.Targets = append(i.Targets, i.Declarations[2].Origin) },
		"context namespace":          func(i *PlanInput) { i.Resources[0].Namespaces = []string{"context"} },
		"origin substitution":        func(i *PlanInput) { i.Resources[0].Origin = i.Declarations[3].Origin },
		"target origin substitution": func(i *PlanInput) { i.Resources[0].Origin = i.Targets[1] },
		"case collision":             func(i *PlanInput) { i.Declarations[2].Declaration.Name = "planner" },
		"child collision":            func(i *PlanInput) { i.Declarations[0].Declaration.ProjectedChildNames = []string{"Source"} },
		"nonliteral":                 func(i *PlanInput) { i.Declarations[0].Declaration.Name = "Plan*" },
		"unsupported disabled phase": func(i *PlanInput) { i.Declarations[0].Declaration.HookPolicy.Phases = []string{"postflight"} },
		"apply disable":              func(i *PlanInput) { i.Steps[0].Operation = OpDisable },
		"foreign action parent":      func(i *PlanInput) { i.Resources[3].Coordinate.ParentID = "foreign" },
		"fabricated generated ID": func(i *PlanInput) {
			i.Resources[1].Coordinate.Unassigned = false
			i.Resources[1].Coordinate.ID = "invented"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			i := proofInput(t)
			mutate(&i)
			if _, e := NewPlan(i); e == nil {
				t.Fatal("invalid plan admitted")
			}
		})
	}
	for _, raw := range []string{`[]`, `{"x":1,"x":2}`, `{"x":{"a":1,"a":2}}`, `{} {}`, string([]byte{'{', '"', 'x', '"', ':', '"', 255, '"', '}'})} {
		if _, e := NewAuthoredSpec([]byte(raw)); e == nil {
			t.Fatalf("bad object accepted: %q", raw)
		}
	}
	raw := []byte(`{"n":1e2,"a":[],"o":{}}`)
	s, e := NewAuthoredSpec(raw)
	if e != nil {
		t.Fatal(e)
	}
	raw[2] = 'z'
	out := s.JSON()
	out[2] = 'z'
	if string(s.JSON()) != `{"a":[],"n":1e2,"o":{}}` {
		t.Fatal("object/number/alias lost")
	}
	if _, e = NewAuthoredSpec([]byte(`{"x":"` + strings.Repeat("a", custodySpecBytes) + `"}`)); !errors.Is(e, ErrCapacity) {
		t.Fatalf("spec capacity: %v", e)
	}
}

func TestFoundationStructuredApproval(t *testing.T) {
	p, e := NewPlan(proofInput(t))
	if e != nil {
		t.Fatal(e)
	}
	pv, e := NewPreview(p, ModeApply, Base{Pristine: true}, proofDigest('1'))
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	raw := proofApprovalJSON(t, pv, "approval", "", now.Add(-time.Second), now.Add(time.Minute), nil)
	if _, e = DecodeApproval(raw); e != nil {
		t.Fatal(e)
	}
	for _, replace := range [][2]string{{`"schema":1`, `"schema":9`}, {`"authorized_operator":true`, `"authorized_operator":false`}, {`"namespaces":["planner","source"]`, `"namespaces":["context"]`}} {
		altered := []byte(strings.Replace(string(raw), replace[0], replace[1], 1))
		a, e := DecodeApproval(altered)
		if e == nil && replace[0] != `"namespaces":["planner","source"]` {
			t.Fatal("invalid structured assertion accepted")
		}
		if e == nil && a.document.Namespaces[0] != "context" {
			t.Fatal("test replacement failed")
		}
	}
	if _, e = DecodeApproval(append(raw, []byte(` {}`)...)); e == nil {
		t.Fatal("trailing approval accepted")
	}
}

// Frozen pre-optimization oracle: retain the two final canonical tree marshals.
// On scanner-produced trees these marshals cannot fail. Outside that domain this
// is not an oracle for the comparator's fail-closed unsupported-type behavior.
func proofOldTreeEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func proofOldCustodyDecode(raw []byte, out any, max int) error {
	original, e := custodyJSON(raw, max, 64, true)
	if e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return ErrCorrupt
	}
	encoded, e := json.Marshal(out)
	if e != nil {
		return ErrCorrupt
	}
	round, e := custodyJSON(encoded, max, 64, true)
	if e != nil {
		return e
	}
	if !proofOldTreeEqual(original, round) {
		return ErrCorrupt
	}
	return nil
}

// Frozen b2ec4b3 custodyDecode: the current two-scan/tree-comparison baseline,
// independent of the older oracle's additional canonical tree marshals.
func proofTwoScanCustodyDecode(raw []byte, out any, max int) error {
	original, e := custodyJSON(raw, max, 64, true)
	if e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return ErrCorrupt
	}
	encoded, e := json.Marshal(out)
	if e != nil {
		return ErrCorrupt
	}
	round, e := custodyJSON(encoded, max, 64, true)
	if e != nil {
		return e
	}
	// Required zero-valued fields cannot silently disappear; integer spellings and
	// nullable pointers are also checked by this complete typed wire comparison.
	if !custodyTreeEqual(original, round) {
		return ErrCorrupt
	}
	return nil
}

func TestCustodyTreeComparisonOracle(t *testing.T) {
	// All pairings exercise symmetry, exact types, lexical numbers, order and
	// absent-vs-null keys. Equivalent escapes/order must remain equal too.
	raw := []string{`null`, `true`, `false`, `"true"`, `""`, `"a"`, `"\u0061"`,
		`"<>&"`, `"\u003c\u003e\u0026"`, `"\ud800\udc00"`, `"𐀀"`,
		`0`, `-0`, `1`, `1.0`, `1e0`, `1E+0`, `9007199254740992`, `9007199254740993`,
		`[]`, `{}`, `[null]`, `[1,2]`, `[2,1]`, `{"x":null}`, `{"y":null}`,
		`{"x":0}`, `{"x":false}`, `{"x":[]}`, `{"x":{}}`,
		`{"a":[1,{"b":true}],"z":null}`, `{"z":null,"\u0061":[1,{"b":true}]}`}
	for _, left := range raw {
		a, err := custodyJSON([]byte(left), 4096, 64, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, right := range raw {
			b, err := custodyJSON([]byte(right), 4096, 64, false)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := custodyTreeEqual(a, b), proofOldTreeEqual(a, b); got != want {
				t.Errorf("tree equality %s / %s: got %v want %v", left, right, got, want)
			}
		}
	}
	type alias string
	for _, unsupported := range []any{1, float64(1), alias("a"), json.RawMessage(`1`), struct{}{}, make(chan int)} {
		if custodyTreeEqual(unsupported, unsupported) || custodyTreeEqual([]any{unsupported}, []any{unsupported}) ||
			custodyTreeEqual(map[string]any{"x": unsupported}, map[string]any{"x": unsupported}) {
			t.Errorf("unsupported type %T accepted", unsupported)
		}
	}
}

// Compare the real decoder with the frozen old algorithm using actual wire
// records, not a reflection-based stand-in. Post-decode validation is unchanged;
// identical typed bytes also pin its input and checksum preimages.
func TestCustodyDecodeComparisonOracle(t *testing.T) {
	s := proofStore(t, proofDirectory(t), false)
	snap, p := proofApply(t, s)
	document, err := custodyDocumentBytes(*snap.Document)
	if err != nil {
		t.Fatal(err)
	}
	pv, err := NewPreview(p, ModeApply, Base{Pristine: true}, proofDigest('1'))
	if err != nil {
		t.Fatal(err)
	}
	approval := proofApprovalJSON(t, pv, "comparison", "", time.Unix(100, 0), time.Unix(160, 0), nil)
	ew := enrollmentWire{Schema: 1, Binding: bindingToWire(proofBinding())}
	pre, _ := json.Marshal(ew)
	ew.Checksum = custodyHash("enrollment", pre)
	enrollment, _ := json.Marshal(ew)
	fixtures := []struct {
		name    string
		raw     []byte
		newWire func() any
	}{
		{"document", document, func() any { return new(documentWire) }},
		{"enrollment", enrollment, func() any { return new(enrollmentWire) }},
		{"approval", approval, func() any { return new(approvalWire) }},
	}
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			compare := func(t *testing.T, raw []byte, max int, want error) {
				t.Helper()
				old, twoScan, actual := f.newWire(), f.newWire(), f.newWire()
				oe := proofOldCustodyDecode(raw, old, max)
				te := proofTwoScanCustodyDecode(raw, twoScan, max)
				ne := custodyDecode(raw, actual, max)
				if oe != ne || te != ne || oe != want {
					t.Fatalf("decoder errors old=%v two-scan=%v actual=%v want=%v", oe, te, ne, want)
				}
				ob, _ := json.Marshal(old)
				tb, _ := json.Marshal(twoScan)
				nb, _ := json.Marshal(actual)
				if !bytes.Equal(ob, nb) || !bytes.Equal(tb, nb) {
					t.Fatal("typed output differs, including on rejected input")
				}
				if ne == nil {
					if f.name == "document" {
						d, e := custodyDocumentDecode(raw)
						if e != nil {
							t.Fatal(e)
						}
						round, e := custodyDocumentBytes(d)
						if e != nil || !bytes.Equal(round, ob) {
							t.Fatal("state checksum/wire changed", e)
						}
					}
					if f.name == "enrollment" {
						w := *actual.(*enrollmentWire)
						hash := w.Checksum
						w.Checksum = ""
						pre, e := json.Marshal(w)
						if e != nil || custodyHash("enrollment", pre) != hash {
							t.Fatal("enrollment checksum changed", e)
						}
					}
					if f.name == "approval" {
						a, e := DecodeApproval(raw)
						if e != nil || a.hash != custodyHash("approval", ob) {
							t.Fatal("approval digest changed", e)
						}
					}
				}
			}
			compare(t, f.raw, len(f.raw), nil)
			compare(t, f.raw, len(f.raw)-1, ErrCapacity)
			compare(t, f.raw, len(f.raw)+1, nil)
			// Noncanonical whitespace must retain the full comparison fallback.
			compare(t, append([]byte(" \n"), f.raw...), custodyStateV2Bytes, nil)
			for _, tc := range []struct {
				name, from, to string
				want           error
			}{
				{"duplicate", `"schema":1`, `"schema":1,"schema":1`, ErrInvalid},
				{"escaped-duplicate", `"schema":1`, `"schema":1,"\u0073chema":1`, ErrInvalid},
				{"unknown", `"schema":1`, `"schema":1,"unknown":0`, ErrCorrupt},
				{"alias", `"schema":1`, `"SCHEMA":1`, ErrCorrupt},
				{"missing", `"schema":1`, ``, ErrCorrupt},
				{"float-integer", `"schema":1`, `"schema":1.0`, ErrCorrupt},
				{"exponent-integer", `"schema":1`, `"schema":1e0`, ErrCorrupt},
				{"null-record", `"schema":1`, `"schema":null`, ErrInvalid},
				{"surrogate", `"schema":1`, `"schema":"\ud800"`, ErrInvalid},
				{"low-surrogate", `"schema":1`, `"schema":"\udc00"`, ErrInvalid},
				{"utf8", `"schema":1`, "\"schema\":\"\xff\"", ErrInvalid},
				{"escaped-key", `"schema":1`, `"\u0073chema":1`, nil},
			} {
				t.Run(tc.name, func(t *testing.T) {
					raw := bytes.Replace(f.raw, []byte(tc.from), []byte(tc.to), 1)
					if tc.name == "missing" {
						raw = bytes.ReplaceAll(raw, []byte(`{,`), []byte(`{`))
						raw = bytes.ReplaceAll(raw, []byte(`,}`), []byte(`}`))
						raw = bytes.ReplaceAll(raw, []byte(`,,`), []byte(`,`))
					}
					if bytes.Equal(raw, f.raw) {
						t.Fatal("control not applied")
					}
					compare(t, raw, custodyStateV2Bytes, tc.want)
				})
			}
			t.Run("trailing", func(t *testing.T) {
				compare(t, append(bytes.Clone(f.raw), []byte(` {}`)...), custodyStateV2Bytes, ErrInvalid)
			})
			// Reverse top-level key order without altering any raw field lexemes.
			var fields map[string]json.RawMessage
			if e := json.Unmarshal(f.raw, &fields); e != nil {
				t.Fatal(e)
			}
			canonical, _ := json.Marshal(fields)
			parts := []string{}
			dec := json.NewDecoder(bytes.NewReader(canonical))
			dec.Token()
			for dec.More() {
				key, _ := dec.Token()
				var value json.RawMessage
				if e := dec.Decode(&value); e != nil {
					t.Fatal(e)
				}
				k, _ := json.Marshal(key)
				parts = append([]string{string(k) + ":" + string(value)}, parts...)
			}
			compare(t, []byte("{"+strings.Join(parts, ",")+"}"), custodyStateV2Bytes, nil)
			// Binding/revision values pass the codec unchanged; later validation
			// still owns their meaning. Compare them without asserting admission.
			for _, replacement := range []string{`"contract-other"`, `""`, `"` + custodyStateV2Revision + `"`} {
				raw := bytes.Replace(f.raw, []byte(`"contract-1"`), []byte(replacement), -1)
				old, twoScan, actual := f.newWire(), f.newWire(), f.newWire()
				oe := proofOldCustodyDecode(raw, old, custodyStateV2Bytes)
				te := proofTwoScanCustodyDecode(raw, twoScan, custodyStateV2Bytes)
				ne := custodyDecode(raw, actual, custodyStateV2Bytes)
				if oe != ne || te != ne {
					t.Fatal("revision decode changed")
				}
				ob, _ := json.Marshal(old)
				tb, _ := json.Marshal(twoScan)
				nb, _ := json.Marshal(actual)
				if !bytes.Equal(ob, nb) || !bytes.Equal(tb, nb) {
					t.Fatal("binding/revision wire changed")
				}
			}
		})
	}
	// Open authored values retain number lexemes, ordered arrays and all three
	// null/empty-container distinctions; required zero fields may not disappear.
	type record struct {
		Zero int             `json:"zero"`
		Spec json.RawMessage `json:"spec"`
	}
	for _, tc := range []struct {
		raw  string
		want error
	}{
		{`{"spec":{}}`, ErrCorrupt},
		{`{"zero":-0,"spec":{}}`, ErrCorrupt},
		{`{"zero":0,"spec":null}`, nil},
		{`{"zero":0,"spec":[]}`, nil},
		{`{"zero":0,"spec":{}}`, nil},
		{`{"zero":0,"spec":{"n":[1,1.0,1e0,-0,9007199254740993],"a":[],"o":{},"v":null}}`, nil},
		{`{"zero":0,"spec":` + strings.Repeat("[", 63) + `0` + strings.Repeat("]", 63) + `}`, nil},
		{`{"zero":0,"spec":` + strings.Repeat("[", 64) + `0` + strings.Repeat("]", 64) + `}`, ErrCapacity},
	} {
		var old, twoScan, actual record
		oe := proofOldCustodyDecode([]byte(tc.raw), &old, 4096)
		te := proofTwoScanCustodyDecode([]byte(tc.raw), &twoScan, 4096)
		ne := custodyDecode([]byte(tc.raw), &actual, 4096)
		if oe != ne || te != ne || oe != tc.want || old.Zero != actual.Zero || twoScan.Zero != actual.Zero || !bytes.Equal(old.Spec, actual.Spec) || !bytes.Equal(twoScan.Spec, actual.Spec) {
			t.Fatalf("open/depth control differs: %s old=%v two-scan=%v actual=%v want=%v", tc.raw, oe, te, ne, tc.want)
		}
	}
	// The typed re-encoding, not just input bytes, remains independently bounded.
	raw := []byte(`{"spec":{}}`)
	var old, twoScan, actual record
	if proofOldCustodyDecode(raw, &old, len(raw)) != ErrCapacity || proofTwoScanCustodyDecode(raw, &twoScan, len(raw)) != ErrCapacity || custodyDecode(raw, &actual, len(raw)) != ErrCapacity {
		t.Fatal("second scan byte bound lost")
	}
}

func TestCustodyDecodeAllocationReduction(t *testing.T) {
	// Bounded, fixed representative wire (no filesystem or time inside the
	// allocation sample). The actual decoder must do strictly less work than
	// the frozen original, not merely beat an arbitrary generous ceiling.
	w := enrollmentWire{Schema: 1, Binding: bindingToWire(proofBinding())}
	pre, _ := json.Marshal(w)
	w.Checksum = custodyHash("enrollment", pre)
	raw, _ := json.Marshal(w)
	measure := func(decode func([]byte, any, int) error) float64 {
		return testing.AllocsPerRun(20, func() {
			var out enrollmentWire
			if e := decode(raw, &out, custodySpecBytes); e != nil || out != w {
				t.Fatal("allocation fixture not decoded exactly", e)
			}
		})
	}
	old, actual := measure(proofOldCustodyDecode), measure(custodyDecode)
	t.Logf("allocations/decode old=%.0f actual=%.0f saved=%.0f", old, actual, old-actual)
	if actual >= old {
		t.Fatalf("strict allocation reduction absent: old=%.0f actual=%.0f", old, actual)
	}
}

func TestCustodyDecodeCanonicalAllocationReduction(t *testing.T) {
	// Generate a real completed custody document through public state operations;
	// no filesystem, clock or fixture construction belongs in the allocation sample.
	snap, _ := proofApply(t, proofStore(t, proofDirectory(t), false))
	document, e := custodyDocumentBytes(*snap.Document)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = custodyDocumentDecode(document); e != nil {
		t.Fatal("canonical document fixture invalid", e)
	}
	w := enrollmentWire{Schema: 1, Binding: bindingToWire(proofBinding())}
	pre, _ := json.Marshal(w)
	w.Checksum = custodyHash("enrollment", pre)
	enrollment, _ := json.Marshal(w)
	for _, f := range []struct {
		name    string
		raw     []byte
		newWire func() any
	}{
		{"document", document, func() any { return new(documentWire) }},
		{"enrollment", enrollment, func() any { return new(enrollmentWire) }},
	} {
		t.Run(f.name, func(t *testing.T) {
			// Exact typed bytes and errors must match the current two-scan baseline.
			// Equality to the input also establishes eligibility for the fast path.
			for _, decode := range []func([]byte, any, int) error{proofTwoScanCustodyDecode, custodyDecode} {
				out := f.newWire()
				if e := decode(f.raw, out, len(f.raw)); e != nil {
					t.Fatal(e)
				}
				raw, e := json.Marshal(out)
				if e != nil || !bytes.Equal(raw, f.raw) {
					t.Fatal("canonical typed output changed", e)
				}
			}
			measure := func(decode func([]byte, any, int) error) float64 {
				return testing.AllocsPerRun(20, func() {
					if e := decode(f.raw, f.newWire(), len(f.raw)); e != nil {
						t.Fatal(e)
					}
				})
			}
			twoScan, actual := measure(proofTwoScanCustodyDecode), measure(custodyDecode)
			t.Logf("canonical bytes=%d allocations/decode two-scan=%.0f actual=%.0f saved=%.0f", len(f.raw), twoScan, actual, twoScan-actual)
			if actual >= twoScan {
				t.Fatalf("strict canonical allocation reduction absent: two-scan=%.0f actual=%.0f", twoScan, actual)
			}
		})
	}
}

func TestFoundationExactDestinationAndUnicode(t *testing.T) {
	for _, endpoint := range []string{"https://runtime.invalid/ingest#", "https://runtime.invalid/ing est", "https://runtime.invalid/ingest?", "https://runtime.invalid/a/../ingest"} {
		in := proofInput(t)
		in.Binding.Destination.RuntimeEndpoint = endpoint
		if _, e := NewPlan(in); e == nil {
			t.Errorf("destination silently rebound: %q", endpoint)
		}
	}
	if _, e := NewAuthoredSpec([]byte(`{"text":"\ud800"}`)); e == nil {
		t.Error("unpaired surrogate silently replaced")
	}
	if _, e := NewAuthoredSpec([]byte(`{"text":"\ud800\udc00"}`)); e != nil {
		t.Fatal("valid Unicode pair rejected", e)
	}
}
