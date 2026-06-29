# gen_ai model catalogue — `internal/genai/models.go` mirror [slug: genai-models]

The **canonical per-model catalogue** for all AI/LLM constructs and workloads in synthkit.
This file is the single source of truth; [`internal/genai/models.go`](../internal/genai/models.go)
is its code mirror — **keep the two in sync**. IDs in `models.go` are declared as LAW (exact
platform-native strings used as metric label values); any drift from this file is a bug.

*Provenance: validated 2026-06-16 against vendor pricing docs (Bedrock/Azure OpenAI/Vertex AI/OpenAI)
and Portkey gateway upstream model IDs. Cost figures are public list-price USD per 1M tokens at
time of capture; they will drift — update and re-date when re-validated.*

---

## Realism notes

- **Anthropic is reached ONLY via Bedrock and Vertex AI** — there is deliberately no
  native-Anthropic platform in synthkit. The same Claude model family therefore wears different
  ID syntax per platform: Bedrock uses suffix form (`anthropic.claude-haiku-4-5-20251001-v1:0`,
  `-v1:0`); Vertex AI uses `@date` syntax (`claude-haiku-4-5@20251001`).
- **`VolumeWeight`** is a unitless relative request-volume multiplier. Cheap/fast models
  (mini/nano/flash/haiku/micro/embed) run far more requests than flagships. Constructs weight
  per-model magnitude by `genai.VolumeWeight(id)`. Unknown IDs fall back to `1.0`.
- **`BlendedCostPerToken`** returns USD per single token at a given input fraction (e.g. 0.3 = 30%
  input / 70% output). Constructs that emit cost metrics call this helper; unknown IDs return `0`.
- **LEGACY IDs** are catalogued (flagged below) because running blueprints may still pin them.
  Without a catalogue entry they would silently fall back to `VolumeWeight=1.0` and `cost=0`,
  producing flat/incorrect KPIs. The LEGACY flag records retirement status, not omission.

---

## AWS Bedrock [slug: genai-models-bedrock]

Platform constant: `bedrock` (Portkey gateway upstream ID).

| ID | Family | Cost in ($/1M) | Cost out ($/1M) | VolumeWeight | Notes |
|---|---|---|---|---|---|
| `anthropic.claude-haiku-4-5-20251001-v1:0` | claude | 1.00 | 5.00 | 4.0 | |
| `anthropic.claude-sonnet-4-6` | claude | 3.00 | 15.00 | 2.0 | |
| `anthropic.claude-opus-4-6-v1` | claude | 5.00 | 25.00 | 0.6 | |
| `amazon.nova-micro-v1:0` | nova | 0.035 | 0.14 | 6.0 | |
| `amazon.nova-lite-v1:0` | nova | 0.06 | 0.24 | 4.0 | |
| `amazon.nova-pro-v1:0` | nova | 0.80 | 3.20 | 1.5 | |
| `amazon.titan-embed-text-v2:0` | titan | 0.02 | 0 | 5.0 | embedding; no output tokens |
| `meta.llama3-3-70b-instruct-v1:0` | llama | 0.72 | 0.72 | 1.2 | |
| `meta.llama3-1-8b-instruct-v1:0` | llama | 0.22 | 0.22 | 3.0 | |
| `mistral.mistral-large-2407-v1:0` | mistral | 2.00 | 6.00 | 0.8 | |
| `cohere.embed-english-v3` | cohere | 0.10 | 0 | 2.0 | embedding; no output tokens |

### Bedrock LEGACY IDs

Retired on the native Bedrock catalogue but still pinned by running blueprints. Catalogued so those
blueprints receive correct `VolumeWeight` and cost rather than the unknown-ID fallback.

| ID | Family | Cost in ($/1M) | Cost out ($/1M) | VolumeWeight | Notes |
|---|---|---|---|---|---|
| `anthropic.claude-3-5-sonnet-20241022-v2:0` | claude | 3.00 | 15.00 | 1.5 | retired-on-native; predecessor to sonnet-4-6 |
| `amazon.titan-text-express-v1` | titan | 0.20 | 0.60 | 1.0 | legacy Titan text model |
| `claude-3.5-sonnet` | claude | 3.00 | 15.00 | 1.5 | Portkey-gateway shorthand (example blueprint) |
| `claude-sonnet-4-6` | claude | 3.00 | 15.00 | 2.0 | bare gateway/workload shorthand for current Sonnet (distinct from `anthropic.`-prefixed Bedrock entry) |

---

## Azure OpenAI [slug: genai-models-azure-openai]

