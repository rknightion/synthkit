// SPDX-License-Identifier: AGPL-3.0-only

// Package solace implements the "solace" construct.
//
// Kind:     "solace"
// Scope:    core.ScopeSubstrate - identity is the declared scrape instance (job + instance); no blueprint label (I21)
// Group:    core.GroupIntegration
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
//
// Emits the families the community solace_prometheus_exporter (github.com/solacecommunity/
// solace-prometheus-exporter) publishes for event brokers, pinned to release tag v1.21.0. Every name,
// label key and target name below is read from that tag's internal/semp sources; see signals/solace.md
// [slug: solace-identity] for the line-level citations. The exporter adds no broker label: a broker is the
// scrape instance, and per-queue and per-VPN series carry only vpn_name and queue_name.
//
// Scrape targets emulated (config `targets`): Vpn, VpnStats, VpnSpool, QueueDetails, QueueStats,
// QueueRates (opt-in, deprecated upstream), GlobalStats, GlobalSystemInfo, Spool.
// Each emits solace_up{endpoint=<target>}.
//
// Model: each queue receives business-hours-shaped traffic and is drained by consumers at a bounded
// rate; its depth integrates the difference, so a consumer stall shows as a growing spool. Queue
// traffic rolls up into VPN and broker totals, so the families agree with one another. A queue may
// declare its own rate_scale and shape_profile; undeclared queues follow the blueprint curve. A
// queue may also declare a drain_schedule (period, window, phase): its consumers then run only
// inside each epoch-aligned window, so the depth is a sawtooth that rises between windows and
// falls to near zero inside them.
//
// Causal binding (opt-in): a queue that names a causal `queue` resource (environments[].resources,
// internal/causal) takes its flows from that resource's model window instead: publishes are the
// admitted arrivals, deletes the served messages, depth the backlog, spool usage the backlog times
// avg_msg_bytes within spool_quota_mb, and spool-usage discards the arrivals the full spool
// rejected. The construct invents no names for it; unbound queues keep the model above unchanged.
//
// Signal contract: signals/solace.md [slug: solace-queue], [slug: solace-vpn], [slug: solace-system]
package solace

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"sync"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

// Kind is the registry key.
const Kind = "solace"

const interval = 60 * time.Second

// ampLoad is the per-queue Spread+Wander magnitude on the publish rate, so peer queues sharing
// one declared rate do not move in lockstep.
const ampLoad = 0.15

// Construct is the per-instance solace renderer. Not exported; callers use Build.
type Construct struct {
	job     string
	targets map[string]bool
	order   []string
	brokers []*resolvedBroker

	// model is the causal model of the bound queues' resources; nil when no queue is bound.
	model *causal.Model

	mu     sync.Mutex
	st     *state.State
	last   time.Time
	from   time.Time          // start of the window being rendered: (from, last]
	levels map[string]float64 // last level written through counterLevel, per series
}

// Compile-time interface checks.
var (
	_ core.Construct  = (*Construct)(nil)
	_ causal.Advancer = (*Construct)(nil)
)

// Build validates cfg and returns a ready core.Construct. A broker's identity is declared, not
// shared with another construct, so fx carries nothing this construct uses, except that a
// per-environment fan-out (for_each_env or envs) is rejected: it would instantiate the same
// declared brokers once per environment, writing identical unlabelled series.
func Build(cfg any, fx *fixture.Set) (core.Construct, error) {
	c, ok := cfg.(*Config)
	if !ok || c == nil {
		return nil, fmt.Errorf("solace: Build called with %T, want *Config", cfg)
	}
	if fx != nil && fx.Env != nil {
		return nil, fmt.Errorf("solace: does not fan out per environment (for_each_env/envs): declared brokers are substrate identity and would be emitted once per environment")
	}
	job, targets, order, brokers, err := resolve(c)
	if err != nil {
		return nil, err
	}
	con := &Construct{job: job, targets: targets, order: order, brokers: brokers, st: state.NewState(), levels: map[string]float64{}}
	if set, bound := boundSet(c.causal, brokers); bound {
		if con.model, err = causal.Compile(set); err != nil {
			return nil, fmt.Errorf("solace: %w", err)
		}
	}
	return con, nil
}

