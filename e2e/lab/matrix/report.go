// SPDX-License-Identifier: AGPL-3.0-only

package matrix

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/rknightion/synthkit/internal/inventory"
)

// ReportVersion is the version of the combined matrix report's machine-readable form.
const ReportVersion = "synthkit.lab.matrix-report/v1alpha1"

// Verdict is the whole run's headline. There are three because "some of it worked" is a
// distinct state that must never be rendered as success.
type Verdict string

const (
	// VerdictCaptured means every permutation in the run captured. This is the only green.
	VerdictCaptured Verdict = "CAPTURED"
	// VerdictPartial means at least one captured and at least one did not.
	VerdictPartial Verdict = "PARTIAL"
	// VerdictFailed means nothing captured.
	VerdictFailed Verdict = "FAILED"
)

// ExitCode is the process status the matrix entrypoint returns.
func (v Verdict) ExitCode() int {
	switch v {
	case VerdictCaptured:
		return 0
	case VerdictPartial:
		return 1
	default:
		return 2
	}
}

// Disagreement is one place two captured permutations describe the same estate differently.
// It is deliberately called a disagreement and never reconciled: two documented deployment
// methods producing different observable shapes for the same cluster is the property SKT-0013
// exists to record, not a defect in either capture.
type Disagreement struct {
	Class  string `json:"class"`
	Signal string `json:"signal"`
	Field  string `json:"field,omitempty"`
	// Sides maps a rendered value ("present", a sorted label-key list, a transport) to the
	// permutations that observed it.
	Sides map[string][]string `json:"sides"`
	Note  string              `json:"note,omitempty"`
}

// Report is the single combined report a matrix run ends in.
type Report struct {
	ReportVersion   string `json:"report_version"`
	RunID           string `json:"run_id"`
	GeneratedAt     string `json:"generated_at"`
	MaxParallel     int    `json:"max_parallel"`
	ParallelismNote string `json:"parallelism_note,omitempty"`

	Verdict Verdict  `json:"verdict"`
	Results []Result `json:"results"`

	Captured int `json:"captured"`
	Partial  int `json:"partial"`
	Empty    int `json:"empty"`
	Failed   int `json:"failed"`
	Skipped  int `json:"skipped"`

	ComparedPermutations []string       `json:"compared_permutations"`
	Disagreements        []Disagreement `json:"disagreements"`
	// ComparisonNote states why a comparison was or was not possible. An unrun comparison must
	// never render as "the permutations agree".
	ComparisonNote string `json:"comparison_note"`
}

// dwellNote states whether the compared permutations looked for the same length of time. They
// must, or a family one job simply had not scraped yet reads as a deployment difference. The
// matrix gives every permutation the same fixed capture window for exactly this reason, and the
// report says so rather than leaving the reader to assume it.
func dwellNote(compared []string, results []Result) string {
	windows := map[int][]string{}
	for _, result := range results {
		for _, name := range compared {
			if result.Permutation == name {
				windows[result.CaptureWindowSeconds] = append(windows[result.CaptureWindowSeconds], name)
			}
		}
	}
	if len(windows) == 1 {
		for window := range windows {
			return fmt.Sprintf("Every compared permutation observed the same %ds capture window, so a family present in one and absent in another is a deployment difference and not a shorter look. A family whose producer scrapes less often than that window can still be absent by timing, so confirm a family_scope difference against a longer window before recording it in signals/.", window)
		}
	}
	parts := make([]string, 0, len(windows))
	for _, window := range sortedInts(windows) {
		parts = append(parts, fmt.Sprintf("%ds: %s", window, strings.Join(windows[window], ", ")))
	}
	return "WARNING: the compared permutations did NOT observe the same capture window (" +
		strings.Join(parts, "; ") +
		"). A family present in one and absent in another may be a shorter look rather than a deployment difference; treat every family_scope row below as unconfirmed."
}

func sortedInts[V any](in map[int]V) []int {
	out := make([]int, 0, len(in))
	for key := range in {
		out = append(out, key)
	}
	sort.Ints(out)
	return out
}

// disagreementListCap bounds how many individual rows one class prints in the Markdown
// report. A three-permutation run over ~100 metric families produces more rows than an
// operator reads; the count is always exact and the JSON form is never truncated.
const disagreementListCap = 25

