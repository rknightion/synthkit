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
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math"
	"net"
	"net/url"
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

// smTimeoutSeconds is the default HTTP/ping/DNS/TCP timeout in seconds.
// Resolved per-check timeouts drive both registration and failure duration.
const smTimeoutSeconds = 3.0

// smBaseLatency is the healthy p50 probe latency in seconds.
const smBaseLatency = 0.12

// smFailureRate is the background probabilistic failure rate (≈2% of ticks fail
// randomly, independent of any scheduled incident).
const smFailureRate = 0.02

// latencyAmp is the per-series Spread+Wander amplitude for probe duration metrics.
const latencyAmp = 0.18

// smProbeDurationBuckets are the le boundaries for probe_all_duration_seconds (LEBare).
// Agent v0.66.1 uses client_golang v1.24.1 DefBuckets for all check types.
// Provenance and predecessor difference: signals/sm.md [slug: sm-checks].
var smProbeDurationBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

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

	// Registration is injected only by the composition root after it validates the private,
	// version-bound provisioner handoff. It is never decoded from blueprint YAML.
	Registration        map[string]string `yaml:"-"`
	RequireRegistration bool              `yaml:"-"`
}

// CheckConfig describes one synthetic check.
type DNSSettings struct {
	Server string `yaml:"server"`
}
type TCPSettings struct {
	TLS bool `yaml:"tls"`
}

type CheckConfig struct {
	// Type selects http (default), ping, dns, tcp or traceroute.
	Type string `yaml:"type"`
	// TimeoutMS is the check timeout in milliseconds; traceroute requires 30000.
	TimeoutMS int `yaml:"timeout"`
	// DNS configures the resolver for a DNS check; the target is the queried name.
	DNS *DNSSettings `yaml:"dns"`
	// TCP enables TLS for a TCP host:port check.
	TCP *TCPSettings `yaml:"tcp"`

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

	// Latitude declares the private probe latitude in degrees [-90,90].
	// Supply with longitude; omitted coordinates retain Frankfurt. Explicit zero is valid.
	Latitude *float64 `yaml:"latitude"`

	// Longitude declares the private probe longitude in degrees [-180,180].
	// Supply with latitude; omitted coordinates retain Frankfurt. Explicit zero is valid.
	Longitude *float64 `yaml:"longitude"`

	// Labels is an optional map of user-defined labels. Each entry is emitted as
	// label_<k>=<v> on every probe_* metric series, on sm_check_info, and on the
	// Loki stream labels. Keys are sorted for deterministic output. An absent or
	// empty map produces no label_ keys (byte-identical to a check with no labels).
	Labels map[string]string `yaml:"labels"`
}

