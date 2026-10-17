---
id: SKT-0149
title: >-
  Let Synthetic Monitoring cleanup run after SM is disabled or deselected, and
  package it for Kubernetes
status: To Do
assignee: []
created_date: '2026-10-17 20:59'
labels:
  - synthetic-monitoring
dependencies: []
priority: medium
ordinal: 272000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
sm-provision cleanup (SKT-0140) binds durable intent to the emitter's published private snapshot, so it refuses ('the current private snapshot is unavailable to bind durable intent') once the emitter publishes a nil snapshot: when synthetic_monitoring is listed in STARTUP_DISABLED_KINDS or SM is deselected. An emission-only operator therefore has to re-enable SM, restart and briefly resume SM emission to clean up. The Helm chart's sm-provision Job also runs apply only, so cleanup has no packaged Kubernetes route. Add a target-bound binding that does not depend on a live snapshot (smstore change), and a chart-supported cleanup invocation.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Cleanup of recorded owned resources succeeds with synthetic_monitoring startup-disabled or deselected, without re-enabling SM emission, keeping exact target binding and durable intent
- [ ] #2 A chart-supported way to run preview and confirmed cleanup exists and is documented, with a template test
- [ ] #3 Foreign, changed or unbound custody still refuses; tests cover both file and kubernetes state backends
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
