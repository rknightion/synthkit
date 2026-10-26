// SPDX-License-Identifier: AGPL-3.0-only

// Package telemetryspec is the custom-telemetry DSL mechanic lib (Spec 5): a generic,
// declarative vocabulary a blueprint uses to describe ANY service's realistic, correlated
// telemetry (metrics/logs/spans → profiles). It is a peer to internal/cw and internal/genai
// — a MECHANIC lib, not strictly stdlib-only: it imports the high-card leaf (internal/highcard)
// for the capability matrix, the shape leaf (internal/shape) for the declarative business profile, and is meant to be driven by the workload layer (which supplies the
// shape reading + correlation refs at eval time). It never imports the OTel SDK; metrics emit via
// promrw final names and spans via the hand-encoded OTLP seam (unchanged hard rule).
//
// This file (value.go) holds the VALUE MODELS — the one-of generic value generators every emitted
// number/string/bool comes from. See spec.go for metric/log/span specs + profiles, and
// capability.go for the load-time value-model→target-kind safety matrix (design §3.4).
package telemetryspec

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/shape"
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
	KindLognormal  = "lognormal"
	KindNow        = "now"
	KindResource   = "resource"
	KindOutcomes   = "outcomes"

	KindRequestDraw = "request_draw"
	KindSourceRead  = "source_read"
)

// Source-read measures (SourceReadModel.Measure): what a collector's own read loop reports.
const (
	ReadRecordsInspected = "records_inspected" // records examined in a tick; none while stalled
	ReadCheckpoint       = "checkpoint"        // checkpoint position advanced in a tick; none while stalled
	ReadFailures         = "read_failures"     // failed reads in a tick; only while stalled
)

// Outcome measures (OutcomeValue.Measure). An omitted measure is MeasureCount.
const (
	MeasureCount   = "count"   // counter: matching hop outcomes in the metric window
	MeasureShare   = "share"   // gauge: matching share of the window's hop outcomes
	MeasureLatency = "latency" // histogram: durations (seconds) of the window's matching hops
)

// Outcome status classes (OutcomeValue.Status entries and status_label enum values), besides an
// exact three-digit HTTP status such as "503".
const (
	StatusSuccess = "success"
	Status4xx     = "4xx"
	Status5xx     = "5xx"
)

// resourceGaugeFields and resourceCounterFields are the model-state fields a resource value
// reads (causal design 5.4). Gauge fields read the latest state; counter fields are window sums
// over (last tick, now].
var resourceGaugeFields = map[string]bool{
	"demand_rps": true, "capacity": true, "utilization": true, "in_use": true, "waiting": true,
	"wait_seconds": true, "backlog": true, "oldest_age_seconds": true, "latency_factor": true,
	"hold_seconds": true, "served_rps": true, "failed_share": true,
}

var resourceCounterFields = map[string]bool{"rejected": true, "admitted": true, "failed": true}

// IsResourceGaugeField reports whether f is a gauge-only resource field.
func IsResourceGaugeField(f string) bool { return resourceGaugeFields[f] }

// IsResourceCounterField reports whether f is a counter-only resource field.
func IsResourceCounterField(f string) bool { return resourceCounterFields[f] }

// IsOutcomeStatus reports whether s is a valid outcome status: "success", "4xx", "5xx" or an
// exact three-digit HTTP status from 100 to 599.
func IsOutcomeStatus(s string) bool {
	switch s {
	case StatusSuccess, Status4xx, Status5xx:
		return true
	}
	if len(s) != 3 || s[0] < '1' || s[0] > '5' {
		return false
	}
	return s[1] >= '0' && s[1] <= '9' && s[2] >= '0' && s[2] <= '9'
}

