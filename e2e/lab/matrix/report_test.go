// SPDX-License-Identifier: AGPL-3.0-only

package matrix

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/inventory"
)

func result(name string, outcome Outcome) Result {
	r := Result{
		ResultVersion: ResultVersion,
		Permutation:   name,
		Title:         name,
		Outcome:       outcome,
		Phase:         "complete",
		CaptureStatus: "proven",
	}
	if outcome == OutcomeCaptured {
		r.CandidatePath = "/tmp/" + name + "/candidate.json"
	}
	if outcome == OutcomeFailed {
		r.FailureReason = "helm install timed out"
		r.Phase = "chart-install"
	}
	return r
}

func schemaWith(metrics []inventory.Metric, logs []inventory.Log) inventory.Schema {
	schema := inventory.New()
	schema.Metrics = metrics
	schema.Logs = logs
	schema.Normalize()
	return schema
}

func TestVerdictIsNeverGreenOnPartialSuccess(t *testing.T) {
	results := []Result{
		result("a", OutcomeCaptured),
		result("b", OutcomeCaptured),
		result("c", OutcomeCaptured),
		result("d", OutcomeFailed),
	}
	report := Build("run", "now", 2, "", results, map[string]inventory.Schema{})
	if report.Verdict != VerdictPartial {
		t.Fatalf("verdict = %q, want PARTIAL", report.Verdict)
	}
	if report.Verdict.ExitCode() == 0 {
		t.Error("a partial run must not exit 0")
	}
	markdown := report.Markdown()
	if !strings.Contains(markdown, "PARTIAL — 3 of 4") {
		t.Errorf("headline does not state the partial count:\n%s", markdown)
	}
	if !strings.Contains(markdown, "This run is NOT a success") {
		t.Error("partial run must say so loudly")
	}
}

func TestVerdictCapturedOnlyWhenEverySelectedPermutationCaptured(t *testing.T) {
	results := []Result{result("a", OutcomeCaptured), result("b", OutcomeSkipped)}
	report := Build("run", "now", 2, "", results, map[string]inventory.Schema{})
	if report.Verdict != VerdictCaptured {
		t.Fatalf("verdict = %q, want CAPTURED; a skipped permutation is not a selected one", report.Verdict)
	}
	if report.Verdict.ExitCode() != 0 {
		t.Error("an all-captured run must exit 0")
	}
}

func TestVerdictFailedWhenNothingCaptured(t *testing.T) {
	results := []Result{result("a", OutcomeFailed), result("b", OutcomeEmpty)}
	report := Build("run", "now", 2, "", results, map[string]inventory.Schema{})
	if report.Verdict != VerdictFailed {
		t.Fatalf("verdict = %q, want FAILED", report.Verdict)
	}
	if report.Verdict.ExitCode() != 2 {
		t.Errorf("exit code = %d, want 2", report.Verdict.ExitCode())
	}
}

func TestEmptyAndFailedReadAsOppositeThings(t *testing.T) {
	empty := result("empty-one", OutcomeEmpty)
	empty.CaptureWindowSeconds = 300
	failed := result("failed-one", OutcomeFailed)
	failed.ExitCode = 3
	report := Build("run", "now", 2, "", []Result{empty, failed}, map[string]inventory.Schema{})
	markdown := report.Markdown()

	if !strings.Contains(markdown, "decoded zero requests in 300s") {
		t.Errorf("empty permutation must state that nothing was sent:\n%s", markdown)
	}
	if !strings.Contains(markdown, "This is evidence about the permutation, not a broken lab.") {
		t.Error("empty permutation must be attributed to the permutation")
	}
	if !strings.Contains(markdown, "The harness stopped in phase `chart-install`") {
		t.Errorf("failed permutation must name the phase:\n%s", markdown)
	}
	if !strings.Contains(markdown, "makes no claim about what this permutation emits") {
		t.Error("failed permutation must disclaim any finding about the permutation")
	}
}

func TestPartialNamesTheUnmetAcceptanceChecks(t *testing.T) {
	partial := result("half", OutcomePartial)
	partial.CaptureWindowSeconds = 240
	partial.Receipts = map[string]int{inventory.TransportPrometheusRW1: 12}
	partial.Counts = Counts{Metrics: 40}
	partial.Checks = []Check{
		{Name: "prometheus_remote_write_v1 receipt", Status: "PASS"},
		{Name: "otlp_logs receipt", Status: "FAIL", Detail: "no OTLP log request decoded"},
	}
	partial.CandidatePath = "/tmp/half/candidate.json"
	report := Build("run", "now", 2, "", []Result{partial}, map[string]inventory.Schema{})
	markdown := report.Markdown()
	if !strings.Contains(markdown, "unmet acceptance check: otlp_logs receipt (no OTLP log request decoded)") {
		t.Errorf("partial run must name the unmet check:\n%s", markdown)
	}
	if !strings.Contains(markdown, "NOT for promotion") {
		t.Error("a partial candidate must be marked ineligible for promotion")
	}
}

