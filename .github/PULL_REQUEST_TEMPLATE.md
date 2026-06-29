<!--
Thanks for contributing! Please keep PRs focused. See CONTRIBUTING.md.
Do not include secrets, tokens, or real infrastructure identifiers.
-->

## What & why

<!-- What does this change do, and what problem does it solve? -->

## Checklist

- [ ] `make gate` is green (build + vet + test + lint + spdx-check + forbidden-words)
- [ ] Tests added/updated (TDD: failing test first), no live network in tests
- [ ] New `.go` files carry `// SPDX-License-Identifier: AGPL-3.0-only` on line 1
- [ ] Conventional Commit title (`feat:` / `fix:` / `docs:` / … ; `!` for breaking)
- [ ] No customer/infra-specific strings added to constructs, workloads, or defaults (kept in blueprints)
- [ ] New metric/label names sourced from `signals/` or added there with provenance (no invented names)
- [ ] `ARCHITECTURE.md` updated if a FROZEN type/interface/invariant changed

## Notes for reviewers

<!-- Anything reviewers should focus on, risks, follow-ups. -->
