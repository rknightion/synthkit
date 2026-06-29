// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import "testing"

func TestSetCarriesHost(t *testing.T) {
	h := &Host{Hostname: "camden", OS: "linux", NumCPU: 4, MemTotal: 8 << 30, Profile: "integration"}
	s := Set{Seed: "bp:host:camden", Host: h}
	if s.Host == nil || s.Host.Hostname != "camden" {
		t.Fatalf("Set.Host not wired: %+v", s.Host)
	}
}