Platform constant: `azure-openai` (Portkey gateway upstream ID).
Azure OpenAI dimension label: `dimension_ModelName` (used by the `cspazure-ai` family;
see [`signals/cspazure.md`](cspazure.md) `[slug: cspazure-ai]`).

| ID | Family | Cost in ($/1M) | Cost out ($/1M) | VolumeWeight | Notes |
|---|---|---|---|---|---|
| `gpt-4o` | gpt | 2.50 | 10.00 | 3.0 | |
| `gpt-4o-mini` | gpt | 0.15 | 0.60 | 6.0 | |
| `gpt-4.1` | gpt | 2.00 | 8.00 | 2.0 | |
| `gpt-4.1-mini` | gpt | 0.40 | 1.60 | 4.0 | |
| `gpt-4.1-nano` | gpt | 0.10 | 0.40 | 5.0 | |
| `o3` | gpt | 2.00 | 8.00 | 1.0 | reasoning |
| `o4-mini` | gpt | 1.10 | 4.40 | 2.0 | reasoning |
| `text-embedding-3-small` | embed | 0.02 | 0 | 6.0 | embedding; no output tokens |
| `text-embedding-3-large` | embed | 0.13 | 0 | 2.0 | embedding; no output tokens |

⚠ `gpt-4o` and `gpt-4o-mini` appear for BOTH Azure OpenAI and OpenAI direct with identical
economics (see note in `Lookup()` godoc: "first wins" on duplicate IDs; only affects
`Lookup(id).Platform` — `VolumeWeight` and `BlendedCostPerToken` are unaffected).

---

## OpenAI direct [slug: genai-models-openai]

Platform constant: `openai` (Portkey gateway upstream ID).

| ID | Family | Cost in ($/1M) | Cost out ($/1M) | VolumeWeight | Notes |
|---|---|---|---|---|---|
| `gpt-4o` | gpt | 2.50 | 10.00 | 3.0 | identical economics to azure-openai entry |
| `gpt-4o-mini` | gpt | 0.15 | 0.60 | 6.0 | identical economics to azure-openai entry |
| `gpt-4.1-mini` | gpt | 0.40 | 1.60 | 4.0 | |
| `o4-mini` | gpt | 1.10 | 4.40 | 2.0 | reasoning |

---

## Google Vertex AI [slug: genai-models-vertex]

Platform constant: `vertex-ai` (Portkey gateway upstream ID).
Vertex AI model IDs use `@date` syntax for versioned models; Claude-on-Vertex follows the same
convention (`claude-haiku-4-5@20251001`), which differs from the Bedrock ID for the same model
(`anthropic.claude-haiku-4-5-20251001-v1:0`).

| ID | Family | Cost in ($/1M) | Cost out ($/1M) | VolumeWeight | Notes |
|---|---|---|---|---|---|
| `gemini-2.5-flash` | gemini | 0.30 | 2.50 | 5.0 | |
| `gemini-2.5-flash-lite` | gemini | 0.10 | 0.40 | 6.0 | |
| `gemini-2.5-pro` | gemini | 1.25 | 10.00 | 1.5 | |
| `claude-sonnet-4-5@20250929` | claude | 3.00 | 15.00 | 1.5 | Claude-on-Vertex @date syntax |
| `claude-haiku-4-5@20251001` | claude | 1.00 | 5.00 | 3.0 | Claude-on-Vertex @date syntax |
| `text-embedding-005` | embed | 0.025 | 0 | 4.0 | embedding; no output tokens |

These are the current-generation Vertex AI models emitted by the `cspgcp` construct's Vertex AI
sub-signal (see [`signals/cspgcp.md`](cspgcp.md) `[slug: cspgcp-vertex]`).

---

## API reference

`internal/genai/models.go` exports three helpers:

- `genai.VolumeWeight(id string) float64` — returns the model's relative request-volume weight,
  or `1.0` for unknown IDs (neutral, not zero).
- `genai.BlendedCostPerToken(id string, inputFrac float64) float64` — USD per single token,
  blending input/output at `inputFrac` (e.g. `0.3` = 30% input). Returns `0` for unknown IDs.
- `genai.ByPlatform(p Platform) []ModelInfo` — all catalogue models for a platform.
- `genai.Lookup(id string) (ModelInfo, bool)` — exact-ID lookup ("first wins" on duplicates;
  see azure/openai note above).

Constructs and workloads that weight per-model magnitude call `genai.VolumeWeight`; those that
emit cost metrics call `genai.BlendedCostPerToken`. See `signals/bedrock.md`, `signals/portkey.md`,
`signals/cspazure.md`, and `signals/cspgcp.md` for the specific families that use these helpers.
