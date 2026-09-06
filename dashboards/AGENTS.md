# dashboards

Grafana dashboards built on synthkit output. Split by role and destination, not by stack name,
because the generator's own telemetry and the synthetic telemetry it produces go to **different
Grafana stacks with different credentials**:

| Dir | Visualises | Destination | Credentials |
|---|---|---|---|
| `internal/` | the synthkit process itself (`service.name=synthkit`), via `internal/selfobs` + `internal/profiling` | staff stack | `GC_SELF_OTLP_*`, `GC_PYROSCOPE_*` |
| `examples/` | the synthetic telemetry synthkit emits (the modelled estate) | target stack | `GC_TOKEN` (+ promrw/loki/otlp/faro) |

The role-based names are deliberate: the concrete stack each targets is a per-deploy detail, but the
internal-versus-synthetic boundary is permanent. Push each set to its own context:
`gcx --context <staff-stack> ...` for `internal/`, `gcx --context <target-stack> ...` for `examples/`.

`examples/control/` is the one exception: a control surface rather than a telemetry view. It is the
self-serve dashboard behind `cmd/synthkit-control-dash`, powered by the Infinity datasource against
the control plane's `/control/*` routes rather than promrw, and it exposes only the audience-safe
knobs (volume and scenarios, per `control.CustomerSchema`). The operator UI at `/control/ui` keeps
the full set.

## What is source and what is output

Every dashboard JSON here is generated. Edit the generator, never the JSON.

- Under `examples/*/`, the tracked Go packages are the source; the per-blueprint JSON they render is
  untracked output of `cmd/synthkit-dash` (`just dashgen -blueprint ... -out ...`), which derives the
  actual emitted signal surface by running one dry cycle through the runner. `examples/README.md`
  holds the exact invocation.
- `examples/control/synthkit-customer-control.json` is tracked output of
  `cmd/synthkit-control-dash`.
- `internal/synthkit-selfobs.json` is tracked output of `internal/build_selfobs_dashboard.py`.
  `just selfobs-dashboard` rebuilds and pushes it, and is marked `[confirm]` because it writes to a
  live stack.

## Verify visually: snapshot AND read the PNG

After pushing a dashboard, snapshot it and actually read the resulting PNG. Never report a snapshot
path and call it done: a clean push or validate proves the JSON parsed, not that the panels render
meaningful data.

```bash
GCX_AGENT_MODE=true gcx dashboards snapshot <dashboard-uid> \
  --context <stack> --output-dir snapshots --since 3h --width 1600 --theme dark
```

## Synthetic data is forward-only

Series exist only from the moment the generator started; there is no historical backfill.

- Snapshot and query with a bounded recent window (`--since 3h`), not `now-7d`. Older ranges are
  empty.
- Traffic follows a diurnal plateau (`internal/shape`), so overnight volume is low by design. Quiet
  overnight panels are expected, not a bug. Verify during the modelled peak, or warm the generator up
  long enough to accrue history.

## Deeper references

- `examples/README.md` - read before generating, validating or pushing an `examples/` dashboard; it
  carries the exact `synthkit-dash` invocation and the per-file push loop.
- `internal/README.md` - read before changing the self-observability dashboard; it carries the UID,
  folder, schema version and tab layout.
