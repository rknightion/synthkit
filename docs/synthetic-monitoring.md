---
title: Synthetic Monitoring
description: Emitting Grafana Cloud Synthetic Monitoring data without a real probe using a two-phase provisioner and data-plane emitter.
---

# Synthetic Monitoring

synthkit emits Grafana Cloud Synthetic Monitoring data — `probe_success`, `probe_duration_seconds`, the full SM histogram family, `sm_check_info`, and the SM Loki log stream — with no real probe ever executing. The SM app becomes populated with checks, probe status, and check history purely from injected telemetry.

This requires a two-phase startup: a one-shot provisioner registers the offline probe and checks with the SM API before the data-plane emitter produces useful output.

---

## Why two phases

The Grafana Cloud SM app hard-guards on its own check registry: it clamps timelines to `check.created` and will not display injected metrics for checks it does not know about. Metric injection alone is not enough. The offline probe must be registered first, the checks must be created (or updated) in Grafana Cloud, and then the data-plane emitter can push forward-in-time data that populates the SM views.

Probe name, region and coordinates resolve from the blueprint through `internal/construct/sm`, so the provisioner snapshot and emitter share the same location. Checks may declare `latitude` and `longitude` together; omission retains Frankfurt (50.1109, 8.6821). Explicit zero is valid. Latitude must be within -90 to 90 and longitude within -180 to 180, both finite. Every check using the same probe name must agree on region and coordinates, including across blueprints. The emitter derives the precision-12 `geohash` on `sm_check_info` from those coordinates.

Changing a probe location changes its desired specification. Follow the preview/apply/restart sequence below to update the owned probe and activate the new snapshot.

---

## Credentials

SM provisioning uses a dedicated SM API token — separate from `GC_TOKEN` and from all other sinks.

| Env var | Description |
|---|---|
| `GC_SM_URL` | SM API base URL, e.g. `https://synthetic-monitoring-api-<region>.grafana.net` |
| `GC_SM_TOKEN` | SM API bearer token (not `GC_TOKEN`) |

The provisioner never scans blueprints. The emitter resolves the exact selected built-in, custom,
and Git sources and publishes an exact snapshot. File mode uses private files under `/data/runtime`;
Kubernetes mode uses a named aggregate ConfigMap and requires no shared volume. The provisioner reads only
that snapshot. `SM_PROVISION_APPLY` is its independent write gate; emitter `DRY_RUN` is irrelevant.

See [Credentials](credentials.md) for token-scoping guidance.

---

## Phase 1 — provision the offline probe and checks

Start the emitter once with the intended SM blueprint selected. Until a matching registration
exists, synthkit writes the snapshot but suppresses SM emission. Preview the version-matched
Docker provisioner:

```bash
docker compose up -d synthkit
docker compose --profile sm-provision run --rm sm-provision
```

The preview reports action counts only and makes no remote mutation. Apply explicitly after review:

```bash
SM_PROVISION_APPLY=true docker compose --profile sm-provision run --rm sm-provision
docker compose restart synthkit
```

The restart is the activation boundary. On startup the emitter validates activation schema 2,
exact specification/target hash, complete resource IDs and authoritative API `modified` bindings.
Release version is provenance, not part of that hash: an already complete registration at release A
remains active at B when every specification field, target and schema is unchanged. The write tool
must still match the **current snapshot source version**, before inventory and on every state write.
Adoption/migration approvals retain their strict source-version binding.

The first v1-to-v2 format upgrade requires one explicit apply/restart. File activation uses new
`sm-snapshot-v2.json` and `sm-registration-v2.json` files. Existing v1 ownership, previews, pending
journal and lock are retained; a legacy registration can import ownership only, never activate v2.
With intact ownership and unchanged remote specs, that first apply makes no remote changes. Later
release-only upgrades need no new apply. Changed specs, partial/missing registration or any pending
journal keep SM suppressed; unreadable/corrupt storage fails closed rather than becoming empty state.

The provisioner owns only IDs recorded in its private durable ownership ledger. That ledger is
independent of one snapshot/source version, so a changed check can update its recorded ID while the
activation registration remains bound to the exact current snapshot. A same-name probe or same
`(job,target)` check without matching ownership is a foreign collision and causes zero writes.

Credential or endpoint rotation changes the target fingerprint and suppresses SM until an explicit
migration succeeds. Recreate the emitter with the new credential so it writes the new snapshot, then
preview and apply the migration:

