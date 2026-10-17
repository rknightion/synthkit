---
id: SKT-0140
title: >-
  Add ownership-bound preview and explicit cleanup for provisioned Synthetic
  Monitoring resources
status: To Do
assignee: []
created_date: '2026-10-16 13:53'
labels: []
dependencies:
  - SKT-0099
priority: high
type: feature
ordinal: 263000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The provisioner records ownership and uncertain mutations for preview/apply but deliberately never deletes resources. Removing a deployment therefore lacks a supported ownership-bound cleanup and remaining-custody receipt. A generic explicit cleanup contract must preserve target binding, authoritative resource identities and concurrent user changes instead of deleting by name, spec or folder membership.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Preview and separately explicit cleanup operate only on exact recorded target-bound owned resource identities and validated remote revisions; foreign, changed, legacy-unbound or incomplete custody remains a refusal rather than adoption
- [ ] #2 Pending intent is durable before remote mutation, lost responses and interruption retain recovery custody, and a secret-free terminal or remaining-obligation receipt distinguishes confirmed absence from unknown outcomes
- [ ] #3 Process-boundary tests exercise owned and foreign resources, concurrent modification, response loss and emitter/provisioner authority; ordinary preview/apply/release-only activation remain compatible and no tenant-wide deletion or credential substitution is introduced
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