// boundSet is the declared set with each bound resource's depth limit set to what its queue's
// spool quota holds (spool_quota_mb / avg_msg_bytes), so the model rejects exactly the arrivals
// the spool discards. The caller's set is not modified. bound reports whether a queue is bound.
func boundSet(base causal.Set, brokers []*resolvedBroker) (set causal.Set, bound bool) {
	depth := map[string]float64{}
	for _, b := range brokers {
		for _, v := range b.vpns {
			for _, q := range v.queues {
				if q.resource != "" {
					depth[q.resource] = q.maxDepth()
				}
			}
		}
	}
	if len(depth) == 0 {
		return base, false
	}
	set.Loads = base.Loads
	for _, d := range base.Envs {
		d.Resources = append([]causal.Resource(nil), d.Resources...)
		for i := range d.Resources {
			if n, ok := depth[d.Resources[i].Name]; ok {
				d.Resources[i].MaxDepth = n
			}
		}
		set.Envs = append(set.Envs, d)
	}
	return set, true
}

// AdvanceCausal advances the bound queues' model to now (causal.Advancer). The runner calls it
// every master tick, so this construct reads the same live faults as every other consumer of the
// blueprint's resources. A construct with no bound queue has nothing to advance.
func (c *Construct) AdvanceCausal(now time.Time, clk causal.Clock) {
	if c.model != nil {
		c.model.Advance(now, clk)
	}
}

func (c *Construct) Kind() string                { return Kind }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *Construct) Interval() time.Duration     { return interval }

// Tick renders one observation window into w.Metrics.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	dt := interval
	if !c.last.IsZero() && now.After(c.last) {
		dt = now.Sub(c.last)
	}
	c.last = now
	c.from = now.Add(-dt)
	if c.model != nil && w != nil && w.Shape != nil {
		c.model.Advance(now, w.Shape) // idempotent after the runner's AdvanceCausal
	}
	c.render(now, dt, w)
	if w != nil && w.Metrics != nil {
		return w.Metrics.Write(ctx, c.st.Collect(now))
	}
	return nil
}

// qres is one queue's result for the tick, used to roll up to the VPN and broker.
type qres struct {
	rx, tx       float64 // messages accepted into / delivered out of the queue
	exceeded     float64 // messages discarded because the queue spool was full
	rxBytes      float64
	txBytes      float64
	depth        float64
	depthBytes   float64
	consumers    int
	subscription int
}

func (c *Construct) render(now time.Time, dt time.Duration, w *core.World) {
	secs := dt.Seconds()
	bf := 1.0
	if w != nil && w.Shape != nil {
		bf = w.Shape.BusinessFactor(now)
	}

	for _, b := range c.brokers {
		var (
			sysRx, sysTx, sysRxB, sysTxB, sysExceeded float64
			sysClients, sysDepth, sysDepthBytes       float64
			sysQueues, sysConsumerFlows, sysPubFlows  float64
		)

		for _, v := range b.vpns {
			var tot qres
			for _, q := range v.queues {
				r := c.stepQueue(w, now, bf, secs, b, v, q)
				tot.rx += r.rx
				tot.tx += r.tx
				tot.exceeded += r.exceeded
				tot.rxBytes += r.rxBytes
				tot.txBytes += r.txBytes
				tot.depth += r.depth
				tot.depthBytes += r.depthBytes
				tot.consumers += r.consumers
				tot.subscription += r.subscription
			}
			conns := float64(v.publishers + tot.consumers)
			c.renderVPN(b, v, tot, conns)

			sysRx += tot.rx
			sysTx += tot.tx
			sysRxB += tot.rxBytes
			sysTxB += tot.txBytes
			sysExceeded += tot.exceeded
			sysClients += conns
			sysDepth += tot.depth
			sysDepthBytes += tot.depthBytes
			sysQueues += float64(len(v.queues))
			sysConsumerFlows += float64(tot.consumers)
			sysPubFlows += float64(v.publishers)
		}

		c.renderBroker(secs, b, brokerTotals{
			rx: sysRx, tx: sysTx, rxBytes: sysRxB, txBytes: sysTxB, exceeded: sysExceeded,
			clients: sysClients, depth: sysDepth, depthBytes: sysDepthBytes, queues: sysQueues,
			consumerFlows: sysConsumerFlows, publisherFlows: sysPubFlows,
		})

		// One solace_up per scrape target: the exporter reports each target's last scrape result,
		// and a healthy scrape carries no error label (an empty label value is an absent label).
		for _, t := range c.order {
			c.st.Set("solace_up", c.labels(b, map[string]string{"endpoint": t}), 1)
		}
	}
}

