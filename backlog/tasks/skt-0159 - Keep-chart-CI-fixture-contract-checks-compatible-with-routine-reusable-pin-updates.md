---
id: SKT-0159
title: >-
  Keep chart CI fixture contract checks compatible with routine reusable pin
  updates
status: Done
assignee: []
created_date: '2026-10-21T15:25:00Z'
updated_date: '2026-10-22T03:42:00Z'
labels: []
dependencies: []
priority: low
type: bug
ordinal: 301000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TestCISchemaFixtureFloors compares the full shared helm-validate reusable SHA to a separately maintained literal. Renovate updates the valid workflow pin alone, causing Go CI failure despite unchanged fixture assignment and schema-floor behavior. The current sweep updates the literal to the verified v1.25.11 pin as a contained repair. Design a durable contract check that preserves trusted immutable pinning, complete fixture assignment, supported Kubernetes floors and aggregator coverage without requiring a second manual pin bump.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A genuine unpinned or wrong reusable fails while an authorized immutable reusable patch update does not require an unrelated fixture-test literal edit.
- [x] #2 Fixture coverage, API floors, serial invocation and aggregator assertions remain enforced.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop52 BATCH1: landed 57fae1d (pushed in 47439ad). l159 candidate 9e399044; rv159 guarded PASS (minors: quoted pin, shared SHA in ci.yml, lost line numbers; each fails safe); CodeRabbit 1 file 0 findings; composed gate gaterun 20261022T032253Z-synthkit-91718e79 just check exit 0 on f8bc631.
<!-- SECTION:NOTES:END -->
