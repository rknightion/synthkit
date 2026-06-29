// SPDX-License-Identifier: AGPL-3.0-only

// Package sm is the Synthetic Monitoring data-plane construct. It emits the full
// probe_*/sm_check_info metric family and Loki log streams that the Grafana Cloud
// Synthetic Monitoring app consumes — with no real probe executing. The label/shape
// contract is byte-exact with the live SM agent output; see ARCHITECTURE §1.3 and
// signals/sm.md [slug: sm-checks] for the full spec.
//
// Kind: "synthetic_monitoring"
// Scope: ScopeSubstrate — disambiguated by check_name (never stamped with blueprint label)
// Signals: Metrics + Logs
// Interval: 60s
//
// Config: one or more checks (see Config / CheckConfig); config_version per check is
// a stable deterministic constant derived from the blueprint seed + check name so it
// never changes between runs (it is the SM app's panel join key).
package sm

import (
	"context"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

// Kind is the registry key for this construct.
const Kind = "synthetic_monitoring"

// smTimeoutSeconds is the registered check Timeout expressed in seconds. A check
// that times out reports probe_duration_seconds ≈ this value. Single source so data-
// plane and (future) control-plane provisioner cannot drift.
const smTimeoutSeconds = 3.0

// smBaseLatency is the healthy p50 probe latency in seconds.
const smBaseLatency = 0.12

// smFailureRate is the background probabilistic failure rate (≈2% of ticks fail
// randomly, independent of any scheduled incident).
const smFailureRate = 0.02

// latencyAmp is the per-series Spread+Wander amplitude for probe duration metrics.
const latencyAmp = 0.18

// smProbeDurationBuckets are the le boundaries for probe_all_duration_seconds (LEBare).
// Verified against the predecessor's SMProbeDurationBuckets.
var smProbeDurationBuckets = []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// DefaultProbeName, DefaultProbeRegion, DefaultProbeGeohash, DefaultProbeLat, and
// DefaultProbeLon are the canonical identity of the offline private probe registered
// by the companion cmd/sm-provision command (Frankfurt, geohash precision-12). They
// are exported so that the provisioner can import them as its single source of truth —
// data-plane and control-plane cannot drift.
const (
	DefaultProbeName    = "synthkit-private"
	DefaultProbeRegion  = "EMEA"
	DefaultProbeGeohash = "u0yjjd6j5sff" // Frankfurt lat/lon 50.1109/8.6821
	DefaultProbeLat     = 50.1109
	DefaultProbeLon     = 8.6821
)

// defaultProbeRegion and defaultProbeGeohash are package-internal aliases kept for
// backward compatibility within this file.
const (
	defaultProbeRegion  = DefaultProbeRegion
	defaultProbeGeohash = DefaultProbeGeohash
)

// failureMode is the incident key queried via World.Shape.Eval. When this mode is
// active the check is treated as failed (probe_success=0, duration=smTimeoutSeconds).
const failureMode = "sm_probe_failure"

// ————————————————————————————————————————————————————————————————————————————
// Config types (decoded from blueprint YAML)
// ————————————————————————————————————————————————————————————————————————————

// Config is decoded from the blueprint's `synthetic_monitoring:` block.
type Config struct {
	Checks []CheckConfig `yaml:"checks"`
}

// CheckConfig describes one synthetic HTTP check.
type CheckConfig struct {
	// Name is required. It becomes the Prometheus `job` label on all emitted series.
	Name string `yaml:"name"`

	// Target is the HTTP URL probed. Defaults to "https://<name>.example.com/health"
	// if empty.
	Target string `yaml:"target"`

	// FrequencyMs is the check poll interval in milliseconds. Defaults to 60000 (60s).
	FrequencyMs int `yaml:"frequency"`

	// Probe is the private probe name. Defaults to "synthkit-private".
	Probe string `yaml:"probe"`

	// Region is the probe region. Defaults to defaultProbeRegion ("EMEA").
	Region string `yaml:"region"`

	// Labels is an optional map of user-defined labels. Each entry is emitted as
	// label_<k>=<v> on every probe_* metric series, on sm_check_info, and on the
	// Loki stream labels. Keys are sorted for deterministic output. An absent or
	// empty map produces no label_ keys (byte-identical to a check with no labels).
	Labels map[string]string `yaml:"labels"`
}

// resolvedCheck is a fully-defaulted, ready-to-emit check instance.
type resolvedCheck struct {
	job              string
	target           string
	frequencyMs      int
	probe            string
	region           string
	geohash          string
	alertSensitivity string
	configVersion    string            // stable deterministic decimal string — the join key
	labels           map[string]string // user labels emitted as label_<k>=<v>; nil if none
}

// resolveChecks applies defaults and derives stable per-check constants.
func resolveChecks(cfg []CheckConfig, seed string) []resolvedCheck {
	out := make([]resolvedCheck, 0, len(cfg))
	for _, c := range cfg {
		// Copy user labels so resolvedCheck owns its own map (blueprint cfg is not mutated).
		var lbls map[string]string
		if len(c.Labels) > 0 {
			lbls = make(map[string]string, len(c.Labels))
			for k, v := range c.Labels {
				lbls[k] = v
			}
		}
		rc := resolvedCheck{
			job:              c.Name,
			target:           c.Target,
			frequencyMs:      c.FrequencyMs,
			probe:            c.Probe,
			region:           c.Region,
			geohash:          defaultProbeGeohash,
			alertSensitivity: "medium",
			configVersion:    configVersion(seed, c.Name),
			labels:           lbls,
		}
		if rc.target == "" {
			rc.target = "https://" + strings.ToLower(c.Name) + ".example.com/health"
		}
		if rc.frequencyMs == 0 {
			rc.frequencyMs = 60000
		}
		if rc.probe == "" {
			rc.probe = DefaultProbeName
		}
		if rc.region == "" {
			rc.region = defaultProbeRegion
		}
		out = append(out, rc)
	}
	return out
}

// configVersion returns a stable decimal uint64 string derived from the blueprint
// seed and check name. The SM app joins all per-check series on
// (instance, job, probe, config_version); any instability here silently breaks joins.
//
// Derivation: first 8 bytes of sha256(seed+":config_version:"+name) interpreted as
// big-endian uint64 and formatted as decimal. This matches the shape of the real SM
// agent's config_version (which is modified_ns — a uint64 decimal).
func configVersion(seed, checkName string) string {
	h := fixture.Sum(seed, "config_version", checkName)
	// h is a 64-hex-char string; parse the first 16 hex chars (8 bytes) as uint64.
	raw := make([]byte, 8)
	for i := range 8 {
		var b byte
		_, _ = fmt.Sscanf(h[i*2:i*2+2], "%02x", &b)
		raw[i] = b
	}
	v := binary.BigEndian.Uint64(raw)
	return fmt.Sprintf("%d", v)
}

// ————————————————————————————————————————————————————————————————————————————
// Construct
// ————————————————————————————————————————————————————————————————————————————

// Construct is the SM data-plane emitter instance. One instance covers all checks
// declared in the blueprint's synthetic_monitoring config block.
type Construct struct {
	checks []resolvedCheck
	st     *state.State
}

// Build validates cfg (must be *Config), applies defaults, and returns a ready
// Construct. fx.Seed drives the stable config_version derivation.
func Build(cfg any, fx *fixture.Set) (core.Construct, error) {
	c, ok := cfg.(*Config)
	if !ok {
		return nil, fmt.Errorf("sm: Build called with %T, want *Config", cfg)
	}
	if len(c.Checks) == 0 {
		return nil, fmt.Errorf("sm: config must have at least one check")
	}
	seed := ""
	if fx != nil {
		seed = fx.Seed
	}
	return &Construct{
		checks: resolveChecks(c.Checks, seed),
		st:     state.NewState(),
	}, nil
}

// NewConfig returns an empty *Config for the YAML decoder.
func NewConfig() any { return &Config{} }

func (c *Construct) Kind() string                { return Kind }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics, core.Logs} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// Tick emits one 60s SM tick: metrics via w.Metrics, logs via w.Logs.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	series, streams := c.build(now, w.Shape)
	if len(series) > 0 && w.Metrics != nil {
		if err := w.Metrics.Write(ctx, series); err != nil {
			return fmt.Errorf("sm: metrics write: %w", err)
		}
	}
	if len(streams) > 0 && w.Logs != nil {
		if err := w.Logs.Write(ctx, streams); err != nil {
			return fmt.Errorf("sm: logs write: %w", err)
		}
	}
	return nil
}

