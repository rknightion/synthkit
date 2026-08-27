---
title: CLI & Commands
description: Reference for every synthkit command, flag, verification mode, and make target.
---

# CLI & Commands

synthkit ships several binaries under `cmd/`. All configuration is environment-driven (read from `.env` or the process environment) unless flags override specific values. Build everything with `go build ./...`.

## synthkit — the generator

The main binary. Scans the directory named by `BLUEPRINTS` (default `./blueprints`) and loads only
the runtime identities selected by `BLUEPRINT_NAMES`. Empty/unset starts setup mode; `*` explicitly
loads the complete catalog.

```bash
./synthkit [flags]
```

| Flag | Default | Description |
|---|---|---|
| `-once` | false | Run one full cycle and exit. |
| `-dump` | false | With `-once`: print the full series/label inventory to stdout (diff against `signals/`). |
| `-preflight` | false | Validate and probe mandatory live Grafana endpoints, then exit with redacted lane/reason output. |
| `-healthcheck` | false | Exit successfully only when the local control plane reports delivery readiness; used by Compose. |
| `-version` | false | Print `{"version":"X.Y.Z","revision":"<40-hex>"}` as JSON, then exit. Local builds report `dev`/`unknown` unless stamped. |
| `-env <path>` | `.env` | Path to the `.env` file (optional; falls back to process environment). |

**Verification modes** (I32):

```bash
# Print the full inventory of distinct series names + label keys — push nothing.
DRY_RUN=true BLUEPRINT_NAMES=otlp-native ./synthkit -once -dump

# One live cycle (DRY_RUN=false to push real data).
DRY_RUN=false BLUEPRINT_NAMES=otlp-native ./synthkit -once

# The continuous loop (default).
./synthkit
```

`DRY_RUN` defaults to `true`. You must explicitly set `DRY_RUN=false` to push synthetic data to Grafana Cloud. See [Configuration](configuration.md) for the full environment variable reference, and [Credentials](credentials.md) for how the Grafana Cloud tokens are scoped.

**What `-dump` output is deterministic, and what isn't.** The authoritative section of a
`-dump` — each sink's `<name> {[sorted label/attribute keys]}` inventory block — is the
structural contract: series names, label/attribute key sets, and (for sigil) the ingest-kind →
operation-name mapping. That block is byte-identical across consecutive runs and is what a
diff against `signals/` should compare.

The `[dry-run <sink>] N series e.g. <series>` lines underneath it are NOT part of that
contract — each one logs a single randomly-sampled exemplar per batch, so which exemplar gets
printed varies run to run by design. Never `diff` two raw `-dump` outputs line-for-line; it will
show noise from this sampling (and, for sigil, from genuinely fresh per-run correlation IDs — see
below) and prove nothing about a regression.

