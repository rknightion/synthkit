# synthkit

Composable synthetic-telemetry generator: YAML blueprints under `blueprints/` declare infrastructure
and applications; synthkit emits structurally-correct synthetic metrics, traces, logs and optional
RUM. `ARCHITECTURE.md` is the design contract and `SIGNALS.md` plus `signals/` is the authoritative
per-construct data contract.

## Architecture contract

Three tiers, mandatory:

- **Constructs** (`internal/construct/<kind>/`) and **workloads** (`internal/workload/<kind>/`) are
  isolated. They may import core/fixture/shape/state/ledger/sink-types and the shared mechanic
  libraries (`internal/cw`, `internal/genai`, `internal/syslog`) only: never each other, the blueprint package, or a
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
The current Agent Observability producer/token/tag contract is [signals/sigil.md](signals/sigil.md).
Generation-stage types alone install no hooks or executor and never authorize backend-derived
telemetry; legacy synthetic scoring must not overlap real rules scoring the same agents.
Pure declaration discovery uses canonical registration callbacks over the full selected post-fanout
set, including inactive legacy agents and their effective native child names, with explicit no-agent
coverage. It shares Build's structural Binding, not runtime collaborators; claims establish neither
runtime custody nor admission.

Agent Observability custody state uses an exact binding-revision gate for its larger state-only
ceiling; input limits stay unchanged. Preserve legacy fail-closed evidence, compatible-binary
pinning and no-migration/no-downgrade semantics. See ARCHITECTURE's custody state capacity contract;
this foundation does not establish typed lifecycle or runtime admission.

## Syslog mechanic

`internal/syslog` renders decoded synthetic RFC5424/RFC3164 facts; it is not a raw parser or network
listener. Owning consumers select the receiver shape. The existing `alloy_health` addon exposes
optional receiver health only, with zero-default intake and no device-log emission. OTel default
`on_error: send` forwards malformed records; accepted/refused/failed counters measure downstream
handoff, not parse failures. Only the Loki receiver has the sourced parsing-error counter. Public
loader/runner tests belong outside construct directories, whose isolation scanner includes tests.

## GPU fixture and consumer admission

`gpu_compute` declares the canonical generic topology in `internal/fixture/gpu*`; integration selectors
are `gpu_pools`, `gpu_fabrics`, `gpu_storage_clusters` and `gpu_schedulers`. Generated EKS node private
IPs are cluster-local; global hostname/IP collision claims apply to fixed physical inventories and
GPU-associated hosts. Traditional host hostname claims remain unchanged.

`hgx` is an additive eight-GPU H100 SXM shape with four node-local NVSwitches and eighteen links
per GPU. Its endpoint projection is pinned to Fabric Manager 595.91.07-1ubuntu1. Fixture slots and
physical module IDs are separate namespaces; an omitted module ID is a synthetic slot binding.
H100 SXM thermal limits are scoped to the captured driver 595.91.07 profile. Usable framebuffer
still requires an explicit per-GPU sourced declaration; switch inventory does not admit exporter
entity mappings or telemetry. The full fixed-input legacy compatibility test runs in the existing
plain integration tier, while HGX validation, identity and fault checks remain race-covered.

`cluster.emit: false` retains declared topology and workload bindings while suppressing cluster-derived
Kubernetes, EC2, profiling, addon and Fleet Management instances. Omitted/true preserves existing
emission; disabled declarations still reject unknown registry kinds and nested config fields.
`k8s_monitoring.enabled: false` alone does not stop base Kubernetes telemetry. Keep unadapted GPU
compute emission disabled until its capacity/platform/NIC and other collection adapters are verified.

Generic topology, allocation and operator-assumption physics do not establish vendor telemetry facts.
The sourced catalogue leaves unsupported product/driver thermal limits, usable framebuffer defaults
and unsourced physical NVLink metadata nullable. Consumer profiles fail closed on required missing capabilities. Advertised
GB is not driver usable/reserved memory; synthetic cooling deltas are not hardware throttle thresholds.
See `cantfind.md` hardware/profile barriers and the pinned consumer contract before enabling collection.

`GPUOperatingPoints` remains unchanged and does not establish active clock constraints.
The additive `GPUConstraintPoints` API requires explicit requested clocks, unconstrained power
demand and synthetic per-cause caps. Only H100 SXM with exact driver 595.91.07 has a sourced profile.
Thermal slowdown requires temperature strictly above 89 C and a reducing cap; the 87 C operating
reference, equality to a power budget and `CoolingDerate` alone never activate a limiter. Power
requires demand strictly above the configured budget and a reducing cap; both causes may coexist.
Unsupported/unavailable GPUs have no achieved clocks. Consumers must not infer vendor event labels,
violation durations or a clock response curve from this seam alone.