```bash
docker compose up -d --force-recreate synthkit
SM_PROVISION_MIGRATE_TARGET=true docker compose --profile sm-provision run --rm sm-provision
SM_PROVISION_MIGRATE_TARGET=true SM_PROVISION_APPLY=true docker compose --profile sm-provision run --rm sm-provision
docker compose restart synthkit
```

Preview uses the new credential to revalidate every recorded API ID, key, managed specification,
and remote revision against the ledger's last authoritative evidence, but changes only a mode-0600 private preview marker. Apply must run within 15
minutes with the identical snapshot, source version, remote evidence, and plan. Only then is the
ownership ledger atomically rebound. Migration is identity-only: every planned resource action must
be unchanged, so it makes no remote API writes. Reconcile any configuration or resource change as a
separate normal apply before or after rotation. Missing or changed resources, a stale or
absent preview, or an absent migration flag fails closed. Target migration and legacy adoption cannot
be combined; finish migration first and review any later adoption separately. If the process stops
after the ledger is rebound, the marker remains and blocks normal reconciliation; rerun apply with
the migration flag to resume the unchanged plan and consume the marker.

Legacy adoption is off by default. First preview with `SM_PROVISION_ADOPT_LEGACY=true`; the
provisioner records a private hash of that exact adoption plan. A later apply accepts only the same
snapshot and plan, and only one exact complete-spec match may be adopted. A pending journal stores
the expected specification as well as its hash. An ambiguous create is never reconciled by
name/spec: inspect it and establish ownership before explicitly clearing the journal and choosing
whether to adopt. Resources are never deleted.

`alertSensitivity` is always registered as `"none"` at the API level (the real value is stamped on the `sm_check_info` metric by the data-plane emitter independently of the provisioner).

---

## In-cluster one-shot mode — no shared volume

The binary supports `STATE_BACKEND=kubernetes` independently of emitter HA. Precreate a distinct
`STATE_SM_CONFIGMAP` and `SM_PROVISION_LEASE_NAME` in `HA_NAMESPACE`, and supply downward-API
`POD_UID`. The provisioning Lease must differ from `HA_LEASE_NAME`, including with emitter HA off.
Every writer to the SM slot must use the same provisioning Lease; the aggregate document records
that immutable lock identity. Give the tool only named get/update on that SM ConfigMap and its
Lease, projected in-cluster API credentials and the SM credential pair. There is no KUBECONFIG,
endpoint override, discovery or resource creation fallback. The emitter needs named state access
and uses its own emission authority, never the tool's provisioning Lease.

No SM request or state update occurs before acquisition. Acquisition is capped at two minutes and
the invocation at ten minutes. The tool reuses the HA duration settings: default HTTP timeout 5s,
whole-operation cap 8s and fence allowance 2s must fit strictly below Lease minus renewal (30s−15s).
State retries are 1..5 CAS attempts sharing 2s. Every mutation first persists pending evidence and
removes activation; uncertain API/state outcomes stop without replaying the mutation. Inspect retained
pending evidence before any explicit recovery. Lease authority is local admission plus bounded hard
exit, **not remote SM fencing or exactly-once delivery**. Planned termination joins work while renewal
continues, then seals and releases; unexpected loss hard-exits without release.

