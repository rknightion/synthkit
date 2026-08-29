---
id: SKT-0024
title: >-
  CloudWatch stamps TargetGroup on load-balancer metrics and tag_VpcId on info
  series
status: To Do
assignee: []
created_date: '2026-08-29 09:22'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 115000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
28 of the 67 contradictions are in `signals/cw.md` on substrate `eks` from `gcx_live_readback`, from two distinct CloudWatch labelling defects in `internal/construct/cwinfra/cwinfra.go`.

**1. `dimension_TargetGroup` on load-balancer-level metrics — 20 findings.** synthkit stamps it at `cwinfra.go:327` on ApplicationELB families that real CloudWatch scopes to the load balancer, not to a target group. Verified on `aws_applicationelb_active_connection_count_average` and `_maximum` and eighteen more, each `only-in-synth=[dimension_TargetGroup]` against `reality=[account_id, dimension_AvailabilityZone, dimension_LoadBalancer, job, name, namespace, region]`. The CloudWatch rule is that a metric's dimensions are fixed by the metric, not by the resource: a LoadBalancer-scoped metric carries LoadBalancer and AvailabilityZone, and only the per-target-group families carry TargetGroup. `internal/construct/cwinfra/cwinfra_test.go:401` and `:411` assert the current wrong set, so those expectations are part of the fix.

**2. `tag_VpcId` on `*_info` families — 8 findings.** synthkit stamps it at `cwinfra.go:246` and `:261`, and reality carries `scrape_job` instead. Verified on `aws_applicationelb_info` and `aws_ebs_info`. `internal/fixture/fixture.go:33` documents `VpcID` as "tag_VpcId on *_info series", so the fixture comment is part of the defect and must not survive the fix. `aws_ebs_info` additionally carries `dimension_VolumeId` in synth and not in reality.

Both are shape defects rather than missing coverage: the label exists and is wrong, which is worse than absent because a dashboard grouping by it silently returns nothing against production.

Establish which families really carry TargetGroup from `signals/cw.md` or current AWS documentation before removing it anywhere. Do not delete the label from the families that legitimately have it in order to make the count go to zero.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 dimension_TargetGroup is carried only by the families CloudWatch actually scopes to a target group, established from signals/cw.md or current AWS docs
- [ ] #2 The cwinfra tests assert the corrected dimension sets rather than the current ones
- [ ] #3 tag_VpcId is no longer stamped on _info families, and scrape_job is emitted as reality carries it
- [ ] #4 The fixture.go comment describing VpcID as tag_VpcId on _info series is corrected, not left contradicting the code
- [ ] #5 The dimension_VolumeId divergence on aws_ebs_info is resolved or recorded with a reason
- [ ] #6 signals/cw.md records both corrections with provenance
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->
