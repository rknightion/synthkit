// SPDX-License-Identifier: AGPL-3.0-only

package datadogreceiver

import "github.com/rknightion/synthkit/internal/core"

// Registration declares the isolated construct. The composition root owns registration,
// producer attribution, the native-verdict guard, and blueprint decoding.
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      Kind,
		Doc:       "Kubernetes Datadog Agent to Alloy Datadog receiver native metric egress",
		Scope:     core.ScopeSubstrate,
		NewConfig: func() any { return &Config{} },
		Build:     Build,
	}
}
