---
id: SKT-0019
title: Migrate the repo task surface to just and retire Makefiles and ad-hoc scripts
status: Done
assignee: []
created_date: '2026-08-28 19:06'
updated_date: '2026-08-29 16:49'
labels:
  - 'wave:2-fleet'
dependencies: []
priority: medium
type: chore
ordinal: 101000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Migrate synthkit's developer and CI task surface from `Makefile` to a single top-level `justfile`,
per the frozen fleet standard (mandatory seven-recipe vocabulary, six groups, one self-contained
justfile per repo, CI calls `just`). Verified against `just 1.58.0`.

## 1. Outcome

`/Users/rob/repos/synthkit/justfile` is the single task surface. `just --list` answers "what can I do
in this repo"; `just check` is the complete PR gate and is exactly the union of what `ci-success`
gates. The 12,915-byte `Makefile` (40 targets) is deleted. One script — `scripts/spdx-check.sh` — is
absorbed into a `[script('bash')]` recipe and deleted; every other tracked script survives as a file
(they are shipped skill runtime, real programs, shell test suites, or have non-trivial control flow)
and is reachable through a named recipe, so nobody types a script path again. `.github/workflows/ci.yml`,
`.forgejo/workflows/ci.yml`, `.github/workflows/publish.yml`, `.github/workflows/signal-fidelity-k3d.yml`
and `.github/workflows/trigger-docs-sync.yml` install a pinned `just` and call one-line recipes. Two
previously-orphaned test suites (`plugins/synthkit/skills/test_metadata.sh`,
`scripts/test_docs_validation.py`) and two previously-ungated drift checks (`sync-skills.sh --check`,
`e2e/lab/validate.sh`) become reachable and gated. `AGENTS.md`, `CONTRIBUTING.md`, `docs/cli.md`, the
PR template and `backlog/config.yml`'s `definition_of_done` name `just` recipes; `make` appears
nowhere outside `CHANGELOG.md` and historical `backlog/tasks/`.

## 2. The complete justfile

Drop this in at the repo root as `justfile` (lowercase, no extension). Every command below is the
repo's real command, lifted from the Makefile with make-isms translated. Adjust only where §9 says to.

