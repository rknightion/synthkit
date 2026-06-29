// SPDX-License-Identifier: AGPL-3.0-only

package snowflake

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      Kind,
		Doc:       "Snowflake account telemetry (prometheus.exporter.snowflake → 27 gauges from ACCOUNT_USAGE)",
		Scope:     core.ScopeSubstrate,
		Group:     core.GroupIntegration,
		NewConfig: func() any { return &Config{} },
		Build:     Build,
	}
}
