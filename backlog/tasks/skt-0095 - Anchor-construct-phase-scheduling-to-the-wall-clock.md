---
id: SKT-0095
title: Anchor construct phase scheduling to the wall clock
status: To Do
assignee: []
created_date: '2026-10-07 20:31'
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
- [ ] #1 Next-due times are a pure function of wall clock, name and interval
- [ ] #2 A restart mid-interval resumes on the same cadence (test with a fake clock)
- [ ] #3 just dump inventory unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
