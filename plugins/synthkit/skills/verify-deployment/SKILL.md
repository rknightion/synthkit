---
name: verify-deployment
description: Use when confirming a synthkit deployment is healthy — checking the control plane, that metrics/traces/logs are landing in the right Grafana Cloud stacks, or triaging why expected data is missing after a deploy.
---

# Verify a synthkit deployment

Confirm the control plane is up and synthetic data is reaching the correct stack(s). Runs standalone
or as the final step of `initial-setup`.

## Locate the synthkit checkout

Plugin installation provides guidance only; it does not install synthkit or create a checkout.
Before any repository command, establish and verify the checkout root:

```bash
SYNTHKIT_CHECKOUT="/absolute/path/to/synthkit"
SYNTHKIT_CHECKOUT="$(git -C "$SYNTHKIT_CHECKOUT" rev-parse --show-toplevel)" || exit 1
test -f "$SYNTHKIT_CHECKOUT/AGENTS.md" && \
  test -f "$SYNTHKIT_CHECKOUT/docker-compose.yml" && \
  test -f "$SYNTHKIT_CHECKOUT/.env.example" || exit 1
cd "$SYNTHKIT_CHECKOUT"
```

All repository paths below are rooted at `$SYNTHKIT_CHECKOUT`. This skill has no plugin-owned
helper to invoke.

## Step 1 — Control plane health
Resolve the bind host (from `.env` `SYNTHKIT_BIND`, default `127.0.0.1`). Then:
- `curl -fsS http://<bind>:8088/control/status` — sinks should report ready; check `DRY_RUN`.
- `curl -fsS -o /dev/null -w '%{http_code}\n' http://<bind>:8088/control/ui` — expect `200`.
- Container: `docker compose ps` (synthkit `running`); `docker compose logs --tail=50 synthkit`.

## Step 2 — Data landing (per configured lane)
Read `.env` to see which lanes are enabled (do not print secret values). Using `gcx --context <stack>`
where available:
- **Customer stack:** query for the series/traces/logs the active blueprint(s) emit (e.g. a known
  metric name from the blueprint). Expect non-empty within ~1–2 ticks.
- **Staff stack** (if `SELFOBS_ENABLED=true`): synthkit's self-obs OTel instruments are
  `synthkit.push`, `synthkit.push.items`, `synthkit.push.bytes`, `synthkit.push.duration`,
  `synthkit.tick`, `synthkit.cycle.duration`, `synthkit.dropped_ticks` (source:
  `internal/selfobs/selfobs.go`). On the wire (OTel→Prometheus) dots become underscores, counters
  gain `_total`, and unit suffixes may be appended — so confirm by querying the name set
  `{__name__=~"synthkit_.*"}` rather than guessing exact suffixes, and expect the push/tick/cycle
  families to appear. If `PYROSCOPE_ENABLED=true`, confirm profiles arriving.
- **Fleet Management** (if `GC_FM_URL` set): confirm collector registration via the FM API.

## Step 3 — Triage (when data is missing)
| Symptom | Likely cause | Fix |
|---|---|---|
| status ok but no data | `DRY_RUN=true` still set | set `DRY_RUN=false`, `docker compose up -d` |
| `curl` connection refused | bound to loopback; querying from elsewhere | query from the host, or use an SSH tunnel |
| 401/403 on push (logs) | token scope/user mismatch | check the lane's token scopes + `*_USER` value |
| container restart loop | `control-state-data` not writable | `sudo chown -R 65532:65532 control-state-data` |
| staff stack empty | reused `GC_TOKEN` instead of staff token | set the separate `GC_SELF_OTLP_*`/`GC_PYROSCOPE_*` creds |
| data in wrong stack | customer vs staff endpoints swapped | verify each lane's endpoint/user against `references` |