// z95 is the standard-normal 0.95 quantile: a lognormal's p95 is median × exp(sigma × z95).
const z95 = 1.6448536269514722

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
	Lognormal  *Lognormal  `yaml:"lognormal"`   // right-skewed positive draw (median, p95)
	Now        *NowModel   `yaml:"now"`         // the tick timestamp in Unix seconds (gauge value only)
	// Resource reads a field of a declared resource's model state (metric value only).
	Resource *ResourceValue `yaml:"resource"`
	// Outcomes reads the service node's hop outcomes in the metric window (metric value only).
	Outcomes *OutcomeValue `yaml:"outcomes"`
	// RequestDraw draws a formatted ID or an enum value once per request and repeats it on every
	// log line of that request that names the same key (log body field only).
	RequestDraw *RequestDraw `yaml:"request_draw"`
	// SourceRead is a collector's own read work, a counter increment that advances with reads and
	// stops (or turns to failures) while its stall_mode incident is active (counter metric value only).
	SourceRead *SourceReadModel `yaml:"source_read"`
}

// RequestDraw is a value drawn once per request and shared by every log line of that request
// that declares the same Key, so a multi-line log bundle repeats one fresh ID and one path
// instead of a fixed specimen. Set exactly one of Format and Enum.
//
// The draw hashes the request's seed unit (the workload supplies it as EvalCtx.Draw), never the
// shape engine, so it is deterministic for a given seed and clock and consumes no engine
// randomness: declaring one never shifts a neighbouring field's values. A line that declares the
// same key on a different format or enum would draw a different value; lines of one bundle
// should repeat the same declaration.
//
// Format is literal text with generated runs written {digits:N}, {hex:N} (lower case) or
// {upper:N} (upper case letters and digits), N from 1 to 32 and at most 64 generated characters in
// all, for example "ORD-{digits:10}" or "{hex:8}-{hex:4}". Literal braces are not supported.
// Enum is a weighted categorical draw with the same entries as an `enum` value model (weights
// select the share of requests; rate_scale, value_scale and shape_profile do not apply).
type RequestDraw struct {
	Key    string      `yaml:"key"`
	Format string      `yaml:"format"`
	Enum   []EnumEntry `yaml:"enum"`
}

// drawRun is one parsed piece of a Format: literal text, or n generated characters drawn from
// alphabet.
type drawRun struct {
	literal  string
	alphabet string
	n        int
}

const (
	maxDrawRun   = 32
	maxDrawChars = 64
)

var drawAlphabets = map[string]string{
	"digits": "0123456789",
	"hex":    "0123456789abcdef",
	"upper":  "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
}

// parseDrawFormat splits a Format into literal and generated runs. A format with no generated
// run is rejected (it would be a constant: use const_str).
func parseDrawFormat(format string) ([]drawRun, error) {
	var runs []drawRun
	var lit strings.Builder
	generated := 0
	for i := 0; i < len(format); {
		switch format[i] {
		case '}':
			return nil, fmt.Errorf("format %q: unmatched } (literal braces are not supported)", format)
		case '{':
			end := strings.IndexByte(format[i:], '}')
			if end < 0 {
				return nil, fmt.Errorf("format %q: unclosed { (write {digits:N}, {hex:N} or {upper:N})", format)
			}
			name, count, ok := strings.Cut(format[i+1:i+end], ":")
			alphabet, known := drawAlphabets[name]
			n, err := strconv.Atoi(count)
			if !ok || !known || err != nil || n < 1 || n > maxDrawRun {
				return nil, fmt.Errorf("format %q: bad run {%s} (want {digits:N}, {hex:N} or {upper:N} with N from 1 to %d)", format, format[i+1:i+end], maxDrawRun)
			}
			if lit.Len() > 0 {
				runs = append(runs, drawRun{literal: lit.String()})
				lit.Reset()
			}
			runs = append(runs, drawRun{alphabet: alphabet, n: n})
			generated += n
			i += end + 1
		default:
			lit.WriteByte(format[i])
			i++
		}
	}
	if lit.Len() > 0 {
		runs = append(runs, drawRun{literal: lit.String()})
	}
	switch {
	case generated == 0:
		return nil, fmt.Errorf("format %q: no generated run (use const_str for a fixed value)", format)
	case generated > maxDrawChars:
		return nil, fmt.Errorf("format %q: %d generated characters exceeds %d", format, generated, maxDrawChars)
	}
	return runs, nil
}

