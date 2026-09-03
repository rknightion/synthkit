// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CaptureV2RoutingManifestVersion identifies the reviewed, hash-keyed routing
// contract used to promote immutable schema-2 captures.
const CaptureV2RoutingManifestVersion = "synthkit.telemetry.capture-v2-routing/v1alpha1"

// CaptureV2RoutingManifest contains every reviewed canonical capture route.
// A route is keyed by raw capture hash and family identity; ProjectCaptureV2
// intentionally has no name, prefix, label-value, or source-order fallback.
type CaptureV2RoutingManifest struct {
	Version  string                  `json:"version"`
	Captures []CaptureV2CaptureRoute `json:"captures"`
}

// CaptureV2CaptureRoute supplies the generic source provenance shared by all
// family routes in one immutable capture.
type CaptureV2CaptureRoute struct {
	SHA256              string                    `json:"sha256"`
	Kind                string                    `json:"kind"`
	Substrate           string                    `json:"substrate"`
	Scope               string                    `json:"scope"`
	Collector           string                    `json:"collector"`
	CollectorVersion    string                    `json:"collector_version"`
	CapturedOn          string                    `json:"captured_on"`
	MetricProducerLabel string                    `json:"metric_producer_label"`
	Families            []CaptureV2FamilyRoute    `json:"families"`
	Unrouted            []CaptureV2UnroutedFamily `json:"unrouted"`
}

// CaptureV2FamilyRoute is the reviewed identity and signals-area ownership of
// one exact captured metric family. Producers are explicit rather than copied
// from a label value during privacy promotion.
type CaptureV2FamilyRoute struct {
	Name      string     `json:"name"`
	Area      string     `json:"area"`
	Producers []Producer `json:"producers"`
}

// CaptureV2UnroutedReason describes why a producerless family is intentionally
// excluded from the privacy-safe corpus. It is evidence, not a routing fallback.
type CaptureV2UnroutedReason string

const (
	CaptureV2UnroutedMissingProducerAndArea    CaptureV2UnroutedReason = "missing_producer_and_area"
	CaptureV2UnroutedMissingProducerUniqueArea CaptureV2UnroutedReason = "missing_producer_unique_exact_area"
)

// CaptureV2UnroutedFamily is one exact captured family that cannot be compared
// until its producer identity is reviewed. The raw capture remains the source
// of truth; this record only makes the residue visible and fail-closed.
type CaptureV2UnroutedFamily struct {
	Name   string                  `json:"name"`
	Reason CaptureV2UnroutedReason `json:"reason"`
	// Area is retained only where exact machine-readable catalogue membership
	// established one. It never licenses promotion without a producer.
	Area string `json:"area,omitempty"`
}

// CaptureV2Projection separates producer-admissible corpus documents from
// producerless residue. Consumers must use DocumentForFamily rather than
// treating an unrouted family as absent or applying a name-derived fallback.
type CaptureV2Projection struct {
	Documents []CorpusDocument          `json:"documents"`
	Unrouted  []CaptureV2UnroutedFamily `json:"unrouted"`
}

// DocumentForFamily returns the promoted corpus document for one exact family.
// An unrouted family is an explicit comparison error, never absent evidence.
func (projection CaptureV2Projection) DocumentForFamily(name string) (CorpusDocument, error) {
	for _, family := range projection.Unrouted {
		if family.Name == name {
			return CorpusDocument{}, fmt.Errorf("capture metric %q is unrouted: %s", name, family.Reason)
		}
	}
	for _, document := range projection.Documents {
		for _, metric := range document.Inventory.Metrics {
			if metric.Name == name {
				return document, nil
			}
		}
	}
	return CorpusDocument{}, fmt.Errorf("capture metric %q is not present in this projection", name)
}

