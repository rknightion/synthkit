// SPDX-License-Identifier: AGPL-3.0-only

package lambda

import "github.com/rknightion/synthkit/internal/causal"

var _ causal.BindingDeclarer = (*Config)(nil)

// ResourceBindings reports each function bound to a concurrency resource, identified by the
// account_id, region and FunctionName dimension its CloudWatch series carry
// (causal.BindingDeclarer). A same-named function in another account or region is another
// function, so account and region are part of the identity. A label the context does not carry is
// absent, never empty.
func (c *Config) ResourceBindings(ctx causal.BindContext) []causal.Binding {
	var out []causal.Binding
	for _, f := range c.Functions {
		if f.Resource != "" {
			id := map[string]string{"dimension_FunctionName": f.Name}
			if ctx.AccountID != "" {
				id["account_id"] = ctx.AccountID
			}
			if ctx.Region != "" {
				id["region"] = ctx.Region
			}
			out = append(out, causal.Binding{Resource: f.Resource, Identity: id})
		}
	}
	return out
}
