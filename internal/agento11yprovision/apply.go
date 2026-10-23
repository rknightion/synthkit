// SPDX-License-Identifier: AGPL-3.0-only

package agento11yprovision

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const engineSourceRevision = "sigil-bc963fb09-engine-inventory404-v1"
const engineContractRevision = "agento11yprovision-state-3264k-v2"

// All inputs have passed NewPlan's defensive-copy and provenance boundary.
// These values are private typed projections, never caller-selected HTTP bodies.
type enginePlan struct {
	plan  Plan
	specs map[string]engineSpec
}
type engineSpec struct {
	Evaluator     *EvaluatorSpec     `json:"evaluator,omitempty"`
	Hook          *HookRuleSpec      `json:"hook,omitempty"`
	Rule          *RuleSpec          `json:"rule,omitempty"`
	Collection    *CollectionSpec    `json:"collection,omitempty"`
	Action        *RuleActionSpec    `json:"action,omitempty"`
	Suite         *SuiteSpec         `json:"suite,omitempty"`
	Draft         *DraftSpec         `json:"draft,omitempty"`
	Case          *CaseSnapshot      `json:"case,omitempty"`
	Bindings      *[]SuiteBinding    `json:"bindings,omitempty"`
	Publication   *PublicationReview `json:"publication,omitempty"`
	SourceVersion string             `json:"source_version,omitempty"`
}

func enginePtr[T any](v T) *T { return &v }
func engineDigest(binding Binding, domain string, coordinate Coordinate, value any) (Digest, error) {
	raw, e := json.Marshal(struct {
		Binding    Digest
		Coordinate Coordinate
		Value      any
	}{custodyBindingHash(binding), coordinate, value})
	if e != nil {
		return "", ErrInvalid
	}
	sum := sha256.Sum256(append([]byte("agento11yprovision/engine/v1/"+domain+"\n"), raw...))
	return Digest(fmt.Sprintf("%x", sum)), nil
}
func engineEvidence(id string, d Digest) []Evidence { return []Evidence{{ID: id, Digest: d}} }
func engineDecode(s AuthoredSpec, out any) error {
	// NewAuthoredSpec rejects duplicate keys throughout, invalid UTF8 and trailing data.
	if _, e := NewAuthoredSpec(s.JSON()); e != nil {
		return e
	}
	if decodeResourceObject(s.JSON(), out) != nil {
		return ErrInvalid
	}
	return nil
}

// Authored DTOs have exact field spellings. encoding/json's struct matching
// (even with DisallowUnknownFields) accepts case aliases and erases null into
// zero/nil values. Check the raw per-kind boundary before any typed decode.
// These helpers validate only the private authored surface, never observations
// or arbitrary case-owned JSON, and never rewrite the approved bytes.
func engineRawObject(raw []byte, fields string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, ErrInvalid
	}
	allowed := strings.Fields(fields)
	for k, v := range object {
		if !slices.Contains(allowed, k) || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return nil, ErrInvalid
		}
	}
	return object, nil
}

func engineRawArray(raw []byte, element func([]byte) error) error {
	if len(raw) == 0 { // An omitted optional list is not an explicit null.
		return nil
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil || items == nil {
		return ErrInvalid
	}
	for _, item := range items {
		if bytes.Equal(bytes.TrimSpace(item), []byte("null")) {
			return ErrInvalid
		}
		if err := element(item); err != nil {
			return err
		}
	}
	return nil
}

func engineRawString(raw []byte) error {
	var s string
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &s) != nil {
		return ErrInvalid
	}
	return nil
}

func engineRawBinding(raw []byte) error {
	m, err := engineRawObject(raw, "binding_id evaluator_id evaluator_version input_mappings report_role")
	if err != nil {
		return err
	}
	var mappings map[string]json.RawMessage
	if json.Unmarshal(m["input_mappings"], &mappings) != nil || mappings == nil {
		return ErrInvalid
	}
	for _, v := range mappings {
		if err := engineRawString(v); err != nil {
			return err
		}
	}
	return nil
}

func engineRawCase(raw []byte, snapshot bool) error {
	fields := "test_case_id name description tags category input expected metadata artifact_refs"
	if snapshot {
		fields += " suite_id suite_version"
	} else {
		fields += " sanitization_reviewed sanitization_content_digest"
	}
	m, err := engineRawObject(raw, fields)
	if err != nil {
		return err
	}
	if err := engineRawArray(m["tags"], engineRawString); err != nil {
		return err
	}
	// Nonempty artifact lists are still refused by engineCase. Validate their
	// typed shape here without walking input/expected/metadata: those are
	// case-owned JSON, where arbitrary keys, nested nulls and empty containers
	// have meaning and must remain distinct.
	return engineRawArray(m["artifact_refs"], func(raw []byte) error {
		_, err := engineRawObject(raw, "artifact_id name kind")
		return err
	})
}

func engineRawSampleRate(raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var f float64
	if json.Unmarshal(raw, &f) != nil || math.IsInf(f, 0) || f < 0 || f > 1 {
		return ErrInvalid
	}
	// Reject underflow before rational parsing; a zero significand may have
	// an arbitrarily written exponent within the existing authored-byte bound.
	// For a finite nonzero float the exponent is bounded by that significand's
	// length and the float range, so Rat cannot allocate for a huge exponent.
	if f == 0 {
		significand := strings.FieldsFunc(string(raw), func(r rune) bool { return r == 'e' || r == 'E' })[0]
		if strings.Trim(significand, "-0.") != "" {
			return ErrInvalid
		}
		return nil
	}
	wire, err := json.Marshal(f)
	if err != nil {
		return ErrInvalid
	}
	a, ok := new(big.Rat).SetString(string(raw))
	b, wireOK := new(big.Rat).SetString(string(wire))
	if !ok || !wireOK || a.Sign() < 0 || a.Cmp(big.NewRat(1, 1)) > 0 || a.Cmp(b) != 0 {
		return ErrInvalid
	}
	return nil
}

func engineAuthored(kind ResourceKind, raw []byte) error {
	var fields string
	switch kind {
	case KindEvaluator:
		fields = "evaluator_id version kind description config output_keys"
	case KindHook:
		fields = "rule_id enabled phase priority selector match evaluator_ids action_on_fail short_circuit tool_filter redact"
	case KindRule:
		fields = "rule_id enabled selector match sample_rate evaluator_ids execution_mode alert_rule_uids filterable_tag_keys min_idle_seconds"
	case KindCollection:
		fields = "name description"
	case KindAction:
		fields = "condition action_config enabled"
	case KindSuite:
		fields = "suite_id name description tags"
	case KindDraft:
		fields = "changelog"
	case KindCase:
		return engineRawCase(raw, false)
	case KindBindings:
		fields = "bindings"
	case KindPublication:
		fields = "cases bindings"
	default:
		return ErrUnsupported
	}
	m, err := engineRawObject(raw, fields)
	if err != nil {
		return err
	}
	switch kind {
	case KindEvaluator:
		// Config is validated by its kind-specific raw decoder below; it is
		// still lossless map/Number content when passed to those decoders.
		return engineRawArray(m["output_keys"], func(raw []byte) error {
			o, err := engineRawObject(raw, "key type description unit pass_threshold enum min max pass_match pass_value")
			if err != nil {
				return err
			}
			for _, k := range []string{"enum", "pass_match"} {
				if err := engineRawArray(o[k], engineRawString); err != nil {
					return err
				}
			}
			return nil
		})
	case KindRule, KindHook:
		for _, k := range []string{"evaluator_ids", "alert_rule_uids", "filterable_tag_keys"} {
			if err := engineRawArray(m[k], engineRawString); err != nil {
				return err
			}
		}
		if kind == KindRule {
			return engineRawSampleRate(m["sample_rate"])
		}
		if raw, ok := m["tool_filter"]; ok {
			filter, err := engineRawObject(raw, "blocked_names")
			if err != nil {
				return err
			}
			if err := engineRawArray(filter["blocked_names"], engineRawString); err != nil {
				return err
			}
		}
		if raw, ok := m["redact"]; ok {
			redact, err := engineRawObject(raw, "patterns")
			if err != nil {
				return err
			}
			return engineRawArray(redact["patterns"], func(raw []byte) error {
				_, err := engineRawObject(raw, "id regex")
				return err
			})
		}
	case KindAction:
		if _, err := engineRawObject(m["condition"], "kind"); err != nil {
			return err
		}
		action, err := engineRawObject(m["action_config"], "kind collection_ids")
		if err != nil {
			return err
		}
		return engineRawArray(action["collection_ids"], engineRawString)
	case KindSuite:
		return engineRawArray(m["tags"], engineRawString)
	case KindPublication:
		if err := engineRawArray(m["cases"], func(raw []byte) error { return engineRawCase(raw, true) }); err != nil {
			return err
		}
		return engineRawArray(m["bindings"], engineRawBinding)
	case KindBindings:
		return engineRawArray(m["bindings"], engineRawBinding)
	}
	return nil
}

