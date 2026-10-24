# CloudWatch families (→ Mimir remote_write) — ScopeBlueprint

AWS CloudWatch metric-stream families. All blueprint-scoped (carry the `blueprint` label).
Global rules: see [`00-canon.md`](00-canon.md) — scoping `[slug: scoping]`, cardinality
`[slug: cardinality]`, shape rules `[slug: shape-rules]`. The CW naming convention is LAW
and lives here `[slug: cw-naming]`.

> **Block convention for this file.** Every `yaml signals` block lists the EMITTED base names in
> `metrics:`; the 5-stat suffix expansion is declared once per block as `stats:` and is NOT
> repeated per metric. All CW stat series are per-period **gauges** (`type: gauge`) — never
> `rate()` a `_sum` (canon `[slug: cardinality]`). Families/series marked 📋 (documented, not yet
> modelled) stay in prose and are deliberately ABSENT from `metrics:`.

---

## CloudWatch naming convention — LAW (stated ONCE; referenced everywhere) ✅ [slug: cw-naming]

> CloudWatch namespace `AWS/X` + metric `M` + statistic → `aws_<namespace, "/"-stripped, lowercased>_<M
> lowercased, CamelCase split EXCEPT consecutive-caps acronyms>_<stat>`. Stats = exactly
> `_sum | _average | _maximum | _minimum | _sample_count` (all five always emitted). Dimensions →
> `dimension_<DimName>` labels **preserving CW casing**. Spaces in a CW dimension name become
> underscores (Prometheus label names cannot contain spaces): `Endpoint Type`→`dimension_Endpoint_Type`,
> `VPC Endpoint Id`→`dimension_VPC_Endpoint_Id`, `Service Name`→`dimension_Service_Name`, `Subnet Id`→
> `dimension_Subnet_Id` — casing otherwise preserved. Live-confirmed via `AWS/PrivateLinkEndpoints`
> (SK-3, 2026-06-14, `job=cloud/aws/privatelinkendpoints`); ⚠ cross-region endpoint *consumers* never
> publish these CW connection metrics (documented AWS limitation) — a same-region endpoint is required.

```
namespace_prefix = lowercase(strip_slash_and_normalise(N))
metric_part      = lowercase(M)    # NO underscore between consecutive-caps acronyms
stat_suffix      = lowercase(S)    # _average | _maximum | _minimum | _sum | _sample_count
series_name      = "aws_" + namespace_prefix + "_" + metric_part + "_" + stat_suffix
```

**Acronym/casing rule (empirically derived):**
- Consecutive all-uppercase sequences stay one token: `CPUUtilization`→`cpuutilization` (NOT
  `cpu_utilization`), `EBSRead`→`ebsread`, `HTTPCode`→`httpcode`, `TTFT`→`ttft`.
- Underscores already in the CW name are preserved: `HTTPCode_Target_2XX_Count`→`httpcode_target_2_xx_count`.

> **Implementation:** the five-stat expansion + the per-period-GAUGE rule (`state.Set`, never `Add` —
> I5) + per-suffix label isolation live in `internal/cw` (`cw.EmitStats` over a `cw.StatSet`). AWS
> constructs pass the exact base name (the metric_part above is NOT computed at runtime — names are
> authored from this table, never invented) and their own per-metric stat values. `cw` owns the
> suffixes and the gauge semantics; the construct owns the numbers.
- `2XX`/`4XX`/`5XX` → `2_xx`/`4_xx`/`5_xx` (numbers pass through; `XX` gets its own boundary).
- New word at an uppercase→lowercase transition following a single preceding uppercase:
  `UnHealthy`→`un_healthy` (but `Unhealthy`→`unhealthy`).

**Core collector labels:** `account_id`, `region`, `namespace` (original CW string, verbatim),
`job`=`cloud/aws/<lowercased service>`, `name` (resource ARN or `"global"`).
`dimension_<DimName>` labels are metric-specific, not universal; `tag_*` labels are optional
resource-tag enrichment joined by the metadata scraper. See `[slug: cw-captured]` for exact
observed profiles, including collector/backend enrichment that is not a vendor dimension. The scraper also emits a per-namespace `aws_<ns>_info` series (gauge=1, no
stat suffix) with `scrape_job`; it does not add resource dimensions or `tag_VpcId`.

> ✅ **SKT-0024 `_info` correction (EKS live-readback corpus captured 2026-08-27, reviewed
> 2026-08-29):** eight observed `aws_*_info` families carry `scrape_job`, but not `tag_VpcId`; the
> observed corpus deliberately elides its values. Current Grafana Cloud CloudWatch scrape
> documentation (retrieved 2026-08-29) establishes that `scrape_job` is the configured CloudWatch
> scrape-job value (for example, `scrape_job="myEC2Job"`). Synthkit therefore uses its stable
> synthetic configured value `synthkit-cloudwatch` consistently, rather than claiming the elided
> captured value. `aws_ebs_info` also omits `dimension_VolumeId`, which belongs only to EBS metric
> families.

> ⚠ **Per-period GAUGE invariant (I5).** `_sum`-suffixed CW series are per-period GAUGES — the
> sum/count within the reporting window (not universally one minute), NOT a monotonic counter. **Never `rate()`/`increase()`
> any `_sum` series** — use the raw value or `delta()`. Applies to ALL CW families without exception.

> ⚠ **No histogram buckets on any CW family** — CW metric-stream series have only the 5 stat
> suffixes; there are NO `_bucket` series. (Lab-confirmed across 21,958 series: zero `_bucket` on any
> CW family.) Publication is metric-specific: sparse traffic families may be absent when idle,
> but this is not a universal zero-traffic rule (see DynamoDB consumed-capacity sample counts below).

**Namespace → prefix quick reference:** `AWS/ApplicationELB`→`aws_applicationelb_` ·
`AWS/NetworkELB`→`aws_networkelb_` · `AWS/EC2`→`aws_ec2_` · `AWS/EBS`→`aws_ebs_` ·
`AWS/NATGateway`→`aws_natgateway_` · `AWS/S3`→`aws_s3_` · `AWS/EKS`→`aws_eks_` ·
`AWS/Firehose`→`aws_firehose_` · `AWS/RDS`→`aws_rds_` · `AWS/ElastiCache`→`aws_elasticache_`.

## Initial captured-service emitters [slug: cw-captured-emitted]

The optional `cloud.lambda`, `cloud.sqs`, `cloud.dynamodb`, `cloud.redshift` and
`cloud.cloudwatch_logs` declarations emit **only these 51 bases (255 final statistic names)**
over remote-write (35 original bases plus ten Redshift, two Logs intake and four optional Logs delivery bases). They are separate blueprint-scoped
constructs, not pipeline-stage adapters. Omission emits nothing for that kind. One declaration
per blueprint/account/region/kind owns the entire resource list; duplicate declarations across
environments reject even with disjoint resources. Different kinds and blueprints remain distinct.
Build rejects `cloudwatch_export: otlp` for all five: this is an implementation boundary, not a
claim that AWS lacks native Metric Streams. No native aliases are installed by these consumers.
The source tables below remain positive-only source evidence, **not emission allowlists**.

All statistics are independent per-period gauges. Inputs are explicit synthetic operator
assumptions, not captured numeric distributions or verified native fidelity. Sparse arrivals
retain active fractional carry. Master volume zero emits no batches and accrues no traffic;
observed resume establishes a new active boundary without paused-time backfill. Positive master
volume retains fixed configured rates (no new diurnal or positive scaling guarantee). A pause
and resume entirely between observations cannot be detected. Sample counts count observations,
never duration/byte magnitudes. Missed intervals are not replayed.

In these five blocks, `dimension_profiles` and each metric's `profiles` describe **exact
alternative vendor-dimension key sets**, not their union. The common labels are `account_id`,
`region`, `namespace`, `job`, `name`, plus the writer-stamped `blueprint`. No caller metadata is
guessed. The public fixture exercises all profiles without enrichment. SQS alone permits optional
explicit P2-vocabulary enrichment (`asserts_env`, `asserts_site`, `service`, `tag_Environment`,
`tag_Owner`, `tag_Purpose`, `tag_Terraform`); omitted keys stay absent and these are not dimensions.

### Lambda emitted subset [slug: cw-lambda-emitted]

Function resources use FunctionName only and `name=global`; no Resource aliases, versions,
EventSourceMappingUUID or tags are inferred. Account concurrency is emitted once without resource
dimensions. Total execution attempts comprise admitted async executions (received minus dropped),
stream batches and a nonnegative synchronous residual. Duration observes each execution; async
age observes admitted async executions, iterator age observes stream batches. Errors are a
subset of executions; throttles are separate. Ages are explicit constants; durations are constant
unless `duration_ms_min`/`duration_ms_max` declare operator-assumed bounds (the mean stays
`duration_ms`); `load: business` is an opt-in operator assumption scaling arrival rates by the
business curve, not a sourced AWS behaviour. Errors stay emitted only when an error occurs (no
sourced zero-value Errors observation); concurrency
is a synthetic snapshot, not an AWS scheduler simulation. Reserved concurrency must be whole.
Units retain the exact historical U1–U9 scope below; the modeled FunctionName-only profile does
not authorize other historical/native profiles.

```yaml signals
family: aws_lambda
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels: {account_id: <account>, region: <region>, namespace: AWS/Lambda, job: cloud/aws/lambda, name: global}
dimension_profiles:
  account: []
  function: [dimension_FunctionName]
metrics:
  - {root: async_event_age, type: gauge, unit: milliseconds, profiles: [function], note: U1 only}
  - {root: async_events_dropped, type: gauge, unit: count, profiles: [function], note: U2 only}
  - {root: async_events_received, type: gauge, unit: count, profiles: [function], note: U3 only}
  - {root: claimed_account_concurrency, type: gauge, unit: count, profiles: [account], note: U4 only}
  - {root: concurrent_executions, type: gauge, unit: count, profiles: [function], note: U5 only}
  - {root: duration, type: gauge, unit: milliseconds, profiles: [function]}
  - {root: errors, type: gauge, unit: count, profiles: [function], note: U6 only}
  - {root: invocations, type: gauge, unit: count, profiles: [function], note: U7 only}
  - {root: iterator_age, type: gauge, unit: milliseconds, profiles: [function]}
  - {root: throttles, type: gauge, unit: count, profiles: [function], note: U8 only}
  - {root: unreserved_concurrent_executions, type: gauge, unit: count, profiles: [account], note: U9 only}
```

Withheld: `deleted_event_count`, `failed_invoke_event_count`, `invoked_event_count`,
`polled_event_count` (ESM identity/unit barriers) and unsuffixed `aws_lambda_info` (no admitted
metadata projection). Their positive source observations remain unchanged below.

### SQS emitted subset [slug: cw-sqs-emitted]

Every resource requires QueueName and an explicit non-global `collector_name` for `name`;
that value is not an inferred ARN. Each receive attempt handles at most one message. Successful
deliveries delete after the declared processing delay; no retries, DLQ or tenant-group attribution
is modeled. Fractional arrivals and receive capacity persist across active windows, but unused
whole idle receive tokens are empty receives, not banked service capacity. Stock is sampled once
per synthetic active second; default four-day retention and six-hour activity retention are model
assumptions, not captured publication/sample-count guarantees. Message size is whole bytes.

```yaml signals
family: aws_sqs
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels: {account_id: <account>, region: <region>, namespace: AWS/SQS, job: cloud/aws/sqs, name: <declared-collector-name>}
dimension_profiles:
  queue: [dimension_QueueName]
metrics:
  - {root: approximate_age_of_oldest_message, type: gauge, unit: seconds, profiles: [queue]}
  - {root: approximate_number_of_messages_delayed, type: gauge, unit: count, profiles: [queue]}
  - {root: approximate_number_of_messages_not_visible, type: gauge, unit: count, profiles: [queue]}
  - {root: approximate_number_of_messages_visible, type: gauge, unit: count, profiles: [queue]}
  - {root: number_of_empty_receives, type: gauge, unit: count, profiles: [queue]}
  - {root: number_of_messages_deleted, type: gauge, unit: count, profiles: [queue]}
  - {root: number_of_messages_received, type: gauge, unit: count, profiles: [queue]}
  - {root: number_of_messages_sent, type: gauge, unit: count, profiles: [queue]}
  - {root: sent_message_size, type: gauge, unit: bytes, profiles: [queue]}
```

Withheld: `approximate_age_of_oldest_message_in_quiet_groups`,
`approximate_number_of_messages_delayed_in_quiet_groups`,
`approximate_number_of_messages_not_visible_in_quiet_groups`,
`approximate_number_of_messages_visible_in_quiet_groups`, `approximate_number_of_noisy_groups`
(no fair-queue model) and unsuffixed `aws_sqs_info` (no admitted metadata projection).

### DynamoDB emitted subset [slug: cw-dynamodb-emitted]

Provisioned tables require explicit TableName and non-global `collector_name`. Account quotas and
utilization snapshots emit once without table identity. Consumed capacity is sampled per second,
including idle zero traffic; request latency/items/Streams observations are sparse. Latency is
service-side only, never client/network time. Supported successful table operations are GetItem,
PutItem, Query, Scan and SELECT-only ExecuteStatement. ReturnedItemCount belongs only to Query,
Scan and SELECT ExecuteStatement. Declared Streams GetRecords uses StreamLabel+Operation+TableName;
it is never union-filled into table-operation observations. Write Source is omitted, Customer or
GlobalTable; GlobalTable replication is independently declared applied writes, not PutItem calls.
Quotas, provisioned capacity, per-request capacity/latency and fixed whole returned items/records/
bytes are explicit assumptions. Already-earned operation, conditional-failure, Streams-poll and
replication fractions survive cadence jitter and long observation gaps; new accrual is capped at
60 seconds per observation without replaying the gap. Fractional rates/read units are valid;
floating-point floor can delay a request at a decimal exact boundary. No overload, on-demand or
cross-service join is claimed.

```yaml signals
family: aws_dynamodb
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels: {account_id: <account>, region: <region>, namespace: AWS/DynamoDB, job: cloud/aws/dynamodb, name: global|<declared-collector-name>}
dimension_profiles:
  account: []
  table: [dimension_TableName]
  source_table: [dimension_Source, dimension_TableName]
  operation_table: [dimension_Operation, dimension_TableName]
  stream: [dimension_Operation, dimension_StreamLabel, dimension_TableName]
metrics:
  - {root: account_max_reads, type: gauge, unit: count, profiles: [account]}
  - {root: account_max_writes, type: gauge, unit: count, profiles: [account]}
  - {root: account_max_table_level_reads, type: gauge, unit: count, profiles: [account]}
  - {root: account_max_table_level_writes, type: gauge, unit: count, profiles: [account]}
  - {root: account_provisioned_read_capacity_utilization, type: gauge, unit: percent, profiles: [account]}
  - {root: account_provisioned_write_capacity_utilization, type: gauge, unit: percent, profiles: [account]}
  - {root: max_provisioned_table_read_capacity_utilization, type: gauge, unit: percent, profiles: [account]}
  - {root: max_provisioned_table_write_capacity_utilization, type: gauge, unit: percent, profiles: [account]}
  - {root: conditional_check_failed_requests, type: gauge, unit: count, profiles: [table]}
  - {root: consumed_read_capacity_units, type: gauge, unit: count, profiles: [table]}
  - {root: consumed_write_capacity_units, type: gauge, unit: count, profiles: [table, source_table]}
  - {root: returned_bytes, type: gauge, unit: bytes, profiles: [stream]}
  - {root: returned_records_count, type: gauge, unit: count, profiles: [stream]}
  - {root: returned_item_count, type: gauge, unit: count, profiles: [operation_table]}
  - {root: successful_request_latency, type: gauge, unit: milliseconds, profiles: [operation_table, stream]}
```

