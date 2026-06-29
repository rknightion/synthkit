# synthkit runbook — credentials → telemetry in Grafana

This is the end-to-end path from a fresh checkout to **visible synthetic telemetry in Grafana
Cloud**: configure credentials, sanity-check offline, push live, and verify metrics / traces /
logs / RUM plus the control plane, Synthetic Monitoring, and Fleet Management registrations.

> Conventions: examples use the generic stack placeholders **`<customer-stack>`** (the synthetic-data
> destination) and **`<staff-stack>`** (the generator's own self-observability + profiling — a
> *separate* stack with its own credentials). Replace them with your `gcx` context names. Never
> commit real stack names, IDs, or tokens — secrets live only in the gitignored `.env`.

---

## 0. What success looks like

When the run is healthy you will see, in the **customer stack**:
- Mimir series for every declared construct (e.g. `aws_rds_cpuutilization_average`,
  `kube_node_info`, `pg_stat_statements_calls_total`), each carrying a `blueprint=<name>` selector
  on blueprint-scoped constructs.
- Tempo traces with a golden thread (`service.name=<workload>` → child DB span).
- Loki streams for the app log stream (`blueprint=<name>`, `source=app`).
- (optional) Faro/RUM beacons, Synthetic Monitoring check series, and Fleet Management collectors.

And locally/on the host: the **operator UI** at `/control/ui` with a green sink-readiness strip.

---

## 1. Prerequisites

- Go 1.26 (for local runs) or Docker (for the containerised deploy).
- `gcx` configured with a context for the customer stack (and optionally the staff stack). See the
  `gcx:setup-gcx` skill if it is not yet set up.
- The credential set for the customer stack. synthkit reads **three independent destinations**, each
  with its own token — never share `GC_TOKEN` across them:
  | Purpose | Env vars | Destination |
  |---|---|---|
  | Synthetic data (metrics+logs+traces) | `GC_TOKEN`, `GC_PROM_RW`(+`GC_PROM_USER`), `GC_OTLP_ENDPOINT`(+`GC_OTLP_USER`), `GC_LOKI`(+`GC_LOKI_USER`) | customer stack |
  | RUM (optional) | `GC_FARO_COLLECTOR`, `GC_FARO_APP_KEY` | customer stack |
  | Synthetic Monitoring provisioning (optional) | `GC_SM_URL`, `GC_SM_TOKEN` | customer stack tenant |
  | Fleet Management registration (optional) | `GC_FM_URL`, `GC_FM_STACK_ID`, `GC_FM_TOKEN` | customer stack |
  | Self-obs + profiling (optional) | `GC_SELF_OTLP_*`, `GC_PYROSCOPE_*` | **staff stack** (separate) |

  The exact endpoint shapes are documented inline in `.env.example`.

---

## 2. Configure `.env`

```bash
cp .env.example .env       # then fill the values
```

Minimum for a live synthetic push: `GC_TOKEN` + `GC_PROM_RW`/`GC_PROM_USER` +
`GC_OTLP_ENDPOINT`/`GC_OTLP_USER` + `GC_LOKI`/`GC_LOKI_USER`. Leave the optional blocks empty to
disable RUM / SM / FM / self-obs.

`DRY_RUN` defaults to **`true`** — a live push is always an explicit opt-in (`DRY_RUN=false`).
Keep comments on their own line (Docker `env_file` does not strip inline `value # comment`).

To enable Fleet Management collector registration, fill the `GC_FM_*` triplet **and** ensure a
blueprint declares a `fleet_management` construct (e.g. `blueprints/k8s-full-stack.yaml`). With the triplet
empty, those collectors still emit metrics — they just are not registered with the FM API.

---

## 3. Sanity-check offline (always do this first)

Confirm the blueprints load and the series inventory is what you expect, with **no network push**:

```bash
DRY_RUN=true go run ./cmd/synthkit -once -dump 2>&1 | less
```

Expected: a `loaded blueprint "<name>"` line per `blueprints/*.yaml`, a `synthkit up: N blueprints`
line, and `[dry-run promrw|loki|otlp]` summaries with example series/streams/spans. Spot-check a few
names against `signals/` — synthkit never invents names, so anything surprising is a bug.

---

## 4. Push live

Pick **one** path.

### 4a. Local foreground run

```bash
DRY_RUN=false go run ./cmd/synthkit
```

It binds the control plane on `127.0.0.1:8088` (loopback-safe). Open <http://127.0.0.1:8088/control/ui>.
Let it run for a few master ticks (default 5s) so cumulative series accumulate, then verify (§5).

### 4b. Containerised deploy (the standing host)

The committed `docker-compose.yml` is secret-free and reads everything via `env_file: .env`.
**First-time setup on a new host — create the state bind-mount directory and give it to the
container's user.** The image is distroless and runs as **uid 65532 (nonroot)**; the bind mount keeps
the state file directly inspectable/editable on the host, but the dir must be writable by 65532 or
every save fails (silently except for the surfaced error — see below):

```bash
# on the host clone (e.g. /opt/synthkit), ONCE:
mkdir -p control-state-data && sudo chown -R 65532:65532 control-state-data
```

Deploy = push the change, pull on the host, rebuild, and copy the (gitignored) `.env` across:

```bash
# on the host clone (e.g. /opt/synthkit):
git pull --ff-only && docker compose up -d --build
```