func engineTrimList(in []string, unique bool) ([]string, error) {
	out := []string{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil, ErrInvalid
		}
		if slices.Contains(out, v) {
			if unique {
				return nil, ErrInvalid
			}
			continue
		}
		out = append(out, v)
	}
	return out, nil
}
func engineTags(in []string) []string {
	out := []string{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}
func engineTarget(v string) (string, error) {
	v = strings.TrimSpace(v)
	switch v {
	case "", "response":
		return "", nil
	case "input", "latest_user_message", "system_prompt":
		return v, nil
	}
	return "", ErrUnsupported
}
func engineRegex(config map[string]any) (map[string]any, error) {
	for _, v := range config {
		if v == nil {
			return nil, ErrInvalid
		}
	}
	raw, e := json.Marshal(config)
	if e != nil {
		return nil, ErrInvalid
	}
	var v struct {
		Pattern  *string   `json:"pattern"`
		Patterns *[]string `json:"patterns"`
		Reject   *bool     `json:"reject"`
		Target   *string   `json:"target"`
	}
	if _, e := engineRawObject(raw, "pattern patterns reject target"); e != nil {
		return nil, e
	}
	if decodeResourceObject(raw, &v) != nil {
		return nil, ErrUnsupported
	}
	out := map[string]any{}
	effect := false
	if v.Pattern != nil {
		p := strings.TrimSpace(*v.Pattern)
		if p == "" {
			return nil, ErrInvalid
		}
		if _, e = regexp.Compile(p); e != nil {
			return nil, ErrInvalid
		}
		out["pattern"] = p
		effect = true
	}
	if v.Patterns != nil {
		ps, e := engineTrimList(*v.Patterns, true)
		if e != nil {
			return nil, e
		}
		for _, p := range ps {
			if _, e = regexp.Compile(p); e != nil {
				return nil, ErrInvalid
			}
		}
		out["patterns"] = ps
		effect = effect || len(ps) > 0
	}
	if v.Reject != nil {
		out["reject"] = *v.Reject
	}
	if v.Target != nil {
		target, e := engineTarget(*v.Target)
		if e != nil {
			return nil, e
		}
		if target != "" {
			out["target"] = target
		}
	}
	if !effect {
		return nil, ErrInvalid
	}
	return out, nil
}
func engineHeuristic(config map[string]any) (map[string]any, error) {
	for _, v := range config {
		if v == nil {
			return nil, ErrInvalid
		}
	}
	raw, e := json.Marshal(config)
	if e != nil {
		return nil, ErrInvalid
	}
	var v struct {
		Version string          `json:"version"`
		Target  *string         `json:"target"`
		Root    json.RawMessage `json:"root"`
	}
	if _, e := engineRawObject(raw, "version target root"); e != nil {
		return nil, e
	}
	if decodeResourceObject(raw, &v) != nil || strings.TrimSpace(v.Version) != "v2" {
		return nil, ErrUnsupported
	}
	nodes := 0
	var node func(json.RawMessage, int) (map[string]any, error)
	node = func(raw json.RawMessage, depth int) (map[string]any, error) {
		nodes++
		if nodes > 25 || depth > 3 {
			return nil, ErrCapacity
		}
		if _, e := engineRawObject(raw, "kind operator rules type target value"); e != nil {
			return nil, e
		}
		var n struct {
			Kind     string            `json:"kind"`
			Operator string            `json:"operator"`
			Rules    []json.RawMessage `json:"rules"`
			Type     string            `json:"type"`
			Target   *string           `json:"target"`
			Value    json.RawMessage   `json:"value"`
		}
		if decodeResourceObject(raw, &n) != nil {
			return nil, ErrInvalid
		}
		kind := strings.TrimSpace(n.Kind)
		out := map[string]any{"kind": kind}
		if kind == "group" {
			op := strings.TrimSpace(n.Operator)
			if (op != "and" && op != "or") || len(n.Rules) == 0 || n.Type != "" || n.Target != nil || len(n.Value) != 0 {
				return nil, ErrInvalid
			}
			rs := []any{}
			for _, r := range n.Rules {
				v, e := node(r, depth+1)
				if e != nil {
					return nil, e
				}
				rs = append(rs, v)
			}
			out["operator"] = op
			out["rules"] = rs
			return out, nil
		}
		if kind != "rule" || n.Operator != "" || n.Rules != nil {
			return nil, ErrInvalid
		}
		typ := strings.TrimSpace(n.Type)
		out["type"] = typ
		if n.Target != nil {
			t, e := engineTarget(*n.Target)
			if e != nil {
				return nil, e
			}
			if t != "" {
				out["target"] = t
			}
		}
		switch typ {
		case "not_empty":
			if len(n.Value) > 0 {
				return nil, ErrInvalid
			}
		case "contains", "not_contains":
			var s string
			if json.Unmarshal(n.Value, &s) != nil || strings.TrimSpace(s) == "" {
				return nil, ErrInvalid
			}
			out["value"] = strings.TrimSpace(s)
		case "min_length", "max_length":
			var number json.Number
			if decodeResourceObject(n.Value, &number) != nil {
				return nil, ErrInvalid
			}
			if len(number.String()) > 64 {
				return nil, ErrInvalid
			}
			f, e := strconv.ParseFloat(number.String(), 64)
			if e != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 9007199254740991 || math.Trunc(f) != f {
				return nil, ErrInvalid
			}
			i := int64(f)
			exact, ok := new(big.Rat).SetString(number.String())
			if !ok || exact.Cmp(new(big.Rat).SetInt64(i)) != 0 {
				return nil, ErrInvalid
			}
			out["value"] = i
		default:
			return nil, ErrUnsupported
		}
		return out, nil
	}
	root, e := node(v.Root, 1)
	if e != nil {
		return nil, e
	}
	if root["kind"] != "group" {
		return nil, ErrInvalid
	}
	out := map[string]any{"version": "v2", "root": root}
	if v.Target != nil {
		t, e := engineTarget(*v.Target)
		if e != nil {
			return nil, e
		}
		if t != "" {
			out["target"] = t
		}
	}
	return out, nil
}
func engineEvaluator(v EvaluatorSpec) (EvaluatorSpec, error) {
	v.EvaluatorID = strings.TrimSpace(v.EvaluatorID)
	v.Version = strings.TrimSpace(v.Version)
	v.Kind = strings.TrimSpace(v.Kind)
	v.Description = strings.TrimSpace(v.Description)
	if !validID(v.EvaluatorID) || v.Version == "" || len(v.OutputKeys) != 1 || v.Config == nil {
		return v, ErrInvalid
	}
	o := v.OutputKeys[0]
	o.Key = strings.TrimSpace(o.Key)
	o.Unit = strings.TrimSpace(o.Unit)
	if o.Key == "" || o.Type != "bool" || o.PassThreshold != nil || o.Min != nil || o.Max != nil || len(o.Enum) != 0 || len(o.PassMatch) != 0 {
		return v, ErrUnsupported
	}
	o.Enum = []string{}
	o.PassMatch = []string{}
	v.OutputKeys = []OutputKey{o}
	var e error
	switch v.Kind {
	case "regex":
		v.Config, e = engineRegex(v.Config)
	case "heuristic":
		v.Config, e = engineHeuristic(v.Config)
	default:
		e = ErrUnsupported
	}
	return v, e
}
func engineMatch(in map[string]any, online bool) (map[string]any, error) {
	if in == nil {
		return nil, ErrInvalid
	}
	out := map[string]any{}
	for key, value := range in {
		k := key
		if online {
			k = strings.TrimSpace(k)
		}
		switch {
		case slices.Contains([]string{"agent_name", "agent_version", "operation_name", "model.provider", "model.name", "mode", "error.type", "error.category", "conversation.tool_used", "conversation.tool_used.mode"}, k):
		case strings.HasPrefix(k, "tags.") && strings.TrimSpace(strings.TrimPrefix(k, "tags.")) != "":
			if online {
				k = "tags." + strings.TrimSpace(strings.TrimPrefix(k, "tags."))
			}
		default:
			return nil, ErrUnsupported
		}
		if _, ok := out[k]; ok {
			return nil, ErrInvalid
		}
		vals := []string{}
		switch v := value.(type) {
		case string:
			vals = []string{v}
		case []string:
			vals = append(vals, v...)
		case []any:
			for _, x := range v {
				s, ok := x.(string)
				if !ok {
					return nil, ErrInvalid
				}
				vals = append(vals, s)
			}
		default:
			return nil, ErrInvalid
		}
		if len(vals) == 0 {
			return nil, ErrInvalid
		}
		for i, s := range vals {
			if strings.TrimSpace(s) == "" {
				return nil, ErrInvalid
			}
			if online {
				vals[i] = strings.TrimSpace(s)
			}
			if slices.Contains([]string{"agent_name", "agent_version", "operation_name", "model.provider", "model.name", "conversation.tool_used"}, k) && strings.ContainsAny(vals[i], "*?[") {
				if _, e := path.Match(strings.ToLower(vals[i]), ""); e != nil {
					return nil, ErrInvalid
				}
			}
		}
		if online {
			out[k] = vals
		} else {
			out[k] = value
		}
	}
	if online {
		if raw, ok := out["conversation.tool_used.mode"]; ok {
			vs := raw.([]string)
			if out["conversation.tool_used"] == nil || len(vs) != 1 {
				return nil, ErrInvalid
			}
			switch strings.ToLower(vs[0]) {
			case "any":
				delete(out, "conversation.tool_used.mode")
			case "all":
				out["conversation.tool_used.mode"] = []string{"all"}
			default:
				return nil, ErrInvalid
			}
		}
	}
	return out, nil
}
func engineNames(m map[string]any) ([]string, bool) {
	raw, ok := m["agent_name"]
	if !ok {
		return nil, false
	}
	vals := []string{}
	switch v := raw.(type) {
	case string:
		vals = []string{v}
	case []string:
		vals = v
	case []any:
		for _, x := range v {
			s, ok := x.(string)
			if !ok {
				return nil, false
			}
			vals = append(vals, s)
		}
	default:
		return nil, false
	}
	if len(vals) == 0 {
		return nil, false
	}
	out := []string{}
	for _, v := range vals {
		if !custodyName(v) {
			return nil, false
		}
		out = append(out, strings.ToLower(strings.TrimSpace(v)))
	}
	return out, true
}
func engineHook(v HookRuleSpec) (HookRuleSpec, error) {
	v.RuleID = strings.TrimSpace(v.RuleID)
	if !validID(v.RuleID) {
		return v, ErrInvalid
	}
	if v.Enabled == nil {
		v.Enabled = enginePtr(true)
	}
	if v.Priority == nil {
		v.Priority = enginePtr(0)
	}
	if v.ShortCircuit == nil {
		v.ShortCircuit = enginePtr(true)
	}
	v.Phase = strings.TrimSpace(v.Phase)
	if v.Phase == "" {
		v.Phase = "preflight"
	}
	if v.Phase != "preflight" {
		return v, ErrUnsupported
	}
	if v.Selector == "" {
		v.Selector = "all"
	}
	if !slices.Contains([]string{"all", "user_visible_turn", "all_assistant_generations", "tool_call_steps", "errored_generations"}, v.Selector) {
		return v, ErrUnsupported
	}
	v.ActionOnFail = strings.TrimSpace(v.ActionOnFail)
	if v.ActionOnFail == "" {
		v.ActionOnFail = "deny"
	}
	if v.ActionOnFail != "deny" && v.ActionOnFail != "warn" {
		return v, ErrUnsupported
	}
	var e error
	v.Match, e = engineMatch(v.Match, false)
	if e != nil {
		return v, e
	}
	v.EvaluatorIDs, e = engineTrimList(v.EvaluatorIDs, true)
	if e != nil {
		return v, e
	}
	if v.ToolFilter != nil {
		out := []string{}
		for _, s := range v.ToolFilter.BlockedNames {
			s = strings.TrimSpace(s)
			if s == "" {
				return v, ErrInvalid
			}
			if _, e = path.Match(strings.ToLower(s), ""); e != nil {
				return v, ErrInvalid
			}
			out = append(out, s)
		}
		v.ToolFilter = nil
		if len(out) > 0 {
			v.ToolFilter = &HookToolFilter{BlockedNames: out}
		}
	}
	if v.Redact != nil {
		out := []HookRedactPattern{}
		for _, p := range v.Redact.Patterns {
			p.ID = strings.TrimSpace(p.ID)
			p.Regex = strings.TrimSpace(p.Regex)
			if p.Regex == "" {
				return v, ErrInvalid
			}
			if _, e = regexp.Compile(p.Regex); e != nil {
				return v, ErrInvalid
			}
			out = append(out, p)
		}
		v.Redact = nil
		if len(out) > 0 {
			v.Redact = &HookRedactConfig{Patterns: out}
		}
	}
	if len(v.EvaluatorIDs) == 0 && v.ToolFilter == nil && v.Redact == nil {
		return v, ErrInvalid
	}
	return v, nil
}
func engineRule(v RuleSpec) (RuleSpec, error) {
	v.RuleID = strings.TrimSpace(v.RuleID)
	if !validID(v.RuleID) {
		return v, ErrInvalid
	}
	if v.Enabled == nil {
		v.Enabled = enginePtr(true)
	}
	if v.SampleRate == nil {
		v.SampleRate = enginePtr(0.01)
	}
	if math.IsNaN(*v.SampleRate) || *v.SampleRate < 0 || *v.SampleRate > 1 {
		return v, ErrInvalid
	}
	v.Selector = strings.TrimSpace(v.Selector)
	if v.Selector == "" {
		v.Selector = "user_visible_turn"
	}
	if !slices.Contains([]string{"user_visible_turn", "all_assistant_generations", "tool_call_steps", "errored_generations", "conversation"}, v.Selector) {
		return v, ErrUnsupported
	}
	if (v.Selector == "conversation" && (v.MinIdleSeconds == nil || *v.MinIdleSeconds <= 0)) || (v.Selector != "conversation" && v.MinIdleSeconds != nil) {
		return v, ErrInvalid
	}
	v.ExecutionMode = strings.TrimSpace(v.ExecutionMode)
	if v.ExecutionMode == "" {
		v.ExecutionMode = "parallel"
	}
	if v.ExecutionMode != "parallel" && v.ExecutionMode != "sequential" {
		return v, ErrInvalid
	}
	if v.Match == nil {
		return v, ErrInvalid
	}
	m, e := engineMatch(*v.Match, true)
	if e != nil {
		return v, e
	}
	v.Match = &m
	v.EvaluatorIDs, e = engineTrimList(v.EvaluatorIDs, true)
	if e != nil || len(v.EvaluatorIDs) == 0 || len(v.EvaluatorIDs) > 10 {
		return v, ErrInvalid
	}
	tags := []string{}
	if v.FilterableTagKeys != nil {
		tags = engineTags(*v.FilterableTagKeys)
		if len(tags) > 10 {
			return v, ErrCapacity
		}
		for _, k := range tags {
			if !regexp.MustCompile(`^[a-z_][a-z0-9_]*$`).MatchString(k) {
				return v, ErrUnsupported
			}
		}
	}
	v.FilterableTagKeys = &tags
	alerts := []string{}
	if v.AlertRuleUIDs != nil {
		alerts = append(alerts, (*v.AlertRuleUIDs)...)
	}
	v.AlertRuleUIDs = &alerts
	return v, nil
}
func engineBindings(v []SuiteBinding) ([]SuiteBinding, error) {
	if v == nil || len(v) > 100 || !validBindings(v) {
		return nil, ErrInvalid
	}
	out := []SuiteBinding{}
	primary := 0
	for _, x := range v {
		x = normalizeBinding(x)
		if x.ReportRole != "" && x.ReportRole != "diagnostic" && x.ReportRole != "primary_verdict" {
			return nil, ErrUnsupported
		}
		if x.ReportRole == "primary_verdict" {
			primary++
		}
		if len(x.InputMappings) == 0 || len(x.InputMappings) > 32 {
			return nil, ErrInvalid
		}
		for k, s := range x.InputMappings {
			// Pin: llm_judge_context.go built-in names/ASCII aliases. The dot
			// and hyphen aliases are already excluded by the identifier grammar.
			if slices.Contains([]string{"generation_id", "conversation_id", "system_prompt", "conversation_transcript", "latest_user_message", "input", "user_history", "agent_response", "assistant_response", "output", "agent_thinking", "assistant_thinking", "agent_sequence", "assistant_sequence", "tool_calls", "tool_results", "tools", "stop_reason", "call_error", "error", "turn_transcript", "turn_agent_sequence", "turn_tool_calls", "turn_tool_results"}, k) {
				return nil, ErrUnsupported
			}
			if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(k) || !regexp.MustCompile(`^test_case\.(input|expected|metadata)(\.[A-Za-z_][A-Za-z0-9_]*)+$`).MatchString(s) {
				return nil, ErrUnsupported
			}
		}
		out = append(out, x)
	}
	if primary > 1 {
		return nil, ErrInvalid
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BindingID < out[j].BindingID })
	return out, nil
}