// Build assembles the combined report. candidates maps a permutation name to the normalized
// candidate that permutation produced; only captured permutations are compared, because
// comparing against a failed or empty job would manufacture a disagreement out of an absence.
func Build(runID, generatedAt string, maxParallel int, parallelismNote string, results []Result, candidates map[string]inventory.Schema) Report {
	report := Report{
		ReportVersion:   ReportVersion,
		RunID:           runID,
		GeneratedAt:     generatedAt,
		MaxParallel:     maxParallel,
		ParallelismNote: parallelismNote,
		Results:         results,
		Disagreements:   []Disagreement{},
	}

	comparable := make([]string, 0, len(results))
	for _, result := range results {
		switch result.Outcome {
		case OutcomeCaptured:
			report.Captured++
			if _, ok := candidates[result.Permutation]; ok {
				comparable = append(comparable, result.Permutation)
			}
		case OutcomePartial:
			report.Partial++
		case OutcomeEmpty:
			report.Empty++
		case OutcomeFailed:
			report.Failed++
		case OutcomeSkipped:
			report.Skipped++
		}
	}
	sort.Strings(comparable)
	report.ComparedPermutations = comparable

	selected := len(results) - report.Skipped
	switch {
	case report.Captured == 0:
		report.Verdict = VerdictFailed
	case report.Captured == selected && selected > 0:
		report.Verdict = VerdictCaptured
	default:
		report.Verdict = VerdictPartial
	}

	switch {
	case len(comparable) >= 2:
		report.Disagreements = compare(comparable, candidates)
		report.ComparisonNote = fmt.Sprintf(
			"Compared %d captured permutations: %s. Permutations that failed, captured nothing, or only partially captured are excluded, because an absence in a job that did not observe is not a disagreement. %s",
			len(comparable), strings.Join(comparable, ", "), dwellNote(comparable, results))
	case len(comparable) == 1:
		report.ComparisonNote = fmt.Sprintf(
			"NOT COMPARED: only one permutation captured (%s). A single capture cannot disagree with anything, so this run says nothing about how the permutations differ.",
			comparable[0])
	default:
		report.ComparisonNote = "NOT COMPARED: no permutation captured, so this run says nothing about how the permutations differ."
	}
	return report
}

type metricView struct {
	labelKeys  []string
	transports []string
}

func viewMetrics(schema inventory.Schema) map[string]metricView {
	out := make(map[string]metricView, len(schema.Metrics))
	for _, metric := range schema.Metrics {
		keys := make([]string, 0, len(metric.Labels))
		for _, label := range metric.Labels {
			keys = append(keys, label.Key)
		}
		sort.Strings(keys)
		transports := append([]string{}, metric.Transports...)
		sort.Strings(transports)
		out[metric.Name] = metricView{labelKeys: keys, transports: transports}
	}
	return out
}

