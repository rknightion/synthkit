// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteFindingsReportGroupsFindingsAndEmitsAreaPendingStubs(t *testing.T) {
	doc := validCorpusDocument("k8s", "producer", "k3s")
	findings := []ScopedFinding{
		{Area: doc.Area, Source: doc.Source, Substrate: doc.Source.Substrate, Finding: Finding{
			Kind: KindExtraMetric, Disposition: DispositionCoverageGap, Signal: "kube_pod_info", Field: "name",
		}},
		{Area: doc.Area, Source: doc.Source, Substrate: doc.Source.Substrate, Finding: Finding{
			Kind: KindMissingMetric, Disposition: DispositionContradiction, Signal: "invented_total", Field: "name",
		}},
	}

	var out bytes.Buffer
	if err := WriteFindingsReport(&out, findings); err != nil {
		t.Fatal(err)
	}
	report := out.String()
	for _, want := range []string{
		"Signal fidelity findings",
		"Contradictions",
		"Coverage gaps",
		"missing_metric",
		"extra_metric",
		"signals/k8s.md",
		"PENDING",
		"kube_pod_info",
		"Do not allocate an SK-N ID",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "SK-1") {
		t.Fatalf("report allocated a cantfind ID:\n%s", report)
	}
	wantFinding := "- `signals/k8s.md` — on substrate `k3s` from generic source `producer`: signal `invented_total`, field `name` (synth=[]; reality=[]).\n"
	if !strings.Contains(report, wantFinding) {
		t.Fatalf("report finding line=%q, want exact line:\n%s", wantFinding, report)
	}
	if strings.Index(report, "Contradictions") > strings.Index(report, "Coverage gaps") {
		t.Fatalf("report groups coverage before contradiction:\n%s", report)
	}
}

func TestWriteFindingsReportIsDeterministicAndEmptyReportIsUseful(t *testing.T) {
	first := []ScopedFinding{
		{Area: "logs", Source: CorpusSource{Kind: "z", Substrate: "k3s"}, Substrate: "k3s", Finding: Finding{
			Kind: KindUnexpectedLabelKey, Disposition: DispositionCoverageGap, Signal: "zeta", Field: "labels",
		}},
		{Area: "apm", Source: CorpusSource{Kind: "a", Substrate: "eks"}, Substrate: "eks", Finding: Finding{
			Kind: KindExtraMetric, Disposition: DispositionCoverageGap, Signal: "alpha", Field: "name",
		}},
	}
	second := []ScopedFinding{first[1], first[0]}
	var a, b bytes.Buffer
	if err := WriteFindingsReport(&a, first); err != nil {
		t.Fatal(err)
	}
	if err := WriteFindingsReport(&b, second); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Fatalf("report order changed output\nfirst:\n%s\nsecond:\n%s", a.String(), b.String())
	}

	var empty bytes.Buffer
	if err := WriteFindingsReport(&empty, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(empty.String(), "No findings") || !strings.Contains(empty.String(), "report-only") {
		t.Fatalf("empty report is not useful:\n%s", empty.String())
	}
}
