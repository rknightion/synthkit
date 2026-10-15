// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"github.com/rknightion/synthkit/internal/construct/dcgm"
	"github.com/rknightion/synthkit/internal/core"
)

func registerAIFDcgm(reg *core.Registry) {
	reg.RegisterConstruct(withMetricProducer(core.ConstructReg{
		Kind: "dcgm", Doc: "Pinned H100 SXM HGX DCGM exporter telemetry",
		Scope: core.ScopeSubstrate, Group: core.GroupIntegration,
		NewConfig: func() any { return &dcgm.Config{} }, Build: dcgm.New,
		FailureModes: dcgm.FailureModes(),
	}, producerPromRW))
}
