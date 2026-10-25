// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
)

// Resource is one declared resource; it is also the blueprint YAML shape
// (`environments[].resources[]`). Fields a kind does not use must be omitted.
type Resource struct {
	// Name is the resource identity and its incident target on axis `resource`. Unique across
	// the whole blueprint. A name of the form `<base>-<lower(env)>` is also reachable through the
	// base name, which then targets that resource in every environment.
	Name string `yaml:"name"`
	// Kind is pool, concurrency, queue, quota, dependency, credential or rule.
	Kind Kind `yaml:"kind"`
	// Demand is the offered load in requests (or messages) per second, or the upstream resource
	// it comes from. Required for pool, concurrency, queue and quota. It must cover every request
	// that passes through the resource; callers' retries add to it.
	Demand *Demand `yaml:"demand"`
	// Size is the connection count (pool), concurrency limit (concurrency) or consumer count
	// (queue). Integer >= 1.
	Size int `yaml:"size"`
	// HoldMS is the mean hold, execution or processing time per request at baseline, in ms,
	// including the held-across dependency's baseline latency, so it is at least that latency_ms.
	// Required for pool, concurrency and queue.
	HoldMS float64 `yaml:"hold_ms"`
	// HeldAcross names a dependency resource (pool, concurrency, queue; optional). The effective
	// hold is hold_ms + latency_ms(dependency) x (latency factor - 1), and the dependency's
	// unavailability and its credentials' failures fail admitted work.
	HeldAcross string `yaml:"held_across"`
	// AcquireTimeoutMS bounds how long a request waits for a pool connection before the
	// acquisition fails. Required for pool.
	AcquireTimeoutMS float64 `yaml:"acquire_timeout_ms"`
	// LimitRPS is the admitted rate of a quota. Required for quota.
	LimitRPS float64 `yaml:"limit_rps"`
	// MaxDepth bounds a queue's backlog in messages; arrivals beyond it are rejected. Omitted
	// means unbounded.
	MaxDepth float64 `yaml:"max_depth"`
	// LatencyMS is a dependency's baseline latency in ms. It is the base duration of a node bound
	// to the dependency, and the unit by which dependency_slow stretches every pool, concurrency
	// or queue held across it: effective hold = hold_ms + latency_ms x (latency factor - 1).
	// Must be > 0 when any resource names the dependency in held_across.
	LatencyMS float64 `yaml:"latency_ms"`
	// SlowFactor is a dependency's latency multiplier under dependency_slow at intensity 1.
	// Default 4, at least 1.
	SlowFactor float64 `yaml:"slow_factor"`
	// Of names the dependency a credential authenticates. Required for credential.
	Of string `yaml:"of"`
	// CredentialType is server_certificate, client_certificate or token. Required for credential.
	CredentialType string `yaml:"credential_type"`
	// RejectShare is the share of arrivals a fully regressed rule rejects, in (0,1]. Required
	// for rule.
	RejectShare float64 `yaml:"reject_share"`
	// Knee is the utilization where waiting begins (pool, queue). Default 0.8, in (0,1).
	Knee float64 `yaml:"knee"`
	// Error optionally overrides the default failure status, error type, exception type and
	// message. Values only, never names.
	Error *ErrorTemplate `yaml:"error"`
}

// Demand is a resource's offered load.
type Demand struct {
	// OffPeakRPS is the rate at the bottom of the business curve.
	OffPeakRPS float64 `yaml:"off_peak_rps"`
	// PeakRPS is the rate at the top of the business curve.
	PeakRPS float64 `yaml:"peak_rps"`
	// Load is business (default: the environment's diurnal and weekly curve, as the app
	// workload's traffic) or constant (peak_rps scaled by the volume multiplier).
	Load string `yaml:"load"`
	// From names an upstream capacity resource (pool, concurrency, queue or quota) whose served
	// rate is this resource's demand (a queue's processed rate, the others' admitted rate). With
	// from, off_peak_rps, peak_rps and load are omitted.
	From string `yaml:"from"`

	// A queue's message exchange (exchange.go) is created on first use and lives here, on the
	// declaration the resolver shares with every consumer of the blueprint.

	exchange atomic.Pointer[Exchange]
}

