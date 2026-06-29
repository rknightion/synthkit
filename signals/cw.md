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

**Universal labels on every CW series:** `account_id`, `region`, `namespace` (original CW string,
verbatim), `job`=`cloud/aws/<lowercased service>`, `name` (resource ARN or `"global"`),
`dimension_<DimName>` (one per CW dimension, CW casing preserved), `tag_*` (resource tags joined by
the metadata scraper). The scraper also emits a per-namespace `aws_<ns>_info` series (gauge=1, no
stat suffix, carries `tag_*`).

> ⚠ **Per-period GAUGE invariant (I5).** `_sum`-suffixed CW series are per-period GAUGES — the
> sum/count within the 1-minute reporting window, NOT a monotonic counter. **Never `rate()`/`increase()`
> any `_sum` series** — use the raw value or `delta()`. Applies to ALL CW families without exception.

> ⚠ **No histogram buckets on any CW family** — CW metric-stream series have only the 5 stat
> suffixes; there are NO `_bucket` series. (Lab-confirmed across 21,958 series: zero `_bucket` on any
> CW family.) Metrics are absent during idle periods (no traffic = no CW data point).

**Namespace → prefix quick reference:** `AWS/ApplicationELB`→`aws_applicationelb_` ·
`AWS/NetworkELB`→`aws_networkelb_` · `AWS/EC2`→`aws_ec2_` · `AWS/EBS`→`aws_ebs_` ·
`AWS/NATGateway`→`aws_natgateway_` · `AWS/S3`→`aws_s3_` · `AWS/EKS`→`aws_eks_` ·
`AWS/Firehose`→`aws_firehose_` · `AWS/RDS`→`aws_rds_` · `AWS/ElastiCache`→`aws_elasticache_`.

## ALB — `aws_applicationelb_*` ✅ [slug: cw-alb]
*Provenance: predecessor SIGNALS §2.9 + `research/aws-metric-streams.md` ALB (25 base names, lab-verified) + `emit/aws.go`.*

Series roots (all 5 stat suffixes apply per `[slug: cw-naming]`): `request_count`, `target_response_time` (seconds),
`httpcode_target_2_xx_count`, `httpcode_target_4_xx_count`, `httpcode_target_5_xx_count`,
`httpcode_elb_5_xx_count`, `healthy_host_count`, `un_healthy_host_count`, `active_connection_count`,
`new_connection_count`, `processed_bytes`, `target_connection_error_count`. Plus `aws_applicationelb_info`.

Dimensions: `dimension_LoadBalancer` (`app/<name>/<hex-id>`), `dimension_TargetGroup`
(`targetgroup/<name>/<hex-id>`), `dimension_AvailabilityZone`.

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
  dimension_TargetGroup: targetgroup/<name>/<hex-id>
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
info_series: aws_applicationelb_info   # gauge=1, no stat suffix, carries tag_*
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
*Provenance: predecessor SIGNALS §2.9 + `research/aws-metric-streams.md` EC2 (23 base names) + `emit/aws.go`.*

Roots: `cpuutilization` (percent), `network_in`, `network_out`, `status_check_failed`,
`status_check_failed_instance`, `status_check_failed_system`, `status_check_failed_attached_ebs`,
`ebsread_bytes`, `ebswrite_bytes`, `ebsread_ops`, `ebswrite_ops`, `cpucredit_balance`. Plus `aws_ec2_info`.

Dimensions: `dimension_AutoScalingGroupName` (ASG/node-group level) or `dimension_InstanceId`
(per-instance) — both coexist; `name`=`"global"` for ASG-level, node ARN for instance-level.

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
info_series: aws_ec2_info
```

## EBS — `aws_ebs_*` ✅ [slug: cw-ebs]
*Provenance: predecessor SIGNALS §2.9 + `research/aws-metric-streams.md` EBS (18 base names) + `emit/aws.go`.*

Roots (dim `dimension_VolumeId`): `volume_read_bytes`, `volume_write_bytes`, `volume_read_ops`,
`volume_write_ops`, `volume_queue_length`, `burst_balance` (percent; gp2/st1/sc1 only),
`volume_avg_read_latency` (ms; Nitro only), `volume_avg_write_latency` (ms; Nitro only),
`volume_total_read_time` (s), `volume_total_write_time` (s). Plus `aws_ebs_info`.
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
info_series: aws_ebs_info
```

## NAT Gateway — `aws_natgateway_*` ✅ [slug: cw-natgw]
*Provenance: predecessor SIGNALS §2.9 + `research/aws-metric-streams.md` NATGateway (15 base names) + `emit/aws.go`.*

Roots (dim `dimension_NatGatewayId`): `bytes_out_to_destination`, `error_port_allocation`,
`packets_drop_count`, `active_connection_count`, `connection_attempt_count` (SYN),
`connection_established_count`. Plus `aws_natgateway_info`.

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
*Provenance: predecessor SIGNALS §2.9/§2.10 + `research/aws-metric-streams.md` Firehose + `emit/aws.go`.*

