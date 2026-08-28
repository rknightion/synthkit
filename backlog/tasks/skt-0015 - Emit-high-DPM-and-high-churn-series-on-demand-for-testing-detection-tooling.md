---
id: SKT-0015
title: 'Emit high-DPM and high-churn series on demand, for testing detection tooling'
status: To Do
assignee: []
created_date: '2026-08-28 08:56'
labels: []
dependencies: []
priority: high
type: feature
ordinal: 86000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
synthkit clamps every metric lane to a 60-second interval. `runner.Options.MinMetricInterval` defaults to 60s in `internal/runner/runner.go:97`, is never set from config, and `clampInterval` (`internal/runner/runner.go:499`) raises any shorter declared interval back to it with a log line. The clamp is deliberate — it is invariant I10, it is what keeps a synthetic estate from silently costing what a real one does, and ARCHITECTURE.md, docs/architecture.md, docs/RUNBOOK.md, internal/core/core.go and several construct files all state 60s as the floor.

That makes synthkit unable to produce the one thing a Grafana Cloud high-DPM detector needs to see: a series arriving more than once a minute. Grafana Cloud bills and alerts on data points per minute, and the tooling that flags a high-DPM tenant, and the Adaptive Metrics recommendations that follow from it, cannot be exercised against an estate that is floored at 1 DPM per series by construction. Today the only way to test those tools is to point them at a real over-scraped cluster.

The ask is a deliberate, opt-in override: a blueprint may declare that its metric lanes run faster than the floor, and synthkit honours that down to a configurable ceiling. Two knobs matter, and they drive different detectors:

- **DPM per series** — few series sampled frequently. This is what high-DPM detection actually flags.
- **Series churn** — the active series set turning over, so 'new series per minute' detectors and Adaptive Metrics recommendations have something to chew on.

Cardinality alone (many series at a normal cadence) is explicitly NOT the target of this work; synthkit can already produce that by declaring a large estate.

**Decisions taken at scoping, do not re-litigate:**

1. **Speed up the existing catalog, do not add a stress construct.** A real 15-second scrape looks exactly like a real construct running at 15 seconds. A synthetic `dpm_stress` construct emitting invented series would produce volume with no shape, and would put invented metric names into the catalogue, which the no-invent rule forbids.
2. **The override is a blueprint field, not an environment variable.** It follows the architecture contract: blueprints own blueprint-specific configuration and explicit wiring; the composition root reads it. One blueprint burning DPM must not lift the floor for every other blueprint in the same process.
3. **The ceiling is configurable and defaults to 6 DPM per series**, i.e. a 10-second minimum interval. That is a real scrape cadence, not a stress number. An operator who genuinely wants to melt a test stack raises the ceiling deliberately.

**The trap this work must not walk into.** `blueprintLoop` resets the per-blueprint series budget on a ticker driven by `MinMetricInterval` (`internal/runner/runner.go:698`). `res.SeriesBudget` is therefore a budget *per DPM-floor window*, not per minute. Lowering a blueprint's floor to 10s resets its budget six times more often and silently multiplies its effective per-minute series allowance by six. Whatever this work does to the floor, the budget window must stay explicit and its meaning must not change by accident.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A blueprint can opt in to sub-60s metric cadence, and blueprints that do not opt in are unaffected in the same process
- [ ] #2 The maximum DPM per series is bounded by a configurable ceiling that defaults to 6 DPM (a 10-second interval)
- [ ] #3 Series churn is declarable, so the active series set turns over at a chosen rate
- [ ] #4 The per-blueprint series budget window is not silently rescaled by a lowered floor
- [ ] #5 A reference blueprint exists that a user can run to exercise high-DPM detection, and its cost is stated where they will see it before running it
- [ ] #6 Every place that documents the 60s floor as absolute is corrected to describe the opt-in override
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->
