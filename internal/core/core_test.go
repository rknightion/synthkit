// SPDX-License-Identifier: AGPL-3.0-only

package core_test

import (
	"testing"

	"github.com/rknightion/synthkit/internal/core"
)

func TestPyroscopeSignalClass(t *testing.T) {
	if core.PyroscopeProfiles.String() != "pyroscope_profiles" {
		t.Fatalf("String()=%q", core.PyroscopeProfiles.String())
	}
	var _ core.PyroscopeWriter = (core.PyroscopeWriter)(nil)
	if (core.World{}).Pyroscope != nil {
		t.Fatal("zero World.Pyroscope must be nil")
	}
	if core.RUM.String() != "rum" {
		t.Fatalf("RUM.String()=%q", core.RUM.String())
	}
	if core.SignalClass(250).String() != "unknown" {
		t.Fatalf("unmatched class must be %q, got %q", "unknown", core.SignalClass(250).String())
	}
}