// stampUserLabels copies user labels from labels into dst, prefixing each key
// with "label_". Keys are iterated in sorted order for deterministic output.
// A nil or empty labels map is a no-op — dst is returned unchanged.
func stampUserLabels(dst map[string]string, labels map[string]string) {
	if len(labels) == 0 {
		return
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		dst["label_"+k] = labels[k]
	}
}

// seriesVar returns a stable-but-living per-series multiplier ≈ 1±amp: a deterministic
// baseline offset (Spread) times a slow per-series drift (Wander). Peer checks keyed on
// distinct strings emit distinct, stable-but-drifting values instead of a shared constant.
// Returns 1.0 when eng is nil.
func (c *Construct) seriesVar(eng *shape.Engine, now time.Time, key string, amp float64) float64 {
	if eng == nil {
		return 1.0
	}
	return eng.Spread(key, amp) * eng.Wander(key, now, amp*0.4)
}

// build accumulates one tick into c.st and assembles the Loki streams. It is a
// separate method so tests can assert the full wire contract without a live sink.
func (c *Construct) build(now time.Time, eng *shape.Engine) ([]promrw.Series, []loki.Stream) {
	var streams []loki.Stream

	for _, ch := range c.checks {
		base := map[string]string{
			"job":            ch.job,
			"instance":       ch.target,
			"probe":          ch.probe,
			"config_version": ch.configVersion,
		}
		stampUserLabels(base, ch.labels)

		// Failure decision: scheduled/live incident OR small probabilistic background rate.
		// Uses eng.Float64() (delegates to global randv2.Float64) for engine-convention
		// consistency — functionally identical to direct randv2.Float64().
		failed := eng.Active(now, failureMode, "") || eng.Float64() < smFailureRate

		success := 1.0
		// Healthy latency: diurnal scaling × per-check series variation so peer checks
		// emit distinct, stable-but-drifting values instead of identical smBaseLatency.
		// Key on metric name + check job to uniquely identify each series.
		dur := smBaseLatency * eng.BusinessFactor(now) * c.seriesVar(eng, now, "probe_duration|"+ch.job, latencyAmp)
		if failed {
			success = 0.0
			dur = smTimeoutSeconds
		}

		// ── metrics ──────────────────────────────────────────────────────────

		// sm_check_info: metadata anchor (full label superset, no blueprint label added
		// by this construct — the scoped writer stamps it if ScopeBlueprint).
		info := map[string]string{
			"job":               ch.job,
			"instance":          ch.target,
			"probe":             ch.probe,
			"config_version":    ch.configVersion,
			"check_name":        "http",
			"region":            ch.region,
			"frequency":         fmt.Sprintf("%d", ch.frequencyMs),
			"geohash":           ch.geohash,
			"alert_sensitivity": ch.alertSensitivity,
		}
		stampUserLabels(info, ch.labels)
		c.st.Set("sm_check_info", info, 1)

		// probe_success / probe_duration_seconds: instantaneous gauges.
		c.st.Set("probe_success", base, success)
		c.st.Set("probe_duration_seconds", base, dur)

		// probe_all_success_{sum,count}: cumulative summary counters (T3 in extract).
		// No bucket series — this is a summary, not a histogram.
		c.st.Add("probe_all_success_count", base, 1)
		c.st.Add("probe_all_success_sum", base, success)

		// probe_all_duration_seconds: cumulative histogram (Observe emits
		// _bucket/_sum/_count — do NOT also Add _sum/_count, that would double-count).
		c.st.Observe("probe_all_duration_seconds", base, smProbeDurationBuckets, state.LEBare, dur)

		// ── logs ─────────────────────────────────────────────────────────────

		msg, ps := "Check succeeded", "1"
		if success == 0 {
			msg, ps = "Check failed", "0"
		}
		body := fmt.Sprintf(
			"level=info target=%s probe=%s region=%s instance=%s job=%s check_name=http source=synthetic-monitoring-agent msg=%q duration_seconds=%.6f",
			ch.target, ch.probe, ch.region, ch.target, ch.job, msg, dur,
		)
		streamLabels := map[string]string{
			"source":        "synthetic-monitoring-agent",
			"check_name":    "http",
			"instance":      ch.target,
			"job":           ch.job,
			"probe":         ch.probe,
			"region":        ch.region,
			"probe_success": ps,
		}
		stampUserLabels(streamLabels, ch.labels)
		streams = append(streams, loki.Stream{
			Labels: streamLabels,
			Lines:  []loki.Line{{T: now, Body: body}},
		})
	}

	return c.st.Collect(now), streams
}
