// SPDX-License-Identifier: AGPL-3.0-only

package agento11yprovision

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rknightion/synthkit/internal/core"
)

func custodyHash(domain string, b []byte) Digest {
	h := sha256.Sum256(append([]byte("agento11yprovision/v1/"+domain+"\n"), b...))
	return Digest(hex.EncodeToString(h[:]))
}
func custodyDigest(d Digest) bool {
	if len(d) != 64 {
		return false
	}
	for _, c := range d {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func custodyText(s string, empty bool) bool {
	return (empty || s != "") && len(s) <= 256 && utf8.ValidString(s) && strings.IndexFunc(s, unicode.IsControl) < 0 && strings.TrimSpace(s) == s
}
func custodyName(s string) bool {
	return custodyText(s, false) && strings.IndexFunc(s, unicode.IsSpace) < 0 && !strings.ContainsAny(s, "*?[]\\")
}
func custodyOrigin(o Origin) bool {
	return custodyText(o.Blueprint, false) && custodyText(o.Instance, false)
}
func custodyBase(b Base) bool {
	return b.Pristine && b.Head == nil || !b.Pristine && b.Head != nil && b.Head.Generation > 0 && custodyDigest(b.Head.Digest)
}
func custodySameBase(a, b Base) bool {
	return a.Pristine == b.Pristine && (a.Head == nil && b.Head == nil || a.Head != nil && b.Head != nil && *a.Head == *b.Head)
}
func custodyCleanPath(p string, empty bool) bool {
	if p == "" {
		return empty
	}
	if p[0] != '/' || strings.ContainsAny(p, "%\\\r\n") || strings.Contains(p, "//") || strings.HasSuffix(p, "/") {
		return false
	}
	for _, s := range strings.Split(p[1:], "/") {
		if s == "." || s == ".." || s == "" {
			return false
		}
	}
	return strings.IndexFunc(p, unicode.IsControl) < 0
}
func custodyURL(s string, origin bool) bool {
	if len(s) > 2048 || !utf8.ValidString(s) || strings.ContainsAny(s, "%?#") {
		return false
	}
	u, e := url.Parse(s)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.Hostname() != strings.ToLower(u.Hostname()) || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.Opaque != "" {
		return false
	}
	if origin {
		return u.Path == "" && u.String() == s
	}
	return u.String() == s && (u.Path == "/" || custodyCleanPath(u.Path, false))
}
func custodyBinding(b Binding) bool {
	d := b.Destination
	return custodyText(b.DeploymentID, false) && custodyText(b.StateID, false) && custodyText(b.ContractRevision, false) && custodyText(b.SourceRevision, false) && custodyURL(d.GrafanaOrigin, true) && custodyCleanPath(d.DeploymentPath, true) && custodyText(d.GrafanaOrg, false) && d.PluginRoute == managementPrefix && custodyText(d.EffectiveTenant, false) && custodyURL(d.RuntimeEndpoint, false) && custodyText(d.RuntimeTenant, false)
}

// Scan JSON before decoding typed records. Duplicate keys, invalid UTF-8 and
// trailing values must not be erased by encoding/json. Authored objects are open;
// record nullability is checked separately, never inferred by reflection.
func custodyJSON(raw []byte, max, depth int, records bool) (any, error) {
	if len(raw) > max {
		return nil, ErrCapacity
	}
	if !utf8.Valid(raw) || !custodyUnicodeEscapes(raw) {
		return nil, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(int, bool, string) (any, error)
	walk = func(level int, open bool, key string) (any, error) {
		if level > depth {
			return nil, ErrCapacity
		}
		t, e := d.Token()
		if e != nil {
			return nil, ErrInvalid
		}
		if t == nil {
			if records && !open && !slices.Contains([]string{"head", "pending", "returned", "recovery", "observation", "managed_hash"}, key) {
				return nil, ErrInvalid
			}
			return nil, nil
		}
		delim, compound := t.(json.Delim)
		if !compound {
			return t, nil
		}
		switch delim {
		case '{':
			m := map[string]any{}
			for d.More() {
				kt, e := d.Token()
				if e != nil {
					return nil, ErrInvalid
				}
				k, ok := kt.(string)
				if !ok {
					return nil, ErrInvalid
				}
				if _, ok = m[k]; ok {
					return nil, ErrInvalid
				}
				v, e := walk(level+1, open || k == "spec" || k == "authored" || k == "desired", k)
				if e != nil {
					return nil, e
				}
				m[k] = v
			}
			if _, e = d.Token(); e != nil {
				return nil, ErrInvalid
			}
			return m, nil
		case '[':
			a := []any{}
			for d.More() {
				v, e := walk(level+1, open, "")
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			if _, e = d.Token(); e != nil {
				return nil, ErrInvalid
			}
			return a, nil
		}
		return nil, ErrInvalid
	}
	v, e := walk(0, !records, "")
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, ErrInvalid
	}
	return v, nil
}

// encoding/json intentionally replaces unpaired UTF-16 escapes. Custody must
// reject that lossy input instead of assigning a digest to invented content.
func custodyUnicodeEscapes(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		i++
		for i < len(raw) && raw[i] != '"' {
			if raw[i] != '\\' {
				i++
				continue
			}
			if i+1 >= len(raw) {
				return false
			}
			if raw[i+1] != 'u' {
				i += 2
				continue
			}
			if i+6 > len(raw) {
				return false
			}
			unit, e := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
			if e != nil {
				return false
			}
			i += 6
			if unit >= 0xd800 && unit <= 0xdbff {
				if i+6 > len(raw) || raw[i] != '\\' || raw[i+1] != 'u' {
					return false
				}
				low, e := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
				if e != nil || low < 0xdc00 || low > 0xdfff {
					return false
				}
				i += 6
			} else if unit >= 0xdc00 && unit <= 0xdfff {
				return false
			}
		}
	}
	return true
}
func custodyDecode(raw []byte, out any, max int) error {
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

// custodyTreeEqual compares only bounded, acyclic custodyJSON trees. Number
// lexemes, container kinds and key presence are significant; map order is not.
// It retains nothing and must not be used as a general Go-value comparator.
func custodyTreeEqual(a, b any) bool {
	switch a := a.(type) {
	case nil:
		return b == nil
	case bool:
		v, ok := b.(bool)
		return ok && a == v
	case string:
		v, ok := b.(string)
		return ok && a == v
	case json.Number:
		v, ok := b.(json.Number)
		return ok && a == v
	case []any:
		v, ok := b.([]any)
		if !ok || (a == nil) != (v == nil) || len(a) != len(v) {
			return false
		}
		for i := range a {
			if !custodyTreeEqual(a[i], v[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		v, ok := b.(map[string]any)
		if !ok || (a == nil) != (v == nil) || len(a) != len(v) {
			return false
		}
		for k, value := range a {
			other, present := v[k]
			if !present || !custodyTreeEqual(value, other) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func NewAuthoredSpec(raw []byte) (AuthoredSpec, error) {
	v, e := custodyJSON(raw, custodySpecBytes, 16, false)
	if e != nil {
		return AuthoredSpec{}, e
	}
	if _, ok := v.(map[string]any); !ok {
		return AuthoredSpec{}, ErrInvalid
	}
	b, e := json.Marshal(v)
	if e != nil || len(b) > custodySpecBytes {
		return AuthoredSpec{}, ErrCapacity
	}
	return AuthoredSpec{b, custodyHash("authored", b)}, nil
}
func custodySpec(s AuthoredSpec) bool {
	v, e := NewAuthoredSpec(s.raw)
	return e == nil && v.hash == s.hash && bytes.Equal(v.raw, s.raw)
}
func custodyMethod(k ResourceKind, o Operation) string {
	switch o {
	case OpCreate:
		if slices.Contains([]ResourceKind{KindEvaluator, KindHook, KindRule, KindCollection, KindAction, KindSuite, KindDraft, KindCase}, k) {
			return "POST"
		}
	case OpInitialBindings:
		if k == KindBindings {
			return "PUT"
		}
	case OpPublish:
		if k == KindPublication {
			return "POST"
		}
	case OpDisable:
		if k == KindHook {
			return "PUT"
		}
		if k == KindRule || k == KindAction {
			return "PATCH"
		}
	case OpDelete:
		if slices.Contains([]ResourceKind{KindEvaluator, KindHook, KindRule, KindCollection, KindAction}, k) {
			return "DELETE"
		}
	}
	return ""
}
func custodyCoordinate(c Coordinate, planned bool, versionFrom string) bool {
	if !custodyText(c.ID, true) || !custodyText(c.ParentID, true) || !custodyText(c.Version, true) {
		return false
	}
	if c.Unassigned {
		if !planned || c.ID != "" || !(c.Kind == KindCollection || c.Kind == KindAction || c.Kind == KindDraft) {
			return false
		}
	} else if c.ID == "" && c.Kind != KindBindings && c.Kind != KindPublication {
		return false
	}
	switch c.Kind {
	case KindEvaluator:
		return c.ParentID == "" && c.Version != "" && !c.Unassigned && versionFrom == ""
	case KindHook, KindRule, KindSuite:
		return c.ParentID == "" && c.Version == "" && !c.Unassigned && versionFrom == ""
	case KindCollection:
		return c.ParentID == "" && c.Version == "" && versionFrom == ""
	case KindAction:
		return c.ParentID != "" && c.Version == "" && versionFrom == ""
	case KindDraft:
		return c.ParentID != "" && versionFrom == "" && (c.Unassigned && c.Version == "" || !c.Unassigned && c.ID == c.Version)
	case KindCase, KindBindings, KindPublication:
		return c.ParentID != "" && !c.Unassigned && (c.Version != "" && versionFrom == "" || planned && c.Version == "" && versionFrom != "")
	}
	return false
}
func custodyIdentity(c Coordinate) string {
	k := string(c.Kind)
	if c.Kind == KindHook || c.Kind == KindRule {
		k = "consumer"
	}
	if c.Unassigned {
		return ""
	}
	if c.Kind == KindEvaluator {
		return k + "/" + c.ID
	}
	return k + "/" + c.ParentID + "/" + c.Version + "/" + c.ID
}
func NewPlan(in PlanInput) (Plan, error) {
	if !custodyBinding(in.Binding) || !(in.Mode == ModeApply || in.Mode == ModeDeactivate || in.Mode == ModeTeardown) {
		return Plan{}, ErrInvalid
	}
	if len(in.Resources) > custodyMaxResources || len(in.Steps) > custodyMaxSteps || len(in.Targets) > custodyMaxClaims {
		return Plan{}, ErrCapacity
	}
	// Lower bounds on encoded size, before copying or marshaling caller buffers.
	// Every declaration's required wire keys alone exceed 240 bytes. Count all
	// variable strings/containers without arithmetic that can overflow.
	budget := 0
	add := func(n int) bool {
		if n > custodyMaxBytes-budget {
			return false
		}
		budget += n
		return true
	}
	for _, o := range in.Targets {
		if !custodyOrigin(o) {
			return Plan{}, ErrInvalid
		}
	}
	for _, d := range in.Declarations {
		a := d.Declaration
		if !custodyOrigin(d.Origin) || a.ProducerMode != core.AgentProducerReal && a.ProducerMode != core.AgentProducerSynthetic {
			return Plan{}, ErrInvalid
		}
		if !add(240) || !add(len(a.Name)) || !add(len(a.Service.Name)) || !add(len(a.Service.Namespace)) {
			return Plan{}, ErrCapacity
		}
		for _, m := range a.Models {
			if !custodyText(m.Provider, true) || !custodyText(m.Model, true) {
				return Plan{}, ErrInvalid
			}
			if !add(24) || !add(len(m.Provider)) || !add(len(m.Model)) {
				return Plan{}, ErrCapacity
			}
		}
		for _, n := range a.ProjectedChildNames {
			if !add(3) || !add(len(n)) {
				return Plan{}, ErrCapacity
			}
		}
		for _, n := range a.HookPolicy.Phases {
			if !add(3) || !add(len(n)) {
				return Plan{}, ErrCapacity
			}
		}
	}
	for _, r := range in.Resources {
		if !custodyText(r.Key, false) || !custodyText(r.VersionFrom, true) || !custodyOrigin(r.Origin) || !custodyCoordinate(r.Coordinate, true, r.VersionFrom) || !custodySpec(r.Spec) {
			return Plan{}, ErrInvalid
		}
		if !add(len(r.Spec.raw)) {
			return Plan{}, ErrCapacity
		}
		for _, list := range [][]string{r.Dependencies, r.Namespaces} {
			for _, n := range list {
				if !add(3) || !add(len(n)) {
					return Plan{}, ErrCapacity
				}
			}
		}
	}
	for _, step := range in.Steps {
		if !custodyText(step.ResourceKey, false) || !custodyText(string(step.Operation), false) {
			return Plan{}, ErrInvalid
		}
	}
	// Typed wire copying is also the immutable ownership boundary for every slice.
	w := planToWire(in)
	raw, e := json.Marshal(w)
	if e != nil || len(raw) > custodyMaxBytes {
		return Plan{}, ErrCapacity
	}
	owned, e := planFromWire(w)
	if e != nil {
		return Plan{}, e
	}
	in = owned
	names := map[string]Reservation{}
	origins := map[Origin]DeclarationInput{}
	target := map[Origin]bool{}
	for _, d := range in.Declarations {
		if !custodyOrigin(d.Origin) || !custodyName(d.Declaration.Name) || !custodyText(d.Declaration.Service.Name, false) || !custodyText(d.Declaration.Service.Namespace, true) {
			return Plan{}, ErrInvalid
		}
		if _, ok := origins[d.Origin]; ok {
			return Plan{}, ErrConflict
		}
		origins[d.Origin] = d
		a := d.Declaration
		if a.ProducerMode != core.AgentProducerReal && a.ProducerMode != core.AgentProducerSynthetic {
			return Plan{}, ErrInvalid
		}
		if a.HookPolicy.Timeout < 0 || a.HookPolicy.Enabled && (a.HookPolicy.Timeout <= 0 || len(a.HookPolicy.Phases) != 1) {
			return Plan{}, ErrInvalid
		}
		phase := map[string]bool{}
		for _, s := range a.HookPolicy.Phases {
			if s != "preflight" || phase[s] {
				return Plan{}, ErrInvalid
			}
			phase[s] = true
		}
		all := append([]string{a.Name}, a.ProjectedChildNames...)
		for _, n := range all {
			if !custodyName(n) {
				return Plan{}, ErrInvalid
			}
			key := strings.ToLower(strings.TrimSpace(n))
			if _, ok := names[key]; ok {
				return Plan{}, ErrConflict
			}
			names[key] = Reservation{Namespace: key, WireName: n, Origin: d.Origin}
		}
	}
	for _, o := range in.Targets {
		d, ok := origins[o]
		if !ok || target[o] || d.Declaration.ProducerMode != core.AgentProducerReal {
			return Plan{}, ErrInvalid
		}
		target[o] = true
	}
	claims := []Reservation{}
	for _, r := range names {
		if target[r.Origin] {
			claims = append(claims, r)
		}
	}
	sort.Slice(claims, func(i, j int) bool { return claims[i].Namespace < claims[j].Namespace })
	if len(claims) > custodyMaxClaims {
		return Plan{}, ErrCapacity
	}
	ns := []string{}
	for _, r := range claims {
		ns = append(ns, r.Namespace)
	}
	resources := map[string]PlannedResource{}
	ids := map[string]bool{}
	for i, r := range in.Resources {
		if !custodyText(r.Key, false) || !target[r.Origin] || !custodySpec(r.Spec) || !custodyCoordinate(r.Coordinate, true, r.VersionFrom) {
			return Plan{}, ErrInvalid
		}
		if _, ok := resources[r.Key]; ok {
			return Plan{}, ErrConflict
		}
		id := custodyIdentity(r.Coordinate)
		if id != "" && ids[id] {
			return Plan{}, ErrConflict
		}
		if id != "" {
			ids[id] = true
		}
		deps := []string{}
		for _, dep := range r.Dependencies {
			if !custodyText(dep, false) || dep == r.Key {
				return Plan{}, ErrInvalid
			}
			if !slices.Contains(deps, dep) {
				deps = append(deps, dep)
			}
		}
		in.Resources[i].Dependencies = deps
		r.Dependencies = deps
		seen := map[string]bool{}
		for _, n := range r.Namespaces {
			if !slices.Contains(ns, n) || names[n].Origin != r.Origin || seen[n] {
				return Plan{}, ErrInvalid
			}
			seen[n] = true
		}
		resources[r.Key] = r
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string) bool
	visit = func(k string) bool {
		r, ok := resources[k]
		if !ok || visiting[k] {
			return false
		}
		if visited[k] {
			return true
		}
		visiting[k] = true
		for _, d := range r.Dependencies {
			if !visit(d) {
				return false
			}
		}
		visiting[k] = false
		visited[k] = true
		return true
	}
	draftParents := map[string]bool{}
	for k, r := range resources {
		if !visit(k) {
			return Plan{}, ErrInvalid
		}
		if in.Mode == ModeApply {
			if r.Coordinate.Kind == KindDraft {
				if draftParents[r.Coordinate.ParentID] {
					return Plan{}, ErrConflict
				}
				draftParents[r.Coordinate.ParentID] = true
			}
			serverID := r.Coordinate.Kind == KindCollection || r.Coordinate.Kind == KindAction || r.Coordinate.Kind == KindDraft
			if serverID && !r.Coordinate.Unassigned {
				return Plan{}, ErrInvalid
			}
			if (r.Coordinate.Kind == KindCase || r.Coordinate.Kind == KindBindings || r.Coordinate.Kind == KindPublication) && r.VersionFrom == "" {
				return Plan{}, ErrInvalid
			}
		}
		if r.Coordinate.ParentID != "" {
			parentKind := KindSuite
			if r.Coordinate.Kind == KindAction {
				parentKind = KindRule
			}
			found := false
			for _, parent := range resources {
				if parent.Coordinate.Kind == parentKind && parent.Coordinate.ID == r.Coordinate.ParentID && slices.Contains(r.Dependencies, parent.Key) {
					found = true
				}
			}
			if !found {
				return Plan{}, ErrInvalid
			}
		}
		if r.VersionFrom != "" {
			d, ok := resources[r.VersionFrom]
			if !ok || d.Coordinate.Kind != KindDraft || d.Coordinate.ParentID != r.Coordinate.ParentID || !slices.Contains(r.Dependencies, d.Key) {
				return Plan{}, ErrInvalid
			}
		}
	}
	first := map[string]int{}
	lastDelete := map[string]int{}
	disables := map[string]int{}
	ops := map[string]bool{}
	for i, s := range in.Steps {
		r, ok := resources[s.ResourceKey]
		if !ok || s.Number != uint32(i+1) || custodyMethod(r.Coordinate.Kind, s.Operation) == "" {
			return Plan{}, ErrInvalid
		}
		tag := s.ResourceKey + "/" + string(s.Operation)
		if ops[tag] {
			return Plan{}, ErrInvalid
		}
		ops[tag] = true
		if in.Mode == ModeApply {
			if s.Operation == OpDisable || s.Operation == OpDelete {
				return Plan{}, ErrInvalid
			}
			if _, ok = first[r.Key]; ok {
				return Plan{}, ErrInvalid
			}
		} else if s.Operation != OpDisable && s.Operation != OpDelete {
			return Plan{}, ErrInvalid
		}
		if _, ok = first[r.Key]; !ok {
			first[r.Key] = i
		}
		if s.Operation == OpDelete {
			lastDelete[r.Key] = i
		}
		if s.Operation == OpDisable {
			disables[r.Key] = i
		}
	}
	if in.Mode == ModeApply {
		for _, r := range in.Resources {
			n, ok := first[r.Key]
			if !ok {
				return Plan{}, ErrInvalid
			}
			for _, d := range r.Dependencies {
				if first[d] >= n {
					return Plan{}, ErrInvalid
				}
			}
		}
	} else {
		// Cleanup prerequisites are NOT the creation graph: the complete consumer
		// disable phase precedes deletes, then dependents delete before dependencies.
		for _, del := range lastDelete {
			for _, dis := range disables {
				if dis >= del {
					return Plan{}, ErrInvalid
				}
			}
		}
		for _, r := range in.Resources {
			d, ok := lastDelete[r.Key]
			if !ok {
				continue
			}
			for _, dep := range r.Dependencies {
				if dd, ok := lastDelete[dep]; ok && dd <= d {
					return Plan{}, ErrInvalid
				}
			}
		}
	}
	raw, e = json.Marshal(planToWire(in))
	if e != nil || len(raw) > custodyMaxBytes {
		return Plan{}, ErrCapacity
	}
	return Plan{in, ns, claims, custodyHash("plan", raw)}, nil
}
func NewPreview(p Plan, mode Mode, base Base, inventory Digest) (Preview, error) {
	if !custodyDigest(p.hash) || !custodyBase(base) || !custodyDigest(inventory) || !(mode == p.input.Mode || mode == ModeRecover) {
		return Preview{}, ErrInvalid
	}
	// Reconstruct rather than trusting an opaque zero/cached value.
	q, e := NewPlan(p.input)
	if e != nil || q.hash != p.hash {
		return Preview{}, ErrInvalid
	}
	b := baseToWire(base)
	raw, e := json.Marshal(previewWire{1, p.hash, mode, bindingToWire(p.input.Binding), b, inventory, append([]string{}, p.names...)})
	if e != nil || len(raw) > custodyMaxBytes {
		return Preview{}, ErrCapacity
	}
	ownBase := baseFromWire(b)
	return Preview{q, mode, ownBase, inventory, raw, custodyHash("preview", raw)}, nil
}

// Private typed codec views. No exported record gains JSON methods/tags and no
// reflect-based serializer can accidentally encode an opaque spec as {}.
type destinationWire struct {
	GrafanaOrigin   string `json:"grafana_origin"`
	DeploymentPath  string `json:"deployment_path"`
	GrafanaOrg      string `json:"grafana_org"`
	PluginRoute     string `json:"plugin_route"`
	EffectiveTenant string `json:"effective_tenant"`
	RuntimeEndpoint string `json:"runtime_endpoint"`
	RuntimeTenant   string `json:"runtime_tenant"`
}
type custodyBindingWire struct {
	DeploymentID     string          `json:"deployment_id"`
	StateID          string          `json:"state_id"`
	ContractRevision string          `json:"contract_revision"`
	SourceRevision   string          `json:"source_revision"`
	Destination      destinationWire `json:"destination"`
}
type headWire struct {
	Generation uint64 `json:"generation"`
	Digest     Digest `json:"digest"`
}
type baseWire struct {
	Pristine bool      `json:"pristine"`
	Head     *headWire `json:"head"`
}
type originWire struct {
	Blueprint   string `json:"blueprint"`
	Instance    string `json:"instance"`
	Declaration uint32 `json:"declaration"`
}
type coordinateWire struct {
	Kind       ResourceKind `json:"kind"`
	ID         string       `json:"id"`
	ParentID   string       `json:"parent_id"`
	Version    string       `json:"version"`
	Unassigned bool         `json:"unassigned"`
}
type serviceWire struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}
type modelWire struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}
type hooksWire struct {
	Enabled  bool     `json:"enabled"`
	Phases   []string `json:"phases"`
	FailOpen bool     `json:"fail_open"`
	Timeout  int64    `json:"timeout"`
}
type agentWire struct {
	Name                string                 `json:"name"`
	Service             serviceWire            `json:"service"`
	ProducerMode        core.AgentProducerMode `json:"producer_mode"`
	HookPolicy          hooksWire              `json:"hook_policy"`
	Models              []modelWire            `json:"models"`
	ProjectedChildNames []string               `json:"projected_child_names"`
}
type declarationWire struct {
	Origin      originWire `json:"origin"`
	Declaration agentWire  `json:"declaration"`
}
type plannedResourceWire struct {
	Key          string          `json:"key"`
	VersionFrom  string          `json:"version_from"`
	Origin       originWire      `json:"origin"`
	Coordinate   coordinateWire  `json:"coordinate"`
	Spec         json.RawMessage `json:"spec"`
	Dependencies []string        `json:"dependencies"`
	Namespaces   []string        `json:"namespaces"`
}
type plannedStepWire struct {
	Number      uint32    `json:"number"`
	ResourceKey string    `json:"resource_key"`
	Operation   Operation `json:"operation"`
}
type planWire struct {
	Binding      custodyBindingWire    `json:"binding"`
	Mode         Mode                  `json:"mode"`
	Declarations []declarationWire     `json:"declarations"`
	Targets      []originWire          `json:"targets"`
	Resources    []plannedResourceWire `json:"resources"`
	Steps        []plannedStepWire     `json:"steps"`
}
type previewWire struct {
	Schema     uint32             `json:"schema"`
	Input      Digest             `json:"input"`
	Mode       Mode               `json:"mode"`
	Binding    custodyBindingWire `json:"binding"`
	Base       baseWire           `json:"base"`
	Inventory  Digest             `json:"inventory"`
	Namespaces []string           `json:"namespaces"`
}
type evidenceWire struct {
	ID     string `json:"id"`
	Digest Digest `json:"digest"`
}
type foreignWire struct {
	Coordinate  coordinateWire `json:"coordinate"`
	ManagedHash Digest         `json:"managed_hash"`
	Reason      string         `json:"reason"`
	Evidence    []evidenceWire `json:"evidence"`
}
type assertionsWire struct {
	AuthorizedOperator              bool           `json:"authorized_operator"`
	EffectiveDestination            bool           `json:"effective_destination"`
	AllAffectedEmittersStopped      bool           `json:"all_affected_emitters_stopped"`
	KeepStoppedUntilReceiptAccepted bool           `json:"keep_stopped_until_receipt_accepted"`
	ExclusiveNamespace              bool           `json:"exclusive_namespace"`
	OutstandingWorkMayFinish        bool           `json:"outstanding_work_may_finish"`
	NonAtomicInitialPublication     bool           `json:"non_atomic_initial_publication"`
	PermanentRecordsMayRemain       bool           `json:"permanent_records_may_remain"`
	NoSyntheticReentry              bool           `json:"no_synthetic_reentry"`
	DestinationEvidence             []evidenceWire `json:"destination_evidence"`
	RuntimeEvidence                 []evidenceWire `json:"runtime_evidence"`
	StopEvidence                    []evidenceWire `json:"stop_evidence"`
	NamespaceEvidence               []evidenceWire `json:"namespace_evidence"`
	Foreign                         []foreignWire  `json:"foreign"`
}
type recoveryWire struct {
	AttemptID           string         `json:"attempt_id"`
	OriginalInput       Digest         `json:"original_input"`
	OriginalPreview     Digest         `json:"original_preview"`
	OriginalApproval    Digest         `json:"original_approval"`
	ResourceKey         string         `json:"resource_key"`
	Coordinate          coordinateWire `json:"coordinate"`
	Operation           Operation      `json:"operation"`
	AuthoredHash        Digest         `json:"authored_hash"`
	ExpectedManagedHash Digest         `json:"expected_managed_hash"`
	ExactRead           Digest         `json:"exact_read"`
	Inventory           Digest         `json:"inventory"`
	Attribution         []evidenceWire `json:"attribution"`
	Disposition         Disposition    `json:"disposition"`
}
type approvalWire struct {
	Schema         uint32             `json:"schema"`
	ApprovalID     string             `json:"approval_id"`
	Operator       string             `json:"operator"`
	ConstructionID string             `json:"construction_id"`
	IssuedMS       int64              `json:"issued_ms"`
	ExpiresMS      int64              `json:"expires_ms"`
	Binding        custodyBindingWire `json:"binding"`
	Input          Digest             `json:"input"`
	Preview        Digest             `json:"preview"`
	Inventory      Digest             `json:"inventory"`
	Base           baseWire           `json:"base"`
	Mode           Mode               `json:"mode"`
	Namespaces     []string           `json:"namespaces"`
	Assertions     assertionsWire     `json:"assertions"`
	Recovery       *recoveryWire      `json:"recovery"`
}
type reservationWire struct {
	Namespace         string     `json:"namespace"`
	WireName          string     `json:"wire_name"`
	Origin            originWire `json:"origin"`
	FirstConstruction string     `json:"first_construction"`
	Approval          Digest     `json:"approval"`
}
type observationWire struct {
	Coordinate     coordinateWire `json:"coordinate"`
	Presence       Presence       `json:"presence"`
	ManagedHash    Digest         `json:"managed_hash"`
	ExactRead      Digest         `json:"exact_read"`
	Inventory      Digest         `json:"inventory"`
	ServerRevision string         `json:"server_revision"`
	Evidence       []evidenceWire `json:"evidence"`
}
type resourceWire struct {
	Key            string            `json:"key"`
	Origin         originWire        `json:"origin"`
	Coordinate     coordinateWire    `json:"coordinate"`
	FirstAttempt   string            `json:"first_attempt"`
	Authored       json.RawMessage   `json:"authored"`
	Dependencies   []string          `json:"dependencies"`
	Namespaces     []string          `json:"namespaces"`
	ManagedHash    Digest            `json:"managed_hash"`
	ServerRevision string            `json:"server_revision"`
	Acquisition    []evidenceWire    `json:"acquisition"`
	Observations   []observationWire `json:"observations"`
	Status         ResourceStatus    `json:"status"`
}
type stepWire struct {
	Number     uint32         `json:"number"`
	AttemptID  string         `json:"attempt_id"`
	Status     StepStatus     `json:"status"`
	Coordinate coordinateWire `json:"coordinate"`
	Evidence   []evidenceWire `json:"evidence"`
}
type classificationWire struct {
	ResourceKey  string           `json:"resource_key"`
	Coordinate   coordinateWire   `json:"coordinate"`
	ExistingHead headWire         `json:"existing_head"`
	Reason       CleanupReason    `json:"reason"`
	Observation  *observationWire `json:"observation"`
	Inventory    Digest           `json:"inventory"`
	Evidence     []evidenceWire   `json:"evidence"`
}
type constructionWire struct {
	ID              string               `json:"id"`
	Input           Digest               `json:"input"`
	Preview         Digest               `json:"preview"`
	Approval        Digest               `json:"approval"`
	Mode            Mode                 `json:"mode"`
	Base            baseWire             `json:"base"`
	Namespaces      []string             `json:"namespaces"`
	Plan            planWire             `json:"plan"`
	Steps           []stepWire           `json:"steps"`
	Classifications []classificationWire `json:"classifications"`
	Complete        bool                 `json:"complete"`
}
type responseWire struct {
	Coordinate  coordinateWire `json:"coordinate"`
	Evidence    []evidenceWire `json:"evidence"`
	ManagedHash *Digest        `json:"managed_hash"`
}
type pendingWire struct {
	AttemptID        string          `json:"attempt_id"`
	ConstructionID   string          `json:"construction_id"`
	ResourceKey      string          `json:"resource_key"`
	BindingDigest    Digest          `json:"binding_digest"`
	Input            Digest          `json:"input"`
	Preview          Digest          `json:"preview"`
	OriginalApproval Digest          `json:"original_approval"`
	IntentHead       headWire        `json:"intent_head"`
	Namespaces       []string        `json:"namespaces"`
	Coordinate       coordinateWire  `json:"coordinate"`
	Operation        Operation       `json:"operation"`
	Method           string          `json:"method"`
	Desired          json.RawMessage `json:"desired"`
	PreInventory     Digest          `json:"pre_inventory"`
	PreObservation   Digest          `json:"pre_observation"`
	Phase            StepStatus      `json:"phase"`
	Returned         *responseWire   `json:"returned"`
}
type memberWire struct {
	ResourceKey string           `json:"resource_key"`
	Coordinate  coordinateWire   `json:"coordinate"`
	Outcome     MemberOutcome    `json:"outcome"`
	Observation *observationWire `json:"observation"`
	Evidence    []evidenceWire   `json:"evidence"`
}
type receiptWire struct {
	ID               string        `json:"id"`
	ConstructionID   string        `json:"construction_id"`
	Mode             Mode          `json:"mode"`
	Result           ReceiptResult `json:"result"`
	BindingDigest    Digest        `json:"binding_digest"`
	Input            Digest        `json:"input"`
	Preview          Digest        `json:"preview"`
	Inventory        Digest        `json:"inventory"`
	Namespaces       []string      `json:"namespaces"`
	Members          []memberWire  `json:"members"`
	OperatorApproval Digest        `json:"operator_approval"`
	ObservedMS       int64         `json:"observed_ms"`
	InvalidatedBy    string        `json:"invalidated_by"`
}
type consumedWire struct {
	ID             string   `json:"id"`
	Digest         Digest   `json:"digest"`
	ConstructionID string   `json:"construction_id"`
	Base           baseWire `json:"base"`
}
type documentWire struct {
	Schema        uint32             `json:"schema"`
	Binding       custodyBindingWire `json:"binding"`
	Generation    uint64             `json:"generation"`
	Checksum      Digest             `json:"checksum,omitempty"`
	Reservations  []reservationWire  `json:"reservations"`
	Resources     []resourceWire     `json:"resources"`
	Constructions []constructionWire `json:"constructions"`
	Receipts      []receiptWire      `json:"receipts"`
	Consumed      []consumedWire     `json:"consumed"`
	Pending       *pendingWire       `json:"pending"`
}
type enrollmentWire struct {
	Schema   uint32             `json:"schema"`
	Binding  custodyBindingWire `json:"binding"`
	Checksum Digest             `json:"checksum,omitempty"`
}

func bindingToWire(b Binding) custodyBindingWire {
	return custodyBindingWire{b.DeploymentID, b.StateID, b.ContractRevision, b.SourceRevision, destinationWire(b.Destination)}
}
func bindingFromWire(b custodyBindingWire) Binding {
	return Binding{b.DeploymentID, b.StateID, b.ContractRevision, b.SourceRevision, Destination(b.Destination)}
}
func baseToWire(b Base) baseWire {
	w := baseWire{Pristine: b.Pristine}
	if b.Head != nil {
		h := headWire(*b.Head)
		w.Head = &h
	}
	return w
}
func baseFromWire(b baseWire) Base {
	v := Base{Pristine: b.Pristine}
	if b.Head != nil {
		h := Head(*b.Head)
		v.Head = &h
	}
	return v
}
func evidenceToWire(in []Evidence) []evidenceWire {
	out := make([]evidenceWire, len(in))
	for i, e := range in {
		out[i] = evidenceWire(e)
	}
	return out
}
func evidenceFromWire(in []evidenceWire) []Evidence {
	out := make([]Evidence, len(in))
	for i, e := range in {
		out[i] = Evidence(e)
	}
	return out
}
func observationToWire(o Observation) observationWire {
	return observationWire{coordinateWire(o.Coordinate), o.Presence, o.ManagedHash, o.ExactRead, o.Inventory, o.ServerRevision, evidenceToWire(o.Evidence)}
}
func observationFromWire(o observationWire) Observation {
	return Observation{Coordinate(o.Coordinate), o.Presence, o.ManagedHash, o.ExactRead, o.Inventory, o.ServerRevision, evidenceFromWire(o.Evidence)}
}
func observationPointerToWire(o *Observation) *observationWire {
	if o == nil {
		return nil
	}
	w := observationToWire(*o)
	return &w
}
func observationPointerFromWire(o *observationWire) *Observation {
	if o == nil {
		return nil
	}
	v := observationFromWire(*o)
	return &v
}
func classificationToWire(c CleanupClassification) classificationWire {
	return classificationWire{c.ResourceKey, coordinateWire(c.Coordinate), headWire(c.ExistingHead), c.Reason, observationPointerToWire(c.Observation), c.Inventory, evidenceToWire(c.Evidence)}
}
func classificationFromWire(c classificationWire) CleanupClassification {
	return CleanupClassification{c.ResourceKey, Coordinate(c.Coordinate), Head(c.ExistingHead), c.Reason, observationPointerFromWire(c.Observation), c.Inventory, evidenceFromWire(c.Evidence)}
}
func responseToWire(r *ResponseEvidence) *responseWire {
	if r == nil {
		return nil
	}
	w := &responseWire{coordinateWire(r.Coordinate), evidenceToWire(r.Evidence), nil}
	if r.ManagedHash != nil {
		h := *r.ManagedHash
		w.ManagedHash = &h
	}
	return w
}
func responseFromWire(r *responseWire) *ResponseEvidence {
	if r == nil {
		return nil
	}
	v := &ResponseEvidence{Coordinate(r.Coordinate), evidenceFromWire(r.Evidence), nil}
	if r.ManagedHash != nil {
		h := *r.ManagedHash
		v.ManagedHash = &h
	}
	return v
}
func planToWire(p PlanInput) planWire {
	w := planWire{Binding: bindingToWire(p.Binding), Mode: p.Mode, Declarations: []declarationWire{}, Targets: []originWire{}, Resources: []plannedResourceWire{}, Steps: []plannedStepWire{}}
	for _, d := range p.Declarations {
		a := d.Declaration
		m := make([]modelWire, len(a.Models))
		for i, x := range a.Models {
			m[i] = modelWire(x)
		}
		w.Declarations = append(w.Declarations, declarationWire{originWire(d.Origin), agentWire{a.Name, serviceWire(a.Service), a.ProducerMode, hooksWire{a.HookPolicy.Enabled, append([]string{}, a.HookPolicy.Phases...), a.HookPolicy.FailOpen, int64(a.HookPolicy.Timeout)}, m, append([]string{}, a.ProjectedChildNames...)}})
	}
	for _, o := range p.Targets {
		w.Targets = append(w.Targets, originWire(o))
	}
	for _, r := range p.Resources {
		w.Resources = append(w.Resources, plannedResourceWire{r.Key, r.VersionFrom, originWire(r.Origin), coordinateWire(r.Coordinate), r.Spec.JSON(), append([]string{}, r.Dependencies...), append([]string{}, r.Namespaces...)})
	}
	for _, s := range p.Steps {
		w.Steps = append(w.Steps, plannedStepWire(s))
	}
	return w
}
func planFromWire(w planWire) (PlanInput, error) {
	p := PlanInput{Binding: bindingFromWire(w.Binding), Mode: w.Mode, Declarations: []DeclarationInput{}, Targets: []Origin{}, Resources: []PlannedResource{}, Steps: []PlannedStep{}}
	for _, d := range w.Declarations {
		a := d.Declaration
		m := make([]core.AgentModelPair, len(a.Models))
		for i, x := range a.Models {
			m[i] = core.AgentModelPair(x)
		}
		p.Declarations = append(p.Declarations, DeclarationInput{Origin(d.Origin), core.AgentDeclaration{Name: a.Name, Service: core.AgentServiceIdentity(a.Service), ProducerMode: a.ProducerMode, HookPolicy: core.AgentHookPolicy{Enabled: a.HookPolicy.Enabled, Phases: append([]string{}, a.HookPolicy.Phases...), FailOpen: a.HookPolicy.FailOpen, Timeout: time.Duration(a.HookPolicy.Timeout)}, Models: m, ProjectedChildNames: append([]string{}, a.ProjectedChildNames...)}})
	}
	for _, o := range w.Targets {
		p.Targets = append(p.Targets, Origin(o))
	}
	for _, r := range w.Resources {
		s, e := NewAuthoredSpec(r.Spec)
		if e != nil {
			return PlanInput{}, e
		}
		p.Resources = append(p.Resources, PlannedResource{r.Key, r.VersionFrom, Origin(r.Origin), Coordinate(r.Coordinate), s, append([]string{}, r.Dependencies...), append([]string{}, r.Namespaces...)})
	}
	for _, s := range w.Steps {
		p.Steps = append(p.Steps, PlannedStep(s))
	}
	return p, nil
}
func documentToWire(d Document) documentWire {
	w := documentWire{Schema: d.Schema, Binding: bindingToWire(d.Binding), Generation: d.Generation, Checksum: d.Checksum, Reservations: []reservationWire{}, Resources: []resourceWire{}, Constructions: []constructionWire{}, Receipts: []receiptWire{}, Consumed: []consumedWire{}}
	for _, r := range d.Reservations {
		w.Reservations = append(w.Reservations, reservationWire{r.Namespace, r.WireName, originWire(r.Origin), r.FirstConstruction, r.Approval})
	}
	for _, r := range d.Resources {
		obs := []observationWire{}
		for _, o := range r.Observations {
			obs = append(obs, observationToWire(o))
		}
		w.Resources = append(w.Resources, resourceWire{r.Key, originWire(r.Origin), coordinateWire(r.Coordinate), r.FirstAttempt, r.Authored.JSON(), append([]string{}, r.Dependencies...), append([]string{}, r.Namespaces...), r.ManagedHash, r.ServerRevision, evidenceToWire(r.Acquisition), obs, r.Status})
	}
	for _, c := range d.Constructions {
		steps := []stepWire{}
		for _, s := range c.Steps {
			steps = append(steps, stepWire{s.Number, s.AttemptID, s.Status, coordinateWire(s.Coordinate), evidenceToWire(s.Evidence)})
		}
		cls := []classificationWire{}
		for _, x := range c.Classifications {
			cls = append(cls, classificationToWire(x))
		}
		w.Constructions = append(w.Constructions, constructionWire{c.ID, c.Input, c.Preview, c.Approval, c.Mode, baseToWire(c.Base), append([]string{}, c.Namespaces...), planToWire(c.Plan), steps, cls, c.Complete})
	}
	for _, r := range d.Receipts {
		ms := []memberWire{}
		for _, m := range r.Members {
			ms = append(ms, memberWire{m.ResourceKey, coordinateWire(m.Coordinate), m.Outcome, observationPointerToWire(m.Observation), evidenceToWire(m.Evidence)})
		}
		w.Receipts = append(w.Receipts, receiptWire{r.ID, r.ConstructionID, r.Mode, r.Result, r.BindingDigest, r.Input, r.Preview, r.Inventory, append([]string{}, r.Namespaces...), ms, r.OperatorApproval, r.ObservedMS, r.InvalidatedBy})
	}
	for _, c := range d.Consumed {
		w.Consumed = append(w.Consumed, consumedWire{c.ID, c.Digest, c.ConstructionID, baseToWire(c.Base)})
	}
	if p := d.Pending; p != nil {
		w.Pending = &pendingWire{p.AttemptID, p.ConstructionID, p.ResourceKey, p.BindingDigest, p.Input, p.Preview, p.OriginalApproval, headWire(p.IntentHead), append([]string{}, p.Namespaces...), coordinateWire(p.Coordinate), p.Operation, p.Method, p.Desired.JSON(), p.PreInventory, p.PreObservation, p.Phase, responseToWire(p.Returned)}
	}
	return w
}
func documentFromWire(w documentWire) (Document, error) {
	d := Document{Schema: w.Schema, Binding: bindingFromWire(w.Binding), Generation: w.Generation, Checksum: w.Checksum, Reservations: []Reservation{}, Resources: []ResourceRecord{}, Constructions: []Construction{}, Receipts: []Receipt{}, Consumed: []ConsumedApproval{}}
	for _, r := range w.Reservations {
		d.Reservations = append(d.Reservations, Reservation{r.Namespace, r.WireName, Origin(r.Origin), r.FirstConstruction, r.Approval})
	}
	for _, r := range w.Resources {
		s, e := NewAuthoredSpec(r.Authored)
		if e != nil {
			return Document{}, e
		}
		obs := []Observation{}
		for _, o := range r.Observations {
			obs = append(obs, observationFromWire(o))
		}
		d.Resources = append(d.Resources, ResourceRecord{r.Key, Origin(r.Origin), Coordinate(r.Coordinate), r.FirstAttempt, s, append([]string{}, r.Dependencies...), append([]string{}, r.Namespaces...), r.ManagedHash, r.ServerRevision, evidenceFromWire(r.Acquisition), obs, r.Status})
	}
	for _, c := range w.Constructions {
		p, e := planFromWire(c.Plan)
		if e != nil {
			return Document{}, e
		}
		steps := []StepRecord{}
		for _, s := range c.Steps {
			steps = append(steps, StepRecord{s.Number, s.AttemptID, s.Status, Coordinate(s.Coordinate), evidenceFromWire(s.Evidence)})
		}
		cls := []CleanupClassification{}
		for _, x := range c.Classifications {
			cls = append(cls, classificationFromWire(x))
		}
		d.Constructions = append(d.Constructions, Construction{c.ID, c.Input, c.Preview, c.Approval, c.Mode, baseFromWire(c.Base), append([]string{}, c.Namespaces...), p, steps, cls, c.Complete})
	}
	for _, r := range w.Receipts {
		ms := []ReceiptMember{}
		for _, m := range r.Members {
			ms = append(ms, ReceiptMember{m.ResourceKey, Coordinate(m.Coordinate), m.Outcome, observationPointerFromWire(m.Observation), evidenceFromWire(m.Evidence)})
		}
		d.Receipts = append(d.Receipts, Receipt{r.ID, r.ConstructionID, r.Mode, r.Result, r.BindingDigest, r.Input, r.Preview, r.Inventory, append([]string{}, r.Namespaces...), ms, r.OperatorApproval, r.ObservedMS, r.InvalidatedBy})
	}
	for _, c := range w.Consumed {
		d.Consumed = append(d.Consumed, ConsumedApproval{c.ID, c.Digest, c.ConstructionID, baseFromWire(c.Base)})
	}
	if p := w.Pending; p != nil {
		s, e := NewAuthoredSpec(p.Desired)
		if e != nil {
			return Document{}, e
		}
		d.Pending = &Pending{p.AttemptID, p.ConstructionID, p.ResourceKey, p.BindingDigest, p.Input, p.Preview, p.OriginalApproval, Head(p.IntentHead), append([]string{}, p.Namespaces...), Coordinate(p.Coordinate), p.Operation, p.Method, s, p.PreInventory, p.PreObservation, p.Phase, responseFromWire(p.Returned)}
	}
	return d, nil
}

// Only this exact existing binding revision opts into the larger STATE bound.
// Invalid bindings never acquire a byte allowance. This is not a migration:
// older binaries may read small v2 states, so operators must pin a compatible
// reader/writer and must not downgrade a v2 authority to an older writer.
func custodyStateLimit(b Binding) int {
	if !custodyBinding(b) {
		return 0
	}
	if b.ContractRevision == custodyStateV2Revision {
		return custodyStateV2Bytes
	}
	return custodyMaxBytes
}
func custodyDocumentBytes(d Document) ([]byte, error) {
	limit := custodyStateLimit(d.Binding)
	if limit == 0 {
		return nil, ErrInvalid
	}
	w := documentToWire(d)
	w.Checksum = ""
	raw, e := json.Marshal(w)
	if e != nil {
		return nil, ErrCorrupt
	}
	w.Checksum = custodyHash("state", raw)
	raw, e = json.Marshal(w)
	if e != nil {
		return nil, ErrCorrupt
	}
	if len(raw) > limit {
		return nil, ErrCapacity
	}
	return raw, nil
}
func custodyDocumentDecode(raw []byte) (Document, error) {
	var w documentWire
	// The file reader has already bounded I/O by its EXPECTED binding. The
	// codec also enforces the embedded binding's limit before returning data,
	// including internal encode/decode copies with no file-reader boundary.
	if e := custodyDecode(raw, &w, custodyStateV2Bytes); e != nil {
		return Document{}, e
	}
	limit := custodyStateLimit(bindingFromWire(w.Binding))
	if limit == 0 {
		return Document{}, ErrCorrupt
	}
	if len(raw) > limit {
		return Document{}, ErrCapacity
	}
	hash := w.Checksum
	w.Checksum = ""
	b, e := json.Marshal(w)
	if e != nil || !custodyDigest(hash) || custodyHash("state", b) != hash {
		return Document{}, ErrCorrupt
	}
	w.Checksum = hash
	d, e := documentFromWire(w)
	if e != nil {
		return Document{}, e
	}
	if e = validateCustodyDocument(d); e != nil {
		return Document{}, e
	}
	return d, nil
}
func custodyBindingHash(b Binding) Digest {
	raw, _ := json.Marshal(bindingToWire(b))
	return custodyHash("binding", raw)
}
