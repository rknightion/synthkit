.PHONY: build test vet gate race dump run docker skills-sync skills-check proto rw-proto-check pyroscope-proto selfobs-dashboard ui ui-install gate-ui ci-go ci-ui ci-docker e2e ci spdx-check forbidden-words hygiene secret-scan notices sbom

GCX_CONTEXT ?= default

# Release-time tooling pins (used by `notices`/`sbom`; not gated) + the CI secret-scanner.
GO_LICENSES_VERSION ?= v1.6.0
SYFT_VERSION ?= v1.18.1
GITLEAKS_VERSION ?= v8.21.2

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

# The mandatory green gate (evidence, not assertion). Includes the race detector for the
# genuinely-concurrent surface: per-blueprint tick goroutines, the control-plane HTTP server,
# faro's POST fan-out, and the selfobs SDK goroutines.
gate: build vet test race rw-proto-check spdx-check forbidden-words

# Race detector over the whole module. Slower (and CGO-dependent), so it is kept out of the
# fast `test` target but is mandatory in `gate`.
#
# internal/integration is EXCLUDED from the race leg: it builds the full estate (every blueprint, one
# full cycle) and, with the race detector's shadow-memory overhead, peaks well past the 16 GB CI
# runner and OOM-reaps the job (SIGTERM/143). It still runs under the plain `test` target, and its
# only race-relevant code — the runner's concurrent per-blueprint tick fan-out — is race-tested
# directly by internal/runner (runner_parallel_test.go) under this same leg, so coverage is retained.
race:
	go test -race $$(go list ./... | grep -v '/internal/integration$$')

# --- OSS hygiene gate legs (in `gate`; CI runs them as a separate `hygiene` job) ---
# spdx-check: every tracked .go (except vendored *.pb.go) carries the AGPL-3.0-only SPDX header.
spdx-check:
	bash scripts/spdx-check.sh

# forbidden-words: content guard for customer/deployment identifiers + credential shapes. Real
# terms come from $$FORBIDDEN_WORDS_PATTERN (CI secret) or the gitignored scripts/forbidden-words.local;
# the credential-shape layer always runs. Self-skips cleanly where neither pattern source is present.
forbidden-words:
	bash scripts/forbidden-words.sh

# hygiene: the non-build gate legs CI runs as a dedicated job.
hygiene: spdx-check forbidden-words

# secret-scan: full-history secret scan via the pinned gitleaks docker image (.gitleaks.toml extends
# the default ruleset). Run in CI with a full-history checkout (fetch-depth: 0). Requires Docker.
secret-scan:
	docker run --rm -v "$(CURDIR):/repo" ghcr.io/gitleaks/gitleaks:$(GITLEAKS_VERSION) \
	  detect --source=/repo --redact --no-banner

# --- release-time artifacts (NOT gated; generated at publish, attached to the GitHub Release) ---
# notices: third-party dependency license inventory → THIRD_PARTY_NOTICES.md (see LICENSING.md).
notices:
	go run github.com/google/go-licenses@$(GO_LICENSES_VERSION) csv ./... > THIRD_PARTY_NOTICES.md

# sbom: SPDX + CycloneDX SBOMs → dist/sbom/ (attached to each GitHub Release).
sbom:
	mkdir -p dist/sbom
	go run github.com/anchore/syft/cmd/syft@$(SYFT_VERSION) scan dir:. \
	  -o spdx-json=dist/sbom/synthkit.spdx.json \
	  -o cyclonedx-json=dist/sbom/synthkit.cdx.json

# Env-surface drift guard (also runs inside `test`): every var read by Go is documented in
# .env.example, every docker-compose ${interpolation} is documented, no stale example keys, and the
# local .env (if present) provisions them all.
env-check:
	go test ./internal/config/ -run TestEnvSurfaceAligned -v

# Regenerate the blueprint-schema artifacts (BLUEPRINT-SCHEMA.md + the embedded
# fielddocs.json) from the live Go types. The TestSchemaCurrent gate (inside `test`) fails if
# they drift, so run this whenever a blueprint field or construct/workload config changes.
blueprint-schema:
	go run ./cmd/blueprint-schema

# Full series/label inventory for offline diff against signals/ (I32).
dump:
	DRY_RUN=true go run ./cmd/synthkit -once -dump

run:
	go run ./cmd/synthkit

docker:
	docker compose up -d --build

# Regenerate the cross-harness skill symlink farm (.claude/skills, .agents/skills, AGENTS.md)
# from the canonical source under plugins/synthkit/skills/.
skills-sync:
	./scripts/sync-skills.sh

# Verify the symlink farm matches the canonical source (fails on drift). Safe for CI.
skills-check:
	./scripts/sync-skills.sh --check

# Regenerate vendored RW2 protobuf Go types (requires protoc + protoc-gen-go on PATH).
# Install regen toolchain (one-time): go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
proto: ## regenerate vendored RW2 protobuf Go types (requires protoc + protoc-gen-go on PATH)
	protoc --go_out=. --go_opt=module=github.com/rknightion/synthkit \
	  --proto_path=internal/sink/promrw/writev2 \
	  internal/sink/promrw/writev2/types.proto

