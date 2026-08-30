---
id: SKT-0044
title: 'Blueprint option: emit only what the k8s-monitoring default allow-lists keep'
status: To Do
assignee: []
created_date: '2026-08-30 13:22'
labels: []
dependencies: []
ordinal: 135000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The multi-cloud capture lab now runs with useDefaultAllowList FALSE everywhere, because the defaults filter each exporter to a small fraction of what it really emits and a corpus built from that would tell synthkit to stop emitting families every real exporter does emit. Full surface is the right default for the corpus.

But the filtered view is ALSO a real customer reality — arguably the more common one, since it is what anyone installing the k8s-monitoring chart with defaults gets. A blueprint should be able to select it.

THE SOURCE IS THE CHART, NOT A CLUSTER. The chart ships the allow-lists as plain YAML data files, one per exporter, so the allowed set can be read directly and version-pinned. Do NOT derive this by inspecting a live cluster: a cluster only shows what that estate happened to emit, which conflates the allow-list with the estate.

  charts/feature-cluster-metrics/default-allow-lists/
    cadvisor.yaml               20 entries
    kube-state-metrics.yaml     44
    kubelet.yaml                37
    kubelet_probes.yaml          2
    kubelet_resource.yaml        3
    opencost.yaml               26
    kepler.yaml                  2
    windows-exporter.yaml        6
  charts/feature-host-metrics/default-allow-lists/
    node-exporter.yaml          11
    node-exporter-integration.yaml  156

Counts are from chart 4.5.0 and will move between versions, which is the point: the allow-list is a property OF A CHART VERSION and must be stored with that version recorded, not copied once into Go source and left to rot.

Note there are TWO node-exporter lists, useDefaultAllowList (11) and useIntegrationAllowList (156), and they are separate switches. A blueprint asking for "the default" has to say which.

Design questions worth settling before building:
  - is this a blueprint-level filter applied at emit time, or a post-emit projection used only by the fidelity comparator?
  - a filter that drops a metric entirely versus one that keeps the family and drops series - the chart drops whole metric names, so match that
  - how the chart version is pinned and refreshed, and what happens when a name in a stored list no longer exists upstream
  - whether the comparator should treat a family absent because of an allow-list differently from one absent because nothing emits it, which it must, or every allow-listed run looks like a coverage gap
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The allowed metric names are read from the chart own default-allow-lists files, with the chart version recorded alongside them
- [ ] #2 No allow-list content is derived from live cluster inspection
- [ ] #3 A blueprint can select the default allow-list, and the two node-exporter variants are distinguishable
- [ ] #4 The comparator tells a family absent by allow-list apart from a family nothing emits
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
