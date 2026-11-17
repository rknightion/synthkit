// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"regexp"
	"strings"

	p "github.com/rknightion/synthkit/internal/agento11yprovision"
	"gopkg.in/yaml.v3"
)

// AgentObservabilityDecl is manual management authoring only, never runtime admission.
// Authored objects are retained independently of the convenient typed request views.
type AgentObservabilityDecl struct {
	Closures []AgentClosureDecl `yaml:"closures" json:"closures"`
	authored map[string]p.AuthoredSpec
}
type AgentRef struct {
	Instance  string `yaml:"instance" json:"instance"`
	AgentName string `yaml:"agent_name" json:"agent_name"`
}
type AgentClosureDecl struct {
	Key         string                `yaml:"key" json:"key"`
	Agent       AgentRef              `yaml:"agent" json:"agent"`
	Evaluators  []AgentEvaluatorDecl  `yaml:"evaluators" json:"evaluators"`
	Hooks       []AgentHookDecl       `yaml:"hooks" json:"hooks"`
	Rules       []AgentRuleDecl       `yaml:"rules" json:"rules"`
	Collections []AgentCollectionDecl `yaml:"collections" json:"collections"`
	Actions     []AgentActionDecl     `yaml:"actions" json:"actions"`
	Suites      []AgentSuiteDecl      `yaml:"suites" json:"suites"`
}
type AgentEvaluatorDecl struct {
	Key  string          `yaml:"key" json:"key"`
	Spec p.EvaluatorSpec `yaml:"spec" json:"spec"`
}
type AgentHookDecl struct {
	Key  string         `yaml:"key" json:"key"`
	Spec p.HookRuleSpec `yaml:"spec" json:"spec"`
}
type AgentRuleDecl struct {
	Key  string     `yaml:"key" json:"key"`
	Spec p.RuleSpec `yaml:"spec" json:"spec"`
}
type AgentCollectionDecl struct {
	Key  string           `yaml:"key" json:"key"`
	Spec p.CollectionSpec `yaml:"spec" json:"spec"`
}
type AgentActionDecl struct {
	Key        string           `yaml:"key" json:"key"`
	ParentRule string           `yaml:"parent_rule" json:"parent_rule"`
	Spec       p.RuleActionSpec `yaml:"spec" json:"spec"`
}
type AgentSuiteDecl struct {
	Key   string         `yaml:"key" json:"key"`
	Spec  p.SuiteSpec    `yaml:"spec" json:"spec"`
	Draft AgentDraftDecl `yaml:"draft" json:"draft"`
}
type AgentDraftDecl struct {
	Key         string               `yaml:"key" json:"key"`
	Spec        p.DraftSpec          `yaml:"spec" json:"spec"`
	Cases       []AgentCaseDecl      `yaml:"cases" json:"cases"`
	Bindings    AgentBindingsDecl    `yaml:"bindings" json:"bindings"`
	Publication AgentPublicationDecl `yaml:"publication" json:"publication"`
}
type AgentCaseDecl struct {
	Key  string     `yaml:"key" json:"key"`
	Spec p.CaseSpec `yaml:"spec" json:"spec"`
}
type AgentBindingsDecl struct {
	Key      string           `yaml:"key" json:"key"`
	Bindings []p.SuiteBinding `yaml:"bindings" json:"bindings"`
}
type AgentPublicationDecl struct {
	Key  string              `yaml:"key" json:"key"`
	Spec p.PublicationReview `yaml:"spec" json:"spec"`
}

var agentLocalKey = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var agentJSONNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// AuthoredSpecs returns independent immutable values, never a DTO remarshal.
func (d *AgentObservabilityDecl) AuthoredSpecs() map[string]p.AuthoredSpec {
	out := map[string]p.AuthoredSpec{}
	if d != nil {
		for k, s := range d.authored {
			out[k] = s
		}
	}
	return out
}

