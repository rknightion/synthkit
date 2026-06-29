# k8s add-ons (→ Mimir) — ScopeSubstrate

AWS and Kubernetes add-on constructs. All substrate-scoped: carry `cluster` + `k8s_cluster_name`,
never a `blueprint` label. Feature-gated (default OFF).
Global rules: see [`00-canon.md`](00-canon.md) — `[slug: scoping]`, `[slug: cardinality]`.

*Provenance: predecessor SIGNALS §6.3 Group A + `emit/{lbc,externaldns,coredns,vpccni,certmanager,clusterautoscaler,ebscsi,ksm_ingress}.go`.*

> ✅ **Job-label reality check (live staff stack capture, k8s-monitoring 4.1.5, 2026-06-13 — SK-10).** The
> `integrations/<addon>` convention below is **systematically wrong** vs how real k8s-monitoring scrapes
> add-ons. Real patterns: **annotation autodiscovery → `job=<bare service name>`**; **built-in
> integration → `job=integrations/kubernetes/<name>`**. Verified corrections: cert-manager →
> **`cert-manager`**, ExternalDNS → **`external-dns`**, CoreDNS → **`integrations/kubernetes/kube-dns`**
> (NOT `coredns`). cluster-autoscaler is absent on EKS-Karpenter stacks — the real autoscaler is
> **Karpenter** (`job=karpenter`, ns `kube-system`, `karpenter_build_info` version `1.13.0`; metrics
> `karpenter_{nodes_created_total,nodeclaims_created_total,cluster_state_node_count,pods_scheduling_decision_duration_seconds,cloudprovider_duration_seconds,build_info}`).
> LBC job label is **`aws-load-balancer-controller`** (bare app name — SK-10 live-confirmed 2026-06-14
> via k8s-monitoring annotation autodiscovery on a real LBC install; matches the cert-manager/external-dns
> bare-name pattern, NOT `integrations/aws-load-balancer-controller`). VPC-CNI / EBS-CSI job labels
> remain Ⓐ (absent on the capture stack — not falsifiable here).
> The per-construct headers below are annotated inline; the constructs themselves remain valid (synthkit
> models clusters that DO run these add-ons) — only the hardcoded `job` constant needs to follow reality.

---

## AWS Load Balancer Controller (`job="aws-load-balancer-controller"`, ns `kube-system` — SK-10 live-confirmed 2026-06-14) [slug: k8s-lbc]

`awslbc_readiness_gate_ready_seconds` (H), `awslbc_controller_reconcile_errors_total` (C;
`error_category` ∈ {transient,terminal}), `awslbc_controller_reconcile_stage_duration` (H;
`reconcile_stage` ∈ {sync,finalize}), `awslbc_webhook_validation_failure_total` (C),
`awslbc_webhook_mutation_failure_total` (C), `awslbc_controller_cache_object_total` (G; `resource` ∈
{ingress,service,targetgroupbinding}), `awslbc_controller_top_talkers` (G),
`awslbc_quic_target_missing_server_id` (C). AWS-SDK: `aws_api_calls_total` (C; `service,operation,
status_code` + `error_code` only on errored calls — OMITTED on success per I13, never `error_code=""`),
`aws_api_call_duration_seconds` (H), `aws_api_call_retries` (H),
`aws_api_requests_total`, `aws_api_request_duration_seconds` (H),
`aws_api_call_{permission,service_limit_exceeded,throttled,validation}_errors_total`,
`aws_target_group_info`. controller-runtime: `controller_runtime_reconcile_total` (C; `result` ∈
{success,error,requeue}), `_reconcile_errors_total`, `_reconcile_time_seconds` (H), `_active_workers`,
`_max_concurrent_reconciles`, `_webhook_requests_total` (`code` ∈ {200,400,500}), `_webhook_latency_seconds`
(H); workqueue: `workqueue_depth`, `_adds_total`, `_queue_duration_seconds` (H), `_work_duration_seconds`
(H), `_retries_total`. ⚠ LBC does NOT emit `rest_client_*` (cluster-autoscaler only).
> ✅ **SK-10 live capture (LBC v2.13.0, k8s-monitoring annotation autodiscovery → `job="aws-load-balancer-controller"`,
> ns `kube-system`, 2026-06-14).** Series confirmed flowing with example label/value pairs:
> `awslbc_controller_cache_object_total{resource="ingress"}=1`, `{resource="service"}=60`,
> `{resource="targetgroupbinding"}=0` (one series per `resource` value, GAUGE);
> `awslbc_controller_top_talkers{controller=<c>,name=<obj>,namespace=<ns>}` — carries **`controller`/`name`/`namespace`**
> labels (e.g. `{controller="albgateway",name="tailscale",namespace="envoy-gateway-system"}=1`), NOT bare.
> `controller` uses SHORT-FORM names here (the reference cluster recon 2026-06-16): `ingress`, `albgateway`, `nlbgateway`,
> `targetgroupbinding` — distinct from the long-form `gateway.k8s.aws/{alb,nlb}` on `controller_runtime_*`.
> The `awslbc_controller_reconcile_*` families carry `controller` too: `reconcile_stage_duration` ∈
> {ingress, targetGroupBinding}, `reconcile_errors_total` = ingress only (no `service` on either).
> `controller_runtime_*` carry a `controller` label spanning the LBC sub-controllers:
> `ingress`, `service`, `targetGroupBinding`, `albgateway`, `nlbgateway`, `gateway.k8s.aws/alb`,
> `gateway.k8s.aws/nlb`, `aws-lbc-gateway-class-controller`, `aws-lbc-loadbalancerconfiguration-controller`,
> `aws-lbc-listenerruleconfiguration-controller`, `aws-lbc-targetgroupconfiguration-controller`,
> `delayed-target-group-binding`, `gateway-{route,listenerset}-status-update-reconciler`; `result` ∈ {success,error,requeue,requeue_after}.
> Plus standard `certwatcher_*`, `go_*`, `process_*`. ⚠ NOT observed under idle (no managed Ingress/LB + no AWS API
> traffic) — likely event-gated, do NOT assume removed: `awslbc_readiness_gate_ready_seconds`,
> `awslbc_controller_reconcile_errors_total`, `awslbc_controller_reconcile_stage_duration`,
> `awslbc_webhook_{validation,mutation}_failure_total`, `awslbc_quic_target_missing_server_id`, and the
> entire `aws_api_*`/`aws_target_group_info` SDK family (these may be renamed/removed in v2.13.0 — re-verify
> under real Ingress + AWS API load before asserting).

```yaml signals
family: aws_load_balancer_controller
scope: substrate
sink: promrw
labels:
  cluster: <cluster>
  k8s_cluster_name: <cluster-name>
  job: aws-load-balancer-controller
  namespace: kube-system
metrics:
  # awslbc_ series
  - {root: awslbc_readiness_gate_ready_seconds, type: histogram, unit: seconds, v: ok, note: event-gated — not observed under idle}
  - {root: awslbc_controller_reconcile_errors_total, type: counter, unit: errors, v: ok, note: "error_category ∈ {transient,terminal}; controller=ingress only (the reference cluster 2026-06-16, no service); event-gated"}
  - {root: awslbc_controller_reconcile_stage_duration, type: histogram, unit: seconds, v: ok, note: "reconcile_stage ∈ {sync,finalize}; controller ∈ {ingress,targetGroupBinding} (the reference cluster 2026-06-16, no service); event-gated"}
  - {root: awslbc_webhook_validation_failure_total, type: counter, unit: errors, v: ok, note: event-gated}
  - {root: awslbc_webhook_mutation_failure_total, type: counter, unit: errors, v: ok, note: event-gated}
  - {root: awslbc_controller_cache_object_total, type: gauge, unit: count, v: ok, note: "resource ∈ {ingress,service,targetgroupbinding}; live-confirmed"}
  - {root: awslbc_controller_top_talkers, type: gauge, unit: count, v: ok, note: "carries controller/name/namespace labels; controller (short-form) ∈ {ingress,albgateway,nlbgateway,targetgroupbinding} (the reference cluster 2026-06-16); live-confirmed"}
  - {root: awslbc_quic_target_missing_server_id, type: counter, unit: count, v: ok, note: event-gated}
  # AWS SDK series (event-gated — entire family absent under idle)
  - {root: aws_api_calls_total, type: counter, unit: calls, v: ok, note: "service,operation,status_code labels; error_code ONLY on errored calls (I13, never error_code="")"}
  - {root: aws_api_call_duration_seconds, type: histogram, unit: seconds, v: ok}
  - {root: aws_api_call_retries, type: histogram, unit: count, v: ok}
  - {root: aws_api_requests_total, type: counter, unit: requests, v: ok}
  - {root: aws_api_request_duration_seconds, type: histogram, unit: seconds, v: ok}
  - {root: aws_api_call_permission_errors_total, type: counter, unit: errors, v: ok, note: "pattern: aws_api_call_{permission,service_limit_exceeded,throttled,validation}_errors_total"}
  - {root: aws_api_call_service_limit_exceeded_errors_total, type: counter, unit: errors, v: ok}
  - {root: aws_api_call_throttled_errors_total, type: counter, unit: errors, v: ok}
  - {root: aws_api_call_validation_errors_total, type: counter, unit: errors, v: ok}
  - {root: aws_target_group_info, type: gauge, unit: info, v: ok, note: event-gated}
  # controller-runtime series
  - {root: controller_runtime_reconcile_total, type: counter, unit: count, v: ok, note: "result ∈ {success,error,requeue,requeue_after} (live-confirmed); controller label spans LBC sub-controllers"}
  - {root: controller_runtime_reconcile_errors_total, type: counter, unit: errors, v: ok}
  - {root: controller_runtime_reconcile_time_seconds, type: histogram, unit: seconds, v: ok}
  - {root: controller_runtime_active_workers, type: gauge, unit: count, v: ok}
  - {root: controller_runtime_max_concurrent_reconciles, type: gauge, unit: count, v: ok}
  - {root: controller_runtime_webhook_requests_total, type: counter, unit: requests, v: ok, note: "code ∈ {200,400,500}"}
  - {root: controller_runtime_webhook_latency_seconds, type: histogram, unit: seconds, v: ok}
  # workqueue series
  - {root: workqueue_depth, type: gauge, unit: count, v: ok}
  - {root: workqueue_adds_total, type: counter, unit: count, v: ok}
  - {root: workqueue_queue_duration_seconds, type: histogram, unit: seconds, v: ok}
  - {root: workqueue_work_duration_seconds, type: histogram, unit: seconds, v: ok}
  - {root: workqueue_retries_total, type: counter, unit: count, v: ok}
note: "⚠ LBC does NOT emit rest_client_* (cluster-autoscaler only)"
```

