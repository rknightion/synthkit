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

## 1. Immutable deployment identity

For Compose, require the intended verified release reference, version, and complete source revision.
Do not infer them from a mutable tag. Run the closed read-only cross-check before health or Grafana
queries:

```bash
python3 scripts/synthkit-deploy.py inspect-running \
  --container "$(docker compose ps -q synthkit)" \
  --expected-reference "ghcr.io/rknightion/synthkit@sha256:<index>" \
  --expected-version "X.Y.Z" \
  --expected-revision "<40-hex-source-sha>"
```

Require the configured index reference, discovered registry index, selected platform manifest, OCI
config/running image ID, binary version, and revision to agree. Docker's image ID normally equals
the config digest but remains a separate runtime observation. Stop if the expected release is not
available; this skill never changes the selector, snapshots/restores state, or prints private
deployment records.

## 2. Preflight context and credentials

1. Read `.env` by presence/shape only; never `cat` it or run `docker compose config`. Confirm
   `DRY_RUN=false` before expecting landing data. Record the declared `TICK_DEFAULT` value; if it
   is absent, use the documented default `5s`. Check whether `BLUEPRINT_NAMES` is empty, exact names,
   or `*` without printing any secret values. Empty means intentional setup mode and no synthetic data.
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

## 3. Control plane health

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

Read `setup_required`, `ready`, and `live_ready` from the readiness/status response. When
`setup_required=true`, require HTTP `200`, `ready=true`, `live_ready=false`, and zero loaded/active
blueprints. Search the retained service log, not only the 50-line sample above, for the actionable
warning with `docker compose logs --no-color synthkit | rg -F 'WARNING: no blueprints selected'`.
Report that the control plane is healthy but no synthetic telemetry is intended, and stop before
remote landing queries. When `setup_required=false`, continue with the selected blueprint checks below.

Inspect authenticated `optional_lanes` immediately after health. Require exactly the nine documented
lanes and report each state/reason/verification. Stop before claiming an optional lane when it is
`unsupported` or unresolved `partial`; missing fields are names only and must never be expanded to
values. An SM partial state is a successful diagnostic boundary: report that snapshot provisioning
and/or the required emitter restart remains. This read-only skill does not run the provisioner.

## 4. Wait for declared cadence, then query

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

## 5. Optional lanes

- Self-observability is expected only with `SELFOBS_ENABLED=true` and the complete self-OTLP
  triplet, independently of synthetic `DRY_RUN`; query the staff context, never the customer context.
- Process profiling is expected only when self-obs is on and the complete
  `GC_PYROSCOPE_*` triplet is present. There is no standalone profiling switch.
- Fleet metrics require the declaration and normal metrics landing; they do not require FM API
  credentials. Registration separately requires the complete `GC_FM_*` triplet and fresh
  registration/heartbeat evidence. Never call the FM API for metrics-only mode.
- Synthetic Monitoring requires a selected declaration, a matching private registration, and the
  post-provision restart before status can be enabled/verified. Query only after its declared
  interval plus delivery deadline.
- An SM credential or endpoint change requires an explicit `SM_PROVISION_MIGRATE_TARGET=true`
  preview and matching apply within 15 minutes before the restart; never treat a new snapshot alone
  as migrated ownership.
- RUM, Sigil, Synthetic Monitoring, and synthetic profiles are expected only when both their
  documented credential/configuration gate and an emitting blueprint lane are present.

## 6. Triage

| Symptom | Likely cause | Safe next step |
|---|---|---|
| Status healthy, no data | `DRY_RUN=true` | Change it only after the dry-run gate is accepted, then redeploy. |
| Empty query before its wait window | Interval/phase/delivery delay | Wait the calculated interval; do not retest every master tick. |
| 401/403 | Wrong token scope or user/stack ID | Compare presence and documented scopes; never print values. |
| Container restart loop | Control-state directory is not writable | Check ownership; container uid is 65532. |
| Staff stack empty | Customer/staff endpoints or tokens crossed | Verify the separate `GC_SELF_OTLP_*` triplet. |
| Data in wrong stack | Endpoint/user mismatch | Compare declared non-secret endpoints and identifiers with the intended stack. |