// ProjectCaptureV2 projects one immutable schema-2 capture into one corpus
// document per explicitly-reviewed signals area, plus explicit producerless
// residue. It fails closed when the hash is absent, direct identity has no
// route, producerless identity has no exact reason, or reviewed producer
// identity disagrees with the capture's direct producer label.
func ProjectCaptureV2(data []byte, manifest CaptureV2RoutingManifest) (CaptureV2Projection, error) {
	if err := validateCaptureV2RoutingManifest(manifest); err != nil {
		return CaptureV2Projection{}, err
	}

	var capture captureV2
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&capture); err != nil {
		return CaptureV2Projection{}, fmt.Errorf("decode schema-2 capture: %w", err)
	}
	if capture.SchemaVersion != captureV2SchemaVersion {
		return CaptureV2Projection{}, fmt.Errorf("capture schema_version: got %q, want %q", capture.SchemaVersion, captureV2SchemaVersion)
	}
	if capture.Tool.Version == "" {
		return CaptureV2Projection{}, fmt.Errorf("capture tool.version: must not be empty")
	}
	if len(capture.Signals.Metrics.Families) == 0 {
		return CaptureV2Projection{}, fmt.Errorf("capture metrics: must contain at least one family")
	}

	hash := captureV2SHA256(data)
	route, ok := captureV2CaptureRouteByHash(manifest, hash)
	if !ok {
		return CaptureV2Projection{}, fmt.Errorf("capture sha256 %q: no reviewed capture route", hash)
	}
	if capture.Capture.Scope.IsScoped != (route.Scope != "full") {
		return CaptureV2Projection{}, fmt.Errorf("capture scope and reviewed routing scope disagree")
	}

	families := make(map[string]CaptureV2FamilyRoute, len(route.Families))
	for _, family := range route.Families {
		families[family.Name] = family
	}
	unrouted := make(map[string]CaptureV2UnroutedFamily, len(route.Unrouted))
	for _, family := range route.Unrouted {
		unrouted[family.Name] = family
	}
	documents := make(map[string]*CorpusDocument)
	projection := CaptureV2Projection{Documents: []CorpusDocument{}, Unrouted: []CaptureV2UnroutedFamily{}}
	seen := make(map[string]struct{}, len(capture.Signals.Metrics.Families))
	for _, captured := range capture.Signals.Metrics.Families {
		if _, exists := seen[captured.Name]; exists {
			return CaptureV2Projection{}, fmt.Errorf("capture metric %q: duplicate family name", captured.Name)
		}
		seen[captured.Name] = struct{}{}
		hasDirectProducer := captureV2MetricHasDirectProducer(captured, route.MetricProducerLabel)
		family, routed := families[captured.Name]
		residue, recordedUnrouted := unrouted[captured.Name]
		if hasDirectProducer {
			if !routed || recordedUnrouted {
				return CaptureV2Projection{}, fmt.Errorf("capture metric %q: direct producer identity requires one reviewed direct route", captured.Name)
			}
		} else {
			if routed || !recordedUnrouted {
				return CaptureV2Projection{}, fmt.Errorf("capture metric %q: no reviewed direct route or explicit unrouted reason", captured.Name)
			}
			projection.Unrouted = append(projection.Unrouted, residue)
			continue
		}
		metric, err := convertCaptureV2Metric(captured, route.MetricProducerLabel, "")
		if err != nil {
			return CaptureV2Projection{}, err
		}
		if !sameProducers(metric.Producers, family.Producers) {
			return CaptureV2Projection{}, fmt.Errorf("capture metric %q: reviewed producers do not match direct capture identity", captured.Name)
		}
		metric.Producers = append([]Producer(nil), family.Producers...)

		document := documents[family.Area]
		if document == nil {
			document = newCaptureV2ProjectionDocument(capture, hash, route, family.Area)
			documents[family.Area] = document
		}
		document.Inventory.Metrics = append(document.Inventory.Metrics, metric)
	}
	for _, family := range route.Families {
		if _, ok := seen[family.Name]; !ok {
			return CaptureV2Projection{}, fmt.Errorf("capture sha256 %q: reviewed family %q is absent", hash, family.Name)
		}
	}
	for _, family := range route.Unrouted {
		if _, ok := seen[family.Name]; !ok {
			return CaptureV2Projection{}, fmt.Errorf("capture sha256 %q: unrouted family %q is absent", hash, family.Name)
		}
	}

	out := make([]CorpusDocument, 0, len(documents))
	for _, document := range documents {
		document.CaptureVolume.ObservedContractCounts = []int{len(document.Inventory.Metrics)}
		normalizeCorpusDocument(document)
		if err := validateCorpusDocument(*document); err != nil {
			return CaptureV2Projection{}, fmt.Errorf("project schema-2 capture: %w", err)
		}
		out = append(out, *document)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Area < out[j].Area })
	projection.Documents = out
	sort.Slice(projection.Unrouted, func(i, j int) bool { return projection.Unrouted[i].Name < projection.Unrouted[j].Name })
	return projection, nil
}

