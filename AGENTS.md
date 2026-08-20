# synthkit — project context

General-purpose composable synthetic-telemetry generator: YAML **blueprints** declare
infrastructure and applications; synthkit emits structurally-correct synthetic metrics, traces,
logs, and optional RUM. Read [ARCHITECTURE.md](./ARCHITECTURE.md) before code changes and use
[SIGNALS.md](./SIGNALS.md) plus `signals/` as the authoritative per-construct data contract.
This file is canonical. Root and `dashboards/` `CLAUDE.md` files are import adapters; keep them
unchanged. Detailed campaign procedure is on demand in Backlog docs `doc-0001` (canonical
agent-fan-out protocol) and `doc-0002` (synthkit wave additions).

## Architecture contract

The three tiers are mandatory:

- **Constructs** (`internal/construct/<kind>/`) and **workloads** (`internal/workload/<kind>/`)
  are isolated. They may import core/fixture/shape/state/ledger/sink-types and shared mechanic
  libraries (`internal/cw`, `internal/genai`) only: never each other, the blueprint package, or a
  blueprint name. `internal/archtest`/`TestCatalogImportIsolation` enforces this boundary.
- **Blueprints** (`blueprints/*.yaml`) own blueprint-specific configuration and explicit wiring
  (including workload-to-cluster binding and shared identity). A declaration may fan into multiple
  constructs; its emission switch gates which lanes are built. Constructs remain isolated and
  unconditional.
- **Composition root** (`internal/runner`) builds the BoM through an explicit registry: no global
  registries, `init()` self-registration, or per-tick `if blueprint == X` logic.

Non-negotiable data-path rules:

- All synthetic metrics go to `sink/promrw` with final pre-mangled names via Prometheus Remote-
  Write v2 (`io.prometheus.write.v2.Request`); vendored RW2 is under
  `internal/sink/promrw/writev2`, pinned to Prometheus v3.12.0; provenance is kept beside the
  vendored proto. Regenerate with `make proto` (`protoc` + `protoc-gen-go`). The
  OTel metrics SDK is banned on this path; OTLP carries hand-encoded traces only.
- `internal/selfobs` is the sole sanctioned OTel SDK user: it instruments the synthkit process
  and uses a separate credential triplet/stack, never `GC_TOKEN`. `internal/pushhook` and
  `runner.TickFunc` are the stdlib-only seams. Constructs/workloads never import OTel, selfobs,
  or profiling. See ARCHITECTURE §6.1.
- Counters and histograms are cumulative across ticks (`internal/state`); push totals, never
  deltas. Request-scoped IDs come only from the per-blueprint ledger; constructs never mint them.
- CloudWatch names and five-stat expansion (`_sum/_average/_maximum/_minimum/_sample_count`) are
  sourced from `signals/cw.md` and emitted through `internal/cw` (`cw.EmitStats`/`cw.StatSet`).
  `_sum` is a per-period GAUGE, never a rate; suffix labels remain isolated; `cw` does not invent
  names.
- High-cardinality keys never become Mimir labels or Loki stream labels. The blueprint selector
  is stamped only by the scoped writer after cloning. Substrate-scoped constructs (k8s, dbo11y,
  CSP, SM, FM) never carry a blueprint label; declared identity disambiguates and load rejects
  collisions. An absent dimension is omitted, never `""` or `"NA"`.
- Never invent a metric, label, or field name. Source `signals/<area>.md` or current vendor docs;
  if unconfirmed, add a PENDING to `cantfind.md` and flag it. The signals catalogue grows with
  every discovered real signal, including provenance/date; correct synth output to observed data,
  never captured data to the synth. `cantfind.md` `SK-N` IDs are stable and separate from tasks.

## Construct boundaries and identity

Use the smallest boundary that is independently declarable in a blueprint and has a distinct
shared identity/cross-construct join, not a delivery-pipeline boundary. Distinct fixture,
declaration, and join means separate constructs sharing mechanics; same pipeline and identity
declared together means one construct with config-gated subfamilies. Resource engine/type variants
are discriminators on that declaration, not new top-level kinds. AI/LLM vocabulary is generic and
technology-native; customer-specific identifiers (accounts, environments, workspaces/use cases)
never enter the catalog and remain blueprint-only.

