---
id: SKT-0027
title: >-
  The only-in-synth value rule is too blunt: deployment-specific values are
  absent evidence
status: In Progress
assignee: []
created_date: '2026-08-29 16:49'
updated_date: '2026-08-29 17:59'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 118000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The gate flip landed with 13 exemption rules covering 256 contradiction findings, and the mechanism is sound: exact set equality on the divergent values, an `expected_matches` drift guard, no disposition change, and every exempted finding still printed with its id and reason. Verified 2026-08-29 by dropping a rule (fails), emptying the document (fails with 256), corrupting a count (fails), and injecting an emitter regression (fails, naming the signal and the invented label).

**But 243 of the 256 matches come from two rules that say the same thing**: `capture-cw-region-five` and `capture-cw-region-four` exempt `labels.region` on `aws_*` because the EKS readback observes `eu-west-1` while synthkit models six configured CloudWatch regions.

That is not a contradiction. A region the capture never observed is **absent evidence**, and SKT-0010.01 already decided absent evidence is a gap. The evidence does not refute `region=us-east-1`; it is silent about it. The same argument covers `capture-k8s-node-architecture`, `capture-k8s-node-os`, `capture-k8s-node-regions`, `capture-k8s-node-zones`, `capture-coredns-protocol`, `capture-coredns-request-types` and `capture-coredns-proxy-rcode` — nine of thirteen rules, and 251 of 256 matches.

The rule this wave implemented came from a decision recorded on SKT-0025: "a value synthkit emits that reality never shows stays a contradiction — that direction is a claim the evidence refutes." That is right for a value that is structurally impossible, and wrong for a value that is merely deployment-specific. A capture enumerates one deployment; it cannot enumerate the valid value set of `region`, `label_kubernetes_io_os`, or `proto`.

So the exemption file is compensating for a rule that is too blunt, and the exemption count is the symptom rather than the problem. **The decision was mine and the wave implemented it faithfully — this is a correction to the rule, not to the work.**

What is needed is a distinction the comparator can make: a label whose value set is closed and enumerable versus one that is deployment-specific and open. An open-valued label's only-in-synth values are a gap; a closed-valued label's are a contradiction. Where the corpus cannot tell, absent evidence should win, because that is the standing rule everywhere else.

This interacts directly with SKT-0020.05: once AKS and GKE captures exist, `region` and `label_kubernetes_io_os` will legitimately differ per substrate, and a rule that treats a value one cloud never showed as a contradiction will produce a wall of them.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A label whose value set is deployment-specific reports its only-in-synth values as a coverage gap, not a contradiction
- [ ] #2 A label whose value set is closed and enumerable still reports an impossible synth value as a contradiction
- [ ] #3 Where the corpus cannot distinguish the two, absent evidence wins, consistent with SKT-0010.01
- [ ] #4 The exemption rules the change makes redundant are deleted, not left matching zero findings
- [ ] #5 The remaining exemption count reflects genuine reviewed differences rather than capture narrowness
- [ ] #6 docs/reality-corpus.md states the open-versus-closed value-set distinction and how a label is classified
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Classify only-in-synth label values using explicit closed-value evidence; default unknown/open sets to absent-evidence gaps, delete the nine now-redundant exemption rules, document semantics, and prove both open and structurally impossible cases.
<!-- SECTION:PLAN:END -->
