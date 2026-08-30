---
id: SKT-0034
title: Make scenario activation effects observable in emitted data
status: Done
assignee:
  - '@codex'
created_date: '2026-08-29 19:05'
updated_date: '2026-08-30 14:17'
labels: []
dependencies: []
references:
  - e2e/acceptance/2026-08-29-fresh-container-findings.md
priority: high
type: bug
ordinal: 125000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0011 scenarios E1-E3 activated all 14 schema-declared scenarios successfully, but the bounded live read did not show the declared effects, ambient intervals, or environment-scoped sibling separation. HTTP 200 control state is not sufficient evidence that a scenario changed telemetry.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Every declared scenario has a queryable emitted-data assertion tied to its documented effect
- [x] #2 Ambient incident effects appear and clear on their declared intervals
- [x] #3 An environment-scoped failure changes only the targeted environment while a sibling remains at baseline
- [x] #4 The acceptance harness activates every schema-derived scenario ID and continues after individual failures
- [x] #5 Deactivation proves active effects clear without misreading cumulative counters
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane C maps every declared scenario to a sourced emitted-data assertion and builds a continue-on-failure harness; root runs it live.

2026-08-31 Lane B: reproduce each of the seven retained scenario assertion failures, correct only proven emitter/assertion seams without lowering thresholds or clear semantics, then return an executable live matrix for root verification.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-08-30 closeout: the harness mapped and ran all 14 schema-derived scenarios, continuing after failures. Seven passed: agentcore_capacity_constraint, ai_gateway_brownout, ai_quality_regression, eks_node_cascade, eks_node_not_ready_tst1, knowledge_retrieval_meltdown, portkey_gateway_degraded. Seven failed: assist_outage, backend_latency_storm, bad_deploy_major, db_collab_saturation, eks_oom_kill_dev1, eks_pod_crashloop_dev2, frontend_vitals_degraded.

2026-08-31 verification: the complete 14-scenario run passed 12. The two corrected observation contracts then passed focused live activation and clearance at the unchanged 20 percent threshold and 130-second waits: OOM restart rate 0.1131 to 1.1083 to 0; exact-target LCP 1391.82 to 3057.54 to 1390.50. Sibling/environment isolation stayed near baseline.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Parked at AC#2, AC#3, and AC#5. Resume from the seven retained assertion failures: two active values did not rise 20%, two baselines had no series, and three cumulative values did not clear near baseline. Do not weaken the registered thresholds or cumulative-clear semantics.

2026-08-31: Corrected scenario observation and emission seams without weakening thresholds. All 14 scenarios now have executable movement, isolation, and clearance evidence: 12 in the complete run and two in focused same-contract live verification.
<!-- SECTION:FINAL_SUMMARY:END -->
