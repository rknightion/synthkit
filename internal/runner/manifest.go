// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/core"
)

// LaneState is the closed manifest state of one intended delivery lane. Only LaneExpected lanes
// are required to deliver; every other state keeps the lane in the manifest with its reason, so a
// consumer never loses a lane from its denominator because it is quiet, disabled or undeliverable.
type LaneState string

const (
	// LaneExpected: an enabled instance of a running blueprint declares the lane and its sink is
	// configured. A push is due within FreshAfter.
	LaneExpected LaneState = "expected"
	// LaneDisabled: every instance declaring the lane is disabled by the control plane.
	LaneDisabled LaneState = "disabled"
	// LaneUnsupported: an enabled instance declares the lane but this process has no sink for it.
	LaneUnsupported LaneState = "unsupported"
	// LaneStandby: the lane would be expected, but this process is an HA standby with no producers.
	LaneStandby LaneState = "standby"
	// LaneDryRun: the lane would be expected, but synthetic delivery is in dry-run mode.
	LaneDryRun LaneState = "dry_run"
)

// LaneFreshness says whether a lane's bound is a missing-evidence bound. It follows the core
// two-cadence contract (core.Workload): constructs and workload metric and profile lanes push from
// Tick on their declared Interval; workload trace, log, Sigil and RUM lanes push from ProjectBatch
// only when requests were minted, so a quiet window there carries no evidence either way.
type LaneFreshness string

const (
	// FreshnessScheduled: at least one deciding contributor pushes from a fixed-cadence Tick.
	FreshnessScheduled LaneFreshness = "scheduled"
	// FreshnessRequestDriven: every deciding contributor pushes only per minted request batch.
	FreshnessRequestDriven LaneFreshness = "request_driven"
)

// ManifestLane is one sink lane of the delivery manifest. Sink is one of the fixed queue names
// (promrw, loki, otlp, pyroscope, faro, otlpmetrics, otlplogs, sigil), so a manifest holds at most
// one lane per sink. EmissionInterval is the slowest declared cadence among the instances that
// decide the lane's state; FreshAfter adds the delivery-queue partial-batch deadline. For a
// FreshnessScheduled lane FreshAfter is a missing-evidence bound: no push within it means the lane
// is missing. For a FreshnessRequestDriven lane it is only the cadence at which a minted batch
// would be projected; silence inside or past it is not evidence of a fault.
type ManifestLane struct {
	Sink             string
	State            LaneState
	Freshness        LaneFreshness
	EmissionInterval time.Duration
	FreshAfter       time.Duration
}

// DeliveryManifest is the bounded, secret-free expectation of what the running set intends to
// deliver. Fingerprint is a hex SHA-256 over the derivation inputs (each running blueprint's name,
// source digest and enablement, each instance's identity, enablement, cadence and declared lanes,
// and the configured sink set); it changes whenever the effective configuration does. It carries
// no session identity: the self-observability exporter binds that.
type DeliveryManifest struct {
	Fingerprint string
	Lanes       []ManifestLane
}

// Suppress relabels every expected lane with a process-level state (LaneStandby, LaneDryRun).
// Lanes keep their cadence and bound; non-expected lanes keep their own reason.
func (m DeliveryManifest) Suppress(state LaneState) DeliveryManifest {
	out := DeliveryManifest{Fingerprint: m.Fingerprint, Lanes: make([]ManifestLane, len(m.Lanes))}
	for i, lane := range m.Lanes {
		if lane.State == LaneExpected {
			lane.State = state
		}
		out.Lanes[i] = lane
	}
	return out
}

// signalLane maps a declared signal class to its delivery lane. RUM is bound per workload
// declaration (rumDeclared), not only through Signals, so both map to faro.
func signalLane(c core.SignalClass) (string, bool) {
	switch c {
	case core.Metrics:
		return "promrw", true
	case core.Logs:
		return "loki", true
	case core.Traces:
		return "otlp", true
	case core.PyroscopeProfiles:
		return "pyroscope", true
	case core.OTLPMetrics:
		return "otlpmetrics", true
	case core.OTLPLogs:
		return "otlplogs", true
	case core.Sigil:
		return "sigil", true
	case core.RUM:
		return "faro", true
	}
	return "", false
}

// sinkConfigured reports whether the process has the raw sink behind a lane.
func (r *Runner) sinkConfigured(lane string) bool {
	switch lane {
	case "promrw":
		return r.sinks.Metrics != nil
	case "loki":
		return r.sinks.Logs != nil
	case "otlp":
		return r.sinks.Traces != nil
	case "pyroscope":
		return r.sinks.Profiles != nil
	case "faro":
		return r.sinks.RUM != nil
	case "otlpmetrics":
		return r.sinks.OTLPMetrics != nil
	case "otlplogs":
		return r.sinks.OTLPLogs != nil
	case "sigil":
		return r.sinks.Sigil != nil
	}
	return false
}

