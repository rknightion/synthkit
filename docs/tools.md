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
| `--version` | — | Print tool version and schema version, then exit. |

The capture output is a versioned JSON `Inventory` envelope containing resource kinds (nodes, namespaces, deployments, statefulsets, daemonsets, services, ingresses, addons). The schema version is embedded in the output.

The current v1 focus is AWS/EKS. The Inventory struct is designed to support additional collectors in future versions.

### What the zero-secret default covers

A capture leaves the cluster, so the boundary of the zero-secret default is worth stating exactly.

**Covered.** Secret and arbitrary ConfigMap values are never captured. The former `--include-secret-data` and `--include-configmap-data` flags were removed because they never implemented data capture, so no shipped RBAC promises that access. The default RBAC in `deploy/skcapture/rbac.yaml` grants no access to Secrets or ConfigMaps. The separate identity grant permits reading only one named ConfigMap's `cluster` and `self-reporting-metric.prom` keys, which provide collector identity and chart version rather than captured object data. Object annotations are reduced to a fixed allowlist of the keys the tooling consumes: Helm release identity, Argo CD tracking, deployment revision, and metric scrape hints. Every other annotation is dropped, including `kubectl.kubernetes.io/last-applied-configuration`, which on any cluster managed with `kubectl apply` embeds the object's full spec and therefore every container environment value.

**Not covered.** A capture is still a description of your environment. It carries namespace, workload, service and ingress names; container image references including the registry host; ingress hostnames; `ExternalName` service targets; pod-template labels; and node instance types and pool names. Treat the output as sensitive and use the encrypted path for anything leaving the cluster. A credential placed in a workload name, a label value or an image reference is captured, because those are identity fields the tool has to read to describe the environment.

### Cluster identity and where it came from

The captured cluster name is the primary join key for everything forged from a capture: get it wrong and the resulting blueprint emits telemetry that can never join to the real cluster's dashboards, while still loading and validating cleanly. `skcapture` therefore records which source produced the name in the cluster's `name_source` field, and prints it on completion.

| `name_source` | Meaning |
|---|---|
| `collector-release-info` | Read from the in-cluster metrics collector's release-info ConfigMap. This is the name the collector applies as a label to every metric, log and trace the cluster ships, so it is the only source guaranteed to join to that cluster's real telemetry. |
| `eks-arn-context` | Recovered from an EKS ARN kubeconfig context. The cluster's AWS identity, which is not necessarily the name its telemetry carries. |
| `kubeconfig-context` | A slug of whatever the current kubeconfig context happens to be called. Describes your kubeconfig, not the cluster. |
| `default` | Nothing was discoverable, and a placeholder was used. |

Resolution starts with the collector release-info ConfigMap, then uses the kubeconfig fallbacks. Anything other than `collector-release-info` prints a warning: verify the name against a live signal before forging a blueprint from that capture.

The collector lookup is a targeted `get` of a named ConfigMap in the collector's own namespace — never a namespace-wide or cluster-wide list — and reads only the two known release-info keys. The `cluster` key supplies the authoritative name. The `self-reporting-metric.prom` key is parsed only for the exact `grafana_kubernetes_monitoring_build_info{version="..."}` line and populates `monitoring.chart_version`; arbitrary metric lines and ConfigMap data are ignored. The default RBAC grants no ConfigMap access, so an in-cluster run under that role falls back and says so. To get the authoritative identity and chart version from an in-cluster run, customise `deploy/skcapture/rbac-collector-identity.yaml` with the observed namespace and exact release-info name before applying it; it grants only `get` on that one ConfigMap, never `list` or Secret access.

### Provider and platform detection

`cluster.provider` records the provider label family found on the nodes: `eks`, `gke`, or `aks`. When no supported family is present it is `undetermined`, so `skforge` can surface the missing evidence instead of treating the cluster as AWS by default. Karpenter-only EKS nodes are recognised from the `karpenter.k8s.aws/` label family even when they have no `eks.amazonaws.com/` labels.

