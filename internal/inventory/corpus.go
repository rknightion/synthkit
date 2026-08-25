// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CorpusVersion is the version of the report-only reality-corpus envelope.
const CorpusVersion = "synthkit.telemetry.reality-corpus/v1alpha1"

// CorpusDocument is one producer's observations for one signals catalogue area.
// The source provenance applies to every entry in Inventory.
type CorpusDocument struct {
	CorpusVersion string          `json:"corpus_version"`
	Area          string          `json:"area"`
	Source        CorpusSource    `json:"source"`
	Authority     CorpusAuthority `json:"authority"`
	CaptureVolume CaptureVolume   `json:"capture_volume"`
	Inventory     Schema          `json:"inventory"`
}

// CorpusSource identifies the generic producer and capture configuration. It deliberately
// contains no deployment-specific identity.
type CorpusSource struct {
	Kind             string `json:"kind"`
	Substrate        string `json:"substrate"`
	Collector        string `json:"collector"`
	CollectorVersion string `json:"collector_version"`
	CapturedOn       string `json:"captured_on"`
}

// CorpusAuthority identifies the substrates for which this document is evidence.
type CorpusAuthority struct {
	Substrates []string `json:"substrates"`
}

// CaptureVolume is provenance only. ObservedContractCounts is the sorted distinct set of
// contract counts seen across Runs capture runs.
type CaptureVolume struct {
	Runs                   int   `json:"runs"`
	ObservedContractCounts []int `json:"observed_contract_counts"`
}

var allowedCorpusAreas = map[string]struct{}{
	"00-canon": {}, "agentcore": {}, "apm": {}, "bedrock": {}, "beyla": {},
	"cloudflare": {}, "cspazure": {}, "cspgcp": {}, "cw": {}, "dbo11y": {},
	"fm": {}, "genai-models": {}, "genai": {}, "host": {}, "k8s-addons": {},
	"k8s": {}, "langsmith": {}, "logs": {}, "nettopo": {}, "otlp-metrics": {},
	"portkey": {}, "profiles": {}, "qualification": {}, "sigil": {}, "sm": {},
	"snowflake": {}, "traces": {},
}

// LoadCorpusDir loads every .json document below path. Files are parsed in deterministic path
// order, and duplicate producer/area/substrate identities are rejected.
func LoadCorpusDir(path string) ([]CorpusDocument, error) {
	files, err := corpusJSONFiles(path)
	if err != nil {
		return nil, err
	}
	documents := make([]CorpusDocument, 0, len(files))
	seen := make(map[string]string, len(files))
	for _, file := range files {
		document, err := loadCorpusFile(file)
		if err != nil {
			return nil, err
		}
		document.Authority.Substrates = sortedUniqueStrings(document.Authority.Substrates)
		key := corpusIdentity(document)
		if previous, exists := seen[key]; exists {
			return nil, fmt.Errorf("%s: duplicate area/source/substrate document %q; already defined by %s", file, key, previous)
		}
		seen[key] = file
		documents = append(documents, document)
	}
	return documents, nil
}

func corpusJSONFiles(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("corpus directory %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("corpus directory %q: not a directory", root)
	}
	files := make([]string, 0)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Ext(entry.Name()) == ".json" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk corpus directory %q: %w", root, err)
	}
	sort.Slice(files, func(i, j int) bool {
		return filepath.ToSlash(files[i]) < filepath.ToSlash(files[j])
	})
	return files, nil
}