func newCaptureV2ProjectionDocument(capture captureV2, hash string, route CaptureV2CaptureRoute, area string) *CorpusDocument {
	return &CorpusDocument{
		CorpusVersion: CorpusVersion,
		Area:          area,
		Source: CorpusSource{
			Kind:                   route.Kind,
			Substrate:              route.Substrate,
			Collector:              route.Collector,
			CollectorRole:          CollectorRoleAudited,
			CollectorVersion:       route.CollectorVersion,
			CapturedOn:             route.CapturedOn,
			CaptureSchemaVersion:   capture.SchemaVersion,
			CaptureToolVersion:     capture.Tool.Version,
			CaptureSHA256:          hash,
			CaptureScope:           route.Scope,
			CaptureWarnings:        captureV2WarningIDs(capture.Capture.Limitations),
			CaptureDurationSeconds: capture.Provenance.CaptureDurationSeconds,
			CaptureWindow:          capture.Provenance.Window.Duration,
			CaptureSoakDuration:    capture.Provenance.Estate.Load.SoakDuration,
			CaptureLoadDriven:      capture.Provenance.Estate.Load.LoadDriven,
		},
		Authority: CorpusAuthority{Substrates: []string{route.Substrate}},
		CaptureVolume: CaptureVolume{
			Runs:                   1,
			ObservedContractCounts: []int{},
		},
		Inventory: New(),
	}
}

