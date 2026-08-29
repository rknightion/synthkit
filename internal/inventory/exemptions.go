// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// ContradictionExemptionsVersion is the version of the explicit contradiction
// exemption document. Exemptions are versioned separately from the reality
// corpus because they are review decisions, not observations.
const ContradictionExemptionsVersion = "synthkit.telemetry.contradiction-exemptions/v1alpha1"

// ContradictionExemptionVersion is the singular spelling retained as a
// convenience for callers that refer to one exemption document.
const ContradictionExemptionVersion = ContradictionExemptionsVersion

// ContradictionExemptionDocument is the strict, versioned envelope for
// deliberate contradiction exemptions.
type ContradictionExemptionDocument struct {
	Version    string                   `json:"version"`
	Exemptions []ContradictionExemption `json:"exemptions"`
}

// ContradictionExemptionsDocument is an alias for callers that use the plural
// name for the document rather than the list it contains.
type ContradictionExemptionsDocument = ContradictionExemptionDocument

// ContradictionExemption identifies the exact contradiction(s) a reviewer has
// decided to tolerate. The rule never changes a finding's disposition and its
// expected count makes a stale rule fail closed when the corpus changes.
type ContradictionExemption struct {
	ID              string      `json:"id"`
	Reason          string      `json:"reason"`
	Area            string      `json:"area"`
	SourceKind      string      `json:"source_kind"`
	Substrate       string      `json:"substrate"`
	FindingKind     FindingKind `json:"finding_kind"`
	Field           string      `json:"field"`
	Signal          string      `json:"signal,omitempty"`
	SignalPrefix    string      `json:"signal_prefix,omitempty"`
	OnlyInSynth     []string    `json:"only_in_synth"`
	ExpectedMatches int         `json:"expected_matches"`
}

// LoadContradictionExemptions loads and strictly validates a versioned
// exemption document. A missing path is an error; callers that intentionally
// make the document optional must handle os.IsNotExist themselves.
func LoadContradictionExemptions(path string) ([]ContradictionExemption, error) {
	document, err := LoadContradictionExemptionDocument(path)
	if err != nil {
		return nil, err
	}
	return document.Exemptions, nil
}

// LoadContradictionExemptionDocument loads and strictly validates the complete
// exemption document, including its version envelope.
func LoadContradictionExemptionDocument(path string) (ContradictionExemptionDocument, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ContradictionExemptionDocument{}, fmt.Errorf("%s: read contradiction exemptions: %w", path, err)
	}
	document, err := DecodeContradictionExemptions(data)
	if err != nil {
		return ContradictionExemptionDocument{}, fmt.Errorf("%s: %w", path, err)
	}
	return document, nil
}