func (d RequestDraw) validate() error {
	if strings.TrimSpace(d.Key) == "" {
		return fmt.Errorf("request_draw: key must be non-blank")
	}
	switch {
	case d.Format != "" && len(d.Enum) > 0:
		return fmt.Errorf("request_draw %q: set either format or enum, not both", d.Key)
	case d.Format == "" && len(d.Enum) == 0:
		return fmt.Errorf("request_draw %q: need a format or an enum", d.Key)
	}
	if d.Format != "" {
		if _, err := parseDrawFormat(d.Format); err != nil {
			return fmt.Errorf("request_draw %q: %w", d.Key, err)
		}
		return nil
	}
	for _, e := range d.Enum {
		if e.Scaled() || e.ShapeProfile != nil || e.StallMode != "" {
			return fmt.Errorf("request_draw %q value %q: rate_scale, value_scale, shape_profile and stall_mode are valid on metric labels only", d.Key, e.Value)
		}
	}
	if err := validateEnumEntries(d.Enum); err != nil {
		return fmt.Errorf("request_draw %q: %w", d.Key, err)
	}
	return nil
}

// eval renders the request's value for this declaration from the per-request draw u, a function of
// a purpose string to [0,1). A nil u reads the midpoint of every draw, so a context without a
// request still renders a stable value.
func (d RequestDraw) eval(u func(string) float64) string {
	draw := func(purpose string) float64 {
		if u == nil {
			return 0.5
		}
		return u(purpose)
	}
	if d.Format == "" {
		return weightedPick(d.Enum, draw(d.Key+"#enum"))
	}
	runs, err := parseDrawFormat(d.Format)
	if err != nil {
		return "" // unreachable after Validate
	}
	var b strings.Builder
	idx := 0
	for _, run := range runs {
		if run.literal != "" {
			b.WriteString(run.literal)
			continue
		}
		for range run.n {
			b.WriteByte(run.alphabet[min(int(draw(d.Key+"#"+strconv.Itoa(idx))*float64(len(run.alphabet))), len(run.alphabet)-1)])
			idx++
		}
	}
	return b.String()
}

// ResourceValue reads one field of the named resource's state. Gauge fields (demand_rps,
// capacity, utilization, in_use, waiting, wait_seconds, backlog, oldest_age_seconds,
// latency_factor, hold_seconds, served_rps, failed_share) are valid on a gauge metric only; counter
// fields (rejected, admitted, failed) are window sums valid on a counter metric only. The
// provider supplies the reading; without one, or for an unknown resource, the value reads 0.
type ResourceValue struct {
	Name  string `yaml:"name"`
	Field string `yaml:"field"`
}

// OutcomeValue reads a node's hop outcomes in the metric window. Status lists the statuses that
// match ("503", "4xx", "5xx", "success"); StatusLabel instead names an enum label of the same
// metric whose value selects the status per series (set exactly one). Measure is count (a counter
// metric, the default), share (a gauge metric) or latency (a histogram metric: each matching hop
// duration, in seconds, is one observation).
type OutcomeValue struct {
	Status      []string `yaml:"status"`
	StatusLabel string   `yaml:"status_label"`
	Measure     string   `yaml:"measure"`
}

// EffectiveMeasure returns Measure with the omitted default (count) applied.
func (o OutcomeValue) EffectiveMeasure() string {
	if o.Measure == "" {
		return MeasureCount
	}
	return o.Measure
}

