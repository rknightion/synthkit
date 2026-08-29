---
id: SKT-0030
title: Make the clean-container deployment path executable from shipped instructions
status: To Do
assignee: []
created_date: '2026-08-29 19:05'
labels: []
dependencies: []
references:
  - e2e/acceptance/2026-08-29-fresh-container-findings.md
priority: high
type: bug
ordinal: 121000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0011 scenarios A1, A2, A6, C2, H1, and H3 found that a public clean clone cannot reach the documented Compose and verification outcomes without outside knowledge. The path omits an end-to-end action, required tool names and minimum versions, numeric Grafana sink-identifier discovery, gcx/context setup, and the socket-mounted state-directory behavior; literal execution failed on missing just, unsupported just syntax, missing bash/python3/gcx, sudo absence, and state-volume permissions.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A fresh Go 1.27 container can follow one shipped path from public clone to healthy Compose without undocumented commands
- [ ] #2 The path names every required tool and minimum version, including just, bash, Python, Docker Compose, and gcx when remote verification is requested
- [ ] #3 The path explains how to obtain the required positive-decimal Prometheus, OTLP, and Loki identifiers without exposing credentials
- [ ] #4 The socket-mounted Docker path prepares state storage without sudo or an undocumented host workaround
- [ ] #5 A clean-container regression check exercises the documented commands in order
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
