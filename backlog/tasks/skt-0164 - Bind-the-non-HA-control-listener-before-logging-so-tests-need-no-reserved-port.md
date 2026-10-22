---
id: SKT-0164
title: Bind the non-HA control listener before logging so tests need no reserved port
status: To Do
assignee: []
created_date: '2026-10-22T04:24:00Z'
labels:
  - test
dependencies: []
priority: medium
ordinal: 307000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0161 (loop52) converted 8 of 9 reserve-and-close sites; declarative_sources_test.go:40 runs the shipped binary whose non-HA path (cmd/synthkit/main.go:903) has no WrapHTTPListener and logs only the configured address. Bind first and log the bound address so a :0 config is observable, then convert the test.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The non-HA binary started with a :0 control address logs its bound address
- [ ] #2 declarative_sources_test.go uses it; a foreign listener on the old port is red at base and green after
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
