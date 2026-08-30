// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"encoding/json"
	"io"
)

// StructuralProjection is the SKT-0004 determinism surface. It intentionally excludes
// label values, capture provenance, receipts, volatile sigil counts, and sampled exemplars.
type StructuralProjection struct {
	Metrics  []MetricProjection  `json:"metrics"`
	Logs     []LogProjection     `json:"logs"`
	Traces   []TraceProjection   `json:"traces"`
	Profiles []ProfileProjection `json:"profiles"`
	Sigil    []Sigil             `json:"sigil"`
}

type MetricProjection struct {
	Name            string     `json:"name"`
	Transports      []string   `json:"transports"`
	InstrumentTypes []string   `json:"instrument_types"`
	LabelKeys       []string   `json:"label_keys"`
	Histogram       *Histogram `json:"histogram,omitempty"`
}

type LogProjection struct {
	Source                         string   `json:"source"`
	Transport                      string   `json:"transport"`
	StreamLabelKeys                []string `json:"stream_label_keys"`
	StructuredMetadataKeys         []string `json:"structured_metadata_keys"`
	OptionalStreamLabelKeys        []string `json:"optional_stream_label_keys"`
	OptionalStructuredMetadataKeys []string `json:"optional_structured_metadata_keys"`
}

type TraceProjection struct {
	Service               string   `json:"service"`
	ResourceAttributeKeys []string `json:"resource_attribute_keys"`
	SpanNames             []string `json:"span_names"`
	SpanAttributeKeys     []string `json:"span_attribute_keys"`
}

type ProfileProjection struct {
	ProfileType string   `json:"profile_type"`
	LabelKeys   []string `json:"label_keys"`
}

// Project returns the deterministic contract-only view of s.
func (s Schema) Project() StructuralProjection {
	s.Normalize()
	out := StructuralProjection{
		Metrics: []MetricProjection{}, Logs: []LogProjection{}, Traces: []TraceProjection{},
		Profiles: []ProfileProjection{}, Sigil: append([]Sigil(nil), s.Sigil...),
	}
	for _, metric := range s.Metrics {
		p := MetricProjection{Name: metric.Name, Transports: append([]string(nil), metric.Transports...), InstrumentTypes: append([]string(nil), metric.InstrumentTypes...), LabelKeys: []string{}, Histogram: metric.Histogram}
		for _, label := range metric.Labels {
			p.LabelKeys = append(p.LabelKeys, label.Key)
		}
		out.Metrics = append(out.Metrics, p)
	}
	for _, log := range s.Logs {
		p := LogProjection{Source: log.Source, Transport: log.Transport, StreamLabelKeys: []string{}, StructuredMetadataKeys: append([]string(nil), log.StructuredMetadataKeys...), OptionalStreamLabelKeys: append([]string(nil), log.OptionalStreamLabelKeys...), OptionalStructuredMetadataKeys: append([]string(nil), log.OptionalStructuredMetadataKeys...)}
		for _, label := range log.StreamLabels {
			p.StreamLabelKeys = append(p.StreamLabelKeys, label.Key)
		}
		out.Logs = append(out.Logs, p)
	}
	for _, trace := range s.Traces {
		p := TraceProjection{Service: trace.Service, ResourceAttributeKeys: []string{}, SpanNames: append([]string(nil), trace.SpanNames...), SpanAttributeKeys: append([]string(nil), trace.SpanAttributeKeys...)}
		for _, attr := range trace.ResourceAttributes {
			p.ResourceAttributeKeys = append(p.ResourceAttributeKeys, attr.Key)
		}
		out.Traces = append(out.Traces, p)
	}
	for _, profile := range s.Profiles {
		p := ProfileProjection{ProfileType: profile.ProfileType, LabelKeys: []string{}}
		for _, label := range profile.Labels {
			p.LabelKeys = append(p.LabelKeys, label.Key)
		}
		out.Profiles = append(out.Profiles, p)
	}
	return out
}

func (p StructuralProjection) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}