Addon recognition combines the allowlisted Helm release name with known namespace and workload names. The capture currently recognises Crossplane (`crossplane-system`, `crossplane`), external-secrets (`external-secrets`), the GitHub Actions runner controller (`arc-systems`, `gha-rs-controller` and `gha-runner-scale-set*`), the GitHub-to-OTel bridge (`github2otel`), and OpenCost (`opencost`). These entries deliberately retain an empty addon kind when there is no standalone construct. Forge keeps one narrow image fallback for Crossplane provider workloads whose name and namespace are not recognised by capture. In the forge coverage report, Crossplane, external-secrets, the runner controller, and github2otel are `no matching construct` gaps. OpenCost is an `unmapped name`: its cost surface is modelled by the registered `k8s_cluster` construct's `k8s_monitoring.opencost` option. Karpenter's construct models node autoscaler telemetry, so it does not make the Actions runner controller a modeled product.

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
just dashgen \
  -blueprint blueprints/my-service.yaml \
  -out dashboards/my-service/ \
  -datasource metrics=grafanacloud-prom \
  -datasource logs=grafanacloud-logs \
  -datasource traces=grafanacloud-traces
```

| Flag | Required | Description |
|---|---|---|
| `-blueprint <path>` | yes | Path to the blueprint YAML. |
| `-out <dir>` | yes | Output directory for generated dashboard JSON files. |
| `-integrations <path>` | no | Optional integrations config YAML for thin-index deep-links. |
| `-folder <uid>` | no | UID for the generated Folder resource (defaults to `<blueprint>-dashboards`). |
| `-datasource <group=name>` | yes, repeated | Explicit datasource name for every rendered query group. Generation fails if any group is unmapped. |

`synthkit-dash` emits a Folder resource, a thin index dashboard, a metrics dashboard, and a panel inventory. Additional per-blueprint dashboards are generated when templates are registered for that blueprint. Files are written to the explicit output directory (never stdout). Every inventory row records its dashboard, panel, datasource, ref ID, and rendered query.

For blueprints that define recording/alert rules, `synthkit-dash` also emits a `<blueprint>-rules.json` file.

Push generated dashboards to Grafana with `gcx`:

```bash
gcx resources push -p dashboards/my-service/
```

After collecting one normalized observation per inventory query, classify every panel:

```bash
just dashgen -verify-inventory dashboards/my-service/panel-inventory.json \
  -observations observations.json \
  -verification-out verification.json
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

When `CONTROL_TOKEN` is set, the Infinity datasource uses secure Basic auth for protected reads;
browser-direct action POSTs use their own native Basic challenge. No token is embedded in the dashboard JSON.

## LLM-assisted skills

synthkit ships agent skills for Claude Code, Codex, and OpenCode. Skills are authored once under `plugins/synthkit/skills/`; `just skills-sync` creates symlinks in `.claude/skills/` and `.agents/skills/` so the same skills are available in all three harnesses. `just skills-check` verifies the symlink farm and is safe for CI.

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

Install synthkit as a Claude Code plugin from any directory. Enter these slash commands in Claude
Code chat; they are not shell commands:

```text
/plugin marketplace add rknightion/synthkit
/plugin install synthkit@synthkit
```

After installation the skills are available as `/synthkit:initial-setup`, `/synthkit:create-blueprint`, `/synthkit:setup-fleet-management`, and `/synthkit:verify-deployment`.

### Cross-harness compatibility

The same skills work in Codex (reads `.agents/skills/`) and OpenCode (reads `.claude/skills/`). Both directories are populated by `just skills-sync`. Install the tool on a new machine and run `just skills-sync` to get the skills available immediately.

For more on authoring custom blueprints, see [custom-blueprints.md](custom-blueprints.md). For Fleet Management setup, see [fleet-management.md](fleet-management.md). For the full CLI reference, see [cli.md](cli.md).

## Local documentation validation

The documentation hub generates its complete Zensical configuration externally, so a
fresh clone cannot run that private build locally. Validate the repository-owned
navigation, every relative Markdown/HTML link, and the intentional `404.md` page with:

```bash
just docs-check
```

The command uses only Python 3.11+ standard-library `tomllib`; it does not install
dependencies or contact the documentation hub. Run it before opening a documentation
change. The same check runs in `.github/workflows/trigger-docs-sync.yml` before the
cross-repository sync is dispatched.
