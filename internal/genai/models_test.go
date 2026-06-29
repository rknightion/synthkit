// SPDX-License-Identifier: AGPL-3.0-only

package genai

import "testing"

func TestLookupReturnsRealModel(t *testing.T) {
	m, ok := Lookup("anthropic.claude-haiku-4-5-20251001-v1:0")
	if !ok {
		t.Fatal("expected haiku 4.5 in catalogue")
	}
	if m.Platform != PlatformBedrock {
		t.Errorf("platform = %q, want bedrock", m.Platform)
	}
	if m.VolumeWeight <= 0 || m.CostOutPerM <= 0 {
		t.Errorf("haiku must have positive weight+cost, got %+v", m)
	}
}

func TestVolumeWeightsDiffer(t *testing.T) {
	micro, _ := Lookup("amazon.nova-micro-v1:0")
	opus, _ := Lookup("anthropic.claude-opus-4-6-v1")
	if !(micro.VolumeWeight > opus.VolumeWeight) {
		t.Errorf("nova-micro weight %v should exceed opus weight %v", micro.VolumeWeight, opus.VolumeWeight)
	}
}

func TestByPlatformNonEmpty(t *testing.T) {
	for _, p := range []Platform{PlatformBedrock, PlatformAzure, PlatformOpenAI, PlatformVertex} {
		if len(ByPlatform(p)) == 0 {
			t.Errorf("no models for platform %q", p)
		}
	}
}

func TestVolumeWeightFallsBackToOne(t *testing.T) {
	if got := VolumeWeight("totally-unknown-model"); got != 1.0 {
		t.Errorf("unknown model weight = %v, want 1.0 fallback", got)
	}
}

func TestBlendedCostPerTokenPositive(t *testing.T) {
	c := BlendedCostPerToken("gpt-4o", 0.3)
	if c <= 0 {
		t.Errorf("blended cost = %v, want > 0", c)
	}
}

// TestBlueprintShorthandModelsCatalogued guards the no-op failure mode: the bare gateway/
// workload model shorthands pinned by the running blueprints must be in the catalogue, else
// they fall back to weight 1.0 / cost 0 and the flagship lines never differentiate.
func TestBlueprintShorthandModelsCatalogued(t *testing.T) {
	for _, id := range []string{"claude-sonnet-4-6", "claude-3.5-sonnet"} {
		m, ok := Lookup(id)
		if !ok {
			t.Errorf("blueprint-pinned shorthand %q is not catalogued (would no-op)", id)
			continue
		}
		if m.VolumeWeight <= 0 || BlendedCostPerToken(id, 0.3) <= 0 {
			t.Errorf("%q must have positive weight + cost, got weight=%v", id, m.VolumeWeight)
		}
	}
}