// agentNodeJSON is restricted to this authoring boundary. It preserves JSON
// numeric spellings and empty containers, and never expands YAML references.
func agentNodeJSON(n *yaml.Node, depth int) (any, error) {
	// Authoring wrappers do not consume the per-spec depth budget: retained
	// objects still pass unchanged through NewAuthoredSpec's exact depth-16 cap.
	if n == nil || depth > 64 || n.Anchor != "" || n.Kind == yaml.AliasNode {
		return nil, p.ErrInvalid
	}
	switch n.Kind {
	case yaml.MappingNode:
		m := map[string]any{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || k.Anchor != "" {
				return nil, p.ErrInvalid
			}
			if _, ok := m[k.Value]; ok {
				return nil, p.ErrConflict
			}
			v, e := agentNodeJSON(n.Content[i+1], depth+1)
			if e != nil {
				return nil, e
			}
			m[k.Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		a := []any{}
		for _, c := range n.Content {
			v, e := agentNodeJSON(c, depth+1)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		return a, nil
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!str":
			return n.Value, nil
		case "!!null":
			return nil, nil
		case "!!bool":
			if n.Value == "true" {
				return true, nil
			}
			if n.Value == "false" {
				return false, nil
			}
		case "!!int", "!!float":
			if agentJSONNumber.MatchString(n.Value) {
				return json.Number(n.Value), nil
			}
		}
	}
	return nil, p.ErrInvalid
}

// Case-owned JSON maps deliberately remain open and Number-valued. Every typed
// request/wrapper field, however, must use its exact source spelling. Checking
// before encoding/json avoids its case-insensitive matching and null erasure.
func agentTypedJSON(v any, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() == reflect.Interface {
		return nil
	}
	if v == nil {
		return p.ErrInvalid
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return p.ErrInvalid
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			key := strings.Split(f.Tag.Get("json"), ",")[0]
			if key != "" && key != "-" {
				fields[key] = f.Type
			}
		}
		for k, x := range m {
			ft, ok := fields[k]
			if !ok {
				return p.ErrInvalid
			}
			if e := agentTypedJSON(x, ft); e != nil {
				return e
			}
		}
	case reflect.Map:
		m, ok := v.(map[string]any)
		if !ok {
			return p.ErrInvalid
		}
		for _, x := range m {
			if e := agentTypedJSON(x, t.Elem()); e != nil {
				return e
			}
		}
	case reflect.Slice:
		a, ok := v.([]any)
		if !ok {
			return p.ErrInvalid
		}
		for _, x := range a {
			if e := agentTypedJSON(x, t.Elem()); e != nil {
				return e
			}
		}
	case reflect.Float32, reflect.Float64:
		n, ok := v.(json.Number)
		if !ok {
			return p.ErrInvalid
		}
		// Bounded numeric spelling before rational parsing (exponents cannot allocate
		// arbitrary memory). Compare the representable wire decimal, not float bits.
		f, e := n.Float64()
		if e != nil {
			return p.ErrInvalid
		}
		raw, e := json.Marshal(f)
		if e != nil {
			return p.ErrInvalid
		}
		if f == 0 {
			sig := strings.FieldsFunc(string(n), func(r rune) bool { return r == 'e' || r == 'E' })[0]
			if strings.Trim(sig, "-0.") != "" {
				return p.ErrInvalid
			}
			return nil
		}
		a, ok := new(big.Rat).SetString(string(n))
		b, bok := new(big.Rat).SetString(string(raw))
		if !ok || !bok || a.Cmp(b) != 0 {
			return p.ErrInvalid
		}
	default:
		raw, e := json.Marshal(v)
		if e != nil {
			return p.ErrInvalid
		}
		out := reflect.New(t).Interface()
		if json.Unmarshal(raw, out) != nil {
			return p.ErrInvalid
		}
	}
	return nil
}

func agentAliasObject(v any) (map[string]any, error) {
	if s, ok := v.(string); ok {
		if len(s) > 65536 || !json.Valid([]byte(s)) {
			return nil, p.ErrInvalid
		}
		var n yaml.Node
		if yaml.Unmarshal([]byte(s), &n) != nil || len(n.Content) != 1 {
			return nil, p.ErrInvalid
		}
		x, e := agentNodeJSON(n.Content[0], 0)
		if e != nil {
			return nil, e
		}
		v = x
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, p.ErrInvalid
	}
	return m, nil
}
func agentAliases(kind p.ResourceKind, spec map[string]any) error {
	if kind == p.KindEvaluator {
		if v, ok := spec["config"]; ok {
			m, e := agentAliasObject(v)
			if e != nil {
				return e
			}
			spec["config"] = m
		}
	}
	if kind == p.KindHook || kind == p.KindRule {
		if v, ok := spec["match"]; ok {
			m, e := agentAliasObject(v)
			if e != nil {
				return e
			}
			spec["match"] = m
		}
	}
	if kind == p.KindHook {
		if v, ok := spec["blocked_tools"]; ok {
			if _, both := spec["tool_filter"]; both {
				return p.ErrConflict
			}
			spec["tool_filter"] = map[string]any{"blocked_names": v}
			delete(spec, "blocked_tools")
		}
		if v, ok := spec["redact"].([]any); ok {
			spec["redact"] = map[string]any{"patterns": v}
		}
	}
	return nil
}

func (d *AgentObservabilityDecl) UnmarshalYAML(n *yaml.Node) error {
	// Never leave a previously usable retained object after a failed decode.
	*d = AgentObservabilityDecl{}
	v, e := agentNodeJSON(n, 0)
	if e != nil {
		return fmt.Errorf("agent_observability: %w", e)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return p.ErrInvalid
	}
	authored := map[string]p.AuthoredSpec{}
	retain := func(entry any, kind p.ResourceKind, bindings bool) error {
		item, ok := entry.(map[string]any)
		if !ok {
			return p.ErrInvalid
		}
		key, ok := item["key"].(string)
		if !ok || !agentLocalKey.MatchString(key) {
			return p.ErrInvalid
		}
		if _, dup := authored[key]; dup {
			return p.ErrConflict
		}
		spec, ok := item["spec"].(map[string]any)
		if bindings {
			spec = map[string]any{"bindings": item["bindings"]}
			ok = true
		}
		if !ok {
			return p.ErrInvalid
		}
		if e := agentAliases(kind, spec); e != nil {
			return e
		}
		raw, e := json.Marshal(spec)
		if e != nil {
			return p.ErrInvalid
		}
		s, e := p.NewAuthoredSpec(raw)
		if e != nil {
			return e
		}
		authored[key] = s
		return nil
	}
	closures, ok := m["closures"].([]any)
	if !ok {
		return p.ErrInvalid
	}
	closureKeys := map[string]bool{}
	for _, v := range closures {
		c, ok := v.(map[string]any)
		if !ok {
			return p.ErrInvalid
		}
		key, ok := c["key"].(string)
		if !ok || !agentLocalKey.MatchString(key) || closureKeys[key] {
			return p.ErrConflict
		}
		closureKeys[key] = true
		for _, group := range []struct {
			key  string
			kind p.ResourceKind
		}{{"evaluators", p.KindEvaluator}, {"hooks", p.KindHook}, {"rules", p.KindRule}, {"collections", p.KindCollection}, {"actions", p.KindAction}, {"suites", p.KindSuite}} {
			if _, present := c[group.key]; !present {
				continue
			}
			entries, ok := c[group.key].([]any)
			if !ok {
				return p.ErrInvalid
			}
			for _, entry := range entries {
				if e := retain(entry, group.kind, false); e != nil {
					return e
				}
				if group.kind != p.KindSuite {
					continue
				}
				suite := entry.(map[string]any)
				draft, ok := suite["draft"].(map[string]any)
				if !ok {
					return p.ErrInvalid
				}
				if e := retain(draft, p.KindDraft, false); e != nil {
					return e
				}
				if cases, present := draft["cases"]; present {
					list, ok := cases.([]any)
					if !ok {
						return p.ErrInvalid
					}
					for _, cs := range list {
						if e := retain(cs, p.KindCase, false); e != nil {
							return e
						}
					}
				}
				if e := retain(draft["bindings"], p.KindBindings, true); e != nil {
					return e
				}
				if e := retain(draft["publication"], p.KindPublication, false); e != nil {
					return e
				}
			}
		}
	}
	if e := agentTypedJSON(m, reflect.TypeFor[AgentObservabilityDecl]()); e != nil {
		return e
	}
	raw, e := json.Marshal(m)
	if e != nil {
		return p.ErrInvalid
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	var owned AgentObservabilityDecl
	if dec.Decode(&owned) != nil {
		return p.ErrInvalid
	}
	owned.authored = authored
	*d = owned
	return nil
}

// Validate the full document restrictions only for management authoring, leaving
// existing blueprint-only YAML semantics unchanged. No trailing document or
// anchor outside the management block can smuggle additional authoring input.
func validateAgentDocument(data []byte, d *Decl) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var n yaml.Node
	if dec.Decode(&n) != nil || len(n.Content) != 1 {
		return p.ErrInvalid
	}
	present := false
	root := n.Content[0]
	if root.Kind == yaml.MappingNode {
		for i := 0; i < len(root.Content); i += 2 {
			if root.Content[i].Value == "agent_observability" {
				present = true
			}
		}
	}
	if !present {
		return nil
	}
	if d.AgentObservability == nil {
		return p.ErrInvalid
	}
	if _, e := agentNodeJSON(root, 0); e != nil {
		return e
	}
	var extra yaml.Node
	if dec.Decode(&extra) != io.EOF {
		return p.ErrInvalid
	}
	return nil
}

func cloneAgentObservability(d *AgentObservabilityDecl) (*AgentObservabilityDecl, error) {
	if d == nil {
		return nil, nil
	}
	// JSON copies the request views, not the authored evidence. The latter stays
	// immutable and independently retained; no caller can mint it by filling DTOs.
	raw, e := json.Marshal(d)
	if e != nil {
		return nil, p.ErrInvalid
	}
	var out AgentObservabilityDecl
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&out) != nil {
		return nil, p.ErrInvalid
	}
	out.authored = d.AuthoredSpecs()
	return &out, nil
}
