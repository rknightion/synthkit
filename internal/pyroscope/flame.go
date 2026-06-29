// SPDX-License-Identifier: AGPL-3.0-only

package pyroscope

import (
	"fmt"
	"time"

	pprofpb "github.com/rknightion/synthkit/internal/pyroscope/pprofpb"
)

// Load describes the synthetic load parameters for a single Build call.
// Factor scales all sample values linearly (1.0 = nominal load).
// Modes carries optional failure-mode intensities keyed by mode name (e.g. "cpu_hotspot");
// absent or nil means no amplification.
type Load struct {
	Factor float64
	Now    time.Time
	Modes  map[string]float64
}

// profileCache holds the pre-built, span-free structural tables for one ProfileType.
// Only Function, Location, Mapping, StringTable, and the base sample weights are cached;
// time/load-varying fields are filled in at Build time.
type profileCache struct {
	stringTable []string
	strIdx      map[string]int64
	functions   []*pprofpb.Function
	locations   []*pprofpb.Location
	mapping     []*pprofpb.Mapping
	// leaf weights for each sample (index matches the leaf location index in stacks).
	leafWeights []int64
	// stacks is the list of location-id stacks, each ordered leaf-first.
	stacks [][]uint64
	// hotLeafIdx is the index into stacks of the "hot" leaf amplified by cpu_hotspot mode.
	hotLeafIdx int
}

// Builder builds pprof Profile protos for a specific runtime + service.
// It caches the deterministic structural tables per ProfileType to ensure run-stable function sets.
type Builder struct {
	runtime string
	service string
	cache   map[string]*profileCache // keyed by ProfileType.Name + ":" + ProfileType.SampleType
}

// NewBuilder returns a Builder for the given runtime (go|jvm|python|node|dotnet|ebpf) and service name.
// The service name is embedded in app-level frame names; it must not contain customer or blueprint tokens.
func NewBuilder(runtime, service string) *Builder {
	return &Builder{
		runtime: runtime,
		service: service,
		cache:   make(map[string]*profileCache),
	}
}

// cacheKey returns the key used to index the frame-table cache.
func cacheKey(pt ProfileType) string {
	return pt.Name + ":" + pt.SampleType
}

// runtimeFrames returns the deterministic ordered frame list for a runtime + service.
// Frames are ordered root-first; the leaf (hottest) frame is last.
// All names are technology-generic — no customer/blueprint tokens.
func runtimeFrames(runtime, service string) []string {
	switch runtime {
	case "go":
		return []string{
			"runtime.main",
			"net/http.(*conn).serve",
			"net/http.HandlerFunc.ServeHTTP",
			fmt.Sprintf("%s.handleRequest", service),
			fmt.Sprintf("%s.queryBackend", service),
			"runtime.mallocgc",
			"runtime.gcBgMarkWorker",
		}
	case "jvm":
		return []string{
			"java.lang.Thread.run",
			"java.util.concurrent.ThreadPoolExecutor.runWorker",
			"org.springframework.web.servlet.DispatcherServlet.doDispatch",
			fmt.Sprintf("%s.handle", service),
		}
	default:
		// python, node, dotnet, ebpf, and anything else
		return []string{
			"main",
			fmt.Sprintf("%s.handler", service),
			"runtime.poll",
		}
	}
}

// hotLeafIndex returns the index of the designated hot leaf within the frames slice.
// For simplicity, the hottest frame is always the last one (the innermost call).
func hotLeafIndex(frames []string) int {
	if len(frames) == 0 {
		return 0
	}
	return len(frames) - 1
}

// intern interns s into the string table, returning its index.
// StringTable[0] is always "" (pprof convention).
func intern(st []string, idx map[string]int64, s string) ([]string, map[string]int64, int64) {
	if i, ok := idx[s]; ok {
		return st, idx, i
	}
	i := int64(len(st))
	st = append(st, s)
	idx[s] = i
	return st, idx, i
}

