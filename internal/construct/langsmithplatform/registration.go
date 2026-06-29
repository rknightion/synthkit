// SPDX-License-Identifier: AGPL-3.0-only

package langsmithplatform

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "langsmith_platform",
		Doc:       "LangSmith platform /metrics scrape — standard process/python/ClickHouse/redis/pg/nginx exporters",
		Scope:     core.ScopeSubstrate,
		Group:     core.GroupIntegration,
		NewConfig: NewConfig,
		Build:     Build,
	}
}