// NowModel selects the emission time as a metric value: the tick timestamp in Unix seconds, for
// a "last success" or heartbeat timestamp gauge (declare it as `now: {}`). The reading is the
// tick time the workload was driven with, never the wall clock, so it is deterministic under the
// fixed clock `-once -dump` uses. It is valid on a gauge value only: a counter or histogram built
// from a timestamp is not a meaningful series.
//
// StallMode names an incident mode that stalls the producer: while that mode is active for the
// service the metric belongs to, the gauge holds its last emitted reading instead of advancing,
// and it resumes at the tick time when the incident ends, so a "last success older than N" alert
// can fire. Omitted means the gauge always advances. The workload that owns the metric resolves
// the incident (the same per-service resolution its shape values use); the model itself only
// carries the name.
//
// ArrivalsOnly makes the gauge a source-event time instead of a read time: it advances only on a
// tick where the source had arrivals (the node's load factor is above 0, so a quiet period of its
// shape_profile or a paused workload counts as none) and holds its last reading otherwise, so a
// quiet source reads old without the collector having stalled. StallMode still freezes it.
//
// The hold is kept per series: an enum label value that declares its own stall_mode (EnumEntry)
// stalls and holds independently of its siblings; every other series follows StallMode.
//
// Period turns the gauge into the last success of a job declared on a schedule: the reading is the
// most recent schedule boundary at or before the tick time (epoch-aligned multiples of Period,
// shifted by Phase), so a job that runs every 15 minutes reads a step every 15 minutes, not every
// tick. Period is in (0, 24h] and Phase in [0, Period); both default to 0, which reads the tick
// time. A run that falls inside a stall (StallMode active) or a quiet source (ArrivalsOnly) is a
// missed run: the gauge keeps its last reading until the first boundary after the stall ends. No
// randomness and no clock beyond the tick time are read.
type NowModel struct {
	StallMode    string        `yaml:"stall_mode"`
	ArrivalsOnly bool          `yaml:"arrivals_only"`
	Period       time.Duration `yaml:"period"`
	Phase        time.Duration `yaml:"phase"`
}

// maxNowPeriod bounds a now schedule: a daily job is the longest cadence a last-success gauge models.
const maxNowPeriod = 24 * time.Hour

// Boundary is the most recent schedule boundary at or before at: the largest k*Period+Phase (k an
// integer, counted from the Unix epoch) not after it. With no Period it is at itself, so the
// default reads the tick time. Boundary of a boundary is itself.
func (n NowModel) Boundary(at time.Time) time.Time {
	if n.Period <= 0 || at.IsZero() {
		return at
	}
	p, ph := int64(n.Period), int64(n.Phase)
	d := at.UnixNano() - ph
	k := d / p
	if d%p < 0 {
		k-- // floor, not truncate, for times before the epoch
	}
	return time.Unix(0, k*p+ph)
}

// SourceReadModel is one counter of a collector's own read loop, so records inspected, checkpoint
// progress and read failures derive from the same stall state instead of three hand-aligned
// shapes. Each metric tick adds PerTick (times the node's load factor for records_inspected and
// checkpoint, like a shape value) while the stall is inactive and nothing while it is active
// (records_inspected, checkpoint), or PerTick only while it is active (read_failures). PerTick is
// per tick, not per second: the metric interval is not divided out. A stall is all or nothing
// (any intensity above 0). The counter is cumulative across ticks (internal/state). The model
// draws no randomness.
//
// StallMode names the incident mode that stalls the collector, resolved per service like a now
// gauge's; an enum label value may override it for its own series (EnumEntry.StallMode).
// read_failures needs a stall mode (here or on an enum value). The model reads no metric name:
// the blueprint names the counter.
type SourceReadModel struct {
	Measure   string  `yaml:"measure"`
	PerTick   float64 `yaml:"per_tick"`
	StallMode string  `yaml:"stall_mode"`
}

// validate enforces the model's own invariants; the instrument and per-value stall rules need
// the owning metric (MetricSpec.Validate).
func (r SourceReadModel) validate() error {
	switch r.Measure {
	case ReadRecordsInspected, ReadCheckpoint, ReadFailures:
	default:
		return fmt.Errorf("source_read: measure %q invalid (want records_inspected|checkpoint|read_failures)", r.Measure)
	}
	if math.IsNaN(r.PerTick) || math.IsInf(r.PerTick, 0) || r.PerTick <= 0 {
		return fmt.Errorf("source_read: per_tick %v must be finite and > 0", r.PerTick)
	}
	if m := r.StallMode; m != "" && strings.TrimSpace(m) != m {
		return fmt.Errorf("source_read: stall_mode %q must be a nonblank incident mode name without surrounding whitespace", m)
	}
	return nil
}

