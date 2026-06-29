// SPDX-License-Identifier: AGPL-3.0-only

// Package telemetryspec is the custom-telemetry DSL mechanic lib (Spec 5): a generic,
// declarative vocabulary a blueprint uses to describe ANY service's realistic, correlated
// telemetry (metrics/logs/spans → profiles). It is a peer to internal/cw and internal/genai
// — a MECHANIC lib, not strictly stdlib-only: it imports the high-card leaf (internal/highcard)
// for the capability matrix and is meant to be driven by the workload layer (which supplies the
// shape reading + correlation refs at eval time). It never imports the OTel SDK; metrics emit via
// promrw final names and spans via the hand-encoded OTLP seam (unchanged hard rule).
//
// This file (value.go) holds the VALUE MODELS — the one-of generic value generators every emitted
// number/string/bool comes from. See spec.go for metric/log/span specs + profiles, and
// capability.go for the load-time value-model→target-kind safety matrix (design §3.4).
package telemetryspec

import (
	"fmt"
	"strings"
)

// Value-model kind discriminators (the YAML keys), returned by ValueModel.Kind.
const (
	KindConst      = "const"
	KindConstStr   = "const_str"
	KindEnum       = "enum"
	KindIntRange   = "int_range"
	KindFloatRange = "float_range"
	KindNormal     = "normal"
	KindBool       = "bool"
	KindShape      = "shape"
	KindRef        = "ref"
)

// ValueModel is the one-of generic value generator: exactly one field is set (Validate
// enforces), mirroring the IncidentDecl scenario-xor-single pattern in the blueprint loader.
// It is a typed Go struct (NOT yaml.Node) so it strict-decodes with loud load errors and appears
// in the reflected authoring schema (BLUEPRINT-SCHEMA.md / TestSchemaCurrent).
type ValueModel struct {
	Const      *float64    `yaml:"const"`       // fixed numeric
	ConstStr   *string     `yaml:"const_str"`   // fixed string
	Enum       []EnumEntry `yaml:"enum"`        // weighted categorical; EnumDomain() = the full ordered value set
	IntRange   *IntRange   `yaml:"int_range"`   // bounded integer (with optional p_zero)
	FloatRange *FloatRange `yaml:"float_range"` // bounded float
	Normal     *Normal     `yaml:"normal"`      // gaussian (mean, stddev)
	Bool       *BoolModel  `yaml:"bool"`        // weighted boolean
	Shape      *ShapeModel `yaml:"shape"`       // base × the shape/incident engine reading (incident-aware)
	Ref        string      `yaml:"ref"`         // pulls a correlation field by name (the correlation glue)
}

// EnumEntry is one weighted categorical value.
type EnumEntry struct {
	Value  string  `yaml:"value"`
	Weight float64 `yaml:"weight"`
}

// IntRange is a bounded integer draw; PZero is the probability the draw is forced to 0 (e.g.
// retry_count is 0 ~95% of the time). PZero in [0,1]; Min<=Max.
type IntRange struct {
	Min   int     `yaml:"min"`
	Max   int     `yaml:"max"`
	PZero float64 `yaml:"p_zero"`
}

// FloatRange is a bounded uniform float draw (Min<=Max).
type FloatRange struct {
	Min float64 `yaml:"min"`
	Max float64 `yaml:"max"`
}

// Normal is a gaussian draw (Stddev>=0). Negative draws are clamped to 0 by Eval (telemetry
// magnitudes are non-negative).
type Normal struct {
	Mean   float64 `yaml:"mean"`
	Stddev float64 `yaml:"stddev"`
}

// BoolModel is a weighted boolean; PTrue in [0,1].
type BoolModel struct {
	PTrue float64 `yaml:"p_true"`
}

// ShapeModel drives a value from the shape/incident engine: the emitted value is Base ×
// EvalCtx.ShapeVal. Mode (optional) names the failure mode this value amplifies under — the
// workload feeds the matching shape.Eval reading into EvalCtx.ShapeVal so the value responds to
// incidents automatically (design §3.1/§6.5).
type ShapeModel struct {
	Base float64 `yaml:"base"`
	Mode string  `yaml:"mode"`
}

// EvalCtx carries the per-evaluation inputs: correlation refs + the shape reading + the
// randomness sources. Randomness comes from the shape ENGINE's draws (world.Shape.Float64 /
// NormFloat64) for consistency with the rest of synthkit — it affects emitted VALUES only and
// must NEVER influence which label keys/values appear (the -dump inventory, design §3.4).
type EvalCtx struct {
	Ref      map[string]string // correlation fields (trace_id, route, model, status, env, ...)
	ShapeVal float64           // the shape.Engine reading for this node/mode (incident-aware); 0 ⇒ treated as 1.0
	Rand     func() float64    // uniform [0,1) — the shape engine's Float64; nil ⇒ deterministic 0.5
	Norm     func() float64    // standard-normal — the shape engine's NormFloat64; nil ⇒ deterministic 0
}

func (c EvalCtx) rand() float64 {
	if c.Rand == nil {
		return 0.5
	}
	return c.Rand()
}

func (c EvalCtx) norm() float64 {
	if c.Norm == nil {
		return 0
	}
	return c.Norm()
}