type brokerTotals struct {
	rx, tx, rxBytes, txBytes, exceeded float64
	clients, depth, depthBytes, queues float64
	consumerFlows, publisherFlows      float64
}

// stepQueue advances one queue by one window and emits its QueueDetails, QueueStats and
// (opt-in) QueueRates families.
func (c *Construct) stepQueue(w *core.World, now time.Time, bf, secs float64, b *resolvedBroker, v *resolvedVPN, q *resolvedQueue) qres {
	var r qres
	if q.resource != "" {
		r = c.stepBoundQueue(now, q)
	} else {
		r = c.stepLegacyQueue(w, now, bf, secs, b, v, q)
	}
	c.emitQueue(b, v, q, r, secs)
	return r
}

// stepBoundQueue reads one bound queue's flows from its resource's model window over (from, now].
// Whole messages are emitted; each flow's fraction carries to the next window.
func (c *Construct) stepBoundQueue(now time.Time, q *resolvedQueue) qres {
	win, ok := c.model.Window(q.resource, c.from, now)
	var rx, tx, exceeded float64
	if ok && win.Steps > 0 {
		rx = wholeMessages(&q.rxRemainder, win.Admitted)
		tx = wholeMessages(&q.txRemainder, win.Served)
		exceeded = wholeMessages(&q.exceededRemainder, win.Rejected)
		q.depth = math.Min(math.Round(win.BacklogEnd), q.maxDepth())
	}
	return qres{
		rx: rx, tx: tx, exceeded: exceeded,
		rxBytes: rx * q.msgBytes, txBytes: tx * q.msgBytes,
		depth: q.depth, depthBytes: q.depth * q.msgBytes,
		consumers: q.consumers, subscription: q.subscriptions,
	}
}

// wholeMessages adds v to the carried fraction and returns the whole messages, leaving the
// fraction in carry. A tiny tolerance keeps float noise (9.999999999999998) from costing a message.
func wholeMessages(carry *float64, v float64) float64 {
	work := *carry + v
	n := math.Floor(work + 1e-9)
	*carry = math.Max(work-n, 0)
	return n
}

// stepLegacyQueue advances one unbound queue by one window from its declared rates.
func (c *Construct) stepLegacyQueue(w *core.World, now time.Time, bf, secs float64, b *resolvedBroker, v *resolvedVPN, q *resolvedQueue) qres {
	key := b.name + "|" + v.name + "|" + q.name
	qbf := bf
	if q.load != nil && w != nil && w.Shape != nil {
		qbf = q.load.business(now, w.Shape)
	}
	load := qbf * seriesVar(w, now, "queue|"+key)
	consumeRate := q.consumeRate
	if w != nil && w.Shape != nil {
		if on, inten := w.Shape.Eval(now, ModePublishDrop, q.name); on {
			load *= 1 - inten
		}
		if on, inten := w.Shape.Eval(now, ModeConsumerStall, q.name); on {
			consumeRate *= 1 - inten
		}
	}

	// Integrate fractional arrivals before applying the quota. Only sub-message work
	// carries forward: rejected whole messages are discarded, not retried next tick.
	publishWork := q.publishRemainder + q.publishRate*load*secs
	arrivals := math.Floor(publishWork)
	q.publishRemainder = publishWork - arrivals
	headroom := math.Floor(math.Max(q.quotaBytes-q.depth*q.msgBytes, 0) / q.msgBytes)
	rx := math.Min(arrivals, headroom)
	exceeded := arrivals - rx

	// Delivery carries fractional service only while there is queued work. Once
	// empty, unused capacity (including fractions) expires rather than banking an
	// idle-time catch-up burst. Consumption is not business/volume shaped: existing
	// backlog continues draining when publishers pause.
	avail := q.depth + rx
	drainSecs := secs
	if q.drain != nil {
		// A scheduled consumer works only while a drain window is open within this tick.
		drainSecs = q.drain.open(c.from, now).Seconds()
	}
	consumeWork := q.consumeRemainder + consumeRate*drainSecs
	capacity := math.Floor(consumeWork)
	tx := math.Min(avail, capacity)
	q.depth = avail - tx
	q.consumeRemainder = 0
	if q.depth > 0 {
		q.consumeRemainder = consumeWork - capacity
	}

	return qres{
		rx: rx, tx: tx, exceeded: exceeded,
		rxBytes: rx * q.msgBytes, txBytes: tx * q.msgBytes,
		depth: q.depth, depthBytes: q.depth * q.msgBytes,
		consumers: q.consumers, subscription: q.subscriptions,
	}
}