Roots (dim `dimension_DeliveryStreamName`): `delivery_to_http_endpoint_success` (fraction 0–1; ≈1.0
steady), `delivery_to_http_endpoint_data_freshness` (seconds lag; <60s steady). Plus `aws_firehose_info`.
Only these two are emitted (the namespace has more). ⚠ `_sum` never `rate()`.

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
info_series: aws_firehose_info
# ⚠ namespace has more base names; only these two are emitted.
```

## RDS — `aws_rds_*` (✅ standalone Postgres/MySQL modelled; Aurora families 📋 documented, not yet modelled) [slug: cw-rds]
*Provenance: live capture 2026-06-14 (a live reference EKS cluster, eu-west-1) (133 base metrics, 665 series across postgres / mysql / aurora-postgresql / aurora-mysql, each writer/reader/serverless-v2). SK-4/SK-5/SK-6 resolved.*

**Names are live-verbatim.** The `aws_rds_*` names below are the EXACT post-mangling series as ingested (AWS metric stream → Firehose → Mimir). CW→Prometheus mangling splits at lower→UPPER transitions but collapses consecutive-caps runs, so real names are a MIX — `cpuutilization`, `acuutilization`, `ddllatency`, `dmlthroughput` (caps-run collapse) vs `database_connections`, `read_iops`, `ebsbyte_balance_percent`, `free_local_storage` (split). The listed names ARE the contract; do not re-derive. Each base emits the five-stat family `_average/_maximum/_minimum/_sum/_sample_count` (per-period GAUGES; `_sum` never `rate()`).

**Label universe (every series):** `account_id`, `region`, `namespace="AWS/RDS"` (Aurora too — there is NO separate `AWS/Aurora` namespace), `job`, `name`(ARN), `asserts_env` (Grafana-injected), `dimension_DBInstanceIdentifier`. Aurora additionally carries `dimension_DBClusterIdentifier` (⚠ also observed as `dimension_DbClusterIdentifier` — BOTH casings seen in live capture), `dimension_Role` ∈ {`WRITER`,`READER`}, `dimension_EngineName`, `dimension_DatabaseClass` ∈ {`db.t3.micro`,`db.t3.medium`,`db.serverless`}.

**Universal core — present on ALL engines** (postgres/mysql/aurora-postgresql/aurora-mysql); ✅ synthkit emits this on the standalone instance:
`cpuutilization, database_connections, freeable_memory, read_iops, write_iops, read_latency, write_latency, read_throughput, write_throughput, network_receive_throughput, network_transmit_throughput, disk_queue_depth, swap_usage`. ⚠ `read_latency`/`write_latency` are in **seconds** (SK-6 live-verified). All engines also emit the EBS-budget pair `ebsbyte_balance_percent, ebsiobalance_percent` (📋 not yet emitted; literal CW `%`→`_percent`). t-class burstable instances additionally emit the CPU-credit family `cpucredit_balance, cpucredit_usage, cpusurplus_credit_balance, cpusurplus_credits_charged` (📋).

**Standalone PostgreSQL (`engine=postgres`):** ✅ `burst_balance, free_storage_space`; 📋 `transaction_logs_disk_usage, transaction_logs_generation, maximum_used_transaction_ids, replication_slot_disk_usage, oldest_replication_slot_lag, oldest_logical_replication_slot_lag, checkpoint_lag`. ⚠ `replica_lag` appears ONLY on the read-replica instance — a lone standalone primary does NOT emit it (synthkit corrected 2026-06-14 to drop the always-emitted `aws_rds_replica_lag`).

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
documented_not_emitted: [ebsbyte_balance_percent, ebsiobalance_percent, cpucredit_balance, cpucredit_usage, cpusurplus_credit_balance, cpusurplus_credits_charged, "postgres: transaction_logs_disk_usage/transaction_logs_generation/maximum_used_transaction_ids/replication_slot_disk_usage/oldest_replication_slot_lag/oldest_logical_replication_slot_lag/checkpoint_lag", "postgres replica: replica_lag (read-replica ONLY)", "mysql: bin_log_disk_usage/lvmread_iops/lvmwrite_iops", "Aurora PG + Aurora MySQL families (see prose)"]
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
  tag_*: <resource-tags>
metrics:
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
*Provenance: predecessor SIGNALS.md §2.9 + `research/aws-infra-cloudwatch.md` §11 (Glue). Brought in from predecessor research dossier; no synthkit live capture yet — see cantfind SK-34. All metrics marked `v: assumed`. ⚠ Namespace is literally `"Glue"` (no `AWS/` prefix); Prometheus prefix is still `aws_glue_`.*

⚠ No CloudWatch metric for job SUCCEEDED/FAILED (outcome is EventBridge/API only). `aggregate.*` metrics are delta values; `jvm.*`/`system.*`/executor counts are absolute.

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