Unsuffixed `aws_dynamodb_info` remains withheld; the observed metadata shape does not establish
an explicit synthetic metadata projection. See [authoring](../docs/blueprints.md#captured-cloudwatch-consumers)
for configuration and model limits. These blocks add no Glue expansion, new source/unit evidence
or native admission.

### Redshift emitted subset [slug: cw-redshift-emitted]

Provisioned clusters require explicit ClusterIdentifier and unique non-global `collector_name`.
The six snapshot fields are required, including zero/false: CPU and disk usage are percentages
0–100 (not fractions); connections/tables and queue waiting/running are whole counts; healthy
and maintenance project explicit boolean 0/1. Each stock observes once per active reporting
window: sum/average/minimum/maximum equal the stock and sample_count is 1. Optional named WLM
queues have ClusterIdentifier+QueueName; no queue dimension leaks into cluster/latency metrics.
Optional independent read/write completed-I/O rates observe a fixed positive latency in **seconds**:
N newly earned observations at L give sum=N×L, average/minimum/maximum=L, sample_count=N.
N=0 omits that latency family. Neither queue stocks nor CPU infer arrivals or I/O throughput.

The selected vendor-dimension projections are P18 (cluster) and P14 (named queue), independently
confirmed by API forms and [AWS Redshift metrics](https://docs.aws.amazon.com/redshift/latest/mgmt/metrics-listing.html).
These are not full captured collector envelopes: captured tags and `asserts_env` are omitted.
No ARN syntax is inferred for collector names. Stock sampling and per-I/O observation counts are
explicit synthetic assumptions, not AWS sample-count/publication or captured numerical fidelity.

```yaml signals
family: aws_redshift
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels: {account_id: <account>, region: <region>, namespace: AWS/Redshift, job: cloud/aws/redshift, name: <declared-collector-name>}
dimension_profiles:
  cluster: [dimension_ClusterIdentifier]
  queue: [dimension_ClusterIdentifier, dimension_QueueName]
metrics:
  - {root: cpuutilization, type: gauge, unit: percent, profiles: [cluster]}
  - {root: database_connections, type: gauge, unit: count, profiles: [cluster]}
  - {root: health_status, type: gauge, unit: count, profiles: [cluster]}
  - {root: maintenance_mode, type: gauge, unit: count, profiles: [cluster]}
  - {root: percentage_disk_space_used, type: gauge, unit: percent, profiles: [cluster]}
  - {root: total_table_count, type: gauge, unit: count, profiles: [cluster]}
  - {root: read_latency, type: gauge, unit: seconds, profiles: [cluster]}
  - {root: write_latency, type: gauge, unit: seconds, profiles: [cluster]}
  - {root: wlmqueue_length, type: gauge, unit: count, profiles: [queue]}
  - {root: wlmrunning_queries, type: gauge, unit: count, profiles: [queue]}
```

Withheld: NodeID, service-class/wlmid/priority/stage alternatives, user-query families (SELECT is
observed in P13 only, but numerical population/timing/statistic semantics remain held),
managed-storage associations (no naming exception), info and all other
unselected families. No automatic cluster pause, failure-mode, pipeline or cross-service adapter.

### CloudWatch Logs emitted subset [slug: cw-logs-emitted]

Intake from each independently declared log group selects only LogGroupName, the P36 vendor-dimension
projection with `name=global`; no captured enrichment is copied. Source names/units and the
uncompressed-byte meaning are confirmed by [AWS Logs metrics](https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/CloudWatch-Logs-Monitoring-CloudWatch-Metrics.html).
For N newly earned upload events, event observations have sum=N, average/minimum/maximum=1,
sample_count=N. Byte observations have sum=N×B, average/minimum/maximum=B, sample_count=N,
where B is the explicit whole **uncompressed** bytes per event. N=0 omits both families. The
AWS event unit is **None**, not Count; neither a unit label nor a scaling factor is introduced.
These are operator-supplied per-event sampling assumptions, not captured numerical fidelity or
AWS datapoint cadence. No log bodies or collector/Loki wire are rendered.

```yaml signals
family: aws_logs
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels: {account_id: <account>, region: <region>, namespace: AWS/Logs, job: cloud/aws/logs, name: global}
dimension_profiles:
  log_group: [dimension_LogGroupName]
  subscription: [dimension_DestinationType, dimension_FilterName, dimension_LogGroupName]
metrics:
  - {root: incoming_bytes, type: gauge, unit: bytes, profiles: [log_group]}
  - {root: incoming_log_events, type: gauge, unit: none, profiles: [log_group]}
  - {root: delivery_errors, type: gauge, unit: none, profiles: [subscription]}
  - {root: delivery_throttling, type: gauge, unit: none, profiles: [subscription]}
  - {root: forwarded_log_events, type: gauge, unit: none, profiles: [subscription]}
  - {root: forwarded_bytes, type: gauge, unit: bytes, profiles: [subscription]}
```

Optional `subscription_filters[]` under an existing log group select **P21 vendor dimensions
only**, with explicit FilterName and DestinationType **exactly `Lambda`**, and `name=global`.
The safe literal-positive projection of retained response SHA-256
`16565c9f119377f6385f1a7e39292ccdf44bea3c4c0b81dc43df0eb0c39e18bc`
establishes Lambda for each of DeliveryErrors, DeliveryThrottling, ForwardedLogEvents and
ForwardedBytes, all five suffixes each, in P21 and P24 (one positive row per suffix/profile).
P24 corroborates the selector, never union-fills tags or collector identity into P21. These are
positive/truncated names/keys/literal facts, not captured values, cadence, units or completeness.
The existing API dimension-name and official source references supply the raw names/dimensions
and documented meanings/units. Other destination literals/profiles remain unselected.

Delivery is **independent explicitly authored period-observation synthesis, not a delivery
simulator**. All four whole nonnegative finite values must be supplied: `delivery_errors` means
errored log events, not permanent losses; `delivery_throttling` means throttled log events, not
API requests; `forwarded_log_events` means forwarded events, not invocations/batches;
`forwarded_bytes` means **already-compressed bytes**. No compression multiplier, retry/loss,
subset/disjointness, temporal-lag, batch or intake relationship is inferred. Each metric observes
its supplied x once per synthetic active minute: sum/average/minimum/maximum=x, sample_count=1,
not x. Explicit x=0 is an authored zero observation, not an AWS idle-publication claim. Even a
zero-intake group progresses independently. Omitted filters preserve intake output unchanged.

The first ordinary active tick may observe exactly one bootstrap period under the existing
startup convention. Integer active-nanosecond progress retains earned fractions; only new elapsed
progress is capped at 60 seconds, with at most one observation per tick and no gap replay. The
pause/silent-resume/repeated-timestamp/attempt-consumption rules below apply equally. Positive
master amplitude does not scale authored snapshots. At most 100 filters total reuse the existing
resource safety bound; each event observation is at most 60,000,001 and compressed bytes at most
3,932,160,065,536, reusing the admitted intake-window/product bounds. These are engineering refusal
bounds, not AWS quotas or recommended values. No function ARN/identity, tags, native, by-data-source,
account aggregate, info, pipeline, log-wire or lifecycle attribution is added.

For these two new consumers, resource lists and required numerical pointers have no defaults.
The first ordinary active tick explicitly models a synthetic bootstrap minute, not prior measured
activity. Later strictly increasing observations cap **new** accrual at 60 seconds; missed windows
are not replayed and earned fractions survive gaps. Exact rational carry over the decoded binary
float rate and integer nanoseconds avoids epsilon-created events; decimal-intent boundaries may
still be delayed. Observed real master-zero pause emits nothing (including stocks), advances the
boundary and accrues nothing. First positive observation after a pause is silent, retaining carry;
initially paused instances do not bootstrap on resume. Positive master values leave rates and authored period values fixed;
pause/resume wholly between observations is unobservable. Writer failure consumes the attempted
window with no replay. No durable checkpoint, native/backend or deployment acceptance is implied.

## Retained positive CloudWatch observations — 2026-10-23 [slug: cw-captured]

This identity-elided series-key catalogue freezes **141 observed bases, 56 exact label-key
profiles and 308 namespace/base/profile/attribution shapes** across Lambda, SQS, DynamoDB,
Redshift, CloudWatch Logs and Glue. These are **positive observations only**: both Mimir
responses warned that results may be truncated (`querier.max-series-query-limit`). An unlisted
name, suffix or profile is not proof of absence, nor is source-filter membership proof of
resource ownership. Lexical ownership classification is inference; account aggregates and
unattributed observations must not be attributed to a declared resource. No identifying resource,
account, tag or other identity values are published here; the identity projection below retains
only whitelisted technology enums and aggregate equality/global counts.

Provenance: retained series responses SHA-256
`16565c9f119377f6385f1a7e39292ccdf44bea3c4c0b81dc43df0eb0c39e18bc` and
`290899b3c2430b798eb324b499bcbfde86c3e1d2c89cb105ec039f91811db56f`.
The series API supplies **names and keys, not sample values, AWS units or instrument types**.
Every non-`_info` base below was observed with **all five** suffixes
`_average`, `_maximum`, `_minimum`, `_sample_count`, `_sum` for every listed profile.
Every `_info` name was observed **unsuffixed only**. Gauge semantics come from the canonical
CW contract above, not from the series API. This table is a source/consumer contract, **not
an emitted-family declaration**: no new construct is implemented by this documentation.

Common exact keys (including the series-name key): `__name__`, `account_id`, `asserts_env`, `job`, `name`, `namespace`, `region`.
Each profile is exactly those keys **plus** the keys below. Profiles are alternatives, never
union-filled. A key omitted from a profile was absent only in those observed rows.
`asserts_env`, `asserts_site`, `service`, `scrape_job`, `name`, `job`, account/region metadata
and `tag_*` are collector/backend enrichment, **not vendor dimensions**. In particular,
`asserts_*` and the captured tag-key sets are not mandatory synthetic labels or values.
`blueprint` is synthkit scoping, not a captured AWS label. Only `dimension_*` keys project
vendor dimensions, subject to exact source-name confirmation; never derive native AWS spelling
by reversing a mangled label. The additional safe CloudWatch API listing independently confirms
Redshift's raw dimension `service class` for the observed `dimension_service_class` key, including
NodeID forms; it is not inferred from that key. Info series have no resource dimensions and are
metadata, not CloudWatch source metrics.

Additional source evidence: an identity-elided, positive-only CloudWatch API listing retains
actual MetricName and dimension **names**, but no values or units. Recently-active listing can
retain old/deleted identities: it proves neither present resources, complete coverage, numeric
behavior nor cleanup status. It confirms the numeric Glue executor spellings and
`glue.driver.aggregate.jvmGCTime`, `glue.driver.bytesWritten`,
`glue.driver.disk.used.percentage`, plus Redshift `UserQueriesSucceeded`/`UserQueryDuration`.
Units still require independent official references or scoped historical unit evidence below. The listing's
`RedshiftManagedStorage{BilledUsedSpace,DsUsedSpace,TotalCapacity}` names do **not** map to the
observed `aws_redshift_managed_storage_*` bases under the canonical naming law (which would
retain the extra `redshift` token); these three associations remain PENDING, not a guessed
collector-prefix exception. API source SHA-256: Glue
`e7bd7434b006f382cb1c015a87082cf741d8db2bd8e2c60ee53dc70dbea57b11`, Redshift
`becc3f58f5abab2ed3cc4d56060df92bcb2b3ae6086080ec2a5016658f419615`.

### Positive identity-relation projections

A second safe projection retains only namespace/profile row counts, `name == global`,
FunctionName/Resource equality counts and whitelisted technology enums. These are grouped
**response rows**, not unique resources, metric values or per-metric identity rules. They
remain positive/truncated evidence; profiles outside this metric catalogue are not admitted.

- Lambda P29: FunctionName equals Resource in all 135 compared rows; P28: equal in all
  70 compared rows. Do **not** append a version or alias to Resource by assumption. P29/P30
  have zero global-name rows (135 rows each); P28/P27 have all 70 rows global each, despite
  carrying function dimensions. A non-global name establishes neither ARN syntax nor its
  relation to FunctionName. Equality is not asserted for unobserved functions or profiles.
- DynamoDB P44 (20 rows) retains Operation values `GetItem`, `PutItem`, `Query`; do not
  distribute this grouped set to every metric or infer missing values. P43's empty retained
  enum set does not prove an absent Operation value. Observed DynamoDB tagged profiles have
  zero global-name rows, without establishing the non-global string's format.
- Glue P35/P34/P33/P32 have respectively 260/240/60/60 rows, all `name=global`, even with
  job/run dimensions. P35/P34 retain Type values `count`, `gauge`; P33/P32 retain `gauge`.
  These sets are profile-level, not per-metric assignments. Vendor Type does not change the
  canonical per-period Prometheus gauge instrument.
- Logs P21/P36 have all 20/40 rows global; tagged P24/P40 have zero global-name rows out of
  20/40. SQS P1/P4 likewise have zero global-name rows out of 135/2. Do not infer that metadata
  tags alone determine name projection or that every non-global name is a resource ARN.

These facts constrain consumer identity assumptions without publishing any actual resource,
account, tag, function, queue, table, job or run identity. They neither widen attribution nor
constitute collector/Loki output or cleanup evidence.

### Positive historical unit metadata — per raw dimension-name profile only

A safe projection of historical CloudWatch datapoints supplies the following positive unit
metadata, point counts and raw-response hashes. **No complete-corpus or all-profile unit claim**:
each U row is limited to its raw dimension-name case, with real identities elided. It does not
establish units for every resource or collector-enrichment profile sharing those keys, numerical
sample values, statistic relationships, scaling behavior, native-wire units or live publication.
Unit cells marked `(U# only)` below reference this table; other label-key profiles in the same
metric row stay unverified by this evidence. Unqualified unit cells remain vendor-doc sourced.
An empty raw dimension list is an account aggregate, not a per-function metric.
Namespaces are AWS/Lambda for U1–U9, AWS/Redshift for U10–U11 and Glue for U12.

| Ref | Raw AWS name | Raw dimension names | Observed Unit | Points | Raw response SHA-256 |
|---|---|---|---|---:|---|
| U1 | `AsyncEventAge` | FunctionName | Milliseconds | 18 | `1eb17956f3bd4e7278ed14b132a443e4dc2541d51897545375b080bb409a6d94` |
| U2 | `AsyncEventsDropped` | FunctionName | Count | 18 | `a0ec45270b14b9300557160b03907e9072bb7403bb7be4e002171e9a78c5a51a` |
| U3 | `AsyncEventsReceived` | FunctionName | Count | 19 | `bb15d650a731cc6e13d88253e4bf8081b350794b062f8c8935492b8865bb4b5a` |
| U4 | `ClaimedAccountConcurrency` | none | Count | 19 | `4851e22249201d10c3c7a377356c15d33ee2f270a2eb2434bd3e6be11d6675cb` |
| U5 | `ConcurrentExecutions` | FunctionName | Count | 18 | `3cddd9bc797d3c47f8a33805a2d5e7ee6b9534ca1b36394c767f227365a7037f` |
| U6 | `Errors` | FunctionName | Count | 19 | `f7ba7e8125858ed04d4dbabc2c13ece3b98f3765c4a44944d1987909a6c2307d` |
| U7 | `Invocations` | FunctionName | Count | 19 | `b1f40bd08c3edb5cc605b7fe3d84c39f793ecd3e0cdb7b836cd09404db5ce2df` |
| U8 | `Throttles` | FunctionName | Count | 19 | `bb5593d639f512f2c5b33665cc5eb00f917a3193ea89da6053be748c479943e0` |
| U9 | `UnreservedConcurrentExecutions` | none | Count | 19 | `646e244ff45d12870947496d6d0ea54f77ca7fd52938b37dd9895ac061189a9e` |
| U10 | `UserQueriesSucceeded` | ClusterIdentifier, QueryType | Count | 18 | `806fc2ab3e564705804d4847031fd69157e18ed7cc4c1e38afb6da1fd93c2d2b` |
| U11 | `UserQueryDuration` | ClusterIdentifier, QueryType | Microseconds | 18 | `f23bd4de869bfc21361e0cc7284950087668548834721f395dc635dd7bdd378a` |
| U12 | `glue.driver.aggregate.jvmGCTime` | JobName, JobRunId, Type | Count | 1 | `256f1998b4b0043158069077c903dc7e30e36ce4594efefcc01cbe030bdc1ccb` |

`DeletedEventCount`, `FailedInvokeEventCount`, `InvokedEventCount` and `PolledEventCount` have
**no datapoints in the scoped historical candidates**. Their units remain PENDING, not inferred
Count from their names; this is not evidence that those metrics never publish. U12 says Count,
not milliseconds despite `Time` in the name; do not infer elapsed GC duration or a conversion
from it. The documented CW-to-UCUM translations are ms/us/{Count} for the corresponding units,
but these historical unit strings are not a native-wire capture or all-profile admission.

### Official source references and consumer rules

Current official AWS pages were retrieved with `curl` on 2026-10-23:

- [Lambda metric types](https://docs.aws.amazon.com/lambda/latest/dg/monitoring-metrics-types.html):
  “Invocation metrics are binary indicators of the outcome” and “Lambda sends the Errors metric
  with a value of 1”; `Duration` is “time in milliseconds” and `IteratorAge` is “age ... in
  milliseconds”. The page names all 15 observed non-info metrics, but omits a formal AWS unit
  for the count metrics and `AsyncEventAge`. U1–U9 now supply positive historical units for
  their specific raw dimension-name cases, including Milliseconds for AsyncEventAge. Four ESM
  units and all unobserved profile extrapolations remain PENDING; numeric scale is not captured.
  [Viewing Lambda metrics](https://docs.aws.amazon.com/lambda/latest/dg/monitoring-metrics-view.html)
  names `FunctionName`, `Resource`, `EventSourceMappingUUID` and “Across All Functions (none)”.
  Event-count metrics require explicit event-source-mapping metrics opt-in. The UUID-only
  profiles are unattributed; do not associate them with a particular captured function.
- [SQS metrics](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-available-cloudwatch-metrics.html):
  “Amazon SQS metrics in CloudWatch use a single dimension: QueueName.” The table explicitly
  gives Seconds for both observed age metrics, Bytes for `SentMessageSize`, and Count for
  the other observed metrics. “All metrics emit non-negative values only when the queue is
  active.” Quiet-group/noisy-group metrics require fair queues; do not infer that every
  queue publishes them. `service` and `asserts_site` in the profiles are not SQS dimensions.
- [DynamoDB metrics and dimensions](https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/metrics-dimensions.html):
  “Not all statistics, such as Average or Sum, are applicable for every metric. However, all
  of these values are available”. The observed five suffixes do not override recommended
  statistics. `ConsumedWriteCapacityUnits` documents `Source` as `Customer` or `GlobalTable`;
  the key-only evidence does not select a value. `StreamLabel` is used with DynamoDB Streams
  `GetRecords`, not arbitrary table operations. `SuccessfulRequestLatency` is Milliseconds
  and “doesn't consider network latency or client-side activity”. Account metrics are
  account/region aggregates, not table-tagged series. Consumed-capacity sample counts can
  exist at zero traffic; do not apply the earlier idle-absence generalization to these rows.
- [Redshift metrics](https://docs.aws.amazon.com/redshift/latest/mgmt/metrics-listing.html):
  `QueryRuntimeBreakdown` has “Units: Milliseconds”; `ReadLatency`/`WriteLatency` have
  “Units: Seconds”. `ReadThroughput`/`WriteThroughput` say “Units: Bytes” despite their
  rate descriptions: retain that documented unit, do not silently substitute Bytes/Second.
  The five original source gaps are not established by this page; the safe API listing confirms
  `UserQueriesSucceeded`/`UserQueryDuration` spellings but supplies no units. Historical U10/U11
  independently supply Count/Microseconds for ClusterIdentifier+QueryType only. In particular
  `RedshiftManagedStorageTotalCapacity` is not evidence for the differently named observed
  `managed_storage_total_capacity`. Some observed NodeID profiles exceed this page's
  documented dimension forms; the safe API listing independently confirms those raw NodeID
  forms. Mimir observation alone would not establish their AWS spelling.
- [CloudWatch Logs metrics](https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/CloudWatch-Logs-Monitoring-CloudWatch-Metrics.html):
  `IncomingBytes` is “uncompressed bytes”; `ForwardedBytes` is “compressed bytes”. The
  event/error/count rows below explicitly say “Units: None”, **not Count**. The two
  `*_by_data_source` source-name/unit pairs remain PENDING in this retrieved reference.
- [Glue profiler metrics](https://docs.aws.amazon.com/glue/latest/dg/monitoring-awsglue-with-cloudwatch-metrics.html)
  explicitly names the `Glue` namespace, dotted source names and units below; “AWS Glue reports
  metrics to CloudWatch every 30 seconds”. Existing exact native aliases are retained.
  Its heap-usage/CPU wording describes “fraction ... (scale: 0-1)” but gives “Unit: Percentage”;
  this capture has no values to resolve the scale. Do not rescale values from the series keys.
- [Glue observability metrics](https://docs.aws.amazon.com/glue/latest/dg/monitor-observability.html)
  names `glue.succeed.ALL`: “Total number of successful job runs”, `Unit: Count`, dimensions
  JobName/JobRunId/Type/ObservabilityGroup. This is positive outcome-metric evidence, not a
  general EventBridge/API-only verdict. Literal/enumerated names below follow this page.
  The page spells `glue.driver.bytesWrittten` (three t's) and
  `glue.driver.disk.used.percentage]` (trailing bracket). The additional safe API listing supplies
  the actual spellings independently, resolving these two name discrepancies; the corresponding
  documented rows give Bytes and Percentage respectively. The profiler's `executorId` template
  supplies units for the API-confirmed executor `1` names. `glue.driver.aggregate.jvmGCTime`
  is API-confirmed; historical U12 supplies Count for JobName+JobRunId+Type only, with no
  elapsed-time interpretation. “Percentage” is retained verbatim.
- [CloudWatch OTLP translation](https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/CloudWatch-metric-streams-formats-opentelemetry-translation-100.html):
  “The Summary datapoint enables CloudWatch to export all of these statistics using one
  datapoint.” Count maps to `{Count}`, Milliseconds to `ms`, Bytes to `By`, None to `1`.
  This is a documented native contract, not native-wire evidence from these series queries.
  Existing Glue aliases map Percentage to `%`; the current translation table lists Percent,
  not Percentage. New Percentage aliases need a source decision before native admission.

**Frozen first-consumer seam:** Lambda, SQS and DynamoDB consumers pass the literal bases below
and independently built per-metric `cw.StatSet` values to `cw.EmitStats`; never use cumulative
`Add`, invent stat relationships from names, or merge dimension profiles. All five outputs
are per-period gauges; `_sample_count` is a count, not a duration/byte/percent value. Native
OTLP additionally needs the exact raw name, unit translation and allowed AWS dimensions;
PENDING cells authorize no native alias. `_info` is a separate metadata gauge, not input to
`cw.EmitStats`. No captured metadata value or inferred ownership may become fixture identity.
These observations contain **no collector or Loki wire** evidence: raw AWS log-event samples
establish neither Loki stream labels, collector profiles, nor output body shape.

### Exact observed label-key profiles

| Profile | Additional label keys (besides common keys) |
|---|---|
| P0 | none |
| P1 | `asserts_site`, `dimension_QueueName`, `service`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P2 | `asserts_site`, `dimension_QueueName`, `service`, `tag_Environment`, `tag_Owner`, `tag_Purpose`, `tag_Terraform` |
| P3 | `asserts_site`, `dimension_QueueName`, `service`, `tag_Name`, `tag_alpha_eksctl_io_cluster_name`, `tag_alpha_eksctl_io_eksctl_version`, `tag_alpha_eksctl_io_karpenter_name`, `tag_alpha_eksctl_io_karpenter_version`, `tag_eksctl_cluster_k8s_io_v1alpha1_cluster_name`, `tag_karpenter_sh_discovery` |
| P4 | `asserts_site`, `scrape_job`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P5 | `asserts_site`, `scrape_job`, `tag_Environment`, `tag_Owner`, `tag_Purpose`, `tag_Terraform` |
| P6 | `asserts_site`, `scrape_job`, `tag_Name`, `tag_alpha_eksctl_io_cluster_name`, `tag_alpha_eksctl_io_eksctl_version`, `tag_alpha_eksctl_io_karpenter_name`, `tag_alpha_eksctl_io_karpenter_version`, `tag_eksctl_cluster_k8s_io_v1alpha1_cluster_name`, `tag_karpenter_sh_discovery` |
| P7 | `dimension_Class`, `dimension_Resource`, `dimension_Service`, `dimension_Type` |
| P8 | `dimension_ClusterIdentifier`, `dimension_NodeID`, `dimension_QueueName`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P9 | `dimension_ClusterIdentifier`, `dimension_NodeID`, `dimension_service_class`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P10 | `dimension_ClusterIdentifier`, `dimension_NodeID`, `dimension_stage`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P11 | `dimension_ClusterIdentifier`, `dimension_NodeID`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P12 | `dimension_ClusterIdentifier`, `dimension_QueryPriority`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P13 | `dimension_ClusterIdentifier`, `dimension_QueryType`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P14 | `dimension_ClusterIdentifier`, `dimension_QueueName`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P15 | `dimension_ClusterIdentifier`, `dimension_service_class`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P16 | `dimension_ClusterIdentifier`, `dimension_stage`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P17 | `dimension_ClusterIdentifier`, `dimension_wlmid`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P18 | `dimension_ClusterIdentifier`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P19 | `dimension_DataSourceName` |
| P20 | `dimension_DataSourceName`, `dimension_DataSourceType` |
| P21 | `dimension_DestinationType`, `dimension_FilterName`, `dimension_LogGroupName` |
| P22 | `dimension_DestinationType`, `dimension_FilterName`, `dimension_LogGroupName`, `tag_Application`, `tag_Environment`, `tag_ManagedBy`, `tag_Repository`, `tag_Root` |
| P23 | `dimension_DestinationType`, `dimension_FilterName`, `dimension_LogGroupName`, `tag_Application`, `tag_ManagedBy`, `tag_Name`, `tag_Repository`, `tag_Root` |
| P24 | `dimension_DestinationType`, `dimension_FilterName`, `dimension_LogGroupName`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P25 | `dimension_DestinationType`, `dimension_FilterName`, `dimension_LogGroupName`, `tag_Environment`, `tag_Namespace`, `tag_Owner`, `tag_Purpose`, `tag_Terraform` |
| P26 | `dimension_EventSourceMappingUUID` |
| P27 | `dimension_FunctionName` |
| P28 | `dimension_FunctionName`, `dimension_Resource` |
| P29 | `dimension_FunctionName`, `dimension_Resource`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P30 | `dimension_FunctionName`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P31 | `dimension_InstanceType` |
| P32 | `dimension_JobName`, `dimension_JobRunId`, `dimension_ObservabilityGroup`, `dimension_Sink`, `dimension_Type` |
| P33 | `dimension_JobName`, `dimension_JobRunId`, `dimension_ObservabilityGroup`, `dimension_Source`, `dimension_Type` |
| P34 | `dimension_JobName`, `dimension_JobRunId`, `dimension_ObservabilityGroup`, `dimension_Type` |
| P35 | `dimension_JobName`, `dimension_JobRunId`, `dimension_Type` |
| P36 | `dimension_LogGroupName` |
| P37 | `dimension_LogGroupName`, `tag_Application`, `tag_Environment`, `tag_ManagedBy`, `tag_Repository`, `tag_Root` |
| P38 | `dimension_LogGroupName`, `tag_Application`, `tag_ManagedBy`, `tag_Name`, `tag_Repository`, `tag_Root` |
| P39 | `dimension_LogGroupName`, `tag_Application`, `tag_Purpose` |
| P40 | `dimension_LogGroupName`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P41 | `dimension_LogGroupName`, `tag_Environment`, `tag_Name`, `tag_Owner`, `tag_Purpose`, `tag_Terraform` |
| P42 | `dimension_LogGroupName`, `tag_Environment`, `tag_Namespace`, `tag_Owner`, `tag_Purpose`, `tag_Terraform` |
| P43 | `dimension_Operation`, `dimension_StreamLabel`, `dimension_TableName`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P44 | `dimension_Operation`, `dimension_TableName`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P45 | `dimension_QueryType` |
| P46 | `dimension_Source`, `dimension_TableName`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P47 | `dimension_TableName`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P48 | `scrape_job`, `tag_Application`, `tag_Environment`, `tag_ManagedBy`, `tag_Repository`, `tag_Root` |
| P49 | `scrape_job`, `tag_Application`, `tag_ManagedBy`, `tag_Name`, `tag_Repository`, `tag_Root` |
| P50 | `scrape_job`, `tag_Application`, `tag_ManagedBy`, `tag_Repository`, `tag_Root` |
| P51 | `scrape_job`, `tag_Application`, `tag_Purpose` |
| P52 | `scrape_job`, `tag_Capture`, `tag_ManagedBy`, `tag_Root` |
| P53 | `scrape_job`, `tag_Environment`, `tag_Name`, `tag_Owner`, `tag_Purpose`, `tag_Terraform` |
| P54 | `scrape_job`, `tag_Environment`, `tag_Namespace`, `tag_Owner`, `tag_Purpose`, `tag_Terraform` |
| P55 | `scrape_job`, `tag_aws_cloudformation_logical_id`, `tag_aws_cloudformation_stack_id`, `tag_aws_cloudformation_stack_name`, `tag_dt_CreatedBy`, `tag_dt_EnvironmentID` |

### AWS/Lambda positive series-key observations [slug: cw-lambda]

| Observed Prometheus base | Raw AWS name | AWS unit | Exact profiles |
|---|---|---|---|
| `aws_lambda_async_event_age` | `AsyncEventAge` | Milliseconds (U1 only) | P0, P27, P28, P29, P30 |
| `aws_lambda_async_events_dropped` | `AsyncEventsDropped` | Count (U2 only) | P0, P27, P28, P29, P30 |
| `aws_lambda_async_events_received` | `AsyncEventsReceived` | Count (U3 only) | P0, P27, P28, P29, P30 |
| `aws_lambda_claimed_account_concurrency` | `ClaimedAccountConcurrency` | Count (U4 only) | P0 |
| `aws_lambda_concurrent_executions` | `ConcurrentExecutions` | Count (U5 only) | P0, P27, P28, P29, P30 |
| `aws_lambda_deleted_event_count` | `DeletedEventCount` | PENDING | P26 |
| `aws_lambda_duration` | `Duration` | Milliseconds | P0, P27, P28, P29, P30 |
| `aws_lambda_errors` | `Errors` | Count (U6 only) | P0, P27, P28, P29, P30 |
| `aws_lambda_failed_invoke_event_count` | `FailedInvokeEventCount` | PENDING | P26 |
| `aws_lambda_info` | collector metadata | not a CW unit | P52, P55 |
| `aws_lambda_invocations` | `Invocations` | Count (U7 only) | P0, P27, P28, P29, P30 |
| `aws_lambda_invoked_event_count` | `InvokedEventCount` | PENDING | P26 |
| `aws_lambda_iterator_age` | `IteratorAge` | Milliseconds | P26, P27, P28, P29, P30 |
| `aws_lambda_polled_event_count` | `PolledEventCount` | PENDING | P26 |
| `aws_lambda_throttles` | `Throttles` | Count (U8 only) | P0, P27, P28, P29, P30 |
| `aws_lambda_unreserved_concurrent_executions` | `UnreservedConcurrentExecutions` | Count (U9 only) | P0 |

### AWS/SQS positive series-key observations [slug: cw-sqs]

| Observed Prometheus base | Raw AWS name | AWS unit | Exact profiles |
|---|---|---|---|
| `aws_sqs_approximate_age_of_oldest_message` | `ApproximateAgeOfOldestMessage` | Seconds | P1, P2, P3 |
| `aws_sqs_approximate_age_of_oldest_message_in_quiet_groups` | `ApproximateAgeOfOldestMessageInQuietGroups` | Seconds | P1, P2, P3 |
| `aws_sqs_approximate_number_of_messages_delayed` | `ApproximateNumberOfMessagesDelayed` | Count | P1, P2, P3 |
| `aws_sqs_approximate_number_of_messages_delayed_in_quiet_groups` | `ApproximateNumberOfMessagesDelayedInQuietGroups` | Count | P1, P2, P3 |
| `aws_sqs_approximate_number_of_messages_not_visible` | `ApproximateNumberOfMessagesNotVisible` | Count | P1, P2, P3 |
| `aws_sqs_approximate_number_of_messages_not_visible_in_quiet_groups` | `ApproximateNumberOfMessagesNotVisibleInQuietGroups` | Count | P1, P2, P3 |
| `aws_sqs_approximate_number_of_messages_visible` | `ApproximateNumberOfMessagesVisible` | Count | P1, P2, P3 |
| `aws_sqs_approximate_number_of_messages_visible_in_quiet_groups` | `ApproximateNumberOfMessagesVisibleInQuietGroups` | Count | P1, P2, P3 |
| `aws_sqs_approximate_number_of_noisy_groups` | `ApproximateNumberOfNoisyGroups` | Count | P1, P2, P3 |
| `aws_sqs_info` | collector metadata | not a CW unit | P4, P5, P6 |
| `aws_sqs_number_of_empty_receives` | `NumberOfEmptyReceives` | Count | P1, P2, P3 |
| `aws_sqs_number_of_messages_deleted` | `NumberOfMessagesDeleted` | Count | P1, P2, P3 |
| `aws_sqs_number_of_messages_received` | `NumberOfMessagesReceived` | Count | P1, P2, P3 |
| `aws_sqs_number_of_messages_sent` | `NumberOfMessagesSent` | Count | P1, P2, P3 |
| `aws_sqs_sent_message_size` | `SentMessageSize` | Bytes | P1, P2, P3 |

### AWS/DynamoDB positive series-key observations [slug: cw-dynamodb]

| Observed Prometheus base | Raw AWS name | AWS unit | Exact profiles |
|---|---|---|---|
| `aws_dynamodb_account_max_reads` | `AccountMaxReads` | Count | P0 |
| `aws_dynamodb_account_max_table_level_reads` | `AccountMaxTableLevelReads` | Count | P0 |
| `aws_dynamodb_account_max_table_level_writes` | `AccountMaxTableLevelWrites` | Count | P0 |
| `aws_dynamodb_account_max_writes` | `AccountMaxWrites` | Count | P0 |
| `aws_dynamodb_account_provisioned_read_capacity_utilization` | `AccountProvisionedReadCapacityUtilization` | Percent | P0 |
| `aws_dynamodb_account_provisioned_write_capacity_utilization` | `AccountProvisionedWriteCapacityUtilization` | Percent | P0 |
| `aws_dynamodb_conditional_check_failed_requests` | `ConditionalCheckFailedRequests` | Count | P47 |
| `aws_dynamodb_consumed_read_capacity_units` | `ConsumedReadCapacityUnits` | Count | P47 |
| `aws_dynamodb_consumed_write_capacity_units` | `ConsumedWriteCapacityUnits` | Count | P46, P47 |
| `aws_dynamodb_info` | collector metadata | not a CW unit | P52 |
| `aws_dynamodb_max_provisioned_table_read_capacity_utilization` | `MaxProvisionedTableReadCapacityUtilization` | Percent | P0 |
| `aws_dynamodb_max_provisioned_table_write_capacity_utilization` | `MaxProvisionedTableWriteCapacityUtilization` | Percent | P0 |
| `aws_dynamodb_returned_bytes` | `ReturnedBytes` | Bytes | P43 |
| `aws_dynamodb_returned_item_count` | `ReturnedItemCount` | Count | P44 |
| `aws_dynamodb_returned_records_count` | `ReturnedRecordsCount` | Count | P43 |
| `aws_dynamodb_successful_request_latency` | `SuccessfulRequestLatency` | Milliseconds | P43, P44 |

### AWS/Redshift positive series-key observations [slug: cw-redshift]

| Observed Prometheus base | Raw AWS name | AWS unit | Exact profiles |
|---|---|---|---|
| `aws_redshift_commit_queue_length` | `CommitQueueLength` | Count | P11, P18 |
| `aws_redshift_concurrency_scaling_active_clusters` | `ConcurrencyScalingActiveClusters` | Count | P18 |
| `aws_redshift_cpuutilization` | `CPUUtilization` | Percent | P11, P18 |
| `aws_redshift_database_connections` | `DatabaseConnections` | Count | P11, P18 |
| `aws_redshift_health_status` | `HealthStatus` | Count | P11, P18 |
| `aws_redshift_info` | collector metadata | not a CW unit | P52 |
| `aws_redshift_maintenance_mode` | `MaintenanceMode` | Count | P11, P18 |
| `aws_redshift_managed_storage_billed_used_space` | PENDING | PENDING | P18, P31 |
| `aws_redshift_managed_storage_ds_used_space` | PENDING | PENDING | P18, P31 |
| `aws_redshift_managed_storage_total_capacity` | PENDING | PENDING | P18 |
| `aws_redshift_max_configured_concurrency_scaling_clusters` | `MaxConfiguredConcurrencyScalingClusters` | Count | P18 |
| `aws_redshift_network_receive_throughput` | `NetworkReceiveThroughput` | Bytes/Second | P11, P18 |
| `aws_redshift_network_transmit_throughput` | `NetworkTransmitThroughput` | Bytes/Second | P11, P18 |
| `aws_redshift_num_exceeded_schema_quotas` | `NumExceededSchemaQuotas` | Count | P18 |
| `aws_redshift_percentage_disk_space_used` | `PercentageDiskSpaceUsed` | Percent | P11, P18 |
| `aws_redshift_query_runtime_breakdown` | `QueryRuntimeBreakdown` | Milliseconds | P10, P16 |
| `aws_redshift_read_iops` | `ReadIOPS` | Count/Second | P11, P18 |
| `aws_redshift_read_latency` | `ReadLatency` | Seconds | P11, P18 |
| `aws_redshift_read_throughput` | `ReadThroughput` | Bytes | P11, P18 |
| `aws_redshift_total_table_count` | `TotalTableCount` | Count | P11, P18 |
| `aws_redshift_user_queries_succeeded` | `UserQueriesSucceeded` | Count (U10 only) | P13, P45 |
| `aws_redshift_user_query_duration` | `UserQueryDuration` | Microseconds (U11 only) | P13, P45 |
| `aws_redshift_wlmqueue_length` | `WLMQueueLength` | Count | P8, P9, P14, P15 |
| `aws_redshift_wlmqueue_wait_time` | `WLMQueueWaitTime` | Milliseconds | P12, P17 |
| `aws_redshift_wlmrunning_queries` | `WLMRunningQueries` | Count | P14, P17 |
| `aws_redshift_write_iops` | `WriteIOPS` | Count/Second | P11, P18 |
| `aws_redshift_write_latency` | `WriteLatency` | Seconds | P11, P18 |
| `aws_redshift_write_throughput` | `WriteThroughput` | Bytes | P11, P18 |

### AWS/Logs positive series-key observations [slug: cw-logs]

| Observed Prometheus base | Raw AWS name | AWS unit | Exact profiles |
|---|---|---|---|
| `aws_logs_call_count` | `CallCount` | None | P0, P7 |
| `aws_logs_delivery_errors` | `DeliveryErrors` | None | P21, P22, P23, P24, P25 |
| `aws_logs_delivery_throttling` | `DeliveryThrottling` | None | P21, P22, P23, P24 |
| `aws_logs_forwarded_bytes` | `ForwardedBytes` | Bytes | P21, P22, P23, P24 |
| `aws_logs_forwarded_log_events` | `ForwardedLogEvents` | None | P21, P22, P23, P24 |
| `aws_logs_incoming_bytes` | `IncomingBytes` | Bytes | P0, P36, P37, P38, P39, P40, P41, P42 |
| `aws_logs_incoming_bytes_by_data_source` | PENDING | PENDING | P19, P20 |
| `aws_logs_incoming_log_events` | `IncomingLogEvents` | None | P0, P36, P37, P38, P39, P40, P41, P42 |
| `aws_logs_incoming_log_events_by_data_source` | PENDING | PENDING | P19, P20 |
| `aws_logs_info` | collector metadata | not a CW unit | P48, P49, P50, P51, P52, P53, P54, P55 |

### Glue positive series-key observations [slug: cw-glue-captured]

| Observed Prometheus base | Raw AWS name | AWS unit | Exact profiles |
|---|---|---|---|
| `aws_glue_1_jvm_heap_usage` | `glue.1.jvm.heap.usage` | Percentage | P35 |
| `aws_glue_1_jvm_heap_used` | `glue.1.jvm.heap.used` | Bytes | P35 |
| `aws_glue_1_s3_filesystem_read_bytes` | `glue.1.s3.filesystem.read_bytes` | Bytes | P35 |
| `aws_glue_1_s3_filesystem_write_bytes` | `glue.1.s3.filesystem.write_bytes` | Bytes | P35 |
| `aws_glue_1_system_cpu_system_load` | `glue.1.system.cpuSystemLoad` | Percentage | P35 |
| `aws_glue_all_disk_available_gb` | `glue.ALL.disk.available_GB` | Gigabytes | P34 |
| `aws_glue_all_disk_used_gb` | `glue.ALL.disk.used_GB` | Gigabytes | P34 |
| `aws_glue_all_disk_used_percentage` | `glue.ALL.disk.used.percentage` | Percentage | P34 |
| `aws_glue_all_jvm_heap_usage` | `glue.ALL.jvm.heap.usage` | Percentage | P35 |
| `aws_glue_all_jvm_heap_used` | `glue.ALL.jvm.heap.used` | Bytes | P35 |
| `aws_glue_all_memory_heap_available` | `glue.ALL.memory.heap.available` | Bytes | P34 |
| `aws_glue_all_memory_heap_used` | `glue.ALL.memory.heap.used` | Bytes | P34 |
| `aws_glue_all_memory_heap_used_percentage` | `glue.ALL.memory.heap.used.percentage` | Percentage | P34 |
| `aws_glue_all_memory_non_heap_used` | `glue.ALL.memory.non-heap.used` | Bytes | P34 |
| `aws_glue_all_memory_non_heap_used_percentage` | `glue.ALL.memory.non-heap.used.percentage` | Percentage | P34 |
| `aws_glue_all_memory_total_used` | `glue.ALL.memory.total.used` | Bytes | P34 |
| `aws_glue_all_memory_total_used_percentage` | `glue.ALL.memory.total.used.percentage` | Percentage | P34 |
| `aws_glue_all_s3_filesystem_read_bytes` | `glue.ALL.s3.filesystem.read_bytes` | Bytes | P35 |
| `aws_glue_all_s3_filesystem_write_bytes` | `glue.ALL.s3.filesystem.write_bytes` | Bytes | P35 |
| `aws_glue_all_system_cpu_system_load` | `glue.ALL.system.cpuSystemLoad` | Percentage | P35 |
| `aws_glue_driver_aggregate_bytes_read` | `glue.driver.aggregate.bytesRead` | Bytes | P35 |
| `aws_glue_driver_aggregate_elapsed_time` | `glue.driver.aggregate.elapsedTime` | Milliseconds | P35 |
| `aws_glue_driver_aggregate_jvm_gctime` | `glue.driver.aggregate.jvmGCTime` | Count (U12 only) | P35 |
| `aws_glue_driver_aggregate_num_completed_stages` | `glue.driver.aggregate.numCompletedStages` | Count | P35 |
| `aws_glue_driver_aggregate_num_completed_tasks` | `glue.driver.aggregate.numCompletedTasks` | Count | P35 |
| `aws_glue_driver_aggregate_num_failed_tasks` | `glue.driver.aggregate.numFailedTasks` | Count | P35 |
| `aws_glue_driver_aggregate_num_killed_tasks` | `glue.driver.aggregate.numKilledTasks` | Count | P35 |
| `aws_glue_driver_aggregate_records_read` | `glue.driver.aggregate.recordsRead` | Count | P35 |
| `aws_glue_driver_aggregate_shuffle_bytes_written` | `glue.driver.aggregate.shuffleBytesWritten` | Bytes | P35 |
| `aws_glue_driver_aggregate_shuffle_local_bytes_read` | `glue.driver.aggregate.shuffleLocalBytesRead` | Bytes | P35 |
| `aws_glue_driver_block_manager_disk_disk_space_used_mb` | `glue.driver.BlockManager.disk.diskSpaceUsed_MB` | Megabytes | P35 |
| `aws_glue_driver_bytes_read` | `glue.driver.bytesRead` | Bytes | P33 |
| `aws_glue_driver_bytes_written` | `glue.driver.bytesWritten` | Bytes | P32 |
| `aws_glue_driver_disk_available_gb` | `glue.driver.disk.available_GB` | Gigabytes | P34 |
| `aws_glue_driver_disk_used_gb` | `glue.driver.disk.used_GB` | Gigabytes | P34 |
| `aws_glue_driver_disk_used_percentage` | `glue.driver.disk.used.percentage` | Percentage | P34 |
| `aws_glue_driver_files_read` | `glue.driver.filesRead` | Count | P33 |
| `aws_glue_driver_files_written` | `glue.driver.filesWritten` | Count | P32 |
| `aws_glue_driver_jvm_heap_usage` | `glue.driver.jvm.heap.usage` | Percentage | P35 |
| `aws_glue_driver_jvm_heap_used` | `glue.driver.jvm.heap.used` | Bytes | P35 |
| `aws_glue_driver_memory_heap_available` | `glue.driver.memory.heap.available` | Bytes | P34 |
| `aws_glue_driver_memory_heap_used` | `glue.driver.memory.heap.used` | Bytes | P34 |
| `aws_glue_driver_memory_heap_used_percentage` | `glue.driver.memory.heap.used.percentage` | Percentage | P34 |
| `aws_glue_driver_memory_non_heap_used` | `glue.driver.memory.non-heap.used` | Bytes | P34 |
| `aws_glue_driver_memory_non_heap_used_percentage` | `glue.driver.memory.non-heap.used.percentage` | Percentage | P34 |
| `aws_glue_driver_memory_total_used` | `glue.driver.memory.total.used` | Bytes | P34 |
| `aws_glue_driver_memory_total_used_percentage` | `glue.driver.memory.total.used.percentage` | Percentage | P34 |
| `aws_glue_driver_records_read` | `glue.driver.recordsRead` | Count | P33 |
| `aws_glue_driver_records_written` | `glue.driver.recordsWritten` | Count | P32 |
| `aws_glue_driver_s3_filesystem_read_bytes` | `glue.driver.s3.filesystem.read_bytes` | Bytes | P35 |
| `aws_glue_driver_s3_filesystem_write_bytes` | `glue.driver.s3.filesystem.write_bytes` | Bytes | P35 |
| `aws_glue_driver_skewness_job` | `glue.driver.skewness.job` | Count | P34 |
| `aws_glue_driver_skewness_stage` | `glue.driver.skewness.stage` | Count | P34 |
| `aws_glue_driver_system_cpu_system_load` | `glue.driver.system.cpuSystemLoad` | Percentage | P35 |
| `aws_glue_driver_worker_utilization` | `glue.driver.workerUtilization` | Percentage | P34 |
| `aws_glue_succeed_all` | `glue.succeed.ALL` | Count | P34 |

## ALB — `aws_applicationelb_*` ✅ [slug: cw-alb]
*Provenance: predecessor SIGNALS §2.9 + `research/aws-metric-streams.md` ALB (25 base names, lab-verified) + `emit/aws.go` + live EKS readback `reality-corpus/cw/eks-live-readback.json` (captured 2026-08-27; coverage roots emitted 2026-09-02).*

Series roots (all 5 stat suffixes apply per `[slug: cw-naming]`): `request_count`, `target_response_time` (seconds),
`httpcode_target_2_xx_count`, `httpcode_target_4_xx_count`, `httpcode_target_5_xx_count`,
`httpcode_elb_5_xx_count`, `healthy_host_count`, `un_healthy_host_count`, `active_connection_count`,
`new_connection_count`, `processed_bytes`, `target_connection_error_count`,
`client_tlsnegotiation_error_count`, `consumed_lcus`,
`desync_mitigation_mode_non_compliant_request_count`, `http_fixed_response_count`,
`http_redirect_count`, `httpcode_elb_3_xx_count`, `httpcode_elb_4_xx_count`,
`peak_lcus`, `request_count_per_target`, and `rule_evaluations`. Plus `aws_applicationelb_info`.

Dimensions are metric-family specific: every modelled ALB family has
`dimension_LoadBalancer` (`app/<name>/<hex-id>`) and `dimension_AvailabilityZone`; only
`request_count`, `target_response_time`, `httpcode_target_{2,4,5}_xx_count`,
`healthy_host_count`, `un_healthy_host_count`, and `target_connection_error_count` carry
`dimension_TargetGroup` (`targetgroup/<name>/<hex-id>`). `active_connection_count`,
`httpcode_elb_5_xx_count`, `new_connection_count`, and `processed_bytes` are load-balancer
families and must omit it. The captured coverage roots are more specific:
`request_count_per_target` carries LoadBalancer + TargetGroup + AvailabilityZone;
`client_tlsnegotiation_error_count`, `desync_mitigation_mode_non_compliant_request_count`,
`http_fixed_response_count`, `http_redirect_count`, and `httpcode_elb_{3,4}_xx_count` carry
LoadBalancer + AvailabilityZone; `consumed_lcus`, `peak_lcus`, and `rule_evaluations` carry
LoadBalancer only.

> ✅ **SKT-0024 correction (AWS docs retrieved 2026-08-29; EKS live-readback corpus captured
> 2026-08-27):** AWS documents `AvailabilityZone, LoadBalancer` for the four load-balancer
> families above, and target-group combinations for the retained families. The corpus independently
> confirms the four load-balancer families omit `dimension_TargetGroup`; the target-group forms of
> `request_count`, target response/HTTP code, and healthy-host families retain it.

⚠ Traps: `5_xx`/`4_xx` (NOT `5xx`/`4xx`); `un_healthy_host_count` (NOT `unhealthy`); `_sum` never
`rate()`; `target_response_time` = target processing only (excludes LB queue time).

```yaml signals
family: aws_applicationelb
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/ApplicationELB
  job: cloud/aws/applicationelb
  name: <resource-arn>|global
  dimension_LoadBalancer: app/<name>/<hex-id>
  dimension_TargetGroup: targetgroup/<name>/<hex-id> # only the target-group roots listed above
  dimension_AvailabilityZone: <az>
  tag_*: <resource-tags>
metrics:
  - {root: request_count, type: gauge, unit: count, v: ok}
  - {root: target_response_time, type: gauge, unit: seconds, v: ok, note: target processing only, excludes LB queue}
  - {root: httpcode_target_2_xx_count, type: gauge, unit: count, v: ok, note: "2_xx not 2xx"}
  - {root: httpcode_target_4_xx_count, type: gauge, unit: count, v: ok}
  - {root: httpcode_target_5_xx_count, type: gauge, unit: count, v: ok}
  - {root: httpcode_elb_5_xx_count, type: gauge, unit: count, v: ok}
  - {root: healthy_host_count, type: gauge, unit: count, v: ok}
  - {root: un_healthy_host_count, type: gauge, unit: count, v: trap, note: "un_healthy not unhealthy"}
  - {root: active_connection_count, type: gauge, unit: count, v: ok}
  - {root: new_connection_count, type: gauge, unit: count, v: ok}
  - {root: processed_bytes, type: gauge, unit: bytes, v: ok}
  - {root: target_connection_error_count, type: gauge, unit: count, v: ok}
  - {root: client_tlsnegotiation_error_count, type: gauge, unit: count, v: ok}
  - {root: consumed_lcus, type: gauge, unit: lcu, v: ok, note: dimension_LoadBalancer only in captured corpus}
  - {root: desync_mitigation_mode_non_compliant_request_count, type: gauge, unit: count, v: ok}
  - {root: http_fixed_response_count, type: gauge, unit: count, v: ok}
  - {root: http_redirect_count, type: gauge, unit: count, v: ok}
  - {root: httpcode_elb_3_xx_count, type: gauge, unit: count, v: ok}
  - {root: httpcode_elb_4_xx_count, type: gauge, unit: count, v: ok}
  - {root: peak_lcus, type: gauge, unit: lcu, v: ok, note: dimension_LoadBalancer only in captured corpus}
  - {root: request_count_per_target, type: gauge, unit: count, v: ok, note: includes dimension_TargetGroup}
  - {root: rule_evaluations, type: gauge, unit: count, v: ok, note: dimension_LoadBalancer only in captured corpus}
info_series: aws_applicationelb_info   # gauge=1, no stat suffix; no tag_VpcId
```

## NLB — `aws_networkelb_*` ✅ [slug: cw-nlb]
*Provenance: predecessor SIGNALS §2.9 + `research/aws-metric-streams.md` NLB (19 base names) + `emit/aws.go`.*

Roots: `active_flow_count`, `new_flow_count`, `processed_bytes`, `healthy_host_count`,
`un_healthy_host_count`, `port_allocation_error_count`, `tcp_client_reset_count`, `tcp_elb_reset_count`,
`tcp_target_reset_count`, `peak_bytes_per_second`, `peak_packets_per_second`. Plus `aws_networkelb_info`.
Dims as ALB; `dimension_LoadBalancer` = `net/<name>/<hex-id>`.

⚠ `un_healthy_host_count`; `_sum` never `rate()`; `active_flow_count` = end-to-end flows;
SG-rejected traffic is captured in NO metric.

```yaml signals
family: aws_networkelb
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/NetworkELB
  job: cloud/aws/networkelb
  name: <resource-arn>|global
  dimension_LoadBalancer: net/<name>/<hex-id>
  dimension_TargetGroup: targetgroup/<name>/<hex-id>
  dimension_AvailabilityZone: <az>
  tag_*: <resource-tags>
metrics:
  - {root: active_flow_count, type: gauge, unit: count, v: ok, note: end-to-end flows}
  - {root: new_flow_count, type: gauge, unit: count, v: ok}
  - {root: processed_bytes, type: gauge, unit: bytes, v: ok}
  - {root: healthy_host_count, type: gauge, unit: count, v: ok}
  - {root: un_healthy_host_count, type: gauge, unit: count, v: trap, note: "un_healthy not unhealthy"}
  - {root: port_allocation_error_count, type: gauge, unit: count, v: ok}
  - {root: tcp_client_reset_count, type: gauge, unit: count, v: ok}
  - {root: tcp_elb_reset_count, type: gauge, unit: count, v: ok}
  - {root: tcp_target_reset_count, type: gauge, unit: count, v: ok}
  - {root: peak_bytes_per_second, type: gauge, unit: bytes_per_second, v: ok}
  - {root: peak_packets_per_second, type: gauge, unit: packets_per_second, v: ok}
info_series: aws_networkelb_info
```

## EC2 — `aws_ec2_*` ✅ [slug: cw-ec2]
*Provenance: predecessor SIGNALS §2.9 + `research/aws-metric-streams.md` EC2 (23 base names) + `emit/aws.go` + live EKS readback `reality-corpus/cw/eks-live-readback.json` (captured 2026-08-27; coverage roots emitted 2026-09-02).*

Roots: `cpuutilization` (percent), `network_in`, `network_out`, `status_check_failed`,
`status_check_failed_instance`, `status_check_failed_system`, `status_check_failed_attached_ebs`,
`ebsread_bytes`, `ebswrite_bytes`, `ebsread_ops`, `ebswrite_ops`, `cpucredit_balance`,
`cpucredit_usage`, `cpusurplus_credit_balance`, `cpusurplus_credits_charged`,
`ebsbyte_balance_percent`, `ebsiobalance_percent`, `instance_ebsiopsexceeded_check`,
`instance_ebsthroughput_exceeded_check`, `metadata_no_token`, `metadata_no_token_rejected`,
`network_packets_in`, and `network_packets_out`. Plus `aws_ec2_info`.

Dimensions: `dimension_AutoScalingGroupName` (ASG/node-group level) or `dimension_InstanceId`
(per-instance) — both coexist; `name`=`"global"` for ASG-level, node ARN for instance-level.
The live corpus observed `instance_ebsiopsexceeded_check`,
`instance_ebsthroughput_exceeded_check`, and `metadata_no_token_rejected` with
`dimension_InstanceId` only; every other added root has both ASG and instance forms.

⚠ Traps: `cpuutilization` NO underscore; `ebsread_bytes`/`ebswrite_*` run together (`EBS` is an
acronym, split after the full token); `EBSReadBytes` here = instance-aggregate (per-volume lives in
`aws_ebs_*`); `DiskReadOps`/`DiskWriteOps` (instance store) NOT emitted; `cpucredit_balance` is
correct (`Credit`→`credit` splits because `B` in `Balance` is uppercase-followed-by-lowercase);
`_sum` never `rate()`.

> **EC2↔node identity (I12):** every `kube_node_info.provider_id`=`aws:///<az>/<instanceID>` must
> have a matching `aws_ec2_cpuutilization_average{dimension_InstanceId=<instanceID>}`. CPU values in
> both lanes derive from the same fixture seed and move together. Resolved ONCE by the composition
> root (the `fixture.Node` identity handed to both the k8s and EC2-CloudWatch constructs).

```yaml signals
family: aws_ec2
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/EC2
  job: cloud/aws/ec2
  name: <node-arn>|global
  dimension_AutoScalingGroupName: <asg-name>    # ASG-level (name=global)
  dimension_InstanceId: <instance-id>           # per-instance (name=node-arn); coexists with ASG
  tag_*: <resource-tags>
metrics:
  - {root: cpuutilization, type: gauge, unit: percent, v: ok, note: "no underscore (acronym run)"}
  - {root: network_in, type: gauge, unit: bytes, v: ok}
  - {root: network_out, type: gauge, unit: bytes, v: ok}
  - {root: status_check_failed, type: gauge, unit: count, v: ok}
  - {root: status_check_failed_instance, type: gauge, unit: count, v: ok}
  - {root: status_check_failed_system, type: gauge, unit: count, v: ok}
  - {root: status_check_failed_attached_ebs, type: gauge, unit: count, v: ok}
  - {root: ebsread_bytes, type: gauge, unit: bytes, v: ok, note: instance-aggregate; per-volume in aws_ebs_*}
  - {root: ebswrite_bytes, type: gauge, unit: bytes, v: ok}
  - {root: ebsread_ops, type: gauge, unit: count, v: ok}
  - {root: ebswrite_ops, type: gauge, unit: count, v: ok}
  - {root: cpucredit_balance, type: gauge, unit: count, v: ok}
  - {root: cpucredit_usage, type: gauge, unit: count, v: ok}
  - {root: cpusurplus_credit_balance, type: gauge, unit: count, v: ok}
  - {root: cpusurplus_credits_charged, type: gauge, unit: count, v: ok}
  - {root: ebsbyte_balance_percent, type: gauge, unit: percent, v: ok}
  - {root: ebsiobalance_percent, type: gauge, unit: percent, v: ok}
  - {root: instance_ebsiopsexceeded_check, type: gauge, unit: count, v: ok, note: dimension_InstanceId only}
  - {root: instance_ebsthroughput_exceeded_check, type: gauge, unit: count, v: ok, note: dimension_InstanceId only}
  - {root: metadata_no_token, type: gauge, unit: count, v: ok}
  - {root: metadata_no_token_rejected, type: gauge, unit: count, v: ok, note: dimension_InstanceId only}
  - {root: network_packets_in, type: gauge, unit: count, v: ok}
  - {root: network_packets_out, type: gauge, unit: count, v: ok}
info_series: aws_ec2_info
```

## EBS — `aws_ebs_*` ✅ [slug: cw-ebs]
*Provenance: predecessor SIGNALS §2.9 + `research/aws-metric-streams.md` EBS (18 base names) + `emit/aws.go` + live EKS readback `reality-corpus/cw/eks-live-readback.json` (captured 2026-08-27; coverage roots emitted 2026-09-02).*

Roots (dim `dimension_VolumeId`): `volume_read_bytes`, `volume_write_bytes`, `volume_read_ops`,
`volume_write_ops`, `volume_queue_length`, `burst_balance` (percent; gp2/st1/sc1 only),
`volume_avg_read_latency` (ms; Nitro only), `volume_avg_write_latency` (ms; Nitro only),
`volume_total_read_time` (s), `volume_total_write_time` (s), `volume_avg_iops`,
`volume_avg_throughput`, `volume_idle_time`, `volume_iopsexceeded_check`,
`volume_stalled_iocheck`, and `volume_throughput_exceeded_check`. Plus `aws_ebs_info`.
> ✅ **Live-verified (staff stack `cloud/aws/ebs`, 2026-06-13 — SK-6 EBS half):** `volume_avg_read_latency_*` /
> `volume_avg_write_latency_*` read ~0.5–1.0 → **milliseconds** confirmed; `dimension_VolumeId`=`vol-<17hex>`
> co-labeled with `dimension_InstanceId`. Full 5-stat suffix set per base.

⚠ `burst_balance` N/A on gp3/io1/io2; `volume_avg_*_latency` Nitro-only; `_sum` never `rate()`;
non-Nitro avg latency = `(volume_total_read_time_sum / volume_read_ops_sum) × 1000` ms.

```yaml signals
family: aws_ebs
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/EBS
  job: cloud/aws/ebs
  name: <resource-arn>|global
  dimension_VolumeId: vol-<17hex>
  dimension_InstanceId: <instance-id>     # co-labeled (live-verified)
  tag_*: <resource-tags>
metrics:
  - {root: volume_read_bytes, type: gauge, unit: bytes, v: ok}
  - {root: volume_write_bytes, type: gauge, unit: bytes, v: ok}
  - {root: volume_read_ops, type: gauge, unit: count, v: ok}
  - {root: volume_write_ops, type: gauge, unit: count, v: ok}
  - {root: volume_queue_length, type: gauge, unit: count, v: ok}
  - {root: burst_balance, type: gauge, unit: percent, v: ok, note: gp2/st1/sc1 only}
  - {root: volume_avg_read_latency, type: gauge, unit: ms, v: ok, note: Nitro-only (SK-6 live-verified)}
  - {root: volume_avg_write_latency, type: gauge, unit: ms, v: ok, note: Nitro-only}
  - {root: volume_total_read_time, type: gauge, unit: seconds, v: ok}
  - {root: volume_total_write_time, type: gauge, unit: seconds, v: ok}
  - {root: volume_avg_iops, type: gauge, unit: count_per_second, v: ok, note: includes dimension_InstanceId}
  - {root: volume_avg_throughput, type: gauge, unit: bytes_per_second, v: ok, note: includes dimension_InstanceId}
  - {root: volume_idle_time, type: gauge, unit: seconds, v: ok, note: dimension_VolumeId only}
  - {root: volume_iopsexceeded_check, type: gauge, unit: count, v: ok, note: includes dimension_InstanceId}
  - {root: volume_stalled_iocheck, type: gauge, unit: count, v: ok, note: includes dimension_InstanceId}
  - {root: volume_throughput_exceeded_check, type: gauge, unit: count, v: ok, note: includes dimension_InstanceId}
info_series: aws_ebs_info # gauge=1, no stat suffix; no dimension_VolumeId or tag_VpcId
```

## NAT Gateway — `aws_natgateway_*` ✅ [slug: cw-natgw]
*Provenance: predecessor SIGNALS §2.9 + `research/aws-metric-streams.md` NATGateway (15 base names) + `emit/aws.go` + live EKS readback `reality-corpus/cw/eks-live-readback.json` (captured 2026-08-27; coverage roots emitted 2026-09-02).*

Roots (dim `dimension_NatGatewayId`): `bytes_out_to_destination`, `error_port_allocation`,
`packets_drop_count`, `active_connection_count`, `connection_attempt_count` (SYN),
`connection_established_count`, `bytes_in_from_source`, `bytes_in_from_destination`,
`bytes_out_to_source`, the four directional `packets_*` roots,
`peak_bytes_per_second`, and `peak_packets_per_second`. Plus `aws_natgateway_info`.

⚠ `_sum` never `rate()`; no `_bucket` series; `error_port_allocation_sum`>0 = port exhaustion (55,000
limit per target IP+port in client-addr-translation mode).

```yaml signals
family: aws_natgateway
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/NATGateway
  job: cloud/aws/natgateway
  name: <resource-arn>|global
  dimension_NatGatewayId: <nat-gw-id>
  tag_*: <resource-tags>
metrics:
  - {root: bytes_out_to_destination, type: gauge, unit: bytes, v: ok}
  - {root: error_port_allocation, type: gauge, unit: count, v: ok, note: ">0 = port exhaustion"}
  - {root: packets_drop_count, type: gauge, unit: count, v: ok}
  - {root: active_connection_count, type: gauge, unit: count, v: ok}
  - {root: connection_attempt_count, type: gauge, unit: count, v: ok, note: SYN}
  - {root: connection_established_count, type: gauge, unit: count, v: ok}
  - {root: bytes_in_from_source, type: gauge, unit: bytes, v: ok}
  - {root: bytes_in_from_destination, type: gauge, unit: bytes, v: ok}
  - {root: bytes_out_to_source, type: gauge, unit: bytes, v: ok}
  - {root: packets_in_from_source, type: gauge, unit: count, v: ok}
  - {root: packets_in_from_destination, type: gauge, unit: count, v: ok}
  - {root: packets_out_to_source, type: gauge, unit: count, v: ok}
  - {root: packets_out_to_destination, type: gauge, unit: count, v: ok}
  - {root: peak_bytes_per_second, type: gauge, unit: bytes_per_second, v: ok}
  - {root: peak_packets_per_second, type: gauge, unit: packets_per_second, v: ok}
info_series: aws_natgateway_info
```

## S3 — `aws_s3_*` (storage ✅ · request metrics ✅ live-verified) [slug: cw-s3]
*Provenance: predecessor SIGNALS §2.9 + `research/aws-metric-streams.md` S3 + cantfind SK-1 + live staff stack capture 2026-06-13.*

Storage (daily gauge, always present): `aws_s3_bucket_size_bytes`, `aws_s3_number_of_objects` (dims
`dimension_BucketName`, `dimension_StorageType`). Plus `aws_s3_info`.
`dimension_StorageType` values: `StandardStorage`, `IntelligentTieringStorage`, `StandardIAStorage`,
`OneZoneIAStorage`, `ReducedRedundancyStorage`, `GlacierInstantRetrievalStorage`, `GlacierStorage`,
`DeepArchiveStorage`, `AllStorageTypes`.

Request metrics (1-min, OPT-IN per bucket via a metrics configuration): `aws_s3_all_requests`,
`aws_s3_{get,put,head,list,delete,post,select}_requests`, `aws_s3_4xx_errors`, `aws_s3_5xx_errors`,
`aws_s3_first_byte_latency` (ms), `aws_s3_total_request_latency` (ms), `aws_s3_bytes_{downloaded,uploaded}`.
> ✅ **Live-verified (staff stack, deployed bucket + EntireBucket metrics config, 2026-06-13 — SK-1 resolved).**
> Request series carry BOTH `dimension_FilterId` (= the metrics-configuration NAME, e.g. `EntireBucket`)
> AND `dimension_BucketName`, plus `name`(ARN)/`service`(=bucket)/account_id/region/tag_*. They follow
> the 5-stat law (`_sum/_average/_maximum/_minimum/_sample_count`). ⚠ **Latency percentiles are an
> EXTENDED stat, NOT part of the 5-stat law:** the GC AWS/S3 integration expects
> **`aws_s3_total_request_latency_p95`** (observed ≈60–120ms) which only flows when the metric stream's
> `statistics_configuration` adds `additional_statistics=["p95"]` for `TotalRequestLatency`. ⚠ Request
> series are INTERMITTENT — emitted only for periods with matching traffic (sparse per-period gauges;
> instant queries miss them, use a range window). Storage metrics are daily (midnight UTC); `_sum` never `rate()`.

```yaml signals
family: aws_s3
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/S3
  job: cloud/aws/s3
  name: <resource-arn>
  service: <bucket>                       # request series
  dimension_BucketName: <bucket>
  dimension_StorageType: StandardStorage|IntelligentTieringStorage|StandardIAStorage|OneZoneIAStorage|ReducedRedundancyStorage|GlacierInstantRetrievalStorage|GlacierStorage|DeepArchiveStorage|AllStorageTypes   # storage series
  dimension_FilterId: <metrics-config-name>   # request series, e.g. EntireBucket
  tag_*: <resource-tags>
metrics:
  # storage (daily gauge, always present)
  - {root: bucket_size_bytes, type: gauge, unit: bytes, v: ok, note: daily, dims BucketName+StorageType}
  - {root: number_of_objects, type: gauge, unit: count, v: ok, note: daily}
  # request metrics (opt-in per bucket; intermittent; dims FilterId+BucketName)
  - {root: all_requests, type: gauge, unit: count, v: ok}
  - {root: get_requests, type: gauge, unit: count, v: ok}
  - {root: put_requests, type: gauge, unit: count, v: ok}
  - {root: head_requests, type: gauge, unit: count, v: ok}
  - {root: list_requests, type: gauge, unit: count, v: ok}
  - {root: delete_requests, type: gauge, unit: count, v: ok}
  - {root: post_requests, type: gauge, unit: count, v: ok}
  - {root: select_requests, type: gauge, unit: count, v: ok}
  - {root: 4xx_errors, type: gauge, unit: count, v: ok}
  - {root: 5xx_errors, type: gauge, unit: count, v: ok}
  - {root: first_byte_latency, type: gauge, unit: ms, v: ok}
  - {root: total_request_latency, type: gauge, unit: ms, v: ok, note: "p95 extended-stat is operator-config, NOT in the 5-stat law"}
  - {root: bytes_downloaded, type: gauge, unit: bytes, v: ok}
  - {root: bytes_uploaded, type: gauge, unit: bytes, v: ok}
info_series: aws_s3_info
```

## EKS control plane — `aws_eks_*` ✅ (k8s ≥1.28, free) [slug: cw-eks]
*Provenance: predecessor SIGNALS §2.9 + `research/aws-metric-streams.md` EKS (30 base names) + `emit/aws.go`.*

Roots (dim `dimension_ClusterName`): `apiserver_request_total`, `apiserver_request_total_4_xx`,
`apiserver_request_total_5_xx`, `apiserver_request_duration_seconds_get_p99` (percentile EMBEDDED in
the CW name → `_average` is the stat applied to the p99 value), `scheduler_pending_pods`,
`etcd_mvcc_db_total_size_in_bytes`. Plus `aws_eks_info`.

⚠ `4_xx`/`5_xx`; ContainerInsights is a separate opt-in namespace, NOT covered here (node-level
metrics come from in-cluster k8s-monitoring, [`k8s.md`](k8s.md)); `_sum` never `rate()`.

```yaml signals
family: aws_eks
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/EKS
  job: cloud/aws/eks
  name: <resource-arn>|global
  dimension_ClusterName: <cluster-name>
  tag_*: <resource-tags>
metrics:
  - {root: apiserver_request_total, type: gauge, unit: count, v: ok}
  - {root: apiserver_request_total_4_xx, type: gauge, unit: count, v: ok}
  - {root: apiserver_request_total_5_xx, type: gauge, unit: count, v: ok}
  - {root: apiserver_request_duration_seconds_get_p99, type: gauge, unit: seconds, v: ok, note: p99 EMBEDDED in CW name; _average stat applied to the p99 value}
  - {root: scheduler_pending_pods, type: gauge, unit: count, v: ok}
  - {root: etcd_mvcc_db_total_size_in_bytes, type: gauge, unit: bytes, v: ok}
info_series: aws_eks_info
```

## Firehose — `aws_firehose_*` ✅ (pipeline-health: "the metric-stream pipeline watching itself") [slug: cw-firehose]
*Provenance: predecessor SIGNALS §2.9/§2.10 + `research/aws-metric-streams.md` Firehose + `emit/aws.go` + live EKS readback `reality-corpus/cw/eks-live-readback.json` (captured 2026-08-27; coverage roots emitted 2026-09-02). Direct PUT quota values independently checked against current AWS Firehose limits via Context7 on 2026-09-02.*

Roots (dim `dimension_DeliveryStreamName`): `delivery_to_http_endpoint_success` (fraction 0–1; ≈1.0
steady), `delivery_to_http_endpoint_data_freshness` (seconds lag; <60s steady),
`bytes_per_second_limit`, `delivery_to_http_endpoint_bytes`,
`delivery_to_http_endpoint_processed_bytes`, `delivery_to_http_endpoint_processed_records`,
`delivery_to_http_endpoint_records`, `describe_delivery_stream_latency`,
`describe_delivery_stream_requests`, `incoming_bytes`, `incoming_put_requests`,
`incoming_records`, `put_record_batch_bytes`, `put_record_batch_latency`,
`put_record_batch_records`, `put_record_batch_requests`, `put_record_bytes`,
`put_record_latency`, `put_record_requests`, `put_requests_per_second_limit`,
`records_per_second_limit`, and `throttled_records`. Plus `aws_firehose_info`.
⚠ `_sum` never `rate()`.

```yaml signals
family: aws_firehose
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/Firehose
  job: cloud/aws/firehose
  name: <resource-arn>|global
  dimension_DeliveryStreamName: <stream-name>
  tag_*: <resource-tags>
metrics:
  - {root: delivery_to_http_endpoint_success, type: gauge, unit: fraction, v: ok, note: 0-1, ~1.0 steady}
  - {root: delivery_to_http_endpoint_data_freshness, type: gauge, unit: seconds, v: ok, note: lag, <60s steady}
  - {root: bytes_per_second_limit, type: gauge, unit: bytes_per_second, v: ok}
  - {root: delivery_to_http_endpoint_bytes, type: gauge, unit: bytes, v: ok}
  - {root: delivery_to_http_endpoint_processed_bytes, type: gauge, unit: bytes, v: ok}
  - {root: delivery_to_http_endpoint_processed_records, type: gauge, unit: count, v: ok}
  - {root: delivery_to_http_endpoint_records, type: gauge, unit: count, v: ok}
  - {root: describe_delivery_stream_latency, type: gauge, unit: milliseconds, v: ok}
  - {root: describe_delivery_stream_requests, type: gauge, unit: count, v: ok}
  - {root: incoming_bytes, type: gauge, unit: bytes, v: ok}
  - {root: incoming_put_requests, type: gauge, unit: count, v: ok}
  - {root: incoming_records, type: gauge, unit: count, v: ok}
  - {root: put_record_batch_bytes, type: gauge, unit: bytes, v: ok}
  - {root: put_record_batch_latency, type: gauge, unit: milliseconds, v: ok}
  - {root: put_record_batch_records, type: gauge, unit: count, v: ok}
  - {root: put_record_batch_requests, type: gauge, unit: count, v: ok}
  - {root: put_record_bytes, type: gauge, unit: bytes, v: ok}
  - {root: put_record_latency, type: gauge, unit: milliseconds, v: ok}
  - {root: put_record_requests, type: gauge, unit: count, v: ok}
  - {root: put_requests_per_second_limit, type: gauge, unit: requests_per_second, v: ok}
  - {root: records_per_second_limit, type: gauge, unit: records_per_second, v: ok}
  - {root: throttled_records, type: gauge, unit: count, v: ok}
info_series: aws_firehose_info
```

## RDS — `aws_rds_*` (✅ standalone Postgres/MySQL modelled; Aurora families 📋 documented, not yet modelled) [slug: cw-rds]
*Provenance: live capture 2026-06-14 (a live reference EKS cluster, eu-west-1) (133 base metrics, 665 series across postgres / mysql / aurora-postgresql / aurora-mysql, each writer/reader/serverless-v2). SK-4/SK-5/SK-6 resolved.*

**Names are live-verbatim.** The `aws_rds_*` names below are the EXACT post-mangling series as ingested (AWS metric stream → Firehose → Mimir). CW→Prometheus mangling splits at lower→UPPER transitions but collapses consecutive-caps runs, so real names are a MIX — `cpuutilization`, `acuutilization`, `ddllatency`, `dmlthroughput` (caps-run collapse) vs `database_connections`, `read_iops`, `ebsbyte_balance_percent`, `free_local_storage` (split). The listed names ARE the contract; do not re-derive. Each base emits the five-stat family `_average/_maximum/_minimum/_sum/_sample_count` (per-period GAUGES; `_sum` never `rate()`).

**Label universe (every series):** `account_id`, `region`, `namespace="AWS/RDS"` (Aurora too — there is NO separate `AWS/Aurora` namespace), `job`, `name`(ARN), `asserts_env` (Grafana-injected), `dimension_DBInstanceIdentifier`. Aurora additionally carries `dimension_DBClusterIdentifier` (⚠ also observed as `dimension_DbClusterIdentifier` — BOTH casings seen in live capture), `dimension_Role` ∈ {`WRITER`,`READER`}, `dimension_EngineName`, `dimension_DatabaseClass` ∈ {`db.t3.micro`,`db.t3.medium`,`db.serverless`}.

**Universal core — present on ALL engines** (postgres/mysql/aurora-postgresql/aurora-mysql); ✅ synthkit emits this on the standalone instance:
`cpuutilization, database_connections, freeable_memory, read_iops, write_iops, read_latency, write_latency, read_throughput, write_throughput, network_receive_throughput, network_transmit_throughput, disk_queue_depth, swap_usage`. ⚠ `read_latency`/`write_latency` are in **seconds** (SK-6 live-verified). All engines also emit the EBS-budget pair `ebsbyte_balance_percent, ebsiobalance_percent` (📋 not yet emitted; literal CW `%`→`_percent`). t-class burstable instances additionally emit the CPU-credit family `cpucredit_balance, cpucredit_usage, cpusurplus_credit_balance, cpusurplus_credits_charged` (📋).

**Standalone PostgreSQL (`engine=postgres`):** ✅ `burst_balance, free_storage_space, transaction_logs_disk_usage, transaction_logs_generation, maximum_used_transaction_ids, replication_slot_disk_usage`; 📋 `oldest_replication_slot_lag, oldest_logical_replication_slot_lag, checkpoint_lag`. ⚠ `replica_lag` appears ONLY on the read-replica instance — a lone standalone primary does NOT emit it (synthkit corrected 2026-06-14 to drop the always-emitted `aws_rds_replica_lag`).

**Standalone MySQL (`engine=mysql`):** ✅ `burst_balance, free_storage_space`; 📋 `bin_log_disk_usage, lvmread_iops, lvmwrite_iops`.

**Aurora PostgreSQL (`engine=aurora-postgresql`; writer/reader/serverless-v2) — 📋 not modelled:** uses `free_local_storage` (NOT `free_storage_space`); serverless-WRITER-only `acuutilization, serverless_database_capacity`; `aurora_replica_lag` (+`_maximum`/`_minimum` on writer), `buffer_cache_hit_ratio, commit_latency, commit_throughput, deadlocks, engine_uptime, maximum_used_transaction_ids, transaction_logs_disk_usage, replication_slot_disk_usage, oldest_replication_slot_lag, to_aurora_postgre_sqlreplica_lag, storage_network_receive_throughput, storage_network_transmit_throughput, storage_network_throughput, network_throughput, temp_storage_iops, temp_storage_throughput, aurora_estimated_shared_memory_bytes`.

**Aurora MySQL (`engine=aurora-mysql`; writer/reader/serverless-v2) — 📋 not modelled, largest family (~105):** full DML set `{commit,ddl,dml,delete,insert,select,update}_latency` + `*_throughput` (⚠ **milliseconds**), `queries, active_transactions, num_active_transactions, blocked_transactions, deadlocks, aborted_clients, connection_attempts, login_failures, row_lock_time, rollback_segment_history_list_length, num_undo_row_operations`; write-forwarding `forwarding_writer_{dmllatency,dmlthroughput,open_sessions}`, `forwarding_replica_{dmllatency,dmlthroughput,open_sessions,read_wait_latency,read_wait_throughput,select_latency,select_throughput}`; parallel-query `aurora_pq_request_*` (~30 variants incl. `_attempted/_executed/_failed/_in_progress/_throttled/_chosen_pk_range_scan` and ~24 `_not_chosen_*` reasons); Aurora memory/OOM `aurora_memory_{health_state,num_declined_sql_total,num_kill_conn_total,num_kill_query_total}, aurora_num_oom_recovery_{successful,triggered}, aurora_milliseconds_spent_in_oom_recovery, aurora_slow_connection_handle_count, aurora_slow_handshake_count`; binlog `num_binary_log_files, sum_binary_log_size, aurora_binlog_replica_lag`; storage/volume `aurora_volume_bytes_left_total, volume_bytes_used, volume_read_iops, volume_write_iops`; lifecycle `purge_boundary, purge_finished_point, truncate_finished_point, transaction_age_maximum, aurora_dmlrejected_writer_full`.

Instance class is a blueprint `instance_class` knob (default `db.t3.medium`; SK-4 â declared attribute on `fixture.DB`, no class-derived series). (Raw per-engine/role capture taken 2026-06-14; full inventory not retained in-repo — signals/cw.md is now the contract of record.)

```yaml signals
family: aws_rds
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/RDS                      # Aurora too — no separate AWS/Aurora namespace
  job: cloud/aws/rds
  name: <resource-arn>
  asserts_env: <env>                      # Grafana-injected
  dimension_DBInstanceIdentifier: <db-instance-id>
  # Aurora-only (📋, not modelled): dimension_DBClusterIdentifier (also DbClusterIdentifier casing),
  #   dimension_Role {WRITER,READER}, dimension_EngineName, dimension_DatabaseClass {db.t3.micro,db.t3.medium,db.serverless}
metrics:   # ✅ EMITTED on the standalone instance only; Aurora + 📋 families are documented in prose above, NOT emitted
  # universal core (all engines)
  - {root: cpuutilization, type: gauge, unit: percent, v: ok}
  - {root: database_connections, type: gauge, unit: count, v: ok}
  - {root: freeable_memory, type: gauge, unit: bytes, v: ok}
  - {root: read_iops, type: gauge, unit: count_per_second, v: ok}
  - {root: write_iops, type: gauge, unit: count_per_second, v: ok}
  - {root: read_latency, type: gauge, unit: seconds, v: ok, note: SECONDS (SK-6 live-verified)}
  - {root: write_latency, type: gauge, unit: seconds, v: ok, note: SECONDS (SK-6 live-verified)}
  - {root: read_throughput, type: gauge, unit: bytes_per_second, v: ok}
  - {root: write_throughput, type: gauge, unit: bytes_per_second, v: ok}
  - {root: network_receive_throughput, type: gauge, unit: bytes_per_second, v: ok}
  - {root: network_transmit_throughput, type: gauge, unit: bytes_per_second, v: ok}
  - {root: disk_queue_depth, type: gauge, unit: count, v: ok}
  - {root: swap_usage, type: gauge, unit: bytes, v: ok}
  # standalone PostgreSQL / MySQL
  - {root: burst_balance, type: gauge, unit: percent, v: ok}
  - {root: free_storage_space, type: gauge, unit: bytes, v: ok}
  - {root: maximum_used_transaction_ids, type: gauge, unit: count, v: ok}
  - {root: replication_slot_disk_usage, type: gauge, unit: bytes, v: ok}
  - {root: transaction_logs_disk_usage, type: gauge, unit: bytes, v: ok}
  - {root: transaction_logs_generation, type: gauge, unit: bytes_per_second, v: ok}
documented_not_emitted: [ebsbyte_balance_percent, ebsiobalance_percent, cpucredit_balance, cpucredit_usage, cpusurplus_credit_balance, cpusurplus_credits_charged, "postgres: oldest_replication_slot_lag/oldest_logical_replication_slot_lag/checkpoint_lag", "postgres replica: replica_lag (read-replica ONLY)", "mysql: bin_log_disk_usage/lvmread_iops/lvmwrite_iops", "Aurora PG + Aurora MySQL families (see prose)"]
```

### RDS native Metric Streams source aliases [slug: cw-rds-native-aliases]

The four PostgreSQL roots below were labelled documented-not-emitted in this catalogue. That
status was stale: `internal/construct/rds/rds.go` emits them, and
`internal/cw/streamtable_rdsfamily.go` maps their exact namespace, source name and unit.
The root decision is to document these four exact native OTLP aliases, rather than treating all
documented-not-emitted prose as an emitted source contract. Name resolution remains name-only;
these rows neither prove a live deployment nor relax producer matching.

Source: [Amazon RDS instance metrics](https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/rds-metrics.html)
and [Metric Streams OTLP unit translation](https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/CloudWatch-metric-streams-formats-opentelemetry-translation-100.html),
as implemented in the reviewed mapping at commit `c6622f786fadc286061403234d1deee16eaeb30a`.
Each source Summary carries the period count and sum and the minimum/maximum quantiles; the
Prometheus path expands the corresponding base into five per-period gauges.

```yaml signals
family: cw_rds_native_aliases
scope: blueprint
sink: otlp
metrics:
  - {root: amazonaws.com/AWS/RDS/MaximumUsedTransactionIDs, type: summary, unit: "{Count}", v: ok, note: "source alias for aws_rds_maximum_used_transaction_ids; DBInstanceIdentifier dimension"}
  - {root: amazonaws.com/AWS/RDS/ReplicationSlotDiskUsage, type: summary, unit: By, v: ok, note: "source alias for aws_rds_replication_slot_disk_usage; DBInstanceIdentifier dimension"}
  - {root: amazonaws.com/AWS/RDS/TransactionLogsDiskUsage, type: summary, unit: By, v: ok, note: "source alias for aws_rds_transaction_logs_disk_usage; DBInstanceIdentifier dimension"}
  - {root: amazonaws.com/AWS/RDS/TransactionLogsGeneration, type: summary, unit: By/s, v: ok, note: "source alias for aws_rds_transaction_logs_generation; DBInstanceIdentifier dimension"}
```

## ElastiCache — `aws_elasticache_*` (✅ single-node Redis modelled; Valkey/Memcached/cluster-mode/tiering 📋 documented, not yet modelled) [slug: cw-elasticache]
*Provenance: live capture 2026-06-14 (a live reference EKS cluster, eu-west-1) (105 base metrics, 525 series across redis 7.1 / valkey 8.1 / valkey 9.0 / memcached 1.6, cluster-mode on+off, primary+replica). SK-5 resolved.*

**Names are live-verbatim** (post-mangling); five-stat family per base; `_sum` per-period gauge. **Label universe:** `account_id, region, namespace="AWS/ElastiCache", job, name, asserts_env, dimension_CacheClusterId, dimension_CacheNodeId`. Additional: `dimension_ReplicationGroupId` + `dimension_NodeGroupId` (cluster-mode shards), `dimension_Role` ∈ {`Primary`,`Replica`} (⚠ capitalised, unlike RDS WRITER/READER — only on cluster-mode `engine_cpuutilization`), `dimension_Tier` ∈ {`Memory`,`SSD`} (data-tiering; on `bytes_used_for_cache`, `curr_items`).

⚠ **Synthkit corrections (2026-06-14, live-confirmed absent):** `aws_elasticache_cache_hit_rate` and `aws_elasticache_get_type_cmds` are NOT real CW metrics (AWS never publishes derived ratios; no `GetTypeCmds`) — both REMOVED from synthkit. `set_type_cmds`, `key_based_cmds`, `non_key_type_cmds`(+`_latency`) ARE real (📋 the broader real command family beyond set_type_cmds is a phase-2 add). `replication_lag`, `is_master`, `master_link_health_status` are real EC metrics.

**Redis 7.1 ≡ Valkey 8.1 — identical core (~55 base), ✅ synthkit emits 24 of these on a single node:** `cpuutilization, engine_cpuutilization, curr_connections, new_connections, curr_items, curr_volatile_items, bytes_used_for_cache, database_memory_usage_percentage, database_capacity_usage_percentage, database_{memory,capacity}_usage_counted_for_evict_percentage, cache_hits, cache_misses, evictions, reclaimed, replication_bytes, replication_lag, freeable_memory, swap_usage, memory_fragmentation_ratio, allocator_fragmentation_{bytes,ratio}, active_defrag_hits, processed_commands, set_type_cmds, key_based_cmds, non_key_type_cmds, non_key_type_cmds_latency, error_count, blocked_connections, rejected_connections, save_in_progress, is_master, master_link_health_status, keys_tracked, used_memory_dataset, major_page_faults, pub_sub_channels, pub_sub_shard_channels, traffic_management_active, authentication_failures, {channel,command,key}_authorization_failures, iam_authentication_{expirations,throttling}, network_bytes_{in,out}, network_max_bytes_{in,out}, network_packets_{in,out}, network_max_packets_{in,out}, network_bandwidth_{in,out}_allowance_exceeded, network_baseline_{max_}usage_{in,out}_percentage, network_conntrack_allowance_exceeded, network_packets_per_second_allowance_exceeded, cpucredit_balance, cpucredit_usage`.

**Valkey 9.0 = Valkey 8.1 + 📋:** vector-search `search_number_of_indexes, search_total_indexed_documents, search_used_memory_bytes, search_write_cpuutilization, search_write_throttle_{active,clients_count,events}`; `reclaimed_fields`; data-tiering `bytes_read_from_disk, bytes_written_to_disk, num_items_read_from_disk, num_items_written_to_disk` (+ `dimension_Tier` on `bytes_used_for_cache`/`curr_items`).

**Memcached 1.6 — distinct family (~30 base, 📋):** `cmd_{get,set,flush,touch,config_get,config_set}, get_hits, get_misses, delete_{hits,misses}, incr_{hits,misses}, decr_{hits,misses}, cas_{hits,misses,badval}, touch_{hits,misses}, new_items, expired_unfetched, evicted_unfetched, reclaimed, bytes_used_for_cache_items, bytes_used_for_hash, bytes_read_into_memcached, bytes_written_out_from_memcached, curr_config, crawler_items_checked, unused_memory` plus the shared host-level `cpuutilization, cpucredit_{balance,usage}, freeable_memory, swap_usage, curr_{connections,items}, new_connections, evictions, database_memory_usage_percentage, network_*`.

**Cluster-mode-enabled** adds RG-level series granularities: `engine_cpuutilization` carries `dimension_Role`/`dimension_NodeGroupId`/`dimension_ReplicationGroupId`; `database_{memory,capacity}_usage_counted_for_evict_percentage` are keyed `dimension_NodeGroupId,dimension_ReplicationGroupId`. Per-node series additionally appear at `{CacheClusterId,CacheNodeId}` and `{CacheClusterId}` alone.

Instance class is a blueprint `instance_class` knob driving `database_memory_usage_percentage`/`freeable_memory` via the real AWS node-type usable-memory map (SK-5; `cache.r6g.large`=13.07 GiB). (Raw capture taken 2026-06-14; full inventory not retained in-repo.)

```yaml signals
family: aws_elasticache
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/ElastiCache
  job: cloud/aws/elasticache
  name: <resource-arn>
  asserts_env: <env>
  dimension_CacheClusterId: <cluster-id>
  dimension_CacheNodeId: <node-id>
  # cluster-mode/tiering (📋): dimension_ReplicationGroupId, dimension_NodeGroupId,
  #   dimension_Role {Primary,Replica} (capitalised, only on cluster-mode engine_cpuutilization),
  #   dimension_Tier {Memory,SSD} (on bytes_used_for_cache, curr_items)
metrics:   # ✅ EMITTED: 24 Redis 7.1 / Valkey 8.1 core on a single node. Valkey 9.0 / Memcached / cluster-mode are 📋 (prose).
  - {root: cpuutilization, type: gauge, unit: percent, v: ok}
  - {root: engine_cpuutilization, type: gauge, unit: percent, v: ok}
  - {root: curr_connections, type: gauge, unit: count, v: ok}
  - {root: new_connections, type: gauge, unit: count, v: ok}
  - {root: curr_items, type: gauge, unit: count, v: ok}
  - {root: curr_volatile_items, type: gauge, unit: count, v: ok}
  - {root: bytes_used_for_cache, type: gauge, unit: bytes, v: ok}
  - {root: database_memory_usage_percentage, type: gauge, unit: percent, v: ok}
  - {root: database_capacity_usage_percentage, type: gauge, unit: percent, v: ok}
  - {root: cache_hits, type: gauge, unit: count, v: ok}
  - {root: cache_misses, type: gauge, unit: count, v: ok}
  - {root: evictions, type: gauge, unit: count, v: ok}
  - {root: reclaimed, type: gauge, unit: count, v: ok}
  - {root: replication_bytes, type: gauge, unit: bytes, v: ok}
  - {root: replication_lag, type: gauge, unit: seconds, v: ok}
  - {root: freeable_memory, type: gauge, unit: bytes, v: ok}
  - {root: swap_usage, type: gauge, unit: bytes, v: ok}
  - {root: memory_fragmentation_ratio, type: gauge, unit: ratio, v: ok}
  - {root: processed_commands, type: gauge, unit: count, v: ok}
  - {root: set_type_cmds, type: gauge, unit: count, v: ok}
  - {root: error_count, type: gauge, unit: count, v: ok}
  - {root: blocked_connections, type: gauge, unit: count, v: ok}
  - {root: is_master, type: gauge, unit: bool, v: ok}
  - {root: master_link_health_status, type: gauge, unit: bool, v: ok}
removed_not_real: [cache_hit_rate, get_type_cmds]   # ⚠ AWS never publishes these — REMOVED from synthkit
note: "✅-emitted 24-set above is a subset of the ~55 live-captured Redis/Valkey core; the remainder + Valkey 9.0 vector-search/tiering + Memcached + cluster-mode RG-level series are 📋 (documented in prose, not emitted)."
```

## Amazon MWAA — `aws_mwaa_*` + `aws_amazonmwaa_*` ✅ modelled — SK-2 resolved [slug: cw-mwaa]
*Provenance: live capture 2026-06-14 (a live reference EKS cluster, eu-west-1; raw inventory not retained in-repo). Resolves cantfind SK-2: MWAA streams to TWO CloudWatch namespaces under TWO distinct Prometheus prefixes (the cantfind `aws_amazonmwaa_*`-only guess was half-right).*

**Two namespaces in one construct:**
- `AWS/MWAA` → prefix `aws_mwaa_*`: infra (Aurora-metadata-DB + worker/scheduler host metrics). `active_connection_count, approximate_age_of_oldest_task, queued_tasks, running_tasks` (env-level); `cpuutilization, memory_utilization` (`dimension_Cluster` ∈ {`AdditionalWorker`,`BaseWorker`,`Scheduler`,`WebServer`}); `database_connections, disk_queue_depth, freeable_memory, read_iops, write_iops, read_latency, write_latency, read_throughput, write_throughput, network_receive_throughput, network_transmit_throughput` (`dimension_DatabaseRole` ∈ {`READER`,`WRITER`}). All carry `dimension_Environment`.
- `AmazonMWAA` → prefix `aws_amazonmwaa_*`: Airflow StatsD operational metrics. `scheduler_heartbeat, scheduler_loop_duration, dag_bag_size, total_parse_time, dagfile_processing_last_{duration,num_of_db_queries,run_seconds_ago}, file_path_queue_{size,update_count}, import_errors, processes, queued_tasks, running_tasks, tasks_executable, tasks_starving, orphaned, orphaned_tasks_{adopted,cleared}, open_slots, job_end, triggers_running, triggerer_heartbeat, celery_worker_heartbeat, critical_section_{busy,duration,query_duration}, pool_{open,running,queued,scheduled,deferred}_slots`.

**Declared-pipeline outcomes (vendor-doc sourced, not live-captured).** Current
[AWS MWAA Airflow metrics](https://docs.aws.amazon.com/mwaa/latest/userguide/access-metrics-cw.html#available-metrics-cw)
retrieved through Firecrawl on 2026-10-23 confirm namespace `AmazonMWAA` and:

| Raw CloudWatch name | Canonical synthkit base | Unit | Dimensions (besides Environment) |
|---|---|---|---|
| `DAGDurationSuccess` | `aws_amazonmwaa_dagduration_success` | Milliseconds | `DAG=<pipeline-name>` |
| `DAGDurationFailed` | `aws_amazonmwaa_dagduration_failed` | Milliseconds | `DAG=<pipeline-name>` |
| `TaskInstanceFailures` | `aws_amazonmwaa_task_instance_failures` | Count | `DAG=All`, `Task=All` |

The bases follow the naming law above (`[slug: cw-naming]`):
consecutive capitals in `DAGDuration` stay joined (`dagduration`, **not** `dag_duration`).
This mapping is derived from the established naming law, not a new captured inventory.
The AWS timer rows name Airflow `dagrun.duration.success.{dag_id}` and
`dagrun.duration.failed.{dag_id}`; the counter row names `ti_failures` with literal `All`
dimensions, not task identity. No Function, State, Job, Operator or Task dimension is
added to the DAG timers. Environment comes from the documented environment grouping.

Only environments with declared pipelines emit these additive families. Actual shared
run-window terminal observations drive trigger-to-terminal timers (including waiting),
not nominal stage durations. Sum/min/max and mean are converted from seconds to ms;
sample count is the matching terminal count. Failed stage executions across the declared
pipelines in an environment supply Count=1 observations to `TaskInstanceFailures`:
sum and sample count equal failures; average/minimum/maximum equal 1. These are synthetic
observation mechanics, not a claim about a particular deployed StatsD aggregation cadence.
No completion/failure means no corresponding observation: all five suffixes are absent,
including after a prior nonempty window. No cumulative or stale values and no fabricated
zero-duration samples. Both remote-write and native OTLP preserve this contract. Legacy
families and environments without declared pipelines remain unchanged.

⚠ `_p90/_p95/_p99` suffixes seen on `aws_mwaa_*` series are OPERATOR-ADDED metric-stream statistics — NOT default (default is the five-stat set); do not bake percentiles into a synthkit MWAA family.

```yaml signals
family: aws_mwaa
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/MWAA
  job: cloud/aws/mwaa
  name: <resource-arn>|global
  dimension_Environment: <env-name>
  dimension_Cluster: AdditionalWorker|BaseWorker|Scheduler|WebServer
  dimension_DatabaseRole: READER|WRITER
  tag_*: <resource-tags>
metrics:
  - {root: active_connection_count, type: gauge, unit: count, v: ok}
  - {root: approximate_age_of_oldest_task, type: gauge, unit: seconds, v: ok}
  - {root: queued_tasks, type: gauge, unit: count, v: ok}
  - {root: running_tasks, type: gauge, unit: count, v: ok}
  - {root: cpuutilization, type: gauge, unit: percent, v: ok}
  - {root: memory_utilization, type: gauge, unit: percent, v: ok}
  - {root: database_connections, type: gauge, unit: count, v: ok}
  - {root: disk_queue_depth, type: gauge, unit: count, v: ok}
  - {root: freeable_memory, type: gauge, unit: bytes, v: ok}
  - {root: read_iops, type: gauge, unit: count_per_second, v: ok}
  - {root: write_iops, type: gauge, unit: count_per_second, v: ok}
  - {root: read_latency, type: gauge, unit: seconds, v: ok}
  - {root: write_latency, type: gauge, unit: seconds, v: ok}
  - {root: read_throughput, type: gauge, unit: bytes_per_second, v: ok}
  - {root: write_throughput, type: gauge, unit: bytes_per_second, v: ok}
  - {root: network_receive_throughput, type: gauge, unit: bytes_per_second, v: ok}
  - {root: network_transmit_throughput, type: gauge, unit: bytes_per_second, v: ok}
info_series: aws_mwaa_info
---
family: aws_amazonmwaa
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AmazonMWAA
  job: cloud/aws/amazonmwaa
  name: <resource-arn>|global
  dimension_Environment: <env-name>
  dimension_Function: Celery|DAG Processing|Dataset|Executor|Scheduler|Trigger|Triggerer
  dimension_Pool: <pool-name>
  dimension_DAG_Filename: <dag-filename>
  dimension_Job: <job-name>
  dimension_HostName: <hostname>
  dimension_DAG: <pipeline-name>|All
  dimension_Task: All
  tag_*: <resource-tags>
metrics:
  - {root: dagduration_success, type: gauge, unit: milliseconds, v: ok}
  - {root: dagduration_failed, type: gauge, unit: milliseconds, v: ok}
  - {root: task_instance_failures, type: gauge, unit: count, v: ok}
  - {root: scheduler_heartbeat, type: gauge, unit: count, v: ok}
  - {root: scheduler_loop_duration, type: gauge, unit: seconds, v: ok}
  - {root: dag_bag_size, type: gauge, unit: count, v: ok}
  - {root: total_parse_time, type: gauge, unit: seconds, v: ok}
  - {root: dagfile_processing_last_duration, type: gauge, unit: seconds, v: ok}
  - {root: dagfile_processing_last_num_of_db_queries, type: gauge, unit: count, v: ok}
  - {root: dagfile_processing_last_run_seconds_ago, type: gauge, unit: seconds, v: ok}
  - {root: file_path_queue_size, type: gauge, unit: count, v: ok}
  - {root: file_path_queue_update_count, type: gauge, unit: count, v: ok}
  - {root: import_errors, type: gauge, unit: count, v: ok}
  - {root: processes, type: gauge, unit: count, v: ok}
  - {root: queued_tasks, type: gauge, unit: count, v: ok}
  - {root: running_tasks, type: gauge, unit: count, v: ok}
  - {root: tasks_executable, type: gauge, unit: count, v: ok}
  - {root: tasks_starving, type: gauge, unit: count, v: ok}
  - {root: orphaned, type: gauge, unit: count, v: ok}
  - {root: orphaned_tasks_adopted, type: gauge, unit: count, v: ok}
  - {root: orphaned_tasks_cleared, type: gauge, unit: count, v: ok}
  - {root: open_slots, type: gauge, unit: count, v: ok}
  - {root: job_end, type: gauge, unit: count, v: ok}
  - {root: triggers_running, type: gauge, unit: count, v: ok}
  - {root: triggerer_heartbeat, type: gauge, unit: count, v: ok}
  - {root: celery_worker_heartbeat, type: gauge, unit: count, v: ok}
  - {root: critical_section_busy, type: gauge, unit: count, v: ok}
  - {root: critical_section_duration, type: gauge, unit: seconds, v: ok}
  - {root: critical_section_query_duration, type: gauge, unit: seconds, v: ok}
  - {root: pool_open_slots, type: gauge, unit: count, v: ok}
  - {root: pool_running_slots, type: gauge, unit: count, v: ok}
  - {root: pool_queued_slots, type: gauge, unit: count, v: ok}
  - {root: pool_scheduled_slots, type: gauge, unit: count, v: ok}
  - {root: pool_deferred_slots, type: gauge, unit: count, v: ok}
info_series: aws_amazonmwaa_info
```

---

## DocumentDB — `aws_docdb_*` ✅ [slug: cw-docdb]
*Provenance: predecessor SIGNALS.md §2.9 + `research/aws-infra-cloudwatch.md` §8 (DocDB). Brought in from predecessor research dossier; no synthkit live capture yet — see cantfind SK-31. All metrics marked `v: assumed`.*

Namespace `AWS/DocDB`. Dimensions: `dimension_DBClusterIdentifier`, `dimension_Role` ∈ {`WRITER`,`READER`}, `dimension_DBInstanceIdentifier`.

⚠ `ReadLatency`/`WriteLatency` units documented ambiguously; treat as seconds (same as RDS). `_sum` never `rate()`.

```yaml signals
family: aws_docdb
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/DocDB
  job: cloud/aws/docdb
  name: <resource-arn>
  dimension_DBClusterIdentifier: <cluster-id>
  dimension_Role: WRITER|READER
  dimension_DBInstanceIdentifier: <instance-id>
  tag_*: <resource-tags>
metrics:
  - {root: cpuutilization, type: gauge, unit: percent, v: assumed}
  - {root: database_connections, type: gauge, unit: count, v: assumed}
  - {root: freeable_memory, type: gauge, unit: bytes, v: assumed}
  - {root: read_latency, type: gauge, unit: seconds, v: assumed}
  - {root: write_latency, type: gauge, unit: seconds, v: assumed}
  - {root: read_iops, type: gauge, unit: count_per_second, v: assumed}
  - {root: write_iops, type: gauge, unit: count_per_second, v: assumed}
  - {root: buffer_cache_hit_ratio, type: gauge, unit: percent, v: assumed}
  - {root: opcounters_insert, type: gauge, unit: count, v: assumed}
  - {root: opcounters_query, type: gauge, unit: count, v: assumed}
  - {root: opcounters_update, type: gauge, unit: count, v: assumed}
  - {root: opcounters_delete, type: gauge, unit: count, v: assumed}
  - {root: opcounters_getmore, type: gauge, unit: count, v: assumed, note: "predecessor-§2.9 only, not in AWS docs dossier"}
  - {root: opcounters_command, type: gauge, unit: count, v: assumed, note: "predecessor-§2.9 only, not in AWS docs dossier"}
  - {root: documents_inserted, type: gauge, unit: count, v: assumed, note: "predecessor-§2.9 only, not in AWS docs dossier"}
  - {root: documents_returned, type: gauge, unit: count, v: assumed, note: "predecessor-§2.9 only, not in AWS docs dossier"}
  - {root: documents_updated, type: gauge, unit: count, v: assumed, note: "predecessor-§2.9 only, not in AWS docs dossier"}
  - {root: documents_deleted, type: gauge, unit: count, v: assumed, note: "predecessor-§2.9 only, not in AWS docs dossier"}
  - {root: swap_usage, type: gauge, unit: bytes, v: assumed}
info_series: aws_docdb_info
```

---

## Neptune — `aws_neptune_*` ✅ [slug: cw-neptune]
*Provenance: predecessor SIGNALS.md §2.9 + `research/aws-infra-cloudwatch.md` §12 (Neptune). Brought in from predecessor research dossier; no synthkit live capture yet — see cantfind SK-32. All metrics marked `v: assumed`.*

Namespace `AWS/Neptune`. Dimensions: `dimension_DBClusterIdentifier`, `dimension_Role` ∈ {`WRITER`,`READER`}.

⚠ Neptune metrics only emit when non-zero. `_sum` never `rate()`.

```yaml signals
family: aws_neptune
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/Neptune
  job: cloud/aws/neptune
  name: <resource-arn>
  dimension_DBClusterIdentifier: <cluster-id>
  dimension_Role: WRITER|READER
  tag_*: <resource-tags>
metrics:
  - {root: gremlin_requests_per_sec, type: gauge, unit: count_per_second, v: assumed}
  - {root: gremlin_client_errors_per_sec, type: gauge, unit: count_per_second, v: assumed}
  - {root: gremlin_server_errors_per_sec, type: gauge, unit: count_per_second, v: assumed}
  - {root: total_requests_per_sec, type: gauge, unit: count_per_second, v: assumed}
  - {root: total_client_errors_per_sec, type: gauge, unit: count_per_second, v: assumed}
  - {root: total_server_errors_per_sec, type: gauge, unit: count_per_second, v: assumed}
  - {root: main_request_queue_pending_requests, type: gauge, unit: count, v: assumed}
  - {root: cpuutilization, type: gauge, unit: percent, v: assumed}
  - {root: buffer_cache_hit_ratio, type: gauge, unit: percent, v: assumed}
  - {root: num_tx_committed, type: gauge, unit: count_per_second, v: assumed}
  - {root: num_tx_opened, type: gauge, unit: count, v: assumed, note: "predecessor-§2.9 only, not in AWS docs dossier"}
  - {root: num_tx_rolled_back, type: gauge, unit: count_per_second, v: assumed}
  - {root: cluster_replica_lag_maximum, type: gauge, unit: milliseconds, v: assumed}
info_series: aws_neptune_info
```

---

## OpenSearch Serverless — `aws_aoss_*` ✅ [slug: cw-aoss]
*Provenance: predecessor SIGNALS.md §2.9 + `research/aws-infra-cloudwatch.md` §9 (AOSS). Brought in from predecessor research dossier; no synthkit live capture yet — see cantfind SK-33. All metrics marked `v: assumed`. OCU scope deviation: see cantfind SK-33.*

Namespace `AWS/AOSS`. ⚠ Serverless ONLY — provisioned OpenSearch uses `AWS/ES`.

Collection-scoped metrics: dims `dimension_ClientId`, `dimension_CollectionId`, `dimension_CollectionName`.
OCU metrics (`search_ocu`, `indexing_ocu`): dim `dimension_ClientId` ALONE (account-level) OR `dimension_ClientId` + `dimension_CollectionGroupId`/`dimension_CollectionGroupName` — NEVER `dimension_CollectionId` (different granularity — see cantfind SK-33).

```yaml signals
family: aws_aoss
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/AOSS
  job: cloud/aws/aoss
  name: <resource-arn>|global
  # collection-scoped metrics:
  dimension_ClientId: <aws-account-id>
  dimension_CollectionId: <collection-id>
  dimension_CollectionName: <collection-name>
  # OCU metrics only: dimension_ClientId alone, or + dimension_CollectionGroupId/dimension_CollectionGroupName
  tag_*: <resource-tags>
metrics:
  - {root: search_request_rate, type: gauge, unit: count_per_minute, v: assumed}
  - {root: search_request_latency, type: gauge, unit: milliseconds, v: assumed}
  - {root: search_request_errors, type: gauge, unit: count, v: assumed}
  - {root: ingestion_request_rate, type: gauge, unit: count, v: assumed}
  - {root: ingestion_request_success, type: gauge, unit: count, v: assumed}
  - {root: ingestion_request_errors, type: gauge, unit: count, v: assumed}
  - {root: ingestion_request_latency, type: gauge, unit: seconds, v: assumed, note: "unit SECONDS not ms"}
  - {root: searchable_documents, type: gauge, unit: count, v: assumed}
  - {root: deleted_documents, type: gauge, unit: count, v: assumed}
  - {root: storage_used_in_s3, type: gauge, unit: bytes, v: assumed}
  - {root: search_ocu, type: gauge, unit: count, v: assumed, note: "account-level OR CollectionGroup scope — NOT per-collection"}
  - {root: indexing_ocu, type: gauge, unit: count, v: assumed, note: "account-level OR CollectionGroup scope — NOT per-collection"}
  - {root: active_collection, type: gauge, unit: bool, v: assumed, note: "1=ACTIVE"}
  - {root: "2xx", type: gauge, unit: count, v: assumed}
  - {root: "4xx", type: gauge, unit: count, v: assumed}
  - {root: "5xx", type: gauge, unit: count, v: assumed}
info_series: aws_aoss_info
```

---

## Glue — `aws_glue_*` ✅ [slug: cw-glue]
*Provenance: predecessor SIGNALS.md §2.9 + `research/aws-infra-cloudwatch.md` §11 (Glue), supplemented by retained positive series-key observations on 2026-10-23 (`[slug: cw-glue-captured]`). All 20 emitted legacy bases below have observed five-stat names and exact JobName/JobRunId/Type keys. Their existing `v: assumed` annotations do not certify numeric behavior or live parity of the construct. SK-34 (Glue source-name and shape verification) is partially resolved, not closed. ⚠ Namespace is literally `"Glue"` (no `AWS/` prefix); Prometheus prefix is still `aws_glue_`.*

The legacy model below emits no terminal-outcome family. The broader claim that Glue has no
CloudWatch outcome metric is false: current observability docs and the retained positive
`aws_glue_succeed_all` family establish `glue.succeed.ALL`. Failure/error-family absence is not
established by the truncated catalogue. `aggregate.*` profiler metrics are delta values;
JVM heap/CPU metrics are absolute, while S3 filesystem read/write metrics are deltas despite
`Type=gauge`, per current AWS docs. This is distinct from the shared per-period Prometheus
GAUGE instrument rule; no renderer behavior is changed here.

```yaml signals
family: aws_glue
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: "Glue"                          # ⚠ no AWS/ prefix — diverges from metric prefix
  job: cloud/aws/glue
  name: <resource-arn>|global
  dimension_JobName: <job-name>
  dimension_JobRunId: <run-id>|ALL
  dimension_Type: count|gauge
  tag_*: <resource-tags>
metrics:
  # delta aggregates (Type=count)
  - {root: driver_aggregate_bytes_read, type: gauge, unit: bytes, v: assumed}
  - {root: driver_aggregate_records_read, type: gauge, unit: count, v: assumed}
  - {root: driver_aggregate_num_completed_tasks, type: gauge, unit: count, v: assumed}
  - {root: driver_aggregate_num_failed_tasks, type: gauge, unit: count, v: assumed}
  - {root: driver_aggregate_num_killed_tasks, type: gauge, unit: count, v: assumed}
  - {root: driver_aggregate_num_completed_stages, type: gauge, unit: count, v: assumed}
  - {root: driver_aggregate_elapsed_time, type: gauge, unit: milliseconds, v: assumed}
  - {root: driver_aggregate_shuffle_bytes_written, type: gauge, unit: bytes, v: assumed}
  - {root: driver_aggregate_shuffle_local_bytes_read, type: gauge, unit: bytes, v: assumed}
  # absolute gauges (Type=gauge)
  - {root: driver_block_manager_disk_disk_space_used_mb, type: gauge, unit: megabytes, v: assumed}
  - {root: driver_jvm_heap_usage, type: gauge, unit: fraction, v: assumed}
  - {root: all_jvm_heap_usage, type: gauge, unit: fraction, v: assumed}
  - {root: driver_jvm_heap_used, type: gauge, unit: bytes, v: assumed}
  - {root: all_jvm_heap_used, type: gauge, unit: bytes, v: assumed}
  - {root: driver_s3_filesystem_read_bytes, type: gauge, unit: bytes, v: assumed}
  - {root: all_s3_filesystem_read_bytes, type: gauge, unit: bytes, v: assumed}
  - {root: driver_s3_filesystem_write_bytes, type: gauge, unit: bytes, v: assumed}
  - {root: all_s3_filesystem_write_bytes, type: gauge, unit: bytes, v: assumed}
  - {root: driver_system_cpu_system_load, type: gauge, unit: fraction, v: assumed}
  - {root: all_system_cpu_system_load, type: gauge, unit: fraction, v: assumed}
info_series: aws_glue_info
```

---

## PrivateLink — `aws_privatelinkendpoints_*` + `aws_privatelinkservices_*` ✅ [slug: cw-privatelink]
*Provenance: synthkit `[slug: cw-naming]` SK-3 (live-confirmed 2026-06-14) + predecessor SIGNALS.md §2.9. `v: ok` (live-captured dimension form via SK-3). ⚠ Space→underscore in dimension names, CW casing preserved.*

```yaml signals
family: aws_privatelinkendpoints
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/PrivateLinkEndpoints
  job: cloud/aws/privatelinkendpoints
  name: <resource-arn>|global
  dimension_Endpoint_Type: <type>
  dimension_Service_Name: <service>
  dimension_Subnet_Id: <subnet>
  dimension_VPC_Endpoint_Id: <endpoint-id>
  dimension_VPC_Id: <vpc-id>
  tag_*: <resource-tags>
metrics:
  - {root: active_connections, type: gauge, unit: count, v: ok}
  - {root: bytes_processed, type: gauge, unit: bytes, v: ok}
  - {root: new_connections, type: gauge, unit: count, v: ok}
  - {root: packets_dropped, type: gauge, unit: count, v: ok}
  - {root: rst_packets_received, type: gauge, unit: count, v: ok}
info_series: aws_privatelinkendpoints_info
---
family: aws_privatelinkservices
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/PrivateLinkServices
  job: cloud/aws/privatelinkservices
  name: <resource-arn>|global
  dimension_Az: <az>
  dimension_Load_Balancer_Arn: <lb-arn>
  dimension_Service_Id: <service-id>
  dimension_VPC_Endpoint_Id: <endpoint-id>
  tag_*: <resource-tags>
metrics:
  - {root: active_connections, type: gauge, unit: count, v: ok}
  - {root: bytes_processed, type: gauge, unit: bytes, v: ok}
  - {root: endpoints_count, type: gauge, unit: count, v: ok}
  - {root: new_connections, type: gauge, unit: count, v: ok}
  - {root: rst_packets_sent, type: gauge, unit: count, v: ok}
info_series: aws_privatelinkservices_info
```


---

## SKT-0021.02 CloudWatch coverage reconciliation (2026-09-01)

This is the durable source-family record for the measured **375** CloudWatch
`extra_metric` findings. It was derived by comparing the 618 CloudWatch series in
`reality-corpus/cw/eks-live-readback.json` with an inventory generated from only
`aws-cloudwatch-infra,aws-cloud-services` (`SELFOBS_ENABLED=false DRY_RUN=true`).
The fold is exactly the naming law's five statistics:
`_sum/_average/_maximum/_minimum/_sample_count`. Thus the finding denominator is
**75 source families × 5 = 375 findings**, not 375 independent source families.

The existing coverage record supplies 70 of the 75 source-family decisions. They
reconcile to SKT-0010.06's CloudWatch scope: its description enumerates 56
CloudWatch `should_emit` roots (the headline's 57th family is the separate
kubelet-storage item). This reconciliation emits no code: an `emit` verdict is
an input to SKT-0010.06, not an assertion that the root now exists in synth output.

| Source family | Verdict | findings | Evidence / durable reason |
| --- | --- | ---: | --- |
| `aws_applicationelb_anomalous_host_count` | PENDING SK-92 | 5 | optional ALB target-health/zonal publication condition remains unconfirmed |
| `aws_applicationelb_capacity_utilization` | PENDING SK-93 | 5 | unit and capacity-reservation publication condition remain unconfirmed |
| `aws_applicationelb_client_tlsnegotiation_error_count` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_applicationelb_consumed_lcus` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_applicationelb_desync_mitigation_mode_non_compliant_request_count` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_applicationelb_healthy_state_dns` | PENDING SK-92 | 5 | optional ALB target-health/zonal publication condition remains unconfirmed |
| `aws_applicationelb_healthy_state_routing` | PENDING SK-92 | 5 | optional ALB target-health/zonal publication condition remains unconfirmed |
| `aws_applicationelb_http_fixed_response_count` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_applicationelb_http_redirect_count` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_applicationelb_httpcode_elb_3_xx_count` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_applicationelb_httpcode_elb_4_xx_count` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_applicationelb_httpcode_elb_503_count` | PENDING (new) | 5 | captured root; neither `signals/cw.md` nor the existing verdict record establishes its independent publication/dimension contract |
| `aws_applicationelb_mitigated_host_count` | PENDING SK-92 | 5 | optional ALB target-health/zonal publication condition remains unconfirmed |
| `aws_applicationelb_peak_lcus` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_applicationelb_request_count_per_target` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_applicationelb_rule_evaluations` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_applicationelb_unhealthy_state_dns` | PENDING SK-92 | 5 | optional ALB target-health/zonal publication condition remains unconfirmed |
| `aws_applicationelb_unhealthy_state_routing` | PENDING SK-92 | 5 | optional ALB target-health/zonal publication condition remains unconfirmed |
| `aws_ebs_volume_avg_iops` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_ebs_volume_avg_throughput` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_ebs_volume_idle_time` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_ebs_volume_iopsexceeded_check` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_ebs_volume_stalled_iocheck` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_ebs_volume_throughput_exceeded_check` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_ec2_cpucredit_usage` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_ec2_cpusurplus_credit_balance` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_ec2_cpusurplus_credits_charged` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_ec2_disk_read_bytes` | out of scope | 5 | EC2 instance-store root; modelled node fixtures cover EBS-backed nodes only |
| `aws_ec2_disk_read_ops` | out of scope | 5 | EC2 instance-store root; modelled node fixtures cover EBS-backed nodes only |
| `aws_ec2_disk_write_bytes` | out of scope | 5 | EC2 instance-store root; modelled node fixtures cover EBS-backed nodes only |
| `aws_ec2_disk_write_ops` | out of scope | 5 | EC2 instance-store root; modelled node fixtures cover EBS-backed nodes only |
| `aws_ec2_ebsbyte_balance_percent` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_ec2_ebsiobalance_percent` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_ec2_instance_ebsiopsexceeded_check` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_ec2_instance_ebsthroughput_exceeded_check` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_ec2_metadata_no_token_rejected` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_ec2_metadata_no_token` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_ec2_network_packets_in` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_ec2_network_packets_out` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_bytes_per_second_limit` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_delivery_to_http_endpoint_bytes` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_delivery_to_http_endpoint_processed_bytes` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_delivery_to_http_endpoint_processed_records` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_delivery_to_http_endpoint_records` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_describe_delivery_stream_latency` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_describe_delivery_stream_requests` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_failed_validation_bytes` | PENDING SK-95 | 5 | requires `dimension_SourcePartitionId`, a stream capability not modelled |
| `aws_firehose_failed_validation_records` | PENDING SK-95 | 5 | requires `dimension_SourcePartitionId`, a stream capability not modelled |
| `aws_firehose_incoming_bytes` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_incoming_put_requests` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_incoming_records` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_kmskey_access_denied` | PENDING SK-94 | 5 | error-only versus continuous-zero publication remains unconfirmed |
| `aws_firehose_kmskey_disabled` | PENDING SK-94 | 5 | error-only versus continuous-zero publication remains unconfirmed |
| `aws_firehose_kmskey_invalid_state` | PENDING SK-94 | 5 | error-only versus continuous-zero publication remains unconfirmed |
| `aws_firehose_kmskey_not_found` | PENDING SK-94 | 5 | error-only versus continuous-zero publication remains unconfirmed |
| `aws_firehose_put_record_batch_bytes` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_put_record_batch_latency` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_put_record_batch_records` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_put_record_batch_requests` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_put_record_bytes` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_put_record_latency` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_put_record_requests` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_put_requests_per_second_limit` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_records_per_second_limit` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_firehose_secrets_manager_access_denied_exception` | PENDING SK-94 | 5 | error-only versus continuous-zero publication remains unconfirmed |
| `aws_firehose_throttled_records` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_natgateway_bytes_in_from_destination` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_natgateway_bytes_in_from_source` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_natgateway_bytes_out_to_source` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_natgateway_packets_in_from_destination` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_natgateway_packets_in_from_source` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_natgateway_packets_out_to_destination` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_natgateway_packets_out_to_source` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_natgateway_peak_bytes_per_second` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |
| `aws_natgateway_peak_packets_per_second` | emit | 5 | Existing `should_emit`: `reality-corpus/verdicts/coverage-verdicts.json`; SKT-0010.06 |

**Counts:** 75 source families verdicted; 56 `emit` verdicts; 4 deliberately
out of scope; 15 PENDING (including one new row awaiting root-assigned `SK-N`);
0 source families emitted by this reconciliation; 375 expanded findings. The four
out-of-scope EC2 roots are `Disk{Read,Write}{Bytes,Ops}`: the EC2 contract models
EBS-backed worker nodes and already emits the EBS-specific roots. The current Amazon
CloudWatch documentation retrieved through Context7 on 2026-09-01 independently
describes NVMe metrics for **instance-store** volumes; that is a different fixture
capability, not a missing EBS sibling.

Every source family in the table expands only through `cw.EmitStats`; its
`_sum` is a per-period gauge (`state.Set`), never a counter or rate. The new
`aws_applicationelb_httpcode_elb_503_count` row is intentionally PENDING rather
than inferred from the existing aggregate `httpcode_elb_5_xx_count`: the captured
name alone does not establish the independent root's publication condition or exact
dimension form.


## CloudWatch Metric Streams OTLP — observed 2026-09-05 [slug: cw-metric-stream-otlp]

The root sent one OTLP/HTTP request through an existing gateway receiver, then queried the metrics backend after more than 90 seconds. Receipt was HTTP 200 with `{"partialSuccess":{}}`. Read-back returned 13 probe series: 12 Summary components plus one `target_info`. This proves queryability for the tested EC2 and RDS forms, not every AWS namespace.

Wire names retain `amazonaws.com/{Namespace}/{MetricName}`. The datapoint is Summary: count=SampleCount, sum=Sum, quantiles 0 and 1 carry minimum and maximum. Average is computed as sum/count; the five-stat remote-write expansion is not the OTLP representation. Units come from AWS's UCUM table: Percent `%`, Count `{Count}`. Resource attributes are `cloud.provider`, `cloud.account.id`, `cloud.region`; the scratch probe additionally used `service.name` to isolate read-back. An empty Dimensions map is omitted.

Observed queryable names:

```text
amazonaws_com_AWS_EC2_CPUUtilization_percent
amazonaws_com_AWS_EC2_CPUUtilization_percent_count
amazonaws_com_AWS_EC2_CPUUtilization_percent_sum
amazonaws_com_AWS_RDS_DatabaseConnections
amazonaws_com_AWS_RDS_DatabaseConnections_count
amazonaws_com_AWS_RDS_DatabaseConnections_sum
target_info
```

Dots and slashes normalized to underscores; case survived. Percent gained `_percent`; `{Count}` added no unit suffix. Each Summary produced `_count`, `_sum`, and two quantile series (labels `quantile="0"` and `quantile="1"`). No `_average`, `_maximum`, `_minimum`, `_sample_count`, or `_ratio` series appeared.

`Namespace` and `MetricName` became labels with case preserved. Nested `Dimensions` was serialized into ONE JSON-valued `Dimensions` label; it was not flattened into `Dimensions.*` labels. The account aggregate had no `Dimensions` label. `cloud.region` was promoted as `cloud_region`; `service.name` as `service_name` and `job`. `cloud.account.id` and `cloud.provider` appeared only on `target_info` in this observation. No scope labels appeared.

Provenance: AWS CloudWatch OpenTelemetry 1.0.0 format and translation documentation, retrieved through Context7 with the exact translation table checked at its official source; collector-contrib `awscloudwatchreceiver` README. Capture: [`cloudwatch-metric-stream-otlp-0462d7cda68525ea.md](../e2e/lab/captures/cloudwatch-metric-stream-otlp-0462d7cda68525ea.md). The wire spelling must never be replaced with the post-gateway spelling in emitters.

### Verified mappings and current skipped inventory

The lookup now contains **287** exact AWS Metric Streams pairs. The original gateway
read-back verified `aws_ec2_cpuutilization` and `aws_rds_database_connections`; the
remaining pairs below are verified from the current AWS metric reference and the AWS
Metric Streams OpenTelemetry unit-translation table. Their exact namespace, metric-name,
UCUM unit, and construct-specific dimensions are recorded in the owning
`internal/cw/streamtable_*.go` table, with the AWS source URLs beside each table.

| namespace group | verified pairs | skipped bases | reason for remaining skips |
| --- | ---: | ---: | --- |
| `cwinfra` | 103 | 1 | Firehose `DeliveryToHttpEndpoint.Success` has incompatible construct semantics |
| `ec2` | 19 | 4 | CPU-credit unit is not in the permitted OTLP translation table |
| `rds` | 19 | 0 | — |
| `docdb` | 17 | 2 | the current page names `ReadLatency` and `SwapUsage` without units |
| `neptune` | 13 | 0 | — |
| `elasticache` | 22 | 2 | `Number` and `Boolean` are not in the permitted OTLP translation table |
| `aoss` | 0 | 16 | the current page supplies names/dimensions but no units |
| `mwaa` | 42 | 9 | six AWS/MWAA and three AmazonMWAA rows remain ambiguous or undocumented |
| `glue` | 20 | 0 | — |
| `bedrock` | 32 | 6 | model-invocation logging rows have no unit/dimension contract |
| `agentcore` | 0 | 12 | current runtime pages do not establish the required unit/dimension pairs |
| **total** | **287** | **52** | **339** five-stat bases in the catalogue |

Every base below is emitted by its construct on the remote-write path but is withheld from
the Metric Streams path because `Lookup` has no verified AWS metric-name and UCUM-unit pair.
These counts exclude `_info` families: an info gauge has no five-stat source form and is not
a Metric Streams datapoint. `StreamReport.SkippedBases` exposes these distinct bases at runtime.

#### `cwinfra` — 1 skipped base

```text
aws_firehose_delivery_to_http_endpoint_success
```

#### `ec2` — 4 skipped bases

```text
aws_ec2_cpucredit_balance
aws_ec2_cpucredit_usage
aws_ec2_cpusurplus_credit_balance
aws_ec2_cpusurplus_credits_charged
```

#### `rds` — 0 skipped bases

```text
none
```

#### `docdb` — 2 skipped bases

```text
aws_docdb_read_latency
aws_docdb_swap_usage
```

#### `neptune` — 0 skipped bases

```text
none
```

#### `elasticache` — 2 skipped bases

```text
aws_elasticache_memory_fragmentation_ratio
aws_elasticache_save_in_progress
```

#### `aoss` — 16 skipped bases

```text
aws_aoss_2xx
aws_aoss_4xx
aws_aoss_5xx
aws_aoss_active_collection
aws_aoss_deleted_documents
aws_aoss_indexing_ocu
aws_aoss_ingestion_request_errors
aws_aoss_ingestion_request_latency
aws_aoss_ingestion_request_rate
aws_aoss_ingestion_request_success
aws_aoss_search_ocu
aws_aoss_search_request_errors
aws_aoss_search_request_latency
aws_aoss_search_request_rate
aws_aoss_searchable_documents
aws_aoss_storage_used_in_s3
```

#### `mwaa` — 9 skipped bases

```text
aws_amazonmwaa_celery_worker_heartbeat
aws_amazonmwaa_file_path_queue_size
aws_amazonmwaa_triggerer_heartbeat
aws_mwaa_active_connection_count
aws_mwaa_network_receive_throughput
aws_mwaa_network_transmit_throughput
aws_mwaa_read_iops
aws_mwaa_read_latency
aws_mwaa_read_throughput
```

#### `glue` — 0 skipped bases

```text
none
```

#### `bedrock` — 6 skipped bases

```text
aws_bedrock_model_invocation_large_data_s3_delivery_failure
aws_bedrock_model_invocation_large_data_s3_delivery_success
aws_bedrock_model_invocation_logs_cloud_watch_delivery_failure
aws_bedrock_model_invocation_logs_cloud_watch_delivery_success
aws_bedrock_model_invocation_logs_s3_delivery_failure
aws_bedrock_model_invocation_logs_s3_delivery_success
```

#### `agentcore` — 12 skipped bases

```text
aws_bedrock_agentcore_active_streaming_connections
aws_bedrock_agentcore_cpu_used_v_cpu_hours
aws_bedrock_agentcore_inbound_streaming_bytes_processed
aws_bedrock_agentcore_invocations
aws_bedrock_agentcore_latency
aws_bedrock_agentcore_memory_used_gb_hours
aws_bedrock_agentcore_outbound_streaming_bytes_processed
aws_bedrock_agentcore_session_count
aws_bedrock_agentcore_system_errors
aws_bedrock_agentcore_throttles
aws_bedrock_agentcore_total_errors
aws_bedrock_agentcore_user_errors
```

The `dimension_` label suffix is **not** a generic CloudWatch Dimension converter. For each
of the 287 emitted entries, the table carries the exact AWS spellings for the construct's
documented form: for example, EC2 uses `AutoScalingGroupName` and `InstanceId`, RDS uses
`DBInstanceIdentifier`, ElastiCache uses `CacheClusterId` and `CacheNodeId`, and the
service-specific tables carry their documented dimension pairs. The existing Prometheus
batches also contain deliberately mangled names, including PrivateLink
`dimension_Endpoint_Type`, `dimension_Service_Name`, and `dimension_VPC_Endpoint_Id` for
AWS dimensions containing spaces. Reconstructing those to `Endpoint Type`, `Service Name`,
or `VPC Endpoint Id` would invent a wire value, so `MetricStreams` emits dimensions only
from the pair-specific exact table and omits every other suffix.
