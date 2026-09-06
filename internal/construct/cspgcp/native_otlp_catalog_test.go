// SPDX-License-Identifier: AGPL-3.0-only

package cspgcp

import (
	"strings"
	"testing"
)

func normalizedNativeComponent(value string) string {
	value = strings.ToLower(value)
	value = strings.NewReplacer(".", "_", "/", "_").Replace(value)
	return value
}

func expectedScrapeName(spec nativeMetricSpec) string {
	resource := normalizedNativeComponent(spec.resourceType)
	// The existing AlloyDB scrape exporter separates the two words in its
	// InstanceNode resource type. This is the only observed deviation from the
	// one-direction resource+metric naming law.
	if spec.resourceType == "alloydb.googleapis.com/InstanceNode" {
		resource = strings.Replace(resource, "instancenode", "instance_node", 1)
	}
	return "stackdriver_" + resource + "_" + normalizedNativeComponent(spec.nativeName)
}

func TestNativeCatalogRoundTripUsesForwardNamingLaw(t *testing.T) {
	if got, want := len(nativeMetricCatalog), 115; got != want {
		t.Fatalf("native catalogue has %d families, want %d source-confirmed families", got, want)
	}
	seenNative := make(map[string]bool, len(nativeMetricCatalog))
	seenScrape := make(map[string]bool, len(nativeMetricCatalog))
	instanceNodeExceptions := 0
	for _, spec := range nativeMetricCatalog {
		if seenNative[spec.nativeName] {
			t.Fatalf("duplicate native family %q", spec.nativeName)
		}
		seenNative[spec.nativeName] = true
		if seenScrape[spec.scrapeName] {
			t.Fatalf("duplicate scrape family %q", spec.scrapeName)
		}
		seenScrape[spec.scrapeName] = true
		if got, want := spec.scrapeName, expectedScrapeName(spec); got != want {
			t.Errorf("native %q with resource %q maps to %q, want forward-mangled %q", spec.nativeName, spec.resourceType, got, want)
		}
		if spec.resourceType == "alloydb.googleapis.com/InstanceNode" {
			instanceNodeExceptions++
		}
		if len(spec.metricKeys) == 0 {
			// Empty is a valid vendor allowlist. Verify it is explicit in the
			// metadata map rather than an omitted enrichment.
			if metadata, ok := nativeMetricMetadata[spec.nativeName]; !ok || metadata.metricKeys == nil {
				t.Errorf("native %q has no explicit empty metric-label allowlist", spec.nativeName)
			}
		}
	}
	if got, want := len(nativeMetricMetadata), len(nativeMetricCatalog); got != want {
		t.Fatalf("vendor metadata has %d families, want %d", got, want)
	}
	if instanceNodeExceptions != 4 {
		t.Fatalf("forward naming law has %d AlloyDB InstanceNode exceptions, want 4", instanceNodeExceptions)
	}
}

func TestNativeMetricAttributeProjectionUsesDescriptorAllowlists(t *testing.T) {
	for _, spec := range nativeMetricCatalog {
		labels := map[string]string{
			"job":        "integrations/gcp",
			"unit":       "1",
			"instance":   "scrape-target",
			"unlisted":   "must-not-cross",
			"project_id": "resource",
		}
		for _, key := range spec.resourceKeys {
			labels[key] = "resource"
		}
		for _, key := range spec.metricKeys {
			labels[key] = "metric"
		}
		attrs := nativeAnyAttrs(labels, spec)
		if _, ok := attrs["unlisted"]; ok {
			t.Errorf("native %q projected an unlisted metric label", spec.nativeName)
		}
		if _, ok := attrs["job"]; ok {
			t.Errorf("native %q projected scrape job label", spec.nativeName)
		}
		if _, ok := attrs["unit"]; ok {
			t.Errorf("native %q projected scrape unit label", spec.nativeName)
		}
		if _, ok := attrs["instance"]; ok {
			t.Errorf("native %q projected scrape instance label", spec.nativeName)
		}
		if _, ok := attrs["project_id"]; ok {
			t.Errorf("native %q projected resource label as a metric attribute", spec.nativeName)
		}
		if got, want := len(attrs), len(spec.metricKeys); got != want {
			t.Errorf("native %q projected %d metric attributes, want %d", spec.nativeName, got, want)
		}
	}
}
