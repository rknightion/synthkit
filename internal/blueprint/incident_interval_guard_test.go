// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"strings"
	"testing"
)

func TestIncidentForMustBeShorterThanEvery(t *testing.T) {
	mk := func(every, forD string) *Decl {
		return &Decl{
			Name:         "t",
			Environments: []EnvDecl{{Name: "e1"}},
			Incidents:    []IncidentDecl{{Kind: "oom_kill", Target: "c1", Every: every, For: forD, Intensity: 0.2}},
		}
	}
	if err := validateDecl(mk("5m", "10m")); err == nil || !strings.Contains(err.Error(), "must be shorter than") {
		t.Fatalf("want for>=every rejection, got: %v", err)
	}
	if err := validateDecl(mk("10m", "5m")); err != nil {
		t.Fatalf("for<every should pass decl validation: %v", err)
	}
}
