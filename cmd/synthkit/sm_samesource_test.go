// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/smstate"
)

// sameSourceSpecs admits a blueprint only when its checks and their probes equal, field for field,
// the ones the registered snapshot records for it; any other blueprint's part is irrelevant.
func TestSameSourceSpecsComparesOneBlueprintsChecksAndProbes(t *testing.T) {
	probe := smstate.ProbeSpec{Key: smstate.ProbeKey("own", "EMEA"), Name: "own", Region: "EMEA", Latitude: 1, Longitude: 2}
	other := smstate.ProbeSpec{Key: smstate.ProbeKey("other", "AMER"), Name: "other", Region: "AMER", Latitude: 3, Longitude: 4}
	check := func(source, job string, probe smstate.ProbeSpec) smstate.CheckSpec {
		target := "https://" + job + ".example"
		return smstate.CheckSpec{Key: smstate.CheckKey(job, target), Source: source, Job: job, Target: target, FrequencyMS: 60000, TimeoutMS: 3000, ProbeKey: probe.Key, Labels: []smstate.Label{{Name: "team", Value: "a"}}}
	}
	snap := func(probes []smstate.ProbeSpec, checks ...smstate.CheckSpec) smstate.Snapshot {
		s, err := smstate.NewSnapshot(probes, checks, "https://sm.example", "token", "v1", time.Unix(100, 0))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	registered := snap([]smstate.ProbeSpec{probe, other}, check("b", "b1", probe), check("b", "b2", probe), check("a", "a1", other))
	moved := probe
	moved.Latitude = 9
	slower := check("b", "b2", probe)
	slower.FrequencyMS = 120000
	relabelled := check("b", "b2", probe)
	relabelled.Labels = []smstate.Label{{Name: "team", Value: "b"}}
	for _, tc := range []struct {
		name    string
		current smstate.Snapshot
		want    bool
	}{
		{"unchanged, other blueprint changed", snap([]smstate.ProbeSpec{probe, other}, check("b", "b1", probe), check("b", "b2", probe), check("a", "a9", other)), true},
		{"probe moved", snap([]smstate.ProbeSpec{moved}, check("b", "b1", moved), check("b", "b2", moved)), false},
		{"frequency changed", snap([]smstate.ProbeSpec{probe}, check("b", "b1", probe), slower), false},
		{"label changed", snap([]smstate.ProbeSpec{probe}, check("b", "b1", probe), relabelled), false},
		{"check removed", snap([]smstate.ProbeSpec{probe}, check("b", "b1", probe)), false},
		{"check added", snap([]smstate.ProbeSpec{probe}, check("b", "b1", probe), check("b", "b2", probe), check("b", "b3", probe)), false},
		{"check moved from another blueprint", snap([]smstate.ProbeSpec{probe, other}, check("b", "b1", probe), check("b", "b2", probe), check("b", "a1", other)), false},
	} {
		if got := sameSourceSpecs(tc.current, registered, "b"); got != tc.want {
			t.Errorf("%s: sameSourceSpecs = %v, want %v", tc.name, got, tc.want)
		}
	}
	if sameSourceSpecs(registered, registered, "absent") {
		t.Error("a blueprint with no checks in either snapshot was admitted")
	}
}
