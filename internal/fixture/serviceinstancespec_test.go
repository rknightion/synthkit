// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import "testing"

func TestLookupRDSSpec_ExactHits(t *testing.T) {
	// db.r6g.large: real RDS physical memory = 16 GiB, 2 vCPU, non-burstable.
	s := LookupRDSSpec("db.r6g.large")
	if !s.Known {
		t.Fatalf("db.r6g.large should be a known catalogue class")
	}
	if s.VCPU != 2 {
		t.Errorf("db.r6g.large vCPU = %d, want 2", s.VCPU)
	}
	if gib := s.MemBytes / (1024 * 1024 * 1024); gib < 15.9 || gib > 16.1 {
		t.Errorf("db.r6g.large mem = %.2f GiB, want ~16", gib)
	}
	if s.Burstable {
		t.Errorf("db.r6g.large must not be burstable")
	}
}

func TestLookupRDSSpec_BurstableHasBaseline(t *testing.T) {
	s := LookupRDSSpec("db.t3.medium")
	if !s.Known || !s.Burstable {
		t.Fatalf("db.t3.medium should be a known burstable class (got known=%v burst=%v)", s.Known, s.Burstable)
	}
	if s.BaselineCPUPct <= 0 || s.BaselineCPUPct >= 100 {
		t.Errorf("db.t3.medium baseline = %v, want a sane 0<x<100 percentage", s.BaselineCPUPct)
	}
}

func TestLookupCacheSpec_UsableMemory(t *testing.T) {
	// cache.r6g.large usable (maxmemory) = 13.07 GiB — strictly LESS than the 16 GiB physical
	// RAM (engine overhead removed). This is the key ElastiCache-vs-RDS distinction.
	c := LookupCacheSpec("cache.r6g.large")
	if !c.Known {
		t.Fatalf("cache.r6g.large should be a known catalogue node type")
	}
	gib := c.MemBytes / (1024 * 1024 * 1024)
	if gib < 12.5 || gib > 13.5 {
		t.Errorf("cache.r6g.large usable mem = %.2f GiB, want ~13.07", gib)
	}
	// Sanity: usable cache memory < the EC2/RDS physical equivalent (16 GiB).
	if rds := LookupRDSSpec("db.r6g.large").MemBytes; c.MemBytes >= rds {
		t.Errorf("cache usable mem (%.0f) should be < RDS physical mem (%.0f) for r6g.large", c.MemBytes, rds)
	}
}

func TestLookupSpec_UnknownFallsBackSynthesised(t *testing.T) {
	s := LookupRDSSpec("db.z9z.42xlarge") // not in catalogue, unknown suffix
	if s.Known {
		t.Errorf("unknown class must be Known=false")
	}
	if s.Class != "db.z9z.42xlarge" {
		t.Errorf("Class must be preserved verbatim, got %q", s.Class)
	}
	if s.VCPU <= 0 || s.MemBytes <= 0 {
		t.Errorf("synthesised spec must still have positive vCPU/mem, got %d/%v", s.VCPU, s.MemBytes)
	}

	// Known size suffix synthesises from the shared size ladder.
	known := LookupCacheSpec("cache.r9g.xlarge") // unknown family, known "xlarge" suffix
	if known.Known {
		t.Errorf("cache.r9g.xlarge should be synthesised (Known=false)")
	}
	if known.VCPU != 4 { // sizeLadder["xlarge"] = {4,16}
		t.Errorf("synthesised xlarge vCPU = %d, want 4", known.VCPU)
	}
}

func TestLookupSpec_CatalogueNonEmpty(t *testing.T) {
	if len(rdsSpecs) < 20 {
		t.Errorf("rds catalogue parsed %d classes, expected many more", len(rdsSpecs))
	}
	if len(cacheSpecs) < 20 {
		t.Errorf("elasticache catalogue parsed %d node types, expected many more", len(cacheSpecs))
	}
}
