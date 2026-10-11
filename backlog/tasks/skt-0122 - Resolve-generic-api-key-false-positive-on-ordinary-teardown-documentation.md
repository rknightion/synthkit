---
id: SKT-0122
title: Resolve generic-api-key false positive on ordinary teardown documentation
status: In Progress
assignee:
  - '@loop-root'
created_date: '2026-10-11 09:56'
updated_date: '2026-10-11 14:16'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 244000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Exact main CI38128447237 atf493ff84292c117f01c16f1d8bb110af8a61ab66 failed secret-scan with one generic-api-key finding. Matching Gitleaks8.30.1 scan restricted to that main ancestry reproduces exactly docs/fleet-management.md line46, a natural-language teardown sentence rather than a credential. Current-source edits cannot remove this historical finding. A default scan of all local refs additionally sees an unpublished review snapshot; that is not public main history. No secret was identified and no matched value is retained here. Existing detector/canary/baseline and history protections must not be weakened; publication is blocked until the scanner proof contract is resolved.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Classify the exact main-ancestry finding using redacted output and preserve full credential/canary detection coverage; no file blanket exclusion, historic-baseline waiver or history rewrite.
- [ ] #2 Owner approves any necessary scanner-contract regrade before implementation; a false-positive correction must also be seen reject representative real-secret canaries.
- [ ] #3 Exact main secret-scan and ci-success succeed with unchanged full-history scope after the approved resolution.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Precise rule-level prose false-positive correction with fail-first full-history redacted scan and preserved plus expanded representative canaries; guarded review before serial landing and exact CI.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
One read-only diagnosis, zero implementation attempts. Exact main-only fully redacted scan reproduces one historical ordinary-prose finding at the landed runtime SHA; no secret values printed or identified. Parkowner approval of a precise scanner-contract correction with unchanged full-history scope and intact credential/canary coverage. This task owns present main CI red; no unchanged rerun or new allowlist attempted.
<!-- SECTION:NOTES:END -->
