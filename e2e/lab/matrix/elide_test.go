// SPDX-License-Identifier: AGPL-3.0-only

package matrix

import (
	"testing"

	"github.com/rknightion/synthkit/internal/inventory"
)

func labelValues(t *testing.T, schema inventory.Schema, metric, key string) ([]string, bool) {
	t.Helper()
	for _, candidate := range schema.Metrics {
		if candidate.Name != metric {
			continue
		}
		for _, label := range candidate.Labels {
			if label.Key == key {
				return label.Values, label.ValuesElided
			}
		}
		t.Fatalf("metric %q has no label %q", metric, key)
	}
	t.Fatalf("no metric %q", metric)
	return nil, false
}

func buildInfoFixture() inventory.Schema {
	schema := inventory.New()
	schema.Metrics = []inventory.Metric{
		{
			Name:       "kubernetes_build_info",
			Transports: []string{inventory.TransportPrometheusRW1},
			Labels: []inventory.Attribute{
				{Key: "build_date", Values: []string{"2026-05-20T11:15:10Z"}},
				{Key: "compiler", Values: []string{"gc"}},
				{Key: "git_commit", Values: []string{"6a4781ad53ee5cad273bedcd9462ae36ac97d798"}},
				{Key: "git_tree_state", Values: []string{"clean"}},
				{Key: "git_version", Values: []string{"v1.35.5+k3s1"}},
				{Key: "go_version", Values: []string{"go1.25.9"}},
				{Key: "job", Values: []string{"integrations/kubernetes/kubelet"}},
				{Key: "major", Values: []string{"1"}},
				{Key: "minor", Values: []string{"35"}},
				{Key: "platform", Values: []string{"linux/arm64"}},
				{Key: "source", Values: []string{"kubernetes"}},
			},
		},
		{
			Name:       "coredns_build_info",
			Transports: []string{inventory.TransportPrometheusRW1},
			Labels: []inventory.Attribute{
				{Key: "goversion", Values: []string{"go1.26.2"}},
				{Key: "namespace", Values: []string{"kube-system"}},
				{Key: "revision", Values: []string{"17fceec"}},
				{Key: "version", Values: []string{"1.14.3"}},
			},
		},
		{
			// A non-build_info family whose label happens to share a key name. The rule is
			// scoped to build-identity families, so this must be untouched.
			Name:       "kube_node_info",
			Transports: []string{inventory.TransportPrometheusRW1},
			Labels: []inventory.Attribute{
				{Key: "version", Values: []string{"v1.35.5+k3s1"}},
				{Key: "node", Values: []string{"lab-server-0"}},
			},
		},
	}
	return schema
}

func TestElideCoversTheFiveKubernetesBuildInfoIdentityLabels(t *testing.T) {
	schema := buildInfoFixture()
	ElideCaptureInstanceIdentity(&schema)

	// Exactly the five labels that produced unactionable contradictions, plus `major`, which
	// is the same kind of value and differs from them only by currently agreeing.
	for _, key := range []string{"build_date", "git_commit", "git_version", "go_version", "major", "minor"} {
		values, elided := labelValues(t, schema, "kubernetes_build_info", key)
		if !elided {
			t.Errorf("kubernetes_build_info label %q: want values_elided=true", key)
		}
		if len(values) != 0 {
			t.Errorf("kubernetes_build_info label %q: want no values, got %v", key, values)
		}
	}
}

func TestElideRetainsRealValueSpaceEvidence(t *testing.T) {
	schema := buildInfoFixture()
	ElideCaptureInstanceIdentity(&schema)

	// compiler and git_tree_state are fixed by the Kubernetes release process, platform names
	// a deployment dimension, job and source are collector-egress contract values. All four
	// are real value-space evidence and must survive.
	for key, want := range map[string]string{
		"compiler":       "gc",
		"git_tree_state": "clean",
		"platform":       "linux/arm64",
		"job":            "integrations/kubernetes/kubelet",
		"source":         "kubernetes",
	} {
		values, elided := labelValues(t, schema, "kubernetes_build_info", key)
		if elided {
			t.Errorf("kubernetes_build_info label %q: must not be elided", key)
		}
		if len(values) != 1 || values[0] != want {
			t.Errorf("kubernetes_build_info label %q: got %v, want [%s]", key, values, want)
		}
	}
}