func TestComparisonIsNotClaimedWithFewerThanTwoCaptures(t *testing.T) {
	results := []Result{result("a", OutcomeCaptured), result("b", OutcomeFailed)}
	candidates := map[string]inventory.Schema{"a": schemaWith(nil, nil)}
	report := Build("run", "now", 2, "", results, candidates)
	if len(report.Disagreements) != 0 {
		t.Error("a single capture cannot disagree with anything")
	}
	if !strings.Contains(report.ComparisonNote, "NOT COMPARED") {
		t.Errorf("comparison note must not read as agreement: %q", report.ComparisonNote)
	}
	if strings.Contains(report.Markdown(), "No disagreement found") {
		t.Error("an unrun comparison must never render as agreement")
	}
}

func TestDisagreementClasses(t *testing.T) {
	loki := schemaWith(
		[]inventory.Metric{
			{Name: "shared_metric", Transports: []string{inventory.TransportPrometheusRW1}, Labels: []inventory.Attribute{{Key: "cluster"}, {Key: "job"}}},
			{Name: "only_in_loki", Transports: []string{inventory.TransportPrometheusRW1}},
		},
		[]inventory.Log{{Source: "kubernetes", Transport: inventory.TransportLoki}},
	)
	otlp := schemaWith(
		[]inventory.Metric{
			{Name: "shared_metric", Transports: []string{inventory.TransportOTLPMetrics}, Labels: []inventory.Attribute{{Key: "cluster"}, {Key: "k8s_node_name"}}},
		},
		[]inventory.Log{{Source: "kubernetes", Transport: inventory.TransportOTLPLogs}},
	)
	results := []Result{result("loki", OutcomeCaptured), result("otlp", OutcomeCaptured)}
	report := Build("run", "now", 2, "", results, map[string]inventory.Schema{"loki": loki, "otlp": otlp})

	classes := map[string]bool{}
	for _, disagreement := range report.Disagreements {
		classes[disagreement.Class] = true
	}
	for _, want := range []string{"family_scope", "label_envelope", "transport", "log_transport"} {
		if !classes[want] {
			t.Errorf("missing disagreement class %q; got %v", want, report.Disagreements)
		}
	}
	markdown := report.Markdown()
	if !strings.Contains(markdown, "not a defect to reconcile") {
		t.Error("the report must frame a disagreement as an estate property")
	}
	if !strings.Contains(markdown, "`only_in_loki`") {
		t.Errorf("family-scope disagreement not listed:\n%s", markdown)
	}
}

func TestReportNamesEveryCandidateAndDeniesCorpusWrites(t *testing.T) {
	captured := result("a", OutcomeCaptured)
	captured.CorpusAreas = []string{"k8s", "k8s-addons"}
	report := Build("run", "now", 2, "one k3d cluster per permutation", []Result{captured}, map[string]inventory.Schema{})
	markdown := report.Markdown()
	if !strings.Contains(markdown, "No permutation job wrote to `reality-corpus/`") {
		t.Error("the report must state that jobs never write the corpus")
	}
	if !strings.Contains(markdown, filepath.Clean("/tmp/a/candidate.json")) {
		t.Errorf("candidate path missing:\n%s", markdown)
	}
	if !strings.Contains(markdown, "signals areas: k8s, k8s-addons") {
		t.Error("candidate promotion target areas missing")
	}
	if !strings.Contains(markdown, "why that bound: one k3d cluster per permutation") {
		t.Error("the concurrency bound rationale must be stated in the report")
	}
}

