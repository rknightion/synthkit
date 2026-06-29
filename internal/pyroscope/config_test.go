// SPDX-License-Identifier: AGPL-3.0-only

package pyroscope_test

import (
	"testing"

	"github.com/rknightion/synthkit/internal/pyroscope"
)

func TestProfilingCfgValidate(t *testing.T) {
	if err := (pyroscope.ProfilingCfg{Enabled: true, Mode: "sdk", Runtime: "go"}).Validate(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if err := (pyroscope.ProfilingCfg{Enabled: true, Mode: "bogus"}).Validate(); err == nil {
		t.Fatal("unknown mode must be rejected")
	}
	if got := (pyroscope.ProfilingCfg{}).ModeOrDefault(); got != "sdk" {
		t.Fatalf("ModeOrDefault default = %q, want sdk", got)
	}
}
