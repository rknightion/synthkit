// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestProducerIdentityOutcomes(t *testing.T) {
	for _, name := range []string{"pg_stat_activity_count", "grafana_kubernetes_monitoring_build_info"} {
		t.Run(name, func(t *testing.T) {
			synth := New()
			key := "env"
			if name == "grafana_kubernetes_monitoring_build_info" {
				key = "source"
			}
			synth.AddMetric(name, TransportPrometheusRW2, InstrumentCounter, map[string]string{key: "synthetic"}, nil)
			synth.AddMetricProducer(name, Producer{Name: "modeled"})
			reality := validCorpusDocument("k8s", "capture", "k3s")
			reality.Inventory.AddMetric(name, TransportPrometheusRW2, InstrumentGauge, map[string]string{"observed": ""}, nil)
			reality.Inventory.AddMetricProducer(name, Producer{Name: "modeled"})
			want := Diff(synth, reality.Inventory)
			got := CompareCorpus(synth, []CorpusDocument{reality})
			var plain []Finding
			for _, f := range got {
				plain = append(plain, f.Finding)
			}
			sortFindings(plain)
			if !reflect.DeepEqual(plain, want) {
				t.Fatalf("same producer changed comparison: got=%+v want=%+v", plain, want)
			}
			reality.Inventory.Metrics[0].Producers = []Producer{{Name: "observed"}}
			got = CompareCorpus(synth, []CorpusDocument{reality})
			if len(got) != 2 {
				t.Fatalf("different producer must retain gap and unmatched claim: %+v", got)
			}
			var gap, unmatched bool
			for _, f := range got {
				gap = gap || f.Finding.Kind == KindProducerMismatch && f.Finding.Disposition == DispositionCoverageGap
				unmatched = unmatched || f.Finding.Kind == KindNoComparableProducer && f.Finding.Disposition == DispositionNoComparableProducer
				if !reflect.DeepEqual(f.Finding.SynthValues, []string{"modeled"}) || !reflect.DeepEqual(f.Finding.RealityValues, []string{"observed"}) {
					t.Fatalf("producer identities lost: %+v", f)
				}
			}
			if !gap || !unmatched {
				t.Fatalf("missing outcome: %+v", got)
			}
			var report bytes.Buffer
			if err := WriteFindingsReport(&report, got); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(report.String(), "## No comparable producer") {
				t.Fatal(report.String())
			}
		})
	}
}

func TestProducerIdentityComparableElsewhereAndPartialOverlap(t *testing.T) {
	synth := New()
	synth.AddMetric("shared_total", TransportPrometheusRW2, InstrumentCounter, map[string]string{"invented": ""}, nil)
	synth.AddMetricProducer("shared_total", Producer{Name: "one"})
	synth.AddMetricProducer("shared_total", Producer{Name: "two"})
	reality := validCorpusDocument("k8s", "capture", "k3s")
	reality.Inventory.AddMetric("shared_total", TransportPrometheusRW2, InstrumentGauge, nil, nil)
	reality.Inventory.AddMetricProducer("shared_total", Producer{Name: "one"})
	findings := CompareCorpus(synth, []CorpusDocument{reality})
	if CountUnexemptedContradictions(findings) == 0 {
		t.Fatal("partial overlap hid same-producer contradictions")
	}
	if CountNoComparableProducers(findings) != 1 {
		t.Fatalf("unmatched second producer disappeared: %+v", findings)
	}
	other := cloneCorpusDocument(reality)
	other.Source.Substrate = "eks"
	other.Authority.Substrates = []string{"eks"}
	other.Inventory.Metrics[0].Producers = []Producer{{Name: "two"}}
	if got := CountNoComparableProducers(CompareCorpus(synth, []CorpusDocument{reality, other})); got != 0 {
		t.Fatalf("comparable evidence elsewhere ignored: %d", got)
	}
}

func TestProducerCoverageRatchet(t *testing.T) {
	for _, tc := range []struct {
		data  string
		count int
		ok    bool
	}{
		{`{"version":"synthkit.telemetry.producer-coverage/v1alpha1","expected_count":2}`, 2, true},
		{`{"version":"synthkit.telemetry.producer-coverage/v1alpha1","expected_count":2}`, 1, true},
		{`{"version":"synthkit.telemetry.producer-coverage/v1alpha1","expected_count":2}`, 3, false},
		{`{"version":"synthkit.telemetry.producer-coverage/v1alpha1"}`, 0, false},
		{`{"version":"synthkit.telemetry.producer-coverage/v1alpha1","expected_count":null}`, 0, false},
		{`{"version":"synthkit.telemetry.producer-coverage/v1alpha1","expected_count":-1}`, 0, false},
		{`{"version":"wrong","expected_count":2}`, 0, false},
		{`{"version":"synthkit.telemetry.producer-coverage/v1alpha1","expected_count":2,"reason":"escape"}`, 0, false},
		{`{"version":"synthkit.telemetry.producer-coverage/v1alpha1","expected_count":0,"expected_count":20}`, 10, false},
	} {
		r, err := DecodeProducerCoverageRatchet([]byte(tc.data))
		if err == nil {
			err = r.Check(tc.count)
		}
		if (err == nil) != tc.ok {
			t.Fatalf("data=%s count=%d error=%v", tc.data, tc.count, err)
		}
	}
}

func TestNoComparableProducerCountsNamesOnce(t *testing.T) {
	synth := New()
	synth.AddMetric("shared_total", TransportPrometheusRW2, InstrumentCounter, nil, nil)
	synth.Metrics[0].Producers = []Producer{{Name: "modeled", AllowListVersion: "one"}, {Name: "modeled", AllowListVersion: "two"}}
	reality := validCorpusDocument("k8s", "capture", "k3s")
	reality.Inventory.AddMetric("shared_total", TransportPrometheusRW2, InstrumentCounter, nil, nil)
	reality.Inventory.AddMetricProducer("shared_total", Producer{Name: "observed"})
	if got := CountNoComparableProducers(CompareCorpus(synth, []CorpusDocument{reality})); got != 1 {
		t.Fatalf("duplicate provenance counted as multiple identities: got=%d want=1", got)
	}
}

func TestProducerCoverageTriagedClaimsRejectReplacement(t *testing.T) {
	data := []byte(`{"version":"synthkit.telemetry.producer-coverage/v1alpha1","expected_count":1,"claims":[{"signal":"shared_total","producer":"promrw/known","reason":"Different observed job; requires independently paired evidence."}]}`)
	ratchet, err := DecodeProducerCoverageRatchet(data)
	if err != nil {
		t.Fatal(err)
	}
	findings := []ScopedFinding{{Finding: Finding{Kind: KindNoComparableProducer, Signal: "shared_total", SynthValues: []string{"promrw/known"}}}}
	if err := ratchet.CheckFindings(findings); err != nil {
		t.Fatal(err)
	}
	findings[0].Finding.SynthValues = []string{"promrw/new"}
	if err := ratchet.CheckFindings(findings); err == nil {
		t.Fatal("same count hid a new unmatched claim")
	}
}