---

## ExternalDNS (✅ real `job="external-dns"`, ns `external-dns`, instance `<podIP>:7979`; ~~`integrations/external-dns`~~) [slug: k8s-externaldns]

`external_dns_controller_last_sync_timestamp_seconds`, `_last_reconcile_timestamp_seconds`,
`_consecutive_soft_errors`, `_no_op_runs_total` (C), `_verified_records` (`record_type`),
`external_dns_registry_records`, `_source_records`, `_registry_endpoints_total`, `_source_endpoints_total`,
`_source_deduplicated_endpoints` (`source_type` ∈ {service,ingress}), `_registry_errors_total` (C),
`_source_errors_total` (C), `external_dns_build_info`, `external_dns_http_request_duration_seconds_count`
(C) + `_sum`. `record_type` ∈ {A,CNAME,TXT}. Also emits logrus text logs to Loki
(`{cluster, k8s_cluster_name, job="integrations/external-dns", service_name="external-dns", level="info"}`).
⚠ no controller-runtime / no `rest_client_*`.

```yaml signals
family: external_dns
scope: substrate
sink: promrw
labels:
  cluster: <cluster>
  k8s_cluster_name: <cluster-name>
  job: external-dns
  namespace: external-dns
  instance: <podIP>:7979
metrics:
  - {root: external_dns_controller_last_sync_timestamp_seconds, type: gauge, unit: seconds, v: ok}
  - {root: external_dns_controller_last_reconcile_timestamp_seconds, type: gauge, unit: seconds, v: ok}
  - {root: external_dns_controller_consecutive_soft_errors, type: gauge, unit: count, v: ok}
  - {root: external_dns_controller_no_op_runs_total, type: counter, unit: count, v: ok}
  - {root: external_dns_controller_verified_records, type: gauge, unit: count, v: ok, note: "record_type ∈ {A,CNAME,TXT}"}
  - {root: external_dns_registry_records, type: gauge, unit: count, v: ok}
  - {root: external_dns_source_records, type: gauge, unit: count, v: ok}
  - {root: external_dns_registry_endpoints_total, type: gauge, unit: count, v: ok}
  - {root: external_dns_source_endpoints_total, type: gauge, unit: count, v: ok}
  - {root: external_dns_source_deduplicated_endpoints, type: gauge, unit: count, v: ok, note: "source_type ∈ {service,ingress}"}
  - {root: external_dns_registry_errors_total, type: counter, unit: errors, v: ok}
  - {root: external_dns_source_errors_total, type: counter, unit: errors, v: ok}
  - {root: external_dns_build_info, type: gauge, unit: info, v: ok}
  - {root: external_dns_http_request_duration_seconds_count, type: counter, unit: requests, v: ok, note: "also _sum; no _bucket"}
loki_logs:
  job: integrations/external-dns
  service_name: external-dns
  level: info
note: "⚠ no controller-runtime / no rest_client_*"
```

---

## CoreDNS (✅ real `job="integrations/kubernetes/kube-dns"`, app=`kube-dns`, container=`coredns`, ns `kube-system`, instance `<podIP>:9153`; ~~`integrations/coredns`~~; base label `server="dns://:53"`) [slug: k8s-coredns]

`coredns_dns_requests_total` (C; `zone=".",view="",proto,family,type`), `coredns_dns_responses_total`
(C; `rcode,plugin="forward"`), `coredns_dns_request_duration_seconds` (H), `_request_size_bytes` (H),
`_response_size_bytes` (H), `coredns_cache_entries` (`type` ∈ {denial,success}), `_cache_hits_total`
(C), `_cache_misses_total` (C), `_cache_evictions_total` (C), `coredns_forward_healthcheck_broken_total`
(C), `coredns_proxy_request_duration_seconds` (H; `proxy_name="forward",to,rcode`),
`_proxy_healthcheck_failures_total` (C), `coredns_health_request_duration_seconds` (H),
`coredns_panics_total` (C; =0 always), `coredns_plugin_enabled` (G). Enums: `rcode` ∈ {NOERROR(93%),
NXDOMAIN(6%),SERVFAIL(1%)}, `proto` ∈ {udp(90%),tcp(10%)}, query `type` ∈ {A,AAAA,PTR,SRV,HTTPS},
`family="1"`, upstream `to` ∈ {8.8.8.8:53, 8.8.4.4:53}.

```yaml signals
family: coredns
scope: substrate
sink: promrw
labels:
  cluster: <cluster>
  k8s_cluster_name: <cluster-name>
  job: integrations/kubernetes/kube-dns
  namespace: kube-system
  instance: <podIP>:9153
  server: "dns://:53"
metrics:
  - {root: coredns_dns_requests_total, type: counter, unit: requests, v: ok, note: 'zone=".",view="",proto,family,type labels'}
  - {root: coredns_dns_responses_total, type: counter, unit: responses, v: ok, note: 'rcode,plugin="forward"'}
  - {root: coredns_dns_request_duration_seconds, type: histogram, unit: seconds, v: ok}
  - {root: coredns_dns_request_size_bytes, type: histogram, unit: bytes, v: ok}
  - {root: coredns_dns_response_size_bytes, type: histogram, unit: bytes, v: ok}
  - {root: coredns_cache_entries, type: gauge, unit: count, v: ok, note: "type ∈ {denial,success}"}
  - {root: coredns_cache_hits_total, type: counter, unit: count, v: ok}
  - {root: coredns_cache_misses_total, type: counter, unit: count, v: ok}
  - {root: coredns_cache_evictions_total, type: counter, unit: count, v: ok}
  - {root: coredns_forward_healthcheck_broken_total, type: counter, unit: count, v: ok}
  - {root: coredns_proxy_request_duration_seconds, type: histogram, unit: seconds, v: ok, note: 'proxy_name="forward",to,rcode'}
  - {root: coredns_proxy_healthcheck_failures_total, type: counter, unit: count, v: ok}
  - {root: coredns_health_request_duration_seconds, type: histogram, unit: seconds, v: ok}
  - {root: coredns_panics_total, type: counter, unit: count, v: ok, note: "=0 always"}
  - {root: coredns_plugin_enabled, type: gauge, unit: bool, v: ok}
enums:
  rcode: [NOERROR(93%), NXDOMAIN(6%), SERVFAIL(1%)]
  proto: [udp(90%), tcp(10%)]
  type: [A, AAAA, PTR, SRV, HTTPS]
  family: "1"
  upstream_to: ["8.8.8.8:53", "8.8.4.4:53"]
```

---

## AWS VPC CNI (`job="integrations/aws-vpc-cni"` — deployment-defined, SK-10; prefix `awscni_`, no namespace prefix) [slug: k8s-vpc-cni]

`awscni_eni_allocated`, `_total_ip_addresses`, `_assigned_ip_addresses`, `_eni_max`, `_ip_max`,
`_ipamd_action_inprogress` (`fn`), `_ipamd_error_count` (C), `_add_ip_req_count` (C),
`_del_ip_req_count` (C; `reason` ∈ {pod_deleted,failed_node}), `_aws_api_latency_ms_count` (C;
`api,error,status`) + `_sum`, `_aws_api_error_count` (C), `_ec2api_req_count` (C), `_ec2api_error_count`
(C), `_reconcile_count` (C), `_total_ipv4_prefixes`(=0), `_no_available_ip_addresses`(=0),
`awscni_build_info`. IPAMD `fn` ∈ {nodeIPPoolReconcile,eniIPPoolReconcile,decreaseIPPool,increaseIPPool}.
⚠ `awscni_aws_api_latency_ms` is a Prometheus **summary** emitting ONLY `_count`+`_sum` (NO `{quantile}`
series) — ✅ live-confirmed (ipamd `:61678/metrics`, 2026-06-13, SK-11 resolved): labels `api`, `error`
(string bool), `status` (HTTP code string). k8s-monitoring does NOT scrape vpc-cni by default; instance=`<podIP>:61678` (SK-10).

