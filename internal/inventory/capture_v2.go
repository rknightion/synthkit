// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const captureV2SchemaVersion = "2.0.0"

// CaptureV2PromotionSource supplies only the reviewed, generic provenance that cannot be
// recovered safely from a raw capture. In particular, scope is a semantic class, not a captured
// cluster or account value.
type CaptureV2PromotionSource struct {
	Area             string
	Kind             string
	Substrate        string
	Scope            string
	Collector        string
	CollectorVersion string
	CapturedOn       string
}

type captureV2 struct {
	SchemaVersion string `json:"schema_version"`
	Tool          struct {
		Version string `json:"version"`
	} `json:"tool"`
	Capture struct {
		Scope struct {
			IsScoped bool `json:"is_scoped"`
		} `json:"scope"`
		Limitations []struct {
			ID string `json:"id"`
		} `json:"limitations"`
	} `json:"capture"`
	Signals struct {
		Metrics struct {
			Families []captureV2Metric `json:"families"`
		} `json:"metrics"`
	} `json:"signals"`
}

type captureV2Metric struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	TypeSource string `json:"type_source"`
	Labels     []struct {
		Key string `json:"key"`
	} `json:"labels"`
	ClassicHistogram *struct {
		LEValues []string `json:"le_values"`
	} `json:"classic_histogram"`
	NativeHistogram json.RawMessage `json:"native_histogram"`
}

// ConvertCaptureV2 parses a schema-2 capture and returns its metrics as one generic,
// privacy-safe corpus document. It intentionally neither copies raw provenance nor derives an
// instrument from name_suffix_hint: type and type_source are the capture's direct evidence.
func ConvertCaptureV2(data []byte, source CaptureV2PromotionSource) (CorpusDocument, error) {
	var capture captureV2
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&capture); err != nil {
		return CorpusDocument{}, fmt.Errorf("decode schema-2 capture: %w", err)
	}
	if capture.SchemaVersion != captureV2SchemaVersion {
		return CorpusDocument{}, fmt.Errorf("capture schema_version: got %q, want %q", capture.SchemaVersion, captureV2SchemaVersion)
	}
	if capture.Tool.Version == "" {
		return CorpusDocument{}, fmt.Errorf("capture tool.version: must not be empty")
	}
	if len(capture.Signals.Metrics.Families) == 0 {
		return CorpusDocument{}, fmt.Errorf("capture metrics: must contain at least one family")
	}
	if source.Scope != "cluster" && source.Scope != "cloud" && source.Scope != "full" {
		return CorpusDocument{}, fmt.Errorf("promotion scope: must be cluster, cloud, or full, got %q", source.Scope)
	}
	if capture.Capture.Scope.IsScoped != (source.Scope != "full") {
		return CorpusDocument{}, fmt.Errorf("capture scope and reviewed promotion scope disagree")
	}

	schema := New()
	seen := make(map[string]struct{}, len(capture.Signals.Metrics.Families))
	for _, family := range capture.Signals.Metrics.Families {
		metric, err := convertCaptureV2Metric(family)
		if err != nil {
			return CorpusDocument{}, err
		}
		if _, exists := seen[metric.Name]; exists {
			return CorpusDocument{}, fmt.Errorf("capture metric %q: duplicate family name", metric.Name)
		}
		seen[metric.Name] = struct{}{}
		schema.Metrics = append(schema.Metrics, metric)
	}

	hash := sha256.Sum256(data)
	document := CorpusDocument{
		CorpusVersion: CorpusVersion,
		Area:          source.Area,
		Source: CorpusSource{
			Kind:                 source.Kind,
			Substrate:            source.Substrate,
			Collector:            source.Collector,
			CollectorRole:        CollectorRoleAudited,
			CollectorVersion:     source.CollectorVersion,
			CapturedOn:           source.CapturedOn,
			CaptureSchemaVersion: capture.SchemaVersion,
			CaptureToolVersion:   capture.Tool.Version,
			CaptureSHA256:        hex.EncodeToString(hash[:]),
			CaptureScope:         source.Scope,
			CaptureWarnings:      captureV2WarningIDs(capture.Capture.Limitations),
		},
		Authority:     CorpusAuthority{Substrates: []string{source.Substrate}},
		CaptureVolume: CaptureVolume{Runs: 1, ObservedContractCounts: []int{len(schema.Metrics)}},
		Inventory:     schema,
	}
	normalizeCorpusDocument(&document)
	if err := validateCorpusDocument(document); err != nil {
		return CorpusDocument{}, fmt.Errorf("convert schema-2 capture: %w", err)
	}
	return document, nil
}

