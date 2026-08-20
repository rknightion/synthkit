# synthkit — project context

General-purpose composable synthetic-telemetry generator: anyone declares infrastructure +
applications in one YAML **blueprint**; synthkit emits structurally-correct synthetic
metrics/traces/logs (+ optional RUM) to Grafana Cloud for whatever each declared construct
supports. Read [ARCHITECTURE.md](./ARCHITECTURE.md) (frozen seams + invariants I1–I34) before
touching code; [SIGNALS.md](./SIGNALS.md) indexes the authoritative per-construct data contract in [`signals/`](./signals/).

This is the canonical repository instruction file. `CLAUDE.md` is a Claude Code import adapter;
edit this file for cross-harness guidance.

## The three-tier rule (never violate)

- **Constructs** (`internal/construct/<kind>/`) and **workloads** (`internal/workload/<kind>/`)
  are isolated: they import core/fixture/shape/state/ledger/sink-types — plus the shared
  mechanic libs `internal/cw` (CloudWatch stat expansion) — ONLY; never each other, never the
  blueprint package, never a blueprint name (grep-tested by `internal/archtest`; the allowlist
  lives in `TestCatalogImportIsolation`).
- **Blueprints** (`blueprints/*.yaml`) are the only home of blueprint-specific config and the
  explicit wiring layer (workload→cluster binding, shared identity). A resource declaration
  may fan into MULTIPLE constructs, gated by an **emission switch** (e.g. a `databases:` entry's
  `observability: { cloudwatch: …, dbo11y: … }` decides whether it emits the RDS CloudWatch lane,
  the dbo11y lane, both, or neither). The blueprint gates WHICH constructs a declaration builds;
  constructs themselves stay isolated and unconditional.
- The **composition root** (`internal/runner`) instantiates the BoM via the explicit registry —
  no global registries, no `init()` self-registration, no per-tick "if blueprint == X" anywhere.

## Hard rules (cost real debugging once — do not re-learn)

- ALL metrics → `sink/promrw` with FINAL pre-mangled names via Prometheus **Remote-Write v2**
  (`io.prometheus.write.v2.Request`). The RW2 proto is vendored under
  `internal/sink/promrw/writev2` (pinned to Prometheus v3.12.0, provenance in
  `internal/sink/promrw/writev2/PROVENANCE.md`; regenerate with `make proto` using
  `protoc` + `protoc-gen-go`). OTel metrics SDK is banned on the synthetic-data path — the sink
  uses `google.golang.org/protobuf` (the protobuf runtime), NOT the metrics SDK. OTLP
  carries traces only (hand-encoded ResourceSpans protos). **This ban is about the SYNTHETIC-DATA
  path only.** The generator's OWN telemetry (self-observability + profiling) is a separate concern:
  `internal/selfobs` is the SOLE sanctioned OTel SDK user — it instruments the synthkit PROCESS and
  ships to a SEPARATE stack via its own credential triplet (never `GC_TOKEN`). It is isolated from
  the synthetic path by two stdlib-only seams — `internal/pushhook` (sink push outcomes) and
  `runner.TickFunc` (per-tick spans) — so neither the sinks nor the runner ever link the SDK.
  Constructs/workloads must NEVER import the OTel SDK, selfobs, or profiling. See ARCHITECTURE §6.1.
- Counters/histograms are cumulative across ticks (`internal/state`); push totals, never deltas.
- CloudWatch `_sum` series are per-period GAUGES — never rate(). CW naming convention is law.
  The five-stat expansion (`_sum/_average/_maximum/_minimum/_sample_count`), the gauge rule, and
  per-suffix label isolation live in `internal/cw` (`cw.EmitStats` + `cw.StatSet`); every AWS
  construct delegates there and keeps only its own value policy. `cw` does NOT generate names
  (that would invent them) — pass the exact `signals/cw.md`-sourced base.
- Request-scoped IDs come from the per-blueprint ledger ONLY. Constructs never mint.
- High-card keys never become Mimir labels or Loki stream labels (the Loki sink asserts).
- The blueprint selector label is stamped only by the scoped writer (clone-before-stamp).
- Substrate-scoped constructs (k8s, dbo11y, CSP, SM, FM) never carry a blueprint label —
  blueprint-declared identity (cluster, account_id, instance) disambiguates; collisions are
  rejected at load.