func TestElideAppliesToEveryBuildInfoFamilyAndNoOther(t *testing.T) {
	schema := buildInfoFixture()
	ElideCaptureInstanceIdentity(&schema)

	for _, key := range []string{"goversion", "revision", "version"} {
		if _, elided := labelValues(t, schema, "coredns_build_info", key); !elided {
			t.Errorf("coredns_build_info label %q: want values_elided=true", key)
		}
	}
	if _, elided := labelValues(t, schema, "coredns_build_info", "namespace"); elided {
		t.Error("coredns_build_info label \"namespace\": must not be elided")
	}
	values, elided := labelValues(t, schema, "kube_node_info", "version")
	if elided || len(values) != 1 {
		t.Errorf("kube_node_info is not a build-identity family; label \"version\" must be untouched, got %v elided=%v", values, elided)
	}
}

func TestIsCaptureInstanceIdentityScope(t *testing.T) {
	cases := []struct {
		metric string
		label  string
		want   bool
	}{
		{"kubernetes_build_info", "git_version", true},
		{"kubernetes_build_info", "platform", false},
		{"alloy_build_info", "revision", true},
		{"kube_pod_info", "git_version", false},
		{"build_info", "version", false},
	}
	for _, tc := range cases {
		if got := IsCaptureInstanceIdentity(tc.metric, tc.label); got != tc.want {
			t.Errorf("IsCaptureInstanceIdentity(%q, %q) = %v, want %v", tc.metric, tc.label, got, tc.want)
		}
	}
}

func TestElideIsStickyAndIdempotent(t *testing.T) {
	schema := buildInfoFixture()
	first := ElideCaptureInstanceIdentity(&schema)
	if first == 0 {
		t.Fatal("first pass elided nothing")
	}
	if second := ElideCaptureInstanceIdentity(&schema); second != 0 {
		t.Errorf("second pass elided %d more labels; the rule is not idempotent", second)
	}
	if _, elided := labelValues(t, schema, "kubernetes_build_info", "minor"); !elided {
		t.Error("elision must stay sticky across passes")
	}
}

func TestNormalizeCandidateStampsProvenanceAndElides(t *testing.T) {
	raw := buildInfoFixture()
	candidate := NormalizeCandidate(raw, inventory.Provenance{
		Substrate:    "k3s",
		ChartVersion: "4.4.0",
		CapturedAt:   "2026-08-27T00:00:00Z",
	})
	if candidate.Provenance == nil || candidate.Provenance.Substrate != "k3s" {
		t.Fatalf("provenance not stamped: %+v", candidate.Provenance)
	}
	if candidate.SchemaVersion != inventory.SchemaVersion {
		t.Errorf("schema_version = %q", candidate.SchemaVersion)
	}
	if _, elided := labelValues(t, candidate, "kubernetes_build_info", "go_version"); !elided {
		t.Error("normalize must apply the capture-instance identity rule")
	}
}

// The measured, rejected alternative: declaring a substrate on the SYNTH export would also
// suppress the five kubernetes_build_info contradictions, but the substrate filter is
// document-level and drops both k3d documents whole, taking 31 real findings with it. The k3d
// producer must therefore reach the same outcome by eliding, never by declaring a substrate.
func TestNormalizeCandidateNeverDeclaresASubstrateOnTheSynthSide(t *testing.T) {
	candidate := NormalizeCandidate(buildInfoFixture(), inventory.Provenance{
		Substrate:  "k3s",
		CapturedAt: "2026-08-27T00:00:00Z",
	})
	// The producer stamps the substrate it actually captured on. That is a reality-side
	// provenance claim, and it is the only substrate this package ever writes.
	if candidate.Provenance.Substrate != "k3s" {
		t.Fatalf("substrate = %q, want the captured substrate", candidate.Provenance.Substrate)
	}
	if len(candidate.Provenance.SelectorLabels) != 0 {
		t.Error("the k3d producer must not declare selector labels; that is a synth-side field")
	}
}