```yaml signals
family: awscni
scope: substrate
sink: promrw
labels:
  cluster: <cluster>
  k8s_cluster_name: <cluster-name>
  job: integrations/aws-vpc-cni    # Ⓐ deployment-defined (SK-72); not falsifiable on capture stack
  instance: <podIP>:61678
metrics:
  - {root: awscni_eni_allocated, type: gauge, unit: count, v: ok}
  - {root: awscni_total_ip_addresses, type: gauge, unit: count, v: ok}
  - {root: awscni_assigned_ip_addresses, type: gauge, unit: count, v: ok}
  - {root: awscni_eni_max, type: gauge, unit: count, v: ok}
  - {root: awscni_ip_max, type: gauge, unit: count, v: ok}
  - {root: awscni_ipamd_action_inprogress, type: gauge, unit: count, v: ok, note: "fn ∈ {nodeIPPoolReconcile,eniIPPoolReconcile,decreaseIPPool,increaseIPPool}"}
  - {root: awscni_ipamd_error_count, type: counter, unit: errors, v: ok}
  - {root: awscni_add_ip_req_count, type: counter, unit: count, v: ok}
  - {root: awscni_del_ip_req_count, type: counter, unit: count, v: ok, note: "reason ∈ {pod_deleted,failed_node}"}
  - {root: awscni_aws_api_latency_ms_count, type: counter, unit: count, v: ok, note: "summary: ONLY _count+_sum; NO {quantile}; api,error(string bool),status(HTTP code string) — SK-11 live-confirmed"}
  - {root: awscni_aws_api_latency_ms_sum, type: counter, unit: ms, v: ok, note: "summary _sum counterpart"}
  - {root: awscni_aws_api_error_count, type: counter, unit: errors, v: ok}
  - {root: awscni_ec2api_req_count, type: counter, unit: count, v: ok}
  - {root: awscni_ec2api_error_count, type: counter, unit: errors, v: ok}
  - {root: awscni_reconcile_count, type: counter, unit: count, v: ok}
  - {root: awscni_total_ipv4_prefixes, type: gauge, unit: count, v: ok, note: "=0"}
  - {root: awscni_no_available_ip_addresses, type: gauge, unit: count, v: ok, note: "=0"}
  - {root: awscni_build_info, type: gauge, unit: info, v: ok}
note: "⚠ k8s-monitoring does NOT scrape vpc-cni by default; job label Ⓐ"
```

---

## cert-manager (✅ real `job="cert-manager"` annotation-autodiscovery default OR `job="integrations/cert-manager"` when `job_mode: integration`; ns `cert-manager`, container `cert-manager-controller`, instance `<podIP>:9402`) [slug: k8s-cert-manager]

*Provenance: live capture 2026-06-15.*