The `dcgm` consumer admits only the sourced eight-GPU H100 SXM HGX/595.91.07 profile.
Shared optional pool `operating.constraints` supplies allocation-derived uncapped demand
and explicit caps; the additive power incident has separate mode/selection APIs so legacy
fixture output stays byte-identical. Fault-profile admission preserves the <=87 C baseline
and requires worst-case fault temperature strictly below 95 C. Source field windows, rising
clock edges and elapsed ns are distinct; initial observations do not invent prior duration.
The pinned exporter uses `CLOCK_THROTTLE_*` health labels and omits clock COUNT for a window
of valid zero masks, unlike its zero fallback for absent source values. Keep other compute
collection adapters and the shipped reference cluster disabled until separately verified.

## File ownership

Single-owner wiring files, serialised rather than worked around: `internal/runner/`, `go.mod`,
`go.sum`, the shared core/fixture/shape/state/ledger types, `blueprints/*.yaml` (one owner each),
`signals/<area>.md`, the generated `BLUEPRINT-SCHEMA.md` and `fielddocs.json`, `cantfind.md`, `.env`,
`.env.example`, and `internal/archtest/arch_test.go`. Live captures are read-only findings; Docker and
live stacks are exclusive resources.

The working tree is habitually dirty with unrelated in-flight work.

## Container trust stores

Distroless can lag Debian security updates. The runtime and e2e receiver copy the patched Debian
trust store into fresh directories and set both `SSL_CERT_FILE` and `SSL_CERT_DIR`; preserve those
overrides so base-image certificate files cannot reintroduce distrusted roots. Node's build stage
also installs both patched Alpine certificate packages before fetching UI dependencies; upgrading only
the bundle can leave the active trust directory stale.

The chart's immutable image digest may reference a verified main publication. Keep its matching
main-SHA tag in the cleanup workflow's `protected-tags` alongside the default stable protections;
ordinary edge images expire after seven days. Update the protection with the chart image pin.

## Secrets and environment surface

Secrets live only in the ignored `.env`, never in committed YAML, a blueprint or a doc. Service
runtime variables load via `docker-compose.yml` `env_file: .env`; Compose `${...}` interpolation reads
the project `.env` by default and can be overridden by the shell or `--env-file`. A new variable
belongs in both `.env` and `.env.example`. In `internal/config`, every read uses a literal
`get("LIT")`/`getInt("LIT")` key so `internal/config.TestEnvSurfaceAligned` can see it; that test
covers the declared Go-read, example, Compose and project-`.env` surfaces, not arbitrary external
override values. Put `.env` comments on their own lines - Docker does not strip an inline comment from
an `env_file` value.

## Contributor workflow

Before editing repository code, configuration, blueprints or documentation, agents must read and
follow `plugins/synthkit/skills/contribute/SKILL.md`. Humans use `CONTRIBUTING.md` and
`docs/contributing.md`; both routes use the same canonical `justfile` task surface. Operational
skills remain scoped to deployment and authoring jobs and do not replace contribution validation.
Use the relevant operational skill as well when the change falls within its scope.

Record the commands actually run and their outcomes. A skill invocation, unavailable tool, skipped
check or owner waiver is not passing evidence. CI checks the resulting change; it cannot prove that
an agent read a skill. Public contributors need no maintainer-local paths or private review service
credentials. Maintainer CodeRabbit review requirements remain applicable to code changes.

When behavior changes, reconcile its canonical contract, user guide and relevant skill together.
Prefer links to canonical instructions over copied command sequences. Keep generated schema and
skill links synchronized. Documentation validation checks structural integrity, not semantic truth;
verify runtime claims against code and examples against the public entry point.

## Console design contract

Shared CSS values live in `internal/control/ui/design-tokens/tokens.css`; the console theme
file is a compatibility and layout adapter. Tokens follow the approved design-board artifact
version `1791408283-0027`; preserve its light/dark contract when adding consumers.

## Operational entrypoints

`plugins/synthkit/skills/` holds the operational procedures; `just skills-sync` regenerates the
`.agents/skills/` and `.claude/skills/` symlink farms from it. Documentation fallback per job:

| Job | Skill | Documentation fallback |
|---|---|---|
| Contribute a repository change | `contribute` | `CONTRIBUTING.md`, `docs/contributing.md` |
| Create or edit a blueprint | `create-blueprint` | `docs/blueprints.md`, `docs/blueprint-reference.md`, `docs/tools.md` |
| First deployment | `initial-setup` | `docs/getting-started.md`, `docs/deployment.md`, `docs/credentials.md` |
| Verify a deployment | `verify-deployment` | `docs/control-plane.md`, `docs/troubleshooting.md`, `docs/RUNBOOK.md` |
| Fleet Management setup | `setup-fleet-management` | `docs/fleet-management.md` |

## Verification

