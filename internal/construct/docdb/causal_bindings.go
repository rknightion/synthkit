// SPDX-License-Identifier: AGPL-3.0-only

package docdb

import "github.com/rknightion/synthkit/internal/causal"

var _ causal.BindingDeclarer = (*Config)(nil)

// ResourceBindings reports the cluster's bound resource, identified by the cluster identifier its
// CloudWatch series carry (causal.BindingDeclarer).
func (c *Config) ResourceBindings(ctx causal.BindContext) []causal.Binding {
	if c.resource == "" {
		return nil
	}
	return []causal.Binding{{Resource: c.resource, Identity: map[string]string{"dimension_DBClusterIdentifier": ctx.Instance}}}
}
