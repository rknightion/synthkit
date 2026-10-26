// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/state"
	"github.com/rknightion/synthkit/internal/telemetryspec"
)

// isDerived reports whether a metric reads another metric on its node (stock_of / count_of)
// instead of evaluating only its own value model in declaration order.
func isDerived(spec telemetryspec.MetricSpec) bool {
	return spec.StockOf != nil || spec.CountOf != ""
}

// incKey keys a counter's per-tick increment by name and DSL label combination (node-local).
func incKey(name string, dsl map[string]string) string {
	return name + "\x00" + state.LabelSig(dsl)
}

// carryCount converts a fractional per-tick observation rate into a whole count, carrying the
// remainder per series so the long-run count equals the rate exactly. No RNG is consumed.
func (w *Workload) carryCount(name string, labels map[string]string, rate float64) int {
	if w.carry == nil {
		w.carry = make(map[string]float64)
	}
	key := name + "\x00" + state.LabelSig(labels)
	prev, seen := w.carry[key]
	if !seen {
		prev = 0.5 // round half up from the first tick: rate 0.5 observes on ticks 1, 3, 5, ...
	}
	c := prev + rate
	if c < 0 {
		c = 0
	}
	n := math.Floor(c)
	w.carry[key] = c - n
	return int(n)
}

// hasDerived reports whether a node needs its counter increments recorded each tick: it
// declares a stock_of / count_of spec that reads them, or another node's count_of_service names
// it as the counter's source. The interpreter records increments only where something reads them.
func (n *node) hasDerived() bool {
	if n.exportsInc {
		return true
	}
	for _, spec := range n.metrics {
		if isDerived(spec) {
			return true
		}
	}
	return false
}

// nodeIncs is one exporting node's recorded counter increments of one tick (the map the node's
// Tick pass fills), stamped with the tick time so a reader never replays an older tick.
type nodeIncs struct {
	at   time.Time
	incs map[string]float64
}

// publishIncs makes the increments an exporting node records this tick readable by the nodes
// that name it in count_of_service. It stores the node's live map, so later counter specs of the
// same pass are visible without a copy, and a new tick's map replaces the old one.
func (w *Workload) publishIncs(service string, at time.Time, incs map[string]float64) {
	if g := w.graph; g == nil || g.byName[service] == nil || !g.byName[service].exportsInc {
		return
	}
	if w.exported == nil {
		w.exported = make(map[string]nodeIncs)
	}
	w.exported[service] = nodeIncs{at: at, incs: incs}
}

// sourceIncs resolves the counter source of a count_of spec on node n: the node's own series and
// this tick's increments, or, when the spec names another service, that service's counter series
// and its increments recorded at tick time now (none when it recorded nothing this tick, such as
// a paused source).
func (w *Workload) sourceIncs(n *node, spec telemetryspec.MetricSpec, own map[string]float64, now time.Time) ([]counterSeries, map[string]float64) {
	from := n
	if spec.CountOfService != "" && spec.CountOfService != n.decl.Name {
		from = w.graph.byName[spec.CountOfService]
		if from == nil {
			return nil, nil // unreachable: buildGraph rejects an unknown source service
		}
		rec := w.exported[spec.CountOfService]
		if !rec.at.Equal(now) {
			return from.counterSeries(spec.CountOf), nil
		}
		return from.counterSeries(spec.CountOf), rec.incs
	}
	return from.counterSeries(spec.CountOf), own
}

