// SPDX-License-Identifier: AGPL-3.0-only

package blueprintschema

import (
	"reflect"
	"testing"

	"github.com/rknightion/synthkit/internal/failuremode"
)

// TestFailureModeFieldsTotalOrder feeds modes whose names tie (same name under several axes and
// with differing help) in every rotation and reversal of the input and requires one identical
// output. Ordering by name alone is not total, so an unstable sort would let the input order
// leak into the generated schema.
func TestFailureModeFieldsTotalOrder(t *testing.T) {
	var modes []failuremode.Mode
	for _, name := range []string{"latency", "errors", "saturation"} {
		for _, axis := range []failuremode.Axis{"zeta", "alpha", "mid"} {
			for _, help := range []string{"second help", "first help", ""} {
				modes = append(modes, failuremode.Mode{Name: name, Axis: axis, Help: help})
			}
		}
	}
	// Duplicates must still collapse.
	modes = append(modes, modes[0], modes[5])

	want := failureModeFields(modes)
	if len(want) != 27 {
		t.Fatalf("got %d fields, want 27 unique (name, axis, help) tuples", len(want))
	}
	for i := 1; i < len(want); i++ {
		a, b := want[i-1], want[i]
		if a.Key > b.Key || (a.Key == b.Key && (a.Type > b.Type || (a.Type == b.Type && a.Doc >= b.Doc))) {
			t.Fatalf("rows %d and %d out of (key, type, doc) order: %+v then %+v", i-1, i, a, b)
		}
	}

	n := len(modes)
	for shift := 0; shift < n; shift++ {
		for _, reverse := range []bool{false, true} {
			in := make([]failuremode.Mode, n)
			for i := range modes {
				j := (i + shift) % n
				if reverse {
					j = n - 1 - j
				}
				in[j] = modes[i]
			}
			if got := failureModeFields(in); !reflect.DeepEqual(got, want) {
				t.Fatalf("shift=%d reverse=%v: order depends on input order", shift, reverse)
			}
		}
	}
}
