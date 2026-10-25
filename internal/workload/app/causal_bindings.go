// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"slices"

	"github.com/rknightion/synthkit/internal/causal"
)

var _ causal.BindingDeclarer = (*Config)(nil)

// ResourceBindings reports each service node's `uses` entry with the identity its span-metric
// series carry and the services that call it, directly or transitively (causal.BindingDeclarer).
// A node with no server span (a db or external leaf) has no latency series of its own: the
// stretch it causes shows on its callers, which is why they are listed.
func (c *Config) ResourceBindings(ctx causal.BindContext) []causal.Binding {
	callers := serviceCallers(c.Services)
	var out []causal.Binding
	for _, sn := range c.Services {
		for _, use := range sn.Uses {
			id := map[string]string{"service": sn.Name}
			if ctx.Env != "" {
				id["deployment_environment_name"] = ctx.Env
			}
			out = append(out, causal.Binding{Resource: use, Service: sn.Name, Callers: callers[sn.Name], Identity: id})
		}
	}
	return out
}

// serviceCallers maps each service to the sorted services that reach it through `calls`,
// directly or transitively (never itself).
func serviceCallers(services []ServiceNode) map[string][]string {
	direct := map[string][]string{}
	for _, sn := range services {
		for _, callee := range sn.Calls {
			direct[callee] = append(direct[callee], sn.Name)
		}
	}
	out := map[string][]string{}
	for _, sn := range services {
		seen := map[string]bool{sn.Name: true}
		queue := []string{sn.Name}
		var all []string
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, caller := range direct[cur] {
				if !seen[caller] {
					seen[caller] = true
					all = append(all, caller)
					queue = append(queue, caller)
				}
			}
		}
		if len(all) > 0 {
			slices.Sort(all)
			out[sn.Name] = all
		}
	}
	return out
}
