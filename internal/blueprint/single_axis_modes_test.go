// SPDX-License-Identifier: AGPL-3.0-only

package blueprint_test

import (
	"slices"
	"testing"

	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/runner"
)

// singleAxisViolations lists every mode name declared on a function or queue axis that is also
// declared on another axis. The loader and the runtime resolve a name shared by several targets
// on the one axis that declares the mode, and the GPU coverage skip keys off that resolution, so
// these modes must be declared on exactly one axis.
func singleAxisViolations(modes []failuremode.Mode) []string {
	axes := map[string][]failuremode.Axis{}
	for _, m := range modes {
		if !slices.Contains(axes[m.Name], m.Axis) {
			axes[m.Name] = append(axes[m.Name], m.Axis)
		}
	}
	var out []string
	for name, as := range axes {
		if len(as) > 1 && (slices.Contains(as, failuremode.AxisFunction) || slices.Contains(as, failuremode.AxisQueue)) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func TestSingleAxisViolationsDetectsSharedModeName(t *testing.T) {
	modes := []failuremode.Mode{
		{Name: "function_x", Axis: failuremode.AxisFunction},
		{Name: "function_x", Axis: failuremode.AxisFunction}, // the same axis again is not a violation
		{Name: "queue_y", Axis: failuremode.AxisQueue},
		{Name: "queue_y", Axis: failuremode.AxisService},
		{Name: "service_z", Axis: failuremode.AxisService},
		{Name: "service_z", Axis: failuremode.AxisWorkload}, // multi-axis outside function/queue is allowed
	}
	if got := singleAxisViolations(modes); len(got) != 1 || got[0] != "queue_y" {
		t.Fatalf("got %v, want [queue_y]", got)
	}
}

// Every function-axis and queue-axis mode of the production catalogue is declared on exactly one
// axis, and both axes are populated so the check is not vacuous.
func TestFunctionAndQueueModesAreDeclaredOnExactlyOneAxis(t *testing.T) {
	modes := runner.Catalog().AllFailureModes()
	var fn, q int
	for _, m := range modes {
		switch m.Axis {
		case failuremode.AxisFunction:
			fn++
		case failuremode.AxisQueue:
			q++
		}
	}
	if fn == 0 || q == 0 {
		t.Fatalf("catalogue declares %d function-axis and %d queue-axis modes; both must be present", fn, q)
	}
	if bad := singleAxisViolations(modes); len(bad) != 0 {
		t.Fatalf("function/queue modes declared on more than one axis: %v", bad)
	}
}
