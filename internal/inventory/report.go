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
	classes := make(map[FindingKind][]ScopedFinding)
	for _, finding := range findings {
		if finding.Finding.Disposition == disposition {
			classes[finding.Finding.Kind] = append(classes[finding.Finding.Kind], finding)
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
		sort.SliceStable(classFindings, func(i, j int) bool { return compareScopedFindings(classFindings[i], classFindings[j]) < 0 })
		for _, finding := range classFindings {
			if err := writeFinding(w, finding); err != nil {
				return err
			}
		}
		if disposition == DispositionCoverageGap {
			if err := writePendingStubs(w, classFindings); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeFinding(w io.Writer, scoped ScopedFinding) error {
	finding := scoped.Finding
	signalPath := signalPath(scoped.Area)
	values := formatFindingValues(finding)
	line := fmt.Sprintf(
		"- `%s` — on substrate `%s` from generic source `%s`: signal `%s`, field `%s` (%s)",
		signalPath,
		scoped.Substrate,
		scoped.Source.Kind,
		finding.Signal,
		finding.Field,
		values,
	)
	if scoped.ExemptionID != "" {
		line += fmt.Sprintf(" [EXEMPTED: `%s` — %s]", scoped.ExemptionID, scoped.ExemptionReason)
	}
	_, err := fmt.Fprintln(w, line+".")
	return err
}

func writePendingStubs(w io.Writer, findings []ScopedFinding) error {
	if _, err := fmt.Fprintln(w, "\nCopy-pasteable cantfind.md PENDING stubs (Do not allocate an SK-N ID here):"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "```markdown"); err != nil {
		return err
	}
	for _, scoped := range findings {
		finding := scoped.Finding
		if _, err := fmt.Fprintf(
			w,
			"PENDING: confirm `%s` signal `%s` field `%s` from generic source `%s` on substrate `%s`; record the verified shape in the area catalogue.\n",
			signalPath(scoped.Area), finding.Signal, finding.Field, scoped.Source.Kind, scoped.Substrate,
		); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w, "```")
	return err
}

// formatFindingValues leads with only the direction represented by the finding so a maintainer
// reads one verdict rather than seeing the same divergence repeated in both report sections. Both
// full sets still follow, unchanged, for context.
func formatFindingValues(finding Finding) string {
	parts := make([]string, 0, 4)
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
