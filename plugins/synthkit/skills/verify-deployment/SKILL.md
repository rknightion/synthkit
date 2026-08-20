---
name: verify-deployment
description: "Verify a synthkit deployment's control plane and declared signals; not generic Grafana queries."
---

# Verify a synthkit deployment

Confirm control-plane health first, then check only signals that the enabled blueprints and
configured lanes declare. This skill is read-only. Stop before a remote query when credentials or
the target Grafana context are absent; never solicit or print a token in order to “verify”.

## Locate the synthkit checkout

```bash
SYNTHKIT_CHECKOUT="/absolute/path/to/synthkit"
SYNTHKIT_CHECKOUT="$(git -C "$SYNTHKIT_CHECKOUT" rev-parse --show-toplevel)" || exit 1
test -f "$SYNTHKIT_CHECKOUT/AGENTS.md" && \
  test -f "$SYNTHKIT_CHECKOUT/docker-compose.yml" && \
  test -f "$SYNTHKIT_CHECKOUT/.env.example" || exit 1
cd "$SYNTHKIT_CHECKOUT"
```

## 1. Preflight context and credentials

1. Read `.env` by presence/shape only; never `cat` it or run `docker compose config`. Confirm
   `DRY_RUN=false` before expecting landing data. Record the declared `TICK_DEFAULT` value; if it
   is absent, use the documented default `5s`.
2. For a CLI query, confirm `gcx` exists and that its active contexts are visible without exposing
   credentials:

   ```bash
   command -v gcx
   gcx config list-contexts
   gcx config current-context
   gcx config check
   ```

   Select the named customer or staff context deliberately. If gcx is unavailable, use the Grafana
   UI or an operator-authenticated HTTP API fallback below.
3. A no-credential stop is a successful safety outcome: report “control-plane checks may proceed;
   remote data landing is unverified because no Grafana context/credential was supplied” and stop
   before any remote request.

## 2. Control plane health

Resolve `SYNTHKIT_BIND` from `.env` (default `127.0.0.1`) and detect only whether
`CONTROL_TOKEN` is non-empty. Never print its value. Use Basic auth for protected routes when set,
and call the public readiness probe without credentials:

```bash
curl -fsS "http://<bind>:8088/control/readiness"
curl -fsS -u "control:${CONTROL_TOKEN}" "http://<bind>:8088/control/status"
curl -fsS -u "control:${CONTROL_TOKEN}" -o /dev/null -w '%{http_code}\n' "http://<bind>:8088/control/ui"
docker compose ps
docker compose logs --tail=50 synthkit
```

Omit `-u` only when `CONTROL_TOKEN` is absent on a loopback-only deployment. Expect UI status
`200`, a running container, and ready sinks in the authenticated status result. Treat a
connection refusal from another machine as a likely loopback bind; query on the host or use an SSH
tunnel, rather than opening the bind casually.

## 3. Wait for declared cadence, then query

The master cadence is not a promise that every series appears every master tick: metric-producing
constructs/workloads have their own intervals and the runner clamps metric lanes to a 60-second
DPM floor with deterministic phase offsets. After a new deployment or configuration change, wait
**one declared emission interval plus the configured delivery deadline** for the selected lane.
Use the per-construct/workload interval from the schema/catalogue, or 60 seconds when it is a
metric lane below that floor; add `SEND_BATCH_DEADLINE` (default `5s`). Do not claim “one or two
ticks” is enough.

For the customer metric lane, choose a real expected metric family from the active blueprint's
relevant `signals/` page. Substitute only the placeholders shown here:

```promql
{__name__=~"<real_metric_name_or_family_regex>", blueprint="<blueprint_label>"}
```

For a substrate-scoped construct, omit `blueprint` because synthkit deliberately does not stamp it:

```promql
{__name__=~"<real_substrate_metric_name_or_family_regex>", cluster="<declared_cluster>"}
```

For staff self-observability, query the known family selector after its own cadence:

```promql
{__name__=~"synthkit_.*"}
```

Run the exact parameterized command below after replacing the bracketed values; `--context` avoids
silently using the wrong default context and `--since` bounds the observation window:

```bash
gcx --context <customer-context> metrics query \
  '{__name__=~"<real_metric_name_or_family_regex>", blueprint="<blueprint_label>"}' \
  --since '<emission-interval-plus-delivery-deadline>'
```

For substrate-scoped output, use the cluster selector instead:

```bash
gcx --context <customer-context> metrics query \
  '{__name__=~"<real_substrate_metric_name_or_family_regex>", cluster="<declared_cluster>"}' \
  --since '<emission-interval-plus-delivery-deadline>'
```

For staff self-observability, change the context and selector:

```bash
gcx --context <staff-context> metrics query '{__name__=~"synthkit_.*"}' \
  --since '<selfobs-emission-interval-plus-delivery-deadline>'
```

Do not invent a metric name or hard-code a stack URL. Verify traces/logs through their Explore
query editors using a service/resource attribute from the active blueprint and the same bounded
time range.

### UI and API fallback

If gcx is unavailable, open the selected stack's Grafana **Explore**, choose Prometheus, set the
same parameterized query, and inspect the last `emission interval + delivery deadline`. For an
operator-authenticated Prometheus API client, make only a GET to:

```text
https://<prometheus-api-host>/api/v1/query?query=<url-encoded-promql>
```

Use the stack's documented endpoint and credentials from the operator's terminal; do not place a
token in a command, chat, or URL. A successful response must have `status: "success"` and at least
one result. The absence of a result before the calculated wait window is inconclusive, not a
failure.

## 4. Optional lanes

- Self-observability is expected only with `SELFOBS_ENABLED=true` and `DRY_RUN=false`; query the
  staff context, never the customer context.
- Process profiling is expected only when self-obs is on, dry run is off, and the complete
  `GC_PYROSCOPE_*` triplet is present. There is no standalone profiling switch.
- Fleet Management registration requires `GC_FM_URL`, `GC_FM_STACK_ID`, `GC_FM_TOKEN`, and a
  `features.fleet_management` declaration. Use the read-only list procedure in
  `setup-fleet-management`.
- RUM, Sigil, Synthetic Monitoring, and synthetic profiles are expected only when both their
  documented credential/configuration gate and an emitting blueprint lane are present.

## 5. Triage

| Symptom | Likely cause | Safe next step |
|---|---|---|
| Status healthy, no data | `DRY_RUN=true` | Change it only after the dry-run gate is accepted, then redeploy. |
| Empty query before its wait window | Interval/phase/delivery delay | Wait the calculated interval; do not retest every master tick. |
| 401/403 | Wrong token scope or user/stack ID | Compare presence and documented scopes; never print values. |
| Container restart loop | Control-state directory is not writable | Check ownership; container uid is 65532. |
| Staff stack empty | Customer/staff endpoints or tokens crossed | Verify the separate `GC_SELF_OTLP_*` triplet. |
| Data in wrong stack | Endpoint/user mismatch | Compare declared non-secret endpoints and identifiers with the intended stack. |
