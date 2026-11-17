// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/failuremode"
)

// A named target on several axes resolves on the single axis declaring the mode; a mode
// declared on two of the name's axes is ambiguous and loud.
func TestEffectResolvesOnTheAxisDeclaringItsMode(t *testing.T) {
	vocab := []failuremode.Mode{
		{Name: "only_fn", Axis: failuremode.AxisFunction},
		{Name: "both", Axis: failuremode.AxisFunction},
		{Name: "both", Axis: failuremode.AxisQueue},
		{Name: "svc", Axis: failuremode.AxisService},
	}
	axes := map[string][]failuremode.Axis{"shared": {failuremode.AxisFunction, failuremode.AxisQueue}}
	multi := failuremode.MultiAxis(vocab)
	if err := validateEffectAxes(EffectDecl{Mode: "only_fn", Target: "shared"}, axes, vocab, multi); err != nil {
		t.Fatal(err)
	}
	if err := validateEffectAxes(EffectDecl{Mode: "both", Target: "shared"}, axes, vocab, multi); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("mode on two of the name's axes: %v", err)
	}
	if err := validateEffectAxes(EffectDecl{Mode: "svc", Target: "shared"}, axes, vocab, multi); err == nil || !strings.Contains(err.Error(), "any of target") {
		t.Fatalf("mode on none of the name's axes: %v", err)
	}
}
