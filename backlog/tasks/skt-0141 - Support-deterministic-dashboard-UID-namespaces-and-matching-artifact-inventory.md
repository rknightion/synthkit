---
id: SKT-0141
title: Support deterministic dashboard UID namespaces and matching artifact inventory
status: To Do
assignee: []
created_date: '2026-10-16 13:54'
labels: []
dependencies: []
references:
  - >-
    https://github.com/rknightion/synthkit/blob/9f04a7d0b1cfebaccf50a8d40063e547967d99a4/cmd/synthkit-dash/main.go
priority: medium
type: feature
ordinal: 264000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The dashboard generator accepts a folder UID, but that changes placement rather than each dashboard UID. Independent installations of the same blueprint can therefore collide even when their folders differ. Consumers need a supported deterministic artifact-identity namespace rather than rewriting generated JSON and risking stale links or panel inventories.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 An explicit optional bounded namespace deterministically isolates all generated folder and dashboard resource identities and internal references for the same blueprint, with legacy default output preserved
- [ ] #2 Panel inventory and verification inputs carry the same effective identities as emitted resources; distinct namespaces do not collide, and malformed or overlong namespace inputs refuse without partial output
- [ ] #3 Actual generator CLI artifact tests cover repeated deterministic output, two namespaces, internal links and inventory verification without copying templates into consumers or requiring tenant credentials
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
