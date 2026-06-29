// SPDX-License-Identifier: AGPL-3.0-only

package genai

// Platform is the upstream serving platform (the Portkey-gateway upstream id). Anthropic is
// reached ONLY via Bedrock/Vertex — there is deliberately no native-Anthropic platform.
type Platform string

const (
	PlatformOpenAI  Platform = "openai"
	PlatformAzure   Platform = "azure-openai"
	PlatformBedrock Platform = "bedrock"
	PlatformVertex  Platform = "vertex-ai"
)

// ModelInfo is the intrinsic, technology-native economics of one model. Sourced from
// signals/genai-models.md (real vendor IDs + public pricing, validated 2026-06-16). Costs are
// USD per 1,000,000 tokens. VolumeWeight is a unitless relative request-volume multiplier:
// cheap/fast models (mini/nano/flash/haiku/micro/embed) run far more requests than flagships.
type ModelInfo struct {
	ID           string
	Platform     Platform
	Family       string
	CostInPerM   float64
	CostOutPerM  float64
	VolumeWeight float64
}

// Models is the canonical catalogue. IDs are LAW — exact platform-native strings (label
// values). Mirror of signals/genai-models.md; keep the two in sync.
var Models = []ModelInfo{
	// --- AWS Bedrock ---
	{"anthropic.claude-haiku-4-5-20251001-v1:0", PlatformBedrock, "claude", 1, 5, 4.0},
	{"anthropic.claude-sonnet-4-6", PlatformBedrock, "claude", 3, 15, 2.0},
	{"anthropic.claude-opus-4-6-v1", PlatformBedrock, "claude", 5, 25, 0.6},
	{"amazon.nova-micro-v1:0", PlatformBedrock, "nova", 0.035, 0.14, 6.0},
	{"amazon.nova-lite-v1:0", PlatformBedrock, "nova", 0.06, 0.24, 4.0},
	{"amazon.nova-pro-v1:0", PlatformBedrock, "nova", 0.8, 3.2, 1.5},
	{"amazon.titan-embed-text-v2:0", PlatformBedrock, "titan", 0.02, 0, 5.0},
	{"meta.llama3-3-70b-instruct-v1:0", PlatformBedrock, "llama", 0.72, 0.72, 1.2},
	{"meta.llama3-1-8b-instruct-v1:0", PlatformBedrock, "llama", 0.22, 0.22, 3.0},
	{"mistral.mistral-large-2407-v1:0", PlatformBedrock, "mistral", 2, 6, 0.8},
	{"cohere.embed-english-v3", PlatformBedrock, "cohere", 0.10, 0, 2.0},
	// --- LEGACY (retired on native catalogues but STILL PINNED by running blueprints; catalogue
	//     them so those blueprints differentiate instead of falling back to weight 1.0 / cost 0).
	{"anthropic.claude-3-5-sonnet-20241022-v2:0", PlatformBedrock, "claude", 3, 15, 1.5},
	{"amazon.titan-text-express-v1", PlatformBedrock, "titan", 0.2, 0.6, 1.0},
	{"claude-3.5-sonnet", PlatformBedrock, "claude", 3, 15, 1.5}, // Portkey-gateway shorthand (legacy pinned ID)
	// Bare (un-prefixed) gateway/workload shorthand for the current Sonnet — distinct catalogue
	// entry from the Bedrock-prefixed "anthropic.claude-sonnet-4-6": the gateway/poller/workload
	// lanes route by a separate provider field, so they pin the bare model id.
	{"claude-sonnet-4-6", PlatformBedrock, "claude", 3, 15, 2.0},
	// --- Azure OpenAI (ModelName dimension values) ---
	{"gpt-4o", PlatformAzure, "gpt", 2.5, 10, 3.0},
	{"gpt-4o-mini", PlatformAzure, "gpt", 0.15, 0.6, 6.0},
	{"gpt-4.1", PlatformAzure, "gpt", 2, 8, 2.0},
	{"gpt-4.1-mini", PlatformAzure, "gpt", 0.4, 1.6, 4.0},
	{"gpt-4.1-nano", PlatformAzure, "gpt", 0.1, 0.4, 5.0},
	{"o3", PlatformAzure, "gpt", 2, 8, 1.0},
	{"o4-mini", PlatformAzure, "gpt", 1.1, 4.4, 2.0},
	{"text-embedding-3-small", PlatformAzure, "embed", 0.02, 0, 6.0},
	{"text-embedding-3-large", PlatformAzure, "embed", 0.13, 0, 2.0},
	// --- OpenAI direct ---
	{"gpt-4o", PlatformOpenAI, "gpt", 2.5, 10, 3.0},
	{"gpt-4o-mini", PlatformOpenAI, "gpt", 0.15, 0.6, 6.0},
	{"gpt-4.1-mini", PlatformOpenAI, "gpt", 0.4, 1.6, 4.0},
	{"o4-mini", PlatformOpenAI, "gpt", 1.1, 4.4, 2.0},
	// --- Google Vertex AI (incl. Claude-on-Vertex @date syntax) ---
	{"gemini-2.5-flash", PlatformVertex, "gemini", 0.30, 2.50, 5.0},
	{"gemini-2.5-flash-lite", PlatformVertex, "gemini", 0.10, 0.40, 6.0},
	{"gemini-2.5-pro", PlatformVertex, "gemini", 1.25, 10, 1.5},
	{"claude-sonnet-4-5@20250929", PlatformVertex, "claude", 3, 15, 1.5},
	{"claude-haiku-4-5@20251001", PlatformVertex, "claude", 1, 5, 3.0},
	{"text-embedding-005", PlatformVertex, "embed", 0.025, 0, 4.0},
}