- An absent dimension is OMITTED — never "" or "NA".
- NEVER invent a metric/label/field name. Source it from `signals/<area>.md` (or vendor docs
  via ctx7); otherwise add a PENDING entry to cantfind.md and flag it.
- The `signals/` catalogue is the LIVING, DEFINITIVE library of every metric/label/value — it is meant to GROW.
  Whenever you discover real observability data through ANY pathway (live capture, exporter/agent
  inspection, metric-stream output, vendor docs), record it in the right `signals/<area>.md` — including signals NOT
  previously listed — and update the constructs/fixtures/structs to match the captured reality.
  Accuracy of metric names + labels + values against real data is the guiding principle: when synth
  diverges from observed reality, correct the synth (see the realism-direction rule), not the data.
  Real-data discovery RESOLVES the matching cantfind.md PENDING (move it into `signals/<area>.md`), and may add
  entirely new families. Capture provenance + date in `signals/<area>.md` as the existing entries do.

## Construct granularity (read before adding infra constructs)

The catalog will grow a lot (more clouds, more k8s app workloads, anything anyone wants to model).
Draw a construct boundary at the **smallest unit that is independently declarable in a blueprint AND
carries a distinct shared identity / cross-construct join** — NOT at the delivery pipeline:

- Distinct fixture + distinct declaration + distinct join → **separate construct**, sharing a
  mechanic lib. (ec2/rds/elasticache are separate — each hangs off its own `fixture.Node`/`DB`/`Cache`
  and joins elsewhere — and all share `internal/cw`.)
- Same pipeline + same identity + declared together → **one construct with config-gated sub-families**.
  (`cwinfra` bundles ALB/EBS/NAT/EKS/S3/Firehose off one cloud identity; `k8s_cluster` gates
  OpenCost/Kepler/Alloy; `cwinfra` gates ALB/NLB/EBS/NAT/EKS/S3/Firehose via `cloud.cloudwatch`;
  `cspazure`/`cspgcp` gate per-service families via `sub_signals`. Empty/omitted ⇒ all emit.)
- Engine/type variants of one resource (RDS Postgres vs Aurora Postgres) are a config discriminator
  on the same declaration + construct, gated by the same emission switch — not a new top-level kind.

See ARCHITECTURE §3 (construct boundaries + emission switch) for the full rationale.

## Working style

- **Execution: subagent-driven and parallel where seams are disjoint.** For any non-trivial build,
  decompose the work into lanes that have one file owner and a checkable acceptance result. Read the
  canonical **Agent fan-out protocol** (`backlog doc view doc-0001 --plain`) and this repository's
  **Wave operating model** (`backlog doc view doc-0002 --plain`) before designing a campaign. Route
  each lane by role — RETRIEVAL, MAPPING, GATE, EXECUTION, JUDGMENT+EXECUTION, REVIEW,
  DESIGN+INTEGRATION, or SECURITY — and record the harness-resolved route in its brief; do not
  hard-code a harness model or plugin name in this portable contract. The root owns architecture,
  integration, tracker and other external mutations, commits, pushes, final gates, and final
  synthesis. A lane does not commit or push unless that authority is explicitly assigned.
- **Branching policy:** Work directly on `main` in this repository. Do not create feature branches or
  pull requests; keep changes scoped and require the relevant green gate before committing.
- Plans/specs are SCRATCH → `docs/superpowers/` (gitignored). Only code + the durable docs
  (AGENTS.md, CLAUDE.md, SIGNALS.md + `signals/`, ARCHITECTURE.md, README.md) are committed.
- Parallel lanes touch DISJOINT files; one file = one owner. Wiring files (registry/catalog,
  YAML schema, go.mod, fixture vocabulary) are single-owner, edited in a dedicated wiring pass.
- **Multiple agents often work this codebase CONCURRENTLY.** When Rob sees overlap risk between
  your task and another agent's, he'll point you at the other agent's plan (`docs/superpowers/plans/`)
  to assess for conflicts; when there's no overlap he gives no plan — proceed. Either way: you WILL
  see other agents' unstaged/uncommitted work + new untracked files in the tree that are NOT yours —
  NEVER stage, commit, or revert them (always `git add <explicit paths>`, never `-A`/`.`), and ignore
  failing tests/build breakage that belong to that in-flight work while it's present. Verify your own
  work in the packages you own; defer the full e2e/`make gate` run until the other agents finish (or
  scope verification to your packages and say so). If a file you must edit is mid-flight under another
  agent, coordinate/sequence rather than racing it.
