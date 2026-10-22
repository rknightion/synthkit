---
id: SKT-0167
title: Stop the k8s_profiling construct emitting full-load profiles at volume 0
status: Done
assignee: []
created_date: '2026-10-22T05:17:00Z'
updated_date: '2026-10-22T06:05:00Z'
labels:
  - control
  - profiling
dependencies: []
priority: medium
ordinal: 311000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Split from SKT-0147 at the loop52 convergence tripwire (rv147b major). internal/construct/k8sprofiling/k8sprofiling.go:98 passes BusinessFactor(now) (0 at a pause) into pyroscope.Load; internal/pyroscope/flame.go:346 maps factor<=0 to 1.0, so paused clusters push full-load eBPF/Go/JVM profiles for every pod (gate-2 probe summed samples: 1800 at 0, 18 at 0.01, 900 at 0.5, 1800 at 1). Shipped profiling-demo.yaml and k8s-full-stack.yaml enable it. Fix in the construct (constructs may import shape): return no profiles when w.Shape.Paused(); leave the shared fallback. Also trim the 28 s TestSpecMetricsVolumeZeroEmitNoNominalLoad positive legs (gateway_native_scrape at volumes 1 and 2) that tripled internal/workload/app test time.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 At volume 0 k8s_profiling emits no profiles; red at base, green after
- [x] #2 Nonzero output byte-identical (compare proto.Marshal Deterministic, never Profile.String())
- [x] #3 docs/control-plane.md drops the k8s_profiling carve-out
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop52 BATCH10: landed da3176e. Red at base (30 profile series at 0), green; nonzero byte-identical (proto.Marshal Deterministic); app package race 332s -> 213s; CodeRabbit 4 files 0; gate gaterun 20261022T055554Z-synthkit-78082632 exit 0 on b92db0a.
<!-- SECTION:NOTES:END -->