// EnumEntry is one weighted categorical value. Weight only steers a per-request draw (a log body
// field, span attribute or stream label); it never divides traffic among metric series, because a
// metric label enum expands to every value every tick.
//
// RateScale and ValueScale are opt-in per-value multipliers for a METRIC label (rejected on log
// and span positions). With several scaled labels on one metric the per-combination multiplier is
// the product of the chosen values' scales. Omitted means 1 (byte-identical legacy output).
//   - RateScale multiplies a counter's per-tick increment, or a histogram's observations per tick
//     (fractional rates carry deterministically across ticks). Rejected on a gauge.
//   - ValueScale multiplies each observed value of a histogram (so its mean and p95 move
//     together) or a gauge reading. Rejected on a counter, whose increment is a rate.
type EnumEntry struct {
	Value      string   `yaml:"value"`
	Weight     float64  `yaml:"weight"`
	RateScale  *float64 `yaml:"rate_scale"`
	ValueScale *float64 `yaml:"value_scale"`
	// ShapeProfile gives this value's series its own business clock and curve, laid field by
	// field over the metric's and the node's profile (METRIC labels only; the metric's value must
	// be a `shape`). The label value is unchanged, so the series set is identical. Omitted means
	// the series follows the inherited profile.
	ShapeProfile *shape.Profile `yaml:"shape_profile"`
	// StallMode gives this value's series its own stall incident mode in place of the metric's
	// (METRIC labels only; the metric's value must be a `now` or `source_read`). At most one label
	// of a metric may declare it. Omitted means the series follows the metric's stall_mode.
	StallMode string `yaml:"stall_mode"`
}

// Scaled reports whether the entry declares either per-value metric multiplier.
func (e EnumEntry) Scaled() bool { return e.RateScale != nil || e.ValueScale != nil }