// ErrorTemplate overrides the default failure presentation of a resource.
type ErrorTemplate struct {
	// Status is the HTTP status of the failure, 400 to 599. Not allowed on a dependency or a
	// certificate credential (their failures have no HTTP status).
	Status int `yaml:"status"`
	// Type is the error.type value.
	Type string `yaml:"type"`
	// Exception is the exception.type value; empty means none unless Type is class-like.
	Exception string `yaml:"exception"`
	// Message is the human message naming the cause.
	Message string `yaml:"message"`
}

// Load values.
const (
	LoadBusiness = "business"
	LoadConstant = "constant"
)

// Credential types.
const (
	CredentialServerCertificate = "server_certificate"
	CredentialClientCertificate = "client_certificate"
	CredentialToken             = "token"
)

const (
	defaultKnee       = 0.8
	defaultSlowFactor = 4.0
)

// namePattern is a strict subset of the incident-scope grammar: no '@', '/', '#', whitespace or
// ':' can appear, so every declared name is a schedulable scope.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

var kinds = map[Kind]bool{
	KindPool: true, KindConcurrency: true, KindQueue: true, KindQuota: true,
	KindDependency: true, KindCredential: true, KindRule: true,
}

// Capacity reports whether the kind carries demand (pool, concurrency, queue, quota).
func (k Kind) Capacity() bool {
	return k == KindPool || k == KindConcurrency || k == KindQueue || k == KindQuota
}

func (k Kind) holds() bool { return k == KindPool || k == KindConcurrency || k == KindQueue }

// Validate checks one blueprint's set: kinds, required fields, ranges, references, acyclicity,
// blueprint-wide unique names. The resolver calls it; Compile calls it again.
func Validate(s Set) error {
	byName := map[string]Resource{}
	owner := map[string]string{}
	for _, d := range s.Envs {
		for _, r := range d.Resources {
			if !namePattern.MatchString(r.Name) {
				return fmt.Errorf("env %q: resource name %q must match %s", d.Env, r.Name, namePattern)
			}
			if prev, dup := owner[r.Name]; dup {
				return fmt.Errorf("resource %q declared by environments %q and %q: resource names must be unique across the blueprint", r.Name, prev, d.Env)
			}
			owner[r.Name] = d.Env
			byName[r.Name] = r
		}
	}
	for _, d := range s.Envs {
		for _, r := range d.Resources {
			if err := validateResource(r, byName); err != nil {
				return fmt.Errorf("env %q: resource %q: %w", d.Env, r.Name, err)
			}
		}
	}
	// held_across and of only point at dependencies, which reference nothing; demand.from links
	// capacity resources and must not loop.
	// The error names the smallest resource name on the cycle, found by walking names in order, so
	// it is the same on every run (a resource merely feeding a cycle is never named).
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		seen := map[string]bool{}
		for cur := name; cur != ""; cur = demandFrom(byName[cur]) {
			if seen[cur] {
				return fmt.Errorf("resource %q: demand.from cycle", cycleMin(cur, byName))
			}
			seen[cur] = true
		}
	}
	return validateLoads(s.Loads, byName)
}

// demandFrom is the upstream a resource's demand comes from, "" for none.
func demandFrom(r Resource) string {
	if r.Demand == nil {
		return ""
	}
	return r.Demand.From
}

// cycleMin is the smallest name on the demand.from cycle that passes through start.
func cycleMin(start string, byName map[string]Resource) string {
	lo := start
	for cur := demandFrom(byName[start]); cur != start; cur = demandFrom(byName[cur]) {
		lo = min(lo, cur)
	}
	return lo
}

