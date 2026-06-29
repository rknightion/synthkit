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

## Development setup

Requires **Go 1.26+**. The single green-bar command is:

```bash
make gate     # build + vet + test + lint + spdx-check + forbidden-words
```

Other useful targets:

```bash
make build    # -> bin/synthkit
make test     # go test ./...
make lint     # golangci-lint run
DRY_RUN=true go run ./cmd/synthkit -once -dump   # series inventory — diff vs signals/
```

`make gate` must pass before any change is merged. CI runs the same gate plus extended checks.

## Making a change

1. Fork the repository and create a topic branch.
2. **Write tests first** (TDD): a failing test, then the minimal code to make it pass. Table-driven
   tests where they fit. Tests must not make live network calls.
3. Every new `.go` file must carry the license header on line 1:
   `// SPDX-License-Identifier: AGPL-3.0-only` (enforced by `scripts/spdx-check.sh`).
4. Keep `make gate` green.
5. Open a pull request with a clear description of the change and its motivation.

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
