// SPDX-License-Identifier: AGPL-3.0-only

// Package causal is the shared mechanic for blueprint-declared request resources and the faults
// that perturb them. It owns no metric, label, log or span name. Deterministic: no wall clock,
// no global RNG; state advances on an epoch-anchored grid.
//
// A blueprint declares resources once per environment (connection pools, concurrency limits,
// queues, rate quotas, dependencies, credentials, validation rules). The composition root hands
// the blueprint's Set to every consumer whose Config implements Consumer. Each consumer compiles
// its own Model and advances it on the same 5 s grid with the blueprint's shape engine, so two
// consumers ticking at different cadences compute identical states at every grid point:
// cross-construct agreement comes from determinism, not shared state (ARCHITECTURE I42).
//
// It is a peer library like internal/pipelinerun: catalog packages may import it, and it imports
// only the failuremode leaf and the standard library.
package causal

import (
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/failuremode"
)

// AxisResource is the failure-mode axis for one declared resource. Target names on it are the
// declared resource names.
const AxisResource failuremode.Axis = "resource"

// Failure-mode names on AxisResource. None collides with a mode on another axis, so an empty
// effect target keeps resolving to this axis alone.
const (
	ModePoolExhausted         = "pool_exhausted"
	ModeConcurrencyReduced    = "concurrency_reduced"
	ModeQuotaReduced          = "quota_reduced"
	ModeConsumerSlowdown      = "consumer_slowdown"
	ModeDependencySlow        = "dependency_slow"
	ModeDependencyUnavailable = "dependency_unavailable"
	ModeCredentialExpired     = "credential_expired"
	ModeRuleRegression        = "rule_regression"
)

// FailureModes is the resource vocabulary every causal consumer registers.
var FailureModes = []failuremode.Mode{
	{Name: ModePoolExhausted, Axis: AxisResource, Help: "pool: effective size shrinks to size x (1 - intensity) (connections leaked or held elsewhere)"},
	{Name: ModeConcurrencyReduced, Axis: AxisResource, Help: "concurrency: effective limit shrinks to size x (1 - intensity)"},
	{Name: ModeQuotaReduced, Axis: AxisResource, Help: "quota: admitted rate shrinks to limit_rps x (1 - intensity)"},
	{Name: ModeConsumerSlowdown, Axis: AxisResource, Help: "queue: service rate scales by (1 - intensity); backlog and oldest-message age grow"},
	{Name: ModeDependencySlow, Axis: AxisResource, Help: "dependency: latency factor 1 + (slow_factor - 1) x intensity; held resources stretch with it"},
	{Name: ModeDependencyUnavailable, Axis: AxisResource, Help: "dependency: share intensity of calls fail to connect"},
	{Name: ModeCredentialExpired, Axis: AxisResource, Help: "credential: share intensity of calls to its dependency fail at handshake or authentication"},
	{Name: ModeRuleRegression, Axis: AxisResource, Help: "rule: share intensity x reject_share of arrivals fail validation"},
}

// modeKinds is the one resource kind each AxisResource mode acts on. A fault aimed at a resource
// of any other kind moves nothing, so the blueprint loader rejects the pairing.
var modeKinds = map[string]Kind{
	ModePoolExhausted:         KindPool,
	ModeConcurrencyReduced:    KindConcurrency,
	ModeQuotaReduced:          KindQuota,
	ModeConsumerSlowdown:      KindQueue,
	ModeDependencySlow:        KindDependency,
	ModeDependencyUnavailable: KindDependency,
	ModeCredentialExpired:     KindCredential,
	ModeRuleRegression:        KindRule,
}

// ModeKind returns the resource kind a mode of FailureModes acts on; ok is false for a mode
// that is not a resource-axis mode.
func ModeKind(mode string) (Kind, bool) {
	k, ok := modeKinds[mode]
	return k, ok
}

// Step is the grid step. Grid points are t_k = k x Step since the Unix epoch, independent of
// which consumer advances the model or how often.
const Step = 5 * time.Second

// Kind is a declared resource kind.
type Kind string

// Resource kinds.
const (
	KindPool        Kind = "pool"
	KindConcurrency Kind = "concurrency"
	KindQueue       Kind = "queue"
	KindQuota       Kind = "quota"
	KindDependency  Kind = "dependency"
	KindCredential  Kind = "credential"
	KindRule        Kind = "rule"
)

// Declaration is one environment's resources plus the environment facts demand needs.
type Declaration struct {
	Env       string
	Weight    float64
	NonProd   bool
	Resources []Resource
}

