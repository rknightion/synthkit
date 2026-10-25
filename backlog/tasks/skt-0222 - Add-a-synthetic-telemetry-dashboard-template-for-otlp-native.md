---
id: SKT-0222
title: Add a synthetic-telemetry dashboard template for otlp-native
status: To Do
assignee: []
created_date: '2026-10-25T20:22:00Z'
labels: []
dependencies: []
references:
  - >-
    https://github.com/rknightion/synthkit/blob/d4a51b34ebb839895c435b46dd14df9c350db7c1/blueprints/otlp-native.yaml
  - >-
    https://github.com/rknightion/synthkit/blob/d4a51b34ebb839895c435b46dd14df9c350db7c1/cmd/synthkit-dash/catalog.go
  - >-
    https://github.com/rknightion/synthkit/blob/d4a51b34ebb839895c435b46dd14df9c350db7c1/cmd/synthkit-dash/namespace.go
  - >-
    https://github.com/rknightion/synthkit/blob/d4a51b34ebb839895c435b46dd14df9c350db7c1/docs/tools.md
priority: medium
type: feature
ordinal: 428000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The bundled otlp-native blueprint emits synthetic telemetry but has no corresponding synthkit-dash template. Users need a supported generated dashboard for that blueprint rather than a copied template or the process operational dashboard. Reuse existing namespace, datasource and inventory contracts. This task adds no operational telemetry dashboards, control actions, provisioning, alert delivery or automatic publication.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The documented synthkit-dash CLI accepts otlp-native and produces valid folder/dashboard resources plus matching panel inventory through its supported output contract
- [ ] #2 Panels and variables use only metric names, labels and signal semantics verified against the bundled blueprint and canonical contracts; histogram queries preserve the actual receiver-projected shape and units without assuming native Prometheus histograms solely from OTLP input; unsupported signals are explicit rather than fabricated
- [ ] #3 Actual CLI tests cover default output, explicit datasource bindings, deterministic repetition and two namespaces; identities, internal references and panel inventory agree without collisions
- [ ] #4 A bounded fixture through the public generation/verification path proves every supported panel query against matching emitted telemetry and distinguishes empty emission, query failure and missing datasource; missing data never implies delivery or healthy zero
- [ ] #5 User documentation lists template and signal/panel coverage, generation/verification commands and limitations; artifact generation requires no tenant credential or private configuration
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
