// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

const ProducerCoverageVersion = "synthkit.telemetry.producer-coverage/v1alpha1"

// ProducerCoverageRatchet bounds unresolved producer claims, without exempting them.
type ProducerCoverageRatchet struct {
	Version       string `json:"version"`
	ExpectedCount *int   `json:"expected_count"`
}

func DecodeProducerCoverageRatchet(data []byte) (ProducerCoverageRatchet, error) {
	var result ProducerCoverageRatchet
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return result, fmt.Errorf("producer coverage requires a JSON object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return result, err
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return result, fmt.Errorf("producer coverage duplicate or invalid field %q", key)
		}
		seen[name] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return result, err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return result, err
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return result, err
	}
	if result.Version != ProducerCoverageVersion || result.ExpectedCount == nil || *result.ExpectedCount < 0 {
		return result, fmt.Errorf("producer coverage requires version %q and nonnegative expected_count", ProducerCoverageVersion)
	}
	return result, nil
}

func LoadProducerCoverageRatchet(path string) (ProducerCoverageRatchet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ProducerCoverageRatchet{}, err
	}
	return DecodeProducerCoverageRatchet(data)
}

func (r ProducerCoverageRatchet) Check(count int) error {
	if r.ExpectedCount == nil {
		return fmt.Errorf("producer coverage expected_count is required")
	}
	if count > *r.ExpectedCount {
		return fmt.Errorf("no-comparable-producer count grew: observed=%d expected=%d", count, *r.ExpectedCount)
	}
	return nil
}

func CountNoComparableProducers(findings []ScopedFinding) int {
	count := 0
	for _, f := range findings {
		if f.Finding.Kind == KindNoComparableProducer {
			count++
		}
	}
	return count
}
