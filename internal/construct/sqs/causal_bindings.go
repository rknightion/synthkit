// SPDX-License-Identifier: AGPL-3.0-only

package sqs

import "github.com/rknightion/synthkit/internal/causal"

var _ causal.BindingDeclarer = (*Config)(nil)

// ResourceBindings reports each queue bound to a queue resource, identified by the QueueName
// dimension and the collector name its CloudWatch series carry (causal.BindingDeclarer).
func (c *Config) ResourceBindings(causal.BindContext) []causal.Binding {
	var out []causal.Binding
	for _, q := range c.Queues {
		if q.Resource != "" {
			out = append(out, causal.Binding{Resource: q.Resource, Identity: map[string]string{
				"dimension_QueueName": q.Name,
				"name":                q.CollectorName,
			}})
		}
	}
	return out
}
