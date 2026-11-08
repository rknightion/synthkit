<!--
Thanks for contributing! Please keep PRs focused. See CONTRIBUTING.md.
Do not include secrets, tokens, or real infrastructure identifiers.
-->

## What & why

<!-- What does this change do, and what problem does it solve? -->

## Checklist

- [ ] `just check` is green
- [ ] Relevant behavioral coverage or documentation/render validation completed; bug reproduction observed failing first where applicable; no live tenant calls in tests
- [ ] New `.go` files carry `// SPDX-License-Identifier: AGPL-3.0-only` on line 1
- [ ] Conventional Commit title (`feat:` / `fix:` / `docs:` / … ; `!` for breaking)
- [ ] No customer/infra-specific strings added to constructs, workloads, or defaults (kept in blueprints)
- [ ] New metric/label names sourced from `signals/` or added there with provenance (no invented names)
- [ ] `ARCHITECTURE.md` updated if a FROZEN type/interface/invariant changed

## Validation

<!-- Commands and outcomes; identify any unavailable/failed checks and agreed exceptions.
For emitted-signal changes, include the selected inventory and source comparison.
Hosted CI must validate this revision; skill invocation alone is not a passing gate. -->

## Notes for reviewers

<!-- Anything reviewers should focus on, risks, follow-ups. -->

## Issue

<!-- Closes #<number> on a line of its own for each issue of this repository this change completes (Closes owner/repo#<number> means the same). Refs #<number> when it only relates to one. A change with no issue says "Task: none, <reason>" on a line of its own. -->

## Acceptance evidence

<!-- One row per acceptance criterion of every issue this change closes, copied exactly from the issue's "- [ ] ACn" list. Evidence class is code, live or browser. In Proof, put repository paths in backticks, and write anything else (a command, a read-back, a run) without them. Delete the example row. Leave the table out only when the change closes no issue. -->

| Issue | AC | AC text | Evidence class | Proof |
| --- | --- | --- | --- | --- |
| #N | AC1 | The acceptance criterion, word for word | code | `path/to/the_test.go` TestWhatItProves |
