// SPDX-License-Identifier: AGPL-3.0-only

package datadogreceiver

import (
	"testing"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

func TestHostModeRequiresDeclaredHardware(t *testing.T) {
	for _, tc := range []struct {
		name          string
		cpus, memory  int
		wantRejection bool
	}{
		{"declared", 2, 8, false},
		{"missing cpus", 0, 8, true},
		{"missing memory_gb", 2, 0, true},
		{"negative cpus", -1, 8, true},
	} {
		cfg := hostConfig(t)
		cfg.CPUs, cfg.MemoryGB = tc.cpus, tc.memory
		_, err := Build(cfg, &fixture.Set{})
		if (err != nil) != tc.wantRejection {
			t.Errorf("%s: Build error = %v, want rejection %v", tc.name, err, tc.wantRejection)
		}
	}
	k8s := &Config{HostName: "node", ServiceName: "svc", Source: "agent", IncrementsPerMinute: 6, CPUs: 2}
	if _, err := Build(k8s, &fixture.Set{Cluster: coretest.Cluster()}); err == nil {
		t.Error("Kubernetes mode accepted a host-only cpus declaration")
	}
}
