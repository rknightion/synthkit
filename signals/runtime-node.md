# Node.js runtime profile — scraped Prometheus metrics

## Node.js `prom-client` default metrics [slug: runtime-node]

These rows describe `internal/telemetryspec/profiles/runtime_node.go`, used by application workload profile declarations. This is not the older `node_*` vocabulary in `signals/portkey.md`. Instrument semantics are sourced from siimon/prom-client v15.1.3 [`eventLoopLag.js`](https://github.com/siimon/prom-client/blob/v15.1.3/lib/metrics/eventLoopLag.js) and [`heapSizeAndUsed.js`](https://github.com/siimon/prom-client/blob/v15.1.3/lib/metrics/heapSizeAndUsed.js). The default collectors create scalar gauges with no metric-specific labels.

```yaml signals
family: runtime_node
scope: blueprint
sink: promrw
labels:
  job: <declared-scrape-job>
  instance: <target>
metrics:
  - {root: nodejs_eventloop_lag_seconds, type: gauge, unit: seconds, v: ok, note: "default prom-client event-loop lag; no metric-specific labels"}
  - {root: nodejs_heap_size_used_bytes, type: gauge, unit: bytes, v: ok, note: "default prom-client V8 heap used; no metric-specific labels"}
```
