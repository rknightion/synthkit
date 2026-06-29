// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import (
	"testing"
)

func TestLookupInstanceSpec_KnownExactMatch(t *testing.T) {
	spec := LookupInstanceSpec("m6i.xlarge")
	if !spec.Known {
		t.Fatalf("m6i.xlarge: expected Known=true, got false")
	}
	if spec.VCPU != 4 {
		t.Errorf("m6i.xlarge VCPU: want 4, got %d", spec.VCPU)
	}
	const want16GiB = 16 * 1024 * 1024 * 1024
	if spec.MemBytes != want16GiB {
		t.Errorf("m6i.xlarge MemBytes: want %d, got %g", want16GiB, spec.MemBytes)
	}
	if spec.Type != "m6i.xlarge" {
		t.Errorf("m6i.xlarge Type: want %q, got %q", "m6i.xlarge", spec.Type)
	}
}

func TestLookupInstanceSpec_m6iLarge(t *testing.T) {
	spec := LookupInstanceSpec("m6i.large")
	if !spec.Known {
		t.Fatalf("m6i.large: expected Known=true, got false")
	}
	if spec.VCPU != 2 {
		t.Errorf("m6i.large VCPU: want 2, got %d", spec.VCPU)
	}
	const want8GiB = 8 * 1024 * 1024 * 1024
	if spec.MemBytes != want8GiB {
		t.Errorf("m6i.large MemBytes: want %d, got %g", want8GiB, spec.MemBytes)
	}
}

func TestLookupInstanceSpec_m8gXlarge_ARM(t *testing.T) {
	spec := LookupInstanceSpec("m8g.xlarge")
	if !spec.Known {
		t.Fatalf("m8g.xlarge: expected Known=true, got false")
	}
	if spec.Arch != "arm64" {
		t.Errorf("m8g.xlarge Arch: want %q, got %q", "arm64", spec.Arch)
	}
}

func TestLookupInstanceSpec_UnknownType_SizeLadder(t *testing.T) {
	spec := LookupInstanceSpec("zz9.2xlarge")
	if spec.Known {
		t.Fatalf("zz9.2xlarge: expected Known=false, got true")
	}
	if spec.Type != "zz9.2xlarge" {
		t.Errorf("zz9.2xlarge Type: want %q (verbatim), got %q", "zz9.2xlarge", spec.Type)
	}
	if spec.VCPU != 8 {
		t.Errorf("zz9.2xlarge VCPU: want 8 (from size ladder), got %d", spec.VCPU)
	}
}

func TestLookupInstanceSpec_TotallyBogus_DefaultFallback(t *testing.T) {
	spec := LookupInstanceSpec("totally-bogus")
	if spec.Known {
		t.Fatalf("totally-bogus: expected Known=false, got true")
	}
	if spec.VCPU != 4 {
		t.Errorf("totally-bogus VCPU: want 4 (default), got %d", spec.VCPU)
	}
}

func TestLookupInstanceSpec_QuotedMemoryComma(t *testing.T) {
	// memory ≥ 1024 GiB is CSV-quoted as "1,024" — a naive comma split drops it to 0.
	spec := LookupInstanceSpec("i4i.32xlarge")
	if !spec.Known {
		t.Fatalf("i4i.32xlarge: expected Known=true, got false")
	}
	const want1024GiB = 1024 * 1024 * 1024 * 1024
	if spec.MemBytes != want1024GiB {
		t.Errorf("i4i.32xlarge MemBytes: want %d (1024 GiB), got %g", int64(want1024GiB), spec.MemBytes)
	}
	if spec.VCPU != 128 {
		t.Errorf("i4i.32xlarge VCPU: want 128, got %d", spec.VCPU)
	}
}

func TestLookupInstanceSpec_QuotedMultiArchComma(t *testing.T) {
	// legacy families carry a quoted multi-arch field "i386, x86_64".
	spec := LookupInstanceSpec("c1.medium")
	if !spec.Known {
		t.Fatalf("c1.medium: expected Known=true, got false")
	}
	if spec.Arch != "i386, x86_64" {
		t.Errorf("c1.medium Arch: want %q, got %q", "i386, x86_64", spec.Arch)
	}
}

func TestEC2Catalogue_NoZeroCapacityKnownSpecs(t *testing.T) {
	// every catalogue (Known) entry must have real capacity — guards against a parser
	// regression silently recording zero-capacity nodes.
	for typ, s := range ec2Specs {
		if s.VCPU <= 0 || s.MemBytes <= 0 {
			t.Errorf("catalogue spec %q has non-positive capacity: vcpu=%d mem=%g", typ, s.VCPU, s.MemBytes)
		}
	}
}

func TestKubeArch(t *testing.T) {
	// values match the GOARCH form real KSM emits on kube_node_labels.label_kubernetes_io_arch
	// (verified against a live reference cluster k8s-monitoring deployment, 2026-06-14).
	cases := map[string]string{
		"m8g.xlarge":      "arm64", // catalogue arch arm64
		"m6i.xlarge":      "amd64", // catalogue arch x86_64
		"c1.medium":       "amd64", // multi-arch "i386, x86_64" → 64-bit baseline
		"zz9.unknownsize": "amd64", // synthesised (Arch="") → default
	}
	for typ, want := range cases {
		if got := LookupInstanceSpec(typ).KubeArch(); got != want {
			t.Errorf("KubeArch(%q) = %q, want %q", typ, got, want)
		}
	}
}

func TestMaxPods(t *testing.T) {
	// validated against a live reference cluster: t4g.large=35, m6g.large=29.
	cases := map[string]int{
		"t4g.large":  35, // 3*(12-1)+2
		"m6g.large":  29, // 3*(10-1)+2
		"m6i.xlarge": 58, // 4*(15-1)+2
	}
	for typ, want := range cases {
		if got := LookupInstanceSpec(typ).MaxPods(); got != want {
			t.Errorf("MaxPods(%q) = %d, want %d", typ, got, want)
		}
	}
	if got := LookupInstanceSpec("zz9.unknownsize").MaxPods(); got != 110 {
		t.Errorf("MaxPods(unknown) = %d, want 110 fallback", got)
	}
}

func TestEC2CatalogueSize(t *testing.T) {
	if len(ec2Specs) <= 800 {
		t.Errorf("ec2Specs catalogue: want >800 entries, got %d", len(ec2Specs))
	}
}