// resolvedCheck is a fully-defaulted, ready-to-emit check instance.
type resolvedCheck struct {
	spec             RuntimeSpec
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

// RuntimeSpec is the fully defaulted identity the composition root snapshots for provisioning.
// It deliberately contains no API client or registration-state type.
type RuntimeSpec struct {
	Type        string
	TimeoutMS   int
	DNS         *DNSSettings
	TCP         *TCPSettings
	Job         string
	Target      string
	FrequencyMs int
	Probe       string
	Region      string
	Latitude    float64
	Longitude   float64
	Labels      map[string]string
}

// ResolveSpec applies the data-plane defaults without constructing an emitter.
func ResolveSpec(c CheckConfig) RuntimeSpec {
	spec := RuntimeSpec{Type: c.Type, TimeoutMS: c.TimeoutMS, DNS: c.DNS, TCP: c.TCP, Job: c.Name, Target: c.Target, FrequencyMs: c.FrequencyMs, Probe: c.Probe, Region: c.Region}
	if c.DNS != nil {
		copy := *c.DNS
		spec.DNS = &copy
	}
	if c.TCP != nil {
		copy := *c.TCP
		spec.TCP = &copy
	}
	if spec.Type == "http" {
		spec.Type = ""
	}
	if spec.Target == "" {
		host := strings.ToLower(c.Name) + ".example.com"
		switch spec.Type {
		case "":
			spec.Target = "https://" + host + "/health"
		case "tcp":
			spec.Target = host + ":443"
		default:
			spec.Target = host
		}
	}
	if spec.TimeoutMS == 0 {
		spec.TimeoutMS = int(smTimeoutSeconds * 1000)
		if spec.Type == "traceroute" {
			spec.TimeoutMS = 30000
		}
	}
	if spec.Type == "tcp" && spec.TCP == nil {
		spec.TCP = &TCPSettings{}
	}
	if spec.FrequencyMs == 0 {
		spec.FrequencyMs = 60000
		if spec.Type == "traceroute" {
			spec.FrequencyMs = 120000
		}
	}
	if spec.Probe == "" {
		spec.Probe = DefaultProbeName
	}
	if spec.Region == "" {
		spec.Region = DefaultProbeRegion
	}
	spec.Latitude, spec.Longitude = DefaultProbeLat, DefaultProbeLon
	if c.Latitude != nil {
		spec.Latitude = *c.Latitude
	}
	if c.Longitude != nil {
		spec.Longitude = *c.Longitude
	}
	if len(c.Labels) > 0 {
		spec.Labels = make(map[string]string, len(c.Labels))
		for key, value := range c.Labels {
			spec.Labels[key] = value
		}
	}
	return spec
}

// CheckKey is the stable registration identity shared with the composition root.
func CheckKey(job, target string) string { return job + "\x00" + target }

// resolveChecks applies defaults and derives stable per-check constants.
func resolveChecks(cfg []CheckConfig, seed string, registration map[string]string, requireRegistration bool) ([]resolvedCheck, error) {
	out := make([]resolvedCheck, 0, len(cfg))
	probes := make(map[string]RuntimeSpec)
	for _, c := range cfg {
		if err := ValidateCheck(c); err != nil {
			return nil, err
		}
		spec := ResolveSpec(c)
		if prior, ok := probes[spec.Probe]; ok && (prior.Region != spec.Region || prior.Latitude != spec.Latitude || prior.Longitude != spec.Longitude) {
			return nil, fmt.Errorf("sm: probe %q has inconsistent region or coordinates", spec.Probe)
		}
		probes[spec.Probe] = spec
		version := configVersion(seed, c.Name)
		if requireRegistration {
			var ok bool
			version, ok = registration[CheckKey(spec.Job, spec.Target)]
			if !ok || version == "" {
				return nil, fmt.Errorf("sm: check %q has no validated registration", c.Name)
			}
		}
		rc := resolvedCheck{
			spec:             spec,
			job:              spec.Job,
			target:           spec.Target,
			frequencyMs:      spec.FrequencyMs,
			probe:            spec.Probe,
			region:           spec.Region,
			geohash:          probeGeohash(spec.Latitude, spec.Longitude),
			alertSensitivity: "medium",
			configVersion:    version,
			labels:           spec.Labels,
		}
		out = append(out, rc)
	}
	return out, nil
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
	checks, err := resolveChecks(c.Checks, seed, c.Registration, c.RequireRegistration)
	if err != nil {
		return nil, err
	}
	return &Construct{
		checks: checks,
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
		failed := eng.Active(now, failureMode, "")
		// Preserve legacy HTTP background failures. Network profiles use explicit
		// incidents so their path/packet/certificate facts share one causal outcome.
		if ch.spec.Type == "" {
			failed = failed || eng.Float64() < smFailureRate
		}

		success := 1.0
		// Healthy latency: diurnal scaling × per-check series variation so peer checks
		// emit distinct, stable-but-drifting values instead of identical smBaseLatency.
		// Key on metric name + check job to uniquely identify each series.
		dur := smBaseLatency * eng.BusinessFactor(now) * c.seriesVar(eng, now, "probe_duration|"+ch.job, latencyAmp)
		if failed {
			success = 0.0
			dur = float64(ch.spec.TimeoutMS) / 1000
		}

		// ── metrics ──────────────────────────────────────────────────────────

		// sm_check_info: metadata anchor (full label superset, no blueprint label added
		// by this construct — the scoped writer stamps it if ScopeBlueprint).
		info := map[string]string{
			"job":               ch.job,
			"instance":          ch.target,
			"probe":             ch.probe,
			"config_version":    ch.configVersion,
			"check_name":        checkType(ch.spec.Type),
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
		hopBodies := c.emitNetwork(ch, base, dur, failed, now)

		// ── logs ─────────────────────────────────────────────────────────────

		msg, ps := "Check succeeded", "1"
		if success == 0 {
			msg, ps = "Check failed", "0"
		}
		body := fmt.Sprintf(
			"level=info target=%s probe=%s region=%s instance=%s job=%s check_name=%s source=synthetic-monitoring-agent msg=%q duration_seconds=%.6f",
			ch.target, ch.probe, ch.region, ch.target, ch.job, checkType(ch.spec.Type), msg, dur,
		)
		streamLabels := map[string]string{
			"source":        "synthetic-monitoring-agent",
			"check_name":    checkType(ch.spec.Type),
			"instance":      ch.target,
			"job":           ch.job,
			"probe":         ch.probe,
			"region":        ch.region,
			"probe_success": ps,
		}
		stampUserLabels(streamLabels, ch.labels)
		lines := []loki.Line{{T: now, Body: body}}
		for _, hop := range hopBodies {
			lines = append(lines, loki.Line{T: now, Body: hop})
		}
		streams = append(streams, loki.Stream{
			Labels: streamLabels,
			Lines:  lines,
		})
	}

	return c.st.Collect(now), streams
}

// ValidateLocation rejects incomplete and invalid coordinate declarations before resolution.
func ValidateLocation(c CheckConfig) error {
	if (c.Latitude == nil) != (c.Longitude == nil) {
		return fmt.Errorf("sm: check %q requires both latitude and longitude", c.Name)
	}
	if c.Latitude == nil {
		return nil
	}
	if math.IsNaN(*c.Latitude) || math.IsInf(*c.Latitude, 0) || *c.Latitude < -90 || *c.Latitude > 90 || math.IsNaN(*c.Longitude) || math.IsInf(*c.Longitude, 0) || *c.Longitude < -180 || *c.Longitude > 180 {
		return fmt.Errorf("sm: check %q has invalid probe coordinates", c.Name)
	}
	return nil
}

// probeGeohash encodes the catalogue's precision-12 base32 geohash, interleaving
// longitude and latitude interval bits, beginning with longitude.
func probeGeohash(latitude, longitude float64) string {
	const alphabet = "0123456789bcdefghjkmnpqrstuvwxyz"
	latMin, latMax, lonMin, lonMax := -90.0, 90.0, -180.0, 180.0
	var out strings.Builder
	for i := 0; i < 12; i++ {
		value := 0
		for bit := 0; bit < 5; bit++ {
			value <<= 1
			if (i*5+bit)%2 == 0 {
				mid := (lonMin + lonMax) / 2
				if longitude >= mid {
					value |= 1
					lonMin = mid
				} else {
					lonMax = mid
				}
			} else {
				mid := (latMin + latMax) / 2
				if latitude >= mid {
					value |= 1
					latMin = mid
				} else {
					latMax = mid
				}
			}
		}
		out.WriteByte(alphabet[value])
	}
	return out.String()
}

func checkType(kind string) string {
	if kind == "" {
		return "http"
	}
	return kind
}

// ValidateCheck validates the admitted typed settings and actual target shape.
func ValidateCheck(c CheckConfig) error {
	if err := ValidateLocation(c); err != nil {
		return err
	}
	spec := ResolveSpec(c)
	if spec.Type != "" && spec.Type != "ping" && spec.Type != "dns" && spec.Type != "tcp" && spec.Type != "traceroute" {
		return fmt.Errorf("sm: unknown check type %q", spec.Type)
	}
	if c.DNS != nil && spec.Type != "dns" || c.TCP != nil && spec.Type != "tcp" {
		return fmt.Errorf("sm: settings do not match check type")
	}
	if spec.Type == "dns" && (spec.DNS == nil || !validHost(spec.DNS.Server)) {
		return fmt.Errorf("sm: DNS resolver server requires a hostname or IPv4 address; IPv6 is not supported")
	}
	switch spec.Type {
	case "":
		u, err := url.Parse(spec.Target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return fmt.Errorf("sm: invalid HTTP target")
		}
	case "tcp":
		host, port, err := net.SplitHostPort(spec.Target)
		if err != nil || !validHost(host) || port == "" {
			return fmt.Errorf("sm: TCP target requires hostname:port or IPv4:port; IPv6 is not supported")
		}
		var n int
		if _, err := fmt.Sscanf(port, "%d", &n); err != nil || fmt.Sprint(n) != port || n < 1 || n > 65535 {
			return fmt.Errorf("sm: invalid TCP port")
		}
	default:
		if !validHost(spec.Target) {
			return fmt.Errorf("sm: target requires hostname or IPv4 address; IPv6 is not supported")
		}
	}
	minFrequency := 1000
	if spec.Type == "traceroute" {
		minFrequency = 120000
		if spec.TimeoutMS != 30000 {
			return fmt.Errorf("sm: traceroute timeout must be 30000")
		}
	}
	if spec.FrequencyMs < minFrequency || spec.FrequencyMs > 3600000 || spec.TimeoutMS < 1000 || spec.TimeoutMS > 60000 || spec.TimeoutMS > spec.FrequencyMs {
		return fmt.Errorf("sm: invalid frequency or timeout")
	}
	return nil
}
func validHost(host string) bool {
	if net.ParseIP(host) != nil {
		return !strings.Contains(host, ":")
	}
	if host == "" || len(host) > 253 {
		return false
	}
	for _, part := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if part == "" || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return false
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// emitNetwork uses a bounded synthetic path: documentation-range IPv4 addresses,
// three routers and five traceroute packets per hop. These are simulation
// assumptions, never discovered topology or live network measurements.
func (c *Construct) emitNetwork(ch resolvedCheck, base map[string]string, dur float64, failed bool, now time.Time) []string {
	kind := ch.spec.Type
	if kind == "" {
		return nil
	}
	set := func(name string, value float64) { c.st.Set(name, base, value) }
	phase := func(name, key string, value float64) {
		labels := make(map[string]string, len(base)+1)
		for k, v := range base {
			labels[k] = v
		}
		labels["phase"] = key
		c.st.Set(name, labels, value)
	}
	if kind != "traceroute" {
		set("probe_dns_lookup_time_seconds", dur*.1)
		set("probe_ip_protocol", 4)
		h := fnv.New32a()
		host := ch.target
		if kind == "tcp" {
			host, _, _ = net.SplitHostPort(host)
		} else if kind == "dns" {
			host = ch.spec.DNS.Server
		}
		ip := net.ParseIP(host).To4()
		if ip == nil {
			// Hostname resolution is simulated with a documentation address.
			ip = net.ParseIP("203.0.113.1").To4()
		}
		_, _ = h.Write(ip)
		set("probe_ip_addr_hash", float64(h.Sum32()))
	}
	switch kind {
	case "ping":
		rtt := dur * .8
		minRTT, maxRTT, stddev, hopLimit := dur*.7, dur*.9, dur*.05, 64.0
		if failed {
			rtt, minRTT, maxRTT, stddev, hopLimit = 0, 0, 0, 0, 0
		}
		phase("probe_icmp_duration_seconds", "resolve", dur*.1)
		phase("probe_icmp_duration_seconds", "setup", dur*.1)
		phase("probe_icmp_duration_seconds", "rtt", rtt)
		set("probe_icmp_duration_rtt_min_seconds", minRTT)
		set("probe_icmp_duration_rtt_max_seconds", maxRTT)
		set("probe_icmp_duration_rtt_stddev_seconds", stddev)
		set("probe_icmp_packets_sent_count", 3)
		received := 3.0
		if failed {
			received = 0
		}
		set("probe_icmp_packets_received_count", received)
		set("probe_icmp_reply_hop_limit", hopLimit)
	case "dns":
		phase("probe_dns_duration_seconds", "resolve", dur*.1)
		phase("probe_dns_duration_seconds", "connect", dur*.1)
		phase("probe_dns_duration_seconds", "request", dur*.8)
		answer := 1.0
		if failed {
			answer = 0
		}
		set("probe_dns_answer_rrs", answer)
		set("probe_dns_authority_rrs", 0)
		set("probe_dns_additional_rrs", 0)
		set("probe_dns_query_succeeded", answer)
	case "tcp":
		set("probe_failed_due_to_regex", 0)
		set("probe_failed_due_to_bytes", 0)
		// Retire failed/renewed handshake metadata without resetting cumulative metrics.
		c.st.DropWhere(func(name string, labels map[string]string) bool {
			return (strings.HasPrefix(name, "probe_ssl_") || name == "probe_tls_version_info") &&
				labels["job"] == ch.job && labels["instance"] == ch.target && labels["probe"] == ch.probe
		})
		if ch.spec.TCP.TLS && !failed {
			// Synthetic CA policy: 90-day validity with renewal after 60 days.
			// The overlap models automated renewal; a certificate's expiry stays fixed.
			const renewalSeconds = int64(60 * 24 * 60 * 60)
			epoch := now.Unix() / renewalSeconds
			expiry := epoch*renewalSeconds + 90*24*60*60
			set("probe_ssl_earliest_cert_expiry", float64(expiry))
			set("probe_ssl_last_chain_expiry_timestamp_seconds", float64(expiry))
			labels := make(map[string]string, len(base)+1)
			for k, v := range base {
				labels[k] = v
			}
			labels["version"] = "TLS 1.3"
			c.st.Set("probe_tls_version_info", labels, 1)
			cert := make(map[string]string, len(base)+5)
			for k, v := range base {
				cert[k] = v
			}
			fingerprint := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", ch.target, epoch)))
			cert["fingerprint_sha256"] = fmt.Sprintf("%x", fingerprint)
			host, _, _ := net.SplitHostPort(ch.target)
			cert["subject"] = "CN=" + host
			cert["issuer"] = "CN=Synthetic CA"
			cert["subjectalternative"] = host
			cert["serialnumber"] = fmt.Sprint(epoch + 1)
			c.st.Set("probe_ssl_last_chain_info", cert, 1)
		}
	case "traceroute":
		hosts := []string{"192.0.2.1", "198.51.100.1", "203.0.113.1"}
		lost := 0.0
		if failed {
			lost = 1
		}
		set("probe_traceroute_total_hops", 3)
		set("probe_traceroute_packet_loss_percent", lost)
		h := fnv.New32()
		_, _ = h.Write([]byte(strings.Join(hosts, "")))
		set("probe_traceroute_route_hash", float64(h.Sum32()))
		sum := sha256.Sum256([]byte(ch.job + "\x00" + ch.target + "\x00" + now.UTC().Format(time.RFC3339Nano)))
		sum[6] = (sum[6] & 15) | 0x40
		sum[8] = (sum[8] & 63) | 0x80
		id := fmt.Sprintf("%x-%x-%x-%x-%x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
		var bodies []string
		for i, host := range hosts {
			// MTR Avg is zero without replies; the agent truncates average milliseconds.
			elapsed := time.Duration(dur*float64(i+1)/3*1000) * time.Millisecond
			if failed {
				elapsed = 0
			}
			bodies = append(bodies, fmt.Sprintf("Level=info Destination=%s Hosts=%s TTL=%d ElapsedTime=%s LossPercent=%g Sent=5 TracerouteID=%s", ch.target, host, i+1, elapsed, lost*100, id))
		}
		return bodies
	}
	return nil
}
