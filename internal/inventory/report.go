// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// WriteFindingsReport writes the deterministic fidelity report. Findings are grouped first by
// disposition and then by finding class; coverage gaps include unnumbered PENDING stubs suitable
// for copying into cantfind.md. Exempted contradictions remain in the contradiction section.
func WriteFindingsReport(w io.Writer, findings []ScopedFinding) error {
	ordered := append([]ScopedFinding{}, findings...)
	sort.SliceStable(ordered, func(i, j int) bool { return compareScopedFindings(ordered[i], ordered[j]) < 0 })
	if _, err := fmt.Fprintln(w, "# Signal fidelity findings"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "\nUnexempted contradictions fail the fidelity command; explicit exemptions remain visible in Contradictions. Coverage gaps are report-only and do not fail the command."); err != nil {
		return err
	}
	if len(ordered) == 0 {
		_, err := fmt.Fprintln(w, "\nNo findings. The loaded corpus produced no confirmed contradictions or coverage gaps.")
		return err
	}
	if err := writeEvidenceLegend(w, ordered); err != nil {
		return err
	}

	if err := writeDispositionReport(w, "Contradictions", DispositionContradiction, ordered); err != nil {
		return err
	}
	if err := writeDispositionReport(w, "Coverage gaps", DispositionCoverageGap, ordered); err != nil {
		return err
	}
	return nil
}

func writeEvidenceLegend(w io.Writer, findings []ScopedFinding) error {
	kinds := make(map[string]struct{})
	for _, finding := range findings {
		kinds[finding.Source.Kind] = struct{}{}
	}
	if _, ok := kinds["gcx_live_readback"]; ok {
		if _, err := fmt.Fprintln(w, "\nEvidence scope: `gcx_live_readback` findings are live-read-only EKS evidence that the k3d lab cannot observe."); err != nil {
			return err
		}
	}
	if _, ok := kinds["k3d_lab"]; ok {
		if _, err := fmt.Fprintln(w, "Evidence scope: `k3d_lab` findings are k3d-covered evidence from the credential-free lab."); err != nil {
			return err
		}
	}
	return nil
}

func writeDispositionReport(w io.Writer, heading string, disposition Disposition, findings []ScopedFinding) error {
	classes := make(map[FindingKind][]groupedFindings)
	for _, group := range groupFindings(findings) {
		if group.representative.Finding.Disposition == disposition {
			classes[group.representative.Finding.Kind] = append(classes[group.representative.Finding.Kind], group)
		}
	}
	if _, err := fmt.Fprintf(w, "\n## %s\n", heading); err != nil {
		return err
	}
	if len(classes) == 0 {
		_, err := fmt.Fprintln(w, "No findings in this class.")
		return err
	}
	classNames := make([]string, 0, len(classes))
	for class := range classes {
		classNames = append(classNames, string(class))
	}
	sort.Strings(classNames)
	for _, className := range classNames {
		if _, err := fmt.Fprintf(w, "\n### %s\n", className); err != nil {
			return err
		}
		classFindings := classes[FindingKind(className)]
		sort.SliceStable(classFindings, func(i, j int) bool { return compareGroupedFindings(classFindings[i], classFindings[j]) < 0 })
		for _, group := range classFindings {
			if err := writeFinding(w, group); err != nil {
				return err
			}
		}
	}
	if disposition == DispositionCoverageGap {
		if err := writePendingStubs(w, groupFindings(findings)); err != nil {
			return err
		}
	}
	return nil
}

type findingGroupKey struct {
	disposition Disposition
	kind        FindingKind
	signal      string
	field       string
}

type groupedFindings struct {
	representative ScopedFinding
	findings       []ScopedFinding
}

func groupFindings(findings []ScopedFinding) []groupedFindings {
	byKey := make(map[findingGroupKey][]ScopedFinding)
	for _, finding := range findings {
		key := findingGroupKey{
			disposition: finding.Finding.Disposition,
			kind:        finding.Finding.Kind,
			signal:      finding.Finding.Signal,
			field:       finding.Finding.Field,
		}
		byKey[key] = append(byKey[key], finding)
	}

	groups := make([]groupedFindings, 0, len(byKey))
	for _, members := range byKey {
		sort.SliceStable(members, func(i, j int) bool { return compareScopedFindings(members[i], members[j]) < 0 })
		groups = append(groups, groupedFindings{representative: members[0], findings: members})
	}
	sort.SliceStable(groups, func(i, j int) bool { return compareGroupedFindings(groups[i], groups[j]) < 0 })
	return groups
}

func compareGroupedFindings(a, b groupedFindings) int {
	left, right := a.representative.Finding, b.representative.Finding
	for _, pair := range [][2]string{
		{string(left.Disposition), string(right.Disposition)},
		{string(left.Kind), string(right.Kind)},
		{left.Signal, right.Signal},
		{left.Field, right.Field},
	} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return compareScopedFindings(a.representative, b.representative)
}

