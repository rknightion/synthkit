// SPDX-License-Identifier: AGPL-3.0-only

package lambda

import "github.com/rknightion/synthkit/internal/causal"

var _ causal.BindingDeclarer = (*Config)(nil)

// ResourceBindings reports each function bound to a concurrency resource, identified by the
// FunctionName dimension its CloudWatch series carry (causal.BindingDeclarer).
func (c *Config) ResourceBindings(causal.BindContext) []causal.Binding {
	var out []causal.Binding
	for _, f := range c.Functions {
		if f.Resource != "" {
			out = append(out, causal.Binding{Resource: f.Resource, Identity: map[string]string{"dimension_FunctionName": f.Name}})
		}
	}
	return out
}