func TestReportSurfacesUnprovenCaptureStatusAndDeviations(t *testing.T) {
	unproven := result("otel-receivers", OutcomeSkipped)
	unproven.CaptureStatus = "unproven"
	unproven.Title = "OTel Collector native receivers"
	unproven.Deviations = []string{"the hosted destination is replaced by the in-cluster capture receiver"}
	unproven.TeardownConfirmed = "true"
	report := Build("run", "now", 2, "", []Result{unproven}, map[string]inventory.Schema{})
	markdown := report.Markdown()
	if !strings.Contains(markdown, "capture status: UNPROVEN") {
		t.Errorf("an unproven permutation must be marked as such:\n%s", markdown)
	}
	if !strings.Contains(markdown, "Do not read an absent family here as evidence") {
		t.Error("an unproven permutation must warn against over-reading it")
	}
	if !strings.Contains(markdown, "deviation from the deployment it models: the hosted destination") {
		t.Error("declared deviations must reach the report")
	}
	if !strings.Contains(markdown, "teardown confirmed: true") {
		t.Error("teardown confirmation must reach the report")
	}
}

// A family one permutation simply had not scraped yet must not read as a deployment
// difference, so the report states whether the compared permutations looked for the same time.
func TestComparisonStatesWhetherTheDwellWasCommon(t *testing.T) {
	same := []Result{result("a", OutcomeCaptured), result("b", OutcomeCaptured)}
	same[0].CaptureWindowSeconds = 300
	same[1].CaptureWindowSeconds = 300
	candidates := map[string]inventory.Schema{"a": schemaWith(nil, nil), "b": schemaWith(nil, nil)}
	report := Build("run", "now", 2, "", same, candidates)
	if !strings.Contains(report.ComparisonNote, "same 300s capture window") {
		t.Errorf("a common dwell must be stated: %q", report.ComparisonNote)
	}

	differing := []Result{result("a", OutcomeCaptured), result("b", OutcomeCaptured)}
	differing[0].CaptureWindowSeconds = 300
	differing[1].CaptureWindowSeconds = 90
	report = Build("run", "now", 2, "", differing, candidates)
	if !strings.Contains(report.ComparisonNote, "did NOT observe the same capture window") {
		t.Errorf("an uneven dwell must be flagged: %q", report.ComparisonNote)
	}
	if !strings.Contains(report.ComparisonNote, "treat every family_scope row below as unconfirmed") {
		t.Error("an uneven dwell must qualify the family_scope rows")
	}
}

// A pod-log stream fused under the empty source by the receiver's `source`-label keying must be
// reported as INCONCLUSIVE, never as an absent family: calling a keying artefact a
// disagreement is the one thing this report must not do.
func TestFusedSourcelessStreamIsInconclusiveNotAbsent(t *testing.T) {
	// A Loki capture whose pod-log stream has fused with another source-less lane: it carries a
	// partial pod-log identity, so no shape rule classifies it.
	lokiFused := schemaWith(nil, []inventory.Log{{
		Source:    "",
		Transport: inventory.TransportLoki,
		StreamLabels: []inventory.Attribute{
			{Key: "container"}, {Key: "namespace"}, {Key: "k8s_kind"}, {Key: "service_name"},
		},
	}})
	otlpClean := schemaWith(nil, []inventory.Log{{
		Source:    "lab-catalog",
		Transport: inventory.TransportOTLPLogs,
		StreamLabels: []inventory.Attribute{
			{Key: "k8s.namespace.name"}, {Key: "k8s.pod.name"}, {Key: "k8s.container.name"},
		},
	}})
	results := []Result{result("loki", OutcomeCaptured), result("otlp", OutcomeCaptured)}
	report := Build("run", "now", 2, "", results, map[string]inventory.Schema{"loki": lokiFused, "otlp": otlpClean})

	var row *Disagreement
	for i := range report.Disagreements {
		if report.Disagreements[i].Signal == inventory.LogFamilyPodLogs {
			row = &report.Disagreements[i]
		}
	}
	if row == nil {
		t.Fatalf("no pod-log row; got %+v", report.Disagreements)
	}
	for side, permutations := range row.Sides {
		if side == "absent" {
			t.Errorf("permutations %v were called absent when the evidence is inconclusive", permutations)
		}
	}
	if !strings.Contains(report.Markdown(), "INCONCLUSIVE (fused source-less stream)") {
		t.Error("the inconclusive side must be visible in the report")
	}

	// A permutation with no source-less stream at all is genuinely absent, and must still say so.
	empty := schemaWith(nil, []inventory.Log{{Source: "kubernetes-events", Transport: inventory.TransportLoki}})
	report = Build("run", "now", 2, "", results, map[string]inventory.Schema{"loki": empty, "otlp": otlpClean})
	for _, disagreement := range report.Disagreements {
		if disagreement.Signal != inventory.LogFamilyPodLogs {
			continue
		}
		if _, ok := disagreement.Sides["absent"]; !ok {
			t.Errorf("a genuine absence must still read as absent: %+v", disagreement.Sides)
		}
	}
}