// emitQueue emits one queue's QueueDetails, QueueStats and (opt-in) QueueRates families from the
// window's result, identically for a bound and an unbound queue.
func (c *Construct) emitQueue(b *resolvedBroker, v *resolvedVPN, q *resolvedQueue, r qres, secs float64) {
	lbl := c.labels(b, map[string]string{"vpn_name": v.name, "queue_name": q.name})

	if c.targets[targetQueueDetails] {
		c.st.Set("solace_queue_spool_quota_bytes", lbl, q.quotaBytes)
		c.counterLevel("solace_queue_spool_usage_bytes", lbl, r.depthBytes)
		c.st.Set("solace_queue_spool_usage_msgs", lbl, r.depth)
		c.st.Set("solace_queue_binds", lbl, float64(q.consumers))
		c.st.Set("solace_queue_subscriptions", lbl, float64(q.subscriptions))
	}

	if c.targets[targetQueueStats] {
		c.st.Add("solace_queue_byte_spooled", lbl, r.rxBytes)
		c.st.Add("solace_queue_msg_spooled", lbl, r.rx)
		c.st.Add("solace_queue_msg_total_deleted", lbl, r.tx)
		c.st.Add("solace_queue_msg_spool_usage_exceeded", lbl, r.exceeded)
		// A healthy queue never redelivers, retransmits, expires or dead-letters: present at zero,
		// as the exporter always emits them.
		for _, zero := range []string{
			"solace_queue_msg_redelivered",
			"solace_queue_msg_retransmitted",
			"solace_queue_msg_max_msg_size_exceeded",
			"solace_queue_msg_shutdown_discarded",
			"solace_queue_msg_ttl_discarded",
			"solace_queue_msg_ttl_dmq",
			"solace_queue_msg_ttl_dmq_failed",
			"solace_queue_msg_max_redelivered_discarded",
			"solace_queue_msg_max_redelivered_dmq",
			"solace_queue_msg_max_redelivered_dmq_failed",
		} {
			c.st.Add(zero, lbl, 0)
		}
	}

	if c.targets[targetQueueRates] {
		rxRate, txRate := r.rx/secs, r.tx/secs
		if !q.rated {
			q.avgRx, q.avgTx, q.rated = rxRate, txRate, true
		} else {
			q.avgRx = 0.5*q.avgRx + 0.5*rxRate
			q.avgTx = 0.5*q.avgTx + 0.5*txRate
		}
		c.st.Set("solace_queue_rx_msg_rate", lbl, rxRate)
		c.st.Set("solace_queue_tx_msg_rate", lbl, txRate)
		c.st.Set("solace_queue_rx_byte_rate", lbl, rxRate*q.msgBytes)
		c.st.Set("solace_queue_tx_byte_rate", lbl, txRate*q.msgBytes)
		c.st.Set("solace_queue_rx_msg_rate_avg", lbl, q.avgRx)
		c.st.Set("solace_queue_tx_msg_rate_avg", lbl, q.avgTx)
		c.st.Set("solace_queue_rx_byte_rate_avg", lbl, q.avgRx*q.msgBytes)
		c.st.Set("solace_queue_tx_byte_rate_avg", lbl, q.avgTx*q.msgBytes)
	}
}