// Set is every environment's declarations of one blueprint plus the retrying caller edges into
// its resources. Resource names are unique blueprint-wide (Validate enforces it). The composition
// root hands one Set to every consumer, so a Set, its slices and the pointers inside its resources are
// read-only: Compile reads it without modifying it and no consumer may mutate it. The one exception
// is a queue's message exchange (QueueExchange), a lazily created pointer private to the Set's data.
type Set struct {
	Envs  []Declaration
	Loads []Load
}

// Load is one retrying caller edge into a capacity resource: Share of the resource's demand
// arrives through callers using Policy, and each such arrival is resent when rejected with a
// class the policy retries. The composition root collects loads from every LoadDeclarer before
// injecting the Set, so every consumer compiles the same closed-loop demand.
type Load struct {
	Resource string
	Share    float64 // in (0,1]; a resource's shares sum to at most 1
	Policy   CallPolicy
}

// LoadDeclarer is implemented by a workload Config whose declared call graph sends traffic
// through resources (the app workload). The resolver calls CausalLoads once per workload
// instance with the declared set (without loads) and the instance's resolved environment name
// ("" when it has none), after strict decode and before UseResources. It resolves its resource
// names with Set.Resolve(name, env) and returns the retrying edges and its traffic share of each
// resource it reaches, or an error for a binding it cannot honour.
type LoadDeclarer interface {
	CausalLoads(s Set, env string) ([]Load, error)
}

// Advancer is implemented by a built construct or workload that holds a Model. The composition
// root calls AdvanceCausal on every master tick for every causal consumer of the blueprint, so
// all consumers sample live faults at the same instants and agree at every grid point.
type Advancer interface {
	AdvanceCausal(now time.Time, c Clock)
}

// Empty reports whether the set declares no resource at all.
func (s Set) Empty() bool {
	for _, d := range s.Envs {
		if len(d.Resources) > 0 {
			return false
		}
	}
	return true
}

// Lookup finds a resource by its exact name.
func (s Set) Lookup(name string) (Resource, Declaration, bool) {
	for _, d := range s.Envs {
		for _, r := range d.Resources {
			if r.Name == name {
				return r, d, true
			}
		}
	}
	return Resource{}, Declaration{}, false
}

// Resolve applies the db_instance-style environment rule: "<name>-<lower(env)>" first, then the
// exact name.
func (s Set) Resolve(name, env string) (Resource, Declaration, bool) {
	if env != "" {
		if r, d, ok := s.Lookup(name + "-" + strings.ToLower(env)); ok {
			return r, d, true
		}
	}
	return s.Lookup(name)
}

// CredentialsOf returns the credentials that authenticate the named dependency, in
// declaration order.
func (s Set) CredentialsOf(dependency string) []Resource {
	var out []Resource
	for _, d := range s.Envs {
		for _, r := range d.Resources {
			if r.Kind == KindCredential && r.Of == dependency {
				out = append(out, r)
			}
		}
	}
	return out
}

// Consumer is implemented by a construct or workload Config that reads the declared resources.
// The blueprint resolver calls UseResources once, after strict decode and before Build, and
// only when the blueprint declares at least one resource. The Set is shared with every other
// consumer of the blueprint and is read-only: keep it or compile it, never modify it or its slices.
type Consumer interface{ UseResources(Set) }

// Clock is the shape-engine surface the model reads. *shape.Engine satisfies it. The model never
// calls the engine's RNG, so advancing it shifts no other draw.
type Clock interface {
	Eval(now time.Time, mode, scope string) (bool, float64)
	Factor(now time.Time, weight float64, nonProd bool) float64
	VolumeMultiplier() float64
}

// scheduledView is a Clock whose Eval reads scheduled windows alone and whose other methods
// are the underlying Clock's.
type scheduledView struct {
	Clock
	sc ScheduledClock
}

func (v scheduledView) Eval(now time.Time, mode, scope string) (bool, float64) {
	return v.sc.EvalScheduled(now, mode, scope)
}

// ScheduledClock is optionally implemented by a Clock that can answer Eval from its scheduled
// incident windows alone, without live control-plane state. Warm start uses it for the part of
// the Horizon older than the retained history, so a live fault active at restart is applied to
// the final stretch only. *shape.Engine satisfies it. A Clock without it keeps the plain Eval.
type ScheduledClock interface {
	EvalScheduled(now time.Time, mode, scope string) (bool, float64)
}