func validateLoads(loads []Load, byName map[string]Resource) error {
	sum := map[string]float64{}
	for _, l := range loads {
		r, ok := byName[l.Resource]
		switch {
		case !ok:
			return fmt.Errorf("retry load names unknown resource %q", l.Resource)
		case !r.Kind.Capacity():
			return fmt.Errorf("retry load on %q: a %s carries no demand", l.Resource, r.Kind)
		case math.IsNaN(l.Share) || l.Share <= 0 || l.Share > 1:
			return fmt.Errorf("retry load on %q: share %v must be in (0,1]", l.Resource, l.Share)
		}
		if err := l.Policy.Validate(); err != nil {
			return fmt.Errorf("retry load on %q: %w", l.Resource, err)
		}
		if l.Flow != nil {
			if err := validateFlow(l.Flow, byName); err != nil {
				return fmt.Errorf("retry load on %q: %w", l.Resource, err)
			}
			if l.Node < 0 || l.Node >= len(l.Flow.Nodes) || !slices.Contains(l.Flow.Nodes[l.Node].Uses, l.Resource) {
				return fmt.Errorf("retry load on %q: flow node %d does not use it", l.Resource, l.Node)
			}
			if np := l.Flow.Nodes[l.Node].Policy; !samePolicy(np, l.Policy) {
				return fmt.Errorf("retry load on %q: policy differs from flow node %d's call policy", l.Resource, l.Node)
			}
		}
		sum[l.Resource] += l.Share
		if sum[l.Resource] > 1+1e-9 {
			return fmt.Errorf("resource %q: callers' traffic shares sum to %v, more than its declared demand", l.Resource, sum[l.Resource])
		}
	}
	return nil
}

// samePolicy reports whether two call policies resend the same way.
func samePolicy(a, b CallPolicy) bool {
	return a.TimeoutMS == b.TimeoutMS && a.Retries == b.Retries && a.BackoffMS == b.BackoffMS &&
		a.PropagateThrottle == b.PropagateThrottle && slices.Equal(a.RetryOn, b.RetryOn)
}