// renderVPN emits the Vpn, VpnStats and VpnSpool families for one message VPN.
func (c *Construct) renderVPN(b *resolvedBroker, v *resolvedVPN, tot qres, conns float64) {
	lbl := c.labels(b, map[string]string{"vpn_name": v.name})

	if c.targets[targetVpn] {
		subs := float64(tot.subscription)
		c.st.Set("solace_vpn_is_management_vpn", lbl, 0)
		c.st.Set("solace_vpn_enabled", lbl, 1)
		c.st.Set("solace_vpn_operational", lbl, 1)
		c.st.Set("solace_vpn_locally_configured", lbl, 1)
		c.st.Set("solace_vpn_local_status", lbl, 1) // encodeMetricMulti: 0=Down, 1=Up
		c.st.Set("solace_vpn_unique_subscriptions", lbl, subs)
		c.st.Set("solace_vpn_total_local_unique_subscriptions", lbl, subs)
		c.st.Set("solace_vpn_total_remote_unique_subscriptions", lbl, 0)
		c.st.Set("solace_vpn_total_unique_subscriptions", lbl, subs)
	}

	if c.targets[targetVpnStats] {
		c.st.Add("solace_vpn_rx_msgs_total", lbl, tot.rx)
		c.st.Add("solace_vpn_tx_msgs_total", lbl, tot.tx)
		c.st.Add("solace_vpn_rx_bytes_total", lbl, tot.rxBytes)
		c.st.Add("solace_vpn_tx_bytes_total", lbl, tot.txBytes)
		c.st.Add("solace_vpn_rx_discarded_msgs_total", lbl, tot.exceeded)
		c.st.Add("solace_vpn_tx_discarded_msgs_total", lbl, 0)
		// Publisher and consumer clients connect over SMF; no other service is declared.
		c.st.Set("solace_vpn_connections", lbl, conns)
		c.st.Set("solace_vpn_connections_service_smf", lbl, conns)
		for _, svc := range []string{"amqp", "mqtt", "web", "rest_in", "rest_out"} {
			c.st.Set("solace_vpn_connections_service_"+svc, lbl, 0)
		}
		if v.maxConnections > 0 {
			c.st.Set("solace_vpn_quota_connections", lbl, float64(v.maxConnections))
		}
	}

	if c.targets[targetVpnSpool] {
		c.st.Set("solace_vpn_spool_quota_bytes", lbl, math.Round(v.quotaBytes))
		c.st.Set("solace_vpn_spool_usage_bytes", lbl, tot.depthBytes)
		pct := -1.0 // the exporter's value when no spool is allocated
		if v.quotaBytes > 0 {
			pct = math.Round(tot.depthBytes / v.quotaBytes * 100)
		}
		c.st.Set("solace_vpn_spool_usage_pct", lbl, pct)
		c.st.Set("solace_vpn_spool_usage_msgs", lbl, tot.depth)
		c.st.Set("solace_vpn_spool_current_endpoints", lbl, float64(len(v.queues)))
		c.st.Set("solace_vpn_spool_current_egress_flows", lbl, float64(tot.consumers))
		c.st.Set("solace_vpn_spool_current_ingress_flows", lbl, float64(v.publishers))
		c.st.Set("solace_vpn_spool_current_transacted_sessions", lbl, 0)
		c.st.Set("solace_vpn_spool_current_transacted_msgs", lbl, 0)
	}
}

