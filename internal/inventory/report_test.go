// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
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

func TestWriteFindingsReportGroupsEvidenceWithoutChangingFindingTuples(t *testing.T) {
	findings := []ScopedFinding{
		{
			Area: "k8s", Source: CorpusSource{Kind: "k3d_lab", Substrate: "k3s"}, Substrate: "k3s",
			Finding: Finding{Kind: KindExtraMetric, Disposition: DispositionCoverageGap, Signal: "shared_metric", Field: "name"},
		},
		{
			Area: "k8s", Source: CorpusSource{Kind: "synthkit_terraform_capture", Substrate: "aks"}, Substrate: "aks",
			Finding: Finding{Kind: KindExtraMetric, Disposition: DispositionCoverageGap, Signal: "shared_metric", Field: "name"},
		},
		{
			Area: "k8s-addons", Source: CorpusSource{Kind: "synthkit_terraform_capture", Substrate: "eks"}, Substrate: "eks",
			Finding: Finding{Kind: KindExtraMetric, Disposition: DispositionCoverageGap, Signal: "shared_metric", Field: "name"},
		},
		{
			Area: "k8s", Source: CorpusSource{Kind: "synthkit_terraform_capture", Substrate: "eks"}, Substrate: "eks",
			Finding: Finding{Kind: KindExtraMetric, Disposition: DispositionCoverageGap, Signal: "shared_metric", Field: "labels"},
		},
		{
			Area: "cw", Source: CorpusSource{Kind: "gcx_live_readback", Substrate: "eks"}, Substrate: "eks",
			Finding: Finding{Kind: KindUnknownInstrumentEvidence, Disposition: DispositionCoverageGap, Signal: "another_metric", Field: "instrument_types"},
		},
	}

	want := findingTupleSet(findings)
	var out bytes.Buffer
	if err := WriteFindingsReport(&out, findings); err != nil {
		t.Fatal(err)
	}
	report := out.String()
	if got := renderedFindingTupleSet(report); !reflect.DeepEqual(got, want) {
		t.Fatalf("report changed finding tuples:\n got=%v\nwant=%v\nreport:\n%s", got, want, report)
	}
	if got := len(findingLines(report)); got != 3 {
		t.Fatalf("report has %d finding lines, want one per class/signal/field group:\n%s", got, report)
	}
	if got := countReportLinesWithPrefix(report, "PENDING: "); got != 2 {
		t.Fatalf("report has %d PENDING lines, want one per signal:\n%s", got, report)
	}
	for _, wantEvidence := range []string{
		"substrate `aks` from generic source `synthkit_terraform_capture`",
		"substrate `eks` from generic source `synthkit_terraform_capture`",
		"substrate `k3s` from generic source `k3d_lab`",
	} {
		if !strings.Contains(report, wantEvidence) {
			t.Fatalf("report missing grouped evidence %q:\n%s", wantEvidence, report)
		}
	}
	if got := strings.Count(report, "\n"); got > 27212 {
		t.Fatalf("report has %d lines, want at most 27212:\n%s", got, report)
	}
}

func TestWriteFindingsReportKeepsSwappedValuePairsWithTheirEvidence(t *testing.T) {
	findings := []ScopedFinding{
		{
			Area: "cw", Source: CorpusSource{Kind: "capture", Substrate: "eks"}, Substrate: "eks",
			Finding: Finding{
				Kind: KindUnexpectedLabelKey, Disposition: DispositionContradiction,
				Signal: "shared_metric", Field: "labels",
				SynthValues: []string{"alpha"}, RealityValues: []string{"beta"},
			},
		},
		{
			Area: "k8s", Source: CorpusSource{Kind: "capture", Substrate: "eks"}, Substrate: "eks",
			Finding: Finding{
				Kind: KindUnexpectedLabelKey, Disposition: DispositionContradiction,
				Signal: "shared_metric", Field: "labels",
				SynthValues: []string{"beta"}, RealityValues: []string{"alpha"},
			},
		},
	}

	var out bytes.Buffer
	if err := WriteFindingsReport(&out, findings); err != nil {
		t.Fatal(err)
	}
	report := out.String()
	lines := findingLines(reportSection(report, "## Contradictions", "## Coverage gaps"))
	if len(lines) != 1 {
		t.Fatalf("report has %d grouped finding lines, want one:\n%s", len(lines), report)
	}
	for _, want := range []string{
		"substrate `eks` from generic source `capture` (only-in-synth=[alpha]; synth=[alpha]; reality=[beta])",
		"substrate `eks` from generic source `capture` (only-in-synth=[beta]; synth=[beta]; reality=[alpha])",
	} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("grouped finding lost value-to-evidence association %q:\n%s", want, lines[0])
		}
	}
	if strings.Contains(lines[0], "synth=[alpha, beta]") || strings.Contains(lines[0], "reality=[alpha, beta]") {
		t.Fatalf("grouped finding independently merged swapped value sets:\n%s", lines[0])
	}

	if got := renderedFindingTupleSet(report); !reflect.DeepEqual(got, findingTupleSet(findings)) {
		t.Fatalf("grouped evidence is no longer parseable as finding tuples:\n got=%v\nwant=%v\nreport:\n%s", got, findingTupleSet(findings), report)
	}
}

