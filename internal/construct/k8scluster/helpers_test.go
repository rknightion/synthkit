// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster

import (
	"testing"

	"github.com/rknightion/synthkit/internal/fixture"
)

func TestVcpusForNode(t *testing.T) {
	tests := []struct {
		instanceType string
		wantVCPU     int
	}{
		{"m6i.large", 2},
		{"m6i.xlarge", 4},
	}
	for _, tt := range tests {
		n := fixture.Node{InstanceType: tt.instanceType}
		got := vcpusForNode(n)
		if got != tt.wantVCPU {
			t.Errorf("vcpusForNode(%q): want %d, got %d", tt.instanceType, tt.wantVCPU, got)
		}
	}
}

func TestMemBytesForNode(t *testing.T) {
	tests := []struct {
		instanceType string
		wantBytes    float64
	}{
		{"m6i.large", 8 * 1024 * 1024 * 1024},
		{"m6i.xlarge", 16 * 1024 * 1024 * 1024},
	}
	for _, tt := range tests {
		n := fixture.Node{InstanceType: tt.instanceType}
		got := memBytesForNode(n)
		if got != tt.wantBytes {
			t.Errorf("memBytesForNode(%q): want %g, got %g", tt.instanceType, tt.wantBytes, got)
		}
	}
}

func TestMemBytesForNode_EmptyNode_DefaultFallback(t *testing.T) {
	n := fixture.Node{} // no InstanceType => unknown => size-ladder default => 16 GiB
	got := memBytesForNode(n)
	const want16GiB = 16 * 1024 * 1024 * 1024
	if got != want16GiB {
		t.Errorf("memBytesForNode(empty Node): want %d, got %g", want16GiB, got)
	}
}

// TestSizeClassDefaults pins the deterministic per-workload size-class tuple. The mapping is
// fnv32(deploy)%4 → {small,medium,large,xlarge}; util by (fnv32(deploy)/4)%3 → {hot,normal,cold}.
// The deploy names below were chosen to land in each class (verified against the FNV mapping):
//
//	test-api      → class 0 (small  0.1/0.25), util 0 (hot   0.75)
//	coredns       → class 1 (medium 0.25/0.5), util 0 (hot   0.75)
//	metrics-server→ class 2 (large  0.5/1.0),  util 1 (normal 0.4)
//	svc-a         → class 3 (xlarge 1.0/2.0),  util 2 (cold  0.15)
func TestSizeClassDefaults(t *testing.T) {
	tests := []struct {
		deploy           string
		wantReq, wantLim float64
		wantUtilFrac     float64
	}{
		{"test-api", 0.1, 0.25, 0.75},
		{"coredns", 0.25, 0.5, 0.75},
		{"metrics-server", 0.5, 1.0, 0.4},
		{"svc-a", 1.0, 2.0, 0.15},
	}
	for _, tt := range tests {
		if got := resolveCPURequest(nil, tt.deploy); got != tt.wantReq {
			t.Errorf("resolveCPURequest(nil,%q): want %g, got %g", tt.deploy, tt.wantReq, got)
		}
		if got := resolveCPULimit(nil, tt.deploy); got != tt.wantLim {
			t.Errorf("resolveCPULimit(nil,%q): want %g, got %g", tt.deploy, tt.wantLim, got)
		}
		// resolveCPUUsageBase = request * utilFrac for a nil fixture (no override).
		wantBase := tt.wantReq * tt.wantUtilFrac
		if got := resolveCPUUsageBase(nil, tt.deploy); got != wantBase {
			t.Errorf("resolveCPUUsageBase(nil,%q): want %g, got %g", tt.deploy, wantBase, got)
		}
	}
}

