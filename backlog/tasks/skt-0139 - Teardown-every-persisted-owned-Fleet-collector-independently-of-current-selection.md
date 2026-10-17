---
id: SKT-0139
title: >-
  Teardown every persisted owned Fleet collector independently of current
  selection
status: To Do
assignee: []
created_date: '2026-10-16 13:53'
labels: []
dependencies:
  - SKT-0100
priority: high
type: feature
ordinal: 262000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The accepted one-shot unregister contract removes the currently resolved selected roster. Historical selections or scaling can leave additional destination-bound collector identities in the persisted ownership ledger. Full teardown cannot be certified from selected-roster success while those retained obligations remain; ordinary restart and leader handoff semantics must stay unchanged.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 An explicit bounded teardown mode inventories every persisted owned collector for the exact recorded destination, including unselected historical entries, without deriving deletion authority from names or prefixes
- [ ] #2 Foreign destination, nonempty unbound legacy custody, incomplete inventory, concurrent emitter authority or ambiguous remote outcomes refuse completion and retain recoverable intent; actual confirmed absence alone retires each recorded identity
- [ ] #3 CLI and local HTTP/state-boundary tests cover historical roster changes, response loss, interruption, foreign identity and a secret-free remaining-obligation receipt; selected-roster mode and ordinary restart behavior stay compatible
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
