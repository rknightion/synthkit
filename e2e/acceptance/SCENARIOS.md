# synthkit user-acceptance scenario catalogue

Tracked as **SKT-0011**. This is the round an agent runs, from scratch, on a machine and a Grafana
Cloud stack with no prior synthkit state.

The reason it exists: every other verification in this repository runs on a machine that has been
carrying synthkit state for months. The one path never tested is the path a new user actually takes,
and a fresh stack has no pre-existing dashboards, datasources, recording rules or folder structure.
**Anything synthkit needs and does not create is a defect the established lab environment hides**, and
it is the defect class most likely to reach a new user first.

## How to run this

Every scenario is `precondition → action → assertion → verdict`. The assertion is an **observable fact
about the live stack**, never a local exit code — a clean `docker compose up` proves the process
started, not that a single series arrived. Where a scenario can only be checked locally, it says so.

Record one row per scenario in a findings file: scenario id, verdict (`pass` / `fail` / `blocked`),
and for a failure, **what a new user would have seen**. That last column is the deliverable; a bare
`fail` is not actionable.

Read-back against the stack uses `gcx` with the operator-selected context. **Never write a stack,
account or tenant identifier into any committed file** — this repository is public and `make hygiene`
carries a forbidden-words guard that fails CI on every subsequent push until the term is removed.

Findings become tracked work. Do not fix defects inline during the round: a round that repairs as it
goes cannot tell you how bad the fresh-start experience actually is.

---

## A — First deployment from nothing

The whole point of this group is that the agent follows the shipped instructions *literally* and
records every place it had to improvise. An undocumented step that an experienced operator supplies
from memory is exactly the defect being hunted.

| id | scenario | assertion |
|---|---|---|
| A1 | Clone the public repository to a machine with no synthkit history and follow `docs/getting-started.md` end to end | The documented steps alone reach a running process. Every improvised step is recorded as a defect |
| A2 | Follow the `initial-setup` skill instead of the docs | Reaches the same running state; where skill and docs disagree, both are recorded |
| A3 | Configure `.env` from `.env.example` with real credentials for the required lanes only | The process starts and reports ready without the optional lanes configured |
| A4 | Start with no blueprints selected, per SKT-0005.19 | The process starts, reports ready, and emits nothing. Confirm no series arrive |
| A5 | Deliberately omit a mandatory credential | Startup fails with a message naming the missing variable, before any partial emission. Per SKT-0005.17 |
| A6 | Run the documented Docker Compose path rather than `go run` | Same outcome as A1, and `SYNTHKIT_BIND` behaves as documented for host exposure |

## B — Credential and lane coverage

Every lane that silently disables itself when unconfigured is a lane whose failure mode a new user
will misread as "synthkit is broken". Each scenario here configures the lane for real and confirms
data arrives at the destination.

| id | lane | assertion |
|---|---|---|
| B1 | Prometheus Remote-Write (`GC_PROM_RW`) | Series queryable on the stack with the expected `job`/`blueprint` identity |
| B2 | OTLP metrics and traces (`GC_OTLP_ENDPOINT`) | Traces searchable in Tempo; native OTLP metric families present |
| B3 | Loki logs (`GC_LOKI`) | Streams queryable with the expected stream labels and structured metadata |
| B4 | Pyroscope profiles (`GC_PYROSCOPE_URL`) | Profiles visible per language and profile type |
| B5 | Faro / RUM (`GC_FARO_COLLECTOR`) | Sessions and page views arrive. Note that RUM disables itself with only a log line when unconfigured — confirm that log line is discoverable |
| B6 | Synthetic Monitoring (`GC_SM_URL`) | Checks registered and reporting; `sm-provision` behaves as documented |
| B7 | Fleet Management (`GC_FM_URL`) | Collectors registered and visible in the Fleet Management UI |
| B8 | sigil AI observability (`GC_SIGIL_ENDPOINT`) | Generations, workflow steps and scores arrive |
| B9 | Self-observability (`SELFOBS_ENABLED`, separate credential triplet) | synthkit's own process telemetry reaches the **self-obs** stack and never the data stack |
| B10 | Every lane configured at once | No lane starves another; the delivery queue reports no sustained loss |

## C — Blueprint coverage

All 26 shipped blueprints. A blueprint that ships and does not emit what it declares is a
false claim, and this is the group that catches it.

