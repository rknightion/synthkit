---
id: SKT-0076
title: Model OTLP pod logs and right-size producer coverage after live receiver proof
status: In Progress
assignee:
  - '@codex'
created_date: '2026-09-11 16:34'
updated_date: '2026-09-11 16:53'
labels: []
dependencies: []
priority: medium
type: task
ordinal: 172000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The accepted OTLP pod-log capture needs live comparison, while stale producer allowances conceal zero-headroom consequences. Measure one attributed local receiver capture before conditional promotion and reduce the allowance only at the final corpus tree. No collector model expansion or cloud operation is authorized.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The OTLP pod-log declaration and sole exemption expected_matches=2 land together with other exemption fields unchanged
- [ ] #2 One local receiver capture reports actual OTLP metric receipts, attribution, proposal hashes and teardown evidence; promotion follows measured constant-bound capacity
- [ ] #3 Final observed count equals expected_count and claim length with no stale or untriaged pairs, with zero-headroom consequence recorded
- [ ] #4 Integrated gates and exact-SHA CI pass and the narrower pod-log envelope finding is recorded with its evidence boundary
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Run independent L3/L4; root R1 modelling pair, R2 gate, R3 scratch measurement and conditional promotion by equal stale swaps, R4 final bound reduction, R5 reconciliation and terminal report. Never right-size before capture disposition.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Preflight fidelity is exit 0 at observed157 expected203 claims203 stale46 untriaged0. An auxiliary export using all blueprints was the wrong recipe scope and yielded 187/203 with 30 untriaged apiserver pairs; retained diagnostically, not used for policy. Correct export follows justfile signal_fidelity_blueprints and raw comparator enumeration agrees with 157/203. L3 historical eight-key envelope origin is undetermined: old raw artifacts are absent locally and current AddLog folds by source/transport. Same-pod key changes alone do not prove an aggregation artifact; pre-fold raw resource evidence is required.

R1 paired declaration and expected_matches2 validate with exactly two underlying manifest contradictions and no untriaged gaps. R2 ordered gen-check, spdx-check and full just check exit0; ratchet observed157 expected203, claims203 stale46. Dump retains all metric, OTLP metric, log and profile inventories unchanged; one acme-datagen-analysis sampled classify_request span appears. No emission code changed. R1 CodeRabbit skipped as explicitly allowed for nonbranching one-line wiring plus JSON; documents and policy data validated instead of unit-tested. Exemption hash bf4fb9e4859f0c7be2e26d88644b3f61d4c789d39ae4c65b340891d4a2f96be9; only expected_matches changed.
<!-- SECTION:NOTES:END -->
