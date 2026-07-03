---
title: Capture & Tooling
description: skcapture, skforge, dashboard generators, and LLM-assisted skills for blueprint authoring and deployment.
---

# Capture & Tooling

synthkit ships several supporting tools for capturing real environments, generating blueprint drafts, generating Grafana dashboards, and LLM-assisted workflow skills.

## skcapture — environment snapshot

`skcapture` inspects a live Kubernetes environment via `kubectl` and writes a versioned, optionally age-encrypted inventory file. It has zero synthkit imports — no blueprint, construct, or runner code. This trust boundary is enforced by `internal/capture.TestCaptureTrustBoundary`.

**Typical use**: run `skcapture` inside the target cluster (as a kubectl-shell container or a one-shot Job), encrypt the output, and retrieve it for processing with `skforge` outside the cluster.

```bash
# Encrypted output (recommended for production clusters)
skcapture \
  --passphrase-file /path/to/passphrase \
  --out capture.age

# Plain JSON (for local development or non-sensitive environments)
skcapture --plain --out capture.json

# Restrict to specific namespaces
skcapture --plain \
  --namespaces my-app,my-services \
  --out capture.json
```

Key flags:

| Flag | Default | Description |
|---|---|---|
| `--out <path>` | `capture.age` | Output file path. |
| `--passphrase-file <path>` | — | Path to a file containing the encryption passphrase. Required unless `--plain`. |
| `--plain` | false | Write unencrypted JSON. Mutually exclusive with `--passphrase-file`. |
| `--namespaces <list>` | (all) | Comma-separated namespace allow-list. |
| `--exclude-namespaces <list>` | `kube-system,kube-node-lease,kube-public` | Comma-separated namespace deny-list. |
| `--collectors <list>` | `k8s` | Comma-separated list of enabled collectors (currently: `k8s`). |
| `--include-secret-data` | false | Read Secret data values (metadata-only by default). |
| `--include-configmap-data` | false | Read ConfigMap data values (metadata-only by default). |
| `--version` | — | Print tool version and schema version, then exit. |

The capture output is a versioned JSON `Inventory` envelope containing resource kinds (nodes, namespaces, deployments, statefulsets, daemonsets, services, ingresses, addons). The schema version is embedded in the output.

The current v1 focus is AWS/EKS. The Inventory struct is designed to support additional collectors in future versions.

## skforge — blueprint forge

`skforge` takes a captured inventory and produces a synthkit blueprint draft. It uses a deterministic skeleton mapper to translate inventory resources into blueprint declarations, then emits a self-contained LLM prompt that you feed to Claude (or another LLM) to produce the final blueprint YAML.

```bash
# Inspect a capture file
skforge inspect capture.age --key /path/to/passphrase

# Generate an LLM prompt + optional coverage report
skforge prompt capture.age \
  --key /path/to/passphrase \
  --report coverage.md > blueprint-prompt.txt

# Validate a blueprint draft
skforge validate my-blueprint.yaml
```

### Subcommands

**`inspect`** — decrypt (or read plain) a capture file and print it as indented JSON for inspection.

**`prompt`** — the main workflow step. Decrypts the capture, runs the deterministic skeleton mapper, and emits a self-contained LLM prompt to stdout. The prompt includes the captured inventory summary, a description of each construct kind available in the catalog, and instructions for the LLM to produce a valid blueprint YAML. Pass `--report <path>` to also write a coverage report showing which resources mapped to which construct kinds and which were not covered.

**`validate`** — load a blueprint file through the real registry and cardinality projection. Prints `OK`, `Name`, `Cardinality`, `Estimated`, and any `Diagnostics`. Exits non-zero if the blueprint is invalid. Useful for confirming an LLM-generated draft is structurally correct before running synthkit.

```text
OK:          true
Name:        my-service
Cardinality: 847
Estimated:   true
```

### The capture → forge → blueprint workflow

1. **Capture**: run `skcapture` in the target environment to produce an inventory file.
2. **Forge prompt**: run `skforge prompt` to generate a self-contained LLM prompt.
3. **LLM authoring**: feed the prompt to Claude (or another LLM). The prompt is self-contained — it includes the full construct catalog description so the LLM can work without any other context.
4. **Validate**: run `skforge validate` on the resulting `blueprint.yaml` to confirm it loads cleanly.
5. **Deploy**: place the validated blueprint in `blueprints/` and run synthkit.

