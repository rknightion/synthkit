# synthkit — project context

General-purpose composable synthetic-telemetry generator: anyone declares infrastructure +
applications in one YAML **blueprint**; synthkit emits structurally-correct synthetic
metrics/traces/logs (+ optional RUM) to Grafana Cloud for whatever each declared construct
supports. Read [ARCHITECTURE.md](./ARCHITECTURE.md) (frozen seams + invariants I1–I34) before
touching code; [SIGNALS.md](./SIGNALS.md) indexes the authoritative per-construct data contract in [`signals/`](./signals/).

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

- **Execution: ALWAYS subagent-driven, highly parallel.** For any non-trivial build, decompose into
  tasks and dispatch fresh subagents (`superpowers:subagent-driven-development`), parallelising every
  lane that touches disjoint files. Prefer **Sonnet** child agents when the task is well-scoped/low-
  judgment (extraction, table-driven tests, fixtures, proto/codegen, doc regen) for cost + speed;
  keep design, cross-cutting integration, and the final adversarial review on the main (Opus) thread.
  Rob will always pick subagent-driven over inline — do not ask which.
- **Branching policy:** SIGNIFICANT features/developments (new signal types, new workloads/constructs,
  new sinks, cross-cutting architecture) → build on a **feature branch** and submit via **PR** to the
  repo. Smaller things — fixes, CI/chore, doc tweaks, single-construct touch-ups — still go **straight
  to `main`**. Green (build + vet + test) is mandatory before every commit — evidence, not assertion.
- Plans/specs are SCRATCH → `docs/superpowers/` (gitignored). Only code + the durable docs
  (SIGNALS.md + `signals/`, ARCHITECTURE.md, CLAUDE.md, README.md) are committed.
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
- Adversarial/critical review runs on **Opus**, as a fresh independent-context agent — but reserve
  it for the END of a SIGNIFICANT code change, not after every artifact or phase, so reviews stay
  rare and high-signal (one comprehensive pass, not a per-commit reflex).
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

## Build & verify

```bash
go build ./... && go vet ./... && go test ./...
DRY_RUN=true go run ./cmd/synthkit -once -dump   # series inventory → diff vs signals/
```
