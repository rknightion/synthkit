---
id: SKT-0016
title: CI e2e intermittently fails on a state-volume permission probe
status: To Do
assignee: []
created_date: '2026-08-28 09:03'
updated_date: '2026-08-28 11:55'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 90000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
`TestDockerE2E` in the `e2e` CI job fails intermittently on main with:

    control: startup state-volume probe failed: open /app/.control-state-3183864943.tmp: permission denied
    control: startup state-volume probe failed: open ./.control-state-4201682616.tmp: permission denied
    --- FAIL: TestDockerE2E (128.01s)

Observed run history on main, newest first: `c2c5ae0f` FAIL, `4fa184e6` FAIL, then five consecutive commits (`c7ee70cc`, `1f3b433e`, `63c826b2`, `e534c582`, `c7059771`) where `e2e` PASSED and only `hygiene` was red, then `87a2d6d6` FAIL. That first failure is the commit `chore(deps): update golang:1.27.0 docker digest to 0ecdc2a (#103)`, which is the obvious suspect, but the five green runs after it mean a base-image change alone does not explain the pattern.

It is intermittent, so a single green run does not close this. Reproduce it locally with `make e2e` before proposing a cause, and reproduce it more than once.

Two things the symptom itself tells you. The probe writes a temp file next to the control-state path to check the volume is writable at startup; the two failures name different working directories (`/app` and `./`), so the probe is resolving its target relative to whatever the process CWD is at that moment rather than to a fixed path. And the failure is `permission denied`, not `no such file or directory`, so the directory exists and the container user cannot write to it — a uid/ownership question about the image layer, not a missing mount.

Do not paper over it by relaxing the probe. The probe exists because a read-only or wrongly-owned state volume is a real deployment failure that used to surface as silent state loss, and SKT-0005.10 added it deliberately. If the probe is right and the image is wrong, fix the image.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The failure is reproduced locally more than once, and the cause is identified rather than inferred from the commit that first showed it
- [ ] #2 Whether the probe resolves its target relative to CWD is established, and if so it is made deterministic
- [ ] #3 The container user's ownership of the state path is stated as observed fact for the image on main
- [ ] #4 The startup writability probe still fails a genuinely unwritable state volume after the fix
- [ ] #5 e2e passes on five consecutive main commits after the fix, since one green run does not close an intermittent failure
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-08-28 wave covering check: exact-SHA CI run 33168159591 at 6e74d7ee62d8c3a02209de5c4bb093b069843440 failed only in e2e job 98839548298. TestDockerE2E logged control persist and startup state-volume probe failures for both ./.control-state-3038999027.tmp and /app/.control-state-3794945131.tmp with permission denied; the remaining exact-SHA jobs passed. This matches the pre-existing intermittent state-volume permission failure and is outside the OTLP/high-DPM wave. Resume at AC #1: run make e2e locally more than once until the failure is reproduced, then inspect the failing container image runtime UID/GID, working directory, configured control-state path, mount target, and directory ownership before changing code. Do not weaken the startup writability probe.
<!-- SECTION:NOTES:END -->
