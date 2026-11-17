// SPDX-License-Identifier: AGPL-3.0-only

package dynamodb

import "github.com/rknightion/synthkit/internal/causal"

var _ causal.BindingDeclarer = (*Config)(nil)

// ResourceBindings reports each operation bound to a quota resource, identified by the
// account_id, region, collector name and TableName+Operation dimensions its ThrottledRequests and
// SuccessfulRequestLatency series carry (causal.BindingDeclarer). A same-named table in another
// account or region is another table, so account and region are part of the identity. A label the
// context does not carry is absent, never empty.
func (c *Config) ResourceBindings(ctx causal.BindContext) []causal.Binding {
	var out []causal.Binding
	for _, t := range c.Tables {
		for _, o := range t.Operations {
			if o.Resource == "" {
				continue
			}
			id := map[string]string{
				"dimension_TableName": t.Name,
				"dimension_Operation": o.Name,
				"name":                t.CollectorName,
			}
			if ctx.AccountID != "" {
				id["account_id"] = ctx.AccountID
			}
			if ctx.Region != "" {
				id["region"] = ctx.Region
			}
			out = append(out, causal.Binding{Resource: o.Resource, Identity: id})
		}
	}
	return out
}