// observeDerived evaluates one stock_of gauge or count_of histogram after the node's ordinary
// specs, from the counter increments recorded this tick (incs). dt is the elapsed tick in
// seconds and ctx.Now the tick time. paused steps stocks with zero inflow and observes no
// histogram values. world supplies an item stock's stall_mode incident state.
func (w *Workload) observeDerived(st, native *state.State, id nodeIdentity, n *node, spec telemetryspec.MetricSpec, ctx telemetryspec.EvalCtx, incs map[string]float64, dt float64, paused bool, world *core.World) {
	base := id.metricBaseLabels()
	switch {
	case spec.StockOf != nil:
		src := n.counterSeries(spec.StockOf.Metric)
		if len(src) == 0 {
			return // unreachable: buildGraph rejects a missing source counter
		}
		for _, combo := range stockCombos(spec, src) {
			var inc float64
			if !paused {
				inc = stockInc(incs, spec, src, combo)
			}
			labels := mergeLabels(base, combo)
			var s float64
			if spec.StockOf.Residence != nil {
				var arrivals func() func(time.Time) float64
				if !paused {
					arrivals = func() func(time.Time) float64 {
						return stockArrivals(world, id, spec, src, combo, incs, dt)
					}
				}
				s = w.stepItemStock(id, spec.StockOf, labels, ctx.Now, inc, dt, world, arrivals)
			} else {
				s = w.stepFluidStock(spec, labels, inc, dt)
			}
			if st != nil {
				st.Set(spec.Name, labels, s)
			}
			if native != nil {
				native.Set(spec.Name, combo, s)
			}
		}
	case spec.CountOf != "":
		if paused {
			return
		}
		src, srcIncs := w.sourceIncs(n, spec, incs, ctx.Now)
		if len(src) == 0 {
			return // unreachable: buildGraph rejects a missing source counter
		}
		for _, combo := range scaledLabelCombos(spec.Labels) {
			labels := mergeLabels(base, combo.labels)
			// The histogram series observes the counter increments of every series that
			// projects onto it: one series when the labels are equal, a sum for a label subset.
			rate := projectedInc(srcIncs, spec.CountOf, src, combo.labels)
			k := min(w.carryCount(spec.Name, labels, rate), telemetryspec.MaxObservationsPerTick)
			for range k {
				val, _ := spec.Value.Eval(ctx)
				w.observeHistogram(st, native, spec, labels, combo.labels, val*combo.value)
			}
		}
	}
}

// stepFluidStock moves a fluid stock_of gauge one tick toward inflow rate × dwell.
func (w *Workload) stepFluidStock(spec telemetryspec.MetricSpec, labels map[string]string, inc, dt float64) float64 {
	dwell := spec.StockOf.DwellSeconds
	if w.stock == nil {
		w.stock = make(map[string]float64)
	}
	target := inc / dt * dwell
	// Key by the full series labels: the node identity keeps two nodes declaring the same stock
	// gauge name independent.
	key := spec.Name + "\x00" + state.LabelSig(labels)
	s, seen := w.stock[key]
	if !seen {
		s = target
	} else {
		s = target + (s-target)*math.Exp(-dt/dwell)
	}
	w.stock[key] = s
	return s
}

// counterSeries is one series of a resolved counter: its DSL labels and the largest increment
// it can make in one tick at master volume 1, summed over every spec that declares the series.
type counterSeries struct {
	labels   map[string]string
	max      float64
	bounded  bool // false when a declaring spec's value model is unbounded (normal, lognormal)
	srcs     []counterSrc
	modelled bool
}

// counterSrc is one spec's contribution to a counter series: its value model, the enum
// rate_scale of the series' combination, and the clock the value reads its load from (the
// series' own or the node's; nil reads the Engine's curve). exact means an exact zero load reads
// 0 (a declared clock) rather than the 1.0 fallback. A counterSeries lists one counterSrc per
// declaring spec in srcs, and modelled is true when every one is a shape model, so the series'
// arrival rate at any instant can be re-evaluated (a stock seeds from its past arrivals).
type counterSrc struct {
	value telemetryspec.ValueModel
	rate  float64
	load  *loadProfile
	exact bool
}

// perTick is the source's expected increment per metric tick at instant at: the value model read
// at the load of at, the way a tick at that time reads it, under the scheduled incidents only
// (a live fault speaks for now, not for the past).
func (c counterSrc) perTick(at time.Time, eng *shape.Engine, service string) float64 {
	ctx := telemetryspec.EvalCtx{ShapeVal: c.load.business(at, eng), ExactLoad: c.exact}
	if sh := c.value.Shape; sh != nil && sh.Mode != "" {
		_, intensity := eng.EvalScheduled(at, sh.Mode, service)
		if sh.IncidentValue != nil {
			ctx.IncidentIntensity = intensity
		} else {
			ctx.ShapeVal *= 1 + (incidentAmplification-1)*intensity
		}
	}
	v, _ := c.value.Eval(ctx)
	return v * c.rate
}

