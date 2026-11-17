---
id: doc-0005
title: Causal migration plan for shipped blueprints
type: specification
created_date: '2026-10-25T02:46:00Z'
updated_date: '2026-10-25T02:46:00Z'
---
Sweep: 36 of 36 blueprints read; 14 scenarios (35 effects, all in acme-ai-platform) and 46 incident rows across 7 blueprints. No shipped blueprint declares lambda, sqs, dynamodb or solace. Only acme-ai-platform has request-path faults.

Parity: `dependency` slow_factor 4 reproduces latency_storm (x(1+3i)); `rule` reject_share 0.5 with error.status 500 approximates error_spike (0.01+0.99*0.5*i).

| Blueprint | Scenario / rows | Declared cause | Family |
|---|---|---|---|
| acme-ai-platform | backend_latency_storm | dependency_slow on content-gen self dependency (slow_factor 4, i 0.8); worker concurrency resource sized by Little's law | F1 |
| acme-ai-platform | assist_outage | rule_regression (status 500) on content-gen rule + dependency_slow 0.5 | F1 |
| acme-ai-platform | bad_deploy_major | request half as assist_outage; cpu_hotspot, memory_leak, pod_crashloop stay legacy | F1 + keep |
| acme-ai-platform | ambient latency_storm on content-gen (20m/2m, 0.12) | dependency_slow 0.12 all envs | F1 |
| acme-ai-platform | knowledge_retrieval_meltdown | dependency_slow on documentdb-store + rule_regression on live-assistant; docdb axis effects stay legacy | F2 |
| acme-ai-platform | db_collab_saturation | dependency_slow on aurora-pg with collab-editor-cs connection pool; DB axis effects stay legacy | F2 |
| acme-ai-platform | 7 ambient DB rows | keep legacy, optional paired dependency_slow | F2 optional |
| acme-ai-platform | ai_gateway_brownout, agentcore_capacity_constraint | concurrency_reduced on an AgentCore runtime concurrency resource + call_policies retries; agentcore_throttle and fallback_storm stay legacy | F3 (partial) |
| acme-ai-platform | ai_quality_regression, portkey_gateway_degraded, frontend_vitals_degraded | keep legacy (quality, poller, RUM); retry halves follow F3 | F5 |
| acme-ai-platform | eks_* scenarios and 20 ambient cluster rows | keep legacy (cluster axis) | F4 |
| acme-ai-platform-eval, acme-ai-eval | cluster rows | keep legacy | F4 |
| grafana-ai-o11y | provider_call_error, eval_quality_regression | keep legacy (ai_agent out of v1) | F5 |
| netobs-global, netobs-enterprise, netobs-spoke | nettopo_* rows | keep legacy (integration construct) | F5 |
| 29 others | none | no migration | - |

Order: F1, F2, F3 (partial). All three edit acme-ai-platform.yaml (single owner): sequential.

Gaps (v1): G1 no bad-deploy cause (ruled: rule kind with status 500 override, D19); G2 gen_ai work is not a call edge, so call_policies cannot reach Portkey/AgentCore hops; G3 no fallback mechanic; G4 rds, docdb, agentcore unbound (follow-up task); G5 cluster faults do not reduce capacity; G6 base-name targets across 7 envs (ruled D19); G7 traffic shape diurnal vs load business parity to assert in L4a.

Rates: content-gen in-flight ~30-40 at peak per env, so worker concurrency >= 64 keeps healthy utilization under the knee; DB pools do not saturate at these rates (dependency_slow raises latency, not exhaustion); design story 5 sketch (size 40, hold 12 ms, 60 rps) gives utilization 0.108 at x6, so its numbers do not exhaust (fixture must size it). Never raise shipped rates; F1-F3 re-source existing intensities with comments.
