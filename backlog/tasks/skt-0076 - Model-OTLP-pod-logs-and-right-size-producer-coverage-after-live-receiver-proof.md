---
id: SKT-0076
title: Model OTLP pod logs and right-size producer coverage after live receiver proof
status: Done
assignee:
  - '@codex'
created_date: '2026-09-11 16:34'
updated_date: '2026-09-11 17:41'
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
- [x] #1 The OTLP pod-log declaration and sole exemption expected_matches=2 land together with other exemption fields unchanged
- [x] #2 One local receiver capture reports actual OTLP metric receipts, attribution, proposal hashes and teardown evidence; promotion follows measured constant-bound capacity
- [x] #3 Final observed count equals expected_count and claim length with no stale or untriaged pairs, with zero-headroom consequence recorded
- [x] #4 Integrated gates and exact-SHA CI pass and the narrower pod-log envelope finding is recorded with its evidence boundary
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Run independent L3/L4; root R1 modelling pair, R2 gate, R3 scratch measurement and conditional promotion by equal stale swaps, R4 final bound reduction, R5 reconciliation and terminal report. Never right-size before capture disposition.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Preflight fidelity is exit 0 at observed157 expected203 claims203 stale46 untriaged0. An auxiliary export using all blueprints was the wrong recipe scope and yielded 187/203 with 30 untriaged apiserver pairs; retained diagnostically, not used for policy. Correct export follows justfile signal_fidelity_blueprints and raw comparator enumeration agrees with 157/203. L3 historical eight-key envelope origin is undetermined: old raw artifacts are absent locally and current AddLog folds by source/transport. Same-pod key changes alone do not prove an aggregation artifact; pre-fold raw resource evidence is required.

R1 paired declaration and expected_matches2 validate with exactly two underlying manifest contradictions and no untriaged gaps. R2 ordered gen-check, spdx-check and full just check exit0; ratchet observed157 expected203, claims203 stale46. Dump retains all metric, OTLP metric, log and profile inventories unchanged; one acme-datagen-analysis sampled classify_request span appears. No emission code changed. R1 CodeRabbit skipped as explicitly allowed for nonbranching one-line wiring plus JSON; documents and policy data validated instead of unit-tested. Exemption hash bf4fb9e4859f0c7be2e26d88644b3f61d4c789d39ae4c65b340891d4a2f96be9; only expected_matches changed.

R1 pair committed at 27e0288c33a3a3936fd76ccd907676251f827c28; R2 exact-SHA CI34624586338 is still running. Root read L4 final result: captured, full300s, otlp_metrics8005, otlp_logs166, metrics74, attributed32/unattributed42. Proposal55 k8s families matches the existing area membership and captured candidate names/types/transports/producers/label keys, with privacy elision only. Source receiver SHA09b71d8fb290ae36409e443fbb51cb80dc58f7fb7925e18f3a272551171018a9 matches the integrated tree. No promotion yet.

A1 root bounded-prerequisite decision: additive step7 scratch load fails duplicate area/source/substrate/permutation k8s|k3d_lab|k3s|otel-receivers before comparison. Existing README documents lab-matrix promote -merge, whose CanonicalMerge preserves established observations. Goal6 step7 clarified to use a cumulative proposal seeded from the existing document, then measure and promote its exact bytes at the same path. No retirement, raw-capture rewrite, identity change or comparator change. L4 re-dispatched only to generate the merged scratch proposal from the same capture. Confidence0.9; materiality medium because publication shape needed an explicit correction, despite using the existing authorized route. R2 CI34624586338 succeeded at27e0288c33a3a3936fd76ccd907676251f827c28, all10 jobs.

R3 scratch cumulative proposal d9a42e55da2c51053a102c845e0d7758c89bfc000e3a2c0fce639f48686885eb measured BEFORE promotion: observed189 expected203 claims203 stale46 untriaged32. All32 exact new pairs have producer otlp-native, not the promrw producer hypothesized in goal section2.5. Swap32 stale claims at unchanged203 gives observed189 claims203 stale14 untriaged0. All55 existing metric non-producer entries independently unchanged. Promotion accepted under A1; host document untouched. Right-size14 remaining stale LAST.

R3 full just check exit0. Producer coverage ratchet: observed=189 expected=203 (report-only within bound; growth fails). Cumulative corpus hash d9a42e55da2c51053a102c845e0d7758c89bfc000e3a2c0fce639f48686885eb exactly matches L4 proposal. Corpus and claims are declarative data: validated, no unit-test or CodeRabbit rerun required.

R3 CI34626745006 success at89da21b9a85ae87f8f7584b41409bc916d905592, all10 jobs. BEFORE R4 live remeasurement189/203/203 stale14 untriaged0. R4 removed exactly14 remaining stale claims; observed=expected_count=len(claims)=189, stale0 untriaged0. ZERO HEADROOM: the next new no_comparable_producer finding anywhere fails the gate; unmodelled permutation disposition does not shield counting. Future evidence work must settle existing gaps or obtain separately authorized policy decisions, never silently increase this bound.

R4 ordered just gen-check, just spdx-check, full just check all exit0. Producer coverage ratchet: observed=189 expected=189 (report-only within bound; growth fails). No corpus or emission changes after right-sizing.

R5 CI34628037101 success atef9862af2f4d02549f2b5e87f0ecac05a29a4c69, all10 jobs. All three required gate SHAs green. Conditional gen not applicable; gen-check passed. L3 historical eight-key envelope remains undetermined, not claimed solved; evidence boundary recorded above.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Done: paired modelling/exemption commit27e0288c33a3a3936fd76ccd907676251f827c28; one local300s OTLP receiver capture live-proved8005 metric items and166 log items; cumulative55-family k8s promotion89da21b9a85ae87f8f7584b41409bc916d905592 preserved all prior observations. Measured32 new gaps fit46 stale slots at203; final right-sizeef9862af2f4d02549f2b5e87f0ecac05a29a4c69 leaves observed=expected_count=claims=189, stale0 untriaged0, ZERO HEADROOM. otel-receivers remains unmodelled. Historical narrow envelope origin remains unknown; no cloud operations or emitter work.
<!-- SECTION:FINAL_SUMMARY:END -->