// Lognormal is a right-skewed positive draw parameterised by its median and 95th percentile:
// value = Median × exp(sigma × z), sigma = ln(P95/Median)/1.6449, z standard-normal. The mean
// exceeds the median and the tail is long, as real latency and dwell-time distributions are.
// Median > 0 and P95 >= Median; P95 == Median degenerates to a constant Median.
type Lognormal struct {
	Median float64 `yaml:"median"`
	P95    float64 `yaml:"p95"`
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

// ShapeModel drives a value from the shape/incident engine. Without IncidentValue it emits
// Base × EvalCtx.ShapeVal, preserving the legacy multiplicative incident response. IncidentValue
// opts metric values into an explicit endpoint: interpolate Base→IncidentValue at the scoped
// IncidentIntensity, then multiply by the unamplified load in ShapeVal. Mode is required for
// that opt-in. This is an instantaneous reading, not a backlog accumulation/drain model.
//
// IncidentAbsolute (requires IncidentValue) moves the load scaling off the incident endpoint:
// the value becomes (1-i) × Base × load + i × IncidentValue, so the baseline still follows the
// load curve while the incident level is absolute and does not shrink at night, on a weekend or
// under a reduced master volume (an authoritative zero load, ExactLoad with ShapeVal 0, still
// reads 0). Meant for durations and latencies, where an outage reads the
// same whatever the traffic. It changes nothing unless set.
type ShapeModel struct {
	Base             float64  `yaml:"base"`
	Mode             string   `yaml:"mode"`
	IncidentValue    *float64 `yaml:"incident_value"`    // metric-only endpoint at intensity 1, before load scaling
	IncidentAbsolute bool     `yaml:"incident_absolute"` // incident share of the value is not multiplied by load; needs incident_value
	// Batch makes a counter's source deliver in batches: the per-tick arrivals accumulate and are
	// released at the cadence (zero between releases), conserving the smooth curve's total. Counter
	// metric values only, and not with incident_value or on a metric that declares stock_of or
	// count_of (those follow the batched counter instead); the workload resolves the release
	// against the series' own clock. Period is in (0, 24h] and Phase in [0, Period).
	Batch *shape.Batch `yaml:"batch"`
}

// EvalCtx carries the per-evaluation inputs: correlation refs + the shape reading + the
// randomness sources. Randomness comes from the shape ENGINE's draws (world.Shape.Float64 /
// NormFloat64) for consistency with the rest of synthkit — it affects emitted VALUES only and
// must NEVER influence which label keys/values appear (the -dump inventory, design §3.4).
type EvalCtx struct {
	Ref               map[string]string // correlation fields (trace_id, route, model, status, env, ...)
	ShapeVal          float64           // load (legacy: incident-amplified); 0 ⇒ treated as 1.0 unless ExactLoad
	ExactLoad         bool              // ShapeVal is authoritative: an exact 0 is zero load, not the 1.0 fallback
	IncidentIntensity float64           // scoped intensity in [0,1], only for shape.incident_value; zero when inactive
	Rand              func() float64    // uniform [0,1) — the shape engine's Float64; nil ⇒ deterministic 0.5
	Norm              func() float64    // standard-normal — the shape engine's NormFloat64; nil ⇒ deterministic 0
	Now               time.Time         // tick time read by the now model; zero ⇒ the model reads 0
	Labels            map[string]string // the evaluated series' label values, passed to the outcome providers

	// NowFor, when set, is the now model's reading for the series carrying these labels (its own
	// stall mode and hold); nil reads Now.
	NowFor func(labels map[string]string) time.Time
	// Stalled reports whether the source_read model's collector is stalled for the series
	// carrying these labels; nil reads as not stalled.
	Stalled func(labels map[string]string) bool

	// Resource reads a resource field (name, field); ok=false or a nil func ⇒ the model reads 0.
	Resource func(name, field string) (float64, bool)

	// Draw is the request's seeded draw: a deterministic function of a purpose string to [0,1)
	// that hashes the request's seed unit, read by request_draw. It is independent of Rand, so
	// it consumes no engine randomness; nil reads the midpoint of every draw.
	Draw func(purpose string) float64
	// Outcomes reads an outcome count or share for the series; a nil func ⇒ the model reads 0.
	Outcomes func(v OutcomeValue, labels map[string]string) float64
	// OutcomeLatencies returns the window's matching hop durations in seconds (measure latency);
	// a nil func ⇒ no observations.
	OutcomeLatencies func(v OutcomeValue, labels map[string]string) []float64
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
		if mult == 0 && !ctx.ExactLoad {
			mult = 1.0
		}
		if v.Shape.IncidentValue != nil {
			// A convex sum avoids overflow from subtracting opposite finite endpoints.
			i := ctx.IncidentIntensity
			if v.Shape.IncidentAbsolute {
				if ctx.ExactLoad && ctx.ShapeVal == 0 {
					return 0, "" // an authoritative zero load is "no activity": nothing to read
				}
				return (1-i)*v.Shape.Base*mult + i*(*v.Shape.IncidentValue), ""
			}
			return ((1-i)*v.Shape.Base + i*(*v.Shape.IncidentValue)) * mult, ""
		}
		return v.Shape.Base * mult, ""
	case KindRef:
		if ctx.Ref == nil {
			return 0, ""
		}
		return 0, ctx.Ref[v.Ref]
	case KindRequestDraw:
		return 0, v.RequestDraw.eval(ctx.Draw)
	case KindNow:
		at := ctx.Now
		if ctx.NowFor != nil {
			at = ctx.NowFor(ctx.Labels)
		}
		if at.IsZero() {
			return 0, ""
		}
		at = v.Now.Boundary(at)
		return float64(at.Unix()) + float64(at.Nanosecond())/1e9, ""
	case KindSourceRead:
		stalled := ctx.Stalled != nil && ctx.Stalled(ctx.Labels)
		if v.SourceRead.Measure == ReadFailures {
			if stalled {
				return v.SourceRead.PerTick, ""
			}
			return 0, ""
		}
		if stalled {
			return 0, ""
		}
		load := ctx.ShapeVal
		if load == 0 && !ctx.ExactLoad {
			load = 1.0
		}
		return v.SourceRead.PerTick * load, ""
	case KindLognormal:
		sigma := math.Log(v.Lognormal.P95/v.Lognormal.Median) / z95
		return v.Lognormal.Median * math.Exp(sigma*ctx.norm()), ""
	case KindResource:
		if ctx.Resource == nil {
			return 0, ""
		}
		if val, ok := ctx.Resource(v.Resource.Name, v.Resource.Field); ok {
			return val, ""
		}
		return 0, ""
	case KindOutcomes:
		// A latency model has no single reading: its observations come from Observations.
		if ctx.Outcomes == nil || v.Outcomes.EffectiveMeasure() == MeasureLatency {
			return 0, ""
		}
		return ctx.Outcomes(*v.Outcomes, ctx.Labels), ""
	}
	return 0, ""
}

