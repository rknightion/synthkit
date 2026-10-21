# Embedded conversation corpus

Each gzip file contains UTF-8 JSONL using `sigil.CorpusRecord`: `archetype`, `kind`,
optional `role`, `parts`, `text`, `stop_reason`, and sampling `facets`. The embedding
pattern includes every `*.jsonl.gz` here; archetype registration is a separate owner seam.
Existing assets are unchanged.

## Fictional regulated-document authoring

`regulated_document_authoring.jsonl.gz` adds protocol objectives, a clinical summary,
a safety narrative, redaction inputs/outputs and two explicitly untrusted source-note
injection examples. All clinical statements, counts and identifiers are invented test
content, not clinical evidence, medical advice, approved product claims or realistic
trial results. `SUBJ-SYN-*` and `INV-SYN-*` are synthetic identifiers deliberately
retained in source inputs so a downstream redaction path has something to remove.
Assistant draft examples use `[REDACTED]`. The corpus itself is not a redaction engine.

The source-note attacks are inert examples of requests to disclose identifiers, conceal
adverse events or invent approval. They are labelled untrusted and paired with refusal
examples. The existing sampler samples records independently: it does **not** guarantee
that adjacent user/tool/assistant examples remain paired, or run a safety evaluation.
Consumers needing an ordered attack scenario must explicitly construct one rather than
assuming this asset changes assembly semantics. Standalone `tool_result` records preserve
source examples in the current corpus format; loader/assembler wiring belongs separately.

### Reproduction and checks

Run `python3 internal/sigil/corpus/generate_regulated.py` from the repository root.
Use `python3 internal/sigil/corpus/generate_regulated.py --check` to detect a missing or
stale asset, and `go test ./internal/sigil/corpus` to exercise the real embedded loader.
The generator has no network, input-file or free-text arguments. Its only source is
handwritten fictional text in the script, never customer material or deployment/design
documents. JSON ordering, UTF-8 encoding, gzip timestamp and gzip filename are fixed;
byte reproduction is checked with the local Python/zlib toolchain.

Before publishing a regenerated asset, decompress it and scan the plaintext with the
repository forbidden-words guard using both the CI pattern set and the complete ignored
local pattern set. The forbidden-words guard decompresses gzip assets before scanning and
fails closed on malformed gzip, including under the CI-injected pattern. Do not copy private
patterns or matching private text into reports.

### Name collision research

On 2026-10-21, exact-phrase current web searches were made for each chosen fictional name:

| Name | Search source | Observation |
|---|---|---|
| Brindlemoor Therapeutics | https://www.bing.com/search?format=rss&q=%22Brindlemoor%20Therapeutics%22 | No exact-name match in returned items; unrelated restaurant results. |
| brindanib | https://www.bing.com/search?format=rss&q=%22brindanib%22 | No exact-name match in returned items; unrelated sign-in results. |
| XQZ-301 | https://www.bing.com/search?format=rss&q=%22XQZ-301%22 | No full-code match; unrelated streaming and electronics results, including other XQZ codes. |
| XQZ-204 | https://www.bing.com/search?format=rss&q=%22XQZ-204%22 | No full-code match; unrelated streaming and electronics results, including other XQZ codes. |

Google exact-phrase searches were also attempted for all four names, but returned a
JavaScript access interstitial, **not** usable collision evidence. Search results are
incomplete and cannot establish worldwide uniqueness, trademark clearance or absence
from non-indexed trial registries. These are fictional labels only; the shared `XQZ`
prefix is not claimed unique. Recheck before any use beyond synthetic examples.
