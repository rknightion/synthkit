# dashboards/examples — synthetic-telemetry dashboards (target stack)

Dashboards that visualise the **synthetic telemetry synthkit emits** (the modelled estate).
Synthetic telemetry goes to the **target stack** using `GC_TOKEN`; publishing dashboards requires
separate Grafana permissions. This is the counterpart to
[`../internal/`](../internal), which monitors the generator process itself on a separate staff stack.

These dashboards are **generated, never hand-edited.** The generator (`cmd/synthkit-dash`) resolves a
blueprint, derives the *actual emitted signal surface* by running one dry cycle through the runner
(the same `-once -dump` path), and renders per-blueprint Grafana **v2** dashboards (GA schema
`dashboard.grafana.app/v2`, via the Grafana Foundation SDK). Nothing ships by default — each blueprint
composes its own cross-construct views.

## Generate → validate → push → verify

For a Docker-only workstation, use the
[container generation path](../../docs/tools.md#synthkit-dash-grafana-dashboard-generator).
It selects the current stable image from `.env.example` and writes files through an output mount.
That image includes the generator and native HTTP template; an older image's bundled templates
can differ from the current checkout. Generation needs no tenant credentials and does not publish
a dashboard.

The following source-development and automation path additionally uses Go, `gcx` and `jq`.
Neither these tools nor a generated dashboard are prerequisites for the
[Docker Quick Start](../../docs/quickstart.md), which verifies signals through Grafana Explore.

```bash
# 1. Generate (offline; dry-run derive, no creds needed). Datasource UIDs are
# explicit so a stack with multiple same-type datasources cannot choose one by accident.
go run ./cmd/synthkit-dash -blueprint blueprints/acme-ai-platform.yaml -out dashboards/examples/acme_ai_platform \
  -datasource prometheus='<Prometheus datasource UID>' \
  -datasource loki='<Loki datasource UID>' \
  -datasource tempo='<Tempo datasource UID>'
# Optional thin-index deep-link targets:
#   -integrations dashboards/examples/integrations.yaml

# 2. Validate and push the Folder resource first, then every Dashboard resource. Select by the
# resource `kind`, not by file name: the generated folder, rules and panel-inventory files are
# named from the blueprint, `-folder` and `-namespace`, and the rules and inventory files are not
# Grafana dashboards at all. Needs `jq`.
push_kind() { # $1 = output dir, $2 = resource kind
  for f in "$1"/*.json; do
    [ "$(jq -r '.kind // empty' "$f")" = "$2" ] || continue
    gcx --context <target-stack> resources validate -p "$f"
    gcx --context <target-stack> resources push -p "$f"
  done
}
out=dashboards/examples/acme_ai_platform
push_kind "$out" Folder
push_kind "$out" Dashboard
GCX_AGENT_MODE=true gcx dashboards snapshot acme_ai_platform-index --context <target-stack> \
  --output-dir dashboards/examples/acme_ai_platform/snapshots --since 3h --width 1600 --theme dark
```

Find these UIDs in the destination Grafana's datasource settings or its
`/connections/datasources/edit/<uid>` URL. Grafana v2's `DataQuery.datasource.name` property carries
the resource UID. The [datasource binding guide](../../docs/tools.md#find-the-datasource-uids)
explains the distinction from a display label.

Synthetic data is **forward-only** (no backfill): snapshot with a bounded recent window (`--since 3h`)
during the modelled peak. Overnight volume is low by design (the diurnal shape engine).

Generation also writes `<blueprint>-panel-inventory.json`, which lists every panel, target, datasource,
and rendered query. Capture one read-only result per listed target into `observations.json`, then require a
complete disposition report before treating a dashboard-level snapshot as verified:

```bash
go run ./cmd/synthkit-dash \
  -verify-inventory dashboards/examples/acme_ai_platform/acme-ai-platform-panel-inventory.json \
  -observations observations.json
```

Each observation has `dashboard`, `panel_id`, `ref_id`, `state` (`rendered`, `empty`, or `errored`), and
verbatim `evidence`. Successful empty results classify as `emission_gap`; datasource-resolution evidence
classifies as `missing_datasource`; query errors classify as `query_defect`. The command fails if an
inventory query is unobserved or an observation does not match a generated query.

## Adding a blueprint's dashboards (the pluggable path)

Additive, mirroring the construct `runner.Catalog()` — no engine/library changes:

1. Add a Go package `dashboards/examples/<blueprint>/` exposing `Templates() []dashboard.Template`.
   Each template is `func(*dashboard.Manifest) (dashboard.Dashboard, error)` — it consumes the derived
   manifest (resolved topology + classified signals) and composes panels with the `dashboard` builder
   library (`Selector`/`RateExpr`/`ClassicHistogramQuantile`/`CWGauge`, panel + tab helpers, the thin
   index). The library bakes in the query discipline (scope-aware selectors, classic-histogram form,
   CW `_sum`-is-a-gauge rule), so templates stay declarative.
2. Register it with one line in `cmd/synthkit-dash/catalog.go`.

See [`acme_ai_platform/`](./acme_ai_platform) for the worked reference (app service graph → RDS
request correlation). The builder library + engine live in [`../../dashboard/`](../../dashboard) and
[`../../internal/dashgen/`](../../internal/dashgen). The off-the-shelf integrations (k8s / AWS / GCP /
Azure) are **not** rebuilt — the generated index deep-links the deployed vendor dashboards using
per-stack targets from `integrations.yaml` (copy `integrations.example.yaml`; the real file is
per-deploy — add it to `.gitignore` so live UIDs are never committed). The index seeds `$cluster`/
`$account` template variables from the resolved estate and interpolates them into the deep-links.

The [`otlp_native/`](./otlp_native) template completes the Quick Start blueprint with a focused HTTP
service view. Its documentation distinguishes the native HTTP gateway projection from the
remote-write inventory view and records the offline query proof and deployed verification limits.

## Control dashboard (self-serve knobs)

[`control/synthkit-customer-control.json`](./control) is a different kind of dashboard: not a view of
synthetic telemetry but a **self-serve control surface**. It exposes only the audience-safe knobs —
the master **volume** presets and the curated **incident scenarios** — while the operator UI
(`/control/ui`) keeps exposing everything. The single place that line is drawn is
`control.CustomerSchema` (`internal/control/audience.go`).

It is powered by the **Infinity datasource** pointed at the synthkit control plane, not by promrw:

- **Reads** use **relative paths** — `/control/schema?audience=customer`,
  `/control/state` — which the Infinity datasource's configured **Base URL** prefixes. So the committed
  dashboard is host-agnostic: it works against whatever base URL the datasource is provisioned with
  (see [`../../provisioning/`](../../provisioning)). The datasource is a normal proxy datasource — no
  pinned cert. Grafana Cloud reaches the host's URL privately via the user-configured PDC
  Tailscale connection. When `CONTROL_TOKEN` is set, provisioning stores the matching Basic
  password in datasource `secureJsonData`; it is never committed to dashboard JSON.
- **Writes** (native fetch-POST action buttons): volume → `/control/load`, clear incidents →
  `/control/scenarios`. These are **browser** fetches (not via the datasource), so they carry an absolute
  `--write-base-url`. POST routes sit behind the control plane's **HTTP Basic** challenge (username
  `control`, password `CONTROL_TOKEN`); the browser prompts natively on the first button press, so **no
  credentials are baked into the dashboard JSON**. Datasource credentials do not flow into these
  browser-direct requests, so verify the challenge flow over the HTTPS write base.

### Connectivity

synthkit serves the control plane over **plain HTTP on :8088**. Front it with **`tailscale serve`**
or a reverse proxy so it gets a browser-trusted cert at the host's address — that is what lets the
write buttons (browser → synthkit) work with no cert warning. The user enables Grafana Cloud's **PDC
Tailscale connection** (adding a tailnet auth key) so Grafana Cloud can run the read queries against
that URL privately.

Regenerate per-deployment (reads stay relative — only the write base + datasource name vary):

```bash
go run ./cmd/synthkit-control-dash \
  -write-base-url https://your-host.example.com \   # absolute; action-button POST base
  -ds-name        "synthkit (Infinity)" \           # Infinity datasource name in the stack
  -out            dashboards/examples/control
```