// Only case-owned JSON is walked; this is numeric roundtrip validation, not a
// generic public codec or a source of request bodies for other resource kinds.
func engineCaseValue(v any) (any, error) {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, v := range x {
			n, e := engineCaseValue(v)
			if e != nil {
				return nil, e
			}
			out[k] = n
		}
		return out, nil
	case []any:
		out := []any{}
		for _, v := range x {
			n, e := engineCaseValue(v)
			if e != nil {
				return nil, e
			}
			out = append(out, n)
		}
		return out, nil
	case json.Number:
		if len(x.String()) > 64 {
			return nil, ErrUnsupported
		}
		f, e := strconv.ParseFloat(x.String(), 64)
		if e != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, ErrUnsupported
		}
		raw, e := json.Marshal(f)
		if e != nil {
			return nil, ErrInvalid
		}
		a, ok := new(big.Rat).SetString(x.String())
		if !ok {
			return nil, ErrInvalid
		}
		b, ok := new(big.Rat).SetString(string(raw))
		if !ok || a.Cmp(b) != 0 {
			return nil, ErrUnsupported
		}
		return json.Number(raw), nil
	case nil, bool, string:
		return x, nil
	default:
		return nil, ErrUnsupported
	}
}
func engineCase(v CaseSnapshot) (CaseSnapshot, error) {
	v.TestCaseID = strings.TrimSpace(v.TestCaseID)
	v.Name = strings.TrimSpace(v.Name)
	v.Description = strings.TrimSpace(v.Description)
	v.Category = strings.TrimSpace(v.Category)
	v.Tags = engineTags(v.Tags)
	if len(v.Tags) == 0 {
		v.Tags = nil
	}
	if !validID(v.TestCaseID) || len(v.Input) == 0 || len(v.ArtifactRefs) > 0 {
		return v, ErrInvalid
	}
	v.ArtifactRefs = nil
	for _, m := range []*map[string]any{&v.Input, &v.Expected, &v.Metadata} {
		if len(*m) == 0 {
			if m != &v.Input {
				*m = nil
			}
			continue
		}
		x, e := engineCaseValue(*m)
		if e != nil {
			return v, e
		}
		*m = x.(map[string]any)
	}
	return v, nil
}
func prepareEngine(input PlanInput) (enginePlan, error) {
	if input.Binding.ContractRevision != engineContractRevision || input.Binding.SourceRevision != engineSourceRevision {
		return enginePlan{}, ErrBinding
	}
	p, e := NewPlan(input)
	if e != nil {
		return enginePlan{}, e
	}
	ep := enginePlan{plan: p, specs: map[string]engineSpec{}}
	for _, r := range p.input.Resources {
		if err := engineAuthored(r.Coordinate.Kind, r.Spec.JSON()); err != nil {
			return enginePlan{}, err
		}
		s := engineSpec{}
		var err error
		switch r.Coordinate.Kind {
		case KindEvaluator:
			var v EvaluatorSpec
			if err = engineDecode(r.Spec, &v); err == nil {
				v, err = engineEvaluator(v)
				if v.EvaluatorID != r.Coordinate.ID || v.Version != r.Coordinate.Version {
					err = ErrConflict
				}
				s.Evaluator = &v
			}
		case KindHook:
			var v HookRuleSpec
			if err = engineDecode(r.Spec, &v); err == nil {
				v, err = engineHook(v)
				if v.RuleID != r.Coordinate.ID {
					err = ErrConflict
				}
				s.Hook = &v
			}
		case KindRule:
			var v RuleSpec
			if err = engineDecode(r.Spec, &v); err == nil {
				v, err = engineRule(v)
				if v.RuleID != r.Coordinate.ID {
					err = ErrConflict
				}
				s.Rule = &v
			}
		case KindCollection:
			var v CollectionSpec
			if err = engineDecode(r.Spec, &v); err == nil {
				v.Name = strings.TrimSpace(v.Name)
				d := ""
				if v.Description != nil {
					d = strings.TrimSpace(*v.Description)
				}
				v.Description = &d
				if v.Name == "" {
					err = ErrInvalid
				}
				s.Collection = &v
			}
		case KindAction:
			var v RuleActionSpec
			if err = engineDecode(r.Spec, &v); err == nil {
				if v.Enabled == nil {
					v.Enabled = enginePtr(true)
				}
				if !validAction(v.Condition, v.ActionConfig) {
					err = ErrUnsupported
				}
				s.Action = &v
			}
		case KindSuite:
			var v SuiteSpec
			if err = engineDecode(r.Spec, &v); err == nil {
				v.SuiteID = strings.TrimSpace(v.SuiteID)
				v.Name = strings.TrimSpace(v.Name)
				d := ""
				if v.Description != nil {
					d = strings.TrimSpace(*v.Description)
				}
				v.Description = &d
				tags := []string{}
				if v.Tags != nil {
					tags = engineTags(*v.Tags)
				}
				v.Tags = &tags
				if v.SuiteID != r.Coordinate.ID || v.Name == "" {
					err = ErrConflict
				}
				s.Suite = &v
			}
		case KindDraft:
			var v DraftSpec
			if err = engineDecode(r.Spec, &v); err == nil {
				ch := ""
				if v.Changelog != nil {
					ch = strings.TrimSpace(*v.Changelog)
				}
				v.Changelog = &ch
				s.Draft = &v
			}
		case KindCase:
			var v CaseSpec
			if err = engineDecode(r.Spec, &v); err == nil {
				c := CaseSnapshot{TestCaseID: v.TestCaseID, Input: v.Input}
				if v.Name != nil {
					c.Name = *v.Name
				}
				if v.Description != nil {
					c.Description = *v.Description
				}
				if v.Category != nil {
					c.Category = *v.Category
				}
				if v.Tags != nil {
					c.Tags = *v.Tags
				}
				if v.Expected != nil {
					c.Expected = *v.Expected
				}
				if v.Metadata != nil {
					c.Metadata = *v.Metadata
				}
				if v.ArtifactRefs != nil {
					c.ArtifactRefs = *v.ArtifactRefs
				}
				if v.SanitizationReviewed != nil && *v.SanitizationReviewed || v.SanitizationContentDigest != nil && *v.SanitizationContentDigest != "" {
					err = ErrUnsupported
				}
				for _, k := range []string{"sigil.sanitization", "sigil.checkpoint_match", "sigil.provenance"} {
					if _, ok := c.Metadata[k]; ok {
						err = ErrInvalid
					}
				}
				if err == nil {
					c, err = engineCase(c)
				}
				if c.TestCaseID != r.Coordinate.ID {
					err = ErrConflict
				}
				s.Case = &c
			}
		case KindBindings:
			var v struct {
				Bindings []SuiteBinding `json:"bindings"`
			}
			if err = engineDecode(r.Spec, &v); err == nil {
				v.Bindings, err = engineBindings(v.Bindings)
				s.Bindings = &v.Bindings
			}
		case KindPublication:
			var v PublicationReview
			if err = engineDecode(r.Spec, &v); err == nil {
				if v.Cases == nil || v.Bindings == nil {
					err = ErrInvalid
				}
				for i, c := range v.Cases {
					if c.SuiteID != "" && c.SuiteID != r.Coordinate.ParentID || c.SuiteVersion != "" {
						err = ErrConflict
					}
					c.SuiteID = ""
					c.SuiteVersion = ""
					v.Cases[i], e = engineCase(c)
					if e != nil {
						err = e
					}
				}
				if err == nil {
					v.Bindings, err = engineBindings(v.Bindings)
				}
				sort.Slice(v.Cases, func(i, j int) bool { return v.Cases[i].TestCaseID < v.Cases[j].TestCaseID })
				s.Publication = &v
			}
		default:
			err = ErrUnsupported
		}
		if err != nil {
			return enginePlan{}, err
		}
		if s.Hook != nil || s.Rule != nil {
			m := map[string]any{}
			if s.Hook != nil {
				m = s.Hook.Match
			} else {
				m = *s.Rule.Match
			}
			ns, ok := engineNames(m)
			if !ok {
				return enginePlan{}, ErrInvalid
			}
			for _, n := range ns {
				if !slices.Contains(r.Namespaces, n) {
					return enginePlan{}, ErrConflict
				}
			}
		}
		ep.specs[r.Key] = s
	}
	// References are local keys of the exact declared dependency, not remote IDs.
	for _, r := range p.input.Resources {
		s := ep.specs[r.Key]
		check := func(key string, kind ResourceKind) bool {
			for _, d := range p.input.Resources {
				if d.Key == key && d.Coordinate.Kind == kind && slices.Contains(r.Dependencies, key) {
					return true
				}
			}
			return false
		}
		ids := []string{}
		if s.Rule != nil {
			ids = s.Rule.EvaluatorIDs
		}
		if s.Hook != nil {
			ids = s.Hook.EvaluatorIDs
		}
		for _, key := range ids {
			if !check(key, KindEvaluator) {
				return enginePlan{}, ErrConflict
			}
		}
		if s.Action != nil {
			for _, key := range s.Action.ActionConfig.CollectionIDs {
				if !check(key, KindCollection) {
					return enginePlan{}, ErrConflict
				}
			}
		}
		bs := []SuiteBinding{}
		if s.Bindings != nil {
			bs = *s.Bindings
		}
		if s.Publication != nil {
			bs = s.Publication.Bindings
		}
		for _, b := range bs {
			if !check(b.EvaluatorID, KindEvaluator) {
				return enginePlan{}, ErrConflict
			}
			for _, d := range p.input.Resources {
				if d.Key == b.EvaluatorID && d.Coordinate.Version != b.EvaluatorVersion {
					return enginePlan{}, ErrConflict
				}
			}
		}
	}
	if p.input.Mode == ModeApply {
		last := -1
		for _, step := range p.input.Steps {
			r := planResource(p.input, step.ResourceKey)
			phase := enginePhase(r.Coordinate.Kind)
			if phase < last {
				return enginePlan{}, ErrConflict
			}
			last = phase
		}
	}
	for _, r := range p.input.Resources {
		s := ep.specs[r.Key]
		if s.Publication == nil {
			continue
		}
		want := PublicationReview{Cases: []CaseSnapshot{}, Bindings: []SuiteBinding{}}
		for _, other := range p.input.Resources {
			if other.Coordinate.ParentID != r.Coordinate.ParentID {
				continue
			}
			os := ep.specs[other.Key]
			if os.Case != nil {
				if !slices.Contains(r.Dependencies, other.Key) {
					return enginePlan{}, ErrConflict
				}
				want.Cases = append(want.Cases, *os.Case)
			}
			if os.Bindings != nil {
				if !slices.Contains(r.Dependencies, other.Key) {
					return enginePlan{}, ErrConflict
				}
				want.Bindings = append(want.Bindings, (*os.Bindings)...)
			}
		}
		sort.Slice(want.Cases, func(i, j int) bool { return want.Cases[i].TestCaseID < want.Cases[j].TestCaseID })
		sort.Slice(want.Bindings, func(i, j int) bool { return want.Bindings[i].BindingID < want.Bindings[j].BindingID })
		if !engineEqual(want, *s.Publication) {
			return enginePlan{}, ErrConflict
		}
	}
	return ep, nil
}