func writeFinding(w io.Writer, group groupedFindings) error {
	scoped := group.representative
	paths := signalPaths(group.findings)
	var line string
	if len(group.findings) == 1 {
		finding := scoped.Finding
		line = fmt.Sprintf(
			"- `%s` — on substrate `%s` from generic source `%s`: signal `%s`, field `%s` (%s)",
			paths[0],
			scoped.Substrate,
			scoped.Source.Kind,
			finding.Signal,
			finding.Field,
			formatFindingValues(finding),
		)
	} else {
		line = fmt.Sprintf(
			"- %s — signal `%s`, field `%s`; evidence: %s",
			formatBacktickList(paths),
			scoped.Finding.Signal,
			scoped.Finding.Field,
			formatFindingEvidence(group.findings),
		)
	}
	matching, absent := groupedEvidenceSubstrates(group.findings)
	if len(matching) > 0 {
		line += fmt.Sprintf("; matching evidence on substrate(s) `%s`", strings.Join(matching, "`, `"))
	}
	if len(absent) > 0 {
		line += fmt.Sprintf("; absent evidence on substrate(s) `%s`", strings.Join(absent, "`, `"))
	}
	if exemptions := formatExemptions(group.findings); exemptions != "" {
		line += " " + exemptions
	}
	_, err := fmt.Fprintln(w, line+".")
	return err
}

func writePendingStubs(w io.Writer, findings []groupedFindings) error {
	bySignal := make(map[string][]ScopedFinding)
	for _, group := range findings {
		if group.representative.Finding.Disposition != DispositionCoverageGap {
			continue
		}
		bySignal[group.representative.Finding.Signal] = append(bySignal[group.representative.Finding.Signal], group.findings...)
	}
	if len(bySignal) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "\nCopy-pasteable cantfind.md PENDING stubs (Do not allocate an SK-N ID here):"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "```markdown"); err != nil {
		return err
	}
	signals := make([]string, 0, len(bySignal))
	for signal := range bySignal {
		signals = append(signals, signal)
	}
	sort.Strings(signals)
	for _, signal := range signals {
		if _, err := fmt.Fprintln(w, formatPendingStub(signal, bySignal[signal])); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w, "```")
	return err
}

func signalPaths(findings []ScopedFinding) []string {
	set := make(map[string]struct{})
	for _, scoped := range findings {
		set[signalPath(scoped.Area)] = struct{}{}
	}
	paths := make([]string, 0, len(set))
	for path := range set {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func formatBacktickList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, fmt.Sprintf("`%s`", value))
	}
	return strings.Join(quoted, ", ")
}

type evidencePair struct {
	substrate string
	source    string
}

type findingEvidence struct {
	substrate string
	source    string
	finding   Finding
}

func formatFindingEvidence(findings []ScopedFinding) string {
	entries := findingEvidenceEntries(findings)
	formatted := make([]string, 0, len(entries))
	for _, entry := range entries {
		formatted = append(formatted, fmt.Sprintf(
			"substrate `%s` from generic source `%s` (%s)",
			entry.substrate,
			entry.source,
			formatFindingValues(entry.finding),
		))
	}
	return strings.Join(formatted, "; ")
}

func findingEvidenceEntries(findings []ScopedFinding) []findingEvidence {
	entries := make([]findingEvidence, 0, len(findings))
	for _, scoped := range findings {
		candidate := findingEvidence{
			substrate: scoped.Substrate,
			source:    scoped.Source.Kind,
			finding:   scoped.Finding,
		}
		duplicate := false
		for _, existing := range entries {
			if sameFindingEvidence(existing, candidate) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			entries = append(entries, candidate)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		left, right := entries[i], entries[j]
		if left.substrate != right.substrate {
			return left.substrate < right.substrate
		}
		if left.source != right.source {
			return left.source < right.source
		}
		if cmp := compareStrings(left.finding.SynthValues, right.finding.SynthValues); cmp != 0 {
			return cmp < 0
		}
		return compareStrings(left.finding.RealityValues, right.finding.RealityValues) < 0
	})
	return entries
}

func sameFindingEvidence(left, right findingEvidence) bool {
	return left.substrate == right.substrate &&
		left.source == right.source &&
		compareStrings(left.finding.SynthValues, right.finding.SynthValues) == 0 &&
		compareStrings(left.finding.RealityValues, right.finding.RealityValues) == 0
}

func formatEvidencePairs(findings []ScopedFinding) string {
	pairs := evidencePairs(findings)
	formatted := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		formatted = append(formatted, fmt.Sprintf("substrate `%s` from generic source `%s`", pair.substrate, pair.source))
	}
	return strings.Join(formatted, "; ")
}

func groupedEvidenceSubstrates(findings []ScopedFinding) (matching, absent []string) {
	matchingSet := make(map[string]struct{})
	absentSet := make(map[string]struct{})
	for _, scoped := range findings {
		for _, substrate := range scoped.MatchingSubstrates {
			matchingSet[substrate] = struct{}{}
		}
		for _, substrate := range scoped.AbsentEvidenceSubstrates {
			absentSet[substrate] = struct{}{}
		}
	}
	return sortedStringSet(matchingSet), sortedStringSet(absentSet)
}

