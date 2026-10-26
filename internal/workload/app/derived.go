// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
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
				s = w.stepItemStock(id, spec.StockOf, labels, ctx.Now, inc, dt, world)
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
	labels  map[string]string
	max     float64
	bounded bool // false when a declaring spec's value model is unbounded (normal, lognormal)
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
		for _, c := range scaledLabelCombos(m.Labels) {
			sig := state.LabelSig(c.labels)
			j, seen := at[sig]
			if !seen {
				j = len(out)
				at[sig] = j
				out = append(out, counterSeries{labels: c.labels, bounded: true})
			}
			out[j].max += nominal * c.rate
			out[j].bounded = out[j].bounded && bounded
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

// stepItemStock steps the item stock behind one gauge series to the tick time now (once per
// tick however many gauges read it) and returns the gauge's reading. inc is this tick's
// (fractional) arrivals. The first tick seeds the steady-state population of an
// infinite-server delay at this tick's arrival rate: rate × the residence mean items, each with
// a length-biased residence and an elapsed share drawn uniformly. Later ticks spread the whole
// arrivals evenly over the tick. Every draw hashes the stock key and tick time.
func (w *Workload) stepItemStock(id nodeIdentity, so *telemetryspec.StockOf, labels map[string]string, now time.Time, inc, dt float64, world *core.World) float64 {
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
		// Items present in a stationary infinite-server delay: count rate × E[S]; each one's
		// residence is length-biased (lognormal median × exp(sigma²)) and its elapsed time is a
		// uniform share of it.
		mean := res.Median * math.Exp(sigma*sigma/2)
		want := math.Round(inc / dt * mean)
		if !(want > 0) { // a negative counter increment or NaN seeds an empty stock
			want = 0
		}
		cnt := int(min(want, telemetryspec.MaxStockItems))
		type seed struct {
			at   int64
			done float64
		}
		seeds := make([]seed, cnt)
		for i := range seeds {
			p := strconv.Itoa(i)
			full := res.Median * math.Exp(sigma*sigma+sigma*stockNorm(draw("seed/"+p+"/residence")))
			// Stratified elapsed shares (one per 1/cnt slice) keep the seeded completions even.
			age := full * (float64(i) + draw("seed/"+p+"/age")) / float64(cnt)
			seeds[i] = seed{at: nowNs - int64(age*1e9), done: full - age}
		}
		sort.SliceStable(seeds, func(i, j int) bool { return seeds[i].at < seeds[j].at })
		for _, sd := range seeds {
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
