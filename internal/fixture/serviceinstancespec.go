// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import (
	"bytes"
	_ "embed"
	"encoding/csv"
	"errors"
	"io"
	"strconv"
	"strings"
)

//go:embed rds_instancetypes.csv
var rdsInstanceCSV []byte

//go:embed elasticache_instancetypes.csv
var cacheInstanceCSV []byte

// ClassSpec is the resolved hardware shape of an RDS DB instance class or an ElastiCache
// node type — the SINGLE source of capacity (vCPU, memory) and burstable-CPU behaviour
// that the rds/elasticache constructs use to scale capacity-bound metric VALUES with the
// declared instance_class. Known is false when the class was absent from the captured
// catalogue and the fields were synthesised from the size suffix; Class is ALWAYS verbatim.
//
// MemBytes semantics differ per service (mirrors the CSV provenance): for RDS it is PHYSICAL
// instance memory; for ElastiCache it is USABLE cache memory (AWS maxmemory, engine overhead
// already removed). BaselineCPUPct is the per-vCPU steady-state CPU% a burstable (t-family)
// instance sustains once CPU credits are exhausted (0 for non-burstable classes).
type ClassSpec struct {
	Class          string
	VCPU           int
	MemBytes       float64
	Burstable      bool
	BaselineCPUPct float64
	Known          bool
}

var (
	rdsSpecs   = parseClassSpecs(rdsInstanceCSV)
	cacheSpecs = parseClassSpecs(cacheInstanceCSV)
)

// parseClassSpecs parses an embedded class catalogue (cols: class,vCPU,memoryGiB,
// burstableBaselinePct). '#' provenance/comment lines are skipped; rows with a bad vCPU or
// memory field are dropped (a zero-capacity "known" class is worse than a size-ladder
// fallback). A blank/zero baseline column ⇒ non-burstable.
func parseClassSpecs(data []byte) map[string]ClassSpec {
	out := map[string]ClassSpec{}
	r := csv.NewReader(bytes.NewReader(data))
	r.Comment = '#'
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	for {
		f, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || len(f) < 3 {
			continue
		}
		class := strings.TrimSpace(f[0])
		vcpu, errV := strconv.Atoi(strings.TrimSpace(f[1]))
		memGiB, errM := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(f[2]), ",", ""), 64)
		if class == "" || errV != nil || errM != nil {
			continue
		}
		baseline := 0.0
		if len(f) >= 4 {
			baseline, _ = strconv.ParseFloat(strings.TrimSpace(f[3]), 64)
		}
		out[class] = ClassSpec{
			Class: class, VCPU: vcpu, MemBytes: memGiB * 1024 * 1024 * 1024,
			Burstable: baseline > 0, BaselineCPUPct: baseline, Known: true,
		}
	}
	return out
}

// LookupRDSSpec resolves an RDS DB instance class (e.g. "db.r6g.large") to its capacity
// shape. Exact catalogue hit ⇒ Known=true. A miss preserves Class verbatim and synthesises
// VCPU/MemBytes from the size suffix (Known=false).
func LookupRDSSpec(class string) ClassSpec {
	if s, ok := rdsSpecs[class]; ok {
		return s
	}
	return synthClassSpec(class, 1.0)
}

// LookupCacheSpec resolves an ElastiCache node type (e.g. "cache.r6g.large") to its capacity
// shape. Exact hit ⇒ Known=true. A miss synthesises from the size suffix (Known=false),
// scaling memory by the typical usable fraction since the catalogue figure is usable memory.
func LookupCacheSpec(class string) ClassSpec {
	if s, ok := cacheSpecs[class]; ok {
		return s
	}
	return synthClassSpec(class, usableMemFraction)
}

// usableMemFraction approximates the ElastiCache usable/maxmemory share of physical RAM for
// synthesised (unknown) node types (real figures are in the catalogue; this is fallback only).
const usableMemFraction = 0.82

// synthClassSpec synthesises a spec from the size suffix using the shared EC2 size ladder,
// scaling memory by memFrac (1.0 for RDS physical, usableMemFraction for ElastiCache usable).
// Burstable t-family classes get a representative 20% baseline. Unknown suffix ⇒ 2 vCPU.
func synthClassSpec(class string, memFrac float64) ClassSpec {
	vcpu, memGiB := 2.0, 8.0
	if dot := strings.LastIndex(class, "."); dot >= 0 {
		if sz, ok := sizeLadder[class[dot+1:]]; ok {
			vcpu, memGiB = sz[0], sz[1]
		}
	}
	burstable := strings.Contains(class, ".t3.") || strings.Contains(class, ".t4g.") || strings.Contains(class, ".t2.")
	baseline := 0.0
	if burstable {
		baseline = 20.0
	}
	return ClassSpec{
		Class: class, VCPU: int(vcpu), MemBytes: memGiB * memFrac * 1024 * 1024 * 1024,
		Burstable: burstable, BaselineCPUPct: baseline, Known: false,
	}
}
