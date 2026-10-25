// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import "github.com/rknightion/synthkit/internal/causal"

// ResolvedResource is one declared causal resource with the consumers the blueprint binds to it.
type ResolvedResource struct {
	Name string
	Kind causal.Kind
	// Env is the environment that declares the resource.
	Env string
	// Alias is the base name that also targets the resource (causal.EnvAlias), "" for none.
	Alias string
	// HeldAcross is the dependency a pool, concurrency or queue resource holds its work across.
	HeldAcross string
	// Consumers lists the constructs, then the workload service nodes, bound to the resource, in
	// declaration order. Never nil.
	Consumers []ResourceConsumer
}

// ResourceConsumer is one construct or workload service bound to a resource.
type ResourceConsumer struct {
	// Kind and Name are the construct or workload kind and instance name.
	Kind, Name string
	// Service and Callers are the app service node that uses the resource and the services that
	// call it; empty for a construct.
	Service  string
	Callers  []string
	Identity map[string]string
}

// resolvedResources lists the declared resources with their bound consumers. Consumers report
// their own bindings (causal.BindingDeclarer); each binding is resolved for the consumer's
// environment the way the consumer resolves it when it builds, so the resource named here is the
// one the consumer drives. A binding naming no declared resource is skipped: the consumer's Build
// reports it.
func resolvedResources(set causal.Set, r *Resolved) []ResolvedResource {
	var out []ResolvedResource
	index := map[string]int{}
	for _, d := range set.Envs {
		for _, res := range d.Resources {
			index[res.Name] = len(out)
			out = append(out, ResolvedResource{
				Name: res.Name, Kind: res.Kind, Env: d.Env,
				Alias: causal.EnvAlias(res.Name, d.Env), HeldAcross: res.HeldAcross,
				Consumers: []ResourceConsumer{},
			})
		}
	}
	bind := func(bd causal.BindingDeclarer, ctx causal.BindContext, kind, name string) {
		for _, b := range bd.ResourceBindings(ctx) {
			res, _, ok := set.Resolve(b.Resource, ctx.Env)
			if !ok {
				continue
			}
			i := index[res.Name]
			out[i].Consumers = append(out[i].Consumers, ResourceConsumer{
				Kind: kind, Name: name, Service: b.Service, Callers: b.Callers, Identity: b.Identity,
			})
		}
	}
	for _, ci := range r.Constructs {
		bd, ok := ci.Config.(causal.BindingDeclarer)
		if !ok {
			continue
		}
		ctx := causal.BindContext{Instance: ci.Name}
		if fx := ci.Fixtures; fx != nil {
			if fx.Env != nil {
				ctx.Env = fx.Env.Name
			}
			if fx.Cloud != nil {
				ctx.Region, ctx.AccountID = fx.Cloud.Region, fx.Cloud.AccountID
			}
		}
		bind(bd, ctx, ci.Kind, ci.Name)
	}
	for _, wi := range r.Workloads {
		bd, ok := wi.Config.(causal.BindingDeclarer)
		if !ok {
			continue
		}
		ctx := causal.BindContext{Instance: wi.Name}
		if wi.Env != nil {
			ctx.Env = wi.Env.Name
		}
		bind(bd, ctx, wi.Kind, wi.Name)
	}
	return out
}