func validateResource(r Resource, byName map[string]Resource) error {
	if !kinds[r.Kind] {
		return fmt.Errorf("unknown kind %q (pool, concurrency, queue, quota, dependency, credential, rule)", r.Kind)
	}
	var errs []string
	bad := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }
	unused := func(field string, set bool) {
		if set {
			bad("%s is not a %s field", field, r.Kind)
		}
	}
	finite := func(field string, v float64) bool {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			bad("%s must be finite", field)
			return false
		}
		return true
	}

	// demand
	if r.Kind.Capacity() {
		if r.Demand == nil {
			bad("demand is required")
		} else if dm := r.Demand; dm.From != "" {
			if up, ok := byName[dm.From]; !ok {
				bad("demand.from %q names no declared resource", dm.From)
			} else if !up.Kind.Capacity() {
				bad("demand.from %q is a %s, not a pool, concurrency, queue or quota", dm.From, up.Kind)
			} else if dm.From == r.Name {
				bad("demand.from names the resource itself")
			}
			if dm.OffPeakRPS != 0 || dm.PeakRPS != 0 || dm.Load != "" {
				bad("demand.from excludes off_peak_rps, peak_rps and load")
			}
		} else {
			dm := r.Demand
			if finite("demand.off_peak_rps", dm.OffPeakRPS) && finite("demand.peak_rps", dm.PeakRPS) {
				if dm.PeakRPS <= 0 {
					bad("demand.peak_rps must be > 0")
				}
				if dm.OffPeakRPS < 0 || dm.OffPeakRPS > dm.PeakRPS {
					bad("demand.off_peak_rps must be in [0, peak_rps]")
				}
			}
			if dm.Load != "" && dm.Load != LoadBusiness && dm.Load != LoadConstant {
				bad("demand.load %q must be business or constant", dm.Load)
			}
		}
	} else {
		unused("demand", r.Demand != nil)
	}

	// size, hold_ms, held_across
	if r.Kind.holds() {
		if r.Size < 1 {
			bad("size must be an integer >= 1")
		}
		if finite("hold_ms", r.HoldMS) && r.HoldMS <= 0 {
			bad("hold_ms must be > 0")
		}
		if r.HeldAcross != "" {
			if dep, ok := byName[r.HeldAcross]; !ok {
				bad("held_across %q names no declared resource", r.HeldAcross)
			} else if dep.Kind != KindDependency {
				bad("held_across %q is a %s, not a dependency", r.HeldAcross, dep.Kind)
			} else if !(dep.LatencyMS > 0) { // also rejects NaN, which the dependency's own check reports
				bad("held_across %q: its latency_ms must be > 0 (dependency_slow stretches the hold by latency_ms)", r.HeldAcross)
			} else if r.HoldMS < dep.LatencyMS {
				bad("hold_ms %v must be >= the held_across %q latency_ms %v (hold_ms includes the dependency's baseline latency)", r.HoldMS, r.HeldAcross, dep.LatencyMS)
			}
		}
	} else {
		unused("size", r.Size != 0)
		unused("hold_ms", r.HoldMS != 0)
		unused("held_across", r.HeldAcross != "")
	}

	// pool
	if r.Kind == KindPool {
		if finite("acquire_timeout_ms", r.AcquireTimeoutMS) && r.AcquireTimeoutMS <= 0 {
			bad("acquire_timeout_ms is required and must be > 0")
		}
	} else {
		unused("acquire_timeout_ms", r.AcquireTimeoutMS != 0)
	}

	// knee
	if r.Kind == KindPool || r.Kind == KindQueue {
		if r.Knee != 0 && (!finite("knee", r.Knee) || r.Knee <= 0 || r.Knee >= 1) {
			bad("knee must be in (0,1)")
		}
	} else {
		unused("knee", r.Knee != 0)
	}

	// quota
	if r.Kind == KindQuota {
		if finite("limit_rps", r.LimitRPS) && r.LimitRPS <= 0 {
			bad("limit_rps is required and must be > 0")
		}
	} else {
		unused("limit_rps", r.LimitRPS != 0)
	}

	// queue
	if r.Kind == KindQueue {
		if finite("max_depth", r.MaxDepth) && r.MaxDepth < 0 {
			bad("max_depth must be >= 0 (omit for unbounded)")
		}
	} else {
		unused("max_depth", r.MaxDepth != 0)
	}

	// dependency
	if r.Kind == KindDependency {
		if finite("latency_ms", r.LatencyMS) && r.LatencyMS < 0 {
			bad("latency_ms must be >= 0")
		}
		if r.SlowFactor != 0 && (!finite("slow_factor", r.SlowFactor) || r.SlowFactor < 1) {
			bad("slow_factor must be >= 1")
		}
	} else {
		unused("latency_ms", r.LatencyMS != 0)
		unused("slow_factor", r.SlowFactor != 0)
	}

	// credential
	if r.Kind == KindCredential {
		if r.Of == "" {
			bad("of is required")
		} else if dep, ok := byName[r.Of]; !ok {
			bad("of %q names no declared resource", r.Of)
		} else if dep.Kind != KindDependency {
			bad("of %q is a %s, not a dependency", r.Of, dep.Kind)
		}
		switch r.CredentialType {
		case CredentialServerCertificate, CredentialClientCertificate, CredentialToken:
		default:
			bad("credential_type %q must be server_certificate, client_certificate or token", r.CredentialType)
		}
	} else {
		unused("of", r.Of != "")
		unused("credential_type", r.CredentialType != "")
	}

	// rule
	if r.Kind == KindRule {
		if !finite("reject_share", r.RejectShare) || r.RejectShare <= 0 || r.RejectShare > 1 {
			bad("reject_share is required and must be in (0,1]")
		}
	} else {
		unused("reject_share", r.RejectShare != 0)
	}

	// error template
	if e := r.Error; e != nil && e.Status != 0 {
		switch {
		case e.Status < 400 || e.Status > 599:
			bad("error.status %d must be in [400, 599]", e.Status)
		case r.Kind == KindDependency:
			bad("error.status is not allowed on a dependency: its failures have no HTTP status")
		case r.Kind == KindCredential && r.CredentialType != CredentialToken:
			bad("error.status is allowed on a credential only for credential_type token")
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// EnvAlias returns the base name a resource is also reachable through: for a name of the form
// `<base>-<lower(env)>` with a non-empty base, base; otherwise "". The resolver publishes each
// alias as a resource-axis target, and the model reads faults scheduled on the alias for every
// resource carrying it, so one base-name target hits that resource in every environment.
func EnvAlias(name, env string) string {
	if env == "" {
		return ""
	}
	suffix := "-" + strings.ToLower(env)
	if base, ok := strings.CutSuffix(name, suffix); ok && base != "" {
		return base
	}
	return ""
}