// DecodeContradictionExemptions strictly decodes and validates one JSON
// exemption document. It is useful to callers that already own the bytes and
// follows the same single-document rule as the corpus loader.
func DecodeContradictionExemptions(data []byte) (ContradictionExemptionDocument, error) {
	var raw map[string]json.RawMessage
	rawDecoder := json.NewDecoder(bytes.NewReader(data))
	if err := rawDecoder.Decode(&raw); err != nil {
		return ContradictionExemptionDocument{}, fmt.Errorf("invalid JSON: %w", err)
	}
	if err := requireJSONEOF(rawDecoder); err != nil {
		return ContradictionExemptionDocument{}, fmt.Errorf("invalid JSON: %w", err)
	}
	if raw == nil {
		return ContradictionExemptionDocument{}, errors.New("document must be a JSON object")
	}
	for _, field := range []string{"version", "exemptions"} {
		value, ok := raw[field]
		if !ok {
			return ContradictionExemptionDocument{}, fmt.Errorf("missing required field %s", field)
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ContradictionExemptionDocument{}, fmt.Errorf("required field %s must not be null", field)
		}
	}
	if value := bytes.TrimSpace(raw["exemptions"]); len(value) > 0 && value[0] != '[' {
		return ContradictionExemptionDocument{}, errors.New("field exemptions must be a JSON array")
	}
	var rawExemptions []map[string]json.RawMessage
	if err := json.Unmarshal(raw["exemptions"], &rawExemptions); err != nil {
		return ContradictionExemptionDocument{}, fmt.Errorf("field exemptions: %w", err)
	}
	for i, rawExemption := range rawExemptions {
		_, hasSignal := rawExemption["signal"]
		_, hasSignalPrefix := rawExemption["signal_prefix"]
		if hasSignal && hasSignalPrefix {
			return ContradictionExemptionDocument{}, fmt.Errorf("exemptions[%d]: signal and signal_prefix are mutually exclusive", i)
		}
		for _, selector := range []string{"signal", "signal_prefix"} {
			rawValue, present := rawExemption[selector]
			if !present {
				continue
			}
			var value *string
			if err := json.Unmarshal(rawValue, &value); err != nil {
				return ContradictionExemptionDocument{}, fmt.Errorf("exemptions[%d].%s: must be a string when present", i, selector)
			}
			if value == nil || strings.TrimSpace(*value) == "" {
				return ContradictionExemptionDocument{}, fmt.Errorf("exemptions[%d].%s: must not be empty when present", i, selector)
			}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document ContradictionExemptionDocument
	if err := decoder.Decode(&document); err != nil {
		return ContradictionExemptionDocument{}, fmt.Errorf("decode document: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return ContradictionExemptionDocument{}, err
	}
	if err := ValidateContradictionExemptionDocument(document); err != nil {
		return ContradictionExemptionDocument{}, err
	}
	return document, nil
}

// ValidateContradictionExemptionDocument checks the version, required list,
// unique IDs, and every rule in a document.
func ValidateContradictionExemptionDocument(document ContradictionExemptionDocument) error {
	if document.Version != ContradictionExemptionsVersion {
		return fmt.Errorf("version: got %q, want %q", document.Version, ContradictionExemptionsVersion)
	}
	if document.Exemptions == nil {
		return errors.New("exemptions: must be present as an array")
	}
	return validateContradictionExemptions(document.Exemptions)
}

// ValidateContradictionExemptions validates a list for use with
// ApplyContradictionExemptions. An empty or nil list is valid: it means there
// are no explicit decisions to apply.
func ValidateContradictionExemptions(exemptions []ContradictionExemption) error {
	return validateContradictionExemptions(exemptions)
}

func validateContradictionExemptions(exemptions []ContradictionExemption) error {
	seenIDs := make(map[string]struct{}, len(exemptions))
	for i, exemption := range exemptions {
		if err := ValidateContradictionExemption(exemption); err != nil {
			return fmt.Errorf("exemptions[%d]: %w", i, err)
		}
		if _, exists := seenIDs[exemption.ID]; exists {
			return fmt.Errorf("exemptions[%d].id: duplicate id %q", i, exemption.ID)
		}
		seenIDs[exemption.ID] = struct{}{}
	}
	return nil
}

// ValidateContradictionExemption checks one explicit contradiction rule.
func ValidateContradictionExemption(exemption ContradictionExemption) error {
	for _, required := range []struct {
		field string
		value string
	}{
		{field: "id", value: exemption.ID},
		{field: "reason", value: exemption.Reason},
		{field: "area", value: exemption.Area},
		{field: "source_kind", value: exemption.SourceKind},
		{field: "substrate", value: exemption.Substrate},
		{field: "finding_kind", value: string(exemption.FindingKind)},
		{field: "field", value: exemption.Field},
	} {
		value, field := required.value, required.field
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s: must not be empty", field)
		}
	}
	if exemption.Signal != "" && strings.TrimSpace(exemption.Signal) == "" {
		return errors.New("signal: must not be blank when present")
	}
	if exemption.SignalPrefix != "" && strings.TrimSpace(exemption.SignalPrefix) == "" {
		return errors.New("signal_prefix: must not be blank when present")
	}
	if exemption.Signal != "" && exemption.SignalPrefix != "" {
		return errors.New("signal and signal_prefix are mutually exclusive")
	}
	if exemption.Signal == "" && exemption.SignalPrefix == "" {
		return errors.New("exactly one of signal or signal_prefix is required")
	}
	if len(exemption.OnlyInSynth) == 0 {
		return errors.New("only_in_synth: must contain at least one value")
	}
	for i, value := range exemption.OnlyInSynth {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("only_in_synth[%d]: must not be empty", i)
		}
		if i > 0 && exemption.OnlyInSynth[i-1] >= value {
			if exemption.OnlyInSynth[i-1] == value {
				return fmt.Errorf("only_in_synth[%d]: duplicate value %q", i, value)
			}
			return errors.New("only_in_synth: values must be sorted lexicographically")
		}
	}
	if exemption.ExpectedMatches <= 0 {
		return errors.New("expected_matches: must be greater than zero")
	}
	return nil
}

// ApplyContradictionExemptions marks matching contradiction findings in place.
// It is atomic: validation, overlap detection, and expected-count checks all
// complete before any finding receives exemption metadata. Findings are never
// removed or reclassified.
func ApplyContradictionExemptions(findings []ScopedFinding, exemptions []ContradictionExemption) error {
	if err := validateContradictionExemptions(exemptions); err != nil {
		return err
	}

	matchedCounts := make([]int, len(exemptions))
	type assignment struct {
		findingIndex   int
		exemptionIndex int
	}
	assignments := make([]assignment, 0)
	for findingIndex, finding := range findings {
		if finding.Finding.Disposition != DispositionContradiction {
			continue
		}
		matched := make([]int, 0, 1)
		for exemptionIndex, exemption := range exemptions {
			if contradictionExemptionMatches(finding, exemption) {
				matched = append(matched, exemptionIndex)
			}
		}
		if len(matched) > 1 {
			return fmt.Errorf("finding %q field %q matches multiple contradiction exemptions %q", finding.Finding.Signal, finding.Finding.Field, exemptionIDs(exemptions, matched))
		}
		if len(matched) == 1 {
			matchedCounts[matched[0]]++
			assignments = append(assignments, assignment{findingIndex: findingIndex, exemptionIndex: matched[0]})
		}
	}
	for i, exemption := range exemptions {
		if matchedCounts[i] != exemption.ExpectedMatches {
			return fmt.Errorf("contradiction exemption %q expected_matches=%d but matched %d findings", exemption.ID, exemption.ExpectedMatches, matchedCounts[i])
		}
	}
	for _, assignment := range assignments {
		finding := &findings[assignment.findingIndex]
		exemption := exemptions[assignment.exemptionIndex]
		finding.ExemptionID = exemption.ID
		finding.ExemptionReason = exemption.Reason
	}
	return nil
}

func contradictionExemptionMatches(finding ScopedFinding, exemption ContradictionExemption) bool {
	if finding.Area != exemption.Area ||
		finding.Source.Kind != exemption.SourceKind ||
		finding.Substrate != exemption.Substrate ||
		finding.Finding.Kind != exemption.FindingKind ||
		finding.Finding.Field != exemption.Field {
		return false
	}
	if exemption.Signal != "" && finding.Finding.Signal != exemption.Signal {
		return false
	}
	if exemption.SignalPrefix != "" && !strings.HasPrefix(finding.Finding.Signal, exemption.SignalPrefix) {
		return false
	}

	onlyInSynth := difference(finding.Finding.SynthValues, finding.Finding.RealityValues)
	sort.Strings(onlyInSynth)
	return equalStrings(onlyInSynth, exemption.OnlyInSynth)
}

func exemptionIDs(exemptions []ContradictionExemption, indexes []int) []string {
	ids := make([]string, 0, len(indexes))
	for _, index := range indexes {
		ids = append(ids, exemptions[index].ID)
	}
	sort.Strings(ids)
	return ids
}

// CountUnexemptedContradictions counts contradiction findings that do not have
// an explicit exemption ID. Coverage gaps and already-exempted contradictions
// are excluded.
func CountUnexemptedContradictions(findings []ScopedFinding) int {
	count := 0
	for _, finding := range findings {
		if finding.Finding.Disposition == DispositionContradiction && finding.ExemptionID == "" {
			count++
		}
	}
	return count
}