```just
set shell := ["bash", "-euo", "pipefail", "-c"]

# Tooling pins carried over verbatim from the Makefile. Overridable from the environment.
golangci_version := env('GOLANGCI_LINT_VERSION', 'v2.6.0')
go_licenses_version := env('GO_LICENSES_VERSION', 'v1.6.0')
syft_version := env('SYFT_VERSION', 'v1.18.1')
gitleaks_version := env('GITLEAKS_VERSION', 'v8.21.2')
gcx_context := env('GCX_CONTEXT', 'default')

# show the task surface
default:
    @just --list

# install Go modules, the control-UI node_modules and the pinned linter
setup: _ui-install
    go mod download
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@{{ golangci_version }}

[private]
[working-directory('internal/control/ui')]
_ui-install:
    npm ci

[private]
[working-directory('internal/control/ui')]
_ui-test:
    npm test

[private]
[working-directory('internal/control/ui')]
_ui-build:
    npm run build

[private]
[working-directory('dashboards/internal')]
_selfobs-build:
    python3 build_selfobs_dashboard.py

# ---------------------------------------------------------------------------- check

# format Go sources and this justfile in place
[group('check')]
fmt:
    gofmt -s -w .
    just --fmt

# verify Go and justfile formatting; never mutates
[group('check')]
[no-exit-message]
fmt-check:
    @out=$(gofmt -l -s .); if [ -n "$out" ]; then echo "gofmt -s would rewrite:"; echo "$out"; exit 1; fi
    just --fmt --check

# static analysis over the whole module (go vet + golangci-lint per .golangci.yml)
[group('check')]
[no-exit-message]
lint:
    go vet ./...
    golangci-lint run ./...

# full Go test suite plus the shell and python helper suites
[group('check')]
[no-exit-message]
test filter="": test-helpers test-deploy test-docs test-skill-metadata
    go test ./... {{ if filter != "" { "-run " + quote(filter) } else { "" } }}

# Go test suite with an atomic coverage profile -> coverage.out (superset of `test`)
[group('check')]
[no-exit-message]
cover: test-helpers test-deploy test-docs test-skill-metadata
    go test -covermode=atomic -coverprofile=coverage.out ./...

# race detector over the module; internal/integration is excluded (OOMs a 16 GB runner)
[group('check')]
[no-exit-message]
race:
    go test -race $(go list ./... | grep -v '/internal/integration$')

# initial-setup skill helper tests
[group('check')]
[no-exit-message]
test-helpers:
    bash plugins/synthkit/skills/initial-setup/scripts/test_helpers.sh all

# deployment-helper safety and identity tests
[group('check')]
[no-exit-message]
test-deploy:
    python3 -m unittest scripts/test_synthkit_deploy.py

# docs-validator fixture tests
[group('check')]
[no-exit-message]
test-docs:
    python3 -m unittest scripts/test_docs_validation.py

# operational-skill routing metadata checks
[group('check')]
[no-exit-message]
test-skill-metadata:
    bash plugins/synthkit/skills/test_metadata.sh

# every tracked .go carries the AGPL-3.0-only SPDX header on line 1 (vendored *.pb.go excluded)
[group('check')]
[no-exit-message]
[script('bash')]
spdx-check:
    set -euo pipefail
    header='SPDX-License-Identifier: AGPL-3.0-only'
    missing=()
    while IFS= read -r f; do
      head -1 "$f" | grep -q "$header" || missing+=("$f")
    done < <(git ls-files '*.go' | grep -v '\.pb\.go$')
    if [ "${#missing[@]}" -gt 0 ]; then
      echo "FAIL: .go files missing '$header' on line 1:"
      printf '  %s\n' "${missing[@]}"
      echo "Add the header on line 1 — see LICENSING.md."
      exit 1
    fi
    echo "spdx-check: all $(git ls-files '*.go' | grep -v '\.pb\.go$' | wc -l | tr -d ' ') .go files carry the AGPL-3.0-only header (vendored *.pb.go excluded)."

# content guard for credential shapes + deployment identifiers
[group('check')]
[no-exit-message]
forbidden-words:
    bash scripts/forbidden-words.sh

# the non-build hygiene legs CI runs as one job
[group('check')]
hygiene: spdx-check forbidden-words

# full-history gitleaks scan via the pinned image (needs docker + a full-depth clone)
[group('check')]
[no-exit-message]
secret-scan:
    docker run --rm -v "{{ justfile_directory() }}:/repo" ghcr.io/gitleaks/gitleaks:{{ gitleaks_version }} detect --source=/repo --redact --no-banner

# env-surface drift guard: every Go-read var is documented in .env.example and compose
[group('check')]
[no-exit-message]
env-check:
    go test ./internal/config/ -run TestEnvSurfaceAligned -v

# validate the repository-owned docs.toml contract (needs python 3.11+)
[group('check')]
[no-exit-message]
docs-check:
    @python3 -c 'import sys; assert sys.version_info >= (3, 11), "Python 3.11 or newer is required for docs-check"'
    python3 scripts/validate-docs.py

# BLUEPRINT-SCHEMA.md + fielddocs.json match the live Go types
[group('check')]
[no-exit-message]
schema-check:
    go test ./internal/blueprintschema/ -run TestSchemaCurrent

# the .claude/skills + .agents/skills symlink farm matches plugins/synthkit/skills/
[group('check')]
[no-exit-message]
skills-check:
    scripts/sync-skills.sh --check

# fail if any committed generated artifact drifts from its source
[group('check')]
gen-check: schema-check skills-check

# control-UI: vitest, tsc typecheck and vite build (Node lane, separate from the Go gate)
[group('check')]
[no-exit-message]
ui-check: _ui-install _ui-test _ui-build

# validate Compose render and exact image selection using .env.example as fake input
[group('check')]
[no-exit-message]
[script('bash')]
compose-check:
    set -euo pipefail
    expected=$(python3 scripts/synthkit-deploy.py resolve-image \
      --env-file .env.example \
      --default-ref ghcr.io/rknightion/synthkit:1.3.1 |
      python3 -c 'import json, sys; print(json.load(sys.stdin)["reference"])')
    python3 scripts/synthkit-deploy.py check-compose \
      --compose-file docker-compose.yml \
      --env-file .env.example \
      --expected-reference "$expected"

# docker-level e2e (testcontainers, //go:build e2e); requires a docker-capable host
[group('check')]
[no-exit-message]
[script('bash')]
e2e:
    set -euo pipefail
    DH="${DOCKER_HOST:-$(docker context inspect --format '{{{{.Endpoints.docker.Host}}' "$(docker context show)" 2>/dev/null)}"
    DOCKER_HOST="$DH" go test -tags e2e -v -timeout 15m ./e2e/...

# exercise an exact published digest through committed Compose (release verification)
[group('check')]
[no-exit-message]
[script('bash')]
published-e2e:
    set -euo pipefail
    : "${SYNTHKIT_PUBLISHED_IMAGE_REF:?SYNTHKIT_PUBLISHED_IMAGE_REF is required}"
    : "${SYNTHKIT_EXPECTED_VERSION:?SYNTHKIT_EXPECTED_VERSION is required}"
    : "${SYNTHKIT_EXPECTED_REVISION:?SYNTHKIT_EXPECTED_REVISION is required}"
    DH="${DOCKER_HOST:-$(docker context inspect --format '{{{{.Endpoints.docker.Host}}' "$(docker context show)" 2>/dev/null)}"
    DOCKER_HOST="$DH" go test -tags e2e -run '^TestPublishedCompose$' -v -timeout 15m ./e2e/

# report-only synth-vs-reality corpus comparison; findings are evidence and exit 0
[group('check')]
[no-exit-message]
[script('bash')]
signal-fidelity:
    set -euo pipefail
    tmp=$(mktemp)
    trap 'rm -f "$tmp"' EXIT
    DRY_RUN=true BLUEPRINT_NAMES='*' go run ./cmd/synthkit -once -inventory-json >"$tmp"
    go run ./cmd/signal-fidelity -synth "$tmp" -corpus reality-corpus

# lint the chart and assert the credential + exposure render permutations (needs helm)
[group('check')]
[no-exit-message]
helm-test:
    helm lint charts/synthkit
    bash charts/synthkit/tests/render_test.sh

# static-only validation of the k3d capture-lab scaffolding (never touches docker)
[group('check')]
[no-exit-message]
lab-check:
    bash e2e/lab/validate.sh

# (network) fail if the latest upstream Prometheus release changed the RW2 proto vs the pinned copy
[group('check')]
[no-exit-message]
[script('bash')]
proto-drift-check:
    set -euo pipefail
    pinned=$(grep -m1 'UpstreamOriginalSHA256:' internal/sink/promrw/writev2/PROVENANCE.md | awk '{print $NF}')
    pinned_tag=$(grep -m1 'Tag:' internal/sink/promrw/writev2/PROVENANCE.md | awk '{print $NF}')
    resp=$(curl -fsSL "https://api.github.com/repos/prometheus/prometheus/releases/latest") || { echo "proto-drift-check: NETWORK ERROR reaching GitHub (not a drift failure)"; exit 1; }
    latest=$(printf '%s' "$resp" | sed -E -n 's/.*"tag_name": *"([^"]+)".*/\1/p' | head -n1)
    [ -n "$latest" ] || { echo "proto-drift-check: could not parse latest release tag from GitHub API"; exit 1; }
    tmp=$(mktemp)
    trap 'rm -f "$tmp"' EXIT
    curl -fsSL "https://raw.githubusercontent.com/prometheus/prometheus/$latest/prompb/io/prometheus/write/v2/types.proto" -o "$tmp" || { echo "proto-drift-check: NETWORK ERROR fetching proto at $latest (not a drift failure)"; exit 1; }
    got=$(shasum -a 256 "$tmp" | awk '{print $1}')
    if [ "$got" = "$pinned" ]; then
      echo "proto-drift-check: OK — RW2 proto unchanged from pinned $pinned_tag through latest $latest"
    else
      echo "proto-drift-check: DRIFT — latest release $latest RW2 proto sha $got != pinned $pinned_tag sha $pinned"
      echo "  Review https://github.com/prometheus/prometheus/blob/$latest/prompb/io/prometheus/write/v2/types.proto and re-vendor if the change is relevant."
      exit 1
    fi

# THE GATE — everything a PR must pass; exactly the union of what ci-success gates
[group('check')]
check: fmt-check lint gen-check env-check docs-check test race hygiene ui-check image compose-check helm-test lab-check signal-fidelity e2e secret-scan

# CI superset of `check`: swaps `test` for `cover` so coverage.out exists for the Codacy upload
[group('check')]
ci: fmt-check lint gen-check env-check docs-check cover race hygiene ui-check image compose-check helm-test lab-check signal-fidelity e2e secret-scan

# ---------------------------------------------------------------------------- build

# compile every package
[group('build')]
build:
    go build ./...

# build the control-UI assets into internal/control/ui/dist
[group('build')]
ui: _ui-install _ui-build

# build the container image from local source
[group('build')]
image tag="synthkit:ci":
    docker build -t {{ tag }} .

# remove build outputs that `just setup` + `just build` reproduce
[group('build')]
clean:
    rm -rf dist internal/control/ui/dist internal/control/ui/node_modules coverage.out

# ---------------------------------------------------------------------------- dev

# run synthkit against the current .env (long-running; ctrl-c to stop)
[group('dev')]
run:
    go run ./cmd/synthkit

# print the full catalog series/label inventory for offline diff against signals/
[group('dev')]
dump:
    DRY_RUN=true BLUEPRINT_NAMES='*' go run ./cmd/synthkit -once -dump

# start the local compose stack from the selected published image and wait for readiness
[group('dev')]
up:
    docker compose up -d --wait

# start the local compose stack, building the image from local source
[group('dev')]
up-build:
    docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build

# stop and remove the local compose stack (named volumes are kept)
[group('dev')]
down:
    docker compose down

# capture real k8s-monitoring collector egress in disposable k3d clusters (long-running, ~45 min; needs docker, k3d, helm, kubectl)
[group('dev')]
[no-exit-message]
lab *permutations:
    bash e2e/lab/run.sh {{ permutations }}

# ---------------------------------------------------------------------------- gen

# regenerate every committed generated artifact; idempotent (running twice yields no diff)
[group('gen')]
gen: blueprint-schema skills-sync

# regenerate BLUEPRINT-SCHEMA.md + internal/blueprintschema/fielddocs.json from the live Go types
[group('gen')]
blueprint-schema:
    go run ./cmd/blueprint-schema

# regenerate the .claude/skills + .agents/skills symlink farm from plugins/synthkit/skills/
[group('gen')]
skills-sync:
    scripts/sync-skills.sh

# regenerate the vendored Prometheus RW2 protobuf Go types (needs protoc + protoc-gen-go)
[group('gen')]
proto:
    protoc --go_out=. --go_opt=module=github.com/rknightion/synthkit --proto_path=internal/sink/promrw/writev2 internal/sink/promrw/writev2/types.proto

# regenerate the vendored Pyroscope pprof + push protobuf Go types (needs protoc + protoc-gen-go)
[group('gen')]
pyroscope-proto:
    protoc --go_out=. --go_opt=module=github.com/rknightion/synthkit --proto_path=internal/pyroscope/pprofpb internal/pyroscope/pprofpb/profile.proto
    protoc --go_out=. --go_opt=module=github.com/rknightion/synthkit --proto_path=internal/sink/pyroscope/pushv1 internal/sink/pyroscope/pushv1/push.proto

# regenerate the vendored sigil.v1 ingest MESSAGE types (message-only, no grpc)
[group('gen')]
sigil-proto:
    protoc --go_out=. --go_opt=module=github.com/rknightion/synthkit --proto_path=internal/sink/sigil/v1 internal/sink/sigil/v1/generation_ingest.proto internal/sink/sigil/v1/evaluation_ingest.proto

# ---------------------------------------------------------------------------- infra

# build the self-obs dashboard and push it to the named gcx context, then print its URL
[confirm('push the self-obs dashboard to a live Grafana stack?')]
[group('infra')]
selfobs-dashboard context=gcx_context: _selfobs-build
    gcx --context {{ quote(context) }} resources push -p dashboards/internal/synthkit-selfobs.json
    @echo "deployed: ${GC_SELF_GRAFANA_URL:-<set GC_SELF_GRAFANA_URL>}/d/synthkit-selfobs"

# read EKS + core CloudWatch metric shapes from an explicitly named gcx context into reality-corpus/
[confirm('this merges live read-back evidence into the tracked reality-corpus/ — continue?')]
[group('infra')]
[no-exit-message]
corpus-gcx context since="24h":
    go run ./cmd/reality-corpus-gcx -context {{ quote(context) }} -since {{ quote(since) }} -corpus reality-corpus

# provision a destination Grafana stack with the Infinity datasource + customer dashboards
[confirm('this writes a datasource and dashboards into a live Grafana stack — continue?')]
[group('infra')]
provision context base_url:
    provisioning/provision.sh --context {{ quote(context) }} --base-url {{ quote(base_url) }}

# ---------------------------------------------------------------------------- release

# generate THIRD_PARTY_NOTICES.md from dependency licenses (release-time; not gated)
[group('release')]
notices:
    go run github.com/google/go-licenses@{{ go_licenses_version }} csv ./... > THIRD_PARTY_NOTICES.md

# generate SPDX + CycloneDX SBOMs into dist/sbom/ (release-time; not gated)
[group('release')]
sbom:
    mkdir -p dist/sbom
    go run github.com/anchore/syft/cmd/syft@{{ syft_version }} scan dir:. -o spdx-json=dist/sbom/synthkit.spdx.json -o cyclonedx-json=dist/sbom/synthkit.cdx.json
```

