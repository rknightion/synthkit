set shell := ["bash", "-euo", "pipefail", "-c"]

# Tooling pins. Overridable from the environment and Renovate-managed from this file.
# renovate: datasource=github-releases depName=golangci/golangci-lint
golangci_version := env('GOLANGCI_LINT_VERSION', 'v2.6.0')
# renovate: datasource=github-releases depName=google/go-licenses
go_licenses_version := env('GO_LICENSES_VERSION', 'v2.0.1')
# renovate: datasource=github-releases depName=anchore/syft
syft_version := env('SYFT_VERSION', 'v1.18.1')
# renovate: datasource=github-releases depName=gitleaks/gitleaks
gitleaks_version := env('GITLEAKS_VERSION', 'v8.21.2')
gcx_context := env('GCX_CONTEXT', 'default')

# show the task surface
default:
    @just --list

# install Go modules, the control-UI node_modules, and the pinned linter
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

# static analysis over the whole module (go vet; golangci-lint awaits cleanup of existing findings)
[group('check')]
[no-exit-message]
lint:
    go vet ./...

# full Go test suite plus the shell and Python helper suites
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

# content guard for credential shapes and deployment identifiers
[group('check')]
[no-exit-message]
forbidden-words:
    bash scripts/forbidden-words.sh

# the non-build hygiene legs CI runs as one job
[group('check')]
hygiene: spdx-check forbidden-words

# CI-only: requires a Docker daemon and a full-depth clone for the full-history scan
[group('check')]
[no-exit-message]
secret-scan:
    docker run --rm -v "{{ justfile_directory() }}:/repo" ghcr.io/gitleaks/gitleaks:{{ gitleaks_version }} detect --source=/repo --redact --no-banner

# env-surface drift guard: every Go-read var is documented in .env.example and Compose
[group('check')]
[no-exit-message]
env-check:
    go test ./internal/config/ -run TestEnvSurfaceAligned -v

# validate the repository-owned docs.toml contract (needs Python 3.11+)
[group('check')]
[no-exit-message]
docs-check:
    @python3 -c 'import sys; assert sys.version_info >= (3, 11), "Python 3.11 or newer is required for docs-check"'
    python3 scripts/validate-docs.py

# BLUEPRINT-SCHEMA.md and fielddocs.json match the live Go types
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

# control-UI: vitest, TypeScript typecheck, and vite build
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

# CI-only: requires a Docker daemon to exercise testcontainers against the production image
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

# lint the chart and assert the credential and exposure render permutations (needs helm)
[group('check')]
[no-exit-message]
helm-test:
    helm lint charts/synthkit
    bash charts/synthkit/tests/render_test.sh

# static-only validation of the k3d capture-lab scaffolding (never touches Docker)
[group('check')]
[no-exit-message]
lab-check:
    bash e2e/lab/validate.sh

# fail if the latest upstream Prometheus release changed the RW2 proto versus the pinned copy
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

# pre-commit gate: every CI validation that runs without a Docker daemon or service container
[group('check')]
check: fmt-check lint gen-check env-check docs-check test race hygiene ui-check compose-check helm-test lab-check signal-fidelity

# CI superset: `check` plus the Docker-daemon legs marked above
[group('check')]
ci: check image e2e secret-scan

# compile every package
[group('build')]
build:
    go build ./...

# build the control-UI assets into internal/control/ui/dist
[group('build')]
ui: _ui-install _ui-build

# CI-only: requires a Docker daemon to build the container image from local source
[group('build')]
image tag="synthkit:ci":
    docker build -t {{ tag }} .

# remove build outputs that `just setup` and `just build` reproduce
[group('build')]
clean:
    rm -rf dist internal/control/ui/dist internal/control/ui/node_modules coverage.out

# run synthkit against the current .env (long-running; ctrl-c to stop)
[group('dev')]
run:
    go run ./cmd/synthkit

# print the full catalog series/label inventory for offline diff against signals/
[group('dev')]
dump:
    DRY_RUN=true BLUEPRINT_NAMES='*' go run ./cmd/synthkit -once -dump

