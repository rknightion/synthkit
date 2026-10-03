# Gateway Health

The existing `alloy_health` cluster addon optionally declares a bounded gateway
pool. It emits a selected Alloy v1.20.1 / Collector v0.161.0 self-health subset,
not a full collector endpoint. See [signal provenance](../../signals/fm.md)
`[slug: fm-gateway]` and the local fixture
`e2e/fixtures/ai-factory-gateway.yaml`.

## Declaration and roles

Put `gateway` beneath the existing cluster addon. It requires `pool`, `site`,
`pool_size` (2..16, exactly matching `members`), `members`, `queue_capacity`
(1..100000 requests/batches), positive finite `drain_requests_per_min`,
`inputs` and a valid zero-based `source_victim`. Each member declares a unique
bounded `instance` host:port, bounded `job`, and nonempty distinct `roles`:

- `otlp_receive`: accepted/refused/failed item counters per configured signal.
- `central_scrape`: local assigned-target gauge and outgoing moved-target counter.
  Pool-wide `scrape_targets` (0..10000) are divided among live scrape members.
- `syslog`: the existing `syslog.receiver: loki|otel` health profile, required
  alongside this role. Aggregate `syslog_records_per_min` defaults to zero and is
  divided among live syslog members. It does not emit device log records.
- `cloud_forward`: queue size/capacity, successfully sent, failed attempts and
  enqueue-rejected item counters per configured signal. At least one forwarding
  member is required. Live intake is divided evenly over live forwarding members.

Each `inputs` entry selects `traces|metrics|logs`, a bounded nonnegative finite
`requests_per_min` and positive `items_per_request`. Intake is business-hours
shaped; no emission floor raises traffic. Central scrape adds metric batches per
assigned target; syslog adds log intake. Roles/site are not vendor label keys.
Configured identities use existing job/instance/component mechanisms.

Common process output is target `up`, healthy controller count, three peer-state
gauges, alive peers including self, and three registered remote-config families.
For three signals, counts per live process are common 9 + OTLP 9 + scrape 2 +
forwarding 15 + syslog 3 = **38** for all roles. Roles add only their own families.
A source gap removes 3 from the victim; process loss leaves only its target `up=0`.
Omitting `gateway` retains legacy 46 series plus optional syslog 6, with no new
member appended. Standalone FM collectors are not replaced.

## Gap analysis retained before code

| Existing coverage | Gateway gap closed |
|---|---|
| Healthy queue-size estimate, no capacity | Explicit per-signal batch capacity and retained queue physics |
| Span send counters only | Trace, metric-point and log-record send/drop counters |
| Two fixed HA identities, no peer state | Declared pool, peer states and target redistribution |
| Pipeline `up`, not target reachability | Target `up`, assigned-target and outgoing move counts |
| Config hash, not load success | Registered remote-config load success/attempt/failure subset |
| Landed opt-in receiver health | Reuse syslog mechanics, not another parser or invented OTel parse counter |
| No unique credential-expiry observation | Generic failed-send observation; expiry cause is injected, not inferred |
| No source freshness proof | Member-local collection omission with independent healthy witnesses |

## Failures and recovery

All six modes use the existing cluster target and the configured member victim.
Control scope must match that cluster; unrelated scopes do not activate them.

| Mode | Observable / recovery |
|---|---|
| `gateway_instance_loss` | Victim `up=0`; other victim families absent. Remaining scrape members own all targets and see one fewer participant; restoration redistributes targets and counts outgoing moves. |
| `gateway_wan_outage` | Failed send attempts rise and retained queues grow. On recovery successful delivery drains backlog. No drops inside capacity. |
| `gateway_queue_overflow` | Egress unavailable; full queues reject excess items, recorded separately from failed attempts. Reduce fixture capacity to witness overflow, never raise shipped traffic. |
| `gateway_config_reload_failure` | Victim unsuccessful last load and cumulative load failures; other member unaffected; next successful load restores gauge. |
| `gateway_cloud_credential_expired` | Egress attempts fail, queues retain input and recover after the injected fault. Generic failed sends do not prove expiry or uniquely distinguish auth from WAN errors. No unique expiry phrase/metric is fabricated. |
| `gateway_source_gap` | Victim same-process remote-config data omitted, process and controller healthy, peer/queue health and other member data present. Recovery resumes cumulative source counters. |

Queue units are requests/batches, with constant items per request. Admission occurs
before bounded service for each tick: input = delivered + rejected + retained.
Failed attempts may repeat the same items and are not extra losses. The first
observation models a 60-second interval; subsequent ticks use nonnegative elapsed
time. Queues and cumulative counters persist for the emitter lifetime, including
modeled member unavailability. This is **not actual disk storage**, crash durability,
retry expiration, arbitrary batch sizes or a real network exporter. Incoming data
at a lost receiver is not buffered on an external host. A new emitter resets state.

## Same-process source absence

The root-selected profile `alloy_remotecfg` contains only
`remotecfg_last_load_successful`, `remotecfg_load_attempts_total` and
`remotecfg_load_failures_total`, at the declared member's cluster/job/instance.
Expected collection cadence is **60 seconds**. During the victim's gap, polling
continues internally but these three families are omitted before the public
metric writer and inventory. They are not frozen/zeroed or filtered in a sink.

The independent target `up=1` and
`alloy_component_controller_running_components{health_type="healthy"}>0` remain.
A successful runner tick alone is not health evidence. This is deliberate
collection/publication failure injection, **not a vendor claim** that a healthy
remote-config service normally removes its registered metrics. Other hardware
producers are not simulated or controlled by this mode.

For a known configured victim, an absence query can combine freshness and health:

```promql
absent_over_time(remotecfg_load_attempts_total{cluster="gateway-fixture-cluster",instance="10.90.1.10:12345"}[3m])
and on ()
(up{cluster="gateway-fixture-cluster",instance="10.90.1.10:12345"} == 1)
```

Also check the positive healthy controller count for the same identity and verify
the other member's source still appears. Use a window exceeding two expected
collections; a single CLI dump proves actual batch absence but not backend staleness
or alert timing. Prometheus may retain the last sample inside its lookback window.

## Local evidence surface

Load the fixture through the public custom-blueprint staging layout documented in
[Custom Blueprints](../custom-blueprints.md), under `BLUEPRINT_DATA_DIR/custom/`
as `<namespace>__ai-factory-gateway.yaml`, selecting the exact namespaced identity.
Run locally with `DRY_RUN=true SELFOBS_ENABLED=false`, `-env /dev/null -once -dump`.
An active failure snapshot is separate from the normal fixture. Public integration
tests exercise fixed before/during/after times, scope, admission and conservation
with real loader, runner, state, shape and metric/log captures. No live stack is
needed or implied by these proofs.
