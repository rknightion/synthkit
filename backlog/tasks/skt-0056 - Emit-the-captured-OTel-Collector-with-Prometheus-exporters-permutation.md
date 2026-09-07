---
id: SKT-0056
title: Emit the captured OTel Collector with Prometheus exporters permutation
status: To Do
assignee: []
created_date: '2026-09-07 11:03'
labels:
  - integration
dependencies: []
references:
  - SKT-0013
  - docs/k8s-monitoring-permutations.md
  - e2e/lab/permutations
  - signals/k8s.md
priority: medium
type: feature
ordinal: 152000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The deployment matrix explicitly says permutation 3 is not emitted even though SKT-0013 completed its capture/documentation scope. Consumers selecting the OTel Collector Prometheus-receiver path cannot safely substitute Alloy or the additive native-OTLP switch. Reuse the captured P3 evidence as the independent contract and implement the missing selectable emission path without reopening completed capture work.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 An explicit validated blueprint selection represents P3's observed Prometheus-shaped metrics and OTLP log/event surface, with a documented supported-family scope and no silent fallback to permutation 1 or 4.
- [ ] #2 Emission reproduces P3 names, labels, resource/record attributes and transports from its pinned lab/corpus evidence; families with missing evidence stay flagged and mixed/additive switch conflicts have explicit behavior.
- [ ] #3 Inventory/fidelity evidence proves positive P3 shape and absence of unintended duplicate/foreign collector lanes; the existing supported permutations keep their contracts.
- [ ] #4 Generated schema and the deployment/emission docs show P3 support with an independently deployable synthetic example and exact provenance; no live customer identifiers enter fixtures.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