// counterSeries resolves a counter name to the series of EVERY non-derived counter spec of that
// name on the node (a blueprint may declare one spec per series), in declaration then label
// order, each series once. It is nil when the node declares no such counter. This is the shared
// resolver for stock_of and count_of: their readers sum this tick's increments over it.
func (n *node) counterSeries(name string) []counterSeries {
	var out []counterSeries
	at := map[string]int{}
	for i := range n.metrics {
		m := &n.metrics[i]
		if m.Name != name || m.Instrument != telemetryspec.InstrumentCounter || isDerived(*m) {
			continue
		}
		nominal, bounded := nominalMax(m.Value)
		for ci, c := range scaledLabelCombos(m.Labels) {
			sig := state.LabelSig(c.labels)
			j, seen := at[sig]
			if !seen {
				j = len(out)
				at[sig] = j
				out = append(out, counterSeries{labels: c.labels, bounded: true, modelled: true})
			}
			out[j].max += nominal * c.rate
			out[j].bounded = out[j].bounded && bounded
			// The clock a tick reads: the combination's own when it declares one, else the node's.
			load, exact := n.load, n.load != nil
			if i < len(n.seriesLoads) && ci < len(n.seriesLoads[i]) && n.seriesLoads[i][ci] != nil {
				load, exact = n.seriesLoads[i][ci], true
			}
			out[j].srcs = append(out[j].srcs, counterSrc{value: m.Value, rate: c.rate, load: load, exact: exact})
			out[j].modelled = out[j].modelled && m.Value.Kind() == telemetryspec.KindShape
		}
	}
	return out
}

// projectedInc sums this tick's increments of the counter series that project onto target
// (every target label carries the series' value; an empty target takes the whole counter).
func projectedInc(incs map[string]float64, name string, src []counterSeries, target map[string]string) float64 {
	var sum float64
	for _, c := range src {
		if projects(target, c.labels) {
			sum += incs[incKey(name, c.labels)]
		}
	}
	return sum
}

// stockCombos is a stock_of gauge's label combinations: its own labels (a projection of the
// counter's) when declared, otherwise one per counter series.
func stockCombos(spec telemetryspec.MetricSpec, src []counterSeries) []map[string]string {
	if len(spec.Labels) != 0 {
		return labelCombos(spec.Labels)
	}
	out := make([]map[string]string, len(src))
	for i, c := range src {
		out[i] = c.labels
	}
	return out
}

// stockInc is one stock series' arrivals this tick: the projected counter increments when the
// gauge declares labels, otherwise exactly its own counter series (an inherited series never
// sums a wider series of another same-named spec).
func stockInc(incs map[string]float64, spec telemetryspec.MetricSpec, src []counterSeries, combo map[string]string) float64 {
	if len(spec.Labels) != 0 {
		return projectedInc(incs, spec.StockOf.Metric, src, combo)
	}
	return incs[incKey(spec.StockOf.Metric, combo)]
}

// stockZ95 is the standard-normal 0.95 quantile (a lognormal's p95 is median × exp(sigma × z)).
const stockZ95 = 1.6448536269514722

// itemStock is one item-stock series: whole items in arrival order, each completing when the
// series' progress clock passes its own completion point. The progress clock advances with the
// tick time scaled by (1 - stall intensity), so a full stall freezes every item.
type itemStock struct {
	stepped time.Time   // last tick stepped (two gauges sharing the stock step it once)
	carry   float64     // fractional arrivals carried to the next tick
	v       float64     // progress clock, seconds
	items   []stockItem // arrival order; items[i] has sequence base+i
	base    int64
	head    int     // items[:head] have completed
	due     []int64 // min-heap of item sequences by completion point
	n       int     // items still in the stock
}

type stockItem struct {
	at   int64   // arrival time, Unix nanoseconds
	done float64 // progress-clock completion point
	gone bool
}

func (s *itemStock) item(seq int64) *stockItem { return &s.items[seq-s.base] }

func (s *itemStock) less(i, j int) bool { return s.item(s.due[i]).done < s.item(s.due[j]).done }

func (s *itemStock) push(at int64, done float64) {
	s.items = append(s.items, stockItem{at: at, done: done})
	s.due = append(s.due, s.base+int64(len(s.items)-1))
	s.n++
	for i := len(s.due) - 1; i > 0; {
		p := (i - 1) / 2
		if !s.less(i, p) {
			break
		}
		s.due[i], s.due[p] = s.due[p], s.due[i]
		i = p
	}
}

func (s *itemStock) pop() {
	last := len(s.due) - 1
	s.item(s.due[0]).gone = true
	s.n--
	s.due[0] = s.due[last]
	s.due = s.due[:last]
	for i := 0; ; {
		l, r, m := 2*i+1, 2*i+2, i
		if l < len(s.due) && s.less(l, m) {
			m = l
		}
		if r < len(s.due) && s.less(r, m) {
			m = r
		}
		if m == i {
			break
		}
		s.due[i], s.due[m] = s.due[m], s.due[i]
		i = m
	}
}

// complete removes every item whose completion point the progress clock has passed and drops
// completed items from the front of the arrival order.
func (s *itemStock) complete() {
	for len(s.due) > 0 && s.item(s.due[0]).done <= s.v {
		s.pop()
	}
	for s.head < len(s.items) && s.items[s.head].gone {
		s.head++
	}
	if s.head > 1024 && 2*s.head > len(s.items) {
		s.items = append([]stockItem(nil), s.items[s.head:]...)
		s.base += int64(s.head)
		s.head = 0
	}
}

