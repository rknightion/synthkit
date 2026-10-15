# Contributing to synthkit

Thanks for your interest in contributing. This document covers how to build, test, and submit
changes.

## Ground rules

- **Blueprint-driven by design.** Constructs and workloads are isolated units; no blueprint-specific
  logic belongs inside them. The three-tier rule (constructs → core libs only; blueprints = wiring
  layer) is a hard invariant enforced by `internal/archtest`. Changes that weaken this will not
  be accepted.
- **No inventing names.** Every metric, label, and field name must be sourced from `signals/` or
  added there with provenance before use. `cantfind.md` tracks open items. Do not emit a name that
  is not in the signals catalogue.
- **Realism direction.** When synthetic data diverges from observed reality, correct the synth to
  match reality — not the other way around.

## Development workflow

Start with the [contributor guide](docs/contributing.md) for the complete tool prerequisites,
setup, focused checks, generated artifacts, and the distinction between local and hosted CI gates.
The `justfile` is the authoritative command surface; run `just` to list its recipes.

Agent-assisted contributions use the bundled
[contribute skill](plugins/synthkit/skills/contribute/SKILL.md) and the repository
[agent instructions](AGENTS.md). Humans follow the same contributor guide and validation commands.
A skill helps select and run checks; it does not replace their evidence.

## Making a change

1. Fork the repository and create a topic branch.
2. Read the relevant architecture and signal contracts. For a bug fix, reproduce the failure before
   fixing it. Add proportionate behavioral coverage for code changes; documentation-only changes
   need documentation validation and visual inspection where relevant, not artificial unit tests.
   Tests use local fixtures or fake upstreams, not live tenants.
3. Every new `.go` file must carry the license header on line 1:
   `// SPDX-License-Identifier: AGPL-3.0-only` (enforced by `just spdx-check`).
4. Run the focused checks during development and `just check` on the final candidate. Report a
   missing tool or failed check explicitly; do not remove a gate to obtain a pass.
5. Open a pull request with the problem, resulting behavior, validation evidence, and any remaining
   limitations. Hosted CI must pass on the submitted revision. Maintainer-specific exceptions or
   direct-to-main workflows do not automatically apply to external contributions.

## License agreement

By contributing, you agree that your contributions are licensed under the
[GNU Affero General Public License v3.0 only](./LICENSE) (`AGPL-3.0-only`), consistent with the
rest of the project. No CLA or sign-off line is required — opening a pull request constitutes
agreement. See [LICENSING.md](./LICENSING.md).

## Commit messages

This project uses [Conventional Commits](https://www.conventionalcommits.org/) — the subject line
drives the generated changelog. Use `feat:`, `fix:`, `docs:`, `refactor:`, `chore:`, etc. Mark
breaking changes with a `!` (e.g. `feat!:`) and a `BREAKING CHANGE:` footer.

## Releases

Releases are automated with [release-please](https://github.com/googleapis/release-please): once
changes land on `main`, it opens a release PR that bumps the version + `CHANGELOG.md` from the
Conventional Commits. Merging that PR publishes the GitHub Release and the container image.
Maintainers cut releases — contributors only need correct commit subjects (`feat`/`fix`/breaking
drive the version).

## Frozen interfaces

Some types and interfaces are marked **FROZEN** in `ARCHITECTURE.md`. Adding, renaming, or removing
fields/methods there is a design change that requires an `ARCHITECTURE.md` update and discussion
first — not a casual edit.
