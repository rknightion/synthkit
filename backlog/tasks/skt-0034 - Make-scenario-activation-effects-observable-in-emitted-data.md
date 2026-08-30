---
id: SKT-0034
title: Make scenario activation effects observable in emitted data
status: Parked
assignee:
  - '@codex'
created_date: '2026-08-29 19:05'
updated_date: '2026-08-30 01:39'
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
- [ ] #2 Ambient incident effects appear and clear on their declared intervals
- [ ] #3 An environment-scoped failure changes only the targeted environment while a sibling remains at baseline
- [x] #4 The acceptance harness activates every schema-derived scenario ID and continues after individual failures
- [ ] #5 Deactivation proves active effects clear without misreading cumulative counters
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
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-08-30 closeout: the harness mapped and ran all 14 schema-derived scenarios, continuing after failures. Seven passed: agentcore_capacity_constraint, ai_gateway_brownout, ai_quality_regression, eks_node_cascade, eks_node_not_ready_tst1, knowledge_retrieval_meltdown, portkey_gateway_degraded. Seven failed: assist_outage, backend_latency_storm, bad_deploy_major, db_collab_saturation, eks_oom_kill_dev1, eks_pod_crashloop_dev2, frontend_vitals_degraded.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Parked at AC#2, AC#3, and AC#5. Resume from the seven retained assertion failures: two active values did not rise 20%, two baselines had no series, and three cumulative values did not clear near baseline. Do not weaken the registered thresholds or cumulative-clear semantics.
<!-- SECTION:FINAL_SUMMARY:END -->