func compare(permutations []string, candidates map[string]inventory.Schema) []Disagreement {
	views := make(map[string]map[string]metricView, len(permutations))
	logs := make(map[string]map[string][]string, len(permutations))
	families := map[string]struct{}{}
	sources := map[string]struct{}{}
	for _, permutation := range permutations {
		schema := candidates[permutation]
		view := viewMetrics(schema)
		views[permutation] = view
		for name := range view {
			families[name] = struct{}{}
		}
		perSource := map[string][]string{}
		for _, log := range schema.Logs {
			// Keyed by SHAPE-derived family, not by the recorded source name. The source name is
			// a capture-specific exemplar and the two pod-log lanes name it differently: the
			// OTLP lane keys on the service name while the Loki-native lane stamps no `source`
			// label at all. Comparing raw source names would therefore report the SAME pod-log
			// stream as four families absent from one permutation, which reads as a coverage
			// difference when the real disagreement is the transport.
			key := logFamilyKey(log)
			perSource[key] = append(perSource[key], log.Transport)
			sources[key] = struct{}{}
		}
		for source := range perSource {
			sort.Strings(perSource[source])
		}
		logs[permutation] = perSource
	}

	out := make([]Disagreement, 0)
	for _, name := range sortedKeys(families) {
		present := map[string][]string{}
		labelSets := map[string][]string{}
		transportSets := map[string][]string{}
		for _, permutation := range permutations {
			view, ok := views[permutation][name]
			if !ok {
				present["absent"] = append(present["absent"], permutation)
				continue
			}
			present["present"] = append(present["present"], permutation)
			labelSets[strings.Join(view.labelKeys, ", ")] = append(labelSets[strings.Join(view.labelKeys, ", ")], permutation)
			transportSets[strings.Join(view.transports, ", ")] = append(transportSets[strings.Join(view.transports, ", ")], permutation)
		}
		if len(present) > 1 {
			out = append(out, Disagreement{
				Class:  "family_scope",
				Signal: name,
				Sides:  present,
				Note:   "One deployment method produces this family and another does not. A blueprint modelling the second must not emit it.",
			})
			continue
		}
		if len(labelSets) > 1 {
			out = append(out, Disagreement{
				Class:  "label_envelope",
				Signal: name,
				Field:  "labels",
				Sides:  labelSets,
				Note:   "Both deployment methods produce the family with a different label envelope.",
			})
		}
		if len(transportSets) > 1 {
			out = append(out, Disagreement{
				Class:  "transport",
				Signal: name,
				Field:  "transports",
				Sides:  transportSets,
				Note:   "The same family leaves the collector on different wire protocols depending on deployment method.",
			})
		}
	}

	fused := map[string]bool{}
	for _, permutation := range permutations {
		fused[permutation] = hasFusedSourcelessStream(candidates[permutation])
	}
	for _, source := range sortedKeys(sources) {
		sides := map[string][]string{}
		inconclusive := false
		for _, permutation := range permutations {
			transports, ok := logs[permutation][source]
			if !ok {
				// A permutation whose capture holds a source-less stream carrying part of a
				// pod-log identity cannot be said to lack the pod-log family: the receiver keys
				// Loki streams on the `source` label, so several source-less lanes fuse into one
				// entry whose union label set matches no shape rule. Calling that "absent" would
				// invent a disagreement out of a keying artefact, which is the one thing this
				// report must never do.
				if source == inventory.LogFamilyPodLogs && fused[permutation] {
					sides["INCONCLUSIVE (fused source-less stream)"] = append(sides["INCONCLUSIVE (fused source-less stream)"], permutation)
					inconclusive = true
					continue
				}
				sides["absent"] = append(sides["absent"], permutation)
				continue
			}
			sides[strings.Join(sortedUnique(transports), ", ")] = append(sides[strings.Join(sortedUnique(transports), ", ")], permutation)
		}
		if len(sides) > 1 {
			note := "The same log source leaves the collector on a different transport depending on deployment method."
			if inconclusive {
				note = "One side is INCONCLUSIVE, not absent: the capture receiver keys Loki streams on the `source` label, and a real pod-log stream carries none, so it fuses with every other source-less lane into one entry that matches no shape rule. Resolve it in the producer before reading this row as a deployment difference."
			}
			out = append(out, Disagreement{
				Class:  "log_transport",
				Signal: source,
				Field:  "transport",
				Sides:  sides,
				Note:   note,
			})
		}
	}

	for i := range out {
		for key := range out[i].Sides {
			sort.Strings(out[i].Sides[key])
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Class != out[j].Class {
			return out[i].Class < out[j].Class
		}
		return out[i].Signal < out[j].Signal
	})
	return out
}

// hasFusedSourcelessStream reports whether a capture holds a source-less Loki entry that
// carries at least one pod-log identity key. That is the fingerprint of several source-less
// lanes having been merged under the empty source by the receiver's `source`-label keying.
func hasFusedSourcelessStream(schema inventory.Schema) bool {
	identityKeys := []string{
		"k8s.pod.name", "k8s_pod_name", "pod",
		"k8s.container.name", "k8s_container_name", "container",
	}
	for _, log := range schema.Logs {
		if log.Source != "" {
			continue
		}
		if _, classified := inventory.ShapeLogFamily(log); classified {
			continue
		}
		for _, label := range log.StreamLabels {
			for _, key := range identityKeys {
				if label.Key == key {
					return true
				}
			}
		}
	}
	return false
}

func sortedUnique(values []string) []string {
	out := append([]string{}, values...)
	sort.Strings(out)
	return slices.Compact(out)
}

// logFamilyKey names a log entry by its structural family where a shape rule recognises it, and
// falls back to the recorded source only for an entry no rule classifies.
func logFamilyKey(log inventory.Log) string {
	if family, ok := inventory.ShapeLogFamily(log); ok {
		return family
	}
	if log.Source == "" {
		return "(unnamed source)"
	}
	return log.Source
}