func loadCorpusFile(path string) (CorpusDocument, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return CorpusDocument{}, fmt.Errorf("%s: read corpus document: %w", path, err)
	}
	var raw map[string]json.RawMessage
	rawDecoder := json.NewDecoder(bytes.NewReader(data))
	if err := rawDecoder.Decode(&raw); err != nil {
		return CorpusDocument{}, fmt.Errorf("%s: invalid JSON: %w", path, err)
	}
	if err := requireJSONEOF(rawDecoder); err != nil {
		return CorpusDocument{}, fmt.Errorf("%s: invalid JSON: %w", path, err)
	}
	if raw == nil {
		return CorpusDocument{}, fmt.Errorf("%s: document must be a JSON object", path)
	}
	for _, field := range []string{"corpus_version", "area", "source", "authority", "capture_volume", "inventory"} {
		if _, ok := raw[field]; !ok {
			return CorpusDocument{}, fmt.Errorf("%s: missing required field %s", path, field)
		}
	}
	if _, err := requiredObject(raw, "source", "source"); err != nil {
		return CorpusDocument{}, fmt.Errorf("%s: %w", path, err)
	}
	if _, err := requiredObject(raw, "authority", "authority"); err != nil {
		return CorpusDocument{}, fmt.Errorf("%s: %w", path, err)
	}
	if _, err := requiredObject(raw, "capture_volume", "capture_volume"); err != nil {
		return CorpusDocument{}, fmt.Errorf("%s: %w", path, err)
	}
	if inventory, err := requiredObject(raw, "inventory", "inventory"); err != nil {
		return CorpusDocument{}, fmt.Errorf("%s: %w", path, err)
	} else if _, ok := inventory["schema_version"]; !ok {
		return CorpusDocument{}, fmt.Errorf("%s: missing required field inventory.schema_version", path)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document CorpusDocument
	if err := decoder.Decode(&document); err != nil {
		return CorpusDocument{}, fmt.Errorf("%s: decode corpus document: %w", path, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return CorpusDocument{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := validateCorpusDocument(document); err != nil {
		return CorpusDocument{}, fmt.Errorf("%s: %w", path, err)
	}
	normalizeCorpusDocument(&document)
	return document, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	switch {
	case err == io.EOF:
		return nil
	case err == nil:
		return errors.New("trailing JSON value: multiple documents are not allowed")
	default:
		return fmt.Errorf("trailing JSON data: %w", err)
	}
}

func requiredObject(raw map[string]json.RawMessage, key, field string) (map[string]json.RawMessage, error) {
	value, ok := raw[key]
	if !ok || string(value) == "null" {
		return nil, fmt.Errorf("missing required object field %s", field)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(value, &object); err != nil || object == nil {
		if err == nil {
			err = errors.New("must be a JSON object")
		}
		return nil, fmt.Errorf("field %s %w", field, err)
	}
	return object, nil
}

func validateCorpusDocument(document CorpusDocument) error {
	if document.CorpusVersion != CorpusVersion {
		return fmt.Errorf("corpus_version: got %q, want %q", document.CorpusVersion, CorpusVersion)
	}
	if _, ok := allowedCorpusAreas[document.Area]; !ok {
		return fmt.Errorf("area: %q is not an allowed signals area", document.Area)
	}
	if err := validateNonEmpty(document.Source.Kind, "source.kind"); err != nil {
		return err
	}
	if err := validateNonEmpty(document.Source.Substrate, "source.substrate"); err != nil {
		return err
	}
	if err := validateNonEmpty(document.Source.Collector, "source.collector"); err != nil {
		return err
	}
	if err := validateNonEmpty(document.Source.CollectorVersion, "source.collector_version"); err != nil {
		return err
	}
	if err := validateCapturedOn(document.Source.CapturedOn); err != nil {
		return err
	}
	if len(document.Authority.Substrates) == 0 {
		return errors.New("authority.substrates: must contain at least one substrate")
	}
	for i, substrate := range document.Authority.Substrates {
		if strings.TrimSpace(substrate) == "" {
			return fmt.Errorf("authority.substrates[%d]: must not be empty", i)
		}
	}
	normalizedAuthority := sortedUniqueStrings(document.Authority.Substrates)
	if len(normalizedAuthority) != 1 || normalizedAuthority[0] != document.Source.Substrate {
		return fmt.Errorf("authority.substrates: must normalize to exactly [%q], matching source.substrate", document.Source.Substrate)
	}
	if document.CaptureVolume.Runs <= 0 {
		return errors.New("capture_volume.runs: must be greater than zero")
	}
	if len(document.CaptureVolume.ObservedContractCounts) == 0 {
		return errors.New("capture_volume.observed_contract_counts: must contain at least one count")
	}
	for i, count := range document.CaptureVolume.ObservedContractCounts {
		if count < 0 {
			return fmt.Errorf("capture_volume.observed_contract_counts[%d]: must not be negative", i)
		}
	}
	if document.Inventory.SchemaVersion != SchemaVersion {
		return fmt.Errorf("inventory.schema_version: got %q, want %q", document.Inventory.SchemaVersion, SchemaVersion)
	}
	return validateInventory(document.Inventory)
}

func validateNonEmpty(value, field string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s: must not be empty", field)
	}
	return nil
}

func validateCapturedOn(value string) error {
	if err := validateNonEmpty(value, "source.captured_on"); err != nil {
		return err
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil || parsed.Format("2006-01-02") != value {
		return fmt.Errorf("source.captured_on: %q must be YYYY-MM-DD", value)
	}
	return nil
}

func validateInventory(schema Schema) error {
	for i, metric := range schema.Metrics {
		if err := validateNonEmpty(metric.Name, fmt.Sprintf("inventory.metrics[%d].name", i)); err != nil {
			return err
		}
		if err := validateAttributes(metric.Labels, fmt.Sprintf("inventory.metrics[%d].labels", i)); err != nil {
			return err
		}
	}
	for i, log := range schema.Logs {
		if err := validateNonEmpty(log.Transport, fmt.Sprintf("inventory.logs[%d].transport", i)); err != nil {
			return err
		}
		if err := validateAttributes(log.StreamLabels, fmt.Sprintf("inventory.logs[%d].stream_labels", i)); err != nil {
			return err
		}
	}
	for i, trace := range schema.Traces {
		if err := validateNonEmpty(trace.Service, fmt.Sprintf("inventory.traces[%d].service", i)); err != nil {
			return err
		}
		if err := validateAttributes(trace.ResourceAttributes, fmt.Sprintf("inventory.traces[%d].resource_attributes", i)); err != nil {
			return err
		}
	}
	for i, profile := range schema.Profiles {
		if err := validateNonEmpty(profile.ProfileType, fmt.Sprintf("inventory.profiles[%d].profile_type", i)); err != nil {
			return err
		}
		if err := validateAttributes(profile.Labels, fmt.Sprintf("inventory.profiles[%d].labels", i)); err != nil {
			return err
		}
	}
	for i, sigil := range schema.Sigil {
		if err := validateNonEmpty(sigil.IngestKind, fmt.Sprintf("inventory.sigil[%d].ingest_kind", i)); err != nil {
			return err
		}
	}
	return nil
}

func validateAttributes(attributes []Attribute, field string) error {
	for i, attribute := range attributes {
		if strings.TrimSpace(attribute.Key) == "" {
			return fmt.Errorf("%s[%d].key: must not be empty", field, i)
		}
	}
	return nil
}

func corpusIdentity(document CorpusDocument) string {
	return strings.Join([]string{document.Area, document.Source.Kind, document.Source.Substrate}, "|")
}

// CanonicalMerge adds candidate's structural evidence to existing. It rejects documents whose
// producer/configuration identity differs. Missing candidate observations never remove existing
// shapes; values that vary become an empty, sticky elided set.
func CanonicalMerge(existing, candidate CorpusDocument) (CorpusDocument, error) {
	if err := validateCorpusDocument(existing); err != nil {
		return CorpusDocument{}, fmt.Errorf("existing corpus document: %w", err)
	}
	if err := validateCorpusDocument(candidate); err != nil {
		return CorpusDocument{}, fmt.Errorf("candidate corpus document: %w", err)
	}
	normalizeCorpusDocument(&existing)
	normalizeCorpusDocument(&candidate)
	if err := matchingCorpusIdentity(existing, candidate); err != nil {
		return CorpusDocument{}, err
	}
	if documentsEqual(existing, candidate) {
		return existing, nil
	}

	out := cloneCorpusDocument(existing)
	structuralEvidence := mergeSchemas(&out.Inventory, candidate.Inventory)
	if structuralEvidence {
		mergeCaptureVolume(&out.CaptureVolume, candidate.CaptureVolume)
		mergeReceipts(&out.Inventory, &candidate.Inventory)
		out.Source.CapturedOn = candidate.Source.CapturedOn
	}
	normalizeCorpusDocument(&out)
	return out, nil
}

func matchingCorpusIdentity(existing, candidate CorpusDocument) error {
	checks := []struct {
		field     string
		existing  string
		candidate string
	}{
		{"area", existing.Area, candidate.Area},
		{"source.kind", existing.Source.Kind, candidate.Source.Kind},
		{"source.substrate", existing.Source.Substrate, candidate.Source.Substrate},
		{"source.collector", existing.Source.Collector, candidate.Source.Collector},
		{"source.collector_version", existing.Source.CollectorVersion, candidate.Source.CollectorVersion},
	}
	for _, check := range checks {
		if check.existing != check.candidate {
			return fmt.Errorf("cannot merge corpus documents: %s differs (%q versus %q)", check.field, check.existing, check.candidate)
		}
	}
	if !equalStrings(existing.Authority.Substrates, candidate.Authority.Substrates) {
		return fmt.Errorf("cannot merge corpus documents: authority.substrates differs (%v versus %v)", existing.Authority.Substrates, candidate.Authority.Substrates)
	}
	return nil
}

func mergeCaptureVolume(existing *CaptureVolume, candidate CaptureVolume) {
	existing.Runs += candidate.Runs
	existing.ObservedContractCounts = sortedUniqueInts(append(existing.ObservedContractCounts, candidate.ObservedContractCounts...))
}

func mergeSchemas(existing *Schema, candidate Schema) bool {
	changed := false
	for _, metric := range candidate.Metrics {
		i := indexMetric(existing.Metrics, metric.Name)
		if i < 0 {
			existing.Metrics = append(existing.Metrics, cloneMetric(metric))
			changed = true
			continue
		}
		changed = mergeMetric(&existing.Metrics[i], metric) || changed
	}
	for _, log := range candidate.Logs {
		i := indexLog(existing.Logs, log)
		if i < 0 {
			existing.Logs = append(existing.Logs, cloneLog(log))
			changed = true
			continue
		}
		changed = mergeLog(&existing.Logs[i], log) || changed
	}
	for _, trace := range candidate.Traces {
		i := indexTrace(existing.Traces, trace.Service)
		if i < 0 {
			existing.Traces = append(existing.Traces, cloneTrace(trace))
			changed = true
			continue
		}
		changed = mergeTrace(&existing.Traces[i], trace) || changed
	}
	for _, profile := range candidate.Profiles {
		i := indexProfile(existing.Profiles, profile.ProfileType)
		if i < 0 {
			existing.Profiles = append(existing.Profiles, cloneProfile(profile))
			changed = true
			continue
		}
		changed = mergeProfile(&existing.Profiles[i], profile) || changed
	}
	for _, sigil := range candidate.Sigil {
		i := corpusIndexSigil(existing.Sigil, sigil.IngestKind)
		if i < 0 {
			existing.Sigil = append(existing.Sigil, cloneSigil(sigil))
			changed = true
			continue
		}
		changed = mergeSigil(&existing.Sigil[i], sigil) || changed
	}
	existing.Normalize()
	normalizeElidedValues(existing)
	return changed
}

func mergeMetric(existing *Metric, candidate Metric) bool {
	changed := corpusMergeStringSet(&existing.Transports, candidate.Transports)
	changed = corpusMergeStringSet(&existing.InstrumentTypes, candidate.InstrumentTypes) || changed
	for _, candidateAttribute := range candidate.Labels {
		i := indexAttribute(existing.Labels, candidateAttribute.Key)
		if i < 0 {
			existing.Labels = append(existing.Labels, cloneAttribute(candidateAttribute))
			changed = true
			continue
		}
		changed = mergeAttributeValues(&existing.Labels[i], candidateAttribute) || changed
	}
	changed = mergeHistogram(&existing.Histogram, candidate.Histogram) || changed
	sortAttributes(&existing.Labels)
	return changed
}

func mergeLog(existing *Log, candidate Log) bool {
	changed := false
	for _, candidateAttribute := range candidate.StreamLabels {
		i := indexAttribute(existing.StreamLabels, candidateAttribute.Key)
		if i < 0 {
			existing.StreamLabels = append(existing.StreamLabels, cloneAttribute(candidateAttribute))
			changed = true
			continue
		}
		changed = mergeAttributeValues(&existing.StreamLabels[i], candidateAttribute) || changed
	}
	changed = corpusMergeStringSet(&existing.StructuredMetadataKeys, candidate.StructuredMetadataKeys) || changed
	sortAttributes(&existing.StreamLabels)
	return changed
}

func mergeTrace(existing *Trace, candidate Trace) bool {
	changed := false
	for _, candidateAttribute := range candidate.ResourceAttributes {
		i := indexAttribute(existing.ResourceAttributes, candidateAttribute.Key)
		if i < 0 {
			existing.ResourceAttributes = append(existing.ResourceAttributes, cloneAttribute(candidateAttribute))
			changed = true
			continue
		}
		changed = mergeAttributeValues(&existing.ResourceAttributes[i], candidateAttribute) || changed
	}
	changed = corpusMergeStringSet(&existing.SpanNames, candidate.SpanNames) || changed
	changed = corpusMergeStringSet(&existing.SpanAttributeKeys, candidate.SpanAttributeKeys) || changed
	sortAttributes(&existing.ResourceAttributes)
	return changed
}

func mergeProfile(existing *Profile, candidate Profile) bool {
	changed := false
	for _, candidateAttribute := range candidate.Labels {
		i := indexAttribute(existing.Labels, candidateAttribute.Key)
		if i < 0 {
			existing.Labels = append(existing.Labels, cloneAttribute(candidateAttribute))
			changed = true
			continue
		}
		changed = mergeAttributeValues(&existing.Labels[i], candidateAttribute) || changed
	}
	sortAttributes(&existing.Labels)
	return changed
}

func mergeSigil(existing *Sigil, candidate Sigil) bool {
	return corpusMergeStringSet(&existing.OperationNames, candidate.OperationNames)
}

func mergeHistogram(existing **Histogram, candidate *Histogram) bool {
	if candidate == nil {
		return false
	}
	if *existing == nil {
		*existing = cloneHistogram(candidate)
		return true
	}
	changed := false
	if candidate.Classic && !(*existing).Classic {
		(*existing).Classic = true
		changed = true
	}
	if candidate.Native && !(*existing).Native {
		(*existing).Native = true
		changed = true
	}
	beforeBounds := len((*existing).BucketBounds)
	(*existing).BucketBounds = sortedUniqueFloats(append((*existing).BucketBounds, candidate.BucketBounds...))
	changed = changed || len((*existing).BucketBounds) != beforeBounds
	beforeSchemas := len((*existing).NativeSchemas)
	(*existing).NativeSchemas = sortedUniqueInt32(append((*existing).NativeSchemas, candidate.NativeSchemas...))
	return changed || len((*existing).NativeSchemas) != beforeSchemas
}

func mergeAttributeValues(existing *Attribute, candidate Attribute) bool {
	if existing.ValuesElided {
		existing.Values = []string{}
		return false
	}
	if candidate.ValuesElided {
		existing.Values = []string{}
		existing.ValuesElided = true
		return true
	}
	candidateValues := sortedUniqueStrings(candidate.Values)
	if len(candidateValues) == 0 {
		return false
	}
	existingValues := sortedUniqueStrings(existing.Values)
	if len(existingValues) == 0 {
		existing.Values = candidateValues
		return true
	}
	if equalStrings(existingValues, candidateValues) || isSubset(candidateValues, existingValues) {
		return false
	}
	existing.Values = []string{}
	existing.ValuesElided = true
	return true
}

func corpusMergeStringSet(existing *[]string, candidate []string) bool {
	before := append([]string{}, (*existing)...)
	*existing = sortedUniqueStrings(append(*existing, candidate...))
	return !equalStrings(before, *existing)
}

func mergeReceipts(existing, candidate *Schema) {
	for _, receipt := range candidate.Receipts {
		found := false
		for _, prior := range existing.Receipts {
			if prior.Protocol == receipt.Protocol {
				found = true
				break
			}
		}
		if !found {
			existing.Receipts = append(existing.Receipts, receipt)
		}
	}
	sort.Slice(existing.Receipts, func(i, j int) bool { return existing.Receipts[i].Protocol < existing.Receipts[j].Protocol })
}

func normalizeCorpusDocument(document *CorpusDocument) {
	document.Authority.Substrates = sortedUniqueStrings(document.Authority.Substrates)
	document.CaptureVolume.ObservedContractCounts = sortedUniqueInts(document.CaptureVolume.ObservedContractCounts)
	document.Inventory.Normalize()
	normalizeElidedValues(&document.Inventory)
}

func normalizeElidedValues(schema *Schema) {
	for i := range schema.Metrics {
		normalizeElidedAttributes(schema.Metrics[i].Labels)
	}
	for i := range schema.Logs {
		normalizeElidedAttributes(schema.Logs[i].StreamLabels)
	}
	for i := range schema.Traces {
		normalizeElidedAttributes(schema.Traces[i].ResourceAttributes)
	}
	for i := range schema.Profiles {
		normalizeElidedAttributes(schema.Profiles[i].Labels)
	}
}

func normalizeElidedAttributes(attributes []Attribute) {
	for i := range attributes {
		attributes[i].Values = sortedUniqueStrings(attributes[i].Values)
		if attributes[i].ValuesElided {
			attributes[i].Values = []string{}
		}
	}
}

func cloneCorpusDocument(document CorpusDocument) CorpusDocument {
	out := document
	out.Authority.Substrates = append([]string{}, document.Authority.Substrates...)
	out.CaptureVolume.ObservedContractCounts = append([]int{}, document.CaptureVolume.ObservedContractCounts...)
	out.Inventory = cloneSchema(document.Inventory)
	return out
}

func cloneSchema(schema Schema) Schema {
	out := schema
	out.Metrics = make([]Metric, len(schema.Metrics))
	for i, metric := range schema.Metrics {
		out.Metrics[i] = cloneMetric(metric)
	}
	out.Logs = make([]Log, len(schema.Logs))
	for i, log := range schema.Logs {
		out.Logs[i] = cloneLog(log)
	}
	out.Traces = make([]Trace, len(schema.Traces))
	for i, trace := range schema.Traces {
		out.Traces[i] = cloneTrace(trace)
	}
	out.Profiles = make([]Profile, len(schema.Profiles))
	for i, profile := range schema.Profiles {
		out.Profiles[i] = cloneProfile(profile)
	}
	out.Sigil = make([]Sigil, len(schema.Sigil))
	for i, sigil := range schema.Sigil {
		out.Sigil[i] = cloneSigil(sigil)
	}
	out.Receipts = append([]Receipt{}, schema.Receipts...)
	return out
}

func cloneMetric(metric Metric) Metric {
	out := metric
	out.Transports = append([]string{}, metric.Transports...)
	out.InstrumentTypes = append([]string{}, metric.InstrumentTypes...)
	out.Labels = cloneAttributes(metric.Labels)
	out.Histogram = cloneHistogram(metric.Histogram)
	return out
}

func cloneLog(log Log) Log {
	out := log
	out.StreamLabels = cloneAttributes(log.StreamLabels)
	out.StructuredMetadataKeys = append([]string{}, log.StructuredMetadataKeys...)
	return out
}

func cloneTrace(trace Trace) Trace {
	out := trace
	out.ResourceAttributes = cloneAttributes(trace.ResourceAttributes)
	out.SpanNames = append([]string{}, trace.SpanNames...)
	out.SpanAttributeKeys = append([]string{}, trace.SpanAttributeKeys...)
	return out
}

func cloneProfile(profile Profile) Profile {
	out := profile
	out.Labels = cloneAttributes(profile.Labels)
	return out
}

func cloneSigil(sigil Sigil) Sigil {
	out := sigil
	out.OperationNames = append([]string{}, sigil.OperationNames...)
	return out
}

func cloneAttributes(attributes []Attribute) []Attribute {
	out := make([]Attribute, len(attributes))
	for i, attribute := range attributes {
		out[i] = cloneAttribute(attribute)
	}
	return out
}

func cloneAttribute(attribute Attribute) Attribute {
	attribute.Values = append([]string{}, attribute.Values...)
	return attribute
}

func cloneHistogram(histogram *Histogram) *Histogram {
	if histogram == nil {
		return nil
	}
	out := *histogram
	out.BucketBounds = append([]float64{}, histogram.BucketBounds...)
	out.NativeSchemas = append([]int32{}, histogram.NativeSchemas...)
	return &out
}

func indexMetric(metrics []Metric, name string) int {
	for i := range metrics {
		if metrics[i].Name == name {
			return i
		}
	}
	return -1
}

func indexLog(logs []Log, candidate Log) int {
	key := logShapeKey(candidate)
	for i := range logs {
		if logShapeKey(logs[i]) == key {
			return i
		}
	}
	return -1
}

func indexTrace(traces []Trace, service string) int {
	for i := range traces {
		if traces[i].Service == service {
			return i
		}
	}
	return -1
}

func indexProfile(profiles []Profile, profileType string) int {
	for i := range profiles {
		if profiles[i].ProfileType == profileType {
			return i
		}
	}
	return -1
}

func corpusIndexSigil(sigils []Sigil, ingestKind string) int {
	for i := range sigils {
		if sigils[i].IngestKind == ingestKind {
			return i
		}
	}
	return -1
}

func indexAttribute(attributes []Attribute, key string) int {
	for i := range attributes {
		if attributes[i].Key == key {
			return i
		}
	}
	return -1
}

func logShapeKey(log Log) string {
	keys := make([]string, 0, len(log.StreamLabels))
	for _, attribute := range log.StreamLabels {
		keys = append(keys, attribute.Key)
	}
	keys = sortedUniqueStrings(keys)
	metadata := sortedUniqueStrings(log.StructuredMetadataKeys)
	return log.Transport + "\x00" + strings.Join(keys, "\x00") + "\x00" + strings.Join(metadata, "\x00")
}

func documentsEqual(a, b CorpusDocument) bool {
	return equalJSON(a, b)
}

func equalJSON(a, b any) bool {
	left, leftErr := json.Marshal(a)
	right, rightErr := json.Marshal(b)
	return leftErr == nil && rightErr == nil && bytes.Equal(left, right)
}

func sortedUniqueStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	out := append([]string{}, values...)
	sort.Strings(out)
	result := out[:0]
	for _, value := range out {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func sortedUniqueInts(values []int) []int {
	if len(values) == 0 {
		return []int{}
	}
	out := append([]int{}, values...)
	sort.Ints(out)
	result := out[:0]
	for _, value := range out {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func sortedUniqueInt32(values []int32) []int32 {
	if len(values) == 0 {
		return []int32{}
	}
	out := append([]int32{}, values...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	result := out[:0]
	for _, value := range out {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func sortedUniqueFloats(values []float64) []float64 {
	if len(values) == 0 {
		return []float64{}
	}
	out := append([]float64{}, values...)
	sort.Float64s(out)
	result := out[:0]
	for _, value := range out {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func sortAttributes(attributes *[]Attribute) {
	for i := range *attributes {
		(*attributes)[i].Values = sortedUniqueStrings((*attributes)[i].Values)
		if (*attributes)[i].ValuesElided {
			(*attributes)[i].Values = []string{}
		}
	}
	sort.Slice(*attributes, func(i, j int) bool { return (*attributes)[i].Key < (*attributes)[j].Key })
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isSubset(candidate, existing []string) bool {
	for _, value := range candidate {
		if !containsString(existing, value) {
			return false
		}
	}
	return true
}

// ScopedFinding carries the corpus document's ownership and provenance beside one inventory
// finding. Area is authoritative; signal names are never used to infer it.
type ScopedFinding struct {
	Area      string       `json:"area"`
	Source    CorpusSource `json:"source"`
	Substrate string       `json:"substrate"`
	Finding   Finding      `json:"finding"`
}

// CompareCorpus compares each document independently. Documents from different substrates are
// never unioned, and signal classes absent from a document are outside that document's coverage.
func CompareCorpus(synth Schema, documents []CorpusDocument) []ScopedFinding {
	synthCopy := cloneSchema(synth)
	synthCopy.Normalize()
	ordered := make([]CorpusDocument, len(documents))
	for i, document := range documents {
		ordered[i] = cloneCorpusDocument(document)
		normalizeCorpusDocument(&ordered[i])
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return compareCorpusDocuments(ordered[i], ordered[j]) < 0
	})

	out := make([]ScopedFinding, 0)
	for _, document := range ordered {
		if synth.Provenance != nil && synth.Provenance.Substrate != "" &&
			!containsString(document.Authority.Substrates, synth.Provenance.Substrate) {
			continue
		}
		reality := document.Inventory
		comparison := scopedSynthSchema(synthCopy, reality)
		for _, finding := range Diff(comparison, reality) {
			out = append(out, ScopedFinding{
				Area:      document.Area,
				Source:    document.Source,
				Substrate: document.Source.Substrate,
				Finding:   finding,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return compareScopedFindings(out[i], out[j]) < 0 })
	return out
}

func scopedSynthSchema(synth, reality Schema) Schema {
	out := New()
	synthCopy := cloneSchema(synth)
	if len(reality.Metrics) > 0 {
		out.Metrics = synthCopy.Metrics
	}
	if len(reality.Logs) > 0 {
		out.Logs = synthCopy.Logs
	}
	if len(reality.Traces) > 0 {
		out.Traces = synthCopy.Traces
	}
	if len(reality.Profiles) > 0 {
		out.Profiles = synthCopy.Profiles
	}
	if len(reality.Sigil) > 0 {
		out.Sigil = synthCopy.Sigil
	}
	return out
}

func compareCorpusDocuments(a, b CorpusDocument) int {
	for _, pair := range [][2]string{
		{a.Area, b.Area},
		{a.Source.Substrate, b.Source.Substrate},
		{a.Source.Kind, b.Source.Kind},
		{a.Source.Collector, b.Source.Collector},
		{a.Source.CollectorVersion, b.Source.CollectorVersion},
		{a.Source.CapturedOn, b.Source.CapturedOn},
	} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return compareStrings(a.Authority.Substrates, b.Authority.Substrates)
}

func compareScopedFindings(a, b ScopedFinding) int {
	if result := compareCorpusDocuments(
		CorpusDocument{Area: a.Area, Source: a.Source, Authority: CorpusAuthority{Substrates: []string{a.Substrate}}},
		CorpusDocument{Area: b.Area, Source: b.Source, Authority: CorpusAuthority{Substrates: []string{b.Substrate}}},
	); result != 0 {
		return result
	}
	left, right := a.Finding, b.Finding
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
	if result := compareStrings(left.SynthValues, right.SynthValues); result != 0 {
		return result
	}
	return compareStrings(left.RealityValues, right.RealityValues)
}
