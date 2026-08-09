---
title: Why This Generator
description: A factual, dated account of how synthkit differs from telemetrygen, avalanche, a load-testing tool, or a script that writes fake series — and when to pick each.
---

# Why This Generator

*Last reviewed: 2026-08.*

Generating synthetic telemetry is not a new problem and there are good tools for it already.
This page states what synthkit does differently and why, so you can decide which one fits the
job in front of you. It describes this codebase; it makes no claims about how anything else is
implemented.

## What already exists

**`telemetrygen` and similar OTLP load generators.** Built to answer "can my pipeline take
this throughput". They emit spans and metrics at a rate you choose, quickly, with almost
nothing to configure. If you are load-testing a collector, this is the right tool and synthkit
is the wrong one.

**`avalanche` and metric-load generators.** Same shape for Prometheus: lots of series, tunable
churn, aimed at the storage and ingest path.

**A script that writes fake series.** For one panel, this is often the whole job — a loop, a
few sine waves, done.

**A load-testing tool against a real app.** If you have a system to point at, driving it
produces genuinely real telemetry, and no generator beats that for fidelity.

## Design choices specific to this generator

**Real names, not invented ones.** Every metric family, label key, span attribute, log stream
label and RUM field comes from the [`signals/` catalogue](signals.md), which is
provenance-cited to vendor documentation, live empirical capture, or a predecessor codebase.
The catalogue is authoritative *over the Go code*: if the two diverge, the code is the thing
that is wrong. Signals referenced in code but not yet verified are tracked openly rather than
quietly shipped.

That is the whole point of the tool. A dashboard built against `test_metric_1` proves the
dashboard renders; a dashboard built against the real technology-native names proves it will
work on the day you point it at production. A load generator has no reason to care about
this, and does not.

**Declarative estates, not streams.** A YAML blueprint declares environments, clusters,
databases and workloads, and the emitted metrics, traces, logs and RUM are structurally
consistent *with each other* — the same service names, the same instances, the same
topology across all four signal types. Correlation is what you are usually trying to test,
and it is exactly what independently generated streams cannot give you.

**Failure modes that brown out, rather than errors that appear.** The
[incident model](incidents.md) separates what can happen from when it happens, and an active
failure mode shifts the shape engine's multipliers: latency histograms right-tail, error
counters climb, restarts increase, connection gauges approach max — while the rest of the
estate stays healthy. That is what an alert rule has to fire on in practice, and a naive
error injection does not produce it.

**Four signal types from one declaration.** Prometheus Remote-Write v2, OTLP traces, Loki
logs and optional Faro RUM, from the same blueprint.

## When to pick something else

**You are load-testing a pipeline.** Use `telemetrygen` or `avalanche`. Throughput is their
job; synthkit is optimising for correctness of shape, not for volume per CPU.

**You have a real system to point at.** Drive it. Real telemetry beats accurate synthetic
telemetry, always.

**You need one fake series for one panel.** A script is less to run and less to understand.

**You need a technology synthkit has no signals for.** The catalogue is the ceiling — it is
meant to grow, and adding an area is a documented path, but the tool will not invent names it
cannot cite.

## See also

- [Reading the Catalogue](signals.md) — how signals are sourced and cited
- [Signal Areas](signal-areas.md) — what is modelled today
- [Blueprints overview](blueprints.md) — declaring an estate
- [Incidents & Scenarios](incidents.md) — failure modes and activation
- [Architecture](architecture.md) — how blueprints become signals
