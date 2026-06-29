// SPDX-License-Identifier: AGPL-3.0-only

package portkeypoller

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:         "portkey_poller",
		Doc:          "Portkey Analytics API poll → portkey_api_* windowed-aggregate gauges + poller_* self-telemetry",
		Scope:        core.ScopeSubstrate,
		Group:        core.GroupIntegration,
		NewConfig:    func() any { return &Config{} },
		Build:        Build,
		FailureModes: FailureModes,
	}
}