// buildCache constructs and caches the structural profile tables for pt.
// Called lazily on first Build for a given ProfileType.
func (b *Builder) buildCache(pt ProfileType) *profileCache {
	frames := runtimeFrames(b.runtime, b.service)
	hotIdx := hotLeafIndex(frames)

	// Initialise string table: index 0 must be "".
	st := []string{""}
	si := map[string]int64{"": 0}

	var internStr = func(s string) int64 {
		st, si, _ = intern(st, si, s)
		return si[s]
	}

	// Build a single Mapping covering all frames.
	mappingFilenameIdx := internStr(b.service)
	mapping := &pprofpb.Mapping{
		Id:           1,
		Filename:     mappingFilenameIdx,
		HasFunctions: true,
		HasFilenames: true,
	}

	// Build Function + Location per frame.
	funcs := make([]*pprofpb.Function, len(frames))
	locs := make([]*pprofpb.Location, len(frames))

	for i, frame := range frames {
		fid := uint64(i + 1)
		nameIdx := internStr(frame)
		sysNameIdx := internStr(frame) // system_name == name for synthetic profiles
		filenameIdx := internStr(b.service)
		funcs[i] = &pprofpb.Function{
			Id:         fid,
			Name:       nameIdx,
			SystemName: sysNameIdx,
			Filename:   filenameIdx,
			StartLine:  int64(i + 1),
		}
		locs[i] = &pprofpb.Location{
			Id:        fid,
			MappingId: 1,
			Address:   uint64(0x1000 + i*0x10),
			Line: []*pprofpb.Line{
				{FunctionId: fid, Line: int64(i + 1)},
			},
		}
	}

	// Build stacks and leaf weights.
	// We synthesise a flamegraph with multiple samples to make it interesting:
	//   - one sample per leaf: the full stack from root down to that leaf.
	// Base weights decrease with depth (root gets less exclusive time, leaf gets most).
	n := len(frames)
	stacks := make([][]uint64, n)
	leafWeights := make([]int64, n)
	for i := range frames {
		// stack is leaf-first: frames[i..0] reversed
		stack := make([]uint64, i+1)
		for j := 0; j <= i; j++ {
			stack[j] = locs[i-j].Id
		}
		stacks[i] = stack
		// Assign increasing weight to deeper frames so the flamegraph has a realistic taper.
		leafWeights[i] = int64((i + 1) * 100)
	}

	return &profileCache{
		stringTable: st,
		strIdx:      si,
		functions:   funcs,
		locations:   locs,
		mapping:     []*pprofpb.Mapping{mapping},
		leafWeights: leafWeights,
		stacks:      stacks,
		hotLeafIdx:  hotIdx,
	}
}

// getCache returns the cached tables for pt, building them on first access.
func (b *Builder) getCache(pt ProfileType) *profileCache {
	k := cacheKey(pt)
	if c, ok := b.cache[k]; ok {
		return c
	}
	c := b.buildCache(pt)
	b.cache[k] = c
	return c
}

// cloneStringTable returns a shallow copy of the string table and a fresh index map.
// buildProfile calls this on every build so per-call interning (SampleType/PeriodType
// strings + any span_id labels) never pollutes the cached tables.
func cloneStringTable(st []string, si map[string]int64) ([]string, map[string]int64) {
	newST := make([]string, len(st))
	copy(newST, st)
	newSI := make(map[string]int64, len(si))
	for k, v := range si {
		newSI[k] = v
	}
	return newST, newSI
}

// Build constructs a pprof Profile proto for the given ProfileType and Load.
// Structural tables (Function, Location, Mapping) are reused from the cache for determinism.
// Only values and time-varying fields change between calls.
func (b *Builder) Build(pt ProfileType, load Load) *pprofpb.Profile {
	c := b.getCache(pt)
	return b.buildProfile(pt, load, c.stringTable, c.strIdx, c.stacks, c.leafWeights, c.hotLeafIdx, nil)
}

// BuildWithSpans is like Build but tags a subset of samples with span_id labels.
// buildProfile works on a private copy of the cached string table, so the appended span
// strings never pollute the cache (subsequent Build calls stay span-free).
func (b *Builder) BuildWithSpans(pt ProfileType, load Load, spanIDs []string) *pprofpb.Profile {
	c := b.getCache(pt)
	return b.buildProfile(pt, load, c.stringTable, c.strIdx, c.stacks, c.leafWeights, c.hotLeafIdx, spanIDs)
}