func convertCaptureV2Metric(family captureV2Metric) (Metric, error) {
	if family.Name == "" {
		return Metric{}, fmt.Errorf("capture metric: name must not be empty")
	}
	if !knownInstrumentType(family.Type) {
		return Metric{}, fmt.Errorf("capture metric %q: unsupported type %q", family.Name, family.Type)
	}
	if family.TypeSource != "undetermined" && family.TypeSource != "observed" && family.TypeSource != "metadata" {
		return Metric{}, fmt.Errorf("capture metric %q: unsupported type_source %q", family.Name, family.TypeSource)
	}
	if family.Type == InstrumentUnknown && family.TypeSource != "undetermined" && family.TypeSource != "metadata" {
		return Metric{}, fmt.Errorf("capture metric %q: unknown type must be undetermined or metadata", family.Name)
	}
	metric := Metric{
		Name:                 family.Name,
		Transports:           []string{},
		InstrumentTypes:      []string{family.Type},
		InstrumentTypeSource: family.TypeSource,
		Labels:               []Attribute{},
	}
	for _, label := range family.Labels {
		if label.Key == "" || strings.HasPrefix(label.Key, "tag_") {
			continue
		}
		metric.Labels = append(metric.Labels, Attribute{Key: label.Key, Values: []string{}, ValuesElided: true})
	}
	if family.ClassicHistogram != nil {
		bounds, err := captureV2Bounds(family.ClassicHistogram.LEValues)
		if err != nil {
			return Metric{}, fmt.Errorf("capture metric %q: %w", family.Name, err)
		}
		// The schema-2 capture records the classic histogram's `le` label structurally
		// in classic_histogram rather than in the ordinary label list. Preserve the key
		// for label-shape comparison without reintroducing its elided values.
		metric.Labels = append(metric.Labels, Attribute{Key: BucketBoundLabel, Values: []string{}, ValuesElided: true})
		metric.Histogram = &Histogram{Classic: true, BucketBounds: bounds, NativeSchemas: []int32{}}
	}
	if len(family.NativeHistogram) > 0 && string(family.NativeHistogram) != "null" {
		if metric.Histogram == nil {
			metric.Histogram = &Histogram{BucketBounds: []float64{}, NativeSchemas: []int32{}}
		}
		metric.Histogram.Native = true
	}
	return metric, nil
}

func knownInstrumentType(value string) bool {
	return value == InstrumentUnknown || value == InstrumentGauge || value == InstrumentCounter || value == InstrumentHistogram || value == InstrumentSummary
}

func captureV2Bounds(values []string) ([]float64, error) {
	bounds := make([]float64, 0, len(values))
	for _, value := range values {
		if value == "+Inf" {
			continue
		}
		bound, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid classic histogram bound %q: %w", value, err)
		}
		bounds = append(bounds, bound)
	}
	sort.Float64s(bounds)
	return bounds, nil
}

func captureV2WarningIDs(limitations []struct {
	ID string `json:"id"`
}) []string {
	ids := make([]string, 0, len(limitations))
	for _, limitation := range limitations {
		if limitation.ID != "" {
			ids = append(ids, limitation.ID)
		}
	}
	sort.Strings(ids)
	return compactStrings(ids)
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