- Strict TDD on logic (ledger, state, shape, blueprint loader/resolver, runner). Renderers are
  data-shape: validate with `-once -dump` inventory diff against `signals/` + adversarial review.
- Reserve an independent adversarial review for the end of a significant or high-risk change, not
  after every artifact or phase. Run the integrated final gate once, with evidence, before commit.
- No customer-specific identifiers anywhere in the catalog (blueprint names, account ids,
  env/workspace strings stay generic or fictional). Technology-native signal names — including
  Portkey/Bedrock/AgentCore/LangSmith/LangGraph/gen_ai/Snowflake — carry over UNCHANGED (they
  are generic constructs).
- **AI/LLM constructs are tech-native and generic** (ban lifted, Spec 2b). The gen_ai/LangGraph
  vocabulary lives in `internal/genai` (a mechanic lib, peer to `internal/cw`); AI metric families
  (portkey/bedrock/agentcore/langsmith/snowflake) are ordinary constructs; the AI request flow
  (gen_ai trace hops + metrics + correlated logs) is workload-emitted via the Spec-1 `hopStamper`
  registry + ledger hop tree. Constructs/workloads still NEVER import the OTel SDK (gen_ai metrics
  go via promrw final names; spans via the hand-encoded OTLP seam). ALL customer identity
  (account IDs, env names, workspace/use-case strings) stays blueprint-only.
- **Secrets live ONLY in `.env`** (gitignored) — NEVER in committed YAML. The committed
  `docker-compose.yml` reads every var via `env_file: .env`; deploying = `git pull` on the server,
  then scp the `.env` across. New env vars: add to `.env` + `.env.example` (env_file passes them
  through automatically — the compose needs no per-var change). All env reads in `internal/config`
  go through `get("LIT")`/`getInt("LIT")` with string-literal keys so the **gate-enforced**
  `TestEnvSurfaceAligned` (run via `make env-check` or the normal `go test`) can keep the four
  surfaces aligned: every Go-read var is in `.env.example`, every compose `${interpolation}` is
  documented, no stale example keys, and the local `.env` provisions them all. Add a comment as
  its OWN line in `.env`/`.env.example` — Docker's `env_file` does NOT strip inline `value # comment`.

## Operational entrypoints (fresh clones)

Use the repository-local skill source as the agent entrypoint; the synced `.agents/skills/` and
`.claude/skills/` paths point to the same files when available. These entrypoints contain procedure,
not credential values. If a skill is unavailable, use the listed documentation fallback.

| Job | Agent entrypoint | Documentation fallback |
|---|---|---|
| Create or edit a blueprint | [`create-blueprint`](./plugins/synthkit/skills/create-blueprint/SKILL.md) | [`blueprints`](./docs/blueprints.md), [`blueprint-reference`](./docs/blueprint-reference.md), [`tools`](./docs/tools.md) |
| Set up a first deployment | [`initial-setup`](./plugins/synthkit/skills/initial-setup/SKILL.md) | [`getting-started`](./docs/getting-started.md), [`deployment`](./docs/deployment.md), [`credentials`](./docs/credentials.md) |
| Verify a deployment | [`verify-deployment`](./plugins/synthkit/skills/verify-deployment/SKILL.md) | [`control-plane`](./docs/control-plane.md), [`troubleshooting`](./docs/troubleshooting.md), [`RUNBOOK`](./docs/RUNBOOK.md) |

Keep secrets in the local ignored environment file and follow the entrypoint's safe presence-check
procedure; never add credential values to a blueprint, documentation, or instruction file.

## Build & verify

```bash
go build ./... && go vet ./... && go test ./...
DRY_RUN=true go run ./cmd/synthkit -once -dump   # series inventory → diff vs signals/
```

## Task tracking — Backlog.md

Open work lives in `backlog/`, driven **only** through the `backlog` CLI. `backlog task list --plain`
is the queue; `backlog doc list --plain` lists the durable docs. New work is `SKT-NNNN`.