func sortedKeys[V any](in map[string]V) []string {
	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// JSON renders the machine-readable report. It is never truncated.
func (r Report) JSON() ([]byte, error) {
	encoded, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

// Markdown renders the operator-facing combined report: one document that answers which
// permutations ran, which produced a capture, why each non-capture is not a capture, and where
// two permutations disagree about the same metric family.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# k3d capture-lab permutation matrix — %s\n\n", r.RunID)
	fmt.Fprintf(&b, "## Verdict: %s\n\n", r.verdictLine())
	if r.Verdict != VerdictCaptured {
		b.WriteString("This run is NOT a success. The rows below that are not `captured` are listed again, with cause, under \"Permutations that did not capture\".\n\n")
	}
	fmt.Fprintf(&b, "- generated_at: %s\n", r.GeneratedAt)
	fmt.Fprintf(&b, "- concurrency bound: %d permutation(s) at a time\n", r.MaxParallel)
	if r.ParallelismNote != "" {
		fmt.Fprintf(&b, "- why that bound: %s\n", r.ParallelismNote)
	}
	fmt.Fprintf(&b, "- outcomes: captured=%d partial=%d empty=%d failed=%d skipped=%d\n\n",
		r.Captured, r.Partial, r.Empty, r.Failed, r.Skipped)

	b.WriteString("## Matrix\n\n")
	b.WriteString("| permutation | outcome | phase reached | duration | metrics | logs | traces | requests decoded | capture status |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")
	for _, result := range r.Results {
		captureStatus := result.CaptureStatus
		if captureStatus == "" {
			captureStatus = "unknown"
		}
		fmt.Fprintf(&b, "| `%s` | **%s** | %s | %ds | %d | %d | %d | %d | %s |\n",
			result.Permutation, result.Outcome, orDash(result.Phase), result.DurationSeconds,
			result.Counts.Metrics, result.Counts.Logs, result.Counts.Traces,
			result.ReceiptTotal(), captureStatus)
	}
	b.WriteString("\nOutcome meanings, because an empty corpus entry has two opposite causes:\n\n")
	b.WriteString("- `captured` — the permutation's declared acceptance predicate was satisfied.\n")
	b.WriteString("- `partial` — the harness completed and evidence arrived, but the acceptance predicate was not satisfied inside the capture window. Partial evidence, not a capture.\n")
	b.WriteString("- `empty` — the harness completed every step, the collector deployed and reported ready, and the receiver then decoded ZERO requests. This is a finding about the permutation.\n")
	b.WriteString("- `failed` — the harness could not complete its own steps. This run observed nothing and makes NO claim about the permutation.\n")
	b.WriteString("- `skipped` — not selected for this run.\n\n")

	b.WriteString(r.permutationSection())
	b.WriteString(r.nonCapturedSection())
	b.WriteString(r.disagreementSection())
	b.WriteString(r.promotionSection())
	return b.String()
}

func (r Report) verdictLine() string {
	selected := len(r.Results) - r.Skipped
	switch r.Verdict {
	case VerdictCaptured:
		return fmt.Sprintf("CAPTURED — all %d selected permutation(s) captured", selected)
	case VerdictPartial:
		return fmt.Sprintf("PARTIAL — %d of %d selected permutation(s) captured", r.Captured, selected)
	default:
		return fmt.Sprintf("FAILED — 0 of %d selected permutation(s) captured", selected)
	}
}

// permutationSection describes what each permutation IS, so the combined report is enough on
// its own to decide which deployment a blueprint should model. It carries each permutation's
// declared deviations from the deployment it stands in for, because a capture read without them
// over-claims.
func (r Report) permutationSection() string {
	var b strings.Builder
	b.WriteString("## Permutations in this matrix\n\n")
	for _, result := range r.Results {
		fmt.Fprintf(&b, "### `%s` — %s\n\n", result.Permutation, result.Title)
		if result.Summary != "" {
			b.WriteString(result.Summary + "\n\n")
		}
		fmt.Fprintf(&b, "- collector: %s@%s\n", orDash(result.Collector), orDash(result.CollectorVersion))
		if result.CaptureStatus == "unproven" {
			b.WriteString("- capture status: UNPROVEN — this harness can select and deploy it, but no run has yet produced a curated corpus entry from it. Do not read an absent family here as evidence.\n")
		} else {
			b.WriteString("- capture status: proven\n")
		}
		if result.TeardownConfirmed != "" {
			fmt.Fprintf(&b, "- teardown confirmed: %s\n", result.TeardownConfirmed)
		}
		if result.InstrumentEvidence != "" {
			fmt.Fprintf(&b, "- producer-declared instrument types observed: %s\n", result.InstrumentEvidence)
		}
		for _, deviation := range result.Deviations {
			fmt.Fprintf(&b, "- deviation from the deployment it models: %s\n", deviation)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (r Report) nonCapturedSection() string {
	var b strings.Builder
	b.WriteString("## Permutations that did not capture\n\n")
	any := false
	for _, result := range r.Results {
		if result.Outcome.Succeeded() {
			continue
		}
		any = true
		fmt.Fprintf(&b, "### `%s` — %s\n\n", result.Permutation, result.Outcome)
		switch result.Outcome {
		case OutcomeFailed:
			fmt.Fprintf(&b, "The harness stopped in phase `%s`. It did not open an honest capture window, so this run makes no claim about what this permutation emits.\n\n", orDash(result.Phase))
			fmt.Fprintf(&b, "- cause: %s\n", orDash(result.FailureReason))
			fmt.Fprintf(&b, "- worker exit code: %d\n", result.ExitCode)
		case OutcomeEmpty:
			fmt.Fprintf(&b, "The harness completed every step and the collector reported ready, then decoded zero requests in %ds. Nothing was sent. This is evidence about the permutation, not a broken lab.\n\n", result.CaptureWindowSeconds)
		case OutcomePartial:
			fmt.Fprintf(&b, "Evidence arrived (%d request(s) decoded; metrics=%d logs=%d traces=%d) but the acceptance predicate was not satisfied in %ds.\n\n",
				result.ReceiptTotal(), result.Counts.Metrics, result.Counts.Logs, result.Counts.Traces, result.CaptureWindowSeconds)
			for _, failed := range result.FailedChecks() {
				fmt.Fprintf(&b, "- unmet acceptance check: %s\n", failed)
			}
			if result.CandidatePath != "" {
				fmt.Fprintf(&b, "- partial candidate retained for inspection, NOT for promotion: `%s`\n", result.CandidatePath)
			}
		case OutcomeSkipped:
			b.WriteString("Not selected for this run.\n")
		}
		for _, diagnostic := range result.DiagnosticPaths {
			fmt.Fprintf(&b, "- diagnostics: `%s`\n", diagnostic)
		}
		b.WriteString("\n")
	}
	if !any {
		b.WriteString("None: every selected permutation captured.\n\n")
	}
	return b.String()
}

func (r Report) disagreementSection() string {
	var b strings.Builder
	b.WriteString("## Where the permutations disagree\n\n")
	b.WriteString(r.ComparisonNote + "\n\n")
	if len(r.ComparedPermutations) < 2 {
		return b.String()
	}
	if len(r.Disagreements) == 0 {
		b.WriteString("No disagreement found: the compared permutations produced the same families, label envelopes and transports.\n\n")
		return b.String()
	}
	b.WriteString("A disagreement is a real property of the estate, not a defect to reconcile. Two documented deployment methods produce materially different observable shapes for the same cluster, and a blueprint modelling one of them must not silently emit the other's families.\n\n")

	byClass := map[string][]Disagreement{}
	for _, disagreement := range r.Disagreements {
		byClass[disagreement.Class] = append(byClass[disagreement.Class], disagreement)
	}
	for _, class := range sortedKeys(byClass) {
		list := byClass[class]
		fmt.Fprintf(&b, "### %s (%d)\n\n", class, len(list))
		if len(list) > 0 && list[0].Note != "" {
			b.WriteString(list[0].Note + "\n\n")
		}
		shown := list
		if len(shown) > disagreementListCap {
			shown = shown[:disagreementListCap]
		}
		for _, disagreement := range shown {
			fmt.Fprintf(&b, "- `%s`", disagreement.Signal)
			if disagreement.Field != "" {
				fmt.Fprintf(&b, " field `%s`", disagreement.Field)
			}
			b.WriteString(": ")
			parts := make([]string, 0, len(disagreement.Sides))
			for _, side := range sortedKeys(disagreement.Sides) {
				parts = append(parts, fmt.Sprintf("%s → [%s]", strings.Join(disagreement.Sides[side], ", "), side))
			}
			b.WriteString(strings.Join(parts, "; ") + "\n")
		}
		if len(list) > len(shown) {
			fmt.Fprintf(&b, "- ... and %d more; the full set is in the machine-readable report.\n", len(list)-len(shown))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (r Report) promotionSection() string {
	var b strings.Builder
	b.WriteString("## Corpus promotion\n\n")
	b.WriteString("No permutation job wrote to `reality-corpus/`. Each job writes its own candidate inside its own output directory, so parallel jobs cannot race each other's corpus entries; merging a candidate into the corpus is a separate, deliberate step taken after reading this report.\n\n")
	any := false
	for _, result := range r.Results {
		if !result.Outcome.Succeeded() || result.CandidatePath == "" {
			continue
		}
		any = true
		areas := "unspecified"
		if len(result.CorpusAreas) > 0 {
			areas = strings.Join(result.CorpusAreas, ", ")
		}
		fmt.Fprintf(&b, "- `%s` → candidate `%s` (signals areas: %s)\n", result.Permutation, result.CandidatePath, areas)
	}
	if !any {
		b.WriteString("- No candidate is eligible for promotion from this run.\n")
	}
	b.WriteString("\n")
	return b.String()
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
