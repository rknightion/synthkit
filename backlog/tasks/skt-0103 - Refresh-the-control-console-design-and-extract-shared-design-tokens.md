---
id: SKT-0103
title: Refresh the control console design and extract shared design tokens
status: In Progress
assignee:
  - '@codex'
created_date: '2026-10-07 20:31'
updated_date: '2026-10-15 11:44'
labels:
  - ui
  - design
dependencies: []
ordinal: 225000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The console is due a design pass and should share design tokens with other frontends that embed or sit beside it, so they converge on one look. Run a design pass, then extract colour, type, spacing and component tokens into a small package the console consumes.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Tokens extracted and consumed by the console with no behaviour change
- [ ] #2 Design pass reviewed and approved by the maintainer
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Recover the approved October design artifact locally or from the maintainer-specified archive; extract reusable CSS tokens and consume them without changing console behavior; validate with focused UI checks and browser comparison. Root owns final repository gate/review/commit.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-10-07: a design pass covering the console and an adjacent frontend was produced and approved by the maintainer; AC #2 is met once the token extraction lands against it.

Recovered maintainer-specified archive and verified published artifact 1791408283-0027 SHA-256 a75b0dd7793e37fde328a6a7b8ee5dde84729ac79fd38682d640fbf91b0dc32d against archive provenance. Extracted dependency-free private CSS package under internal/control/ui/design-tokens; console compatibility aliases consume approved colour/type/spacing/component values. Preserved explicit light default, stored theme toggle, routing/API/actions. Restored missing full-width rail styles from approved board and wrapped mobile toolbar/status bar after browser evidence showed inline navigation and horizontal overflow. Focused just ui-check passed 22 files/178 tests plus TypeScript/Vite before final layout correction; final rerun pending. Browser proof compares all 54 extracted board tokens in light/dark, checks persisted theme/navigation/mobile width; negative controls establish missing shared token contract and inline navigation before correction. Evidence in /Users/rob/repos/synthkit/codex/console-design/ (layout-before.log, layout-after.log, browser-proof.cjs, console-light.png, console-dark.png, console-mobile-dark.png). Root owns final gate/review/commit and task finalization. No renderer changes, so inventory dump and schema generation are not applicable.

Final focused gate after layout corrections: just ui-check passed again (22 test files, 178 tests, TypeScript and production Vite build), log /Users/rob/repos/synthkit/codex/console-design/ui-check-final.log. Final browser proof passed with 54/54 approved tokens in each theme, persistence/navigation, no page errors and no mobile horizontal overflow; screenshots visually inspected. git diff --check passed for UI changes.
<!-- SECTION:NOTES:END -->