`certmanager_certificate_ready_status` (G 0/1; per-cert labels carry `namespace="cert-manager"` AND
`exported_namespace`=cert's real ns; `name,condition,issuer_name,issuer_kind,issuer_group="cert-manager.io"`),
`_expiration_timestamp_seconds`, `_renewal_timestamp_seconds`, `_not_after_timestamp_seconds`,
`_not_before_timestamp_seconds`. **`certmanager_certificate_challenge_status`** is event-gated (NOT emitted
at idle — only appears when ACME challenges are active; omit from idle baseline). `certmanager_issuer_ready_status`
(G 0/1; Issuer), `_clusterissuer_ready_status` (G 0/1; ClusterIssuer),
`certmanager_http_acme_client_request_count` (C; `scheme,host,action,method,status`),
`_request_duration_seconds_count` (C) + `_sum`, `certmanager_controller_sync_call_count` (C; `controller`),
`_sync_error_count` (C; =0; `controller`), `certmanager_clock_time_seconds` (G), `certmanager_clock_time_seconds_gauge` (G;
BOTH emitted simultaneously — live-confirmed). Enums: `condition` ∈ {True,False,Unknown} (all three emitted
per cert/issuer), `issuer_kind` ∈ {ClusterIssuer,Issuer}.

```yaml signals
family: certmanager
scope: substrate
sink: promrw
labels:
  cluster: <cluster>
  k8s_cluster_name: <cluster-name>
  job: cert-manager                  # annotation-autodiscovery default; "integrations/cert-manager" when job_mode=integration
  namespace: cert-manager
  instance: <podIP>:9402
metrics:
  # Per-cert series — namespace="cert-manager"; exported_namespace=cert's real ns
  - {root: certmanager_certificate_ready_status, type: gauge, unit: bool, v: ok, note: "0/1; namespace=cert-manager AND exported_namespace=cert-real-ns; name,condition,issuer_name,issuer_kind,issuer_group=cert-manager.io"}
  - {root: certmanager_certificate_expiration_timestamp_seconds, type: gauge, unit: seconds, v: ok}
  - {root: certmanager_certificate_renewal_timestamp_seconds, type: gauge, unit: seconds, v: ok}
  - {root: certmanager_certificate_not_after_timestamp_seconds, type: gauge, unit: seconds, v: ok}
  - {root: certmanager_certificate_not_before_timestamp_seconds, type: gauge, unit: seconds, v: ok}
  # certmanager_certificate_challenge_status: event-gated — NOT emitted at idle (ACME challenges only)
  - {root: certmanager_issuer_ready_status, type: gauge, unit: bool, v: ok, note: "0/1; Issuer"}
  - {root: certmanager_clusterissuer_ready_status, type: gauge, unit: bool, v: ok, note: "0/1; ClusterIssuer"}
  - {root: certmanager_http_acme_client_request_count, type: counter, unit: requests, v: ok, note: "scheme,host,action,method,status"}
  - {root: certmanager_http_acme_client_request_duration_seconds, type: summary, unit: seconds, v: ok, note: "Prometheus summary — quantile{0.5,0.9,0.99} + _sum + _count; NO _bucket"}
  - {root: certmanager_controller_sync_call_count, type: counter, unit: calls, v: ok, note: "controller label"}
  - {root: certmanager_controller_sync_error_count, type: counter, unit: errors, v: ok, note: "=0; controller label"}
  - {root: certmanager_clock_time_seconds, type: gauge, unit: seconds, v: ok, note: "BOTH this AND certmanager_clock_time_seconds_gauge emitted simultaneously (live-confirmed 2026-06-15)"}
  - {root: certmanager_clock_time_seconds_gauge, type: gauge, unit: seconds, v: ok}
enums:
  condition: [True, False, Unknown]   # all three emitted per cert/issuer
  issuer_kind: [ClusterIssuer, Issuer]
note: "certmanager_certificate_challenge_status is event-gated (not emitted at idle)"
```

---

## etcd (`job="integrations/etcd"`, instance `<nodeIP>:2381`, one per quorum node, capped at 3) [slug: k8s-etcd]

*Provenance: doc-sourced (Grafana cloud-onboarding allowlist + etcd mixin). Managed EKS does not expose
etcd directly — values are representative/plausible healthy-steady-state. All `v: PENDING` (see cantfind.md SK-49).
Scope: substrate; no blueprint label. One Construct instance per cluster covers all quorum nodes.*

```yaml signals
family: etcd
scope: substrate
sink: promrw
labels:
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  job: integrations/etcd
  instance: <nodeIP>:2381    # one per control-plane node (capped at 3 quorum members)
metrics:
  # etcd_server_* — per instance
  - {root: etcd_server_has_leader, type: gauge, unit: bool, v: PENDING, note: "=1 (healthy); no extra labels"}
  - {root: etcd_server_leader_changes_seen_total, type: counter, unit: count, v: PENDING}
  - {root: etcd_server_proposals_failed_total, type: counter, unit: count, v: PENDING, note: "=0 (healthy)"}
  - {root: etcd_server_quota_backend_bytes, type: gauge, unit: bytes, v: PENDING, note: "~8GiB default"}
  # etcd_disk_* — per instance
  - {root: etcd_disk_wal_fsync_duration_seconds, type: histogram, unit: seconds, v: PENDING, note: "fast NVMe: typical 1-4ms; buckets doc-sourced — see SK-49"}
  - {root: etcd_disk_backend_commit_duration_seconds, type: histogram, unit: seconds, v: PENDING, note: "buckets doc-sourced — see SK-49"}
  # etcd_mvcc_db_* — per instance
  - {root: etcd_mvcc_db_total_size_in_bytes, type: gauge, unit: bytes, v: PENDING, note: "~100MB representative"}
  - {root: etcd_mvcc_db_total_size_in_use_in_bytes, type: gauge, unit: bytes, v: PENDING}
  # etcd_network_client_* — per instance
  - {root: etcd_network_client_grpc_received_bytes_total, type: counter, unit: bytes, v: PENDING}
  - {root: etcd_network_client_grpc_sent_bytes_total, type: counter, unit: bytes, v: PENDING}
  # etcd_network_peer_* — per instance × peer (To label = peer ID)
  - {root: etcd_network_peer_received_bytes_total, type: counter, unit: bytes, v: PENDING, note: "To label = peer ID"}
  - {root: etcd_network_peer_sent_bytes_total, type: counter, unit: bytes, v: PENDING}
  - {root: etcd_network_peer_sent_failures_total, type: counter, unit: count, v: PENDING, note: "=0 (healthy)"}
  - {root: etcd_network_peer_round_trip_time_seconds, type: histogram, unit: seconds, v: PENDING, note: "per instance×peer; To label; buckets doc-sourced — see SK-49"}
  # gRPC server — per instance × (grpc_type, grpc_service, grpc_method, grpc_code)
  - {root: grpc_server_handled_total, type: counter, unit: count, v: PENDING, note: "grpc_type,grpc_service,grpc_method,grpc_code labels"}
  - {root: grpc_server_started_total, type: counter, unit: count, v: PENDING}
  - {root: grpc_server_handling_seconds, type: histogram, unit: seconds, v: PENDING, note: "grpc_type,grpc_service,grpc_method (no grpc_code); buckets doc-sourced — see SK-49"}
  # process self-metrics — per instance
  - {root: process_resident_memory_bytes, type: gauge, unit: bytes, v: PENDING, note: "~100MB representative"}
enums:
  grpc_type: [unary]
  grpc_service: ["etcdserverpb.KV", "etcdserverpb.Watch", "etcdserverpb.Lease"]
  grpc_method: [Range, Put, Watch]
  grpc_code: [OK]
note: "v: PENDING — managed EKS does not expose etcd; all values doc-sourced. See cantfind.md SK-49 for resolution path (bare-metal/self-managed etcd capture)"
```

---

## Cluster Autoscaler (`job="integrations/cluster-autoscaler"` Ⓐ; emits `rest_client_*`) [slug: k8s-cluster-autoscaler]

> ⚠ Note (live capture): EKS+Karpenter stacks run **Karpenter, not cluster-autoscaler** — real `job="karpenter"`, ns `kube-system`, `karpenter_build_info` version `1.13.0`. A future `karpenter` add-on construct is the realistic EKS equivalent; cluster-autoscaler remains valid for non-Karpenter clusters.

`cluster_autoscaler_cluster_safe_to_autoscale` (G 0/1; =1), `_nodes_count` (`state`),
`_node_groups_count` (`node_group_type`), `_node_group_target_count`, `_unschedulable_pods_count`
(`type` ∈ {unschedulable,timeout}), `_cluster_cpu_current_cores`, `_cluster_memory_current_bytes`,
`_cpu_limits_cores` (`direction` ∈ {up,down}), `_memory_limits_bytes`, `_last_activity` (`activity`),
`_function_duration_seconds` (H; `function`), `_errors_total` (C), `_scaled_up_nodes_total` (C),
`_failed_scale_ups_total` (C; `reason`), `_scaled_down_nodes_total` (C; `reason`),
`_evicted_pods_total` (C; `eviction_result`), `_unneeded_nodes_count`, `_unremovable_nodes_count`
(`reason`), `_scale_down_in_cooldown` (G 0/1), `_aws_request_duration_seconds` (H; `endpoint,status`),
`rest_client_requests_total` (C; `code,method,host`). ⚠ Three metrics intentionally NOT emitted:
`binpacking_heterogeneity`, `max_node_skip_eval_duration_seconds`, `inconsistent_instances_migs_count`.

```yaml signals
family: cluster_autoscaler
scope: substrate
sink: promrw
labels:
  cluster: <cluster>
  k8s_cluster_name: <cluster-name>
  job: integrations/cluster-autoscaler    # Ⓐ assumed — absent on EKS-Karpenter stacks
metrics:
  - {root: cluster_autoscaler_cluster_safe_to_autoscale, type: gauge, unit: bool, v: ok, note: "0/1; =1"}
  - {root: cluster_autoscaler_nodes_count, type: gauge, unit: count, v: ok, note: state label}
  - {root: cluster_autoscaler_node_groups_count, type: gauge, unit: count, v: ok, note: node_group_type label}
  - {root: cluster_autoscaler_node_group_target_count, type: gauge, unit: count, v: ok}
  - {root: cluster_autoscaler_unschedulable_pods_count, type: gauge, unit: count, v: ok, note: "type ∈ {unschedulable,timeout}"}
  - {root: cluster_autoscaler_cluster_cpu_current_cores, type: gauge, unit: cores, v: ok}
  - {root: cluster_autoscaler_cluster_memory_current_bytes, type: gauge, unit: bytes, v: ok}
  - {root: cluster_autoscaler_cpu_limits_cores, type: gauge, unit: cores, v: ok, note: "direction ∈ {up,down}"}
  - {root: cluster_autoscaler_memory_limits_bytes, type: gauge, unit: bytes, v: ok}
  - {root: cluster_autoscaler_last_activity, type: gauge, unit: seconds, v: ok, note: activity label}
  - {root: cluster_autoscaler_function_duration_seconds, type: histogram, unit: seconds, v: ok, note: function label}
  - {root: cluster_autoscaler_errors_total, type: counter, unit: errors, v: ok}
  - {root: cluster_autoscaler_scaled_up_nodes_total, type: counter, unit: count, v: ok}
  - {root: cluster_autoscaler_failed_scale_ups_total, type: counter, unit: count, v: ok, note: reason label}
  - {root: cluster_autoscaler_scaled_down_nodes_total, type: counter, unit: count, v: ok, note: reason label}
  - {root: cluster_autoscaler_evicted_pods_total, type: counter, unit: count, v: ok, note: eviction_result label}
  - {root: cluster_autoscaler_unneeded_nodes_count, type: gauge, unit: count, v: ok}
  - {root: cluster_autoscaler_unremovable_nodes_count, type: gauge, unit: count, v: ok, note: reason label}
  - {root: cluster_autoscaler_scale_down_in_cooldown, type: gauge, unit: bool, v: ok, note: "0/1"}
  - {root: cluster_autoscaler_aws_request_duration_seconds, type: histogram, unit: seconds, v: ok, note: "endpoint,status labels"}
  - {root: rest_client_requests_total, type: counter, unit: requests, v: ok, note: "code,method,host labels"}
not_emitted: [binpacking_heterogeneity, max_node_skip_eval_duration_seconds, inconsistent_instances_migs_count]
```

---

## AWS EBS CSI Driver (`job="integrations/aws-ebs-csi-driver"` — deployment-defined, SK-10) [slug: k8s-ebs-csi]

> ✅ **Live-verified (staff stack, aws-ebs-csi-driver controller `:3301` metrics endpoint, 2026-06-13 —
> SK-12 resolved w/ correction).** The driver controller endpoint emits ONLY these three families:

`aws_ebs_csi_api_request_duration_seconds` (H; `request` = EC2 API request type),
`aws_ebs_csi_ec2_collector_duration_seconds` (H; controller-level, no per-volume label),
`aws_ebs_csi_ec2_collector_scrapes_total` (C; controller-level, no per-volume label).
⚠ **The driver emits NO `volume_id`-labelled series at all** — synthkit's former
`aws_ebs_csi_{read,write}_*`/`_volume_queue_length`/`_*_io_latency_seconds`/`_exceeded_*` volume series
are DROPPED. Per-volume identity lives in CloudWatch `AWS/EBS dimension_VolumeId` (§2.1.5, SK-6) and
kubelet `kubelet_volume_stats_*` (labelled by `persistentvolumeclaim`, §2.2). `_api_request_errors_total`/
`_api_request_throttles_total`/`csi_sidecar_operations_seconds` are NOT emitted (errors/throttles series
only exist under actual errors — reproduce the absence; sidecar metrics are a separate endpoint).
⚠ Deprecated `cloudprovider_aws_*` NOT emitted. The `job` label is deployment-defined: k8s-monitoring does
NOT scrape EBS-CSI by default (EKS-managed addon); instance=`<podIP>:3301` (SK-10).

```yaml signals
family: aws_ebs_csi
scope: substrate
sink: promrw
labels:
  cluster: <cluster>
  k8s_cluster_name: <cluster-name>
  job: integrations/aws-ebs-csi-driver    # Ⓐ deployment-defined (SK-72); not falsifiable on capture stack
  instance: <podIP>:3301
metrics:
  - {root: aws_ebs_csi_api_request_duration_seconds, type: histogram, unit: seconds, v: ok, note: "request label = EC2 API request type; controller-level, NO volume_id"}
  - {root: aws_ebs_csi_ec2_collector_duration_seconds, type: histogram, unit: seconds, v: ok, note: "controller-level, no per-volume label"}
  - {root: aws_ebs_csi_ec2_collector_scrapes_total, type: counter, unit: count, v: ok, note: "controller-level, no per-volume label"}
not_emitted: [aws_ebs_csi_api_request_errors_total, aws_ebs_csi_api_request_throttles_total, csi_sidecar_operations_seconds, "cloudprovider_aws_* (deprecated)"]
dropped_from_synthkit: ["aws_ebs_csi_{read,write}_*", "aws_ebs_csi_volume_queue_length", "aws_ebs_csi_*_io_latency_seconds", "aws_ebs_csi_exceeded_*"]
note: "⚠ NO volume_id-labelled series; per-volume lives in aws_ebs_* (CW) + kubelet_volume_stats_* (kubelet)"
```

---

## KSM Ingress (`job="integrations/kubernetes/kube-state-metrics"` — reuses KSM job) [slug: k8s-ksm-ingress]

`kube_ingress_info` (`ingressclass`), `kube_ingress_path` (`host,path,path_type,service_name,
service_port`), `kube_ingress_tls` (`tls_host,secret`), `kube_ingress_created`, `kube_ingress_labels`,
`kube_ingress_annotations` (ALPHA), `kube_ingress_metadata_resource_version` (ALPHA). ⚠ **`cluster`
injection MANDATORY (I16):** KSM's default `kube_ingress_*` labels are only `{namespace,ingress}`, so
two clusters with same namespace+name collide — the emitter explicitly injects `cluster`+
`k8s_cluster_name`. This is the only KSM family where the emitter adds `cluster` beyond the base.

```yaml signals
family: kube_ingress
scope: substrate
sink: promrw
labels:
  cluster: <cluster>                      # MANDATORY injection (I16) — default KSM labels omit it
  k8s_cluster_name: <cluster-name>
  job: integrations/kubernetes/kube-state-metrics
metrics:
  - {root: kube_ingress_info, type: gauge, unit: info, v: ok, note: ingressclass label}
  - {root: kube_ingress_path, type: gauge, unit: info, v: ok, note: "host,path,path_type,service_name,service_port labels"}
  - {root: kube_ingress_tls, type: gauge, unit: info, v: ok, note: "tls_host,secret labels"}
  - {root: kube_ingress_created, type: gauge, unit: seconds, v: ok}
  - {root: kube_ingress_labels, type: gauge, unit: info, v: ok}
  - {root: kube_ingress_annotations, type: gauge, unit: info, v: assumed, note: ALPHA}
  - {root: kube_ingress_metadata_resource_version, type: gauge, unit: count, v: assumed, note: ALPHA}
note: "⚠ cluster injection MANDATORY (I16): default kube_ingress_* only carries {namespace,ingress}; emitter explicitly adds cluster+k8s_cluster_name — the only KSM family requiring this"
```

---

## Karpenter (`job="karpenter"`, ns `kube-system`, container `controller`, port 8080; v1.13.0 — SK-72 resolved 2026-06-16) [slug: k8s-karpenter]

*Provenance: live a live reference EKS cluster capture 2026-06-16 (svc-group-b.md §1.A). Container name `controller` (not `karpenter`) — live-confirmed via `kube_pod_container_info`. EKS-Karpenter replaces cluster-autoscaler on this stack.*

> ⚠ **Cardinality cap — offering price.** `karpenter_cloudprovider_instance_type_offering_price_estimate` is ~9261 series live (1034 instance types × ~3 AZs × 3 capacity types). Synthkit emits a BOUNDED representative subset: 10 instance types × 3 AZs × 3 capacity types = 90 series. `capacity_type` ∈ {on-demand, spot, reserved} (the reference cluster recon 2026-06-16 — Karpenter v1.x always emits all three; `reserved` carries `offering_available=0` when no capacity reservation is configured). Dashboards that topk-filter on this metric still work.

> ⚠ **Two SUMMARY metrics** (not histograms): `karpenter_nodes_termination_duration_seconds` and `karpenter_pods_startup_duration_seconds` emit quantile series (quantile ∈ {0,0.5,0.9,0.99,1}) + _count/_sum with NO _bucket. All other duration/scheduling families are histograms.

> ⚠ **`nodepools_allowed_disruptions` reason is PascalCase** (Empty|Underutilized) — ALL other karpenter reason labels are lowercase. Live-confirmed quirk.

> ⚠ **Per-pod correlation:** leader-elected domain metrics (`karpenter_*` families) stamp the leader pod only. `go_*` / `process_*` / `controller_runtime_*` stamp both replicas. `kube_pod_container_info.container="controller"` (NOT "karpenter" — that is the deployment name).

Key families (leader-only unless noted): `karpenter_build_info` (G=1; BOTH replicas — carries `version,goversion,goarch,commit`), `karpenter_cluster_state_node_count` (G), `karpenter_cluster_state_synced` (G=1), `karpenter_cluster_utilization_percent` (G; `resource_type` ∈ {cpu,memory,pods,ephemeral_storage}), `karpenter_cloudprovider_duration_seconds` (H; `controller,method,provider=aws`; buckets 0.005…10+Inf), `karpenter_cloudprovider_batcher_batch_size` (H; custom int buckets 1,2,4,5,10…1000+Inf), `karpenter_cloudprovider_batcher_batch_time_seconds` (H), `karpenter_cloudprovider_errors_total` (C; `controller,error,method,provider`), `karpenter_cloudprovider_instance_type_{cpu_cores,memory_bytes,offering_available,offering_price_estimate}` (G; `instance_type[,zone,capacity_type]`), `karpenter_nodeclaims_{created,disrupted,terminated,instance_termination_duration_seconds,termination_duration_seconds}_*` (C/H/SUMMARY), `karpenter_nodepools_{usage,limit,cost_total,allowed_disruptions,nodes_consuming_budgets}` (G), `karpenter_nodes_{allocatable,created_total,current_lifetime_seconds,drained_total,lifetime_duration_seconds,system_overhead,terminated_total,termination_duration_seconds,total_daemon_limits,total_daemon_requests,total_pod_limits,total_pod_requests}`, `karpenter_pods_{bound_duration_seconds,drained_total,eviction_requests_total,provisioning_{bound,startup}_duration_seconds,scheduling_decision_duration_seconds,startup_duration_seconds,state}` (C/G/H/SUMMARY), `karpenter_scheduler_{ignored_pods_count,queue_depth,scheduling_duration_seconds,unschedulable_pods_count}`, `karpenter_interruption_{deleted_messages_total,message_queue_duration_seconds,received_messages_total}`, `karpenter_voluntary_disruption_{consolidation_timeouts_total,decision_evaluation_duration_seconds,decisions_by_nodepool_total,decisions_total,eligible_nodes}`.

Rich `karpenter_nodes_allocatable` labels (live recon): `arch,capacity_type,instance_category,instance_cpu,instance_cpu_manufacturer,instance_ebs_bandwidth,instance_family,instance_generation,instance_hypervisor,instance_memory,instance_network_bandwidth,instance_size,instance_tenancy,instance_type,node_name,nodepool,os,region,resource_type,zone,zone_id` (plus `instance_capability_flex,instance_cpu_sustained_clock_speed_mhz,instance_encryption_in_transit_supported`).

All-pods families: `controller_runtime_reconcile_total` (C; `controller,result`; controllers: node.termination,node.drift,nodeclaim.disruption,nodeclaim.lifecycle,disruption,provisioner), `controller_runtime_active_workers` (G), `controller_runtime_max_concurrent_reconciles` (G), `go_goroutines`, `go_threads`, `go_gc_duration_seconds` (SUMMARY quantile 0/0.25/0.5/0.75/1), `go_memstats_{alloc_bytes,heap_alloc_bytes,heap_inuse_bytes}`, `process_{cpu_seconds_total,resident_memory_bytes,open_fds,max_fds}`.

Scheduling histogram bucket sets (live recon svc-group-b.md §1.A):
- `cloudprovider_duration_seconds` / `batcher_batch_time_seconds`: le = `0.005,0.01,0.025,0.05,0.1,0.25,0.5,1.0,2.5,5.0,10.0,+Inf` (12 buckets)
- `scheduler_scheduling_duration_seconds` / `pods_scheduling_decision_duration_seconds` / `interruption_message_queue_duration_seconds` / `voluntary_disruption_decision_evaluation_duration_seconds` / `pods_bound_duration_seconds` / `pods_provisioning_bound_duration_seconds` / `pods_provisioning_startup_duration_seconds` / `nodeclaims_instance_termination_duration_seconds` / `nodeclaims_termination_duration_seconds` (histogram variant): le = `0.005,0.01,0.025,0.05,0.1,0.15,0.2,0.25,0.3,0.35,0.4,0.45,0.5,0.6,0.7,0.8,0.9,1.0,1.25,1.5,1.75,2.0,2.5,3.0,3.5,4.0,4.5,5.0,6.0,7.0,8.0,9.0,10.0,15.0,20.0,25.0,30.0,40.0,50.0,60.0,120.0,150.0,300.0,450.0,600.0,+Inf` (46 buckets)
- `batcher_batch_size`: le = `1,2,4,5,10,15,20,25,30,40,50,60,70,80,90,100,125,150,175,200,225,250,275,300,350,400,450,500,550,600,700,800,900,1000,+Inf` (35 buckets)
- `nodes_lifetime_duration_seconds`: le = `900,1800,2700,3600,7200,14400,21600,28800,36000,43200,57600,72000,86400,172800,259200,432000,864000,1.296e6,1.728e6,2.16e6,2.592e6,+Inf` (22 buckets)

```yaml signals
family: karpenter
scope: substrate
sink: promrw
labels:
  cluster: <cluster>
  k8s_cluster_name: <cluster-name>
  job: karpenter
  namespace: kube-system
  container: controller          # NOT "karpenter" — kube_pod_container_info.container=controller
  instance: <podIP>:8080
  endpoint: http-metrics
metrics:
  # Info / state (leader)
  - {root: karpenter_build_info, type: gauge, unit: info, v: ok, note: "BOTH replicas; version=1.13.0 goversion=go1.26.4 goarch=arm64 commit=2be9554"}
  - {root: karpenter_cluster_state_node_count, type: gauge, unit: count, v: ok}
  - {root: karpenter_cluster_state_synced, type: gauge, unit: bool, v: ok, note: "=1"}
  - {root: karpenter_cluster_state_unsynced_time_seconds, type: gauge, unit: seconds, v: ok, note: "=0 healthy"}
  - {root: karpenter_cluster_utilization_percent, type: gauge, unit: percent, v: ok, note: "resource_type ∈ {cpu,memory,pods,ephemeral_storage}"}
  # Cloudprovider (leader)
  - {root: karpenter_cloudprovider_duration_seconds, type: histogram, unit: seconds, v: ok, note: "controller,method,provider=aws; le 0.005…10+Inf"}
  - {root: karpenter_cloudprovider_batcher_batch_size, type: histogram, unit: count, v: ok, note: "le 1,2,4,5,10…1000,+Inf"}
  - {root: karpenter_cloudprovider_batcher_batch_time_seconds, type: histogram, unit: seconds, v: ok}
  - {root: karpenter_cloudprovider_errors_total, type: counter, unit: errors, v: ok, note: "controller,error(InsufficientCapacityError|NodeClaimNotFoundError),method,provider"}
  - {root: karpenter_cloudprovider_instance_type_cpu_cores, type: gauge, unit: cores, v: ok, note: instance_type label}
  - {root: karpenter_cloudprovider_instance_type_memory_bytes, type: gauge, unit: bytes, v: ok, note: instance_type label}
  - {root: karpenter_cloudprovider_instance_type_offering_available, type: gauge, unit: bool, v: ok, note: "instance_type,zone,capacity_type∈{on-demand,spot,reserved}; reserved=0 (no reservation configured); bounded subset — see cardinality cap note"}
  - {root: karpenter_cloudprovider_instance_type_offering_price_estimate, type: gauge, unit: usd_per_hour, v: ok, note: "instance_type,zone,capacity_type∈{on-demand,spot,reserved}; ~9261 live; synthkit emits 90 (cardinality cap)"}
  # NodeClaims (leader)
  - {root: karpenter_nodeclaims_created_total, type: counter, unit: count, v: ok, note: "min_values_relaxed,nodepool,reason=provisioned"}
  - {root: karpenter_nodeclaims_disrupted_total, type: counter, unit: count, v: ok, note: "nodepool,reason(empty|spot_interrupted|underutilized|insufficient_capacity); capacity_type ABSENT when reason=insufficient_capacity (I13)"}
  - {root: karpenter_nodeclaims_instance_termination_duration_seconds, type: histogram, unit: seconds, v: ok, note: "nodepool; scheduling buckets 46"}
  - {root: karpenter_nodeclaims_terminated_total, type: counter, unit: count, v: ok, note: "nodepool,reason"}
  - {root: karpenter_nodeclaims_termination_duration_seconds, type: histogram, unit: seconds, v: ok, note: "nodepool; scheduling buckets 46"}
  # NodePools (leader)
  - {root: karpenter_nodepools_allowed_disruptions, type: gauge, unit: count, v: ok, note: "nodepool,reason PascalCase ∈ {Empty,Underutilized} — exception to lowercase-reason rule"}
  - {root: karpenter_nodepools_cost_total, type: gauge, unit: usd_per_hour, v: ok, note: nodepool label}
  - {root: karpenter_nodepools_limit, type: gauge, unit: various, v: ok, note: "nodepool,resource_type ∈ {cpu,memory}"}
  - {root: karpenter_nodepools_nodes_consuming_budgets, type: gauge, unit: count, v: ok, note: nodepool label}
  - {root: karpenter_nodepools_usage, type: gauge, unit: various, v: ok, note: "nodepool,resource_type ∈ {cpu,memory,ephemeral_storage,hugepages_*,nodes,pods,vpc.amazonaws.com/pod_eni}"}
  # Nodes (leader)
  - {root: karpenter_nodes_allocatable, type: gauge, unit: various, v: ok, note: "rich topology labels: arch,capacity_type,instance_*,node_name,nodepool,os,region,resource_type,zone,zone_id"}
  - {root: karpenter_nodes_created_total, type: counter, unit: count, v: ok, note: "nodepool,capacity_type,arch,os,instance_type"}
  - {root: karpenter_nodes_current_lifetime_seconds, type: gauge, unit: seconds, v: ok, note: "arch,capacity_type,instance_type,node_name,nodepool,os,region,zone,zone_id"}
  - {root: karpenter_nodes_drained_total, type: counter, unit: count, v: ok, note: "nodepool,reason"}
  - {root: karpenter_nodes_lifetime_duration_seconds, type: histogram, unit: seconds, v: ok, note: "nodepool; lifetime buckets 900s…2.592e6s,+Inf"}
  - {root: karpenter_nodes_system_overhead, type: gauge, unit: various, v: ok, note: "same topology labels as nodes_allocatable"}
  - {root: karpenter_nodes_terminated_total, type: counter, unit: count, v: ok, note: "nodepool,capacity_type,arch,os,instance_type"}
  - {root: karpenter_nodes_termination_duration_seconds, type: summary, unit: seconds, v: ok, note: "SUMMARY — quantile ∈ {0,0.5,0.9,0.99,1} + _count/_sum; NO _bucket; nodepool label"}
  - {root: karpenter_nodes_total_daemon_limits, type: gauge, unit: various, v: ok, note: "same topology as nodes_allocatable"}
  - {root: karpenter_nodes_total_daemon_requests, type: gauge, unit: various, v: ok}
  - {root: karpenter_nodes_total_pod_limits, type: gauge, unit: various, v: ok}
  - {root: karpenter_nodes_total_pod_requests, type: gauge, unit: various, v: ok}
  # Pods (leader)
  - {root: karpenter_pods_bound_duration_seconds, type: histogram, unit: seconds, v: ok, note: "scheduling buckets 46"}
  - {root: karpenter_pods_drained_total, type: counter, unit: count, v: ok, note: nodepool label}
  - {root: karpenter_pods_eviction_requests_total, type: counter, unit: count, v: ok, note: nodepool label}
  - {root: karpenter_pods_provisioning_bound_duration_seconds, type: histogram, unit: seconds, v: ok}
  - {root: karpenter_pods_provisioning_startup_duration_seconds, type: histogram, unit: seconds, v: ok}
  - {root: karpenter_pods_scheduling_decision_duration_seconds, type: histogram, unit: seconds, v: ok}
  - {root: karpenter_pods_startup_duration_seconds, type: summary, unit: seconds, v: ok, note: "SUMMARY — quantile ∈ {0,0.5,0.9,0.99,1} + _count/_sum; NO _bucket"}
  - {root: karpenter_pods_state, type: gauge, unit: count, v: ok, note: "arch,capacity_type,exported_namespace,instance_type,nodepool,phase(Running|Pending|Failed),ready,scheduled,zone"}
  # Scheduler (leader)
  - {root: karpenter_scheduler_ignored_pods_count, type: gauge, unit: count, v: ok}
  - {root: karpenter_scheduler_queue_depth, type: gauge, unit: count, v: ok}
  - {root: karpenter_scheduler_scheduling_duration_seconds, type: histogram, unit: seconds, v: ok}
  - {root: karpenter_scheduler_unschedulable_pods_count, type: gauge, unit: count, v: ok}
  # Interruption (leader)
  - {root: karpenter_interruption_deleted_messages_total, type: counter, unit: count, v: ok}
  - {root: karpenter_interruption_message_queue_duration_seconds, type: histogram, unit: seconds, v: ok}
  - {root: karpenter_interruption_received_messages_total, type: counter, unit: count, v: ok, note: "message_type ∈ {instance_terminated,no_op,rebalance_recommendation,scheduled_change,spot_interrupted}"}
  # Voluntary disruption (leader)
  - {root: karpenter_voluntary_disruption_consolidation_timeouts_total, type: counter, unit: count, v: ok}
  - {root: karpenter_voluntary_disruption_decision_evaluation_duration_seconds, type: histogram, unit: seconds, v: ok, note: "consolidation_type ∈ {empty,single,multi}; scheduling buckets 46"}
  - {root: karpenter_voluntary_disruption_decisions_by_nodepool_total, type: counter, unit: count, v: ok, note: "consolidation_type,decision,nodepool,reason"}
  - {root: karpenter_voluntary_disruption_decisions_total, type: counter, unit: count, v: ok, note: "consolidation_type,decision,reason"}
  - {root: karpenter_voluntary_disruption_eligible_nodes, type: gauge, unit: count, v: ok, note: "reason ∈ {drifted,empty,underutilized}"}
  # All-pods shared
  - {root: controller_runtime_reconcile_total, type: counter, unit: count, v: ok, note: "controller ∈ {node.termination,node.drift,nodeclaim.disruption,nodeclaim.lifecycle,disruption,provisioner}; result ∈ {error,requeue,requeue_after,success}"}
  - {root: controller_runtime_active_workers, type: gauge, unit: count, v: ok}
  - {root: controller_runtime_max_concurrent_reconciles, type: gauge, unit: count, v: ok}
  - {root: go_goroutines, type: gauge, unit: count, v: ok}
  - {root: go_gc_duration_seconds, type: summary, unit: seconds, v: ok, note: "SUMMARY quantile ∈ {0,0.25,0.5,0.75,1}"}
  - {root: process_cpu_seconds_total, type: counter, unit: seconds, v: ok}
  - {root: process_resident_memory_bytes, type: gauge, unit: bytes, v: ok}
note: "container='controller' (NOT 'karpenter'); offering_price capped at 90 series (3 capacity_types incl reserved); 2 SUMMARYs (termination+startup); nodepools_allowed_disruptions reason PascalCase"
```

---

## Argo CD (`job="argocd-metrics"` / `"argocd-server-metrics"` / `"argocd-repo-server-metrics"`, ns `argocd` — v3.4.3, 2026-06-16) [slug: k8s-argocd]

*Provenance: live a live reference EKS cluster capture 2026-06-16 (svc-group-b.md §2.A). 49 metric names. Version v3.4.3 (Helm chart argo-cd-9.5.21).*

> ⚠ **Four distinct scrape jobs / ports** — all substrate-scoped but each component has its own job label:
> `job=argocd-metrics` → app-controller (port 8082) + applicationset-controller (port 8080)
> `job=argocd-server-metrics` → server (port 8083)
> `job=argocd-repo-server-metrics` → repo-server (port 8084)
> `job=argocd-metrics` also used for redis_exporter sidecar (port 9121) scraped from argocd-redis pod.

> ⚠ **Two redis histogram families (version artifact):** app-controller emits `argocd_redis_request_duration_{bucket,count,sum}` (le 0.01,0.05,0.1,0.25,0.5,1.0,2.0,+Inf; has `hostname` label); repo-server emits `argocd_redis_request_duration_seconds_{bucket,count,sum}` (le 0.1,0.25,0.5,1.0,2.0,+Inf; no hostname). Both are emitted.

> ⚠ **NOT present on this stack:** `controller_runtime_*` (argocd does not emit it), `argocd_notifications_*` (not scraped).

> ⚠ **Per-pod correlation:** app-controller is a StatefulSet (pod `argocd-application-controller-0`) — stream label carries `k8s_statefulset_name`; all others have `k8s_deployment_name`. `kube_pod_container_info` containers: `application-controller`, `applicationset-controller`, `server`, `repo-server`, `redis` (the Redis process), `metrics` (redis_exporter sidecar).

Metric families: `argocd_app_info` (G; `autosync_enabled,dest_namespace,dest_server,health_status,name,project,repo,sync_status`), `argocd_app_k8s_request_total` (C), `argocd_app_orphaned_resources_count` (G), `argocd_app_reconcile` (H; le 0.25,0.5,1,2,4,8,16,+Inf; `dest_server` only), `argocd_app_sync_duration_seconds_total` (C), `argocd_app_sync_total` (C; `dry_run,name,phase,project`), `argocd_cluster_{api_resource_objects,api_resources,cache_age_seconds,connection_status,events_total,info}` (G/C), `argocd_git_request_total` / `argocd_git_request_duration_seconds` (C+H; `repo,request_type ∈ {fetch,ls-remote}`; le 0.1,0.25,0.5,1,2,4,10,20,+Inf), `argocd_info` (G; `version=v3.4.3`), `argocd_kubectl_exec_{pending,total}` (G/C), `argocd_kubectl_{rate_limiter,request,request_size_bytes,response_size_bytes}_duration_seconds`/`_bytes` (H; le 0.005,0.1,0.5,2,8,30,+Inf), `argocd_kubectl_{request_retries_total,requests_total,transport_cache_entries,transport_create_calls_total}`, `argocd_redis_request_{duration,total}`, `argocd_redis_request_duration_seconds_*`, `argocd_repo_pending_request_total`, `argocd_resource_events_{processed_in_batch,processing}`. `workqueue_*` family (per controller ∈ {app_hydration_queue,app_operation_processing_queue,app_reconciliation_queue,manifest_hydration_queue,project_reconciliation_queue,applicationset}).

```yaml signals
family: argocd
scope: substrate
sink: promrw
labels:
  cluster: <cluster>
  k8s_cluster_name: <cluster-name>
  namespace: argocd
  # job varies per component — see notes
metrics:
  # app-controller (job=argocd-metrics, port 8082)
  - {root: argocd_app_info, type: gauge, unit: info, v: ok, note: "autosync_enabled,dest_namespace,dest_server,health_status(Healthy|Degraded|Missing|Unknown|Suspended|Progressing),name,project,repo,sync_status(Synced|OutOfSync)"}
  - {root: argocd_app_k8s_request_total, type: counter, unit: requests, v: ok, note: "server,verb ∈ {list,watch,get}"}
  - {root: argocd_app_orphaned_resources_count, type: gauge, unit: count, v: ok, note: "project,name labels"}
  - {root: argocd_app_reconcile, type: histogram, unit: seconds, v: ok, note: "dest_server only; le 0.25,0.5,1,2,4,8,16,+Inf"}
  - {root: argocd_app_sync_duration_seconds_total, type: counter, unit: seconds, v: ok}
  - {root: argocd_app_sync_total, type: counter, unit: count, v: ok, note: "dry_run,name,phase(Succeeded|Failed),project"}
  - {root: argocd_cluster_api_resource_objects, type: gauge, unit: count, v: ok, note: server label}
  - {root: argocd_cluster_api_resources, type: gauge, unit: count, v: ok, note: server label}
  - {root: argocd_cluster_cache_age_seconds, type: gauge, unit: seconds, v: ok, note: server label}
  - {root: argocd_cluster_connection_status, type: gauge, unit: bool, v: ok, note: "server,k8s_version labels"}
  - {root: argocd_cluster_events_total, type: counter, unit: count, v: ok, note: server label}
  - {root: argocd_cluster_info, type: gauge, unit: info, v: ok, note: "k8s_version=v1.35.5,name=in-cluster,server labels"}
  - {root: argocd_kubectl_exec_pending, type: gauge, unit: count, v: ok}
  - {root: argocd_kubectl_exec_total, type: counter, unit: count, v: ok, note: "command ∈ {apply,auth,create,replace},hostname labels"}
  - {root: argocd_kubectl_rate_limiter_duration_seconds, type: histogram, unit: seconds, v: ok, note: "le 0.005,0.1,0.5,2,8,30,+Inf"}
  - {root: argocd_kubectl_request_duration_seconds, type: histogram, unit: seconds, v: ok, note: "host,verb(Create|Delete|Get|List|Patch|Update) labels"}
  - {root: argocd_kubectl_request_retries_total, type: counter, unit: count, v: ok, note: host label}
  - {root: argocd_kubectl_request_size_bytes, type: histogram, unit: bytes, v: ok, note: "host,verb labels"}
  - {root: argocd_kubectl_requests_total, type: counter, unit: requests, v: ok, note: "code(200,201,404,429),host,method labels"}
  - {root: argocd_kubectl_response_size_bytes, type: histogram, unit: bytes, v: ok, note: "host,verb labels"}
  - {root: argocd_kubectl_transport_cache_entries, type: gauge, unit: count, v: ok}
  - {root: argocd_kubectl_transport_create_calls_total, type: counter, unit: count, v: ok, note: "result ∈ {hit,miss}"}
  # redis request (app-controller emitter — has hostname label; no _seconds suffix)
  - {root: argocd_redis_request_duration, type: histogram, unit: seconds, v: ok, note: "app-controller; has hostname label; le 0.01,0.05,0.1,0.25,0.5,1.0,2.0,+Inf"}
  # redis request (repo-server emitter — no hostname; has _seconds suffix)
  - {root: argocd_redis_request_duration_seconds, type: histogram, unit: seconds, v: ok, note: "repo-server; NO hostname; le 0.1,0.25,0.5,1.0,2.0,+Inf"}
  - {root: argocd_redis_request_total, type: counter, unit: requests, v: ok, note: "initiator(argocd-application-controller|argocd-repo-server|argocd-server),failed labels"}
  - {root: argocd_repo_pending_request_total, type: gauge, unit: count, v: ok}
  - {root: argocd_resource_events_processed_in_batch, type: counter, unit: count, v: ok}
  - {root: argocd_resource_events_processing, type: histogram, unit: seconds, v: ok}
  # server (job=argocd-server-metrics, port 8083)
  - {root: argocd_info, type: gauge, unit: info, v: ok, note: "version=v3.4.3"}
  # repo-server (job=argocd-repo-server-metrics, port 8084)
  - {root: argocd_git_request_total, type: counter, unit: requests, v: ok, note: "repo,request_type ∈ {fetch,ls-remote}"}
  - {root: argocd_git_request_duration_seconds, type: histogram, unit: seconds, v: ok, note: "le 0.1,0.25,0.5,1,2,4,10,20,+Inf"}
  # workqueue (all controllers — carries asserts_env label on this stack)
  - {root: workqueue_adds_total, type: counter, unit: count, v: ok, note: "controller/name ∈ {app_hydration_queue,app_operation_processing_queue,app_reconciliation_queue,manifest_hydration_queue,project_reconciliation_queue,applicationset}"}
  - {root: workqueue_depth, type: gauge, unit: count, v: ok}
  - {root: workqueue_longest_running_processor_seconds, type: gauge, unit: seconds, v: ok}
  - {root: workqueue_queue_duration_seconds, type: histogram, unit: seconds, v: ok}
  - {root: workqueue_retries_total, type: counter, unit: count, v: ok}
  - {root: workqueue_unfinished_work_seconds, type: gauge, unit: seconds, v: ok}
  - {root: workqueue_work_duration_seconds, type: histogram, unit: seconds, v: ok}
not_emitted: ["controller_runtime_* (argocd does not emit it)", "argocd_notifications_* (not scraped on this stack)"]
note: "two redis histogram families (version artifact: _duration vs _duration_seconds); app-controller is StatefulSet (pod argocd-application-controller-0); redis_exporter sidecar container='metrics'"
```

---

## Envoy Gateway — TWO surfaces (control plane + data plane, ns `envoy-gateway-system`, port 19001, v1.8.1 — 2026-06-16) [slug: k8s-envoy-gateway]

*Provenance: live a live reference EKS cluster capture 2026-06-16 (svc-group-b.md §3.A). Two scrape jobs, both on :19001.*

> ⚠ **CRITICAL: NO `envoy_gateway_*` prefix on the control plane.** The query `{__name__=~"envoy_gateway_.+"}` is EMPTY in Mimir. The control plane emits `xds_*`, `watchable_*`, `resource_*`, `status_update_*`, `topology_*`, `wasm_*` (plus `controller_runtime_*` / `rest_client_*` / `workqueue_*` / `certwatcher_*`). The `envoy_gateway_*` family is a vendor doc artifact — it does NOT appear in the real scrape.

> ⚠ **Data-plane histograms are MILLISECONDS.** `envoy_http_downstream_rq_time` and `envoy_cluster_upstream_rq_time` le values are in ms (0.5,1,5,10,25,50,100,250,500,1000,2500,5000,10000,30000,60000,300000,600000,1.8e6,3.6e6,+Inf), NOT seconds. Controller-runtime histograms are in seconds.

> ⚠ **Data-plane proxy pods have TWO containers** (`envoy` + `shutdown-manager`) — BOTH scraped on :19001. Some metrics appear twice per pod (once per container) including `envoy_control_plane_connected_state`, `envoy_server_uptime`, `envoy_tracing_opentelemetry_spans_sent`.

> ⚠ **Proxy deployment naming:** `envoy-<gw-ns>-<gw-name>-<hash>` (e.g. `envoy-envoy-gateway-system-tailscale-899d26d2`). The specific hash is deployment-specific — synthkit models this as the `envoy-default-eg-proxy` workload (representative form).

> ⚠ **Data-plane extra node-topology labels:** `architecture,availability_zone,instance_type,nodepool,os,region` — stamped from the node the proxy pod runs on.

**Surface 1 — Control Plane (`job="gateway-helm"`, pod `envoy-gateway-*`, container `envoy-gateway`, 1 pod):**
`xds_snapshot_create_total` (C; `status=success`), `xds_snapshot_update_total` (C), `xds_stream_duration_seconds` (H), `watchable_{depth,event_total,publish_total,subscribe_total}` (G/C; `runner ∈ {gateway-api,infrastructure,xds}`, `event_type=update`), `resource_{apply,delete}_total` (C; `kind ∈ {ConfigMap,Deployment,PDB,Service,ServiceAccount}`, `name,status`), `resource_apply_duration_seconds` (H), `status_update_total` (C), `topology_injector_webhook_events_total` (C), `wasm_cache_entries` (G), `controller_runtime_reconcile_total` (C; `controller=gatewayapi-<hash>,result`), `controller_runtime_reconcile_time_seconds` (H), `controller_runtime_{active_workers,max_concurrent_reconciles}` (G), `controller_runtime_webhook_{requests_total,requests_in_flight}` (C/G), `rest_client_requests_total` (C; `code,host=172.20.0.1:443,method`), `workqueue_{adds_total,depth,longest_running_processor_seconds,retries_total,unfinished_work_seconds}` (C/G), `certwatcher_read_certificate_total` (C), `leader_election_master_status` (G; `name=envoy-gateway-leader`).

**Surface 2 — Data Plane (`job="envoy"`, pods `envoy-*-proxy-*`, container `envoy`, 2 pods per Gateway):**
`envoy_cluster_upstream_rq_total` (C; `envoy_cluster_name ∈ {httproute/<ns>/<name>/rule/<N>,prometheus_stats,tracing,xds_cluster,<ns>/<svc>}`), `envoy_cluster_upstream_rq_time` (H; ms), `envoy_cluster_upstream_{cx_active,cx_total,rq_pending_active,membership_total}` (G/C), `envoy_http_downstream_rq_xx` (C; `envoy_http_conn_manager_prefix,envoy_response_code_class ∈ {"1","2","3","4","5"}`), `envoy_http_downstream_rq_time` (H; ms), `envoy_listener_downstream_cx_active` (G; `envoy_listener_address ∈ {0.0.0.0_10443,0.0.0.0_19001,0.0.0.0_19003}`), `envoy_control_plane_connected_state` (G=1), `envoy_server_{uptime,live,concurrency,days_until_first_cert_expiring,memory_allocated,memory_heap_size}` (G), `envoy_tracing_opentelemetry_spans_sent` (C).

```yaml signals
family: envoy_gateway
scope: substrate
sink: promrw
labels:
  cluster: <cluster>
  k8s_cluster_name: <cluster-name>
  namespace: envoy-gateway-system
  instance: <podIP>:19001    # both surfaces scrape port 19001
metrics:
  # --- Control plane (job=gateway-helm, container=envoy-gateway) ---
  - {root: xds_snapshot_create_total, type: counter, unit: count, v: ok, note: "status=success"}
  - {root: xds_snapshot_update_total, type: counter, unit: count, v: ok}
  - {root: xds_stream_duration_seconds, type: histogram, unit: seconds, v: ok}
  - {root: watchable_depth, type: gauge, unit: count, v: ok, note: "runner ∈ {gateway-api,infrastructure,xds}"}
  - {root: watchable_event_total, type: counter, unit: count, v: ok, note: "event_type=update,runner"}
  - {root: watchable_publish_total, type: counter, unit: count, v: ok}
  - {root: watchable_subscribe_total, type: counter, unit: count, v: ok}
  - {root: resource_apply_total, type: counter, unit: count, v: ok, note: "kind ∈ {ConfigMap,Deployment,PDB,Service,ServiceAccount},name,status"}
  - {root: resource_apply_duration_seconds, type: histogram, unit: seconds, v: ok}
  - {root: resource_delete_total, type: counter, unit: count, v: ok, note: "kind,name labels"}
  - {root: status_update_total, type: counter, unit: count, v: ok}
  - {root: topology_injector_webhook_events_total, type: counter, unit: count, v: ok}
  - {root: wasm_cache_entries, type: gauge, unit: count, v: ok}
  - {root: controller_runtime_reconcile_total, type: counter, unit: count, v: ok, note: "controller=gatewayapi-<hash>,result ∈ {error,requeue,requeue_after,success}"}
  - {root: controller_runtime_reconcile_time_seconds, type: histogram, unit: seconds, v: ok}
  - {root: controller_runtime_active_workers, type: gauge, unit: count, v: ok}
  - {root: controller_runtime_max_concurrent_reconciles, type: gauge, unit: count, v: ok}
  - {root: controller_runtime_webhook_requests_total, type: counter, unit: requests, v: ok}
  - {root: controller_runtime_webhook_requests_in_flight, type: gauge, unit: count, v: ok}
  - {root: rest_client_requests_total, type: counter, unit: requests, v: ok, note: "code,host=172.20.0.1:443,method ∈ {GET,POST,PUT,PATCH}"}
  - {root: workqueue_adds_total, type: counter, unit: count, v: ok}
  - {root: workqueue_depth, type: gauge, unit: count, v: ok}
  - {root: workqueue_longest_running_processor_seconds, type: gauge, unit: seconds, v: ok}
  - {root: workqueue_retries_total, type: counter, unit: count, v: ok}
  - {root: workqueue_unfinished_work_seconds, type: gauge, unit: seconds, v: ok}
  - {root: certwatcher_read_certificate_total, type: counter, unit: count, v: ok}
  - {root: leader_election_master_status, type: gauge, unit: bool, v: ok, note: "name=envoy-gateway-leader"}
  # --- Data plane (job=envoy, container=envoy, 2 pods per Gateway) ---
  - {root: envoy_cluster_upstream_rq_total, type: counter, unit: requests, v: ok, note: "envoy_cluster_name ∈ {httproute/<ns>/<name>/rule/<N>,prometheus_stats,tracing,xds_cluster,<ns>/<svc>}"}
  - {root: envoy_cluster_upstream_rq_time, type: histogram, unit: milliseconds, v: ok, note: "MILLISECONDS le 0.5,1,5,10,25,50,100,250,500,1000,2500,5000,10000,30000,60000,300000,600000,1.8e6,3.6e6,+Inf"}
  - {root: envoy_cluster_upstream_cx_active, type: gauge, unit: count, v: ok}
  - {root: envoy_cluster_upstream_cx_total, type: counter, unit: count, v: ok}
  - {root: envoy_cluster_upstream_rq_pending_active, type: gauge, unit: count, v: ok}
  - {root: envoy_cluster_membership_total, type: gauge, unit: count, v: ok}
  - {root: envoy_http_downstream_rq_xx, type: counter, unit: requests, v: ok, note: "envoy_http_conn_manager_prefix,envoy_response_code_class ∈ {\"1\",\"2\",\"3\",\"4\",\"5\"} — STRING values"}
  - {root: envoy_http_downstream_rq_time, type: histogram, unit: milliseconds, v: ok, note: "MILLISECONDS — same bucket set as upstream_rq_time"}
  - {root: envoy_listener_downstream_cx_active, type: gauge, unit: count, v: ok, note: "envoy_listener_address ∈ {0.0.0.0_10443,0.0.0.0_19001,0.0.0.0_19003}"}
  - {root: envoy_control_plane_connected_state, type: gauge, unit: bool, v: ok, note: "=1; emitted by BOTH containers (envoy + shutdown-manager)"}
  - {root: envoy_server_uptime, type: gauge, unit: seconds, v: ok}
  - {root: envoy_server_live, type: gauge, unit: bool, v: ok, note: "=1"}
  - {root: envoy_server_concurrency, type: gauge, unit: count, v: ok}
  - {root: envoy_server_days_until_first_cert_expiring, type: gauge, unit: days, v: ok}
  - {root: envoy_server_memory_allocated, type: gauge, unit: bytes, v: ok}
  - {root: envoy_server_memory_heap_size, type: gauge, unit: bytes, v: ok}
  - {root: envoy_tracing_opentelemetry_spans_sent, type: counter, unit: count, v: ok, note: "emitted by BOTH containers"}
not_emitted: ["envoy_gateway_* (prefix is EMPTY in Mimir — NO such control-plane family; vendor-doc artifact)"]
note: "data-plane: job=envoy; extra topology labels: architecture,availability_zone,instance_type,nodepool,os,region; _time histograms in MILLISECONDS; TWO containers per data-plane pod (envoy+shutdown-manager)"
```

---

## Cert-manager — updated per-pod correlation [slug: k8s-cert-manager]

(Already documented above — see the existing section. Additional per-pod correlation details added 2026-06-16.)

> ✅ **Per-pod correlation labels now emitted (2026-06-16, svc-cert-manager.md):** All cert-manager metric families carry `pod`, `namespace`, `container`, `instance` (podIP:port), and `node` (via kube_pod_info join). This is the standard per-pod stamp from `k8saddon.StampPods/StampPodsContainer`. Container names per scrape job: controller=`cert-manager-controller`, cainjector=`cert-manager-cainjector`, webhook=`cert-manager-webhook`. `kube_pod_container_info.container` matches these values exactly.

---

## Cluster Autoscaler — UNVERIFIED on EKS-Karpenter stacks [slug: k8s-cluster-autoscaler]

(Already documented above. See cantfind.md SK-72 — the `job` label is Ⓐ assumed; the entire `cluster_autoscaler_*` family has NEVER been live-captured on the reference cluster because EKS+Karpenter does not run cluster-autoscaler.)
