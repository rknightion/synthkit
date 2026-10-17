---
id: SKT-0137
title: Expose an unambiguous leadership-session identity on operational telemetry
status: Done
assignee: []
created_date: '2026-10-16 13:53'
updated_date: '2026-10-17 23:26'
labels: []
dependencies: []
references:
  - >-
    https://github.com/rknightion/synthkit/blob/9f04a7d0b1cfebaccf50a8d40063e547967d99a4/cmd/synthkit/ha_main.go
priority: high
type: feature
ordinal: 260000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Operational resources identify a process by hostname and PID plus HA role, while Kubernetes Lease holder identity includes a fresh random election nonce that is not exported. A container restart can reuse hostname/PID while buffered observations from an older process arrive. External rollout or failure observers therefore cannot prove a push outcome belongs to the current lease session from role or Pod identity alone.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A documented non-secret operational identity is bound to the actual leadership session and is independently joinable to the current Lease holder; it changes across restart or reacquisition without user, credential or hardware identifiers
- [x] #2 Actual self-observability export and HA process-boundary tests distinguish current, stale buffered, standby and restarted-session observations; role/provider rotation and cumulative writer identity remain correct
- [x] #3 Unsupported or missing attribution remains explicitly unknown, with a stable bounded contract and documentation rather than inferred hostname/PID or role-only membership
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Landed c0c9712 (loop50 BATCH4). Main CI green on 1d627bf (rc v1.6.0-rc.202).
<!-- SECTION:NOTES:END -->
