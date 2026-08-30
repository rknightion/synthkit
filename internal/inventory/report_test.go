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

func TestWriteFindingsReportDistinguishesLiveReadbackFromK3D(t *testing.T) {
	t.Parallel()
	findings := []ScopedFinding{
		{Area: "k8s", Source: CorpusSource{Kind: "gcx_live_readback", Substrate: "eks"}, Substrate: "eks", Finding: Finding{
			Kind: KindExtraMetric, Disposition: DispositionCoverageGap, Signal: "kube_node_info", Field: "name",
		}},
		{Area: "k8s", Source: CorpusSource{Kind: "k3d_lab", Substrate: "k3s"}, Substrate: "k3s", Finding: Finding{
			Kind: KindExtraMetric, Disposition: DispositionCoverageGap, Signal: "container_cpu_usage_seconds_total", Field: "name",
		}},
	}
	var out bytes.Buffer
	if err := WriteFindingsReport(&out, findings); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"live-read-only EKS evidence", "k3d-covered evidence"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("report missing evidence scope %q:\n%s", want, out.String())
		}
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

func TestWriteFindingsReportLeadsWithTheOneSidedDifference(t *testing.T) {
	findings := []ScopedFinding{
		{Area: "k8s-addons", Source: CorpusSource{Kind: "k3d_lab", Substrate: "k3s"}, Substrate: "k3s", Finding: Finding{
			Kind: KindUnexpectedLabelKey, Disposition: DispositionContradiction, Signal: "coredns_panics_total", Field: "labels",
			SynthValues:   []string{"job", "node"},
			RealityValues: []string{"job", "source"},
		}},
	}
	var out bytes.Buffer
	if err := WriteFindingsReport(&out, findings); err != nil {
		t.Fatal(err)
	}
	want := "field `labels` (only-in-synth=[node]; synth=[job, node]; reality=[job, source])."
	if !strings.Contains(out.String(), want) {
		t.Fatalf("report missing the one-sided difference %q:\n%s", want, out.String())
	}
}