// manifestDeadline is the effective partial-batch deadline the queues use (queue default 5s).
func (r *Runner) manifestDeadline() time.Duration {
	if d := r.opts.SendDeadline; d > 0 {
		return d
	}
	return 5 * time.Second
}

// workloadLaneScheduled reports whether a workload pushes a lane from its Tick (metric and profile
// lanes) rather than from ProjectBatch (traces, logs, Sigil, RUM), per the core.Workload contract.
func workloadLaneScheduled(lane string) bool {
	switch lane {
	case "promrw", "otlpmetrics", "pyroscope":
		return true
	}
	return false
}

// DeliveryManifest derives intended delivery from the running set: the declared signal classes of
// every instance of every running blueprint, the control-plane enablement and the configured
// sinks. It never consults push outcomes, so a lane that has not pushed yet is still present.
// Live apply and removal change the running set, so each call follows it. The control snapshot is
// loaded once, so one manifest never mixes two control revisions.
func (r *Runner) DeliveryManifest() DeliveryManifest {
	type class struct {
		any, scheduled bool
		interval       time.Duration
	}
	type acc struct{ enabled, disabled class }
	lanes := map[string]*acc{}
	var inputs []string
	add := func(lane string, on, scheduled bool, interval time.Duration) {
		a := lanes[lane]
		if a == nil {
			a = &acc{}
			lanes[lane] = a
		}
		c := &a.disabled
		if on {
			c = &a.enabled
		}
		c.any = true
		c.scheduled = c.scheduled || scheduled
		c.interval = max(c.interval, interval)
	}
	declare := func(signals []core.SignalClass, rum bool) []string {
		set := map[string]bool{}
		for _, c := range signals {
			if lane, ok := signalLane(c); ok {
				set[lane] = true
			}
		}
		if rum {
			set["faro"] = true
		}
		out := make([]string, 0, len(set))
		for lane := range set {
			out = append(out, lane)
		}
		sort.Strings(out)
		return out
	}
	st := r.ctl.Load()
	for _, bp := range r.blueprintSet() {
		bpOn := blueprintEnabledIn(st, bp.name)
		src := sha256.Sum256([]byte(bp.source))
		inputs = append(inputs, fmt.Sprintf("blueprint\x00%s\x00%x\x00%t", bp.name, src, bpOn))
		for _, bc := range bp.constructs {
			on := bpOn && constructEnabledIn(st, bp.name, bc.kind, bc.name)
			declared := declare(bc.signals, false)
			for _, lane := range declared {
				add(lane, on, true, bc.interval)
			}
			inputs = append(inputs, fmt.Sprintf("construct\x00%s\x00%s\x00%s\x00%t\x00%d\x00%s", bp.name, bc.kind, bc.name, on, bc.interval, strings.Join(declared, ",")))
		}
		for _, bw := range bp.workloads {
			declared := declare(bw.signals, bw.rumDeclared)
			for _, lane := range declared {
				add(lane, bpOn, workloadLaneScheduled(lane), bw.interval)
			}
			inputs = append(inputs, fmt.Sprintf("workload\x00%s\x00%s\x00%s\x00%t\x00%d\x00%s", bp.name, bw.kind, bw.workload.Name(), bpOn, bw.interval, strings.Join(declared, ",")))
		}
	}
	names := make([]string, 0, len(lanes))
	for name := range lanes {
		names = append(names, name)
	}
	sort.Strings(names)
	deadline := r.manifestDeadline()
	out := DeliveryManifest{Lanes: make([]ManifestLane, 0, len(names))}
	for _, name := range names {
		a := lanes[name]
		c, state := a.disabled, LaneDisabled
		if a.enabled.any {
			c, state = a.enabled, LaneUnsupported
			if r.sinkConfigured(name) {
				state = LaneExpected
			}
		}
		lane := ManifestLane{Sink: name, State: state, Freshness: FreshnessRequestDriven, EmissionInterval: c.interval, FreshAfter: c.interval + deadline}
		if c.scheduled {
			lane.Freshness = FreshnessScheduled
		}
		out.Lanes = append(out.Lanes, lane)
	}
	for _, sink := range []string{"promrw", "loki", "otlp", "pyroscope", "faro", "otlpmetrics", "otlplogs", "sigil"} {
		inputs = append(inputs, fmt.Sprintf("sink\x00%s\x00%t", sink, r.sinkConfigured(sink)))
	}
	inputs = append(inputs, fmt.Sprintf("deadline\x00%d", deadline))
	sort.Strings(inputs)
	h := sha256.New()
	for _, in := range inputs {
		_, _ = h.Write([]byte(in))
		_, _ = h.Write([]byte{'\n'})
	}
	out.Fingerprint = hex.EncodeToString(h.Sum(nil))
	return out
}