// Eval returns (numeric, string); callers use whichever the target expects (metric magnitude /
// label / body field / span attr). String-typed models (const_str, ref, enum) return ("", value)
// with numeric 0; numeric models return (value, "").
func (v ValueModel) Eval(ctx EvalCtx) (float64, string) {
	switch v.Kind() {
	case KindConst:
		return *v.Const, ""
	case KindConstStr:
		return 0, *v.ConstStr
	case KindEnum:
		return 0, weightedPick(v.Enum, ctx.rand())
	case KindIntRange:
		if v.IntRange.PZero > 0 && ctx.rand() < v.IntRange.PZero {
			return 0, ""
		}
		span := v.IntRange.Max - v.IntRange.Min
		if span <= 0 {
			return float64(v.IntRange.Min), ""
		}
		// ctx.rand() in [0,1); map to [Min, Max] inclusive.
		n := min(v.IntRange.Min+int(ctx.rand()*float64(span+1)), v.IntRange.Max)
		return float64(n), ""
	case KindFloatRange:
		return v.FloatRange.Min + ctx.rand()*(v.FloatRange.Max-v.FloatRange.Min), ""
	case KindNormal:
		val := v.Normal.Mean + ctx.norm()*v.Normal.Stddev
		if val < 0 {
			val = 0
		}
		return val, ""
	case KindBool:
		if ctx.rand() < v.Bool.PTrue {
			return 1, "true"
		}
		return 0, "false"
	case KindShape:
		mult := ctx.ShapeVal
		if mult == 0 {
			mult = 1.0
		}
		return v.Shape.Base * mult, ""
	case KindRef:
		if ctx.Ref == nil {
			return 0, ""
		}
		return 0, ctx.Ref[v.Ref]
	}
	return 0, ""
}

// weightedPick selects an enum value by weight using r in [0,1). Falls back to the first value
// when all weights are zero (still deterministic for a fixed r).
func weightedPick(entries []EnumEntry, r float64) string {
	if len(entries) == 0 {
		return ""
	}
	var total float64
	for _, e := range entries {
		if e.Weight > 0 {
			total += e.Weight
		}
	}
	if total <= 0 {
		return entries[0].Value
	}
	target := r * total
	var acc float64
	for _, e := range entries {
		if e.Weight <= 0 {
			continue
		}
		acc += e.Weight
		if target < acc {
			return e.Value
		}
	}
	return entries[len(entries)-1].Value
}

// EnumDomain returns the full ordered value set of an enum model (for label determinism, §3.4:
// every label value combination must appear every run). Empty for non-enum models.
func (v ValueModel) EnumDomain() []string {
	if len(v.Enum) == 0 {
		return nil
	}
	out := make([]string, len(v.Enum))
	for i, e := range v.Enum {
		out[i] = e.Value
	}
	return out
}

// Kind returns the one set field's discriminator ("" if none/ambiguous-empty set).
func (v ValueModel) Kind() string {
	switch {
	case v.Const != nil:
		return KindConst
	case v.ConstStr != nil:
		return KindConstStr
	case len(v.Enum) > 0:
		return KindEnum
	case v.IntRange != nil:
		return KindIntRange
	case v.FloatRange != nil:
		return KindFloatRange
	case v.Normal != nil:
		return KindNormal
	case v.Bool != nil:
		return KindBool
	case v.Shape != nil:
		return KindShape
	case v.Ref != "":
		return KindRef
	}
	return ""
}

// setCount returns how many one-of fields are populated (for the exactly-one rule).
func (v ValueModel) setCount() int {
	n := 0
	for _, set := range []bool{
		v.Const != nil, v.ConstStr != nil, len(v.Enum) > 0, v.IntRange != nil,
		v.FloatRange != nil, v.Normal != nil, v.Bool != nil, v.Shape != nil, v.Ref != "",
	} {
		if set {
			n++
		}
	}
	return n
}

// Validate enforces exactly-one-set plus per-model field bounds (loud load error). Empty string
// values are rejected where they would silently drop a label key or emit an empty field (S1).
func (v ValueModel) Validate() error {
	switch n := v.setCount(); {
	case n == 0:
		return fmt.Errorf("value model: no model set (need exactly one of const|const_str|enum|int_range|float_range|normal|bool|shape|ref)")
	case n > 1:
		return fmt.Errorf("value model: %d models set, need exactly one", n)
	}
	switch v.Kind() {
	case KindConstStr:
		if strings.TrimSpace(*v.ConstStr) == "" {
			return fmt.Errorf("const_str: empty string not allowed")
		}
	case KindEnum:
		seen := make(map[string]bool, len(v.Enum))
		for i, e := range v.Enum {
			if strings.TrimSpace(e.Value) == "" {
				return fmt.Errorf("enum[%d]: empty value not allowed", i)
			}
			if e.Weight < 0 {
				return fmt.Errorf("enum value %q: negative weight %v", e.Value, e.Weight)
			}
			if seen[e.Value] {
				return fmt.Errorf("enum value %q: duplicate (would collapse to one series / double-count a counter)", e.Value)
			}
			seen[e.Value] = true
		}
	case KindIntRange:
		if v.IntRange.Min > v.IntRange.Max {
			return fmt.Errorf("int_range: min %d > max %d", v.IntRange.Min, v.IntRange.Max)
		}
		if v.IntRange.PZero < 0 || v.IntRange.PZero > 1 {
			return fmt.Errorf("int_range: p_zero %v out of [0,1]", v.IntRange.PZero)
		}
	case KindFloatRange:
		if v.FloatRange.Min > v.FloatRange.Max {
			return fmt.Errorf("float_range: min %v > max %v", v.FloatRange.Min, v.FloatRange.Max)
		}
	case KindNormal:
		if v.Normal.Stddev < 0 {
			return fmt.Errorf("normal: negative stddev %v", v.Normal.Stddev)
		}
	case KindBool:
		if v.Bool.PTrue < 0 || v.Bool.PTrue > 1 {
			return fmt.Errorf("bool: p_true %v out of [0,1]", v.Bool.PTrue)
		}
	case KindRef:
		if strings.TrimSpace(v.Ref) == "" {
			return fmt.Errorf("ref: empty correlation field name")
		}
	}
	return nil
}