# start the local Compose stack from the selected published image and wait for readiness
[group('dev')]
up:
    docker compose up -d --wait

# start the local Compose stack, building the image from local source
[group('dev')]
up-build:
    docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build

# stop and remove the local Compose stack (named volumes are kept)
[group('dev')]
down:
    docker compose down

# capture real k8s-monitoring collector egress in disposable k3d clusters (long-running; needs Docker, k3d, helm, and kubectl)
[group('dev')]
[no-exit-message]
lab *permutations:
    bash e2e/lab/run.sh {{ permutations }}

# regenerate every committed generated artifact; idempotent (running twice yields no diff)
[group('gen')]
gen: blueprint-schema skills-sync

# regenerate BLUEPRINT-SCHEMA.md and internal/blueprintschema/fielddocs.json from the live Go types
[group('gen')]
blueprint-schema:
    go run ./cmd/blueprint-schema

# regenerate the .claude/skills and .agents/skills symlink farm from plugins/synthkit/skills/
[group('gen')]
skills-sync:
    scripts/sync-skills.sh

# regenerate the vendored Prometheus RW2 protobuf Go types (needs protoc + protoc-gen-go)
[group('gen')]
proto:
    protoc --go_out=. --go_opt=module=github.com/rknightion/synthkit --proto_path=internal/sink/promrw/writev2 internal/sink/promrw/writev2/types.proto

# regenerate the vendored Pyroscope pprof and push protobuf Go types (needs protoc + protoc-gen-go)
[group('gen')]
pyroscope-proto:
    protoc --go_out=. --go_opt=module=github.com/rknightion/synthkit --proto_path=internal/pyroscope/pprofpb internal/pyroscope/pprofpb/profile.proto
    protoc --go_out=. --go_opt=module=github.com/rknightion/synthkit --proto_path=internal/sink/pyroscope/pushv1 internal/sink/pyroscope/pushv1/push.proto

# regenerate the vendored sigil.v1 ingest MESSAGE types (message-only, no gRPC)
[group('gen')]
sigil-proto:
    protoc --go_out=. --go_opt=module=github.com/rknightion/synthkit --proto_path=internal/sink/sigil/v1 internal/sink/sigil/v1/generation_ingest.proto internal/sink/sigil/v1/evaluation_ingest.proto

# build the self-observability dashboard and push it to the named gcx context
[confirm('push the self-obs dashboard to a live Grafana stack?')]
[group('infra')]
selfobs-dashboard context=gcx_context: _selfobs-build
    gcx --context {{ quote(context) }} resources push -p dashboards/internal/synthkit-selfobs.json
    @echo "deployed: ${GC_SELF_GRAFANA_URL:-<set GC_SELF_GRAFANA_URL>}/d/synthkit-selfobs"

# read EKS and core CloudWatch metric shapes into reality-corpus/ from an explicit gcx context
[confirm('this merges live read-back evidence into the tracked reality-corpus/ — continue?')]
[group('infra')]
[no-exit-message]
corpus-gcx context since="24h":
    go run ./cmd/reality-corpus-gcx -context {{ quote(context) }} -since {{ quote(since) }} -corpus reality-corpus

# provision a destination Grafana stack with the Infinity datasource and customer dashboards
[confirm('this writes a datasource and dashboards into a live Grafana stack — continue?')]
[group('infra')]
provision context base_url:
    provisioning/provision.sh --context {{ quote(context) }} --base-url {{ quote(base_url) }}

# generate THIRD_PARTY_NOTICES.md from dependency licenses (release-time; not gated)
[group('release')]
notices:
    go run github.com/google/go-licenses@{{ go_licenses_version }} csv ./... > THIRD_PARTY_NOTICES.md

# generate SPDX and CycloneDX SBOMs into dist/sbom/ (release-time; not gated)
[group('release')]
sbom:
    mkdir -p dist/sbom
    go run github.com/anchore/syft/cmd/syft@{{ syft_version }} scan dir:. -o spdx-json=dist/sbom/synthkit.spdx.json -o cyclonedx-json=dist/sbom/synthkit.cdx.json