func enginePhase(k ResourceKind) int {
	switch k {
	case KindEvaluator:
		return 0
	case KindCollection:
		return 1
	case KindSuite:
		return 2
	case KindDraft:
		return 3
	case KindCase:
		return 4
	case KindBindings:
		return 5
	case KindPublication:
		return 6
	case KindHook, KindRule:
		return 7
	case KindAction:
		return 8
	}
	return -1
}
func engineEqual(a, b any) bool {
	aa, e := json.Marshal(a)
	bb, f := json.Marshal(b)
	return e == nil && f == nil && bytes.Equal(aa, bb)
}
func engineRecord(s Snapshot, key string) *ResourceRecord {
	if s.Document != nil {
		for i := range s.Document.Resources {
			if s.Document.Resources[i].Key == key {
				return &s.Document.Resources[i]
			}
		}
	}
	return nil
}
func engineConstruction(s Snapshot, id string) *Construction {
	if s.Document != nil {
		for i := range s.Document.Constructions {
			if s.Document.Constructions[i].ID == id {
				return &s.Document.Constructions[i]
			}
		}
	}
	return nil
}
func engineCoordinate(p enginePlan, r PlannedResource, s Snapshot) (Coordinate, error) {
	co := r.Coordinate
	if record := engineRecord(s, r.Key); record != nil {
		co = record.Coordinate
	}
	if r.VersionFrom != "" && co.Version == "" {
		d := engineRecord(s, r.VersionFrom)
		if d == nil || d.Status != ResourceOwned || d.Coordinate.Version == "" {
			return co, ErrConflict
		}
		co.Version = d.Coordinate.Version
	}
	return co, nil
}
func engineResolved(p enginePlan, r PlannedResource, s Snapshot) (engineSpec, error) {
	raw, e := json.Marshal(p.specs[r.Key])
	if e != nil {
		return engineSpec{}, ErrInvalid
	}
	var v engineSpec
	if decodeResourceObject(raw, &v) != nil {
		return v, ErrInvalid
	}
	ref := func(key string, kind ResourceKind) (*ResourceRecord, error) {
		d := engineRecord(s, key)
		if d == nil || d.Coordinate.Kind != kind || (p.plan.input.Mode == ModeApply && d.Status != ResourceOwned) || (p.plan.input.Mode != ModeApply && len(d.Acquisition) == 0) || !slices.Contains(r.Dependencies, key) {
			return nil, ErrConflict
		}
		return d, nil
	}
	ids := []string{}
	if v.Rule != nil {
		ids = v.Rule.EvaluatorIDs
	}
	if v.Hook != nil {
		ids = v.Hook.EvaluatorIDs
	}
	for i, key := range ids {
		d, e := ref(key, KindEvaluator)
		if e != nil {
			return v, e
		}
		ids[i] = d.Coordinate.ID
	}
	if v.Rule != nil {
		v.Rule.EvaluatorIDs = ids
	}
	if v.Hook != nil {
		v.Hook.EvaluatorIDs = ids
	}
	if v.Action != nil {
		for i, key := range v.Action.ActionConfig.CollectionIDs {
			d, e := ref(key, KindCollection)
			if e != nil {
				return v, e
			}
			v.Action.ActionConfig.CollectionIDs[i] = d.Coordinate.ID
		}
	}
	resolve := func(bs []SuiteBinding) ([]SuiteBinding, error) {
		for i, b := range bs {
			d, e := ref(b.EvaluatorID, KindEvaluator)
			if e != nil || d.Coordinate.Version != b.EvaluatorVersion {
				return nil, ErrConflict
			}
			bs[i].EvaluatorID = d.Coordinate.ID
		}
		return engineBindings(bs)
	}
	if v.Bindings != nil {
		bs, e := resolve(*v.Bindings)
		if e != nil {
			return v, e
		}
		v.Bindings = &bs
	}
	if v.Publication != nil {
		v.Publication.Bindings, e = resolve(v.Publication.Bindings)
		if e != nil {
			return v, e
		}
	}
	return v, nil
}

type engineEntity struct {
	Coordinate Coordinate
	Spec       engineSpec
	Hash       Digest
}
type engineCheckpoint struct {
	Suite    Suite
	Cases    map[string][]SuiteCase
	Bindings map[string][]SuiteBinding
}
type engineInventory struct {
	Evaluators  []Evaluator
	Hooks       []HookRule
	Rules       []Rule
	Collections []Collection
	Suites      []Suite
	Actions     map[string][]RuleAction
	Checkpoints map[string]engineCheckpoint
	Entities    map[string]engineEntity
	Digest      Digest
}