// Observations returns the per-observation values (seconds) of an outcomes model with measure
// latency, for a histogram. It is nil for every other model and when no OutcomeLatencies
// provider is set.
func (v ValueModel) Observations(ctx EvalCtx) []float64 {
	if v.Kind() != KindOutcomes || v.Outcomes.EffectiveMeasure() != MeasureLatency || ctx.OutcomeLatencies == nil {
		return nil
	}
	return ctx.OutcomeLatencies(*v.Outcomes, ctx.Labels)
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
	case v.Lognormal != nil:
		return KindLognormal
	case v.Now != nil:
		return KindNow
	case v.Resource != nil:
		return KindResource
	case v.Outcomes != nil:
		return KindOutcomes
	case v.RequestDraw != nil:
		return KindRequestDraw
	case v.SourceRead != nil:
		return KindSourceRead
	}
	return ""
}

// setCount returns how many one-of fields are populated (for the exactly-one rule).
func (v ValueModel) setCount() int {
	n := 0
	for _, set := range []bool{
		v.Const != nil, v.ConstStr != nil, len(v.Enum) > 0, v.IntRange != nil,
		v.FloatRange != nil, v.Normal != nil, v.Bool != nil, v.Shape != nil, v.Ref != "",
		v.Lognormal != nil, v.Now != nil, v.Resource != nil, v.Outcomes != nil, v.RequestDraw != nil, v.SourceRead != nil,
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
		return fmt.Errorf("value model: no model set (need exactly one of const|const_str|enum|int_range|float_range|normal|lognormal|bool|shape|now|resource|outcomes|source_read|request_draw|ref)")
	case n > 1:
		return fmt.Errorf("value model: %d models set, need exactly one", n)
	}
	switch v.Kind() {
	case KindConstStr:
		if strings.TrimSpace(*v.ConstStr) == "" {
			return fmt.Errorf("const_str: empty string not allowed")
		}
	case KindEnum:
		if err := validateEnumEntries(v.Enum); err != nil {
			return err
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
	case KindShape:
		if v.Shape.IncidentAbsolute && v.Shape.IncidentValue == nil {
			return fmt.Errorf("shape: incident_absolute requires incident_value")
		}
		if v.Shape.Batch != nil {
			if err := v.Shape.Batch.Validate(); err != nil {
				return fmt.Errorf("shape: %w", err)
			}
		}
		if v.Shape.IncidentValue != nil {
			if strings.TrimSpace(v.Shape.Mode) == "" {
				return fmt.Errorf("shape: incident_value requires a nonblank mode")
			}
			if math.IsNaN(v.Shape.Base) || math.IsInf(v.Shape.Base, 0) ||
				math.IsNaN(*v.Shape.IncidentValue) || math.IsInf(*v.Shape.IncidentValue, 0) {
				return fmt.Errorf("shape: base and incident_value must be finite")
			}
		}
	case KindNow:
		if m := v.Now.StallMode; m != "" && strings.TrimSpace(m) != m {
			return fmt.Errorf("now: stall_mode %q must be a nonblank incident mode name without surrounding whitespace", m)
		}
		if p, ph := v.Now.Period, v.Now.Phase; p < 0 || p > maxNowPeriod {
			return fmt.Errorf("now: period %v must be in (0, %v]", p, maxNowPeriod)
		} else if ph < 0 || (p == 0 && ph != 0) || (p > 0 && ph >= p) {
			return fmt.Errorf("now: phase %v must be in [0, period) and needs a period", ph)
		}
	case KindResource:
		if strings.TrimSpace(v.Resource.Name) == "" {
			return fmt.Errorf("resource: name must be non-blank")
		}
		if f := v.Resource.Field; !IsResourceGaugeField(f) && !IsResourceCounterField(f) {
			return fmt.Errorf("resource: unknown field %q (want a gauge field demand_rps|capacity|utilization|in_use|waiting|wait_seconds|backlog|oldest_age_seconds|latency_factor|hold_seconds|served_rps|failed_share, or a counter field rejected|admitted|failed)", f)
		}
	case KindOutcomes:
		return v.Outcomes.validate()
	case KindRequestDraw:
		return v.RequestDraw.validate()
	case KindSourceRead:
		return v.SourceRead.validate()
	case KindRef:
		if strings.TrimSpace(v.Ref) == "" {
			return fmt.Errorf("ref: empty correlation field name")
		}
	case KindLognormal:
		m, p := v.Lognormal.Median, v.Lognormal.P95
		if math.IsNaN(m) || math.IsInf(m, 0) || math.IsNaN(p) || math.IsInf(p, 0) {
			return fmt.Errorf("lognormal: median and p95 must be finite")
		}
		if m <= 0 {
			return fmt.Errorf("lognormal: median %v must be > 0", m)
		}
		if p < m {
			return fmt.Errorf("lognormal: p95 %v must be >= median %v", p, m)
		}
	}
	return nil
}

// validateEnumEntries enforces the entries of an enum value model or request draw: non-empty
// unique values, non-negative weights and finite non-negative scales.
func validateEnumEntries(entries []EnumEntry) error {
	seen := make(map[string]bool, len(entries))
	for i, e := range entries {
		if strings.TrimSpace(e.Value) == "" {
			return fmt.Errorf("enum[%d]: empty value not allowed", i)
		}
		if e.Weight < 0 {
			return fmt.Errorf("enum value %q: negative weight %v", e.Value, e.Weight)
		}
		for _, sc := range []*float64{e.RateScale, e.ValueScale} {
			if sc != nil && (math.IsNaN(*sc) || math.IsInf(*sc, 0) || *sc < 0) {
				return fmt.Errorf("enum value %q: rate_scale/value_scale must be finite and >= 0", e.Value)
			}
		}
		if seen[e.Value] {
			return fmt.Errorf("enum value %q: duplicate (would collapse to one series / double-count a counter)", e.Value)
		}
		seen[e.Value] = true
	}
	return nil
}

// validate enforces the outcomes model's own invariants; the instrument and label rules need the
// owning metric (MetricSpec.Validate).
func (o OutcomeValue) validate() error {
	switch o.Measure {
	case "", MeasureCount, MeasureShare, MeasureLatency:
	default:
		return fmt.Errorf("outcomes: measure %q invalid (want count|share|latency)", o.Measure)
	}
	switch {
	case len(o.Status) == 0 && o.StatusLabel == "":
		return fmt.Errorf("outcomes: need status or status_label")
	case len(o.Status) > 0 && o.StatusLabel != "":
		return fmt.Errorf("outcomes: set either status or status_label, not both")
	case o.StatusLabel != "" && strings.TrimSpace(o.StatusLabel) != o.StatusLabel:
		return fmt.Errorf("outcomes: status_label %q must not have surrounding whitespace", o.StatusLabel)
	}
	for i, st := range o.Status {
		if !IsOutcomeStatus(st) {
			return fmt.Errorf("outcomes: status[%d] %q invalid (want success|4xx|5xx or a three-digit HTTP status)", i, st)
		}
	}
	return nil
}