func validateCaptureV2RoutingManifest(manifest CaptureV2RoutingManifest) error {
	if manifest.Version != CaptureV2RoutingManifestVersion {
		return fmt.Errorf("capture routing manifest version: got %q, want %q", manifest.Version, CaptureV2RoutingManifestVersion)
	}
	if len(manifest.Captures) == 0 {
		return fmt.Errorf("capture routing manifest: must contain at least one capture")
	}
	seenCaptures := make(map[string]struct{}, len(manifest.Captures))
	for _, capture := range manifest.Captures {
		if len(capture.SHA256) != sha256.Size*2 {
			return fmt.Errorf("capture routing sha256 %q: must be a SHA-256 hex string", capture.SHA256)
		}
		if _, err := hex.DecodeString(capture.SHA256); err != nil {
			return fmt.Errorf("capture routing sha256 %q: %w", capture.SHA256, err)
		}
		if _, exists := seenCaptures[capture.SHA256]; exists {
			return fmt.Errorf("capture routing sha256 %q: duplicate capture route", capture.SHA256)
		}
		seenCaptures[capture.SHA256] = struct{}{}
		for _, field := range []struct{ name, value string }{
			{"kind", capture.Kind}, {"substrate", capture.Substrate}, {"collector", capture.Collector},
			{"collector_version", capture.CollectorVersion}, {"captured_on", capture.CapturedOn},
			{"metric_producer_label", capture.MetricProducerLabel},
		} {
			if strings.TrimSpace(field.value) == "" {
				return fmt.Errorf("capture routing sha256 %q: %s must not be empty", capture.SHA256, field.name)
			}
		}
		if capture.Scope != "cluster" && capture.Scope != "cloud" && capture.Scope != "full" {
			return fmt.Errorf("capture routing sha256 %q: scope must be cluster, cloud, or full", capture.SHA256)
		}
		if len(capture.Families) == 0 && len(capture.Unrouted) == 0 {
			return fmt.Errorf("capture routing sha256 %q: must contain at least one direct route or unrouted family", capture.SHA256)
		}
		seenFamilies := make(map[string]struct{}, len(capture.Families))
		for _, family := range capture.Families {
			if strings.TrimSpace(family.Name) == "" {
				return fmt.Errorf("capture routing sha256 %q: family name must not be empty", capture.SHA256)
			}
			if _, exists := seenFamilies[family.Name]; exists {
				return fmt.Errorf("capture routing sha256 %q: family %q has duplicate routes", capture.SHA256, family.Name)
			}
			seenFamilies[family.Name] = struct{}{}
			if _, ok := allowedCorpusAreas[family.Area]; !ok {
				return fmt.Errorf("capture routing sha256 %q: family %q area %q is not allowed", capture.SHA256, family.Name, family.Area)
			}
			if len(family.Producers) == 0 {
				return fmt.Errorf("capture routing sha256 %q: family %q must declare producers", capture.SHA256, family.Name)
			}
			for _, producer := range family.Producers {
				if strings.TrimSpace(producer.Name) == "" || producer.AllowListVersion != "" || producer.AllowListVariant != "" {
					return fmt.Errorf("capture routing sha256 %q: family %q has invalid producer identity", capture.SHA256, family.Name)
				}
			}
		}
		seenUnrouted := make(map[string]struct{}, len(capture.Unrouted))
		for _, family := range capture.Unrouted {
			if strings.TrimSpace(family.Name) == "" {
				return fmt.Errorf("capture routing sha256 %q: unrouted family name must not be empty", capture.SHA256)
			}
			if _, exists := seenFamilies[family.Name]; exists {
				return fmt.Errorf("capture routing sha256 %q: family %q is both directly routed and unrouted", capture.SHA256, family.Name)
			}
			if _, exists := seenUnrouted[family.Name]; exists {
				return fmt.Errorf("capture routing sha256 %q: unrouted family %q is duplicated", capture.SHA256, family.Name)
			}
			seenUnrouted[family.Name] = struct{}{}
			if family.Reason != CaptureV2UnroutedMissingProducerAndArea && family.Reason != CaptureV2UnroutedMissingProducerUniqueArea {
				return fmt.Errorf("capture routing sha256 %q: unrouted family %q has invalid reason %q", capture.SHA256, family.Name, family.Reason)
			}
			if family.Reason == CaptureV2UnroutedMissingProducerUniqueArea {
				if _, ok := allowedCorpusAreas[family.Area]; !ok {
					return fmt.Errorf("capture routing sha256 %q: unrouted family %q needs one allowed exact area", capture.SHA256, family.Name)
				}
			} else if family.Area != "" {
				return fmt.Errorf("capture routing sha256 %q: unrouted family %q must not claim an area", capture.SHA256, family.Name)
			}
		}
	}
	return nil
}

func captureV2CaptureRouteByHash(manifest CaptureV2RoutingManifest, hash string) (CaptureV2CaptureRoute, bool) {
	for _, route := range manifest.Captures {
		if route.SHA256 == hash {
			return route, true
		}
	}
	return CaptureV2CaptureRoute{}, false
}

func captureV2SHA256(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func captureV2MetricHasDirectProducer(family captureV2Metric, producerLabel string) bool {
	for _, label := range family.Labels {
		if label.Key != producerLabel {
			continue
		}
		for _, value := range label.Values {
			if strings.TrimSpace(value) != "" {
				return true
			}
		}
	}
	return false
}

func sameProducers(left, right []Producer) bool {
	left = append([]Producer(nil), left...)
	right = append([]Producer(nil), right...)
	normalizeProducers(&left)
	normalizeProducers(&right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
