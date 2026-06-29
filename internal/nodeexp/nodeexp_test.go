// SPDX-License-Identifier: AGPL-3.0-only

package nodeexp

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/state"
)

// testEngine returns a seeded shape.Engine for deterministic tests.
func testEngine() *shape.Engine {
	return shape.New("", nil)
}

// testNow returns a fixed reference time for deterministic tests.
func testNow() time.Time {
	return time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
}

// TestEmitLinuxWritesSeries is the Phase-2 target test. It is EXPECTED TO FAIL now
// because EmitLinux has an empty body (stub). Phase 2 fills in the real implementation.
func TestEmitLinuxWritesSeries(t *testing.T) {
	st := state.NewState()
	base := map[string]string{"job": "integrations/node_exporter", "instance": "camden"}
	top := HostTopology{Hostname: "camden", NumCPU: 2, MemTotal: 8 << 30,
		Disks: []string{"sda"}, NICs: []NIC{{Name: "eth0", SpeedBytes: 1e9}},
		FS: FSMount{Device: "/dev/sda1", FSType: "ext4", Mountpoint: "/", SizeBytes: 100 << 30}}
	EmitLinux(st, base, top, ProfileIntegration, 0.5, 60, 2, testEngine())
	series := st.Collect(testNow())
	if len(series) == 0 {
		t.Fatal("EmitLinux emitted no series")
	}
}