// renderBroker emits the GlobalStats, GlobalSystemInfo and Spool families for one broker.
func (c *Construct) renderBroker(secs float64, b *resolvedBroker, t brokerTotals) {
	lbl := c.labels(b, nil)

	if c.targets[targetGlobalStats] {
		c.st.Set("solace_system_total_clients_connected", lbl, t.clients)
		c.st.Add("solace_system_rx_msgs_total", lbl, t.rx)
		c.st.Add("solace_system_tx_msgs_total", lbl, t.tx)
		c.st.Add("solace_system_rx_bytes_total", lbl, t.rxBytes)
		c.st.Add("solace_system_tx_bytes_total", lbl, t.txBytes)
		c.st.Add("solace_system_total_rx_discards", lbl, t.exceeded)
		c.st.Add("solace_system_total_tx_discards", lbl, 0)
	}

	// The exporter dispatches GetGlobalSystemInfoSemp1 separately from GetGlobalStatsSemp1,
	// despite both functions using its GlobalStats descriptor group (exporter.collect.go:68-71).
	if c.targets[targetGlobalSystemInfo] {
		// Uptime is a counter in the exporter. A broker that has been up since before the first
		// observation starts from a deterministic, per-broker age and then advances with time.
		if !b.uptimeSeeded {
			c.st.Add("solace_system_uptime_seconds", lbl, initialUptimeSeconds(b.name))
			b.uptimeSeeded = true
		} else {
			c.st.Add("solace_system_uptime_seconds", lbl, secs)
		}
		if b.cpuCores > 0 {
			c.st.Set("solace_system_cpu_cores", lbl, float64(b.cpuCores))
		}
		if b.memoryGiB > 0 {
			c.st.Set("solace_system_memory_bytes", lbl, b.memoryGiB*gib)
		}
		if b.maxConnections > 0 {
			c.counterLevel("solace_system_total_clients_quota", lbl, float64(b.maxConnections))
		}
	}

	if c.targets[targetSpool] {
		c.st.Set("solace_system_spool_usage_bytes", lbl, math.Round(t.depthBytes))
		c.st.Set("solace_system_spool_usage_msgs", lbl, t.depth)
		c.st.Set("solace_system_spool_ingress_flows_count", lbl, t.publisherFlows)
		c.st.Set("solace_system_spool_egress_flows_count", lbl, t.consumerFlows)
		c.st.Set("solace_system_spool_egress_flows_active", lbl, t.consumerFlows)
		c.st.Set("solace_system_spool_egress_flows_inactive", lbl, 0)
		c.st.Set("solace_system_spool_egress_flows_browser", lbl, 0)
		c.st.Set("solace_system_spool_endpoints_queue", lbl, t.queues)
		c.st.Set("solace_system_spool_endpoints_dte", lbl, 0)
	}
}

// counterLevel emits a level the exporter registers as a Counter (prometheus.CounterValue) although
// the value rises and falls: solace_queue_spool_usage_bytes and solace_system_total_clients_quota
// (getQueueDetailsSemp1.go:77, getGlobalStatsSemp1.go:53 at v1.21.0). A real scrape carries counter
// type metadata with the level as the value, so the series is written through state.Add with the
// delta from the previous level. Upstream semantics are deliberately not corrected.
func (c *Construct) counterLevel(name string, lbl map[string]string, level float64) {
	key := name + "|" + state.LabelSig(lbl)
	c.st.Add(name, lbl, level-c.levels[key])
	c.levels[key] = level
}

// labels builds the label map for one series: the declared scrape identity plus the family's own
// labels. Absent labels are omitted, never empty-valued (I13).
func (c *Construct) labels(b *resolvedBroker, extra map[string]string) map[string]string {
	m := make(map[string]string, 2+len(extra))
	m["job"] = c.job
	m["instance"] = b.instance
	for k, v := range extra {
		if v != "" {
			m[k] = v
		}
	}
	return m
}

// seriesVar returns a stable-but-living multiplier near 1: a deterministic per-series baseline
// (Spread) times a slow drift (Wander). It is 1.0 when no shape engine is wired.
func seriesVar(w *core.World, now time.Time, key string) float64 {
	if w == nil || w.Shape == nil {
		return 1.0
	}
	return w.Shape.Spread(key, ampLoad) * w.Shape.Wander(key, now, ampLoad*0.4)
}

// initialUptimeSeconds gives a broker a deterministic age between 7 and 60 days at first
// observation, derived from its name so restarts of synthkit do not reshuffle the fleet.
func initialUptimeSeconds(name string) float64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	days := 7 + h.Sum32()%54
	return float64(days) * 86400
}
