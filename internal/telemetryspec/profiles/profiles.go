// SPDX-License-Identifier: AGPL-3.0-only

// Package profiles is the catalog of generic, real-named telemetry profile templates (Spec 5).
// Each profile is a telemetryspec.Profile built from REAL names sourced from signals/ and extracted
// from the existing emission code (Wave 2: P1–P9). The app workload composes them onto service
// nodes by name.
//
// Registration is via per-file init() into THIS package's own lookup table — NOT cross-tier
// self-registration into the core runner registry (the rule the three-tier model bans). That
// keeps each Wave-2 profile in its own file (disjoint lanes) while the app workload consumes the
// catalog through the explicit Lookup function.
package profiles

import (
	"fmt"
	"sort"

	"github.com/rknightion/synthkit/internal/telemetryspec"
)

// registry is the catalog table, populated by per-file init() via register.
var registry = map[string]telemetryspec.Profile{}

// register adds a profile to the catalog. It panics on a duplicate name or an invalid profile —
// a programming error in a catalog file, caught at startup.
func register(p telemetryspec.Profile) {
	if _, dup := registry[p.Name]; dup {
		panic(fmt.Sprintf("profiles: duplicate profile %q", p.Name))
	}
	if err := p.Validate(); err != nil {
		panic(fmt.Sprintf("profiles: invalid profile %q: %v", p.Name, err))
	}
	registry[p.Name] = p
}

// Lookup returns the named catalog profile.
func Lookup(name string) (telemetryspec.Profile, bool) {
	p, ok := registry[name]
	return p, ok
}

// Names returns the registered profile names, sorted (for error messages / schema docs).
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
