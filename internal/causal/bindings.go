// SPDX-License-Identifier: AGPL-3.0-only

package causal

// Binding is one declared resource a consumer is bound to, with the identity the consumer's own
// telemetry carries. No emitted series carries a resource name, so a client that wants to observe
// what a resource fault moved (the live scenario check) reads the consumer's identity from the
// control schema instead.
type Binding struct {
	// Resource is the resource name as the consumer declares it, before the environment rule
	// (Set.Resolve): the resolver resolves it for the consumer's environment.
	Resource string
	// Service is the app service node that uses the resource; empty for a construct.
	Service string
	// Callers are the app services that call Service, directly or transitively. A request
	// through them includes Service's hop, so a fault that stretches Service stretches their
	// server-span latency too.
	Callers []string
	// Identity is the consumer's emitted identity as label name to value, exactly as its series
	// carry the label. Labels the consumer does not stamp are absent, never empty.
	Identity map[string]string
}

// BindContext carries what a consumer cannot know from its own Config.
type BindContext struct {
	// Instance is the resolver's name for the construct or workload instance (a database's name
	// for the rds and docdb constructs, which have no YAML of their own).
	Instance string
	// Env is the consumer's resolved environment name; empty when it has none.
	Env string
	// Region and AccountID are the consumer's cloud fixture, empty when it has none.
	Region, AccountID string
}

// BindingDeclarer is implemented by a construct or workload Config that binds to declared
// resources. The resolver calls ResourceBindings after UseResources, only when the blueprint
// declares at least one resource, and publishes the result in the control schema. It is
// read-only: it reports declarations and changes no state.
type BindingDeclarer interface {
	ResourceBindings(BindContext) []Binding
}
