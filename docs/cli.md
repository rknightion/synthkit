---
title: CLI & Commands
description: Reference for every synthkit command, flag, verification mode, and make target.
---

# CLI & Commands

synthkit ships several binaries under `cmd/`. All configuration is environment-driven (read from `.env` or the process environment) unless flags override specific values. Build everything with `go build ./...`.

## synthkit — the generator

The main binary. Loads all blueprints from the directory named by `BLUEPRINTS` (default `./blueprints`), validates the set, and drives the two-cadence generator loop.

```bash
./synthkit [flags]
```

| Flag | Default | Description |
|---|---|---|
| `-once` | false | Run one full cycle and exit. |
| `-dump` | false | With `-once`: print the full series/label inventory to stdout (diff against `signals/`). |
| `-env <path>` | `.env` | Path to the `.env` file (optional; falls back to process environment). |

**Verification modes** (I32):

```bash
# Print the full inventory of distinct series names + label keys — push nothing.
DRY_RUN=true ./synthkit -once -dump

# One live cycle (DRY_RUN=false to push real data).
DRY_RUN=false ./synthkit -once

# The continuous loop (default).
./synthkit
```

`DRY_RUN` defaults to `true`. You must explicitly set `DRY_RUN=false` to push synthetic data to Grafana Cloud. See [Configuration](configuration.md) for the full environment variable reference, and [Credentials](credentials.md) for how the Grafana Cloud tokens are scoped.

The control plane is available at `http://<bind>:<port>/control/` (default port **8088**). See [control-plane.md](control-plane.md).

## sm-provision — Synthetic Monitoring provisioner

One-shot idempotent provisioner for Synthetic Monitoring. Reads blueprints, registers the offline private probe, and creates/updates SM checks in Grafana Cloud. Safe to re-run.

```bash
GC_SM_URL=https://synthetic-monitoring-api.grafana.net \
  GC_SM_TOKEN=<sm-bearer-token> \
  DRY_RUN=false \
  go run ./cmd/sm-provision
```

| Environment variable | Required | Description |
|---|---|---|
| `GC_SM_URL` | yes | SM API base URL |
| `GC_SM_TOKEN` | yes | SM API bearer token |
| `BLUEPRINTS` | no | Blueprint directory (default `./blueprints`) |
| `PROBE_NAME` | no | Offline probe name (default from `sm.DefaultProbeName`) |
| `PROBE_REGION` | no | Probe region string (default from `sm.DefaultProbeRegion`) |
| `DRY_RUN` | no | `true` (default) previews operations without calling the API |

`DRY_RUN=true` (the default) prints the planned operations without making any API calls. See [synthetic-monitoring.md](synthetic-monitoring.md) for the two-phase startup procedure.

## blueprint-schema — schema artifact generator

Regenerates the blueprint schema artifacts from the live Go types: `BLUEPRINT-SCHEMA.md` (the human reference) and `internal/blueprintschema/fielddocs.json` (the embedded field-description index used by the control-plane UI). Run this whenever a blueprint field or construct/workload config changes.

```bash
go run ./cmd/blueprint-schema
# or
make blueprint-schema
```

The gate test `TestSchemaCurrent` (run by `go test ./...`) fails if these artifacts drift from the live types. See [blueprint-reference.md](blueprint-reference.md).

## skcapture — environment snapshot tool

Inspects a Kubernetes environment via `kubectl` and writes a versioned, optionally age-encrypted inventory file for later processing by `skforge`.

```bash
skcapture [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--out <path>` | `capture.age` | Output file path. |
| `--passphrase-file <path>` | — | Path to a file containing the encryption passphrase. Required unless `--plain`. |
| `--plain` | false | Write unencrypted JSON. Mutually exclusive with `--passphrase-file`. |
| `--namespaces <list>` | (all) | Comma-separated namespace allow-list. |
| `--exclude-namespaces <list>` | `kube-system,kube-node-lease,kube-public` | Comma-separated namespace deny-list. |
| `--collectors <list>` | `k8s` | Comma-separated list of enabled collectors. |
| `--include-secret-data` | false | Read Secret data values (default: metadata only). |
| `--include-configmap-data` | false | Read ConfigMap data values (default: metadata only). |
| `--version` | — | Print tool version and schema version, then exit. |

`skcapture` imports only `internal/capture` and the Go standard library — it has no dependency on any blueprint, construct, or workload package. See [tools.md](tools.md) for the full capture-to-blueprint workflow.

## skforge — blueprint forge

Converts a captured inventory into a synthkit blueprint draft. Three subcommands:

```bash
skforge inspect <capture> --key <passphrase-file> [--plain]
skforge prompt  <capture> --key <passphrase-file> [--plain] [--report <path>]
skforge validate <blueprint.yaml>
```

| Subcommand | Description |
|---|---|
| `inspect` | Decrypt (or read plain) a capture file and print it as indented JSON. |
| `prompt` | Decrypt, map the deterministic skeleton, and emit a self-contained LLM prompt to stdout. Optionally write a coverage report to `--report`. |
| `validate` | Load a blueprint through the real registry + cardinality projection and print the result. Exits non-zero if invalid. |

