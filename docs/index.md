---
title: synthkit
description: Get Grafana-visible synthetic telemetry from a YAML model — realistic metrics, traces, logs, and RUM without deploying an application.
---

# synthkit

synthkit gives you a Grafana-visible synthetic environment: run a bundled blueprint and see realistic metrics, traces, and logs arrive in your stack. It models the infrastructure and applications you declare; it does **not** deploy them. From that YAML **blueprint**, synthkit emits structurally-correct synthetic metrics (Prometheus Remote-Write v2), traces (OTLP), logs (Loki), and optional RUM (Faro) using the **real** technology-native metric/label/field names of each technology it models. No invented names, no placeholder shapes. Every signal is sourced from production-validated contracts in the [`signals/`](https://github.com/rknightion/synthkit/blob/main/SIGNALS.md) catalogue.

Use synthkit to build and validate dashboards and alerts against realistic data, generate demo environments, or run observability training without touching a production system.

<div class="grid cards" markdown>

-   :material-rocket-launch: **Getting started**

    ---

    Mental model, vocabulary, and concepts before you install.

    [:octicons-arrow-right-24: Getting started](getting-started.md)

-   :material-file-document-edit: **Writing blueprints**

    ---

    Declare environments, clusters, databases, workloads, and incident scenarios.

    [:octicons-arrow-right-24: Blueprints overview](blueprints.md)

-   :material-book-open-variant: **Signal catalogue**

    ---

    Every metric family, label set, and field name — sourced from real stacks.

    [:octicons-arrow-right-24: Reading the catalogue](signals.md)

-   :material-floor-plan: **Architecture**

    ---

    Three-tier design, frozen seams, invariants, and composition model.

    [:octicons-arrow-right-24: Architecture](architecture.md)

</div>

## Quickstart

The dry run needs no credentials at all — it prints the full series and label
inventory it would emit, and pushes nothing:

```bash
go build ./cmd/synthkit

DRY_RUN=true ./synthkit -once -dump
```

To push for real, create a private environment file, fill in `GC_TOKEN` and the
endpoints, and explicitly change `DRY_RUN=false` before starting:

```bash
if test -e .env; then
  printf '%s\n' '.env already exists; review it before changing live-mode settings.' >&2
else
  install -m 600 .env.example .env
  ./plugins/synthkit/skills/initial-setup/scripts/set-env.sh DRY_RUN false .env
fi
# Edit .env and fill in the required Grafana Cloud values without printing them.
./synthkit
```

For an existing `.env`, review its credentials first, then run the same `set-env.sh DRY_RUN false
.env` command when you are ready to opt into live delivery.

From another terminal, confirm the status endpoint reports `dry_run=false`:

The `-u control` form prompts for `CONTROL_TOKEN` without echoing it; press Enter only for an
intentionally token-free loopback run.

```bash
curl -fsS -u control http://localhost:8088/control/status | jq -e '.dry_run == false'
```

The command prints `true`; it exits nonzero if live mode is not active. `jq` is optional: without
it, inspect the same JSON with `curl -fsS -u control http://localhost:8088/control/status` and look for
`"dry_run":false`.

## What synthkit emits

| Signal type | Protocol | Destination |
|---|---|---|
| Metrics | Prometheus Remote-Write v2 | Grafana Cloud Mimir |
| Traces | OTLP (hand-encoded ResourceSpans) | Grafana Cloud Tempo |
| Logs | Loki push | Grafana Cloud Loki |
| RUM (optional) | Faro collector | Grafana Cloud Frontend Observability |
| Profiles (optional) | Pyroscope | Grafana Cloud Profiles |

Each signal type uses its own credential triplet. A single Cloud Access Policy token with `metrics:write`, `logs:write`, `traces:write`, and `profiles:write` scopes covers all four synthetic sinks — see [Credentials](credentials.md).

## The blueprint model

A **blueprint** is a single YAML file that wires together construct and workload instances with config. Constructs are isolated modules — each emits the real signal names of one technology (EKS, RDS, Cloudflare, Fleet Management, and so on). Workloads generate correlated request traffic: `web_service` models a single service with a browser→backend→DB hop tree; `app` models a multi-service graph whose nodes emit custom telemetry via a DSL. They model traffic and telemetry only; synthkit does not deploy a real app. Constructs know nothing about blueprints or each other; deleting a blueprint file removes its telemetry and affects nothing else.

`DRY_RUN` defaults to `true` — live pushing is always an explicit opt-in.

## Principles

- **No invented names.** Every metric, label, and field name is sourced from the `signals/` catalogue, which is lifted from production-validated stacks with provenance citations.
- **Isolated constructs.** Constructs and workloads never import each other, the blueprint package, or any OTel SDK. Zero coupling flows backward.
- **One blueprint = one file.** Config lives in the blueprint; constructs are unconditional. Add a scenario, remove an environment, or delete the whole blueprint — nothing else changes.
- **Deterministic.** The same blueprint produces the same identities (pod names, node IDs, instance keys) on every run. Fixtures are seeded from the blueprint name and path, not from process time.
- **Cumulative correctness.** Counters and histograms accumulate across ticks; the sink receives running totals, not deltas, matching how real exporters work.

## License

synthkit is licensed under the [GNU Affero General Public License v3.0 only (`AGPL-3.0-only`)](https://github.com/rknightion/synthkit/blob/main/LICENSE). Every Go source file carries an `SPDX-License-Identifier: AGPL-3.0-only` header.
