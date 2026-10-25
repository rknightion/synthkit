// SPDX-License-Identifier: AGPL-3.0-only

package agentcore

import (
	"fmt"

	"github.com/rknightion/synthkit/internal/causal"
)

var _ causal.BindingDeclarer = (*Config)(nil)

// ResourceBindings reports each runtime bound to a concurrency resource, identified by the
// runtime ARN its invocation-class series carry as their name label (causal.BindingDeclarer).
func (c *Config) ResourceBindings(ctx causal.BindContext) []causal.Binding {
	var out []causal.Binding
	for _, rt := range c.Runtimes {
		if rt.Resource != "" {
			out = append(out, causal.Binding{Resource: rt.Resource, Identity: map[string]string{
				"name": fmt.Sprintf("arn:aws:bedrock-agentcore:%s:%s:runtime/%s", ctx.Region, ctx.AccountID, rt.Agent),
			}})
		}
	}
	return out
}