selfobs-dashboard: ## build + push the self-obs dashboard to GCX_CONTEXT, print its URL
	cd dashboards/internal && python3 build_selfobs_dashboard.py
	gcx --context $(GCX_CONTEXT) resources push -p dashboards/internal/synthkit-selfobs.json
	@echo "deployed: $${GC_SELF_GRAFANA_URL:-<set GC_SELF_GRAFANA_URL>}/d/synthkit-selfobs"

pyroscope-proto: ## regenerate vendored Pyroscope pprof + push protobuf Go types
	protoc --go_out=. --go_opt=module=github.com/rknightion/synthkit \
	  --proto_path=internal/pyroscope/pprofpb \
	  internal/pyroscope/pprofpb/profile.proto
	protoc --go_out=. --go_opt=module=github.com/rknightion/synthkit \
	  --proto_path=internal/sink/pyroscope/pushv1 \
	  internal/sink/pyroscope/pushv1/push.proto

# (network) fail if the LATEST upstream Prometheus release changed the RW2 proto vs our pinned copy.
# Compares original-file sha256 at the latest release against the pinned-tag original sha in PROVENANCE.md.
# Prints NETWORK ERROR (distinct from DRIFT) when GitHub is unreachable so offline failures are diagnosable.
rw-proto-check:
	@set -e; \
	pinned=$$(grep -m1 'UpstreamOriginalSHA256:' internal/sink/promrw/writev2/PROVENANCE.md | awk '{print $$NF}'); \
	pinned_tag=$$(grep -m1 'Tag:' internal/sink/promrw/writev2/PROVENANCE.md | awk '{print $$NF}'); \
	resp=$$(curl -fsSL "https://api.github.com/repos/prometheus/prometheus/releases/latest") || { echo "rw-proto-check: NETWORK ERROR reaching GitHub (not a drift failure)"; exit 1; }; \
	latest=$$(printf '%s' "$$resp" | sed -E -n 's/.*"tag_name": *"([^"]+)".*/\1/p' | head -n1); \
	[ -n "$$latest" ] || { echo "rw-proto-check: could not parse latest release tag from GitHub API"; exit 1; }; \
	tmp=$$(mktemp); \
	curl -fsSL "https://raw.githubusercontent.com/prometheus/prometheus/$$latest/prompb/io/prometheus/write/v2/types.proto" -o "$$tmp" || { echo "rw-proto-check: NETWORK ERROR fetching proto at $$latest (not a drift failure)"; exit 1; }; \
	got=$$(shasum -a 256 "$$tmp" | awk '{print $$1}'); rm -f "$$tmp"; \
	if [ "$$got" = "$$pinned" ]; then \
	  echo "rw-proto-check: OK — RW2 proto unchanged from pinned $$pinned_tag through latest $$latest"; \
	else \
	  echo "rw-proto-check: DRIFT — latest release $$latest RW2 proto sha $$got != pinned $$pinned_tag sha $$pinned"; \
	  echo "  Review https://github.com/prometheus/prometheus/blob/$$latest/prompb/io/prometheus/write/v2/types.proto and re-vendor (Task 0) if the change is relevant."; \
	  exit 1; \
	fi

ui-install: ## install control-UI npm deps (clean, lockfile-pinned)
	cd internal/control/ui && npm ci

ui: ui-install ## build the control-UI assets into internal/control/ui/dist
	cd internal/control/ui && npm run build

gate-ui: ui-install ## control-UI test + typecheck + build (separate from the Node-free Go gate)
	cd internal/control/ui && npm run test && npm run build

# --- CI abstraction (called identically by .forgejo/workflows + .github/workflows) ---
# ci-go is `gate` minus rw-proto-check (network → fails offline CI) and minus schema-gen.
ci-go: build vet test race
	@echo "ci-go: go gate passed"

ci-ui:
	cd internal/control/ui && npm ci && npm test && npm run build

ci-docker:
	docker build -t synthkit:ci .

# Docker-level e2e (build tag keeps it out of the normal `go test ./...` gate).
# Requires a docker-capable host. Builds the production image + receiver, runs the smoke blueprint.
# DOCKER_HOST is passed explicitly so testcontainers-go resolves the active Docker context
# (OrbStack / Docker Desktop / rootful) without relying on the rootless-socket heuristic.
e2e: ## docker-level e2e (testcontainers). Honors an existing DOCKER_HOST; else derives from the active docker context (local OrbStack/Docker Desktop).
	@DH="$${DOCKER_HOST:-$$(docker context inspect --format '{{.Endpoints.docker.Host}}' "$$(docker context show)" 2>/dev/null)}"; \
	  DOCKER_HOST="$$DH" go test -tags e2e -v -timeout 15m ./e2e/...

# Local "simulate full CI" umbrella.
ci: ci-go ci-ui ci-docker
