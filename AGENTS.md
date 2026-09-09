# synthkit

Composable synthetic-telemetry generator: YAML blueprints under `blueprints/` declare infrastructure
and applications; synthkit emits structurally-correct synthetic metrics, traces, logs and optional
RUM. `ARCHITECTURE.md` is the design contract and `SIGNALS.md` plus `signals/` is the authoritative
per-construct data contract.

## Architecture contract

Three tiers, mandatory:

- **Constructs** (`internal/construct/<kind>/`) and **workloads** (`internal/workload/<kind>/`) are
  isolated. They may import core/fixture/shape/state/ledger/sink-types and the shared mechanic
  libraries (`internal/cw`, `internal/genai`) only: never each other, the blueprint package, or a
  blueprint name. `internal/archtest.TestCatalogImportIsolation` enforces this.
- **Blueprints** (`blueprints/*.yaml`) own blueprint-specific configuration and explicit wiring,
  including workload-to-cluster binding and shared identity. One declaration may fan into several
  constructs; its emission switch gates which lanes are built. Constructs stay isolated and
  unconditional.
- **Composition root** (`internal/runner`) builds the BoM through an explicit registry. No global
  registries, no `init()` self-registration, no per-tick `if blueprint == X`.

## Data path

- Most synthetic metrics go to `sink/promrw` with final pre-mangled names over Prometheus
  Remote-Write v2 (`io.prometheus.write.v2.Request`). The vendored RW2 proto is
  `internal/sink/promrw/writev2`, pinned to Prometheus v3.12.0 with provenance beside it. A workload
  with `otel.metrics: true` instead emits hand-encoded native OTLP ResourceMetrics to `/v1/metrics`.
  Neither path uses the OTel metrics SDK; OTLP traces are hand-encoded too.
- `internal/selfobs` is the sole sanctioned OTel SDK user. It instruments the synthkit process itself
  on a separate credential triplet and stack, never `GC_TOKEN`. `internal/pushhook` and
  `runner.TickFunc` are the stdlib-only seams; constructs and workloads never import OTel, selfobs or
  profiling. See ARCHITECTURE §6.1.
- Counters and histograms are cumulative across ticks (`internal/state`): push totals, never deltas.
  Request-scoped IDs come only from the per-blueprint ledger; constructs never mint them.
- CloudWatch names and five-stat expansion (`_sum/_average/_maximum/_minimum/_sample_count`) come
  from `signals/cw.md` and are emitted through `cw.EmitStats`/`cw.StatSet`. `_sum` is a per-period
  GAUGE, never a rate; suffix labels stay isolated; `cw` invents no names.
- High-cardinality keys never become Mimir labels or Loki stream labels. The blueprint selector is
  stamped only by the scoped writer after cloning. Substrate-scoped constructs (k8s, dbo11y, CSP, SM,
  FM) carry no blueprint label; declared identity disambiguates and load rejects collisions. An
  absent dimension is omitted, never `""` or `"NA"`.
- **Never invent a metric, label or field name.** Source `signals/<area>.md` or current vendor docs;
  if unconfirmed, add a PENDING to `cantfind.md` and flag it. The signals catalogue grows with every
  discovered real signal, including provenance. Correct synth output to observed data, never captured
  data to the synth.
- **Rates and volumes in a shipped blueprint must be plausible for the thing being modelled**, not
  tuned to make something show up. A rate no real deployment produces is fabricated data, and it is
  worse than an invented name because it looks legitimate. A real coding agent runs at about
  `sessions_per_min: 0.5` with `turns_p50` in the tens (`blueprints/grafana-ai-o11y.yaml`, drawn from
  captures). State the real-world basis for any rate, count or cardinality in a comment beside it.
- Never force emission by raising a blueprint's rates. A lane that must fire inside one tick belongs
  in a fixture under `e2e/fixtures/` (see `e2e/fixtures/e2e-agents.yaml`), outside `blueprints/`.
  Anything in `blueprints/` is loadable by `BLUEPRINT_NAMES=*` and deployable by an operator, so a
  test-shaped value there reaches production.

## Construct boundaries and identity

Use the smallest boundary that is independently declarable in a blueprint and has a distinct shared
identity or cross-construct join, not a delivery-pipeline boundary. Distinct fixture, declaration and
join means separate constructs sharing mechanics; same pipeline and identity declared together means
one construct with config-gated subfamilies. Resource engine and type variants are discriminators on
that declaration, not new top-level kinds.

AI/LLM vocabulary in the catalog is generic and technology-native. Customer-specific identifiers
(accounts, environments, workspaces, use cases) never enter the catalog and stay blueprint-only.

## File ownership

Single-owner wiring files, serialised rather than worked around: `internal/runner/`, `go.mod`,
`go.sum`, the shared core/fixture/shape/state/ledger types, `blueprints/*.yaml` (one owner each),
`signals/<area>.md`, the generated `BLUEPRINT-SCHEMA.md` and `fielddocs.json`, `cantfind.md`, `.env`,
`.env.example`, and `internal/archtest/arch_test.go`. Live captures are read-only findings; Docker and
live stacks are exclusive resources.

The working tree is habitually dirty with unrelated in-flight work. Stage explicit paths only; never
`git add -A` or `git add .`, and preserve unrelated dirty-tree changes.

## Secrets and environment surface

