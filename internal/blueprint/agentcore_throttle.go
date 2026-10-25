// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"fmt"
	"slices"
	"strings"

	"github.com/rknightion/synthkit/internal/causal"
)

// validateAgentCoreThrottle rejects a legacy cloud effect reaching a bound runtime.
// Check both the declared environment target and the renderer's region scope: a
// mixed deployment cannot silently apply the story only to its global series.
// Bindings come from the construct contract, without importing its config type.
func validateAgentCoreThrottle(r *Resolved, e EffectDecl) error {
	if e.Mode != "agentcore_throttle" {
		return nil
	}
	scopes := expandScopes(e.Target, r.Targets)
	if e.Target != "" {
		// Validate the expanded names, not just the authored selector (cloud:*).
		// shape.New trims Unicode whitespace and splits at the last '/' and
		// '@'. Never let that turn a named scope into an unscoped incident,
		// another target, or a skipped entry. scopeSafe alone only checks ASCII
		// whitespace. Keep this fence local to legacy AgentCore admission.
		for _, scope := range scopes {
			if !scopeSafe(scope) || strings.TrimSpace(scope) != scope {
				return fmt.Errorf("effect %q: target %q expands to invalid scheduler scope %q; use a cloud environment name without scheduler delimiters, reserved wildcard suffix or whitespace altered by schedule parsing", e.Mode, e.Target, scope)
			}
		}
	}
	for _, ci := range r.Constructs {
		if ci.Kind != KindAgentCore || ci.Fixtures == nil || ci.Fixtures.Cloud == nil {
			continue
		}
		ctx := causal.BindContext{Instance: ci.Name, Region: ci.Fixtures.Cloud.Region, AccountID: ci.Fixtures.Cloud.AccountID}
		if ci.Fixtures.Env != nil {
			ctx.Env = ci.Fixtures.Env.Name
		}
		if e.Target != "" && !slices.Contains(scopes, ctx.Env) && !slices.Contains(scopes, ctx.Region) {
			continue
		}
		bd, ok := ci.Config.(causal.BindingDeclarer)
		if !ok {
			continue
		}
		for _, b := range bd.ResourceBindings(ctx) {
			if b.Resource == "" {
				continue
			}
			return fmt.Errorf("effect %q: target %q reaches bound AgentCore runtime %q in environment %q (resource %q); use concurrency_reduced on its concurrency resource or a resource-axis fault on its dependency instead", e.Mode, e.Target, b.Identity["name"], ctx.Env, b.Resource)
		}
	}
	return nil
}