func formatExemptions(findings []ScopedFinding) string {
	type exemption struct {
		id, reason string
	}
	set := make(map[exemption]struct{})
	for _, scoped := range findings {
		if scoped.ExemptionID != "" {
			set[exemption{id: scoped.ExemptionID, reason: scoped.ExemptionReason}] = struct{}{}
		}
	}
	if len(set) == 0 {
		return ""
	}
	exemptions := make([]exemption, 0, len(set))
	for item := range set {
		exemptions = append(exemptions, item)
	}
	sort.Slice(exemptions, func(i, j int) bool {
		if exemptions[i].id != exemptions[j].id {
			return exemptions[i].id < exemptions[j].id
		}
		return exemptions[i].reason < exemptions[j].reason
	})
	parts := make([]string, 0, len(exemptions))
	for _, item := range exemptions {
		parts = append(parts, fmt.Sprintf("`%s` — %s", item.id, item.reason))
	}
	return fmt.Sprintf("[EXEMPTED: %s]", strings.Join(parts, "; "))
}

func formatPendingStub(signal string, findings []ScopedFinding) string {
	paths := signalPaths(findings)
	type pendingKey struct {
		kind  FindingKind
		field string
	}
	byField := make(map[pendingKey][]ScopedFinding)
	for _, scoped := range findings {
		key := pendingKey{kind: scoped.Finding.Kind, field: scoped.Finding.Field}
		byField[key] = append(byField[key], scoped)
	}
	keys := make([]pendingKey, 0, len(byField))
	for key := range byField {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].kind != keys[j].kind {
			return keys[i].kind < keys[j].kind
		}
		return keys[i].field < keys[j].field
	})
	pairs := evidencePairs(findings)
	if len(paths) == 1 && len(keys) == 1 && len(pairs) == 1 {
		pair := pairs[0]
		return fmt.Sprintf(
			"PENDING: confirm `%s` signal `%s` field `%s` from generic source `%s` on substrate `%s`; record the verified shape in the area catalogue.",
			paths[0], signal, keys[0].field, pair.source, pair.substrate,
		)
	}
	details := make([]string, 0, len(keys))
	for _, key := range keys {
		details = append(details, fmt.Sprintf(
			"class `%s` field `%s` on %s",
			key.kind,
			key.field,
			formatEvidencePairs(byField[key]),
		))
	}
	return fmt.Sprintf(
		"PENDING: confirm signal `%s`; %s; area(s) %s; record the verified shape in the area catalogue.",
		signal,
		strings.Join(details, "; "),
		formatBacktickList(paths),
	)
}

func evidencePairs(findings []ScopedFinding) []evidencePair {
	set := make(map[evidencePair]struct{})
	for _, scoped := range findings {
		set[evidencePair{substrate: scoped.Substrate, source: scoped.Source.Kind}] = struct{}{}
	}
	pairs := make([]evidencePair, 0, len(set))
	for pair := range set {
		pairs = append(pairs, pair)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].substrate != pairs[j].substrate {
			return pairs[i].substrate < pairs[j].substrate
		}
		return pairs[i].source < pairs[j].source
	})
	return pairs
}

// formatFindingValues leads with only the direction represented by the finding so a maintainer
// reads one verdict rather than seeing the same divergence repeated in both report sections. Both
// full sets still follow, unchanged, for context.
func formatFindingValues(finding Finding) string {
	parts := make([]string, 0, 4)
	if finding.Kind == KindUnknownInstrumentEvidence {
		parts = append(parts, "corpus did not observe an instrument type")
	}
	switch finding.Disposition {
	case DispositionContradiction:
		if onlySynth := difference(finding.SynthValues, finding.RealityValues); len(onlySynth) > 0 {
			parts = append(parts, "only-in-synth="+formatValues(onlySynth))
		}
	case DispositionCoverageGap:
		if onlySynth := difference(finding.SynthValues, finding.RealityValues); len(onlySynth) > 0 {
			parts = append(parts, "only-in-synth="+formatValues(onlySynth))
			if finding.Kind == KindLabelValueContradiction {
				parts = append(parts, "synth-only value has no closed-set evidence")
			}
		}
		if onlyReality := difference(finding.RealityValues, finding.SynthValues); len(onlyReality) > 0 {
			parts = append(parts, "only-in-reality="+formatValues(onlyReality))
		}
	}
	parts = append(parts, "synth="+formatValues(finding.SynthValues), "reality="+formatValues(finding.RealityValues))
	return strings.Join(parts, "; ")
}

func signalPath(area string) string {
	return "signals/" + strings.TrimSuffix(strings.TrimSpace(area), ".md") + ".md"
}

func formatValues(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	return "[" + strings.Join(values, ", ") + "]"
}
