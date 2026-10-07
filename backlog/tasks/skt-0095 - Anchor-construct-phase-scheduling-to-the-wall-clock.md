---
id: SKT-0095
title: Anchor construct phase scheduling to the wall clock
status: Done
assignee:
  - '@loop-root'
created_date: '2026-10-07 20:31'
updated_date: '2026-10-07 22:54'
labels:
  - feature
  - ha
dependencies: []
ordinal: 217000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
seedPhases (internal/runner/runner.go) sets each construct's first due time to now + phaseOffset(name, interval), so phases are relative to process start. After any restart or HA handoff every series' cadence restarts, which can drop up to about two scheduled samples per series at 60s intervals regardless of how fast the takeover is. Anchor to the epoch: next due = first t >= now with (t - phase) mod interval == 0. Two processes running the same blueprint then share one schedule. RunOnce / -dump unaffected.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Next-due times are a pure function of wall clock, name and interval
- [x] #2 A restart mid-interval resumes on the same cadence (test with a fake clock)
- [x] #3 just dump inventory unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Isolated candidate with frozen ownership; acceptance reproduction, focused checks and just check; independent review before root landing. HA seam design is independently challenged and frozen first.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop1: implementation attempts 1; ownership repair was read-only, later gate rerun had a new actual base. Accepted and landed f3d701a1a4500eb2944a0a354c53a9c641fed371; composed just check exit0 at that exact SHA. CI run37696196429 all required jobs/ci-success green. Original fake-clock regression red retained, seven focused tests green; same-second actual-base/candidate complete dump parity 542277 bytes, no filtering/overlay. Real clock affects model-span coverage; one final coordinated pair, earlier separate attempts disclosed. No blueprint field/config/skill changed, generation not applicable; inventory parity and composed signal-fidelity validation cover catalogue. Routine aggregate review remains scheduled after last routine land.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Epoch-anchored startup and post-late-tick scheduling; fake-clock restart reproduced before fix and passes. Full dump proof, independent review, exact-SHA composed gate and CI passed. Evidence /tmp/skh-U95-proof/acceptance-a8610b9.json and /tmp/U95-composed-f3d701a/gate.log. Release v1.6.0-rc.134 targets this SHA.
<!-- SECTION:FINAL_SUMMARY:END -->
