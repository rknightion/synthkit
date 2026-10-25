// SPDX-License-Identifier: AGPL-3.0-only

package sqs

import "github.com/rknightion/synthkit/internal/causal"

var _ causal.BindingDeclarer = (*Config)(nil)

// ResourceBindings reports each queue bound to a queue resource, identified by the account_id,
// region, QueueName dimension and collector name its CloudWatch series carry
// (causal.BindingDeclarer). A same-named queue in another account or region is another queue, so
// account and region are part of the identity. A label the context does not carry is absent,
// never empty.
func (c *Config) ResourceBindings(ctx causal.BindContext) []causal.Binding {
	var out []causal.Binding
	for _, q := range c.Queues {
		if q.Resource != "" {
			id := map[string]string{
				"dimension_QueueName": q.Name,
				"name":                q.CollectorName,
			}
			if ctx.AccountID != "" {
				id["account_id"] = ctx.AccountID
			}
			if ctx.Region != "" {
				id["region"] = ctx.Region
			}
			out = append(out, causal.Binding{Resource: q.Resource, Identity: id})
		}
	}
	return out
}
