// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"encoding/json"
	"testing"

	"github.com/rknightion/synthkit/internal/sink/promrw"
)

func TestMatchedProducerShapeDoesNotInheritOtherJobs(t *testing.T) {
	synth := New()
	addPromSeries(&synth, promrw.Series{Name: "shared", Producer: "promrw/one", Labels: map[string]string{"job": "one", "common": "yes"}})
	addPromSeries(&synth, promrw.Series{Name: "shared", Producer: "promrw/two", Labels: map[string]string{"job": "two", "unrelated": "yes"}, Kind: promrw.KindCounter})
	// Serialization is the actual dump-to-comparator seam.
	data, err := json.Marshal(synth)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &synth); err != nil {
		t.Fatal(err)
	}
	reality := validCorpusDocument("k8s", "capture", "k3s")
	reality.Inventory.AddMetric("shared", TransportPrometheusRW2, InstrumentGauge, map[string]string{"common": "yes"}, nil)
	reality.Inventory.AddMetricProducer("shared", Producer{Name: "promrw/one"})
	for _, f := range CompareCorpus(synth, []CorpusDocument{reality}) {
		if f.Finding.Disposition == DispositionContradiction {
			t.Fatalf("unrelated producer leaked: %+v", f.Finding)
		}
	}
	reality.Inventory.Metrics[0].InstrumentTypes = []string{InstrumentCounter}
	found := false
	for _, f := range CompareCorpus(synth, []CorpusDocument{reality}) {
		if f.Finding.Disposition == DispositionContradiction {
			found = true
		}
	}
	if !found {
		t.Fatal("same-producer instrument contradiction disappeared")
	}
}

func TestDocumentedConditionalKeysRemainCoverage(t *testing.T) {
	for _, tc := range []struct{ name, key string }{
		{"kube_node_labels", "label_karpenter_sh_nodepool"},
		{"node_os_info", "variant_id"},
		{"kube_job_owner", "owner_kind"},
		{"azure_microsoft_network_virtualnetworks_subnets_count", "tag_app"},
		{"azure_microsoft_network_virtualnetworks_subnets_count", "env"},
	} {
		synth, reality := New(), New()
		synth.AddMetric(tc.name, "", InstrumentGauge, map[string]string{tc.key: "present", "invented_key": "bad"}, nil)
		reality.AddMetric(tc.name, "", InstrumentGauge, nil, nil)
		findings := Diff(synth, reality)
		gap, bad := false, false
		for _, f := range findings {
			if f.Disposition == DispositionCoverageGap {
				gap = true
			}
			if f.Disposition == DispositionContradiction {
				bad = true
				for _, v := range f.SynthValues {
					if v == tc.key {
						t.Fatalf("conditional key treated as impossible: %+v", f)
					}
				}
			}
		}
		if !gap || !bad {
			t.Fatalf("%s: conditional gap=%v invalid-key contradiction=%v", tc.name, gap, bad)
		}
	}
}
