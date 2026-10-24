// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"slices"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/fixture"
	"gopkg.in/yaml.v3"
)

// sharedKeyFnConfig is a test-only construct config declaring one function target whose name is
// also a GPU target key, as an app service node and a Lambda function can share a name.
type sharedKeyFnConfig struct{}

func (sharedKeyFnConfig) FailureTargets() []failuremode.Target {
	return []failuremode.Target{{Name: "device:pdu-a", Axis: failuremode.AxisFunction}}
}

// gpuSharedKeyDecl is the smallest GPU declaration with a PDU local-collection device whose key
// "device:pdu-a" is also declared as a function target by the test-only construct. withCollection
// controls whether the PDU has a sourced collection profile (a profile makes it a local member of
// the snmp_exporter consumer).
func gpuSharedKeyDecl(t *testing.T, withCollection bool, mode string) (Decl, *core.Registry) {
	t.Helper()
	d := gpuMinimalDecl()
	dev := fixture.GPUDeviceSpec{Name: "pdu-a", Kind: "pdu"}
	if withCollection {
		dev.Collection = &fixture.GPUCollectionSpec{Protocol: "snmp", Module: "test_pdu_profile", Port: 161, Source: gpuTestSource()}
	}
	d.GPUCompute.Racks[0].FacilityDevices = []fixture.GPUDeviceSpec{dev}
	var snmp, fn yaml.Node
	_ = yaml.Unmarshal([]byte("gpu_pools: [pool-a]\n"), &snmp)
	_ = yaml.Unmarshal([]byte("{}\n"), &fn)
	d.Integrations = map[string]yaml.Node{"snmp_exporter": *snmp.Content[0], "shared_key_fn": *fn.Content[0]}
	d.Scenarios = []ScenarioDecl{{Name: "shared-key", Effects: []EffectDecl{{Mode: mode, Target: "device:pdu-a", Intensity: 1}}}}
	reg := gpuLocalRegistry(t, fixture.AxisGPUDevice)
	reg.RegisterConstruct(core.ConstructReg{
		Kind: "shared_key_fn", Group: core.GroupIntegration, Scope: core.ScopeSubstrate,
		NewConfig:    func() any { return &sharedKeyFnConfig{} },
		FailureModes: []failuremode.Mode{{Name: "test_function_mode", Axis: failuremode.AxisFunction}},
		Build:        func(any, *fixture.Set) (core.Construct, error) { return nil, nil },
	})
	return d, reg
}

// The GPU coverage check skips a key only when the key's mode resolved on the function/queue
// axis of a name that is also a GPU key: the GPU consumer coverage rule does not apply to it.
func TestGPUCoverageSkipsSharedKeyOnFunctionAxis(t *testing.T) {
	d, reg := gpuSharedKeyDecl(t, false, "test_function_mode")
	r, err := gpuDeclLoad(t, d, reg)
	if err != nil {
		t.Fatalf("function-axis mode on a name shared with a GPU key was rejected by GPU coverage: %v", err)
	}
	var axes []string
	for _, tg := range r.Targets {
		if tg.Name == "device:pdu-a" {
			axes = append(axes, string(tg.Axis))
		}
	}
	if !slices.Contains(axes, string(fixture.AxisGPUDevice)) || !slices.Contains(axes, string(failuremode.AxisFunction)) {
		t.Fatalf("device:pdu-a must be both a GPU device and a function target, got axes %v", axes)
	}
}

// The skip never hides a coverage gap of a mode that resolves on the GPU axis, even when the same
// name is also a function target: an uncovered GPU-axis mode on the shared key is still rejected,
// and the same effect is accepted once a compatible consumer covers the key.
func TestGPUCoverageSkipNeverHidesGPUAxisGap(t *testing.T) {
	const want = "no enabled compatible consumer covers"
	d, reg := gpuSharedKeyDecl(t, false, "test_collection_outage")
	if _, err := gpuDeclLoad(t, d, reg); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("uncovered GPU-axis mode on a name shared with a function: got %v, want %q", err, want)
	}
	covered, reg := gpuSharedKeyDecl(t, true, "test_collection_outage")
	if _, err := gpuDeclLoad(t, covered, reg); err != nil {
		t.Fatalf("the same GPU-axis mode with a covering consumer must load: %v", err)
	}
}
