---
title: synthkit
description: Composable synthetic-telemetry generator for Grafana Cloud — declare infrastructure and applications in YAML, receive structurally-correct metrics, traces, logs, and RUM.
---

# synthkit

synthkit is a composable synthetic-telemetry generator for Grafana Cloud. Declare the infrastructure and applications you want in one YAML **blueprint** and synthkit emits structurally-correct synthetic metrics (Prometheus Remote-Write v2), traces (OTLP), logs (Loki), and optional RUM (Faro) — using the **real** technology-native metric/label/field names of each technology it models. No invented names, no placeholder shapes. Every signal is sourced from production-validated contracts in the [`signals/`](https://github.com/rknightion/synthkit/blob/main/SIGNALS.md) catalogue.

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

A **blueprint** is a single YAML file that wires together construct and workload instances with config. Constructs are isolated modules — each emits the real signal names of one technology (EKS, RDS, Cloudflare, Fleet Management, and so on). Workloads generate correlated request traffic: `web_service` models a single service with a browser→backend→DB hop tree; `app` models a multi-service graph whose nodes emit custom telemetry via a DSL. Constructs know nothing about blueprints or each other; deleting a blueprint file removes its telemetry and affects nothing else.

`DRY_RUN` defaults to `true` — live pushing is always an explicit opt-in.

## Principles

- **No invented names.** Every metric, label, and field name is sourced from the `signals/` catalogue, which is lifted from production-validated stacks with provenance citations.
- **Isolated constructs.** Constructs and workloads never import each other, the blueprint package, or any OTel SDK. Zero coupling flows backward.
- **One blueprint = one file.** Config lives in the blueprint; constructs are unconditional. Add a scenario, remove an environment, or delete the whole blueprint — nothing else changes.
- **Deterministic.** The same blueprint produces the same identities (pod names, node IDs, instance keys) on every run. Fixtures are seeded from the blueprint name and path, not from process time.
- **Cumulative correctness.** Counters and histograms accumulate across ticks; the sink receives running totals, not deltas, matching how real exporters work.

## License

synthkit is licensed under the [GNU Affero General Public License v3.0 only (`AGPL-3.0-only`)](https://github.com/rknightion/synthkit/blob/main/LICENSE). Every Go source file carries an `SPDX-License-Identifier: AGPL-3.0-only` header.
