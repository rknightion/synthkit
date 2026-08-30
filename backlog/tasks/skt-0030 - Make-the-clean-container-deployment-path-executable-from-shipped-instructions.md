---
id: SKT-0030
title: Make the clean-container deployment path executable from shipped instructions
status: Done
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
ordinal: 121000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0011 scenarios A1, A2, A6, C2, H1, and H3 found that a public clean clone cannot reach the documented Compose and verification outcomes without outside knowledge. The path omits an end-to-end action, required tool names and minimum versions, numeric Grafana sink-identifier discovery, gcx/context setup, and the socket-mounted state-directory behavior; literal execution failed on missing just, unsupported just syntax, missing bash/python3/gcx, sudo absence, and state-volume permissions.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A fresh Go 1.27 container can follow one shipped path from public clone to healthy Compose without undocumented commands
- [x] #2 The path names every required tool and minimum version, including just, bash, Python, Docker Compose, and gcx when remote verification is requested
- [x] #3 The path explains how to obtain the required positive-decimal Prometheus, OTLP, and Loki identifiers without exposing credentials
- [x] #4 The socket-mounted Docker path prepares state storage without sudo or an undocumented host workaround
- [x] #5 A clean-container regression check exercises the documented commands in order
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane A owns the clean-container mechanism and shipped setup documentation; root applies task-surface wiring and runs Docker evidence after all lanes return.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-08-30 closeout: just clean-container completed from a public clone with Compose v5.4.0; final just check, just dump, just e2e, and exact rc.38 published-e2e all exited 0.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Delivered and executed the documented public-clone-to-healthy-Compose route, including tool discovery, identifier guidance, rootless state preparation, and the clean-container regression.
<!-- SECTION:FINAL_SUMMARY:END -->