// clamp01mode clamps v to [0, 1].
func clamp01mode(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// profileModeAmp returns the uniform sample-value amplification factor for pt given modes.
// It applies the formula (1 + 4*i) for each matching mode (k=4 gives up to 5× at i=1).
// If multiple matching modes are active, the highest intensity wins.
// Modes that do not match pt.Name have no effect (factor stays 1).
// cpu_hotspot is handled per-sample in buildProfile (hot-leaf concentration) and is NOT
// applied here.
//
// Matching rules:
//   - "memory_leak"     → pt.Name == "memory"
//   - "lock_contention" → pt.Name == "mutex" or pt.Name == "block"
//   - "goroutine_leak"  → pt.Name == "goroutines" or pt.Name == "goroutine"
func profileModeAmp(pt ProfileType, modes map[string]float64) float64 {
	if len(modes) == 0 {
		return 1.0
	}
	best := 0.0
	for name, intensity := range modes {
		i := clamp01mode(intensity)
		if i <= 0 {
			continue
		}
		switch name {
		case "memory_leak":
			if pt.Name == "memory" && i > best {
				best = i
			}
		case "lock_contention":
			if (pt.Name == "mutex" || pt.Name == "block") && i > best {
				best = i
			}
		case "goroutine_leak":
			if (pt.Name == "goroutines" || pt.Name == "goroutine") && i > best {
				best = i
			}
			// cpu_hotspot: handled per-sample (hot-leaf concentration), not here.
		}
	}
	if best <= 0 {
		return 1.0
	}
	return 1.0 + 4.0*best
}

// buildProfile is the shared implementation for Build and BuildWithSpans.
// spanIDs may be nil (no span labels).
func (b *Builder) buildProfile(
	pt ProfileType,
	load Load,
	stringTable []string,
	strIdx map[string]int64,
	stacks [][]uint64,
	leafWeights []int64,
	hotLeafIdx int,
	spanIDs []string,
) *pprofpb.Profile {
	// Work on a PRIVATE copy of the cache's string table + index. The cache MUST stay immutable
	// across calls: interning the SampleType/PeriodType strings (and any span_id label strings)
	// straight into the shared index would record positions past len(cache.stringTable), desyncing
	// the (table, index) pair. A later BuildWithSpans then clones that inconsistent pair and appends
	// span strings into the stale slots, silently rewriting SampleType/PeriodType to point at
	// "span_id" + the hex span value (the `block:span_id:<hex>::` cardinality bug — see
	// TestBuildThenBuildWithSpansKeepsSampleType). Cloning here is the single source of immutability.
	stringTable, strIdx = cloneStringTable(stringTable, strIdx)

	// Helper: intern into the private (cloned) string table.
	internLocal := func(s string) int64 {
		if i, ok := strIdx[s]; ok {
			return i
		}
		i := int64(len(stringTable))
		stringTable = append(stringTable, s)
		strIdx[s] = i
		return i
	}

	// Build SampleType and PeriodType from the ProfileType fields.
	sampleTypeIdx := internLocal(pt.SampleType)
	sampleUnitIdx := internLocal(pt.SampleUnit)
	periodTypeIdx := internLocal(pt.PeriodType)
	periodUnitIdx := internLocal(pt.PeriodUnit)

	// ptModeAmp: uniform amplifier for non-cpu profile types (memory/mutex/block/goroutine).
	// cpu_hotspot is handled per-sample (hot-leaf concentration) further below.
	ptModeAmp := profileModeAmp(pt, load.Modes)

	// Build span label key index once (if we have span IDs).
	var spanKeyIdx int64
	if len(spanIDs) > 0 {
		spanKeyIdx = internLocal("span_id")
	}

	// Intern span value strings upfront so they appear in the final string table.
	spanValIdx := make([]int64, len(spanIDs))
	for i, sid := range spanIDs {
		spanValIdx[i] = internLocal(sid)
	}

	// Build samples.
	samples := make([]*pprofpb.Sample, len(stacks))
	factor := load.Factor
	if factor <= 0 {
		factor = 1.0
	}
	for i, stack := range stacks {
		w := leafWeights[i]
		// For cpu_hotspot: concentrate on the hot leaf (existing behaviour).
		// For other modes: amplify all samples uniformly.
		var amp float64
		if pt.Name == "process_cpu" {
			if load.Modes != nil {
				if v, ok := load.Modes["cpu_hotspot"]; ok && v > 0 {
					// Hot-leaf gets full cpu_hotspot amplification; other leaves get 1.
					if i == hotLeafIdx {
						amp = 1 + 4*clamp01mode(v)
					} else {
						amp = 1.0
					}
				} else {
					amp = 1.0
				}
			} else {
				amp = 1.0
			}
		} else {
			// memory, mutex, block, goroutines: uniform amplification.
			amp = ptModeAmp
		}
		val := int64(float64(w) * factor * amp)
		if val < 1 {
			val = 1
		}

		sm := &pprofpb.Sample{
			LocationId: stack,
			Value:      []int64{val},
		}

		// Attach span labels to the leaf sample (index 0 = deepest/hottest frame).
		if len(spanIDs) > 0 && i == len(stacks)-1 {
			for si2, sidIdx := range spanValIdx {
				_ = si2
				sm.Label = append(sm.Label, &pprofpb.Label{
					Key: spanKeyIdx,
					Str: sidIdx,
				})
			}
		}
		samples[i] = sm
	}

	// Retrieve cached structural tables (Function, Location, Mapping from the builder's cache).
	cached := b.getCache(pt)

	return &pprofpb.Profile{
		SampleType: []*pprofpb.ValueType{
			{Type: sampleTypeIdx, Unit: sampleUnitIdx},
		},
		Sample:   samples,
		Mapping:  cached.mapping,
		Location: cached.locations,
		Function: cached.functions,
		// Use the (possibly augmented) string table from this call.
		StringTable: stringTable,
		PeriodType:  &pprofpb.ValueType{Type: periodTypeIdx, Unit: periodUnitIdx},
		Period:      pt.Period,
		TimeNanos:   load.Now.UnixNano(),
		// 60s cadence in nanoseconds.
		DurationNanos: 60_000_000_000,
	}
}
