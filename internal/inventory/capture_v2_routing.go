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
	SHA256              string                 `json:"sha256"`
	Kind                string                 `json:"kind"`
	Substrate           string                 `json:"substrate"`
	Scope               string                 `json:"scope"`
	Collector           string                 `json:"collector"`
	CollectorVersion    string                 `json:"collector_version"`
	CapturedOn          string                 `json:"captured_on"`
	MetricProducerLabel string                 `json:"metric_producer_label"`
	Families            []CaptureV2FamilyRoute `json:"families"`
}

// CaptureV2FamilyRoute is the reviewed identity and signals-area ownership of
// one exact captured metric family. Producers are explicit rather than copied
// from a label value during privacy promotion.
type CaptureV2FamilyRoute struct {
	Name      string     `json:"name"`
	Area      string     `json:"area"`
	Producers []Producer `json:"producers"`
}

// ProjectCaptureV2 projects one immutable schema-2 capture into one corpus
// document per explicitly-reviewed signals area. It fails closed when the hash
// is absent, a route is incomplete, a family is unmapped, or the reviewed
// producer identity disagrees with the capture's direct producer label.
func ProjectCaptureV2(data []byte, manifest CaptureV2RoutingManifest) ([]CorpusDocument, error) {
	if err := validateCaptureV2RoutingManifest(manifest); err != nil {
		return nil, err
	}

	var capture captureV2
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&capture); err != nil {
		return nil, fmt.Errorf("decode schema-2 capture: %w", err)
	}
	if capture.SchemaVersion != captureV2SchemaVersion {
		return nil, fmt.Errorf("capture schema_version: got %q, want %q", capture.SchemaVersion, captureV2SchemaVersion)
	}
	if capture.Tool.Version == "" {
		return nil, fmt.Errorf("capture tool.version: must not be empty")
	}
	if len(capture.Signals.Metrics.Families) == 0 {
		return nil, fmt.Errorf("capture metrics: must contain at least one family")
	}

	hash := captureV2SHA256(data)
	route, ok := captureV2CaptureRouteByHash(manifest, hash)
	if !ok {
		return nil, fmt.Errorf("capture sha256 %q: no reviewed capture route", hash)
	}
	if capture.Capture.Scope.IsScoped != (route.Scope != "full") {
		return nil, fmt.Errorf("capture scope and reviewed routing scope disagree")
	}

	families := make(map[string]CaptureV2FamilyRoute, len(route.Families))
	for _, family := range route.Families {
		families[family.Name] = family
	}
	documents := make(map[string]*CorpusDocument)
	seen := make(map[string]struct{}, len(capture.Signals.Metrics.Families))
	for _, captured := range capture.Signals.Metrics.Families {
		if _, exists := seen[captured.Name]; exists {
			return nil, fmt.Errorf("capture metric %q: duplicate family name", captured.Name)
		}
		seen[captured.Name] = struct{}{}
		family, ok := families[captured.Name]
		if !ok {
			return nil, fmt.Errorf("capture metric %q: no reviewed family route", captured.Name)
		}
		// A family may genuinely lack the source producer label (the full capture
		// has such families). Its route still needs a reviewed producer identity;
		// use that explicit row only to satisfy the privacy projection, never as a
		// capture-wide or name-derived fallback.
		fallback := ""
		hasDirectProducer := captureV2MetricHasDirectProducer(captured, route.MetricProducerLabel)
		if !hasDirectProducer {
			fallback = family.Producers[0].Name
		}
		metric, err := convertCaptureV2Metric(captured, route.MetricProducerLabel, fallback)
		if err != nil {
			return nil, err
		}
		if hasDirectProducer && !sameProducers(metric.Producers, family.Producers) {
			return nil, fmt.Errorf("capture metric %q: reviewed producers do not match direct capture identity", captured.Name)
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
			return nil, fmt.Errorf("capture sha256 %q: reviewed family %q is absent", hash, family.Name)
		}
	}

	out := make([]CorpusDocument, 0, len(documents))
	for _, document := range documents {
		document.CaptureVolume.ObservedContractCounts = []int{len(document.Inventory.Metrics)}
		normalizeCorpusDocument(document)
		if err := validateCorpusDocument(*document); err != nil {
			return nil, fmt.Errorf("project schema-2 capture: %w", err)
		}
		out = append(out, *document)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Area < out[j].Area })
	return out, nil
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
		if len(capture.Families) == 0 {
			return fmt.Errorf("capture routing sha256 %q: must contain at least one family", capture.SHA256)
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
