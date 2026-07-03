# synthkit

[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/rknightion/synthkit/badge)](https://scorecard.dev/viewer/?uri=github.com/rknightion/synthkit)

Composable synthetic-telemetry generator for Grafana Cloud. Declare the infrastructure and
applications you want — in one YAML **blueprint** — and synthkit emits structurally-correct
synthetic metrics, traces, and logs (plus optional Faro/RUM) with the REAL metric/label/field
names of each technology it models: EKS + the k8s-monitoring substrate, EC2/ALB/NAT-gateway/EBS/
S3 CloudWatch families, RDS/ElastiCache, Database Observability (MySQL/Postgres), Azure/GCP CSP,
Cloudflare, Synthetic Monitoring, Fleet Management, and correlated request workloads — `web_service`
(a single service) and `app` (a declared multi-service GRAPH whose nodes emit custom metrics/logs/
spans via a telemetry DSL) — sharing one end-to-end correlation ID per request across the graph.

- **Catalog**: isolated construct/workload modules, each with a verified signal contract
  ([SIGNALS.md](./SIGNALS.md)).
- **Composition**: a blueprint is one deletable YAML file; constructs know nothing about it.
  See [ARCHITECTURE.md](./ARCHITECTURE.md).

## Quick start

```bash
go build ./cmd/synthkit

# Dry run: print the full series/label inventory, push nothing
DRY_RUN=true ./synthkit -once -dump

# Live: set credentials, then run the loop
cp .env.example .env   # fill GC_TOKEN (+ GC_PROM_RW / GC_OTLP_ENDPOINT / GC_LOKI)
./synthkit
```

## LLM-assisted setup (Claude Code / Codex / OpenCode)

synthkit ships agent skills for deployment and operation. In **Claude Code**, open this repo and run
`/initial-setup` (also `/verify-deployment`, `/create-blueprint`,
`/setup-fleet-management`). Or install as a plugin from anywhere:

    /plugin marketplace add rknightion/synthkit
    /plugin install synthkit@synthkit      # → /synthkit:initial-setup, etc.

The same skills work in **Codex** (`.agents/skills/`) and **OpenCode** (reads `.claude/skills/`).
Skills are authored once under `plugins/synthkit/skills/`; `.claude/skills` and `.agents/skills` are
symlinks kept in sync by `make skills-sync` (verified by `make skills-check`).

## Synthetic Monitoring — two-phase startup

Blueprints with `synthetic_monitoring` blocks require a one-shot control-plane step to register
the offline private probe and the checks in Grafana Cloud before the data-plane emitter produces
useful output. Run the provisioner once per environment (idempotent — safe to re-run):

```bash
# Phase 1: provision the offline probe + SM checks (idempotent, one-shot)
GC_SM_URL=https://synthetic-monitoring-api.grafana.net \
  GC_SM_TOKEN=<sm-bearer-token> \
  DRY_RUN=false \
  go run ./cmd/sm-provision

# Phase 2: run the generator normally
./synthkit
```

`DRY_RUN=true` (the default) previews the planned operations without making any API calls.
The probe name, region, and Frankfurt coordinates are shared constants in `internal/construct/sm`
so data-plane and control-plane cannot drift.

## Authoring a blueprint

Copy `blueprints/k8s-minimal.yaml`, rename, declare your environments/cluster/databases/workloads, and
enable it. The schema is documented in ARCHITECTURE.md §3; unknown constructs or fields fail
loudly at load. Deleting your blueprint file removes its telemetry and affects nothing else.

```yaml
name: mine
environments:
  - name: prod
    cloud:   { provider: aws, account_id: "210987654321", region: eu-west-1, vpc_id: vpc-0mine01 }
    cluster: { type: eks, name: mine-prod-euw1, addons: [core_dns, ebs_csi] }
    databases: [{ engine: postgres, version: "16.2", name: mine-db, observability: { mode: dbo11y } }]
workloads:
  - { type: web_service, name: mine-api, runs_on: mine-prod-euw1,
      traffic: { off_peak_rps: 5, peak_rps: 40 },
      endpoints: [{ route: "GET /v1/ping", error_rate: 0.01, p95_ms: 80 }] }
```

## Incident scenarios

Declare named, reusable failure bundles in a blueprint, then fire them on a schedule or live via
the control plane. Each effect names a mode, an optional target (instance name, `<axis>:*` wildcard,
or omitted for a single-axis mode), and an intensity in [0,1].

```yaml
scenarios:
  - name: db-pressure
    title: "Database under load"
    summary: "Connection saturation + slow queries hitting the production DB"
    effects:
      - { mode: connection_saturation, target: mine-db, intensity: 0.7 }
      - { mode: slow_query_storm,      target: mine-db, intensity: 0.5 }

incidents:
  # Schedule the whole scenario:
  - { scenario: db-pressure, at: "2026-06-19T14:00", for: 30m }
  # Or fire a single mode directly:
  - { kind: oom_kill, target: mine-prod-euw1, at: "2026-06-20T10:00", for: 10m, intensity: 0.6 }
```

Scenarios can also be activated or deactivated live without a restart — see **Control plane** below.

## Control plane

The control plane (`GET /control/ui`, `GET /control/schema`, `GET /control/state`) is available
by default. Mutation routes require `CONTROL_TOKEN` when set.

| Route | Method | Description |
|---|---|---|
| `/control/schema` | GET | Blueprint-derived schema: all modes, addressable targets, scenarios, live scaling state. |
| `/control/state` | GET | Current control snapshot (volume multiplier, active scenarios, failures, scaling). |
| `/control/scenarios` | POST | Activate/deactivate scenarios by `blueprint/name`. Validated against derived schema. |
| `/control/scaling` | POST | Set live workload pod counts (within blueprint-declared bounds). Node count cascades automatically — k8scluster and ec2 agree via the shared `fixture.DeriveNodes` call. |
| `/control/failures` | POST | Ad-hoc `{mode → {enabled, intensity, scope}}` override (escape hatch; unknown modes warned, not rejected). |
| `/control/load` | POST | Master volume multiplier — scales all synthetic volume coherently. |

**Example — activate a scenario:**

```bash
curl -s -X POST http://localhost:9090/control/scenarios \
  -H "Content-Type: application/json" \
  -d '{"active_scenarios": ["mine/db-pressure"]}'
```

**Example — scale workload pods live:**

```bash
curl -s -X POST http://localhost:9090/control/scaling \
  -H "Content-Type: application/json" \
  -d '{"mine-api": 8}'
# Node count cascades: k8scluster + ec2 both re-derive via fixture.DeriveNodes.
# Scale-down retires old pod/node series automatically (state.DropWhere).
```

## Status

v1 catalog + composition + platform complete and green (`go build ./... && go vet ./... && go test ./...`):
21 construct kinds + the `web_service` and `app` workloads, the blueprint loader/resolver, the two-cadence runner,
the control plane + operator UI, the Infinity JSON host, and the SM provisioner. The signal contracts are
lifted from a proven production-shaped generator with full provenance citations ([SIGNALS.md](./SIGNALS.md)).
Open items are tracked in [cantfind.md](./cantfind.md). The live-push end-to-end path is validated
(metrics, traces, logs, and Fleet collectors confirmed in Grafana Cloud) — see
[docs/RUNBOOK.md](./docs/RUNBOOK.md) for the credentials→telemetry runbook.

## License

`synthkit` is licensed under the GNU Affero General Public License v3.0 only (`AGPL-3.0-only`).
See [LICENSE](./LICENSE) and [LICENSING.md](./LICENSING.md). Every Go source file carries an
`SPDX-License-Identifier: AGPL-3.0-only` header.