For the same reason, the sigil block's final summary line —
`== sigil: generations=N workflow_steps=N scores=N ==` — is a live COUNT, not part of the
structural contract, and it is expected to vary run to run: `internal/ledger` mints a fresh,
cryptographically-random `SessionID`/correlation ID per conversation on every run (by design —
correlation ids must be unique and unguessable, never derived from a seed unit). Per-conversation
turn count is a deterministic hash of that id (`internal/workload/aiagent/minter.go`'s
`TurnCount`), so a fresh id set naturally reshuffles the aggregate turn-derived counts
(`generations`, `scores`) even though the *number of conversations minted per tick* is itself
fully deterministic (fixed `sessions_per_min` config × the shape engine's fixed-seed PRNG). Do
not treat a `generations=`/`scores=` count mismatch across two `-once -dump` runs as a
determinism regression; treat a mismatch in the structural inventory block as one.

The control plane is available at `http://<bind>:<port>/control/` (default port **8088**). See [control-plane.md](control-plane.md).

## synthkit-deploy.py — deployment identity and rollback helper

The stdlib-only helper emits closed, secret-safe JSON reports. It never prints `.env` values or
private state content.

| Subcommand | Mutation boundary |
|---|---|
| `resolve-image` | Read-only selector precedence and mutable-tag report. |
| `set-image` | Compare-and-swap only `SYNTHKIT_IMAGE_REF`; preserves unrelated bytes and aborts on drift. Mutable `main`/`latest` needs `--allow-mutable`. |
| `snapshot-state` | Requires the named container stopped; creates an integrity-manifested external private snapshot. |
| `restore-state` | Requires the container stopped; verifies the snapshot, atomically replaces the validated state target, and retains displaced state. |
| `write-record` | Writes one external mode-0600 closed identity record. |
| `verify-image` | Read-only exact index/platform/config/binary/signature/provenance verification. |
| `check-compose` | Read-only minimum-version and default/profile rendering check with a fake env file. |
| `inspect-running` | Read-only cross-check of configured index, platform/config/image ID, health, binary version, and revision against expected values. |

See [Deployment](deployment.md#reproducible-upgrade) for the ordered upgrade and rollback commands.

## sm-provision — Synthetic Monitoring provisioner

One-shot snapshot-bound provisioner for Synthetic Monitoring. The published image contains both
the emitter and provisioner at the same source version. Preview is always the default.

```bash
docker compose --profile sm-provision run --rm sm-provision
SM_PROVISION_APPLY=true docker compose --profile sm-provision run --rm sm-provision
docker compose restart synthkit
```

| Environment variable | Required | Description |
|---|---|---|
| `GC_SM_URL` | yes | SM API base URL |
| `GC_SM_TOKEN` | yes | SM API bearer token |
| `CONFIG_SNAPSHOT_PATH` | no | Emitter control-state path; Compose sets `/data/control-state.json` |
| `SM_PROVISION_APPLY` | no | Exact `true` enables mutations; absent/`false` previews |
| `SM_PROVISION_ADOPT_LEGACY` | no | Exact `true` records the exact-match plan during preview and allows only that same plan during apply |
| `SM_PROVISION_MIGRATE_TARGET` | no | Exact `true` previews or applies a credential/endpoint target migration; apply also requires `SM_PROVISION_APPLY=true` and a matching preview no older than 15 minutes |

`DRY_RUN` does not control this command. Source checkouts may use `go run ./cmd/sm-provision`, but
the Compose profile is the supported Docker-only deployment route. See
[synthetic-monitoring.md](synthetic-monitoring.md) for ownership and crash-recovery rules.

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

When `CONTROL_TOKEN` is set, protected GETs use the Infinity datasource's secure Basic auth and
browser-direct POSTs use the browser's separate Basic challenge. No token is embedded in the dashboard.

## make targets

| Target | Description |
|---|---|
| `make build` | `go build ./...` |
| `make test` | `go test ./...` |
| `make deploy-tests` | Focused deployment-helper safety and identity tests. |
| `make vet` | `go vet ./...` |
| `make gate` | Full mandatory gate: build + vet + test + race + `rw-proto-check` + `spdx-check` + `forbidden-words`. Run before every commit. |
| `make race` | Race-detector test run over the whole module. |
| `make blueprint-schema` | Regenerate schema artifacts from live Go types. See [blueprint-reference.md](blueprint-reference.md). |
| `make dump` | `DRY_RUN=true BLUEPRINT_NAMES='*' go run ./cmd/synthkit -once -dump` — explicit full-catalog series/label inventory. |
| `make run` | `go run ./cmd/synthkit` |
| `make docker` | `docker compose up -d --wait` — pulls the selected `SYNTHKIT_IMAGE_REF` and waits for readiness. |
| `make docker-build` | `docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build` — build from source instead of pulling the published image. |
| `make compose-check` | Validate Compose 2.24.4+, the default/profile render, and exact image selection using `.env.example`. |
| `make skills-sync` | Regenerate the cross-harness skill symlink farm (`.claude/skills`, `.agents/skills`) from `plugins/synthkit/skills/`; `AGENTS.md` remains the canonical repository guidance. |
| `make skills-check` | Verify the symlink farm matches the canonical source. Safe for CI. |
| `make proto` | Regenerate vendored RW2 protobuf Go types (requires `protoc` + `protoc-gen-go`). |
| `make pyroscope-proto` | Regenerate vendored Pyroscope pprof + push protobuf Go types. |
| `make rw-proto-check` | Detect upstream RW2 proto drift (network; in `gate`). |
| `make selfobs-dashboard` | Build and push the self-obs dashboard to `GCX_CONTEXT`. |
| `GCX_CONTEXT=<operator-selected-context> make signal-fidelity-eks-readback` | Read EKS and core non-AI CloudWatch metric shapes through read-only gcx calls and cumulative-merge generic EKS corpus documents. |
| `make ui` | Build the control-plane UI assets (runs `npm ci` + `npm run build`). |
| `make gate-ui` | Control-plane UI test + typecheck + build. |
| `make spdx-check` | Verify every `.go` file carries the AGPL-3.0-only SPDX header. |
| `make forbidden-words` | Content guard for customer/deployment identifiers + credential shapes. |
| `make hygiene` | `spdx-check` + `forbidden-words`. |
| `make secret-scan` | Full-history secret scan via gitleaks (requires Docker). |
| `make notices` | Generate `THIRD_PARTY_NOTICES.md` from dependency licenses. |
| `make sbom` | Generate SPDX + CycloneDX SBOMs into `dist/sbom/`. |
| `make e2e` | Docker-level end-to-end smoke test (requires Docker; `//go:build e2e`). |
| `make published-e2e` | Exercise an exact published digest through committed Compose with writable state, health, and positive fake-sink receipts. Requires expected image/version/revision env vars. |
| `make ci` | Local full-CI simulation: `ci-go` + `ci-ui` + `ci-docker`. |
| `make env-check` | Env-surface drift guard: verifies all Go-read vars are documented in `.env.example`. |