Kubernetes HA standby performs zero writes; acquisition publishes/revalidates the current snapshot
before building the production runner and its first tick. File Lease HA remains suppressed without
artifact I/O. There is no automatic file-to-Kubernetes ownership migration or dual writing: moving
existing ownership needs a separately approved offline procedure. The chart supports a
[Kubernetes-state SM Job with HA emission](kubernetes.md#diskless-one-shot-job). Enable
`ha.enabled` and `smProvision.enabled`, select `smProvision.stateBackend: kubernetes`, and set
`smProvision.emitterLeaseName` equal to `ha.leaseName`. The chart wires the named SM aggregate
slot and provisioning lock identity into the emitters with named `get`/`update` only; each
principal retains its own Lease authority. Both binaries must use the same verified image, at
least **1.6.0-rc.176**, which carries the matching named-SM engine. The chart default
`1.6.0-rc.160` predates it. Preview, explicit apply, then emitter restart/reacquisition remain
required; there is no automatic activation or remote-write approval. With HA off, the
Kubernetes-state Job remains standalone and its emitter is configured separately. The default
file-mode Job retains its shared-volume behavior.

## Phase 2 — run the emitter

After the successful apply and required restart, verify the authenticated disposition first:

```bash
curl -s -u control http://127.0.0.1:8088/control/status |
  jq '.optional_lanes[] | select(.lane=="synthetic_monitoring")'
```

Require `state="enabled"` and `verification="verified"`, then wait one declared emission interval
plus `SEND_BATCH_DEADLINE` before querying `probe_*` data. A successful provisioner exit without
the restart is not activation.

The SM emitter pushes per-tick:

- `probe_success` (gauge, 0/1)
- `probe_duration_seconds` (gauge)
- `probe_all_success_sum` / `_count` (summary counters)
- `probe_all_duration_seconds_bucket` / `_sum` / `_count` (histogram, buckets `[0.1, 0.25, 0.5, 1, 2.5, 5, 10]`)
- `sm_check_info` (info gauge, carries `check_name`, `region`, `frequency`, `geohash`, `alert_sensitivity`, user labels)

Each check also emits a Loki log line per tick with stream labels `{source="synthetic-monitoring-agent", check_name, instance, job, probe, region, probe_success}` and a logfmt body (`msg="Check succeeded"` or `"Check failed"`, `duration_seconds`).

Base labels on every series: `job` (check job), `instance` (check target URL), `probe` (probe name), `config_version`. User labels declared in the blueprint become `label_<k>` labels on both metrics and the Loki stream.

!!! warning "config_version is a stable join key"
    All SM series join on `(instance, job, probe, config_version)`. `config_version` encodes the check's `modified` timestamp. If you re-provision after editing a check, all series must be re-stamped with the new `config_version`. A successful explicit apply followed by restart/acquisition injects the new authoritative value; an already running process is not hot-activated.

---

## Blueprint configuration

Checks accept `type: ping`, `type: dns`, `type: tcp` and `type: traceroute`;
omitted `type` retains HTTP. Ping/traceroute targets are hostnames or IPv4 addresses.
DNS targets are queried names and require `dns: {server: resolver.example.com}`;
the supported profile uses A queries over UDP port 53. TCP targets use `host:port`
and optionally `tcp: {tls: true}`. Network checks default to 60-second frequency
and 3-second timeout, except traceroute, which defaults to 120 seconds and requires
a 30-second timeout. Wrong-type settings and malformed targets are rejected.

Traceroute emits one logfmt entry per simulated hop. Its aggregate packet-loss
metric is a 0–1 fraction; hop `LossPercent` is 0–100. Network duration histograms use
the pinned agent's full default buckets; the existing HTTP histogram remains unchanged.
TLS certificates model a synthetic CA with 90-day validity and renewal every 60 days.
Their identities and expiry remain fixed between renewals. These are simulated checks,
not network observations or certificate verification against the declared endpoint.

The sample blueprint includes all four network types. Existing HTTP-only snapshot
hashes remain unchanged. Adding or changing a check type/settings changes its desired
specification and requires the usual preview/apply/restart sequence.

Enable SM by adding a `synthetic_monitoring` block under `features:`:

```yaml
features:
  synthetic_monitoring:
    enabled: true
    checks:
      - name: my-api-health
        target: "https://api.example.com/health"
        frequency: 60000         # ms; default 60000
        probe: synthkit-private  # must match the registered probe name
        region: EMEA
        labels:
          team: platform
          tier: api
```

Each check entry becomes one registered SM check and one emitted series family. `target` defaults to `https://<name>.example.com/health` if omitted.

See [`blueprints/synthetic-monitoring.yaml`](https://github.com/rknightion/synthkit/blob/main/blueprints/synthetic-monitoring.yaml) for a complete example combining SM checks with a Fleet Management collector roster.

---

## Failure mode

The `sm_probe_failure` incident mode targets a specific check and environment: it sets `probe_success=0`, `probe_duration_seconds=3.0` (the SM timeout), and emits a Loki log line with `probe_success="0"` and `msg="Check failed"`. See [Incidents](incidents.md) for how to declare and activate failure modes.

---

## See also

- [`signals/sm.md`](https://github.com/rknightion/synthkit/blob/main/signals/sm.md) — full signal contract with label shapes, histogram buckets, and provenance
- [CLI](cli.md) — `sm-provision` command reference
- [Configuration](configuration.md) — complete environment variable reference
- [Credentials](credentials.md) — SM token scoping
