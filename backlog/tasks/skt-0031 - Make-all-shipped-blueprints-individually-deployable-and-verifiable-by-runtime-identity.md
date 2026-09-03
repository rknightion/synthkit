---
id: SKT-0031
title: >-
  Make all shipped blueprints individually deployable and verifiable by runtime
  identity
status: Parked
assignee:
  - '@codex'
created_date: '2026-08-29 19:05'
updated_date: '2026-09-03 19:37'
labels: []
dependencies: []
references:
  - e2e/acceptance/2026-08-29-fresh-container-findings.md
priority: high
type: bug
ordinal: 122000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0011 scenarios C1, C4, and C5 selected every shipped blueprint individually. Only 18 of 26 became healthy within the bounded wait; some timed out after logging that synthkit was up, filenames did not always match selectable runtime names, immediate live read-back returned no frames, and the control inventory did not expose per-blueprint identity evidence needed to compare dump families with landed data.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The canonical selectable runtime name for every shipped blueprint is discoverable without inferring it from the filename
- [x] #2 Every shipped blueprint reaches a truthful healthy or setup state individually within a documented bound, or reports the lane preventing readiness
- [x] #3 The verification path waits for two tick intervals and proves each declared signal class arrived
- [x] #4 Dump families can be compared to live queryability per blueprint without a hand-built mapping
- [x] #5 Representative substrate-scoped and blueprint-scoped families expose enough evidence to verify label separation
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane B exposes canonical runtime identity and declared signal verification; root proves all shipped blueprints live after integration.

2026-08-31 Lane C: extend the identity/readiness verifier to all 26 canonical runtime names with bounded truthful healthy/setup/lane-failure verdicts; root runs it live after integration.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-08-30 closeout: runtime identity, declared-signal verification, live query mapping, and identity projections landed and passed the final gates. Live verification covered the eight-blueprint acceptance deployment, not every one of the 26 shipped blueprints individually.

2026-08-31 verification: the canonical verifier enumerated all 28 runtime identities with bounded concurrency. Twenty completed every declared live signal assertion; eight returned explicit named lane failures: missing traces_host_info in two identities, missing gen-AI bucket in one, stale Loki success in four, and a Sigil error in one. The verifier did not hide or infer any identity.

2026-08-30, Wave A triage. NOT finalised, and the blocker is a deliberate decision rather than
missing work.

Every acceptance criterion and every Definition-of-Done item is already ticked. What keeps this
Parked is the resume boundary itself: it requires just e2e-identity to be re-run for the eight
identities that returned named lane failures, and that recipe needs a synthkit control plane on
127.0.0.1:8088 plus GC_PROM_RW / GC_PROM_USER / GC_TOKEN.

The local Docker deployment was TORN DOWN on Rob instruction the same day - "we should leave the
EKS lab running only the local one can be stopped" - so finishing this means standing the local
deployment back up, which reverses a decision he made rather than completing work he asked for.
Flagging instead of doing it.

The eight retained failures, so the resume needs no re-discovery:
  missing traces_host_info   2 identities
  missing gen-AI bucket      1
  stale Loki success         4
  Sigil error                1  -- owned by SKT-0043, which has already established the Sigil
                                   endpoint and payload contract changed under the Agent
                                   Observability rebrand. Do not debug this one here.

So seven identities are genuinely this task, and the eighth resolves when SKT-0043 lands. Worth
sequencing SKT-0043 first to avoid chasing a failure whose cause is already known.

The EKS lab is not a substitute host: it runs a fixed six-lane blueprint selection under ArgoCD,
not the 26 shipped blueprints this task enumerates.

2026-09-03 decision by Rob: stand the LOCAL Docker deployment back up for the wave, fix the retained lane failures, and tear it down at closeout. This supersedes the 2026-08-30 'leave only the EKS lab running' instruction for the duration of one wave only. No agent-declaring blueprint may be selected, so no Agent Observability traffic is produced.

The eighth failure (Sigil error) is resolved: SKT-0043 is Done and settled the Agent Observability contract. Seven remain: 2 missing traces_host_info, 1 missing gen-AI bucket, 4 stale Loki success.

Related live finding: SKT-0050 records four blueprints loaded and ticking on the EKS lab while emitting zero synthetic families. That is the same class of defect this task enumerates and is visible without a local deployment, so read SKT-0050's evidence before diagnosing the seven locally - a shared cause is plausible.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Parked at AC#2. Resume by running the executable identity/readiness check for all 26 canonical runtime names, retaining each truthful healthy/setup result or named lane failure within the documented bound.

2026-08-31: Parked after proving the verifier across all 28 canonical identities. Resume from the eight retained live lane failures and rerun just e2e-identity until each identity reaches healthy or an intentional setup state with every declared signal class landed.
<!-- SECTION:FINAL_SUMMARY:END -->