func (s *itemStock) read(field string, now time.Time) float64 {
	if field == telemetryspec.StockFieldOldestAge {
		if s.n == 0 {
			return 0
		}
		return float64(now.UnixNano()-s.items[s.head].at) / 1e9
	}
	return float64(s.n)
}

// stockNorm maps a uniform [0,1) draw to a standard-normal value (open interval, so finite).
func stockNorm(u float64) float64 {
	return math.Sqrt2 * math.Erfinv(2*(u+1.0/(1<<54))-1)
}

// stockSources are the counter series one stock series reads: the series projecting onto the
// stock's labels when it declares them, otherwise exactly the series sharing its labels.
func stockSources(spec telemetryspec.MetricSpec, src []counterSeries, combo map[string]string) []counterSeries {
	var out []counterSeries
	for _, c := range src {
		if len(spec.Labels) != 0 {
			if projects(combo, c.labels) {
				out = append(out, c)
			}
		} else if state.LabelSig(c.labels) == state.LabelSig(combo) {
			out = append(out, c)
		}
	}
	return out
}

// stockArrivals is the arrival rate (items per second) a stock series saw at an earlier instant,
// for seeding its population. A series whose every source is a shape model is re-evaluated at that
// instant, so a diurnal or weekly curve shapes the history; any other series (a constant, a
// random draw, a read of another metric) holds this tick's increment, flat. world and the
// increments are read when the function is built, so it is built on the first tick only.
func stockArrivals(world *core.World, id nodeIdentity, spec telemetryspec.MetricSpec, src []counterSeries, combo map[string]string, incs map[string]float64, dt float64) func(time.Time) float64 {
	var flat float64
	var modelled []counterSrc
	for _, c := range stockSources(spec, src, combo) {
		if c.modelled && world != nil && world.Shape != nil {
			modelled = append(modelled, c.srcs...)
		} else {
			flat += incs[incKey(spec.StockOf.Metric, c.labels)]
		}
	}
	service := id.service
	return func(at time.Time) float64 {
		perTick := flat
		for _, m := range modelled {
			perTick += m.perTick(at, world.Shape, service)
		}
		return perTick / dt
	}
}

// Seed grid: the arrival curve is integrated over the residence window preceding the first tick
// in at most stockSeedMaxCells midpoint cells (never fewer than stockSeedMinCells, about one per
// minute between), so a seed costs a few thousand rate evaluations however long the residence.
const (
	stockSeedTailZ    = 4.0 // the window reaches the residence quantile z = 4 (all but 3e-5 of items)
	stockSeedMinCells = 64
	stockSeedMaxCells = 4096
	stockSeedCellSecs = 60.0
)

// lognormalCDF is P(residence <= x). A zero sigma is a fixed residence that completes at the median.
func lognormalCDF(x, median, sigma float64) float64 {
	if !(x > 0) {
		return 0
	}
	if sigma == 0 {
		if x < median {
			return 0
		}
		return 1
	}
	return 0.5 * math.Erfc(-math.Log(x/median)/(sigma*math.Sqrt2))
}

// stockSeed is one seeded item: its arrival time (Unix nanoseconds) and its progress-clock
// completion point (the remaining residence at the seed).
type stockSeed struct {
	at   int64
	done float64
}