Secrets live only in the ignored `.env`, never in committed YAML, a blueprint or a doc. Service
runtime variables load via `docker-compose.yml` `env_file: .env`; Compose `${...}` interpolation reads
the project `.env` by default and can be overridden by the shell or `--env-file`. A new variable
belongs in both `.env` and `.env.example`. In `internal/config`, every read uses a literal
`get("LIT")`/`getInt("LIT")` key so `internal/config.TestEnvSurfaceAligned` can see it; that test
covers the declared Go-read, example, Compose and project-`.env` surfaces, not arbitrary external
override values. Put `.env` comments on their own lines - Docker does not strip an inline comment from
an `env_file` value.

## Operational entrypoints

`plugins/synthkit/skills/` holds the operational procedures; `just skills-sync` regenerates the
`.agents/skills/` and `.claude/skills/` symlink farms from it. Documentation fallback per job:

| Job | Skill | Documentation fallback |
|---|---|---|
| Create or edit a blueprint | `create-blueprint` | `docs/blueprints.md`, `docs/blueprint-reference.md`, `docs/tools.md` |
| First deployment | `initial-setup` | `docs/getting-started.md`, `docs/deployment.md`, `docs/credentials.md` |
| Verify a deployment | `verify-deployment` | `docs/control-plane.md`, `docs/troubleshooting.md`, `docs/RUNBOOK.md` |
| Fleet Management setup | `setup-fleet-management` | `docs/fleet-management.md` |

## Verification

- Renderer changes are checked by inventory diff, not by unit test: `just dump` prints the full
  series/label inventory for offline comparison against `signals/`.
- A new blueprint field or config struct needs the wiring pass `just gen` (regenerates
  `BLUEPRINT-SCHEMA.md` and `fielddocs.json`, and the skills symlink farm). `just gen-check` fails on
  drift.
- Every new tracked `.go` file needs the AGPL-3.0-only SPDX header on line 1 (`just spdx-check`).
- `just race` deliberately excludes `internal/integration`, which OOMs a 16 GB runner. The plain
  `just test` leg still covers it - do not "fix" the exclusion.

## Task interface

`just check` is the pre-commit gate. Three recipes mutate a live Grafana stack and are marked
`[confirm(...)]` (`selfobs-dashboard`, `corpus-gcx`, `provision`): stop and ask before running one,
and never pass `--yes` or `JUST_YES=1`. Run `just` with stdin from `/dev/null`.

## Tracker

`backlog/` is the durable queue; new work uses `SKT-NNNN`. Run `backlog instructions overview` first,
then `task-execution` before planning or changing task work, `task-creation` before creating tasks,
and `task-finalization` before acceptance checks, summaries or a terminal status change. Do not use
the Backlog MCP surface here.

Finalize acceptance checks and terminal status in one CLI call; never let two agents edit one task.
`backlog task edit` takes `--append-notes`/`--append-plan`; the bare `--notes`/`--plan` silently
replaces the whole section. Backlog content carries no real identifiers or credentials. Park blocked
work with a concrete resume boundary and leave untouched work To Do. Do not recreate retired external
issue history in another tracker.

`cantfind.md` is not the tracker: its `SK-N` IDs are stable, separate from `SKT-NNNN`, and must not be
imported or renumbered.

## Code graph

`graft/` is a local, gitignored index of this repo: every symbol, its `file:line` span, and the call
edges between them. Built by tree-sitter - no LLM, no key, no network. Six MCP tools serve it, and
each refreshes the graph before answering, so results include uncommitted edits. `just graft-build`
creates it on a fresh clone.

- `graft_repo_map` - directory clusters, per-directory hub symbols, global hotspots. One call for
  "explain this codebase" or landing in an unfamiliar area. Do not then walk every subsystem it names.
- `graft_file_api` - every signature in one file with spans, about a tenth of the cost of reading it.
  Use before editing or wiring into a file.
- `graft_find_all` - regex over every indexed file, hits grouped by enclosing symbol and ranked by
  coupling. **This is the reliable primitive.** Use it whenever you need every occurrence.
- `graft_find_code` - ranked natural-language retrieval, lexical only. It ranks on name overlap with
  your question, so it lands when the code is named the way you asked and drifts when it is not.
  Weak hits mean switch tool, not re-ask.
- `graft_trace_calls` - precomputed call edges. Correct for module-level functions. **It misses calls
  made through a receiver** (`obj.method(...)`, `Class.method(...)`). A "no indexed callers" result is
  not evidence a symbol is unused - confirm with `graft_find_all` before you rename it, change its
  signature or delete it.
- `graft_check_freshness` - drift report, never auto-refreshes.

Use `Grep` and `Read` freely for anything graft does not index. It covers source files only, so
markdown, YAML, Helm charts, the `justfile` and JSON fixtures are invisible to it.

## Deeper references

- `ARCHITECTURE.md` - read before any change to the runner, sinks, tier boundaries or the selfobs
  seam.
- `SIGNALS.md` and `signals/<area>.md` - read before adding or renaming any metric, label or field.
- `BLUEPRINT-SCHEMA.md` - generated; read for the current blueprint field surface, never hand-edit.
- Backlog docs `doc-0001` (canonical agent-fan-out protocol) and `doc-0002` (Wave operating
  model) - read both before designing a multi-lane campaign here.
- `dashboards/AGENTS.md` - read before touching anything under `dashboards/`.