See [custom-blueprints.md](custom-blueprints.md) for how to add custom blueprints at runtime.

## synthkit-dash — Grafana dashboard generator

`synthkit-dash` generates Grafana v2 dashboard JSON for a blueprint's synthetic telemetry. It resolves the blueprint, derives the signal manifest (via `internal/dashgen`), runs registered per-blueprint templates, and writes dashboard JSON files to an output directory.

```bash
go run ./cmd/synthkit-dash \
  -blueprint blueprints/my-service.yaml \
  -out dashboards/my-service/
```

| Flag | Required | Description |
|---|---|---|
| `-blueprint <path>` | yes | Path to the blueprint YAML. |
| `-out <dir>` | yes | Output directory for generated dashboard JSON files. |
| `-integrations <path>` | no | Optional integrations config YAML for thin-index deep-links. |
| `-folder <uid>` | no | Grafana folder UID to place every dashboard in. The folder must already exist in Grafana. |

`synthkit-dash` always emits a thin index dashboard and a metrics dashboard. Additional per-blueprint dashboards are generated when templates are registered for that blueprint. Files are named `<dashboard-uid>.json` and written to explicit `--out` paths (never stdout).

For blueprints that define recording/alert rules, `synthkit-dash` also emits a `<blueprint>-rules.json` file.

Push generated dashboards to Grafana with `gcx`:

```bash
gcx resources push -p dashboards/my-service/
```

## synthkit-control-dash — control dashboard generator

`synthkit-control-dash` generates the customer-facing self-serve control dashboard: an Infinity-datasource-backed Grafana v2 dashboard with volume and scenario knobs as read panels and native action buttons.

```bash
go run ./cmd/synthkit-control-dash \
  -ds-name "My Infinity DS" \
  -out dashboards/control/
```

| Flag | Required | Description |
|---|---|---|
| `-ds-name <name>` | yes | Infinity datasource name in Grafana. |
| `-out <dir>` | yes | Output directory for generated JSON. |
| `-write-base-url <url>` | no | Absolute browser-reachable base URL for action-button POSTs. Override per deployment (default is the tailscale-serve endpoint pattern). |
| `-blueprints <dir>` | no | Directory of blueprint YAML files for enumerating scenarios (default `./blueprints`). |

GET routes on the control plane are open. POST routes use HTTP Basic auth — the browser handles the credential prompt natively. No token is embedded in the dashboard JSON.

## LLM-assisted skills

synthkit ships agent skills for Claude Code, Codex, and OpenCode. Skills are authored once under `plugins/synthkit/skills/`; `make skills-sync` creates symlinks in `.claude/skills/` and `.agents/skills/` so the same skills are available in all three harnesses. `make skills-check` verifies the symlink farm and is safe for CI.

### Available skills

| Skill | Description |
|---|---|
| `/initial-setup` | Walks through credentials, environment variables, and first-run verification. Start here when deploying synthkit for the first time. |
| `/create-blueprint` | Guided blueprint authoring — asks about your infrastructure and applications and produces a blueprint YAML. |
| `/setup-fleet-management` | Configures Grafana Fleet Management collector registration for a blueprint. |
| `/verify-deployment` | End-to-end deployment verification — checks credentials, runs `-once -dump`, and confirms telemetry is reaching Grafana Cloud. |

### Using skills in Claude Code

Open the synthkit repository in Claude Code and run a skill directly:

```text
/initial-setup
/create-blueprint
/verify-deployment
```

### Using skills from outside the repo (plugin install)

Install synthkit as a Claude Code plugin from any directory:

```text
/plugin marketplace add rknightion/synthkit
/plugin install synthkit@synthkit
```

After installation the skills are available as `/synthkit:initial-setup`, `/synthkit:create-blueprint`, `/synthkit:setup-fleet-management`, and `/synthkit:verify-deployment`.

### Cross-harness compatibility

The same skills work in Codex (reads `.agents/skills/`) and OpenCode (reads `.claude/skills/`). Both directories are populated by `make skills-sync`. Install the tool on a new machine and run `make skills-sync` to get the skills available immediately.

For more on authoring custom blueprints, see [custom-blueprints.md](custom-blueprints.md). For Fleet Management setup, see [fleet-management.md](fleet-management.md). For the full CLI reference, see [cli.md](cli.md).