| Flag | Applies to | Description |
|---|---|---|
| `--key <file>` | inspect, prompt | Path to the passphrase file. Required unless `--plain`. |
| `--plain` | inspect, prompt | Skip decryption; treat the file as plain JSON. |
| `--report <path>` | prompt | Write a coverage report to this path. |

See [tools.md](tools.md) for the full skcapture → skforge → blueprint workflow.

## synthkit-dash — dashboard generator

Generates Grafana v2 dashboards for a blueprint's synthetic telemetry. Resolves the blueprint, derives the signal manifest, runs registered templates, and writes dashboard JSON files.

```bash
go run ./cmd/synthkit-dash -blueprint <path> -out <dir> [flags]
```

| Flag | Required | Description |
|---|---|---|
| `-blueprint <path>` | yes | Path to the blueprint YAML. |
| `-out <dir>` | yes | Output directory for generated JSON files. |
| `-integrations <path>` | no | Optional integrations config YAML for deep-link index. |
| `-folder <uid>` | no | Grafana folder UID to place every dashboard in (must already exist). |

Always emits a thin index dashboard and a metrics dashboard. Per-blueprint templates produce additional dashboards when registered. Generated files are named `<dashboard-uid>.json`. Push and validate with `gcx`. See [tools.md](tools.md).

## synthkit-control-dash — control dashboard generator

Generates the customer self-serve control dashboard: an Infinity-datasource-backed Grafana v2 dashboard exposing the master volume multiplier and incident scenario controls as read panels with native action buttons.

```bash
go run ./cmd/synthkit-control-dash -ds-name <name> -out <dir> [flags]
```

| Flag | Required | Description |
|---|---|---|
| `-ds-name <name>` | yes | Infinity datasource name. |
| `-out <dir>` | yes | Output directory for generated JSON. |
| `-write-base-url <url>` | no | Absolute browser-reachable base URL for action-button POSTs (per-deploy; defaults to tailscale-serve endpoint). |
| `-blueprints <dir>` | no | Directory of `*.yaml` blueprints to enumerate scenarios from (default `./blueprints`). |

GET routes are open; POST routes use HTTP Basic auth so the browser handles the credential prompt natively — no token is embedded in the dashboard. See [tools.md](tools.md).

## make targets

| Target | Description |
|---|---|
| `make build` | `go build ./...` |
| `make test` | `go test ./...` |
| `make vet` | `go vet ./...` |
| `make gate` | Full mandatory gate: build + vet + test + race + `rw-proto-check` + `spdx-check` + `forbidden-words`. Run before every commit. |
| `make race` | Race-detector test run over the whole module. |
| `make blueprint-schema` | Regenerate schema artifacts from live Go types. See [blueprint-reference.md](blueprint-reference.md). |
| `make dump` | `DRY_RUN=true go run ./cmd/synthkit -once -dump` — full series/label inventory. |
| `make run` | `go run ./cmd/synthkit` |
| `make docker` | `docker compose up -d` — pulls `ghcr.io/rknightion/synthkit` and starts the stack. |
| `make docker-build` | `docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build` — build from source instead of pulling the published image. |
| `make skills-sync` | Regenerate the cross-harness skill symlink farm (`.claude/skills`, `.agents/skills`, `AGENTS.md`) from `plugins/synthkit/skills/`. |
| `make skills-check` | Verify the symlink farm matches the canonical source. Safe for CI. |
| `make proto` | Regenerate vendored RW2 protobuf Go types (requires `protoc` + `protoc-gen-go`). |
| `make pyroscope-proto` | Regenerate vendored Pyroscope pprof + push protobuf Go types. |
| `make rw-proto-check` | Detect upstream RW2 proto drift (network; in `gate`). |
| `make selfobs-dashboard` | Build and push the self-obs dashboard to `GCX_CONTEXT`. |
| `make ui` | Build the control-plane UI assets (runs `npm ci` + `npm run build`). |
| `make gate-ui` | Control-plane UI test + typecheck + build. |
| `make spdx-check` | Verify every `.go` file carries the AGPL-3.0-only SPDX header. |
| `make forbidden-words` | Content guard for customer/deployment identifiers + credential shapes. |
| `make hygiene` | `spdx-check` + `forbidden-words`. |
| `make secret-scan` | Full-history secret scan via gitleaks (requires Docker). |
| `make notices` | Generate `THIRD_PARTY_NOTICES.md` from dependency licenses. |
| `make sbom` | Generate SPDX + CycloneDX SBOMs into `dist/sbom/`. |
| `make e2e` | Docker-level end-to-end smoke test (requires Docker; `//go:build e2e`). |
| `make ci` | Local full-CI simulation: `ci-go` + `ci-ui` + `ci-docker`. |
| `make env-check` | Env-surface drift guard: verifies all Go-read vars are documented in `.env.example`. |