// modelIndex is "first wins" on duplicate ids. NOTE: gpt-4o et al. appear for BOTH azure and
// openai with identical economics, so Lookup("gpt-4o") returns the azure entry — fine for
// VolumeWeight/BlendedCostPerToken (economics identical); ByPlatform iterates the slice so it
// is unaffected. Only matters if a caller keys off Lookup(id).Platform.
var modelIndex = func() map[string]ModelInfo {
	m := make(map[string]ModelInfo, len(Models))
	for _, mi := range Models {
		if _, exists := m[mi.ID]; !exists {
			m[mi.ID] = mi
		}
	}
	return m
}()

// Lookup returns the ModelInfo for an exact platform-native model id.
func Lookup(id string) (ModelInfo, bool) {
	mi, ok := modelIndex[id]
	return mi, ok
}

// ByPlatform returns all catalogue models for a platform (catalogue order).
func ByPlatform(p Platform) []ModelInfo {
	var out []ModelInfo
	for _, mi := range Models {
		if mi.Platform == p {
			out = append(out, mi)
		}
	}
	return out
}

// VolumeWeight returns a model's relative request-volume weight, or 1.0 for unknown ids
// (so a never-before-seen blueprint model id stays neutral rather than vanishing).
func VolumeWeight(id string) float64 {
	if mi, ok := modelIndex[id]; ok && mi.VolumeWeight > 0 {
		return mi.VolumeWeight
	}
	return 1.0
}

// BlendedCostPerToken returns USD per single token for a model, blending input/output cost
// at the given input fraction (e.g. 0.3 ⇒ 30% input / 70% output tokens). Unknown ids cost 0
// (caller decides whether to fall back). Divides the per-1M cost down to per-token.
func BlendedCostPerToken(id string, inputFrac float64) float64 {
	mi, ok := modelIndex[id]
	if !ok {
		return 0
	}
	if inputFrac < 0 {
		inputFrac = 0
	}
	if inputFrac > 1 {
		inputFrac = 1
	}
	perM := inputFrac*mi.CostInPerM + (1-inputFrac)*mi.CostOutPerM
	return perM / 1_000_000
}
