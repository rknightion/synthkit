# Fresh-container acceptance findings - 2026-08-29

Run from a disposable Go 1.27 container with git, Docker CLI, Compose 2.33, and the host Docker socket. The repository was cloned directly from the public URL at the public default revision. No local checkout was mounted or copied. This register is deliberately anonymized.

## Evidence boundary

- A cold image pull occurred before the first Compose dry run. The relevant image was absent from the host cache before the run.
- The initial credential handoff did not include the three required positive-decimal sink identifiers. A supplementary local-only handoff supplied them. Its first OTLP mapping was corrected by the run owner; it is a handoff error, not a synthkit defect.
- The final full-catalogue Compose deployment was healthy, reported `ready=true`, `live_ready=true`, and selected all 26 shipped blueprints. A read-only stack metrics query returned one result for the focused blueprint after delivery.
- Optional credentials beyond the required telemetry token were not supplied. The supplied token lacks the profile write scope. No optional product was treated as failed merely because it was unavailable.

| ID | Verdict | Observable assertion and evidence | New user saw / blocker |
| --- | --- | --- | --- |
| A1 | fail | `docs/getting-started.md` is conceptual only and did not reach a running process. | The page says to read Installation or Quick Start next, but has no end-to-end deploy action. |
| A2 | fail | Literal skill execution stopped before `just compose-check`; continuing required installing `just`, a newer `just` release, `bash`, and `python3`. | `just: not found`; then `Unknown attribute working-directory`; then `python3: command not found`. |
| A3 | pass | Required lanes only, after the authorized identifier handoff, produced a healthy live process. | No failure after the corrected handoff. The original missing identifiers are an operator handoff gap. |
| A4 | pass | With `BLUEPRINT_NAMES` absent, Compose was healthy and readiness returned `ready=true`, `live_ready=false`, `setup_required=true`. | Existing earlier focused samples prevented a clean no-series time-window proof, but the control plane correctly entered setup mode. |
| A5 | pass | Removing `GC_TOKEN` made Compose unhealthy before live delivery. | `synthkit: config: missing mandatory live settings: GC_TOKEN`. |
| A6 | fail | Compose ultimately became healthy, but the documented path was not runnable on the stated clean container without undocumented tools and a socket-specific state-directory workaround. | `sudo: not found`; without the workaround the dry run logged `startup state-volume probe failed: permission denied`. |
| B1 | pass | Read-only Prometheus query for the focused blueprint returned one frame after the live deployment. | No failure observed. |
| B2 | pass | Read-only Tempo datasource search returned trace results after the focused deployment. | No failure observed. Native-OTLP metric-specific inventory remains unproven. |
| B3 | pass | Read-only Loki datasource query returned a result frame for the focused blueprint. | No failure observed. |
| B4 | blocked | The supplied token lacks `profiles:write`; no separate profile credential was supplied. | Stack capability is present, but profile ingest is unsupported by the supplied authority, not a synthkit failure. |
| B5 | blocked | Faro credentials were not supplied. | Required `GC_FARO_COLLECTOR` and app key are absent; capability unknown. |
| B6 | blocked | Synthetic Monitoring token was not supplied. | Endpoint was advertised but registration authority is absent; capability not assumed. |
| B7 | blocked | Fleet registration credentials were not supplied. | Required Fleet URL, stack ID, and token are absent; capability unknown. |
| B8 | blocked | Sigil endpoint was advertised but its tenant identifier and token were not supplied. | Status reported Sigil `partial` with three missing fields. |
| B9 | blocked | No separate self-observability credential triplet or target was supplied. | The required staff destination cannot be verified safely. |
| B10 | blocked | Optional lanes cannot be configured together without their missing credentials. | No cross-lane result is available. |
| C1 | fail | All 26 runtime names were selected individually with a 50-second Compose deadline. Eighteen became healthy; several timed out after startup; immediate Prometheus read-back returned zero frames. | A user sees Compose wait time out although the service log says `synthkit up`; filename is not always the runtime name. |
| C2 | fail | The shipped verification procedure was exercised for all 26 selections, but the fresh container had no `gcx` binary or active Grafana context. | `gcx` was unavailable (`127`) at the documented verification step. |
| C3 | pass | `BLUEPRINT_NAMES='*'` selected all 26 shipped blueprints; the live schema exposed 26 blueprints and Compose reported healthy. | No silent startup drop-out observed. Memory and series-cap observation remains unproven. |
| C4 | fail | Full-catalogue `-once -dump` completed while the deployment was healthy, but bounded live read-back did not establish family-by-family queryability. | A user has inventory output but no matching delivery proof. |
| C5 | fail | Full deployment inventory endpoint was read, but its returned shape had no per-blueprint entries for representative label inspection. | Identity separation could not be verified from the supplied control data. |
| D1 | pass | Authenticated deployment readiness returned `ready=true`, `live_ready=true`, `setup_required=false`. | No failure observed. |
| D2 | pass | Authenticated item disable and enable requests for the focused blueprint both returned object responses; pending-state read also returned successfully. | No failure observed. |
| D3 | fail | Invalid validation returned a diagnostic response; a documented valid bundled-YAML upload returned HTTP 400. | New user sees an HTTP 400 response when posting a namespaced copy of the shipped focused blueprint. |
| D4 | pass | Added and fetched a public Git source through the documented control routes; both returned HTTP 200. | No failure observed; staged-versus-running remains restart-bound by design. |
| D5 | fail | Authenticated `/control/ui` returned HTTP 302, but the SPA's individual views could not be enumerated through the container-only HTTP check. | A user reaches the redirect, not a tested view inventory. |
| D6 | blocked | `/control/reset` returned HTTP 200. The full schema reported zero scalable targets, so a valid scaling mutation could not be exercised. | Scaling capability is absent from this selected catalogue state. |
| D7 | pass | With `SYNTHKIT_BIND=0.0.0.0` and no acknowledgement, Compose was unhealthy before service exposure. | `control exposure: unsafe non-loopback SYNTHKIT_BIND requires CONTROL_EXPOSURE_ACK=trusted-network or tls-proxy`. The loopback deployment was restored. |
| D8 | fail | Diagnostics, inventory, health, and status bodies were retained; diagnostics was an array and inventory an object with no per-blueprint entries. | The available response shape did not establish sufficient shell-free debugging detail. |
| E1 | fail | All 14 schema-derived scenario IDs accepted activation HTTP 200, but the bounded control read did not provide a data-effect assertion. | A user sees accepted activation without observed emitted-data proof. |
| E2 | fail | The 15-second bounded observation after activating every declared scenario did not establish an ambient effect interval in emitted data. | No observable ambient incident effect appeared in the bounded read. |
| E3 | fail | Schema scenarios were activated, but the retained control read did not expose a sibling-versus-environment data comparison. | Scope restriction was not observable through the available response. |
| E4 | pass | Every declared scenario accepted deactivation, and the persisted state response exposed the active-scenarios field after the clear. | No deactivation request failed; cumulative counter residue was not interpreted as an active effect. |
| F1 | pass | Running image config digest, OCI version, and OCI revision were observed together through Compose container inspection. | No mismatch observed. |
| F2 | blocked | Public registry inspection of the attempted prior immutable release reference failed. | No second verified published digest was available from the attempted public reference. |
| F3 | blocked | No verified upgrade target existed to create a rollback point. | Depends on F2's unavailable public target. |
| F4 | fail | Compose restart was issued, but bounded health read did not return a post-restart persistence assertion. | A user cannot confirm selected-blueprint/control-state survival from the documented restart result. |
| G1 | pass | The focused blueprint generated two dashboard resources; after the documented `gcx` push path was enabled by an undocumented container-local installation, both pushed with zero errors. | No push failure observed. |
| G2 | fail | Both dashboard-level snapshots rendered, but the documented snapshot command produced dashboard PNGs rather than a panel-by-panel rendered/empty report. | A user sees successful dashboard images without an actionable empty-panel inventory. |
| G3 | fail | The fresh datasource inventory has multiple same-type datasources, while generated dashboard JSON had no explicit datasource references in the bounded inspection. | A user must resolve datasource choice outside the generator/push path. |
| G4 | fail | Both dashboards snapshot-rendered, but the generated files contained no native-histogram query marker in the bounded inspection. | No native-histogram panel result was available to validate. |
| G5 | fail | The shipped generator documents that its target folder must already exist in Grafana; it does not create one. | A fresh-stack user sees a required pre-existing folder prerequisite. |
| H1 | fail | Executing the documented Compose path required unlisted tools and version knowledge. | Missing `just`, `bash`, and `python3`; distribution `just` was too old for the checked-in justfile. |
| H2 | fail | The documented troubleshooting symptom set was enumerated; missing-token and exposure failures reproduced as documented, but every listed symptom/remedy could not be reproduced in the bounded clean run. | New users lack an executable matrix for the remaining symptom reproductions. |
| H3 | fail | RUNBOOK configuration, offline dump, and Compose steps were followed through the retained deployment, but it assumes prerequisite tools not installed by its own path. | `just`, `bash`, `python3`, and socket-state setup required outside knowledge. |
| H4 | fail | The documented marketplace commands are slash commands, but the clean container has no Codex/Claude plugin host or CLI. | `codex` exit 127; `/plugin marketplace add` is not executable in the documented shell path. |

## Completion and teardown boundary

The 46-row round is complete. Retain the bare stack only until findings are tracked, then let root perform the approved teardown.

Further stack evidence was deliberately abandoned after bounded attempts for C4/C5, D5, D8, F4, G2/G4, and E1-E3. Those rows are failures, not unattempted blocks. Blocks remain only for unavailable optional-lane authority or capability (B4-B10), no scalable target (D6), and no verified second published digest (F2/F3).