OTLP pod-log inventory retains emitter-selected `collector_profile` provenance separately.
Corpus comparison scopes known profiles to their matching captured collector; unknown and
unattributed evidence remains independently compared. Missing matching profiles retain wrong-profile
evidence. Provenance never reaches the wire; captured reality and the sole exemption stay unchanged.

- Synthetic Monitoring probe coordinates resolve once from paired blueprint latitude/longitude;
  omitted coordinates retain Frankfurt and explicit zero is valid. Shared probe names require
  identical region and coordinates across blueprints. The provisioner snapshot and emitted geohash
  use that resolved location. Acquired file provisioner stores never publish snapshots, even after
  release; emitter publication uses the ordinary file store and positive lock acquisition.
- SM network profiles are pinned to agent v0.66.1: ping, DNS A/UDP/53, TCP with optional TLS,
  and default traceroute. Traceroute requires 30-second timeout and at least 120-second frequency;
  loss is a fraction in metrics and a percentage in hop logs. HTTP and network derived duration
  histograms use the pinned agent Prometheus DefBuckets, including the four sub-100ms HTTP buckets.
  HTTP snapshot hashes stay compatible.
  HA plus SM chart wiring uses named state, separate principals/Leases, and a separate emitter
  ConfigMap rule granting only get/update. It grants no provisioner-Lease access or automatic apply.
  The default chart image predates this engine contract; select matching image >=1.6.0-rc.176.
- Go ignores nested modules, not Git ignore rules. Retained Go proof sources under `codex/` use
  the ignored `codex/go.mod` boundary; preserve production package discovery and all proof artifacts.

- Sink acknowledgement is protocol acceptance, not backend readback or a durable checkpoint watermark.
  OTLP partial rejection and contradictory RW2 written counts fail without whole-request retries;
  unknown/deferred acknowledgements cannot certify continuity. Queue Flush reports background
  failures since the previous barrier; it is not a durable outbox. The optional checkpoint package
  stores opaque payloads only, requires actual leadership authority for writes, and leaves runtime
  snapshot/restore, takeover and backfill disabled. Cancelled publications retain evidence and may
  require explicit recovery; do not silently reinitialize unbound storage.

- HA-loss subprocess proofs synchronize on the real HTTP handler's recorded observation before
  revocation; sender entry or an elapsed sleep is not a delivery receipt. Keep the five-second
  subprocess bound, partial-batch exclusion, admission fence and hard-exit assertions intact.
- Fleet cleanup intent is destination-bound. Retain old intent on URL/stack mismatch or nonempty
  unbound legacy state; never silently relabel it. Explicit teardown requires actual completion,
  while the binary's ordinary restart leaves collectors registered. Chart-managed Kubernetes Fleet wiring
  remains separate from the runtime adapter and requires the generic named-state chart seam.
- Chart checks require `helm` on PATH, as in CI's unpinned preinstalled toolchain; locally verified versions are 3.19.0, 3.20.0 and 4.3.0. The operator-owned-state proof uses combined server dry-run/validate flags for the verified Helm 3 versions and server dry-run alone for 4.3.0, preserving discovery/precreation assertions; other versions remain unverified.
- Secret scanning covers Git history. A forward prose edit does not clear a historical finding;
  preserve credential canaries and history scope rather than adding a blanket file exception.
  The forbidden-words guard also decompresses gzip assets before applying its selected public
  and CI/local patterns, and rejects malformed gzip; a binary-only scan is not corpus clearance.

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
Run `just check` and `just ci` off the Mac with `gaterun -- just check` when the remote gate runner is
installed (`which gaterun`); `.gaterunner.yaml` is its contract. Run one test or package locally.

## Tracker

New work uses `SKT-NNNN`. Do not use the Backlog MCP surface here. Do not recreate retired external
issue history in another tracker.

`cantfind.md` is not the tracker: its `SK-N` IDs are stable, separate from `SKT-NNNN`, and must not be
imported or renumbered.

## Deeper references

- `ARCHITECTURE.md` - read before any change to the runner, sinks, tier boundaries or the selfobs
  seam.
- `SIGNALS.md` and `signals/<area>.md` - read before adding or renaming any metric, label or field.
- `BLUEPRINT-SCHEMA.md` - generated; read for the current blueprint field surface, never hand-edit.
- Loops: the protocol lives in `~/repos/agent-docs/sources/loop/` (this board's "Loop protocol pointer" doc points there); this repository's loop facts are in `LOOP.md`. These are not prerequisites for an ordinary public contribution; never assume a contributor has a maintainer checkout.
- `dashboards/AGENTS.md` - read before touching anything under `dashboards/`.

Slow tier: `just lab` (disposable k3d collector-egress fidelity capture) took about 35 minutes in CI; run it once on the final candidate when that capture surface changes, not in the inner loop.
`just race` is also slow tier (about 10 minutes in CI): keep focused package checks in the inner loop and run the full race tier once on the final candidate.