func TestWriteFindingsReportNamesCrossSubstrateMatchAndAbsentEvidence(t *testing.T) {
	findings := []ScopedFinding{{
		Area: "k8s", Source: CorpusSource{Kind: "capture", Substrate: "gcp"}, Substrate: "gcp",
		MatchingSubstrates:       []string{"eks"},
		AbsentEvidenceSubstrates: []string{"k3s"},
		Finding: Finding{
			Kind: KindUnexpectedLabelKey, Disposition: DispositionContradiction,
			Signal: "kube_pod_info", Field: "labels", SynthValues: []string{"job", "source"}, RealityValues: []string{"job"},
		},
	}}
	var out bytes.Buffer
	if err := WriteFindingsReport(&out, findings); err != nil {
		t.Fatal(err)
	}
	report := out.String()
	for _, want := range []string{"on substrate `gcp`", "matching evidence on substrate(s) `eks`", "absent evidence on substrate(s) `k3s`"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func TestWriteFindingsReportExplainsOpenValueSetCoverageGap(t *testing.T) {
	findings := []ScopedFinding{
		{Area: "cw", Source: CorpusSource{Kind: "gcx_live_readback", Substrate: "eks"}, Substrate: "eks", Finding: Finding{
			Kind: KindLabelValueContradiction, Disposition: DispositionCoverageGap,
			Signal: "aws_request_total", Field: "labels.region",
			SynthValues: []string{"eu-west-1", "us-east-1"}, RealityValues: []string{"eu-west-1"},
		}},
	}
	var out bytes.Buffer
	if err := WriteFindingsReport(&out, findings); err != nil {
		t.Fatal(err)
	}
	if want := "only-in-synth=[us-east-1]; synth-only value has no closed-set evidence"; !strings.Contains(out.String(), want) {
		t.Fatalf("report missing %q:\n%s", want, out.String())
	}
}

func TestWriteFindingsReportMakesUnknownInstrumentEvidenceExplicit(t *testing.T) {
	findings := []ScopedFinding{
		{Area: "k8s", Source: CorpusSource{Kind: "k3d_lab", Substrate: "k3s"}, Substrate: "k3s", Finding: Finding{
			Kind: KindUnknownInstrumentEvidence, Disposition: DispositionCoverageGap,
			Signal: "kube_pod_info", Field: "instrument_types",
			SynthValues: []string{InstrumentGauge}, RealityValues: []string{InstrumentUnknown},
		}},
	}
	var out bytes.Buffer
	if err := WriteFindingsReport(&out, findings); err != nil {
		t.Fatal(err)
	}
	if want := "corpus did not observe an instrument type"; !strings.Contains(out.String(), want) {
		t.Fatalf("report missing %q:\n%s", want, out.String())
	}
}

func TestWriteFindingsReportDoesNotMislabelKeyCoverageAsOpenValueSet(t *testing.T) {
	findings := []ScopedFinding{
		{Area: "k8s", Source: CorpusSource{Kind: "k3d_lab", Substrate: "k3s"}, Substrate: "k3s", Finding: Finding{
			Kind: KindUnexpectedLabelKey, Disposition: DispositionCoverageGap,
			Signal: "kubernetes_build_info", Field: "labels",
			SynthValues: []string{"job", "source"}, RealityValues: []string{"job"},
		}},
	}
	var out bytes.Buffer
	if err := WriteFindingsReport(&out, findings); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "synth-only value has no closed-set evidence") {
		t.Fatalf("label-key coverage was described as an open value set:\n%s", out.String())
	}
}

func TestWriteFindingsReportSplitsTwoDirectionalFindingLines(t *testing.T) {
	findings := []ScopedFinding{
		{Area: "cw", Source: CorpusSource{Kind: "k3d_lab", Substrate: "k3s"}, Substrate: "k3s", Finding: Finding{
			Kind: KindUnexpectedLabelKey, Disposition: DispositionContradiction, Signal: "aws_applicationelb_info", Field: "labels",
			SynthValues: []string{"job", "tag_VpcId"}, RealityValues: []string{"job"},
		}},
		{Area: "cw", Source: CorpusSource{Kind: "k3d_lab", Substrate: "k3s"}, Substrate: "k3s", Finding: Finding{
			Kind: KindUnexpectedLabelKey, Disposition: DispositionCoverageGap, Signal: "aws_applicationelb_info", Field: "labels",
			SynthValues: []string{"job"}, RealityValues: []string{"job", "scrape_job"},
		}},
	}

	var out bytes.Buffer
	if err := WriteFindingsReport(&out, findings); err != nil {
		t.Fatal(err)
	}
	report := out.String()
	contradictions := reportSection(report, "## Contradictions", "## Coverage gaps")
	gaps := reportSection(report, "## Coverage gaps", "")
	if !strings.Contains(contradictions, "only-in-synth=[tag_VpcId]") || strings.Contains(contradictions, "only-in-reality=[scrape_job]") {
		t.Fatalf("contradiction line includes the wrong direction:\n%s", contradictions)
	}
	if !strings.Contains(gaps, "only-in-reality=[scrape_job]") || strings.Contains(gaps, "only-in-synth=[tag_VpcId]") {
		t.Fatalf("coverage-gap line includes the wrong direction:\n%s", gaps)
	}
	if contradictionLines, gapLines := findingLines(contradictions), findingLines(gaps); len(contradictionLines) != 1 || len(gapLines) != 1 || contradictionLines[0] == gapLines[0] {
		t.Fatalf("directional lines are not independent:\ncontradictions=%v\ngaps=%v", contradictionLines, gapLines)
	}
}

func reportSection(report, heading, nextHeading string) string {
	start := strings.Index(report, heading)
	if start < 0 {
		return ""
	}
	section := report[start:]
	if nextHeading != "" {
		if end := strings.Index(section, nextHeading); end >= 0 {
			section = section[:end]
		}
	}
	return section
}

func findingLines(section string) []string {
	lines := make([]string, 0)
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "- `") {
			lines = append(lines, line)
		}
	}
	return lines
}
