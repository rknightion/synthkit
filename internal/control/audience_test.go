// SPDX-License-Identifier: AGPL-3.0-only

package control

import "testing"

func fullSchema() Schema {
	return Schema{
		VolumeMultiplier: Descriptor{Key: "volume_multiplier", Type: "float", Default: 1.0, Min: 0, Max: 100},
		Blueprints:       []string{"initech"},
		Modes:            []ModeInfo{{Name: "latency", Axis: "http"}},
		Targets:          []TargetInfo{{Name: "api", Axis: "workload"}},
		Scenarios:        []ScenarioInfo{{Blueprint: "initech", Name: "outage", Title: "Outage", Active: true}},
		Constructs:       []ConstructInfo{{Blueprint: "initech", Kind: "rds", Name: "db", Enabled: true}},
		Kinds:            []string{"rds"},
	}
}

func TestCustomerSchemaKeepsOnlyVolumeAndScenarios(t *testing.T) {
	got := CustomerSchema(fullSchema())
	if got.VolumeMultiplier.Key != "volume_multiplier" {
		t.Errorf("volume_multiplier dropped: %+v", got.VolumeMultiplier)
	}
	if len(got.Scenarios) != 1 || got.Scenarios[0].Name != "outage" {
		t.Errorf("scenarios not preserved: %+v", got.Scenarios)
	}
	if !got.Scenarios[0].Active {
		t.Error("scenario Active state must be preserved")
	}
	if got.Modes != nil || got.Targets != nil || got.Constructs != nil || got.Kinds != nil || got.Blueprints != nil {
		t.Errorf("operator-only fields must be nil, got %+v", got)
	}
}

func TestParseAudience(t *testing.T) {
	cases := map[string]Audience{"customer": AudienceCustomer, "operator": AudienceOperator, "": AudienceOperator, "bogus": AudienceOperator}
	for in, want := range cases {
		if got := ParseAudience(in); got != want {
			t.Errorf("ParseAudience(%q)=%q want %q", in, got, want)
		}
	}
}