The host `.env` runs live (`DRY_RUN=false`) and binds `0.0.0.0:8088` **inside** the container so
Docker's port mapping can reach it; host exposure is restricted separately by `SYNTHKIT_BIND` in the
compose port mapping. Control state persists to the mounted `/data` volume
(`CONFIG_SNAPSHOT_PATH=/data/control-state.json`, set in compose); the bind mount must be a
**directory** owned by uid 65532 (distroless nonroot) — a single-file mount breaks the atomic save.

> **No `control-state.json` yet?** That's normal until the first control-plane change — the snapshot
> is written lazily on the first mutation, not at startup. If a change you make in the operator UI
> doesn't stick across a restart, check `persist.last_error` in `/control/status` (§5.1): a
> `permission denied` there means the bind-mount dir isn't owned by uid 65532 — run the `chown` above.
> To wipe state, just delete the file (or the dir's contents) on the host.

---

## 5. Verify in Grafana

### 5.1 Sink readiness (fastest signal)

```bash
curl -s http://127.0.0.1:8088/control/status | jq
```

Each sink shows `last_success_ms` advancing and `failures: 0`. `dry_run: true` means you are not
actually pushing — re-check `DRY_RUN`. This strip is also rendered in the operator UI.

### 5.2 Metrics (Mimir)

```bash
gcx --context <customer-stack> metrics query 'count by (blueprint) ({__name__=~"aws_rds_.+"})'
gcx --context <customer-stack> metrics query 'kube_node_info'
gcx --context <customer-stack> metrics query 'pg_stat_statements_calls_total'
```

Expect one series group per blueprint that declares the construct. (Use the `gcx:explore-datasources`
skill to browse what landed.)

### 5.3 Traces (Tempo) — the golden thread

In Explore → Tempo (customer stack), search `service.name="<your-service>"` (or your workload) and
confirm a trace whose root request span has a **child DB span** to the declared database. The
RED metrics derived from spans appear as `traces_spanmetrics_*{blueprint=<name>}` in Mimir.

### 5.4 Logs (Loki)

```bash
gcx --context <your-stack> logs query '{blueprint="<blueprint-name>", source="app"} | json'
```

Expect structured app log lines (route, status, latency). High-cardinality fields are JSON payload
fields, never stream labels.

### 5.5 Synthetic Monitoring (if `GC_SM_*` set)

SM checks are **provisioned offline** by a one-shot command (not the emitter):

```bash
go run ./cmd/sm-provision      # registers the offline probe + checks
```

Then the SM app populates and `probe_*` series appear in Mimir (`job=<check>`); no real probe
execution occurs.

### 5.6 Fleet Management (if `GC_FM_*` set)

With the triplet configured and a `fleet_management` blueprint, the runner registers each collector
with the FM connect API at startup and heartbeats it every 45s. Open the Fleet Management app on the
customer stack and confirm the fake collectors (linux/windows/darwin per the blueprint's
`collectors_per_os`) appear; their `collector_id`/`os`/`cluster` attributes match the
`alloy_*` metrics the construct emits. The process logs `fleet: register …` per collector
(or, in `DRY_RUN`, logs the call without hitting the API).

### 5.7 Self-observability (if `SELFOBS_ENABLED=true`)

The generator's *own* telemetry ships to the **staff stack**: `service.name=synthkit`, metrics
`synthkit.*` (push/tick/ledger.size/volume.multiplier/blueprint.count), per-tick traces, the
operational log stream, and continuous profiles (`app=synthkit`). This is a separate data path from
the synthetic telemetry above and never uses `GC_TOKEN`.

---

## 6. Operate (control plane)

The operator UI (`/control/ui`) drives the live runtime without a restart: master volume multiplier,
per-blueprint incident scenarios, ad-hoc failure injection, live service/node scaling, and
per-construct / per-kind / per-blueprint enable toggles. Mutations are gated by HTTP Basic auth when
`CONTROL_TOKEN` is set — username `control`, password = `CONTROL_TOKEN` (empty = unauthenticated,
acceptable only on loopback or an off-network host mapping). GETs are always open. In the browser the
first mutation triggers Chrome's native credential dialog; the Grafana Infinity datasource (e.g. the
customer dashboard) authenticates with `basicAuthUser: control` + `basicAuthPassword: <CONTROL_TOKEN>`.
State persists across restarts via the snapshot file.

**Security notes for shared-use deployments.** Set `CONTROL_TOKEN` whenever the bind address is
non-loopback (the startup log warns if it is not set and the bind is not `127.0.0.1`). The UI
only ever stores or displays the *env-var name* for git blueprint source tokens (e.g.
`MY_GIT_TOKEN`) — it never transmits the value over the wire — but the *resolved token value*
is written into the control-state snapshot (`control-state.json` at `BLUEPRINT_DATA_DIR`). Treat
that file as a secret: restrict filesystem permissions on the host, and do not include it in
backups that land in less-trusted storage.

---

## 7. Teardown

- Local: Ctrl-C (graceful drain, bounded).
- Container: `docker compose down` (state survives in the `/data` bind mount).
- To stop emitting a blueprint without redeploying: disable it from the control UI, or delete its
  `blueprints/*.yaml` and restart — removing a blueprint affects nothing else.

---

## 8. First-value smoke checklist

- [ ] `.env` filled; `DRY_RUN=true … -once -dump` inventory matches `signals/`.
- [ ] `DRY_RUN=false` run; `/control/status` shows every sink `last_success` advancing, `failures: 0`.
- [ ] Mimir: per-blueprint series present.
- [ ] Tempo: golden-thread trace (service → DB) present.
- [ ] Loki: app log stream present.
- [ ] (optional) SM checks, FM collectors, RUM beacons, self-obs on the staff stack.