func engineKey(c Coordinate) string { raw, _ := json.Marshal(c); return string(raw) }
func engineClient(p enginePlan, c *Client) error {
	b := p.plan.input.Binding
	if b.ContractRevision != engineContractRevision || b.SourceRevision != engineSourceRevision || c == nil || c.base != b.Destination.GrafanaOrigin+b.Destination.DeploymentPath+b.Destination.PluginRoute || c.tenant != b.Destination.EffectiveTenant {
		return ErrBinding
	}
	return nil
}
func engineProject(binding Binding, co Coordinate, value any) (engineEntity, error) {
	v := engineSpec{}
	var e error
	switch x := value.(type) {
	case Evaluator:
		s, e2 := engineEvaluator(x.EvaluatorSpec)
		e = e2
		v.Evaluator = &s
	case HookRule:
		s, e2 := engineHook(HookRuleSpec{RuleID: x.RuleID, Enabled: enginePtr(x.Enabled), Phase: x.Phase, Priority: enginePtr(x.Priority), Selector: x.Selector, Match: x.Match, EvaluatorIDs: x.EvaluatorIDs, ActionOnFail: x.ActionOnFail, ShortCircuit: enginePtr(x.ShortCircuit), ToolFilter: x.ToolFilter, Redact: x.Redact})
		e = e2
		v.Hook = &s
	case Rule:
		s, e2 := engineRule(RuleSpec{RuleID: x.RuleID, Enabled: enginePtr(x.Enabled), Selector: x.Selector, Match: enginePtr(x.Match), SampleRate: enginePtr(x.SampleRate), EvaluatorIDs: x.EvaluatorIDs, ExecutionMode: x.ExecutionMode, AlertRuleUIDs: enginePtr(x.AlertRuleUIDs), FilterableTagKeys: enginePtr(x.FilterableTagKeys), MinIdleSeconds: x.MinIdleSeconds})
		e = e2
		v.Rule = &s
	case Collection:
		v.Collection = &CollectionSpec{Name: x.Name, Description: enginePtr(x.Description)}
	case RuleAction:
		if !validAction(x.Condition, x.ActionConfig) {
			e = ErrUnsupported
		}
		v.Action = &RuleActionSpec{Condition: x.Condition, ActionConfig: x.ActionConfig, Enabled: enginePtr(x.Enabled)}
	case Suite:
		tags := engineTags(x.Tags)
		v.Suite = &SuiteSpec{SuiteID: x.SuiteID, Name: x.Name, Description: enginePtr(x.Description), Tags: &tags}
	case SuiteVersion:
		v.Draft = &DraftSpec{Changelog: enginePtr(x.Changelog)}
		v.SourceVersion = x.SourceVersion
	case SuiteCase:
		snapshot := x.CaseSnapshot
		metadata := map[string]any{}
		for k, val := range snapshot.Metadata {
			metadata[k] = val
		}
		snapshot.Metadata = metadata
		evidence, ok := metadata["sigil.sanitization"].(map[string]any)
		if !ok || evidence["policy"] != "secrets-email.v2" || evidence["requires_review"] != false {
			return engineEntity{}, ErrConflict
		}
		for _, k := range []string{"sigil.provenance", "sigil.checkpoint_match"} {
			if _, ok := metadata[k]; ok {
				return engineEntity{}, ErrUnsupported
			}
		}
		delete(metadata, "sigil.sanitization")
		snapshot.SuiteID = ""
		snapshot.SuiteVersion = ""
		snapshot, e = engineCase(snapshot)
		if e != nil {
			return engineEntity{}, e
		}
		content := map[string]any{"name": snapshot.Name, "description": snapshot.Description, "category": snapshot.Category, "tags": snapshot.Tags, "input": snapshot.Input, "expected": snapshot.Expected, "metadata": snapshot.Metadata}
		raw, err := json.Marshal(content)
		if err != nil {
			return engineEntity{}, ErrInvalid
		}
		if evidence["content_digest"] != fmt.Sprintf("sha256:%x", sha256.Sum256(raw)) {
			return engineEntity{}, ErrConflict
		}
		paths, ok := evidence["changed_paths"].([]any)
		if !ok || len(paths) != 0 {
			return engineEntity{}, ErrConflict
		}
		v.Case = &snapshot
	case []SuiteBinding:
		bs, e2 := engineBindings(x)
		e = e2
		v.Bindings = &bs
	case engineSpec:
		v = x
	default:
		return engineEntity{}, ErrUnsupported
	}
	if e != nil {
		return engineEntity{}, e
	}
	h, e := engineDigest(binding, "managed", co, v)
	return engineEntity{co, v, h}, e
}
func engineInventoryRead(ctx context.Context, c *Client, p enginePlan, s Snapshot) (engineInventory, error) {
	inv := engineInventory{Actions: map[string][]RuleAction{}, Checkpoints: map[string]engineCheckpoint{}, Entities: map[string]engineEntity{}}
	if e := engineClient(p, c); e != nil {
		return inv, e
	}
	if e := ctx.Err(); e != nil {
		return inv, e
	}
	var e error
	if inv.Evaluators, e = c.ListEvaluators(ctx); e != nil {
		return inv, e
	}
	if inv.Hooks, e = c.ListHookRules(ctx); e != nil {
		return inv, e
	}
	if inv.Rules, e = c.ListRules(ctx); e != nil {
		return inv, e
	}
	if inv.Collections, e = c.ListCollections(ctx); e != nil {
		return inv, e
	}
	if inv.Suites, e = c.ListSuites(ctx); e != nil {
		return inv, e
	}
	add := func(co Coordinate, value any) error {
		entity, e := engineProject(p.plan.input.Binding, co, value)
		if e != nil {
			return e
		}
		inv.Entities[engineKey(co)] = entity
		return nil
	}
	for _, v := range inv.Evaluators {
		if e = add(Coordinate{Kind: KindEvaluator, ID: v.EvaluatorID, Version: v.Version}, v); e != nil {
			return inv, e
		}
	}
	for _, v := range inv.Hooks {
		if e = add(Coordinate{Kind: KindHook, ID: v.RuleID}, v); e != nil {
			return inv, e
		}
	}
	actionDigests := map[string]Digest{}
	for _, v := range inv.Rules {
		if e = add(Coordinate{Kind: KindRule, ID: v.RuleID}, v); e != nil {
			return inv, e
		}
		actions, err := c.ListRuleActions(ctx, v.RuleID)
		if err != nil {
			return inv, err
		}
		entries := []engineEntity{}
		tracked := false
		for _, r := range p.plan.input.Resources {
			if r.Coordinate.ID == v.RuleID && r.Coordinate.Kind == KindRule {
				tracked = true
			}
		}
		for _, a := range actions {
			co := Coordinate{Kind: KindAction, ID: a.ActionID, ParentID: a.RuleID}
			entity, err := engineProject(p.plan.input.Binding, co, a)
			if err != nil {
				return inv, err
			}
			entries = append(entries, entity)
			if tracked {
				inv.Entities[engineKey(co)] = entity
			}
		}
		sort.Slice(entries, func(i, j int) bool { return engineKey(entries[i].Coordinate) < engineKey(entries[j].Coordinate) })
		actionDigests[v.RuleID], e = engineDigest(p.plan.input.Binding, "actions", Coordinate{Kind: KindRule, ID: v.RuleID}, entries)
		if e != nil {
			return inv, e
		}
		if tracked {
			inv.Actions[v.RuleID] = actions
		}
	}
	for _, r := range p.plan.input.Resources {
		record := engineRecord(s, r.Key)
		if r.Coordinate.Kind != KindAction || record == nil || record.Coordinate.Unassigned || len(record.Acquisition) == 0 {
			continue
		}
		parent := record.Coordinate.ParentID
		if _, ok := inv.Actions[parent]; ok {
			continue
		}
		actions, err := c.ListRuleActions(ctx, parent)
		if err != nil {
			return inv, err
		}
		inv.Actions[parent] = actions
		entries := []engineEntity{}
		for _, a := range actions {
			co := Coordinate{Kind: KindAction, ID: a.ActionID, ParentID: a.RuleID}
			if e = add(co, a); e != nil {
				return inv, e
			}
			entries = append(entries, inv.Entities[engineKey(co)])
		}
		sort.Slice(entries, func(i, j int) bool { return engineKey(entries[i].Coordinate) < engineKey(entries[j].Coordinate) })
		actionDigests[parent], e = engineDigest(p.plan.input.Binding, "actions", Coordinate{Kind: KindRule, ID: parent}, entries)
		if e != nil {
			return inv, e
		}
	}
	for _, v := range inv.Collections {
		if e = add(Coordinate{Kind: KindCollection, ID: v.CollectionID}, v); e != nil {
			return inv, e
		}
	}
	for _, v := range inv.Suites {
		if e = add(Coordinate{Kind: KindSuite, ID: v.SuiteID}, v); e != nil {
			return inv, e
		}
		tracked := false
		for _, r := range p.plan.input.Resources {
			if r.Coordinate.Kind == KindSuite && r.Coordinate.ID == v.SuiteID {
				tracked = true
			}
		}
		if !tracked {
			continue
		}
		suite, err := c.GetSuite(ctx, v.SuiteID)
		if err != nil {
			return inv, err
		}
		cp := engineCheckpoint{Suite: suite, Cases: map[string][]SuiteCase{}, Bindings: map[string][]SuiteBinding{}}
		for _, version := range suite.Versions {
			cases, err := c.ListCases(ctx, v.SuiteID, version.Version)
			if err != nil {
				return inv, err
			}
			bs, err := c.GetBindings(ctx, v.SuiteID, version.Version)
			if err != nil {
				return inv, err
			}
			cp.Cases[version.Version] = cases
			cp.Bindings[version.Version] = bs
			for _, cs := range cases {
				if e = add(Coordinate{Kind: KindCase, ID: cs.TestCaseID, ParentID: v.SuiteID, Version: version.Version}, cs); e != nil {
					return inv, e
				}
			}
		}
		inv.Checkpoints[v.SuiteID] = cp
	}
	entries := []engineEntity{}
	for _, v := range inv.Entities {
		entries = append(entries, v)
	}
	sort.Slice(entries, func(i, j int) bool { return engineKey(entries[i].Coordinate) < engineKey(entries[j].Coordinate) })
	// Checkpoint metadata is projected explicitly: actors/timestamps are volatile.
	type phase struct {
		Suite             string
		Version           string
		Count             int
		Published         bool
		Source, Changelog string
		Bindings          []SuiteBinding
		CaseIDs           []string
	}
	phases := []phase{}
	for id, cp := range inv.Checkpoints {
		for _, v := range cp.Suite.Versions {
			bs, e := engineBindings(cp.Bindings[v.Version])
			if e != nil {
				return inv, e
			}
			ids := []string{}
			for _, cs := range cp.Cases[v.Version] {
				ids = append(ids, cs.TestCaseID)
			}
			sort.Strings(ids)
			phases = append(phases, phase{id, v.Version, v.TestCaseCount, v.Published, v.SourceVersion, v.Changelog, bs, ids})
		}
	}
	sort.Slice(phases, func(i, j int) bool {
		if phases[i].Suite == phases[j].Suite {
			return phases[i].Version < phases[j].Version
		}
		return phases[i].Suite < phases[j].Suite
	})
	inv.Digest, e = engineDigest(p.plan.input.Binding, "inventory", Coordinate{}, struct {
		Entities []engineEntity
		Actions  map[string]Digest
		Phases   []phase
	}{entries, actionDigests, phases})
	if e != nil {
		return inv, e
	}
	if e = ctx.Err(); e != nil {
		return inv, e
	}
	return inv, nil
}
func engineVerifyPhases(p enginePlan, s Snapshot, inv engineInventory, current string, recovered ...Coordinate) error {
	active := func(key string) bool {
		r := engineRecord(s, key)
		return r != nil && (r.Status == ResourceOwned || r.Status == ResourceDisabled || r.Status == ResourceRetained || key == current)
	}
	for _, r := range p.plan.input.Resources {
		if r.Coordinate.Kind != KindSuite || !active(r.Key) {
			continue
		}
		if current == "" && s.Document != nil && s.Document.Pending != nil {
			pending := s.Document.Pending
			original := planResource(p.plan.input, pending.ResourceKey)
			if original != nil && (original.Coordinate.ParentID == r.Coordinate.ID || original.Coordinate.Kind == KindSuite && original.Coordinate.ID == r.Coordinate.ID) {
				continue
			}
		}
		cp, ok := inv.Checkpoints[r.Coordinate.ID]
		if !ok {
			return ErrConflict
		}
		var draft *PlannedResource
		for i := range p.plan.input.Resources {
			x := &p.plan.input.Resources[i]
			if x.Coordinate.Kind == KindDraft && x.Coordinate.ParentID == r.Coordinate.ID && active(x.Key) {
				draft = x
			}
		}
		if draft == nil {
			if len(cp.Suite.Versions) != 0 || cp.Suite.LatestVersion != "" {
				return ErrConflict
			}
			continue
		}
		co, e := engineCoordinate(p, *draft, s)
		if draft.Key == current && co.Version == "" && len(recovered) == 1 {
			co = recovered[0]
		}
		if e != nil || co.Version == "" || len(cp.Suite.Versions) != 1 {
			return ErrConflict
		}
		v := cp.Suite.Versions[0]
		if v.Version != co.Version || v.SourceVersion != "" || v.Changelog != *p.specs[draft.Key].Draft.Changelog {
			return ErrConflict
		}
		expectedCases := map[string]bool{}
		expectedBindings := []SuiteBinding{}
		published := false
		for _, child := range p.plan.input.Resources {
			if child.Coordinate.ParentID != r.Coordinate.ID || !active(child.Key) {
				continue
			}
			switch child.Coordinate.Kind {
			case KindCase:
				expectedCases[child.Coordinate.ID] = true
			case KindBindings:
				resolved, e := engineResolved(p, child, s)
				if e != nil {
					return e
				}
				expectedBindings = *resolved.Bindings
			case KindPublication:
				published = true
			}
		}
		if v.Published != published || v.TestCaseCount != len(expectedCases) || len(cp.Cases[co.Version]) != len(expectedCases) {
			return ErrConflict
		}
		for _, cs := range cp.Cases[co.Version] {
			if !expectedCases[cs.TestCaseID] {
				return ErrConflict
			}
		}
		bs, e := engineBindings(cp.Bindings[co.Version])
		if e != nil || !engineEqual(bs, expectedBindings) {
			return ErrConflict
		}
		listed, e := engineBindings(append([]SuiteBinding{}, v.Bindings...))
		if e != nil || !engineEqual(listed, bs) {
			return ErrConflict
		}
		latest := ""
		if published {
			latest = co.Version
		}
		if cp.Suite.LatestVersion != latest {
			return ErrConflict
		}
	}
	return nil
}
func engineForeign(ctx context.Context, c *Client, p enginePlan, s Snapshot, inv engineInventory, approval *Approval) error {
	owned := func(co Coordinate) bool {
		if s.Document != nil {
			for _, r := range s.Document.Resources {
				if r.Coordinate == co && len(r.Acquisition) > 0 && r.Status != ResourceRemoved {
					return true
				}
			}
			if approval != nil && approval.document.Mode == ModeRecover && approval.document.Recovery != nil && s.Document.Pending != nil {
				rec := approval.document.Recovery
				if rec.Coordinate == co && rec.AttemptID == s.Document.Pending.AttemptID && len(rec.Attribution) > 0 {
					return true
				}
			}
		}
		return false
	}
	check := func(co Coordinate, m map[string]any, ids []string) error {
		if owned(co) {
			return nil
		}
		for _, id := range ids {
			v, e := c.GetEvaluator(ctx, id)
			if e != nil {
				return e
			}
			entity, e := engineProject(p.plan.input.Binding, Coordinate{Kind: KindEvaluator, ID: v.EvaluatorID, Version: v.Version}, v)
			if e != nil {
				return e
			}
			listed, ok := inv.Entities[engineKey(entity.Coordinate)]
			if !ok || listed.Hash != entity.Hash {
				return ErrConflict
			}
		}
		names, literal := engineNames(m)
		overlap := !literal
		for _, n := range names {
			if slices.Contains(p.plan.names, n) {
				overlap = true
			}
		}
		if !overlap {
			return nil
		}
		if approval == nil {
			return nil
		}
		hash := inv.Entities[engineKey(co)].Hash
		for _, coverage := range approval.document.Assertions.Foreign {
			if coverage.Coordinate == co && coverage.ManagedHash == hash && coverage.Reason == "controlled_real_only" && len(coverage.Evidence) > 0 {
				return nil
			}
		}
		return ErrApproval
	}
	for _, v := range inv.Hooks {
		if v.Enabled {
			if e := check(Coordinate{Kind: KindHook, ID: v.RuleID}, v.Match, v.EvaluatorIDs); e != nil {
				return e
			}
		}
	}
	for _, v := range inv.Rules {
		if v.Enabled {
			if e := check(Coordinate{Kind: KindRule, ID: v.RuleID}, v.Match, v.EvaluatorIDs); e != nil {
				return e
			}
		}
	}
	return nil
}
func engineVerifyPrefix(ctx context.Context, c *Client, p enginePlan, s Snapshot, inv engineInventory, current string) error {
	for _, r := range p.plan.input.Resources {
		record := engineRecord(s, r.Key)
		if record == nil || record.Status != ResourceOwned && r.Key != current {
			continue
		}
		co, e := engineCoordinate(p, r, s)
		if e != nil {
			return e
		}
		actual, _, e := engineReadExact(ctx, c, p, inv, co)
		if e != nil {
			return fmt.Errorf("%w: verify prefix exact read: %w", ErrConflict, e)
		}
		if actual.Presence != PresencePresent {
			return ErrConflict
		}
		expected, e := engineResolved(p, r, s)
		if e != nil {
			return e
		}
		hash, e := engineDigest(p.plan.input.Binding, "managed", co, expected)
		if e != nil || hash != actual.ManagedHash {
			return ErrConflict
		}
		if r.Key != current && record.ManagedHash != actual.ManagedHash {
			return ErrConflict
		}
	}
	return engineVerifyPhases(p, s, inv, current)
}
func enginePreflight(ctx context.Context, c *Client, p enginePlan, s Snapshot, inv engineInventory, mode Mode, approval *Approval) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := engineForeign(ctx, c, p, s, inv, approval); e != nil {
		return e
	}
	if s.Document != nil && s.Document.Binding != p.plan.input.Binding {
		return ErrBinding
	}
	if mode != ModeRecover && s.Document != nil {
		if s.Document.Pending != nil {
			return ErrConflict
		}
		for _, construction := range s.Document.Constructions {
			if !construction.Complete {
				return ErrConflict
			}
		}
	}
	if p.plan.input.Mode != ModeApply {
		return engineCleanupPreflight(ctx, c, p, s, inv)
	}
	if s.Document != nil {
		for _, reservation := range s.Document.Reservations {
			if !slices.Contains(p.plan.names, reservation.Namespace) {
				continue
			}
			same := false
			for _, construction := range s.Document.Constructions {
				if construction.Input == p.plan.Digest() {
					same = true
				}
			}
			if !same {
				return ErrConflict
			}
		}
	}
	for _, r := range p.plan.input.Resources {
		record := engineRecord(s, r.Key)
		if record != nil {
			if record.Status == ResourceOwned {
				continue
			}
			if mode == ModeRecover {
				continue
			}
			return ErrConflict
		}
		co := r.Coordinate
		if co.Unassigned || co.Version == "" && r.VersionFrom != "" || co.Kind == KindBindings || co.Kind == KindPublication {
			continue
		}
		// A not-yet-created parent/version is not an invented negative lookup.
		if co.ParentID != "" {
			continue
		}
		o, _, e := engineReadExact(ctx, c, p, inv, co)
		if e != nil {
			return fmt.Errorf("%w: preflight exact read: %w", ErrConflict, e)
		}
		if o.Presence != PresenceAbsent {
			return ErrConflict
		}
		if co.Kind == KindHook || co.Kind == KindRule {
			other := KindHook
			if co.Kind == KindHook {
				other = KindRule
			}
			if !engineMissing(inv, Coordinate{Kind: other, ID: co.ID}) {
				return ErrConflict
			}
		}
	}
	return engineVerifyPrefix(ctx, c, p, s, inv, "")
}
func previewEngine(ctx context.Context, c *Client, locked *LockedState, p enginePlan, mode Mode) (Preview, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if e := engineClient(p, c); e != nil {
		return Preview{}, e
	}
	s, e := locked.Load(ctx)
	if e != nil {
		return Preview{}, e
	}
	// Refuse before the first request when the resulting teardown would be.
	if e = custodyPreviewForecast(s, locked.binding, p.plan, mode); e != nil {
		return Preview{}, e
	}
	inv, e := engineInventoryRead(ctx, c, p, s)
	if e != nil {
		return Preview{}, e
	}
	if e = enginePreflight(ctx, c, p, s, inv, mode, nil); e != nil {
		return Preview{}, e
	}
	return NewPreview(p.plan, mode, s.Base, inv.Digest)
}
func enginePayload(v engineSpec, k ResourceKind) any {
	switch k {
	case KindEvaluator:
		return v.Evaluator
	case KindHook:
		return v.Hook
	case KindRule:
		return v.Rule
	case KindCollection:
		return v.Collection
	case KindAction:
		return v.Action
	case KindSuite:
		return v.Suite
	case KindDraft:
		return v.Draft
	case KindCase:
		return engineCaseRequest(*v.Case)
	case KindBindings:
		return struct {
			Bindings []SuiteBinding `json:"bindings"`
		}{*v.Bindings}
	case KindPublication:
		return v.Publication
	}
	return nil
}
func engineCaseRequest(v CaseSnapshot) CaseSpec {
	return CaseSpec{TestCaseID: v.TestCaseID, Name: enginePtr(v.Name), Description: enginePtr(v.Description), Tags: enginePtr(append([]string{}, v.Tags...)), Category: enginePtr(v.Category), Input: v.Input, Expected: enginePtr(v.Expected), Metadata: enginePtr(v.Metadata), ArtifactRefs: enginePtr([]ArtifactRef{}), SanitizationReviewed: enginePtr(false), SanitizationContentDigest: enginePtr("")}
}
func engineDispatch(ctx context.Context, c *Client, p enginePlan, co Coordinate, op Operation, v engineSpec) (*ResponseEvidence, error) {
	var value any
	var e error
	switch op {
	case OpCreate:
		switch co.Kind {
		case KindEvaluator:
			value, e = c.CreateEvaluator(ctx, *v.Evaluator)
		case KindHook:
			value, e = c.CreateHookRule(ctx, *v.Hook)
		case KindRule:
			value, e = c.CreateRule(ctx, *v.Rule)
		case KindCollection:
			var x Collection
			x, e = c.CreateCollection(ctx, *v.Collection)
			co.ID = x.CollectionID
			co.Unassigned = false
			value = x
		case KindAction:
			var x RuleAction
			x, e = c.CreateRuleAction(ctx, co.ParentID, *v.Action)
			co.ID = x.ActionID
			co.Unassigned = false
			value = x
		case KindSuite:
			value, e = c.CreateSuite(ctx, *v.Suite)
		case KindDraft:
			var x SuiteVersion
			x, e = c.CreateDraft(ctx, co.ParentID, *v.Draft)
			co.ID = x.Version
			co.Version = x.Version
			co.Unassigned = false
			value = x
		case KindCase:
			value, e = c.CreateCase(ctx, co.ParentID, co.Version, engineCaseRequest(*v.Case))
		default:
			e = ErrUnsupported
		}
	case OpInitialBindings:
		value, e = c.putInitialBindingsOnce(ctx, co.ParentID, co.Version, *v.Bindings)
	case OpPublish:
		_, e = c.Publish(ctx, co.ParentID, co.Version, *v.Publication)
	case OpDisable:
		switch co.Kind {
		case KindHook:
			value, e = c.ReplaceHookRule(ctx, co.ID, *v.Hook)
		case KindRule:
			value, e = c.DisableRule(ctx, co.ID)
		case KindAction:
			value, e = c.DisableRuleAction(ctx, co.ParentID, co.ID)
		default:
			e = ErrUnsupported
		}
	case OpDelete:
		switch co.Kind {
		case KindHook:
			e = c.DeleteHookRule(ctx, co.ID)
		case KindRule:
			e = c.DeleteRule(ctx, co.ID)
		case KindAction:
			e = c.DeleteRuleAction(ctx, co.ParentID, co.ID)
		case KindEvaluator:
			e = c.DeleteEvaluator(ctx, co.ID)
		case KindCollection:
			e = c.DeleteCollection(ctx, co.ID)
		default:
			e = ErrUnsupported
		}
	default:
		e = ErrUnsupported
	}
	if e != nil {
		return nil, e
	}
	response := &ResponseEvidence{Coordinate: co}
	if value != nil {
		entity, err := engineProject(p.plan.input.Binding, co, value)
		e = err
		if err == nil {
			response.ManagedHash = enginePtr(entity.Hash)
		}
	}
	digest, err := engineDigest(p.plan.input.Binding, "typed-response", co, struct {
		Operation Operation
		Hash      *Digest
	}{op, response.ManagedHash})
	if err != nil {
		return nil, err
	}
	response.Evidence = engineEvidence("typed-response-acceptance-not-outcome", digest)
	return response, e
}
func engineResultContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok {
		return context.WithDeadline(context.WithoutCancel(ctx), deadline)
	}
	return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
}
func engineBefore(ctx context.Context, c *Client, p enginePlan, s Snapshot, inv engineInventory, r PlannedResource, op Operation) (Coordinate, engineSpec, Digest, error) {
	if p.plan.input.Mode != ModeApply {
		return engineCleanupBefore(ctx, c, p, s, inv, r, op)
	}
	if e := engineVerifyPrefix(ctx, c, p, s, inv, ""); e != nil {
		return Coordinate{}, engineSpec{}, "", e
	}
	co, e := engineCoordinate(p, r, s)
	if e != nil {
		return co, engineSpec{}, "", e
	}
	spec, e := engineResolved(p, r, s)
	if e != nil {
		return co, spec, "", e
	}
	if !co.Unassigned && co.Kind != KindBindings && co.Kind != KindPublication {
		o, _, e := engineReadExact(ctx, c, p, inv, co)
		if e != nil {
			return co, spec, "", fmt.Errorf("%w: before exact read: %w", ErrConflict, e)
		}
		if o.Presence != PresenceAbsent {
			return co, spec, "", ErrConflict
		}
		if co.Kind == KindHook || co.Kind == KindRule {
			other := KindHook
			if co.Kind == KindHook {
				other = KindRule
			}
			if !engineMissing(inv, Coordinate{Kind: other, ID: co.ID}) {
				return co, spec, "", ErrConflict
			}
		}
	}
	pre, e := engineDigest(p.plan.input.Binding, "preflight", co, inv.Digest)
	return co, spec, pre, e
}
func engineStep(ctx context.Context, c *Client, locked *LockedState, p enginePlan, s Snapshot, auth *Authorization, construction string, chain Digest, step PlannedStep) (Snapshot, Digest, error) {
	r := planResource(p.plan.input, step.ResourceKey)
	if r == nil {
		return s, chain, ErrInvalid
	}
	inv, e := engineInventoryRead(ctx, c, p, s)
	if e != nil {
		return s, chain, e
	}
	if inv.Digest != chain {
		return s, chain, ErrConflict
	}
	if e = engineForeign(ctx, c, p, s, inv, &auth.approval); e != nil {
		return s, chain, e
	}
	co, wanted, pre, e := engineBefore(ctx, c, p, s, inv, *r, step.Operation)
	if e != nil {
		return s, chain, e
	}
	raw, e := json.Marshal(enginePayload(wanted, co.Kind))
	if e != nil {
		return s, chain, ErrInvalid
	}
	desired, e := NewAuthoredSpec(raw)
	if e != nil {
		return s, chain, e
	}
	deps := []Coordinate{}
	for _, key := range r.Dependencies {
		d := engineRecord(s, key)
		if d == nil {
			return s, chain, ErrConflict
		}
		deps = append(deps, d.Coordinate)
	}
	attempt := "attempt-" + rand.Text()
	s, e = locked.Commit(ctx, *s.Base.Head, auth, Change{Kind: ChangeIntent, ExpectedInventory: chain, ConstructionID: construction, AttemptID: attempt, Step: step.Number, Intent: &IntentEvidence{Desired: desired, PreObservation: pre, Dependencies: deps}})
	if e != nil {
		return s, chain, e
	}
	grant, e := locked.ClaimDispatch(ctx, *s.Base.Head, auth, attempt)
	if e != nil {
		return s, chain, e
	}
	requestCtx := grant.Context()
	var response *ResponseEvidence
	rpcErr := requestCtx.Err()
	if rpcErr == nil {
		response, rpcErr = engineDispatch(requestCtx, c, p, co, step.Operation, wanted)
	}
	// The typed call is synchronous: it has actually returned before any result
	// commit/release. Cancellation is never treated as an independent join proof.
	resultCtx, cancel := engineResultContext(ctx)
	defer cancel()
	var persistErr error
	if response != nil {
		s, persistErr = locked.Commit(resultCtx, *s.Base.Head, auth, Change{Kind: ChangeResponse, ExpectedInventory: chain, ConstructionID: construction, AttemptID: attempt, Step: step.Number, Response: response})
	}
	releaseErr := locked.ReleaseDispatch(grant)
	if e = errors.Join(rpcErr, persistErr, releaseErr); e != nil {
		return s, chain, e
	}
	if response == nil {
		return s, chain, ErrConflict
	}
	// Keep attributable evidence, but do not read around a known complete
	// configuration contradiction hoping a later observation will authorize it.
	if response.ManagedHash != nil {
		expected, err := engineDigest(p.plan.input.Binding, "managed", response.Coordinate, wanted)
		if err != nil || expected != *response.ManagedHash {
			return s, chain, ErrConflict
		}
	}
	inv, e = engineInventoryRead(ctx, c, p, s)
	if e != nil {
		return s, chain, e
	}
	co = response.Coordinate
	o, _, e := engineReadExact(ctx, c, p, inv, co)
	if e != nil {
		return s, chain, e
	}
	if step.Operation == OpDelete {
		if o.Presence != PresenceAbsent {
			return s, chain, ErrConflict
		}
	} else {
		wantHash, err := engineDigest(p.plan.input.Binding, "managed", co, wanted)
		if err != nil || o.Presence != PresencePresent || wantHash != o.ManagedHash {
			return s, chain, ErrConflict
		}
	}
	if p.plan.input.Mode == ModeApply {
		if e = engineVerifyPrefix(ctx, c, p, s, inv, r.Key); e != nil {
			return s, chain, e
		}
	} else if e = engineVerifyCustody(ctx, c, p, s, inv, r.Key); e != nil {
		return s, chain, e
	}
	s, e = locked.Commit(ctx, *s.Base.Head, auth, Change{Kind: ChangeObserve, ExpectedInventory: chain, ConstructionID: construction, AttemptID: attempt, Step: step.Number, Observation: &o})
	if e != nil {
		return s, chain, e
	}
	return s, inv.Digest, nil
}
func engineAuthorize(ctx context.Context, c *Client, locked *LockedState, p enginePlan, pv Preview, approval Approval, mode Mode) (Snapshot, engineInventory, *Authorization, error) {
	if e := engineClient(p, c); e != nil {
		return Snapshot{}, engineInventory{}, nil, e
	}
	s, e := locked.Load(ctx)
	if e != nil {
		return s, engineInventory{}, nil, e
	}
	inv, e := engineInventoryRead(ctx, c, p, s)
	if e != nil {
		return s, inv, nil, e
	}
	if e = enginePreflight(ctx, c, p, s, inv, mode, &approval); e != nil {
		return s, inv, nil, e
	}
	current, e := NewPreview(p.plan, mode, s.Base, inv.Digest)
	if e != nil || current.Digest() != pv.Digest() {
		return s, inv, nil, ErrConflict
	}
	auth, e := locked.Authorize(ctx, approval, pv, inv.Digest)
	return s, inv, auth, e
}
func engineReceipt(ctx context.Context, c *Client, p enginePlan, s Snapshot, construction string, chain Digest, preview Preview, approval Approval) (Receipt, error) {
	inv, e := engineInventoryRead(ctx, c, p, s)
	if e != nil {
		return Receipt{}, e
	}
	if inv.Digest != chain {
		return Receipt{}, ErrConflict
	}
	if p.plan.input.Mode != ModeApply {
		return engineCleanupReceipt(ctx, c, p, s, construction, inv, preview, approval)
	}
	if e = engineVerifyPrefix(ctx, c, p, s, inv, ""); e != nil {
		return Receipt{}, e
	}
	members := []ReceiptMember{}
	for _, r := range p.plan.input.Resources {
		rec := engineRecord(s, r.Key)
		if rec == nil || rec.Status != ResourceOwned {
			return Receipt{}, ErrConflict
		}
		o, _, e := engineReadExact(ctx, c, p, inv, rec.Coordinate)
		if e != nil {
			return Receipt{}, fmt.Errorf("%w: receipt exact read: %w", ErrConflict, e)
		}
		if o.Presence != PresencePresent || o.ManagedHash != rec.ManagedHash {
			return Receipt{}, ErrConflict
		}
		members = append(members, ReceiptMember{ResourceKey: r.Key, Coordinate: rec.Coordinate, Outcome: OutcomePresent, Observation: &o, Evidence: o.Evidence})
	}
	return Receipt{ID: "receipt-" + rand.Text(), ConstructionID: construction, Mode: ModeApply, Result: ReceiptApplyObserved, BindingDigest: custodyBindingHash(p.plan.input.Binding), Input: p.plan.Digest(), Preview: preview.Digest(), Inventory: chain, Namespaces: append([]string{}, p.plan.names...), Members: members, OperatorApproval: approval.hash, ObservedMS: time.Now().UnixMilli()}, nil
}
func engineContinue(ctx context.Context, c *Client, locked *LockedState, p enginePlan, s Snapshot, auth *Authorization, construction string, chain Digest, pv Preview, approval Approval) (Receipt, error) {
	cons := engineConstruction(s, construction)
	if cons == nil {
		return Receipt{}, ErrConflict
	}
	for _, step := range p.plan.input.Steps {
		cons = engineConstruction(s, construction)
		status := cons.Steps[int(step.Number)-1].Status
		if status == StepVerified || status == StepClassified || status == StepNotApplied {
			continue
		}
		if status != StepPlanned {
			return Receipt{}, ErrConflict
		}
		var e error
		s, chain, e = engineStep(ctx, c, locked, p, s, auth, construction, chain, step)
		if e != nil {
			return Receipt{}, e
		}
	}
	receipt, e := engineReceipt(ctx, c, p, s, construction, chain, pv, approval)
	if e != nil {
		return Receipt{}, e
	}
	_, e = locked.Commit(ctx, *s.Base.Head, auth, Change{Kind: ChangeFinish, ExpectedInventory: chain, ConstructionID: construction, Receipt: &receipt})
	return receipt, e
}
func applyEngine(ctx context.Context, c *Client, locked *LockedState, p enginePlan, pv Preview, approval Approval) (Receipt, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if p.plan.input.Mode != ModeApply {
		return Receipt{}, ErrInvalid
	}
	s, inv, auth, e := engineAuthorize(ctx, c, locked, p, pv, approval, ModeApply)
	if e != nil {
		return Receipt{}, e
	}
	construction := approval.document.ConstructionID
	if s.Base.Pristine {
		s, e = locked.Bootstrap(ctx, auth)
		construction = approval.document.ApprovalID
	} else {
		if construction == "" {
			construction = "construction-" + rand.Text()
		}
		s, e = locked.Commit(ctx, *s.Base.Head, auth, Change{Kind: ChangeStart, ExpectedInventory: inv.Digest, ConstructionID: construction})
	}
	if e != nil {
		return Receipt{}, e
	}
	return engineContinue(ctx, c, locked, p, s, auth, construction, inv.Digest, pv, approval)
}
func engineMissing(inv engineInventory, co Coordinate) bool {
	for _, v := range inv.Entities {
		if co.Kind == KindEvaluator {
			if v.Coordinate.Kind == KindEvaluator && v.Coordinate.ID == co.ID {
				return false
			}
		} else if v.Coordinate == co {
			return false
		}
	}
	return true
}
func engineReadExact(ctx context.Context, c *Client, p enginePlan, inv engineInventory, co Coordinate) (Observation, engineSpec, error) {
	var value any
	var err error
	switch co.Kind {
	case KindEvaluator:
		value, err = c.GetEvaluator(ctx, co.ID)
	case KindHook:
		value, err = c.GetHookRule(ctx, co.ID)
	case KindRule:
		value, err = c.GetRule(ctx, co.ID)
	case KindCollection:
		value, err = c.GetCollection(ctx, co.ID)
	case KindAction:
		if _, ok := inv.Actions[co.ParentID]; !ok {
			return Observation{}, engineSpec{}, ErrConflict
		}
		value, err = c.GetRuleAction(ctx, co.ParentID, co.ID)
	case KindSuite:
		value, err = c.GetSuite(ctx, co.ID)
	case KindCase:
		cp, ok := inv.Checkpoints[co.ParentID]
		if !ok {
			return Observation{}, engineSpec{}, ErrConflict
		}
		if _, ok = cp.Cases[co.Version]; !ok {
			return Observation{}, engineSpec{}, ErrConflict
		}
		value, err = c.GetCase(ctx, co.ParentID, co.Version, co.ID)
	case KindDraft, KindBindings, KindPublication:
		cp, ok := inv.Checkpoints[co.ParentID]
		if !ok {
			return Observation{}, engineSpec{}, ErrConflict
		}
		var ver *SuiteVersion
		for i := range cp.Suite.Versions {
			if cp.Suite.Versions[i].Version == co.Version {
				ver = &cp.Suite.Versions[i]
			}
		}
		if ver == nil {
			return Observation{}, engineSpec{}, ErrConflict
		}
		if co.Kind == KindDraft {
			value = *ver
		} else if co.Kind == KindBindings {
			value = cp.Bindings[co.Version]
		} else {
			review := PublicationReview{Cases: []CaseSnapshot{}, Bindings: cp.Bindings[co.Version]}
			if !ver.Published {
				return Observation{}, engineSpec{}, ErrConflict
			}
			for _, cs := range cp.Cases[co.Version] {
				entity, e := engineProject(p.plan.input.Binding, Coordinate{Kind: KindCase, ID: cs.TestCaseID, ParentID: co.ParentID, Version: co.Version}, cs)
				if e != nil {
					return Observation{}, engineSpec{}, e
				}
				review.Cases = append(review.Cases, *entity.Spec.Case)
			}
			sort.Slice(review.Cases, func(i, j int) bool { return review.Cases[i].TestCaseID < review.Cases[j].TestCaseID })
			bs, e := engineBindings(review.Bindings)
			if e != nil {
				return Observation{}, engineSpec{}, e
			}
			review.Bindings = bs
			value = engineSpec{Publication: &review}
		}
	default:
		return Observation{}, engineSpec{}, ErrUnsupported
	}
	if e := ctx.Err(); e != nil {
		return Observation{}, engineSpec{}, e
	}
	presence := PresencePresent
	managed := Digest("")
	spec := engineSpec{}
	if err != nil {
		var status *HTTPError
		if !errors.As(err, &status) || status.StatusCode != 404 || !engineMissing(inv, co) {
			return Observation{}, spec, ErrConflict
		}
		presence = PresenceAbsent
	} else {
		entity, e := engineProject(p.plan.input.Binding, co, value)
		if e != nil {
			return Observation{}, spec, e
		}
		managed = entity.Hash
		spec = entity.Spec
		if co.Kind == KindEvaluator {
			found := false
			for _, row := range inv.Evaluators {
				if row.EvaluatorID == co.ID {
					if row.Version != co.Version {
						return Observation{}, spec, ErrConflict
					}
					found = true
				}
			}
			actual := value.(Evaluator)
			listed, ok := inv.Entities[engineKey(co)]
			if !found || actual.Version != co.Version || !ok || listed.Hash != managed {
				return Observation{}, spec, ErrConflict
			}
		} else if co.Kind != KindDraft && co.Kind != KindBindings && co.Kind != KindPublication {
			listed, ok := inv.Entities[engineKey(co)]
			if !ok || listed.Hash != managed {
				return Observation{}, spec, ErrConflict
			}
		}
	}
	exact, e := engineDigest(p.plan.input.Binding, "exact", co, struct {
		Presence  Presence
		Managed   Digest
		Inventory Digest
	}{presence, managed, inv.Digest})
	if e != nil {
		return Observation{}, spec, e
	}
	return Observation{Coordinate: co, Presence: presence, ManagedHash: managed, ExactRead: exact, Inventory: inv.Digest, Evidence: engineEvidence("typed-exact-and-complete-inventory", exact)}, spec, nil
}
