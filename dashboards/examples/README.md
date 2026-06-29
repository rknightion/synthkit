# dashboards/examples — synthetic-telemetry dashboards (target stack)

Dashboards that visualise the **synthetic telemetry synthkit emits** (the modelled estate) — pushed
to the **target stack** via `GC_TOKEN`. This is the counterpart to
[`../internal/`](../internal), which monitors the generator process itself on a separate staff stack.

These dashboards are **generated, never hand-edited.** The generator (`cmd/synthkit-dash`) resolves a
blueprint, derives the *actual emitted signal surface* by running one dry cycle through the runner
(the same `-once -dump` path), and renders per-blueprint Grafana **v2** dashboards (GA schema
`dashboard.grafana.app/v2`, via the Grafana Foundation SDK). Nothing ships by default — each blueprint
composes its own cross-construct views.

## Generate → validate → push → verify

```bash
# 1. Generate (offline; dry-run derive, no creds needed). Writes <uid>.json into -out.
go run ./cmd/synthkit-dash -blueprint blueprints/acme-ai-platform.yaml -out dashboards/examples/acme_ai_platform \
  [-integrations dashboards/examples/integrations.yaml]   # optional: thin-index deep-link targets

# 2. Validate / 3. push / 4. snapshot AND READ the PNG — same discipline as ../internal/ (see ../CLAUDE.md)
gcx --context <target-stack> resources validate -p dashboards/examples/acme_ai_platform/acme_ai_platform-index.json
gcx --context <target-stack> resources push     -p dashboards/examples/acme_ai_platform/acme_ai_platform-index.json
GCX_AGENT_MODE=true gcx dashboards snapshot acme_ai_platform-index --context <target-stack> \
  --output-dir dashboards/examples/acme_ai_platform/snapshots --since 3h --width 1600 --theme dark
```

Synthetic data is **forward-only** (no backfill): snapshot with a bounded recent window (`--since 3h`)
during the modelled peak. Overnight volume is low by design (the diurnal shape engine).

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

## Control dashboard (self-serve knobs)

[`control/synthkit-customer-control.json`](./control) is a different kind of dashboard: not a view of
synthetic telemetry but a **self-serve control surface**. It exposes only the audience-safe knobs —
the master **volume** presets and the curated **incident scenarios** — while the operator UI
(`/control/ui`) keeps exposing everything. The single place that line is drawn is
`control.CustomerSchema` (`internal/control/audience.go`).

It is powered by the **Infinity datasource** pointed at the synthkit control plane, not by promrw:

- **Reads** (GET, open — no auth) use **relative paths** — `/control/schema?audience=customer`,
  `/control/state` — which the Infinity datasource's configured **Base URL** prefixes. So the committed
  dashboard is host-agnostic: it works against whatever base URL the datasource is provisioned with
  (see [`../../provisioning/`](../../provisioning)). The datasource is a normal proxy datasource — no
  pinned cert. Grafana Cloud reaches the host's URL privately via the user-configured PDC
  Tailscale connection.
- **Writes** (native fetch-POST action buttons): volume → `/control/load`, clear incidents →
  `/control/scenarios`. These are **browser** fetches (not via the datasource), so they carry an absolute
  `--write-base-url`. POST routes sit behind the control plane's **HTTP Basic** challenge (username
  `control`, password `CONTROL_TOKEN`); the browser prompts natively on the first button press, so **no
  credentials are baked into the dashboard JSON**.

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