## Safe parallel work and repository policy

For non-trivial work, use role-based lanes and one file owner. Read `doc-0001` and `doc-0002`
before designing a campaign; the run contract resolves routes for the current harness. The root
owns architecture, integration, wiring, tracker/external mutations, commits, pushes, final gates,
and synthesis. Lanes do not delegate, commit, push, or edit another lane's files.

Single-owner wiring files include `internal/runner/`, `go.mod`/`go.sum`, shared core/fixture/shape/
state/ledger types, `blueprints/*.yaml` (one owner each), `signals/<area>.md`, generated
`BLUEPRINT-SCHEMA.md`/`fielddocs.json`, `cantfind.md`, `.env`/`.env.example`, and
`internal/archtest/arch_test.go`. Serialize these; return a precise hand-off request instead of
working around ownership. Live captures are read-only findings; Docker and live stacks are
exclusive resources.

Work directly on `main`; do not create branches or PRs for this repository. Preserve unrelated
dirty-tree changes. Stage explicit paths only; never use `git add -A` or `git add .`. The root
commits completed work to `main` and pushes after the relevant green gate.

## Secrets and environment surface

Secrets live only in ignored `.env`, never committed YAML. `docker-compose.yml` reads variables via
`env_file: .env`; new variables belong in both `.env` and `.env.example`. In `internal/config`,
all reads use literal `get("LIT")`/`getInt("LIT")` keys. `TestEnvSurfaceAligned` checks Go reads,
`.env.example`, compose interpolation, and local `.env`. Put `.env` comments on their own lines;
Docker does not strip inline comments from `env_file` values.

## Operational entrypoints

Use the repository-local skill source (synced to `.agents/skills/` and `.claude/skills/`) for:

| Job | Skill | Documentation fallback |
|---|---|---|
| Create/edit blueprint | `plugins/synthkit/skills/create-blueprint/SKILL.md` | `docs/blueprints.md`, `docs/blueprint-reference.md`, `docs/tools.md` |
| First deployment | `plugins/synthkit/skills/initial-setup/SKILL.md` | `docs/getting-started.md`, `docs/deployment.md`, `docs/credentials.md` |
| Verify deployment | `plugins/synthkit/skills/verify-deployment/SKILL.md` | `docs/control-plane.md`, `docs/troubleshooting.md`, `docs/RUNBOOK.md` |

Skills contain procedure, not credentials. Follow their safe presence checks; never put credential
values in blueprints, docs, or instructions.

## Verification

Logic changes use focused tests; renderers use `-once -dump` inventory comparison against
`signals/`. New Go files need the SPDX header. Blueprint fields/config structs require the wiring
pass `make blueprint-schema`. The race leg intentionally excludes `internal/integration`; the
plain test leg still covers it. Run the proportionate check during work and the root-owned final
gate once:

```bash
go build ./... && go vet ./... && go test ./...
DRY_RUN=true go run ./cmd/synthkit -once -dump
make gate
```

## Backlog.md workflow

For every request, first run `backlog instructions overview`; use `backlog task list --plain` and
`backlog doc list --plain` for discovery. Read `backlog instructions task-execution` before
planning/changing task work, `task-creation` before creating tasks, and `task-finalization` before
acceptance checks, summaries, or terminal status changes. The tracker is the only task/doc source:
never hand-edit its Markdown or use the MCP surface. Use CLI help for unfamiliar commands.

Use `backlog task edit` with `--append-notes`/`--append-plan`; bare `--notes`/`--plan` replaces
content. Never let two agents edit one task. Finalize acceptance checks and terminal status in one
CLI call. Backlog content contains no real identifiers or credentials. `cantfind.md` is not the
tracker and its `SK-N` IDs must not be imported or renumbered.

The repository's durable task queue is `backlog/`; new work uses `SKT-NNNN`. Do not recreate
retired external issue history in another tracker. Park blocked work with a concrete resume
boundary; keep untouched work To Do.