GitHub Issues was retired for this repo on **2026-08-14**, and the one issue synthkit had ever closed
(`#26`, the docs site) was **deleted from GitHub with no archive**. `gh issue view 26` 404s and the
body is not recoverable, so the *Closed GitHub issues* doc **is the record**, not a pointer — treat it
as the only surviving account of that work. The GitHub tracker itself stays **open on purpose**:
external contributors can file there, and Renovate's dependency dashboard (`#9`) lives there and is
recreated on every run. Anything arriving that way becomes an `SKT-NNNN` task; the board, not the
issue, is where it is worked.

Read the **Agent fan-out protocol (canonical)** doc before designing a wave, and the **Wave operating
model** doc for this project's own rules — lane conventions, the single-owner wiring files, the
exclusive resources, and the ownership escape hatch. Docs load on demand via `backlog doc view <id>
--plain`, so neither costs context until something reads it. The protocol is harness-neutral: it
routes lanes by **role**, and its Appendix A (Codex) or Appendix B (Claude Code) resolves a role into
a concrete model and reasoning depth. Name the harness in the run contract and resolve every lane
from that profile.

- **`backlog/` is committed, so no real identifiers in tasks or docs.** No email addresses, handles,
  usernames, account IDs, device or host names, addresses, or credential values — write the shape,
  not the instance ("the reference cluster", not its name). This repo already has a
  `make forbidden-words` guard for exactly this class of leak, and `backlog/` is inside its scope.
  Aggregate counts, timings and structural findings are fine. A tracker *feels* private, which is
  precisely why this breaks by accident.
- **Never use `--notes` or `--plan` bare** — they *silently replace* the whole section, destroying
  another session's writes with no warning and exit 0. Use `--append-notes` and `--append-plan`.
  a global `PreToolUse` hook in the agent config denies the unsafe forms rather than trusting anyone to remember.
- **Finalize in one call**, so an interrupted run cannot leave finished work looking unfinished:
  `backlog task edit SKT-0007 --check-ac 1 --check-ac 2 -s Done`. Checking criteria at one step and
  setting status several steps later leaves the task inconsistent if anything interrupts between.
- **Never hand-edit task, draft, doc, decision or milestone markdown.** Section boundaries are
  HTML-comment markers; break one and the section is *silently dropped* at exit 0 — still in the file
  but invisible to the CLI, until the next write destroys it for real. There is no repair command;
  `backlog doctor` only fixes duplicate task IDs. `backlog/config.yml` is the one file edited by
  hand, because list-valued keys cannot be set through `backlog config set`.
- **Never let two agents edit the same task.** The v1.50 concurrency fix covers the edit funnel but
  *not* reorder, draft saves, the TUI edit path, `doc update` or decision updates.
- **`Parked` is a real status**, not a synonym for To Do: attempted, blocked, and left with a
  concrete resume boundary. Flattening it loses the most valuable thing a long autonomous run
  produces.
- **`cantfind.md` is NOT the tracker and must not be mirrored onto it.** Its `SK-N` IDs are stable,
  never renumbered, never reused, and already cited across `signals/`, code comments and commits.
  A task may point at an `SK-N`; it never replaces one. Importing them would create a second ID
  space over the same history.
- **Do not build on decisions, and do not use the MCP surface.** Decisions are half-built upstream —
  no `edit`, `view` or `update`, no supersede mechanism, no validation — so durable reference goes in
  **docs** and tasks stay the unit. MCP is frozen upstream and costs 10-50k tokens of permanent
  context against 1-2k for the CLI.

<!-- BACKLOG.MD GUIDELINES START -->
<!-- backlog.md-instructions-version: 1.50.1 -->
<CRITICAL_INSTRUCTION>

## Backlog.md Workflow

This project uses Backlog.md for task and project management.

**For every user request in this project, run `backlog instructions overview` before answering or taking action.**

Use the overview to decide whether to search, read, create, or update Backlog tasks.

Before task lifecycle actions, read the matching detailed guide:
- `backlog instructions task-creation` before creating or splitting tasks
- `backlog instructions task-execution` before planning, changing status or assignee, adding a plan or implementation notes, or implementing task work
- `backlog instructions task-finalization` before checking acceptance criteria, writing final summaries, or moving tasks to terminal statuses

Use `backlog <command> --help` before running unfamiliar commands. Help shows options, fields, and examples.

Do not edit Backlog task, draft, document, decision, or milestone markdown files directly. Use the `backlog` CLI so metadata, relationships, and history stay consistent.

</CRITICAL_INSTRUCTION>
<!-- BACKLOG.MD GUIDELINES END -->
