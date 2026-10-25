// SPDX-License-Identifier: AGPL-3.0-only

package solace

import "github.com/rknightion/synthkit/internal/causal"

var _ causal.BindingDeclarer = (*Config)(nil)

// ResourceBindings reports each queue bound to a queue resource, identified by the scrape
// instance, message VPN and queue name its series carry (causal.BindingDeclarer).
func (c *Config) ResourceBindings(causal.BindContext) []causal.Binding {
	var out []causal.Binding
	for _, b := range c.Brokers {
		instance := b.Instance
		if instance == "" {
			instance = b.Name + ":" + defaultPort
		}
		for _, v := range b.VPNs {
			for _, q := range v.Queues {
				if q.Resource != "" {
					out = append(out, causal.Binding{Resource: q.Resource, Identity: map[string]string{
						"instance":   instance,
						"vpn_name":   v.Name,
						"queue_name": q.Name,
					}})
				}
			}
		}
	}
	return out
}