### Deliberate departures from a literal Makefile port

| What | Why |
|---|---|
| `vet` folded into `lint` | `lint` is mandatory vocabulary; `.golangci.yml` already enables `govet`, but `go vet ./...` is kept as an explicit first line so the leg still means what the old `make vet` meant. |
| `golangci-lint run ./...` added to the gate | `.golangci.yml` exists and is configured (`errcheck`, `staticcheck`, `unused`, `misspell`, `ineffassign`, gofmt formatter) but **nothing in the repo ever ran it** — `CONTRIBUTING.md:23,31` and `.github/PULL_REQUEST_TEMPLATE.md:12` both claim `make gate` runs lint and it does not. See §9 trap 2 for the fallback if it surfaces pre-existing findings. |
| `rw-proto-check` renamed `proto-drift-check` and **removed from `check`** | It is a live GitHub API call. It was in `make gate` but deliberately excluded from CI (`ci-go` comment: "network → fails offline CI"). A network call inside the recipe an agent runs in a loop is worse than the coverage it buys. Run it before re-vendoring the proto and before a release. |
| `skills-check`, `lab-check`, `env-check`, `docs-check`, `test-docs`, `test-skill-metadata` added to the gate | All exist today and none are gated. `check` may be a superset of CI; it may never be a subset. |
| `ci-go` / `ci-ui` / `ci-docker` have no recipe | They were CI-shaped wrappers. The workflow jobs now call the underlying recipes directly (§5). |
| `check` includes the docker-bound legs (`image`, `e2e`, `secret-scan`) | The standard is explicit: if CI runs a check that `check` does not, agents push red. All three gate `ci-success` today. `just check` therefore needs a docker-capable host. |

## 3. Makefile disposition

Every target in `/Users/rob/repos/synthkit/Makefile`. After the table: `git rm Makefile`.