| id | scenario | assertion |
|---|---|---|
| C1 | Deploy each blueprint individually | For each: every signal class it declares produces data on the stack within two tick intervals |
| C2 | Run `verify-deployment` against each | The skill's own checks pass against a stack it has never seen — this is the first time that skill is exercised without pre-existing state |
| C3 | Deploy the full catalogue at once (`BLUEPRINT_NAMES='*'`) | Process stays within memory and the series cap; no blueprint silently drops out |
| C4 | Compare `-once -dump` structural inventory against what actually landed | Every family in the dump is queryable on the stack. A family in the dump with no series on the stack is a delivery defect |
| C5 | Confirm identity separation | Substrate-scoped constructs carry no `blueprint` label; blueprint-scoped ones all do |

## D — Control plane and admin UI

| id | scenario | assertion |
|---|---|---|
| D1 | `/control/health`, `/control/readiness`, `/control/status` before and during emission | Readiness reflects real deployment state, not merely "process alive". Per SKT-0005.10 |
| D2 | Blueprint enable / disable / pending / staged through the API | State transitions are visible and take effect on the next tick |
| D3 | Custom blueprint upload, valid and invalid | A valid one applies; an invalid one is rejected with a diagnostic naming the problem. Per SKT-0005.08 |
| D4 | Git blueprint source add and poll | Namespaced blueprints appear; collisions skip with a diagnostic. Per SKT-0005.09 |
| D5 | Admin UI at `/control/ui` — every view | Each view renders and reflects live state. Note the a11y descope is deliberate; do not report it |
| D6 | `/control/reset` and `/control/scaling` | Both take effect observably in the emitted data |
| D7 | Control-plane exposure without `CONTROL_EXPOSURE_ACK` | Refuses to expose beyond loopback. Per SKT-0005.12 |
| D8 | `/control/diagnostics` and `/control/inventory` | Content is sufficient to debug a failing deployment without shell access to the container |

## E — Failure modes and scenarios

| id | scenario | assertion |
|---|---|---|
| E1 | Activate each scenario via `/control/scenarios/activate` | The declared effect appears in the emitted data, not merely in the control-plane state |
| E2 | Ambient incidents on their declared intervals | Effects appear and clear on schedule |
| E3 | Scoped failure modes with an environment target | The scope actually restricts the blast radius. Constructs scoping by account or region rather than environment are a known trap — confirm which apply |
| E4 | Deactivate | Effects clear fully; no residue in cumulative counters |

## F — Upgrade, rollback and reproducibility

| id | scenario | assertion |
|---|---|---|
| F1 | Deploy a published image by digest via committed Compose | Reported version and revision match the digest. Per SKT-0005.14 |
| F2 | Upgrade to a newer digest | Cumulative counters behave correctly across the restart; no counter resets that a dashboard would read as a spike |
| F3 | Roll back to the previous digest | Returns to the prior state cleanly |
| F4 | Restart with a config snapshot present | Selected blueprints and control state survive the restart |

## G — Dashboards against real synthetic data

This is where "the data is structurally correct" gets tested against "a dashboard built for it
actually renders". A panel that renders empty is the defect a user sees first.

| id | scenario | assertion |
|---|---|---|
| G1 | Generate dashboards and push them to the fresh stack | Every panel is enumerated with a rendered / empty verdict |
| G2 | For each empty panel, establish the cause | Emission gap, query defect, or missing datasource. Each is recorded distinctly — they route to different fixes |
| G3 | Datasource resolution on a fresh stack | Panels resolve their datasource without hand-editing. Multiple same-type datasources are a known trap |
| G4 | Native histogram panels | Latency and service-graph panels query native series and render |
| G5 | Anything synthkit needs on a stack but does not create | Enumerated explicitly — recording rules, folders, datasources. This is the group's most valuable output |

## H — Documentation truth

| id | scenario | assertion |
|---|---|---|
| H1 | Every command in `docs/` executed as written | Each runs, or is recorded as wrong |
| H2 | Every `docs/troubleshooting.md` symptom reproduced | The documented remedy resolves it |
| H3 | `docs/RUNBOOK.md` followed by an agent with no prior context | Reaches the stated outcome unaided |
| H4 | The marketplace / plugin installation path | Installs and relocates as documented. Per SKT-0005.05 |

---

## Deliberately out of scope

- Accessibility of the admin UI. Descoped by explicit direction; do not report it as a defect.
- Anything requiring a write to the operator's Kubernetes clusters — that is SKT-0012 and it needs
  the cluster owner's decision, not a QA round's.
- Fixing what the round finds. File it; do not repair it inline.