// TestResolveMemDefaults checks memory resolvers fall back to memoryForDeploy basis.
func TestResolveMemDefaults(t *testing.T) {
	tests := []struct {
		deploy           string
		wantReq, wantLim float64
	}{
		{"foo-worker", 512 * 1024 * 1024 * 0.5, 512 * 1024 * 1024}, // -worker => 512 MiB
		{"foo-api", 256 * 1024 * 1024 * 0.5, 256 * 1024 * 1024},    // -api => 256 MiB
		{"plain", 128 * 1024 * 1024 * 0.5, 128 * 1024 * 1024},      // default => 128 MiB
	}
	for _, tt := range tests {
		if got := resolveMemRequest(nil, tt.deploy); got != tt.wantReq {
			t.Errorf("resolveMemRequest(nil,%q): want %g, got %g", tt.deploy, tt.wantReq, got)
		}
		if got := resolveMemLimit(nil, tt.deploy); got != tt.wantLim {
			t.Errorf("resolveMemLimit(nil,%q): want %g, got %g", tt.deploy, tt.wantLim, got)
		}
	}
}

// TestResolveOverridesWin verifies a blueprint-pinned Resources value wins over the size-class default.
func TestResolveOverridesWin(t *testing.T) {
	fwl := &fixture.Workload{Name: "test-api", Resources: &fixture.WorkloadResources{
		CPURequest:   0.3,
		CPULimit:     0.9,
		MemRequest:   100 * 1024 * 1024,
		MemLimit:     400 * 1024 * 1024,
		CPUUsageBase: 0.27,
	}}
	if got := resolveCPURequest(fwl, "test-api"); got != 0.3 {
		t.Errorf("resolveCPURequest override: want 0.3, got %g", got)
	}
	if got := resolveCPULimit(fwl, "test-api"); got != 0.9 {
		t.Errorf("resolveCPULimit override: want 0.9, got %g", got)
	}
	if got := resolveMemRequest(fwl, "test-api"); got != 100*1024*1024 {
		t.Errorf("resolveMemRequest override: want %d, got %g", 100*1024*1024, got)
	}
	if got := resolveMemLimit(fwl, "test-api"); got != 400*1024*1024 {
		t.Errorf("resolveMemLimit override: want %d, got %g", 400*1024*1024, got)
	}
	if got := resolveCPUUsageBase(fwl, "test-api"); got != 0.27 {
		t.Errorf("resolveCPUUsageBase override: want 0.27, got %g", got)
	}
}

// TestResolvePartialOverride verifies a zero field falls through to the default while a set field wins.
func TestResolvePartialOverride(t *testing.T) {
	// test-api default req=0.1; pin only the limit.
	fwl := &fixture.Workload{Name: "test-api", Resources: &fixture.WorkloadResources{CPULimit: 4.0}}
	if got := resolveCPURequest(fwl, "test-api"); got != 0.1 {
		t.Errorf("resolveCPURequest partial: want default 0.1, got %g", got)
	}
	if got := resolveCPULimit(fwl, "test-api"); got != 4.0 {
		t.Errorf("resolveCPULimit partial: want override 4.0, got %g", got)
	}
}

// TestResolveCPUUsageBaseAboveFloor verifies the smallest class (small 0.1 * cold 0.15 = 0.015) stays
// above the cadvisor cpuDelta floor (0.005) so usage never clips for any workload.
func TestResolveCPUUsageBaseAboveFloor(t *testing.T) {
	// find any deploy name that lands small+cold; "alloy-logs" is class 0 util 1 (normal). Construct a
	// worst-case directly: smallest req * coldest util.
	const smallestBase = 0.1 * 0.15 // 0.015
	if smallestBase <= 0.005 {
		t.Fatalf("design invariant broken: smallest usage base %g <= floor 0.005", smallestBase)
	}
	// And confirm the resolver never returns below that for nil fixtures across many names.
	for _, deploy := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"} {
		if got := resolveCPUUsageBase(nil, deploy); got < 0.005 {
			t.Errorf("resolveCPUUsageBase(nil,%q)=%g below floor 0.005", deploy, got)
		}
	}
}