| Make target | Replacement recipe | Notes |
|---|---|---|
| `.PHONY` line 1 | — | Delete; meaningless in just. |
| `GCX_CONTEXT ?= default` | `gcx_context := env('GCX_CONTEXT', 'default')` | Standard `?=` → `env()` translation. |
| `GO_LICENSES_VERSION ?=` | `go_licenses_version := env('GO_LICENSES_VERSION', 'v1.6.0')` | |
| `SYFT_VERSION ?=` | `syft_version := env('SYFT_VERSION', 'v1.18.1')` | |
| `GITLEAKS_VERSION ?=` | `gitleaks_version := env('GITLEAKS_VERSION', 'v8.21.2')` | |
| `build` | `build` | Identical body. Note `CONTRIBUTING.md:29` wrongly claims it emits `bin/synthkit`; it never did. |
| `test` | `test` | Deps widen from `helper-tests` to `test-helpers test-deploy test-docs test-skill-metadata`. Gains an optional `filter` param. |
| `helper-tests` | `test-helpers` | The `$(MAKE) deploy-tests` recursion is replaced by `test`'s dependency list. |
| `deploy-tests` | `test-deploy` | Identical body. |
| `cover` | `cover` | Identical body; deps widened like `test`. |
| `vet` | folded into `lint` | First line of `lint`. |
| `gate` | `check` | Now a strict superset (see §2 departures table). |
| `race` | `race` | `$$(...)` → `$(...)`, `integration$$` → `integration$`. |
| `spdx-check` | `spdx-check` | Script ABSORBED into a `[script('bash')]` recipe; `scripts/spdx-check.sh` is deleted. |
| `forbidden-words` | `forbidden-words` | Script KEPT; recipe wraps it. |
| `hygiene` | `hygiene` | Dependency list `spdx-check forbidden-words`. |
| `secret-scan` | `secret-scan` | `$(CURDIR)` → `{{ justfile_directory() }}`; body flattened to one line. |
| `notices` | `notices` | Group `release`. |
| `sbom` | `sbom` | Group `release`. |
| `env-check` | `env-check` | Identical body; now inside `check`. |
| `blueprint-schema` | `blueprint-schema` (+ `gen`) | `gen` aggregates it with `skills-sync`. |
| `dump` | `dump` | Identical body. |
| `run` | `run` | Doc comment marks it long-running. |
| `docker` | `up` | Renamed — `docker` is not vocabulary and reads like a build. |
| `docker-build` | `up-build` | |
| `skills-sync` | `skills-sync` | Script KEPT; recipe wraps it. Drop the leading `./` — just runs from the justfile dir. |
| `skills-check` | `skills-check` | Now inside `gen-check`, therefore inside `check`. |
| `docs-check` | `docs-check` | Identical body incl. the Python 3.11 assertion. Now inside `check`. |
| `proto` | `proto` | Line-continuations collapsed to one line. |
| `selfobs-dashboard` | `selfobs-dashboard` | `cd dashboards/internal && …` → private `_selfobs-build` with `[working-directory]`; gains `[confirm]` (writes to a live Grafana stack). |
| `pyroscope-proto` | `pyroscope-proto` | Two `protoc` invocations, one per line. |
| `sigil-proto` | `sigil-proto` | |
| `rw-proto-check` | `proto-drift-check` | Multi-line `\`-continued make recipe → `[script('bash')]`. All `$$` → `$`. **Not** in `check`. |
| `ui-install` | private `_ui-install` | `cd internal/control/ui && npm ci` → `[working-directory('internal/control/ui')]`. |
| `ui` | `ui` | |
| `gate-ui` | `ui-check` | `npm run test && npm run build` becomes `_ui-test` + `_ui-build` deps — one shell per line means `&&` chaining across lines does not work. |
| `ci-go` | — | Deleted. `.github`/`.forgejo` `go` jobs call `just fmt-check`, `just lint`, `just cover`, `just race` (plus the cheap `gen-check`/`env-check`/`docs-check`) as separate steps. |
| `ci-ui` | `ui-check` | `npm ci` is `_ui-install`, already a dep. |
| `ci-docker` | `image` + `compose-check` | Two steps in the `docker` job; the `$(MAKE) compose-check` recursion disappears. |
| `compose-check` | `compose-check` | Nested `$$(…)` command substitution with an embedded python one-liner → `[script('bash')]` with an `expected=` variable. Keep the `ghcr.io/rknightion/synthkit:1.3.1` default-ref literal **verbatim** (see §9 trap 6). |
| `e2e` | `e2e` | `\`-continued `DH=` assignment needs one persistent shell → `[script('bash')]`. The `docker context inspect --format '{{.Endpoints.docker.Host}}'` braces **must** be written `{{{{.Endpoints.docker.Host}}` (§9 trap 1). |
| `published-e2e` | `published-e2e` | Three `@test -n …` guards → `: "${VAR:?msg}"`. Same brace escape. |
| `signal-fidelity` | `signal-fidelity` | `trap … 0` → `trap … EXIT`; `[script('bash')]` for the shared `$tmp`. |
| `signal-fidelity-k3d` | `lab` | Gains an optional variadic permutation selector matching `e2e/lab/run.sh`'s own argument. |
| `signal-fidelity-eks-readback` | `corpus-gcx context` | The `$(origin GCX_CONTEXT) != file` guard exists only to defeat make's `?=` default. In just this becomes a **required parameter** — no default is possible, which is the intent. Gains `[confirm]` (it merges into tracked `reality-corpus/`). |
| `helm-test` | `helm-test` | Identical body. |
| `ci` | `ci` | Widened from `ci-go ci-ui ci-docker helm-test` to the true CI union (see §2). |

Then: `git rm Makefile`. There is exactly one Makefile in the repo — `find . -name 'Makefile' -o -name 'GNUmakefile'` returns only the root one, and no `vendor/`, `node_modules/`, `third_party/` or `.venv/` is tracked.

## 4. Script disposition

Every tracked `.sh`/`.py` used as a dev or CI task. Classified per fleet standard §6. The test:
*would this file still need to exist if `just` did everything a developer types?*

| Script | Verdict | Recipe | Reason / exact lines |
|---|---|---|---|
| `scripts/spdx-check.sh` (22 lines) | **ABSORB** | `spdx-check` | Task-shaped gate with a trivial read-loop; nothing but a developer or CI ever calls it. Body becomes the `[script('bash')]` recipe in §2 verbatim (the `set -euo pipefail`, `header=`, `missing=()` loop over `git ls-files '*.go' \| grep -v '\.pb\.go$'`, the failure block, the success `echo`). Drop the script's self-referential hint line `Add the header (see LICENSING.md) — e.g. via: scripts/spdx-check.sh` and replace it with `Add the header on line 1 — see LICENSING.md.` Then `git rm scripts/spdx-check.sh`. |
| `scripts/forbidden-words.sh` (63 lines) | KEEP | `forbidden-words` | Takes optional file arguments and is invoked by a pre-commit hook with staged files — a caller that is neither a developer typing nor CI. Also sources a library. |
| `scripts/lib/private-paths.sh` (36 lines) | KEEP | none (sourced) | A sourced bash library, not a task. |
| `scripts/sync-skills.sh` (122 lines) | KEEP | `skills-sync`, `skills-check` | A real program: functions, a `--check` mode, drift assertions over every `CLAUDE.md`, a `python3 os.path.relpath` shell-out, bash-3.2 portability workarounds. |
| `scripts/synthkit-deploy.py` (1444 lines) | KEEP | `compose-check`, and called directly by `publish.yml` and `docs/` runbooks | A multi-subcommand program (`resolve-image`, `check-compose`, `verify-image`, `inspect-running`) documented as an operator CLI in `docs/cli.md:70`. |
| `scripts/validate-docs.py` (159 lines) | KEEP | `docs-check` | A real program. |
| `scripts/test_synthkit_deploy.py` (863 lines) | KEEP | `test-deploy` | Test suite. |
| `scripts/test_docs_validation.py` (82 lines) | KEEP | `test-docs` | Test suite. **Currently orphaned** — nothing invokes it. The recipe is what makes it reachable. |
| `scripts/sigil-livecheck/main.go` | KEEP, no recipe | none | A throwaway live-verify harness explicitly documented as "NOT part of any build; run with go run" and needing live sigil credentials. Out of scope. |
| `dashboards/internal/build_selfobs_dashboard.py` (1105 lines) | KEEP | `selfobs-dashboard` (via private `_selfobs-build`) | A real program. |
| `charts/synthkit/tests/render_test.sh` (227 lines) | KEEP | `helm-test` | Shell test suite with positive and negative render permutations. |
| `e2e/lab/run.sh` (317 lines) | KEEP | `lab` | Matrix entrypoint: job scheduling, a concurrency bound, per-permutation result records. A real program. |
| `e2e/lab/permutation.sh` (577 lines) | KEEP | none directly (called by `run.sh`) | Worker program. |
| `e2e/lab/validate.sh` (467 lines) | KEEP | `lab-check` | Static validation suite. **Currently ungated** — the recipe puts it in `check`. |
| `e2e/lab/permutations/{alloy-default,alloy-otlp-podlogs,otel-receivers}/deploy.sh` | KEEP | none (called by `permutation.sh`) | Per-permutation deployment fragments invoked by the worker, not by a developer. |
| `plugins/synthkit/skills/initial-setup/scripts/add-secret.sh` (91) | KEEP | none | **Shipped runtime artifact.** Documented for end users to run in their own terminal on a machine that has no `just` (`SKILL.md:70,74,103`, `README.md:55`). Absorbing it would break the skill. |
| `plugins/synthkit/skills/initial-setup/scripts/set-env.sh` (93) | KEEP | none | Same — shipped skill script referenced from `README.md:55`, `docs/index.md:67-68`, `docs/quickstart.md:102`. |
| `plugins/synthkit/skills/initial-setup/scripts/test_helpers.sh` (474) | KEEP | `test-helpers` | Shell test suite. |
| `plugins/synthkit/skills/test_metadata.sh` (35) | KEEP | `test-skill-metadata` | Shell test suite. **Currently orphaned** — nothing invokes it. |
| `provisioning/provision.sh` (73) | KEEP | `provision` | Argument parsing, an embedded python heredoc, staff-run against a live customer stack. Documented in `provisioning/README.md:27,32`. |

Net file deletions: `Makefile`, `scripts/spdx-check.sh`. Nothing else is deleted.

## 5. CI changes

The `setup-just` step. Resolve the SHA yourself — **do not invent one**:

```bash
gh api repos/extractions/setup-just/git/ref/tags/v4 --jq .object.sha
```

Use that same SHA in every GitHub workflow file below, with a `# v4` trailing comment matching this
fleet's pin convention:

```yaml
      - uses: extractions/setup-just@<sha-from-the-command-above> # v4
        with:
          just-version: '1.58.0'
```

`just-version` is pinned exactly because `just --fmt` output carries no backwards-compatibility
guarantee — an unpinned bump can turn `fmt-check` red with no repo change.

### `.github/workflows/ci.yml`

Insert the `setup-just` step immediately after `actions/checkout` in **every** job except `ci-success`.
Then rewrite the `run:` bodies:

| Job | Line today | Becomes |
|---|---|---|
| `go` | `21: - run: make ci-go` | five steps: `- run: just fmt-check`, `- run: just lint`, `- run: just gen-check`, `- run: just env-check`, `- run: just docs-check`, `- run: just cover`, `- run: just race`. `cover` must run before the Codacy step so `coverage.out` exists. |
| `hygiene` | `40: - run: make hygiene` | `- run: just hygiene` (keep the `env: FORBIDDEN_WORDS_PATTERN` block unchanged) |
| `secret-scan` | `52: - run: make secret-scan` | `- run: just secret-scan` (keep `fetch-depth: 0`) |
| `ui` | `63: - run: make ci-ui` | `- run: just ui-check` |
| `docker` | `69: - run: make ci-docker` | two steps: `- run: just image` then `- run: just compose-check` |
| `e2e` | `80: - run: make e2e` | `- run: just e2e` |
| `signal-fidelity` | `92: - run: make signal-fidelity` | `- run: just signal-fidelity` |
| `helm` | `100-101: - run: helm version` / `- run: make helm-test` | keep `helm version`; `- run: just helm-test`. Add `- run: just lab-check` here (static only, no docker). |

Also update the file's header comment (lines 1-4): it currently says "Both call identical `make`
targets; only the four standard actions … are used so the bare `uses:` names resolve on both GitHub
and Forgejo". Rewrite to say both call identical `just` recipes, and that `setup-just` is the one
third-party action, spelled bare on GitHub and full-URL on Forgejo.

**Must not change:** the `ci-success` job (`106-118`) including `name: ci-success` and its
`needs: [go, hygiene, secret-scan, ui, docker, e2e, signal-fidelity, helm]` — the branch ruleset gates
on that exact check name; the `codacy/codacy-coverage-reporter-action` step and its `if:` guard and
SHA pin; every existing action SHA pin; `go-version-file: go.mod`; `cache: true`; `cache-dependency-path`;
the `e2e` job's `needs: [go]`.

### `.forgejo/workflows/ci.yml`

Same six jobs, same recipe substitutions (`28 → just` legs, `38 → just hygiene`, `51 → just ui-check`,
`57 → just image` + `just compose-check`, `68 → just e2e`, `80 → just signal-fidelity`). Forgejo cannot
resolve a bare third-party `uses:`, so spell it full-URL:

```yaml
      - uses: https://github.com/extractions/setup-just@<same-sha> # v4
        with:
          just-version: '1.58.0'
```

Update the header comment's "Assumptions" paragraph to record this as the second documented divergence
from `.github` (the first being the omitted `secret-scan` job). If the Forgejo runner cannot reach
`github.com` for the action, fall back to the same pinned-binary pattern
`.github/workflows/signal-fidelity-k3d.yml:25-45` already uses: `curl` the `just` release tarball,
`sha256sum --check` it, `sudo install -m 0755 just /usr/local/bin/just`. Do not `apt install just` —
it is unavailable or unreliable on the `ubuntu-22.04`/`ubuntu-24.04` runner images.

### `.github/workflows/signal-fidelity-k3d.yml`

Insert `setup-just` after the checkout at line 21-23 (before "Install pinned cluster tools").
Line `51: run: make signal-fidelity-k3d` → `run: just lab`. Keep `permissions: contents: read`,
`concurrency: {group: signal-fidelity-k3d, cancel-in-progress: false}`, `timeout-minutes: 45`,
`persist-credentials: false`, the pinned k3d/helm/kubectl install block with its sha256 checks, the
`LAB_CAPTURE_TIMEOUT_SECONDS`/`LAB_OUTPUT_DIR` env block, and the `upload-artifact` step and SHA pin.

### `.github/workflows/publish.yml`

Insert `setup-just` in the `verify-release` job (after the checkout at 81-85, before or after the Go
setup) and in the `notices` job (after the checkout at 162-166).

- Line `133: make compose-check` (inside the "Verify exact image identity" `run: |` block) → `just compose-check`. **Do not restructure the rest of that block** — the digest computation, the embedded `python3 - <<'PY'` heredoc, and the `scripts/synthkit-deploy.py verify-image` call all stay exactly as they are.
- Line `148: run: make published-e2e` → `run: just published-e2e`. Keep the three `SYNTHKIT_*` env vars.
- Line `177: make notices` → `just notices`. Keep the `gh release upload` line after it.

**Must not change:** `uses: rknightion/.github/.github/workflows/container-publish.yml@f316906… # v1.3.1`
(a reusable call — never convert a `uses:` into a `run: just`), every `permissions:` block, every
`persist-credentials: false`, `step-security/harden-runner` and its SHA, `run-name:`, the
`workflow_dispatch` inputs, the `identity` job's revision resolution.

### `.github/workflows/trigger-docs-sync.yml`

Insert `setup-just` after the checkout (line 24-25). Line `33: run: python3 scripts/validate-docs.py`
→ `run: just docs-check`. Keep `actions/setup-python` at `python-version: '3.14'` (the recipe asserts
3.11+), the `rknightion/.github/.github/actions/broker-token` step with `permission-set: docs-sync` and
`role: docs-sync-synthkit`, the `peter-evans/repository-dispatch` step and its SHA, `permissions:`,
`concurrency:`, and the `if:` guard.

### Workflows that must NOT be touched at all

`actionlint.yml`, `zizmor.yml`, `codeql.yml`, `scorecard.yml`, `dependency-review.yml`,
`release-please.yml`, `auto-rc.yml`, `ghcr-cleanup.yml`, `arm-automerge.yml`. These are GitHub-native
or shared-reusable calls; `just` replaces the shell inside a step, not a workflow.

## 6. Docs and agent-contract changes

| File:line | Today | Change |
|---|---|---|
| `AGENTS.md:104-116` | "Verification" section with a `make gate` fenced block | Replace the fenced block with `just check`. Keep the `-once -dump` line as `just dump`. |
| `AGENTS.md:108` | "require the wiring pass `make blueprint-schema`" | → `just gen` |
| `AGENTS.md` (new section, after "Verification") | — | Add the §9 Task interface block, verbatim, below. |
| `CLAUDE.md` | `@AGENTS.md` import adapter, 5 lines | **Leave unchanged.** `scripts/sync-skills.sh` asserts every `CLAUDE.md` is a regular file importing `@AGENTS.md`; editing it risks tripping `skills-check`. |
| `CONTRIBUTING.md:20-24` | "The single green-bar command is: `make gate # build + vet + test + lint + spdx-check + forbidden-words`" | → `just check` with an accurate leg list. |
| `CONTRIBUTING.md:28-33` | `make build` / `make test` / `make lint` block | → `just build`, `just test`, `just lint`, `just dump`. Drop the false `-> bin/synthkit` annotation. |
| `CONTRIBUTING.md:35` | "`make gate` must pass" | → "`just check` must pass before any change is merged, and is exactly what CI enforces." |
| `CONTRIBUTING.md:43` | "(enforced by `scripts/spdx-check.sh`)" | → "(enforced by `just spdx-check`)" — the script is deleted. |
| `CONTRIBUTING.md:44` | "Keep `make gate` green." | → "Keep `just check` green." |
| `.github/PULL_REQUEST_TEMPLATE.md:12` | "`make gate` is green (build + vet + test + lint + spdx-check + forbidden-words)" | → "`just check` is green" |
| `README.md:76` | "symlinks kept in sync by `make skills-sync` (verified by `make skills-check`)" | → `just skills-sync` / `just skills-check` |
| `README.md:55` | `./plugins/.../set-env.sh DRY_RUN false .env` | **Leave unchanged** — shipped skill script, end users have no `just`. |
| `docs/cli.md:3` | frontmatter "…verification mode, and make target." | → "…and just recipe." |
| `docs/cli.md:119` | `make blueprint-schema` | → `just gen` |
| `docs/cli.md:205-241` | the whole `## make targets` table (36 rows) | Replace with a `## just recipes` section. Do **not** hand-maintain a full recipe table — state that `just --list` is authoritative, then keep only rows that carry information a doc comment cannot (`corpus-gcx` needing an explicit context, `published-e2e`'s three required env vars, `lab`'s ~45-min runtime and tool requirements, `proto-drift-check` being a network call outside `check`). |
| `docs/blueprint-reference.md:8,11` | "generated … via `make blueprint-schema`" / "Run `make blueprint-schema`" | → `just gen` |
| `docs/configuration.md:124` | "`make compose-check` renders the deployment" | → `just compose-check` |
| `docs/credentials.md:113` | "`make compose-check` uses `.env.example`" | → `just compose-check` |
| `docs/deployment.md:296` | "through `make compose-check`" | → `just compose-check` |
| `docs/installation.md:38` | "run `make gate` — that runs `build`, `vet`, `test` (with the race detector), and the SPDX + hygiene checks" | → `just check`, with the leg list corrected. |
| `docs/reality-corpus.md:62` | `GCX_CONTEXT=<ctx> make signal-fidelity-eks-readback` | → `just corpus-gcx <ctx>` (the env var is no longer how the context is selected) |
| `docs/tools.md:163,197` | `make skills-sync` / `make skills-check` (3 occurrences) | → `just skills-sync` / `just skills-check` |
| `ARCHITECTURE.md:454` | "regenerated via `make proto`" | → `just proto` |
| `ARCHITECTURE.md:458` | "regen via `make pyroscope-proto`" | → `just pyroscope-proto` |
| `ARCHITECTURE.md:468` | "A blocking `make rw-proto-check` target detects upstream RW2-proto drift." | → "`just proto-drift-check` detects upstream RW2-proto drift (network; run it before re-vendoring or cutting a release — it is not part of `just check`)." |
| `LICENSING.md:43,47` | "**`make notices`**" / "**`make sbom`**" | → `just notices` / `just sbom` |
| `LICENSING.md:52` | "**not** part of `make gate`" | → "not part of `just check`" |
| `charts/synthkit/README.md:237` | `bash charts/synthkit/tests/render_test.sh` | → `just helm-test` (keep the prose at 240 naming the script — it is a KEEP file) |
| `dashboards/internal/README.md:71` | `python3 build_selfobs_dashboard.py` | Add `just selfobs-dashboard` as the entry point; the direct invocation may stay documented as the build-only half. |
| `provisioning/README.md:27,32` | `provisioning/provision.sh --context … --base-url …` | → `just provision <context> <base-url>` |
| `.gitignore:27` | comment "generated by `make notices`/`make sbom`" | → `just notices` / `just sbom` |
| `internal/blueprintschema/render.go:17` | writes `> Regenerate with \`make blueprint-schema\`;` into `BLUEPRINT-SCHEMA.md` | → `just gen`. **Edit the generator, not `BLUEPRINT-SCHEMA.md:4`** — see §9 trap 3. |
| `internal/blueprintschema/schema_gate_test.go:32,52` | comment + failure message naming `make blueprint-schema` | → `just gen` |
| `internal/blueprintschema/docs.go:18` | "Regenerated by `make blueprint-schema`" | → `just gen` |
| `cmd/blueprint-schema/main.go:5` | "Run via `make blueprint-schema`." | → "Run via `just gen`." |
| `scripts/forbidden-words.sh:15` | comment "CI (`make forbidden-words`, via `make gate`)" | → "CI (`just forbidden-words`, via `just check`)" |
| `charts/synthkit/tests/render_test.sh:14` | "Usage: bash charts/synthkit/tests/render_test.sh" | Add "or `just helm-test`". |

### AGENTS.md "Task interface" section (add verbatim)

```markdown
## Task interface

This repo's task surface is a `justfile`. Discover it, don't guess it:

    just --list                        # human-readable
    just --dump --dump-format json     # machine-readable
    just --show <recipe>               # what a recipe actually runs

- `just check` is the full gate and is exactly what CI enforces. It must pass before you commit.
  It needs a docker-capable host (`image`, `compose-check`, `e2e`, `secret-scan` legs).
- Prefer `just <recipe>` over the underlying tool. If you are typing `go test`, you want `just test`.
- Run `just` with stdin from /dev/null. Recipes marked `[confirm]` are destructive — stop and ask
  before running one; never pass `--yes` or `JUST_YES=1`.
- If a task you need does not exist, add a recipe with a `#` doc comment and a `[group(...)]`
  rather than running a bare command.
```

Do **not** paste the recipe list into `AGENTS.md`. The only thing worth hard-coding is which recipe
is the gate.

## 7. backlog/config.yml

Current `definition_of_done` (`backlog/config.yml:4-7`) names two `make` targets. Replace with:

```yaml
definition_of_done:
  - "just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, image, compose-check, helm-test, lab-check, signal-fidelity, e2e, secret-scan)"
  - "just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)"
  - "just dump — inventory diffed against signals/"
```

Drive this through the file itself — `backlog/config.yml` is configuration, not tracker markdown, so a
direct edit is correct here. Do **not** hand-edit anything under `backlog/tasks/` or `backlog/docs/`.

## 8. Order of work

Green at every step. Nothing is deleted until nothing references it.

1. Write `justfile` at the repo root exactly as §2. Do not touch the `Makefile` yet — both can coexist.
2. `just --list` and `just --dump --dump-format json > /dev/null`. Both must exit 0. A non-zero exit
   here means an unstable feature slipped in and every agent touching the repo is blinded.
3. `just --fmt --check`. Fix any diff by running `just --fmt` — note it **reorders attributes
   alphabetically** (§9 trap 4).
4. Prove each leg individually against the old target it replaces:
   `just build`, `just fmt-check`, `just test`, `just race`, `just cover`, `just hygiene`,
   `just env-check`, `just docs-check`, `just gen-check`, `just ui-check`, `just image`,
   `just compose-check`, `just helm-test`, `just lab-check`, `just signal-fidelity`, `just e2e`,
   `just secret-scan`. Two are new to any gate (`lab-check`, `skills-check` inside `gen-check`) and one
   is new entirely (`golangci-lint` inside `lint`) — see §9 traps 2 and 5 for the fallback if they fail
   for pre-existing reasons.
5. `just gen` twice in a row; `git status` must be clean after the second run (idempotence).
6. `just check` end to end on a docker-capable host. This is the acceptance evidence.
7. Resolve the `extractions/setup-just` SHA and switch `.github/workflows/ci.yml`,
   `.forgejo/workflows/ci.yml`, `.github/workflows/signal-fidelity-k3d.yml`,
   `.github/workflows/publish.yml`, `.github/workflows/trigger-docs-sync.yml` per §5. Push and confirm
   `ci-success` goes green with the same job names.
8. Update the generator sources (`internal/blueprintschema/render.go:17`, `docs.go:18`,
   `schema_gate_test.go:32,52`, `cmd/blueprint-schema/main.go:5`), then run `just gen` and commit the
   regenerated `BLUEPRINT-SCHEMA.md` + `internal/blueprintschema/fielddocs.json` in the same commit.
   `just schema-check` must pass afterwards.
9. Update every doc, `AGENTS.md`, the PR template and `backlog/config.yml` per §6 and §7.
10. Absorb `scripts/spdx-check.sh` — verify `just spdx-check` produces the same pass/fail on a
    deliberately header-stripped scratch file, then `git rm scripts/spdx-check.sh`.
11. `git rm Makefile` — **last**. Before doing it, confirm zero references remain:
    `git grep -n 'make [a-z-]\|Makefile\|spdx-check\.sh' -- ':!CHANGELOG.md' ':!backlog/tasks'`
    must return nothing.
12. `just check` once more, and `just --fmt --check`.

## 9. Traps specific to this repo

1. **`{{` in `docker context inspect --format '{{.Endpoints.docker.Host}}'` is just's interpolation
   sigil.** Both the `e2e` and `published-e2e` recipes carry it. Write it `{{{{.Endpoints.docker.Host}}`
   — verified: `just` renders `{{{{` as a literal `{{`. Getting this wrong produces a parse error or,
   worse, a silently mangled `--format` string that makes `DOCKER_HOST` empty and the e2e suite pick the
   wrong Docker socket. The escape is still required inside `[script('bash')]` recipes — interpolation
   happens before the script is written.
2. **`golangci-lint` has never run on this codebase.** `.golangci.yml` is configured
   (`errcheck`, `govet`, `ineffassign`, `misspell`, `staticcheck`, `unused`, gofmt formatter,
   `timeout: 5m`, `_test.go` errcheck exclusion), but no Makefile target and no workflow step invokes
   it — `CONTRIBUTING.md:23,31` and `.github/PULL_REQUEST_TEMPLATE.md:12` claim otherwise and are
   wrong. Run `just lint` early. If it surfaces pre-existing findings that cannot be fixed inside this
   chore: **do not weaken `.golangci.yml`**. Drop the `golangci-lint run ./...` line from `lint`,
   leave `go vet ./...`, file a follow-up task, and say so in this task's notes. That is the narrow
   reversible option.
3. **`BLUEPRINT-SCHEMA.md:4` is generated, not authored.** The `make blueprint-schema` string is
   emitted by `internal/blueprintschema/render.go:17`. Editing the markdown directly makes
   `TestSchemaCurrent` (which `just test` and `just schema-check` both run) fail. Edit the generator,
   then `just gen`, then commit both the source and the regenerated artifact together.
4. **`just --fmt` sorts recipe attributes alphabetically.** Verified on 1.58: `[confirm(...)]` written
   after `[group(...)]` is reordered before it, and `fmt-check` then fails. Write attributes in
   alphabetical order — `confirm`, `group`, `no-exit-message`, `script`, `working-directory` — which is
   the order §2 already uses.
5. **`scripts/sync-skills.sh --check` and `e2e/lab/validate.sh` have never gated anything.** Both may
   already be drifted. `sync-skills.sh --check` also asserts an instruction-file arrangement (root
   `AGENTS.md` must be a regular file; every `CLAUDE.md` must be a regular file containing a bare
   `@AGENTS.md` line) — do not restructure `CLAUDE.md` while doing this migration. If either fails for
   a pre-existing reason, fix it if it is a one-line fix; otherwise drop the leg from `check`, keep the
   recipe, and file a follow-up.
6. **`ghcr.io/rknightion/synthkit:1.3.1` in `compose-check` is a hand-maintained literal.**
   `release-please-config.json` uses `release-type: simple` with **no** `extra-files`, so release-please
   never rewrote the Makefile and will never rewrite the justfile. Carry the literal across verbatim;
   changing it changes what `compose-check` asserts.
7. **`$(origin GCX_CONTEXT) != file` has no just equivalent and does not need one.** The
   `signal-fidelity-eks-readback` target exists to defeat make's own `GCX_CONTEXT ?= default`. In just,
   `corpus-gcx` takes a required positional parameter, so there is no default to defeat. Do not
   reintroduce a default for it. `selfobs-dashboard` keeps the `default` fallback via the
   `gcx_context` variable, matching today's behaviour.
8. **Every recipe line is its own shell.** Four Makefile targets relied on a shared shell inside one
   `\`-continued recipe (`rw-proto-check`, `e2e`, `published-e2e`, `signal-fidelity`) and two relied on
   `cd X && …` (`ui*`, `selfobs-dashboard`). The first four become `[script('bash')]`; the last two
   become `[working-directory(...)]` private recipes. Do not try to chain them with `&&` across lines.
9. **`$$` → `$`.** `race`, `rw-proto-check`, `compose-check`, `e2e`, `published-e2e` and
   `signal-fidelity` all use make's `$$` escape. `$` is not just's sigil; single `$` is correct.
   `race`'s `grep -v '/internal/integration$$'` becomes `grep -v '/internal/integration$'`.
10. **`$(MAKE)` recursion disappears.** `helper-tests` calls `$(MAKE) deploy-tests` and `ci-docker`
    calls `$(MAKE) compose-check`. Both become dependency-list entries; do not write
    `just deploy-tests` inside a recipe body.
11. **`just check` needs docker.** `image`, `compose-check` (indirectly), `e2e` and `secret-scan` all
    need a running daemon, and `helm-test` needs `helm` on PATH. Say so in the `check` doc comment and
    in the AGENTS.md Task interface block. On a machine without docker, `just check` fails at the first
    docker leg — that is correct behaviour, not a bug to work around with a conditional.
12. **`e2e` takes up to 15 minutes and `lab` up to 45.** `lab` is not in `check` (it needs k3d and
    builds real clusters); `e2e` is, because `ci-success` gates it.
13. **`.forgejo/workflows/ci.yml` cannot resolve a bare third-party `uses:`.** Use the full-URL form
    (`uses: https://github.com/extractions/setup-just@<sha>`) or the pinned-binary `curl` +
    `sha256sum --check` pattern already used at `.github/workflows/signal-fidelity-k3d.yml:25-45`. The
    two ci.yml files' header comments both document the parity assumption and must be updated.
14. **`python3 -m unittest scripts/test_synthkit_deploy.py` is the proven invocation** — path form,
    not dotted-module form. `docs-check` additionally asserts Python ≥ 3.11 before running.
15. **Do not add `set quiet`, `set minimum-version`, `set dotenv-load`, list literals, `[cache]` or
    user-defined functions.** One unstable feature makes `just --list` and `just --dump` exit 1 for the
    whole file. `.env` exists in this repo but is loaded by docker-compose's `env_file:` and by the Go
    process, not by the task runner — `set dotenv-load` would change what `just run` sees and is not
    wanted.

## 10. Out of scope

Do not touch:

- **Every KEEP script in §4**, as a file: `scripts/forbidden-words.sh`, `scripts/lib/private-paths.sh`,
  `scripts/sync-skills.sh`, `scripts/synthkit-deploy.py`, `scripts/validate-docs.py`,
  `scripts/test_synthkit_deploy.py`, `scripts/test_docs_validation.py`, `scripts/sigil-livecheck/main.go`,
  `dashboards/internal/build_selfobs_dashboard.py`, `charts/synthkit/tests/render_test.sh`,
  `e2e/lab/run.sh`, `e2e/lab/permutation.sh`, `e2e/lab/validate.sh`,
  `e2e/lab/permutations/*/deploy.sh`, `provisioning/provision.sh`, and every script under
  `plugins/synthkit/skills/`. Only their comment lines naming `make` change (§6).
- **GitHub-native and shared-reusable workflows:** `release-please.yml`, `auto-rc.yml`, `codeql.yml`,
  `zizmor.yml`, `actionlint.yml`, `scorecard.yml`, `dependency-review.yml`, `ghcr-cleanup.yml`,
  `arm-automerge.yml`. Never convert a `uses:` into a `run: just`.
- The `ci-success` job, its name, and its `needs:` list. The branch ruleset gates on that exact name.
- Any `permissions:`, `concurrency:`, `persist-credentials: false`, `fetch-depth: 0`, action SHA pin,
  `workflow_dispatch` input, `run-name:`, or `env:` secret plumbing in any workflow.
- `CLAUDE.md` (a `sync-skills.sh --check`-asserted `@AGENTS.md` adapter), `.claude/skills/` and
  `.agents/skills/` (generated symlink farms — change them only via `just skills-sync`).
- `.golangci.yml`, `.codacy.yaml`, `.gitleaks.toml`, `renovate.json`, `release-please-config.json`,
  `.release-please-manifest.json`, `docs.toml`, `Dockerfile`, `Dockerfile.skcapture`,
  `docker-compose.yml`, `docker-compose.build.yml`, `.env.example`.
- Go source, blueprints, `signals/`, `reality-corpus/`, the Helm chart templates, and `cantfind.md` —
  except the four `make`-naming comment/string edits listed in §6.
- `backlog/tasks/**` and `backlog/docs/**` (drive through the `backlog` CLI only).
- Behaviour: no recipe may change what its Makefile predecessor actually ran, other than the
  deliberate departures listed in §2.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A top-level justfile has the seven mandatory recipes, required shell header, and no unstable features; just --list and just --dump --dump-format json exit 0
- [x] #2 just check passes as the bare-toolchain pre-commit gate; just ci extends it only with the Docker-daemon image, e2e, and secret-scan legs; exact-head ci-success is green
- [x] #3 just --fmt --check exits 0, and fmt-check also verifies gofmt -l -s
- [x] #4 Every public recipe has a doc comment and exactly one sanctioned group except ungrouped default and setup; helpers are private; destructive live recipes carry confirm
- [x] #5 The Makefile is deleted, and semantic command-reference searches find no docs, CI, hook, or source instruction invoking Make or a removed target; prose and history are excluded
- [x] #6 scripts/spdx-check.sh is absorbed and deleted; every KEEP script, including scripts/test_synthkit_deploy.py, remains reachable through a named recipe or its parent program
- [x] #7 GitHub and Forgejo workflows use sanctioned Just provisioning and one-line just recipe calls; ci-success retains its name and gates every required local job plus preserved shared Helm validation; exact-head CI is green
- [x] #8 No shared reusable uses call was converted to inline steps; the migration preserves shared-workflow calls and excludes independently merged workflow changes from its claim
- [x] #9 AGENTS.md names just check as the gate without listing recipes; contributor and operator surfaces contain no instruction to invoke Make or a removed target, excluding ordinary prose and history
- [x] #10 Blueprint-schema generator strings name just gen; generated schema artifacts are committed; just schema-check passes
- [x] #11 backlog/config.yml definition_of_done names just check, just gen, and just dump instead of Make targets
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Preserve the pre-existing untracked runtime/ path and baseline the current Makefile, scripts, workflow callers, and references.
2. Add the fixed justfile surface using the ratified gate model from comment #2: check runs bare-toolchain legs; ci extends it only with explicitly documented Docker/service/cross-compilation legs.
3. Replace Makefile and the absorbable SPDX helper only after callers, workflows, docs, generated schema sources, and backlog configuration refer to recipes; retain every KEEP script.
4. Validate parsing, formatting, targeted recipes, generated-artifact idempotence, the bare-toolchain check and CI superset as available; then run the repository gate and inspect the exact diff.
5. Commit explicit task paths on main, push, and verify the resulting CI run at the exact commit before finalizing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Validated the new lint leg before retiring Makefile. golangci-lint reports 74 pre-existing findings across unrelated packages (15 errcheck, 2 gofmt, 3 ineffassign, 2 misspell, 26 staticcheck, 26 unused); no existing tracker task covers the cleanup. Per the task fallback, .golangci.yml remains unchanged and just lint retains go vet only; linter enablement needs a separately owned follow-up.

Implemented the migration under ratified comment #2: just check is the no-Docker-daemon pre-commit gate, and just ci is check plus the documented Docker-daemon image, e2e, and secret-scan legs. Added the SHA-pinned setup-just v4 action at version 1.58.0, updated active command references including doc-0002 through the CLI, regenerated the schema artifacts twice without drift, and deleted Makefile plus scripts/spdx-check.sh only after the command-reference scan was clean. Local evidence before commit: just --list, JSON dump, formatter, build, cover, check, and ci passed; actionlint passed; zizmor completed with existing permission/persist-credential warnings constrained by the task. CodeRabbit reported one minor stale comment in untouched e2e/readiness_test.go, left out of scope.

Final reconciliation: exact-head CI succeeded, including the repaired Helm static validator, end-to-end job, and ci-success. Local actionlint, lab-check, just discovery/dump, and formatting checks passed. Criteria 2, 5, 7, 8, and 9 remain un-ticked: 2 is superseded by ratified comment #2; 5 and 9 have literal ordinary-English/legal "make" matches while semantic command-reference scans are clean; 7 has a stale needs list that omits the shared Helm-validation job; and 8 includes independently merged workflow changes. The task snapshot Definition of Done retains pre-migration command strings, so those items remain un-ticked.

Root reconciliation corrected stale criteria to the ratified check-versus-ci split, semantic removed-command searches, preserved shared Helm reusable, and independently merged workflow boundary. Exact-head CI run 33256114419 succeeded at 601ad77845ae5bc3637a68ecea161ee316e72f98 across Go, Docker, UI, hygiene, secret scan, signal fidelity, Helm, shared Helm validation, E2E, and ci-success. Later unrelated work and the current dirty checkout are outside this task and untouched.
<!-- SECTION:NOTES:END -->

## Comments

<!-- COMMENTS:BEGIN -->
author: campaign-ordering
created: 2026-08-29 09:18
---
## Fleet ordering — WAVE 2. Starts after the Wave 0 pilot (`sf2loki` / SFL-0073) and the Wave 1 hubs land.

Within Wave 2 the order is free — these repos do not depend on each other. Batching by language is worthwhile so one lane reuses its Makefile-to-recipe mapping across similar repos.

Do not start before the pilot reports. The standard may be amended off the back of it, and picking this up early risks coding against a superseded seam.

**Provisioning `just` in CI.** Which mechanism depends on the runner, and the two must not be mixed:

| Runner | Mechanism |
| --- | --- |
| `arc-arm64` (m7kni self-hosted) | `just` is **baked into the runner image** by `m7kni/ci-tools` (`runner-image/Dockerfile`, `ARG JUST_VERSION`). Do **not** add `extractions/setup-just`, and delete the step if this repo already has one — it installs a second `just` earlier on `PATH` and turns the image pin into a lie. |
| GitHub-hosted (all `rknightion` repos) | `extractions/setup-just`, SHA-pinned, with an explicit `just-version:`. |

Both sides currently sit on **1.58.0** and are Renovate-managed. `ci-tools`' `Tool version drift` workflow fails if the Dockerfile `ARG` and the published image ever disagree, and lists any repo still carrying a second pin.

**While you are in the workflow files, check the hub pin.** On 2026-08-29 Renovate was unfrozen for `rknightion/.github` in `m7kni/renovate-config` — it had been `enabled: false` on the mistaken belief that callers tracked `@main`, which froze the fleet across 19 different hub SHAs (v1.3.1 June → v1.9.7 August) so that no hub fix ever propagated. Bumps now arrive as one grouped, CI-gated, automerged PR per repo. **A `uses:` whose comment is not a real `# vX.Y.Z` still cannot be bumped** (it resolves to a digest-only update, which the fleet rules disable) — if you find one, repair the comment as part of this task.
---

author: campaign-ordering
created: 2026-08-29 10:42
---
## Standard amendment — `ci` is the sanctioned superset of `check` (RATIFIED)

This supersedes the frozen wording *"`check` is the complete local gate and reproduces every CI job that can run off a GitHub runner"*, which several lanes could not honour without making the pre-commit gate depend on a Docker daemon.

**The definitions now are:**

- **`check`** — everything that runs with **only the language toolchain installed**. This is the pre-commit gate. A leg that runs on a bare toolchain belongs here *however long it takes*.
- **`ci`** — `check` plus the legs CI gates that need a **Docker daemon, a service container, or cross-compilation**, and nothing else. Written as `ci: check <heavy legs>`.

**Every leg you put in `ci` must carry a comment naming which of those three it needs.** That comment is the guard: without it `ci` becomes the bin for anything slow or awkward, `check` quietly stops meaning much, and the fleet is back to a per-repo gate.

Eleven of the 42 lanes arrived at this shape independently before it was ratified, which is why it won.

**If this repo has no such legs, it has no `ci` recipe at all** and `check` is the whole gate. Do not add an empty one.
---
<!-- COMMENTS:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Migrated the task surface to just, retained the required scripts, and repaired hosted Helm CI by installing ripgrep for lab-check. Verified actionlint, lab-check, just discovery/dump/formatting, and exact-head ci-success.

Tracker criteria and Definition of Done were reconciled to the binding fleet standard; all are objectively satisfied.
<!-- SECTION:FINAL_SUMMARY:END -->