// seedItems is the population of an infinite-server delay at now whose arrivals followed rate
// (items per second) for ever: the expected count is the integral over past arrivals of
// rate(a) x P(residence > now - a), taken over the residence window with midpoint cells. Each
// item's age is a stratified quantile of that integrand (one per 1/count slice of its mass), and
// its remaining residence is drawn from the residence conditioned on exceeding that age, so
// ages and completions are consistent. Draws are hashes of the stock key and tick (draw), so it
// is a pure function of the tick time and the curve.
func seedItems(res *telemetryspec.Lognormal, sigma float64, nowNs int64, rate func(time.Time) float64, draw func(string) float64) []stockSeed {
	horizon := res.Median
	if sigma > 0 {
		horizon = res.Median * math.Exp(sigma*stockSeedTailZ)
	}
	cells := min(max(int(horizon/stockSeedCellSecs)+1, stockSeedMinCells), stockSeedMaxCells)
	step := horizon / float64(cells)
	cum := make([]float64, cells)
	var total float64
	for j := range cells {
		mid := (float64(j) + 0.5) * step
		r := rate(time.Unix(0, nowNs-int64(mid*1e9)))
		if math.IsInf(r, 1) {
			r = 1e12
		}
		if !(r > 0) { // zero, negative or NaN
			r = 0
		}
		total += r * (1 - lognormalCDF(mid, res.Median, sigma)) * step
		cum[j] = total
	}
	want := math.Round(total)
	if !(want > 0) {
		return nil
	}
	cnt := int(min(want, telemetryspec.MaxStockItems))
	seeds := make([]stockSeed, cnt)
	for i := range seeds {
		p := strconv.Itoa(i)
		q := (float64(i) + draw("seed/"+p+"/age")) / float64(cnt) * total
		j := min(sort.SearchFloat64s(cum, q), cells-1)
		lo := 0.0
		if j > 0 {
			lo = cum[j-1]
		}
		frac := 0.5
		if m := cum[j] - lo; m > 0 {
			frac = min(max((q-lo)/m, 0), 1)
		}
		age := (float64(j) + frac) * step
		rem := res.Median - age
		if sigma > 0 {
			// Residence given it exceeds the age: invert the CDF over (F(age), 1).
			fa := lognormalCDF(age, res.Median, sigma)
			u := min(fa+draw("seed/"+p+"/residence")*(1-fa), 1-1e-12)
			rem = res.Median*math.Exp(sigma*stockNorm(u)) - age
		}
		seeds[i] = stockSeed{at: nowNs - int64(age*1e9), done: max(rem, 1e-6)}
	}
	sort.SliceStable(seeds, func(i, j int) bool { return seeds[i].at < seeds[j].at })
	return seeds
}

// stepItemStock steps the item stock behind one gauge series to the tick time now (once per
// tick however many gauges read it) and returns the gauge's reading. inc is this tick's
// (fractional) arrivals. The first tick seeds the population an infinite-server delay would hold
// at now (see seedItems), from the arrival curve over the residence window before it when
// arrivals (built on demand) supplies one, else from this tick's rate held flat: a restart at the
// plateau or at night starts at the level the stock would have reached, not at rate x mean for
// that instant. Later ticks spread the whole arrivals evenly over the tick. Every draw hashes the
// stock key and tick time.
func (w *Workload) stepItemStock(id nodeIdentity, so *telemetryspec.StockOf, labels map[string]string, now time.Time, inc, dt float64, world *core.World, arrivals func() func(time.Time) float64) float64 {
	res := so.Residence
	key := "items\x00" + so.Metric + "\x00" + formatNum(res.Median) + "\x00" + formatNum(res.P95) + "\x00" + so.StallMode + "\x00" + state.LabelSig(labels)
	if w.items == nil {
		w.items = make(map[string]*itemStock)
	}
	s := w.items[key]
	first := s == nil
	if first {
		s = &itemStock{carry: 0.5}
		w.items[key] = s
	}
	if !first && !now.After(s.stepped) {
		return s.read(so.EffectiveField(), now)
	}
	s.stepped = now
	sigma := math.Log(res.P95/res.Median) / stockZ95
	nowNs := now.UnixNano()
	draw := causal.UnitDraw(key + "\x00" + strconv.FormatInt(nowNs, 10))
	if first {
		var rate func(time.Time) float64
		if arrivals != nil {
			rate = arrivals()
		} else { // paused: no inflow. A negative counter increment or NaN also seeds an empty stock.
			flat := inc / dt
			rate = func(time.Time) float64 { return flat }
		}
		for _, sd := range seedItems(res, sigma, nowNs, rate, draw) {
			s.push(sd.at, sd.done)
		}
		s.complete()
		return s.read(so.EffectiveField(), now)
	}
	speed := 1.0
	if so.StallMode != "" && world != nil && world.Shape != nil {
		if active, intensity := world.Shape.Eval(now, so.StallMode, id.service); active {
			speed = 1 - min(max(intensity, 0), 1)
		}
	}
	v0 := s.v
	s.v += dt * speed
	c := max(s.carry+inc, 0)
	k := math.Floor(c)
	s.carry = c - k
	for j := range int(k) {
		if s.n >= telemetryspec.MaxStockItems {
			break // dropped, never carried
		}
		frac := (float64(j) + 0.5) / k
		at := nowNs - int64(dt*(1-frac)*1e9)
		residence := res.Median * math.Exp(sigma*stockNorm(draw("arrival/"+strconv.Itoa(j))))
		s.push(at, v0+dt*frac*speed+residence)
	}
	s.complete()
	return s.read(so.EffectiveField(), now)
}