func TestWriteFindingsReportCommittedCorpusPreservesTuplesAndBound(t *testing.T) {
	const inventoryEnv = "SYNTHKIT_SIGNAL_FIDELITY_INVENTORY"
	inventoryPath := strings.TrimSpace(os.Getenv(inventoryEnv))
	if inventoryPath == "" {
		t.Skipf("%s is unset; set it to an existing -inventory-json export to run the committed-corpus renderer regression", inventoryEnv)
	}
	repoRoot := reportTestRepoRoot(t)
	if !filepath.IsAbs(inventoryPath) {
		inventoryPath = filepath.Join(repoRoot, inventoryPath)
	}
	synth := readReportTestInventory(t, inventoryPath)
	documents, err := LoadCorpusDir(filepath.Join(repoRoot, "reality-corpus"))
	if err != nil {
		t.Fatalf("load committed reality corpus: %v", err)
	}
	findings := CompareCorpus(synth, documents)

	var out bytes.Buffer
	if err := WriteFindingsReport(&out, findings); err != nil {
		t.Fatalf("render committed-corpus findings: %v", err)
	}
	if got, want := renderedFindingTupleSet(out.String()), findingTupleSet(findings); !reflect.DeepEqual(got, want) {
		t.Fatalf("committed-corpus report changed finding tuples: got %d, want %d", len(got), len(want))
	}
	wantPendingSignals := coverageGapSignalSet(findings)
	if got := renderedPendingSignalSet(out.String()); !reflect.DeepEqual(got, wantPendingSignals) {
		t.Fatalf("committed-corpus report changed PENDING signal set: got %d, want %d", len(got), len(wantPendingSignals))
	}
	if got := strings.Count(out.String(), "\n"); got > 27212 {
		t.Fatalf("committed-corpus report has %d lines, want at most 27212", got)
	}
	t.Logf("committed corpus findings=%d report_lines=%d", len(findings), strings.Count(out.String(), "\n"))
}

func reportTestRepoRoot(t *testing.T) string {
	t.Helper()
	_, sourcePath, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve report test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(sourcePath), "..", ".."))
}

func readReportTestInventory(t *testing.T, path string) Schema {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read inventory %q: %v", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var schema Schema
	if err := decoder.Decode(&schema); err != nil {
		t.Fatalf("decode inventory %q: %v", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			t.Fatalf("decode inventory %q: multiple JSON documents are not allowed", path)
		}
		t.Fatalf("decode inventory %q trailing JSON data: %v", path, err)
	}
	if schema.SchemaVersion != SchemaVersion {
		t.Fatalf("inventory %q schema_version=%q, want %q", path, schema.SchemaVersion, SchemaVersion)
	}
	return schema
}

func coverageGapSignalSet(findings []ScopedFinding) map[string]struct{} {
	set := make(map[string]struct{})
	for _, scoped := range findings {
		if scoped.Finding.Disposition == DispositionCoverageGap {
			set[scoped.Finding.Signal] = struct{}{}
		}
	}
	return set
}

func renderedPendingSignalSet(report string) map[string]struct{} {
	pattern := regexp.MustCompile("^PENDING: confirm (?:`[^`]*` )?signal `([^`]*)`")
	set := make(map[string]struct{})
	for _, line := range strings.Split(report, "\n") {
		match := pattern.FindStringSubmatch(line)
		if len(match) == 2 {
			set[match[1]] = struct{}{}
		}
	}
	return set
}

type reportFindingTuple struct {
	class, signal, field, substrate, source string
}

func findingTupleSet(findings []ScopedFinding) map[reportFindingTuple]struct{} {
	set := make(map[reportFindingTuple]struct{}, len(findings))
	for _, scoped := range findings {
		set[reportFindingTuple{
			class:     string(scoped.Finding.Kind),
			signal:    scoped.Finding.Signal,
			field:     scoped.Finding.Field,
			substrate: scoped.Substrate,
			source:    scoped.Source.Kind,
		}] = struct{}{}
	}
	return set
}

func renderedFindingTupleSet(report string) map[reportFindingTuple]struct{} {
	linePattern := regexp.MustCompile("signal `([^`]*)`, field `([^`]*)`")
	evidencePattern := regexp.MustCompile("substrate `([^`]*)` from generic source `([^`]*)`")
	set := make(map[reportFindingTuple]struct{})
	class := ""
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(line, "### ") {
			class = strings.TrimPrefix(line, "### ")
			continue
		}
		if !strings.HasPrefix(line, "- `") {
			continue
		}
		match := linePattern.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		for _, evidence := range evidencePattern.FindAllStringSubmatch(line, -1) {
			set[reportFindingTuple{
				class:     class,
				signal:    match[1],
				field:     match[2],
				substrate: evidence[1],
				source:    evidence[2],
			}] = struct{}{}
		}
	}
	return set
}

func countReportLinesWithPrefix(report, prefix string) int {
	count := 0
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(line, prefix) {
			count++
		}
	}
	return count
}
