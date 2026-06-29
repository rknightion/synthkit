# dashboards — Grafana dashboard suite

Dashboard JSON for dashboards built on synthkit's synthetic telemetry. Two working disciplines
ported from the predecessor (cost real debugging there — do not re-learn).

## Layout — split by destination, not by stack name

Dashboards are grouped by **role/destination**, because the generator's own telemetry and the
synthetic telemetry it produces go to **different stacks** with different credentials:

| Dir | Visualises | Destination | Creds |
|---|---|---|---|
| `internal/` | the synthkit **process itself** (`service.name=synthkit`) — `internal/selfobs` + `internal/profiling` | **staff stack** | `GC_SELF_OTLP_*` / `GC_PYROSCOPE_*` |
| `examples/` | the **synthetic telemetry** synthkit emits (the modelled estate) | **target stack** | `GC_TOKEN` (+ promrw/loki/otlp/faro) |

One exception lives under `examples/`: `examples/control/` is a **control surface**, not a telemetry
view — the self-serve dashboard (`cmd/synthkit-control-dash`), powered by the Infinity
datasource against the control plane's `/control/*` routes rather than promrw. It exposes only the
audience-safe knobs (volume + scenarios; see `control.CustomerSchema`); the operator UI `/control/ui`
keeps the full set. See [`examples/README.md`](./examples/README.md).

Role-based names (`internal`/`examples`), not deployment stack names — the concrete stack each
targets is a per-deploy detail, but the internal-vs-synthetic boundary is permanent. Push each set
to its own gcx context: `gcx --context <staff-stack> …` for `internal/`, `gcx --context
<target-stack> …` for `examples/`.

## Verify visually — snapshot AND read the PNG

After pushing a dashboard, **always snapshot it and actually READ the resulting PNG** — never just
report the snapshot path and call it done. A clean push/validate says the JSON parsed, not that the
panels render meaningful data.

```bash
GCX_AGENT_MODE=true gcx dashboards snapshot <dashboard-uid> \
  --context <stack> --output-dir snapshots --since 3h --width 1600 --theme dark
```

## Synthetic data is forward-only — no backfill

Series only exist from the moment the generator started; there is **no historical backfill**. So:

- Snapshot/query with a **bounded recent window** (`--since 3h`), not `now-7d` — older ranges are empty.
- Traffic follows a **diurnal plateau** (the `internal/shape` engine): overnight volume is LOW *by
  design*. Empty/quiet overnight panels are expected, not a bug — verify during the modelled peak
  (or warm up the generator long enough to accrue history).
