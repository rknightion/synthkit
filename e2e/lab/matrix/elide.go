// SPDX-License-Identifier: AGPL-3.0-only

package matrix

import (
	"slices"
	"strings"

	"github.com/rknightion/synthkit/internal/inventory"
)

// The capture-instance identity rule (SKT-0010.12).
//
// A capture observes ONE deployment at ONE moment. For most labels that is still evidence
// about a value space: `job` takes its value from a fixed set of collector job names, and
// seeing one of them is a real observation about the contract. For a small class of labels it
// is not. A label that carries the build identity of the software instance the lab happened to
// start takes a value that:
//
//  1. differs between any two deployments of the same software, on any substrate, and
//  2. changes again on the next patch release of that same software.
//
// Recording the single observed value for such a label asserts the corpus knows the whole
// value space. It does not. `values_elided: true` is the corpus encoding for exactly that
// claim — the KEY is real collector-egress evidence and still compares, the value space is
// declared unenumerable from one capture — and the gcx read-back producer already applies it
// to these labels of this metric. Only the k3d lab did not, which left five contradictions on
// kubernetes_build_info that no maintainer can act on: a k3s patch release or a Go toolchain
// bump would break a gate with teeth for a reason that is not drift.
//
// The rule, stated so a future producer applies it consistently:
//
//	On a Prometheus build-identity family (the `*_build_info` convention), the version tuple
//	and build provenance labels carry capture-instance identity. Record the key, elide the
//	values.
//
// What deliberately stays: labels whose value set is fixed by the software's RELEASE PROCESS
// rather than by the instance (`compiler` is `gc` for every Kubernetes build ever shipped,
// `git_tree_state` is `clean` for every release build), and labels that name a DIMENSION of
// the deployment rather than its build (`platform`, `job`, `namespace`, `service`, `source`).
// Those are real value-space evidence and eliding them would cost the corpus something.
//
// This is deliberately NOT the way the same five findings could be suppressed by declaring
// `provenance.substrate = "eks"` on the synth export. That alternative was measured and
// rejected: the substrate filter is document-level, so it drops both k3d documents whole and
// removes 38 contradictions of which 31 are real. Do not reopen it.
var captureInstanceIdentityLabels = []string{
	"build_date",
	"git_commit",
	"git_version",
	"go_version",
	"goversion",
	"major",
	"minor",
	"revision",
	"version",
}

// buildInfoSuffix is the Prometheus convention for a build-identity info family. The rule is
// scoped by it rather than by an explicit metric list so a newly captured addon's build_info
// family is covered the first time it appears, instead of adding a fresh unactionable
// contradiction that someone has to notice.
const buildInfoSuffix = "_build_info"

// IsCaptureInstanceIdentity reports whether metric/label carries capture-instance identity
// under the rule above.
func IsCaptureInstanceIdentity(metric, label string) bool {
	if !strings.HasSuffix(metric, buildInfoSuffix) {
		return false
	}
	return slices.Contains(captureInstanceIdentityLabels, label)
}

// ElideCaptureInstanceIdentity applies the rule in place. Eliding is sticky: a label already
// marked elided for another reason (an over-limit value set, a previous producer run) stays
// elided.
func ElideCaptureInstanceIdentity(schema *inventory.Schema) int {
	elided := 0
	for metricIndex := range schema.Metrics {
		metric := &schema.Metrics[metricIndex]
		for labelIndex := range metric.Labels {
			label := &metric.Labels[labelIndex]
			if !IsCaptureInstanceIdentity(metric.Name, label.Key) {
				continue
			}
			if !label.ValuesElided || len(label.Values) > 0 {
				elided++
			}
			label.Values = []string{}
			label.ValuesElided = true
		}
	}
	return elided
}

// NormalizeCandidate turns a raw receiver inventory into the corpus candidate the k3d lab
// publishes: provenance stamped, capture-instance identity elided, everything sorted and
// deduplicated so two runs of the same permutation produce byte-identical output.
func NormalizeCandidate(raw inventory.Schema, provenance inventory.Provenance) inventory.Schema {
	out := raw
	out.SchemaVersion = inventory.SchemaVersion
	stamped := provenance
	out.Provenance = &stamped
	if out.Metrics == nil {
		out.Metrics = []inventory.Metric{}
	}
	if out.Logs == nil {
		out.Logs = []inventory.Log{}
	}
	if out.Traces == nil {
		out.Traces = []inventory.Trace{}
	}
	if out.Profiles == nil {
		out.Profiles = []inventory.Profile{}
	}
	if out.Sigil == nil {
		out.Sigil = []inventory.Sigil{}
	}
	if out.Receipts == nil {
		out.Receipts = []inventory.Receipt{}
	}
	ElideCaptureInstanceIdentity(&out)
	out.Normalize()
	return out
}
