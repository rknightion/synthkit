// SPDX-License-Identifier: AGPL-3.0-only

package solace

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/shape"
)

// Config is the construct's YAML config struct: the declared brokers, their message VPNs and
// queues. The exporter itself carries no broker label, so a broker's identity is the scrape
// instance; every name here is operator-declared and the construct invents no defaults for them.
type Config struct {
	// Job is the scrape job label stamped on every series. Default "solace-prometheus-exporter"
	// (the exporter's own name; the job value is an operator choice, not set by the exporter).
	Job string `yaml:"job"`
	// Targets lists the exporter scrape targets to emulate, by the exporter's own case-sensitive
	// target names: Vpn, VpnStats, VpnSpool, QueueDetails, QueueStats, QueueRates, GlobalStats,
	// GlobalSystemInfo, Spool. Default: all except deprecated QueueRates. GlobalStats emits
	// traffic and connected clients; GlobalSystemInfo emits uptime and declared capacities.
	Targets []string `yaml:"targets"`
	// Brokers is the declared event brokers; at least one is required.
	Brokers []BrokerConfig `yaml:"brokers"`

	// causal is the blueprint's declared request resources, injected by the resolver
	// (UseResources); empty when the blueprint declares none.
	causal causal.Set `yaml:"-"`
}

// UseResources receives the blueprint's declared causal resources (causal.Consumer). The
// resolver calls it after strict decode and before Build, only when the blueprint declares
// resources.
func (c *Config) UseResources(s causal.Set) { c.causal = s }

var _ causal.Consumer = (*Config)(nil)

// BrokerConfig declares one event broker as seen through one exporter scrape.
type BrokerConfig struct {
	// Name identifies the broker; it must be unique and is the default instance host.
	Name string `yaml:"name"`
	// Instance overrides the scrape instance label. Default "<name>:9628" (9628 is the
	// exporter's default listen port).
	Instance string `yaml:"instance"`
	// CPUCores is the broker's available cores (solace_system_cpu_cores). Omitted when 0.
	CPUCores int `yaml:"cpu_cores"`
	// MemoryGiB is the broker's available RAM in GiB (solace_system_memory_bytes). Omitted when 0.
	MemoryGiB float64 `yaml:"memory_gib"`
	// MaxConnections is the broker-wide client connection limit
	// (solace_system_total_clients_quota). Omitted when 0.
	MaxConnections int `yaml:"max_connections"`
	// VPNs is the message VPNs on this broker; at least one is required.
	VPNs []VPNConfig `yaml:"vpns"`
}

// VPNConfig declares one message VPN and its queues.
type VPNConfig struct {
	// Name is the message VPN name (the vpn_name label); unique within the broker.
	Name string `yaml:"name"`
	// Publishers is the number of publishing client connections, one guaranteed ingress flow
	// each. Default 1.
	Publishers *int `yaml:"publishers"`
	// MaxConnections is the VPN connection limit (solace_vpn_quota_connections). Omitted when 0.
	MaxConnections int `yaml:"max_connections"`
	// SpoolQuotaMB is the VPN message-spool quota in MB. Default: the sum of the queue quotas.
	SpoolQuotaMB float64 `yaml:"spool_quota_mb"`
	// Queues is the queues in this VPN; at least one is required.
	Queues []QueueConfig `yaml:"queues"`
}

// QueueConfig declares one queue and the traffic through it.
type QueueConfig struct {
	// Name is the queue name (the queue_name label); unique within the VPN.
	Name string `yaml:"name"`
	// Resource binds the queue to a causal `queue` resource declared under
	// environments[].resources (opt-in). A bound queue's publishes, deletes, depth and discards
	// follow the resource's model, so a consumer slowdown or a slow held-across dependency grows
	// the spool and it drains after the fault. The consumer count is the resource's size and the
	// depth limit is spool_quota_mb / avg_msg_bytes, so the resource declares no max_depth.
	// publish_msgs_per_sec, consume_msgs_per_sec, rate_scale, shape_profile and the queue-axis
	// failure modes are rejected on a bound queue, and one resource binds one queue.
	Resource string `yaml:"resource"`
	// PublishMsgsPerSec is the business-hours publish rate into the queue, scaled by the shape
	// engine's business factor. Default 2: a master-data change feed is a low-volume stream
	// (about 170k messages a day at this peak), not bulk transfer. 0 declares an idle queue.
	PublishMsgsPerSec *float64 `yaml:"publish_msgs_per_sec"`
	// ConsumeMsgsPerSec is the consumers' maximum drain rate. Default 3x PublishMsgsPerSec so a
	// healthy queue keeps no backlog, or 0 when Consumers is 0; set it below the publish rate (0 stalls the consumers)
	// to model a growing backlog. With a drain_schedule it is the rate while a window is open.
	ConsumeMsgsPerSec *float64 `yaml:"consume_msgs_per_sec"`
	// AvgMsgBytes is the average message size. Default 2048 (a JSON master-data change record).
	AvgMsgBytes int `yaml:"avg_msg_bytes"`
	// Consumers is the number of bound consumer flows (solace_queue_binds). Default 1. A queue
	// with 0 consumers drains nothing unless ConsumeMsgsPerSec is declared.
	Consumers *int `yaml:"consumers"`
	// Subscriptions is the queue's topic subscription count (solace_queue_subscriptions).
	// Default 1.
	Subscriptions *int `yaml:"subscriptions"`
	// SpoolQuotaMB is the queue's maximum spool usage in MB. Default 5000, the Solace default
	// for a queue's max spool usage. Messages arriving once it is full are discarded and counted
	// in solace_queue_msg_spool_usage_exceeded.
	SpoolQuotaMB float64 `yaml:"spool_quota_mb"`
	// RateScale multiplies this queue's publish rate, and the default drain rate derived from it
	// (3x the publish rate); a declared consume_msgs_per_sec is not scaled. Default 1 (omitted
	// leaves the queue exactly as before). It lets peer queues carry different volumes without
	// restating each rate; 0 declares an idle queue.
	RateScale *float64 `yaml:"rate_scale"`
	// ShapeProfile gives this queue its own business-hours load curve in place of the blueprint's
	// (a queue fed by an overnight batch peaks when the rest of the estate is idle). Omitted
	// keeps the blueprint curve. Same keys and defaults as the application workload's
	// shape_profile.
	ShapeProfile *ShapeProfile `yaml:"shape_profile"`
	// DrainSchedule makes the queue's consumers drain on a schedule instead of continuously
	// (opt-in): a batch consumer that wakes at fixed times. Omitted leaves the queue exactly as
	// before. Rejected on a queue bound to a causal resource, whose drain belongs to the model.
	DrainSchedule *DrainSchedule `yaml:"drain_schedule"`
}

// DrainSchedule declares when a queue's consumers run. Consumption happens only inside each
// drain window, at the queue's drain capacity (consume_msgs_per_sec, default 3x the publish
// rate); between windows the consumers are idle and the spool grows with the arrivals, so the
// depth is a sawtooth. Windows are aligned to the Unix epoch, not to the start of the run, so a
// restart does not move them: window k opens at k*period + phase and stays open for window.
// Size the drain capacity so a window can clear its period's arrivals (capacity x window at
// least publish rate x period), or the backlog carries into the next period.
type DrainSchedule struct {
	// Period is the interval between window openings (> 0), for example 1h.
	Period time.Duration `yaml:"period"`
	// Window is how long each drain window stays open (> 0 and <= period).
	Window time.Duration `yaml:"window"`
	// Phase offsets the window opening from the epoch-aligned period boundary, in [0, period).
	// Default 0.
	Phase time.Duration `yaml:"phase"`
}

// open reports how much of (from, to] falls inside a drain window.
func (d *DrainSchedule) open(from, to time.Time) time.Duration {
	return d.openUntil(to) - d.openUntil(from)
}

// openUntil is the cumulative time the windows have been open since the epoch, at t.
func (d *DrainSchedule) openUntil(t time.Time) time.Duration {
	// Nanosecond arithmetic on the Unix timeline; a tick is never far enough from the epoch to
	// overflow int64 nanoseconds (until 2262).
	ns := t.UnixNano() - int64(d.Phase)
	p := int64(d.Period)
	k := ns / p
	rem := ns % p
	if rem < 0 {
		k--
		rem += p
	}
	return time.Duration(k)*d.Window + min(time.Duration(rem), d.Window)
}

// ShapeProfile parameterises one queue's diurnal load curve: a floor, a smoothstep ramp up to a
// plateau, a ramp back down, and a weekend factor, read in a chosen timezone. Every field is
// optional; an omitted field takes the blueprint-default curve value (shape.DefaultCurve).
type ShapeProfile struct {
	// Timezone is the IANA zone the hours are read in. Empty uses the blueprint timezone.
	Timezone string `yaml:"timezone"`
	// Floor is the overnight load fraction of the plateau, in [0,1]. Default 0.1.
	Floor *float64 `yaml:"floor"`
	// RampUpStartHour is the local hour the morning ramp starts. Default 6.
	RampUpStartHour *float64 `yaml:"ramp_up_start_hour"`
	// PlateauStartHour is the local hour full load is reached. Default 10.
	PlateauStartHour *float64 `yaml:"plateau_start_hour"`
	// PlateauEndHour is the local hour the evening ramp starts. Default 16.
	PlateauEndHour *float64 `yaml:"plateau_end_hour"`
	// RampDownEndHour is the local hour the floor is reached again. Default 20.
	RampDownEndHour *float64 `yaml:"ramp_down_end_hour"`
	// WeekendFactor multiplies Saturday and Sunday load (>= 0). Default 0.3.
	WeekendFactor *float64 `yaml:"weekend_factor"`
}

// queueLoad is a resolved ShapeProfile: the curve and its location (nil reads the blueprint
// engine's).
type queueLoad struct {
	curve shape.Curve
	loc   *time.Location
}

func resolveShapeProfile(p *ShapeProfile) (*queueLoad, error) {
	if p == nil {
		return nil, nil
	}
	c := shape.DefaultCurve()
	for _, f := range []struct {
		src *float64
		dst *float64
	}{
		{p.Floor, &c.Floor},
		{p.RampUpStartHour, &c.RampUpStartHour},
		{p.PlateauStartHour, &c.PlateauStartHour},
		{p.PlateauEndHour, &c.PlateauEndHour},
		{p.RampDownEndHour, &c.RampDownEndHour},
	} {
		if f.src != nil {
			*f.dst = *f.src
		}
	}
	if p.WeekendFactor != nil {
		wf := *p.WeekendFactor
		c.WeekendFactor = &wf
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	ql := &queueLoad{curve: c}
	if p.Timezone != "" {
		loc, err := time.LoadLocation(p.Timezone)
		if err != nil {
			return nil, fmt.Errorf("timezone %q: %w", p.Timezone, err)
		}
		ql.loc = loc
	}
	return ql, nil
}

// business is the substrate-style load (diurnal x business weekly x master volume) on this
// queue's own curve, the counterpart of Engine.BusinessFactor.
func (l *queueLoad) business(now time.Time, eng *shape.Engine) float64 {
	loc := eng.Loc()
	if l.loc != nil {
		loc = l.loc
	}
	t := now.In(loc)
	return l.curve.Diurnal(t) * l.curve.BusinessWeekly(t) * eng.VolumeMultiplier()
}

// Scrape-target names, exactly as the exporter spells them (internal/exporter/exporter.collect.go,
// v1.21.0). They are also the value of the endpoint label on solace_up.
const (
	targetVpn              = "Vpn"
	targetVpnStats         = "VpnStats"
	targetVpnSpool         = "VpnSpool"
	targetQueueDetails     = "QueueDetails"
	targetQueueStats       = "QueueStats"
	targetQueueRates       = "QueueRates"
	targetGlobalStats      = "GlobalStats"
	targetGlobalSystemInfo = "GlobalSystemInfo"
	targetSpool            = "Spool"
)

var (
	// allTargets is the full set a declaration may name.
	allTargets = []string{targetVpn, targetVpnStats, targetVpnSpool, targetQueueDetails,
		targetQueueStats, targetQueueRates, targetGlobalStats, targetGlobalSystemInfo, targetSpool}
	// defaultTargets omits QueueRates, which the exporter documents as DEPRECATED.
	defaultTargets = []string{targetVpn, targetVpnStats, targetVpnSpool, targetQueueDetails,
		targetQueueStats, targetGlobalStats, targetGlobalSystemInfo, targetSpool}
)

const (
	defaultJob          = "solace-prometheus-exporter"
	defaultPort         = "9628"
	defaultPublishRate  = 2.0
	defaultMsgBytes     = 2048
	defaultQueueQuotaMB = 5000.0
	mib                 = 1048576.0
	gib                 = 1073741824.0
)

// resolved holds validated, defaulted declarations.
type resolvedQueue struct {
	name             string
	publishRate      float64
	consumeRate      float64
	msgBytes         float64
	consumers        int
	subscriptions    int
	quotaBytes       float64
	publishRemainder float64
	consumeRemainder float64
	load             *queueLoad     // nil: the blueprint curve
	resource         string         // bound causal queue resource; empty: the legacy model
	drain            *DrainSchedule // nil: consumers run continuously
	// bound-queue whole-message carries (admitted, rejected, served fractions)
	rxRemainder, exceededRemainder, txRemainder float64
	// runtime state
	depth  float64 // messages currently spooled
	avgRx  float64 // smoothed msgs/s, for the *_avg rate gauges
	avgTx  float64
	rated  bool
	rxRate float64
	txRate float64
}

type resolvedVPN struct {
	name           string
	publishers     int
	maxConnections int
	quotaBytes     float64
	queues         []*resolvedQueue
}

type resolvedBroker struct {
	name           string
	instance       string
	cpuCores       int
	memoryGiB      float64
	maxConnections int
	vpns           []*resolvedVPN
	uptimeSeeded   bool
}

func resolve(c *Config) (job string, targets map[string]bool, order []string, brokers []*resolvedBroker, err error) {
	job = c.Job
	if job == "" {
		job = defaultJob
	}

	order = c.Targets
	if len(order) == 0 {
		order = defaultTargets
	}
	targets = make(map[string]bool, len(order))
	for _, t := range order {
		if !contains(allTargets, t) {
			return "", nil, nil, nil, fmt.Errorf("solace: unknown target %q (want one of %s)", t, strings.Join(allTargets, ", "))
		}
		if targets[t] {
			return "", nil, nil, nil, fmt.Errorf("solace: duplicate target %q", t)
		}
		targets[t] = true
	}

	if len(c.Brokers) == 0 {
		return "", nil, nil, nil, fmt.Errorf("solace: at least one broker must be declared")
	}
	names := map[string]bool{}
	instances := map[string]bool{}
	bound := map[string]string{} // causal resource -> the queue bound to it
	for bi, bc := range c.Brokers {
		if bc.Name == "" {
			return "", nil, nil, nil, fmt.Errorf("solace: brokers[%d].name is required", bi)
		}
		if names[bc.Name] {
			return "", nil, nil, nil, fmt.Errorf("solace: duplicate broker %q", bc.Name)
		}
		names[bc.Name] = true
		if bc.CPUCores < 0 || bc.MemoryGiB < 0 || bc.MaxConnections < 0 {
			return "", nil, nil, nil, fmt.Errorf("solace: broker %q: cpu_cores, memory_gib and max_connections must be >= 0", bc.Name)
		}
		instance := bc.Instance
		if instance == "" {
			instance = bc.Name + ":" + defaultPort
		}
		if instances[instance] {
			return "", nil, nil, nil, fmt.Errorf("solace: broker %q: duplicate instance %q", bc.Name, instance)
		}
		instances[instance] = true
		if len(bc.VPNs) == 0 {
			return "", nil, nil, nil, fmt.Errorf("solace: broker %q declares no vpns", bc.Name)
		}
		rb := &resolvedBroker{name: bc.Name, instance: instance, cpuCores: bc.CPUCores,
			memoryGiB: bc.MemoryGiB, maxConnections: bc.MaxConnections}

		vpnNames := map[string]bool{}
		for vi, vc := range bc.VPNs {
			if vc.Name == "" {
				return "", nil, nil, nil, fmt.Errorf("solace: broker %q: vpns[%d].name is required", bc.Name, vi)
			}
			if vpnNames[vc.Name] {
				return "", nil, nil, nil, fmt.Errorf("solace: broker %q: duplicate vpn %q", bc.Name, vc.Name)
			}
			vpnNames[vc.Name] = true
			if vc.MaxConnections < 0 || vc.SpoolQuotaMB < 0 {
				return "", nil, nil, nil, fmt.Errorf("solace: vpn %q: max_connections and spool_quota_mb must be >= 0", vc.Name)
			}
			publishers := 1
			if vc.Publishers != nil {
				if *vc.Publishers < 0 {
					return "", nil, nil, nil, fmt.Errorf("solace: vpn %q: publishers must be >= 0", vc.Name)
				}
				publishers = *vc.Publishers
			}
			if len(vc.Queues) == 0 {
				return "", nil, nil, nil, fmt.Errorf("solace: vpn %q declares no queues", vc.Name)
			}
			rv := &resolvedVPN{name: vc.Name, publishers: publishers, maxConnections: vc.MaxConnections}

			queueNames := map[string]bool{}
			var quotaSum float64
			for qi, qc := range vc.Queues {
				if qc.Name == "" {
					return "", nil, nil, nil, fmt.Errorf("solace: vpn %q: queues[%d].name is required", vc.Name, qi)
				}
				if queueNames[qc.Name] {
					return "", nil, nil, nil, fmt.Errorf("solace: vpn %q: duplicate queue %q", vc.Name, qc.Name)
				}
				queueNames[qc.Name] = true
				rq, qerr := resolveQueue(qc, c.causal)
				if qerr != nil {
					return "", nil, nil, nil, qerr
				}
				if rq.resource != "" {
					if prev, dup := bound[rq.resource]; dup {
						return "", nil, nil, nil, fmt.Errorf("solace: queue %q: resource %q is already bound to %s; one resource drives exactly one queue", qc.Name, rq.resource, prev)
					}
					bound[rq.resource] = fmt.Sprintf("broker %q vpn %q queue %q", bc.Name, vc.Name, qc.Name)
				}
				quotaSum += rq.quotaBytes
				rv.queues = append(rv.queues, rq)
			}
			rv.quotaBytes = vc.SpoolQuotaMB * mib
			if vc.SpoolQuotaMB == 0 {
				rv.quotaBytes = quotaSum
			}
			rb.vpns = append(rb.vpns, rv)
		}
		brokers = append(brokers, rb)
	}
	return job, targets, order, brokers, nil
}

func resolveQueue(qc QueueConfig, set causal.Set) (*resolvedQueue, error) {
	if qc.Resource != "" {
		return resolveBoundQueue(qc, set)
	}
	rq := &resolvedQueue{name: qc.Name, publishRate: defaultPublishRate, msgBytes: defaultMsgBytes,
		consumers: 1, subscriptions: 1}
	if qc.PublishMsgsPerSec != nil {
		if *qc.PublishMsgsPerSec < 0 {
			return nil, fmt.Errorf("solace: queue %q: publish_msgs_per_sec must be >= 0", qc.Name)
		}
		rq.publishRate = *qc.PublishMsgsPerSec
	}
	if qc.RateScale != nil {
		if s := *qc.RateScale; math.IsNaN(s) || math.IsInf(s, 0) || s < 0 {
			return nil, fmt.Errorf("solace: queue %q: rate_scale must be finite and >= 0", qc.Name)
		}
		rq.publishRate *= *qc.RateScale
		if math.IsInf(rq.publishRate, 0) || math.IsInf(3*rq.publishRate, 0) {
			return nil, fmt.Errorf("solace: queue %q: rate_scale %v makes the publish rate overflow", qc.Name, *qc.RateScale)
		}
	}
	load, err := resolveShapeProfile(qc.ShapeProfile)
	if err != nil {
		return nil, fmt.Errorf("solace: queue %q: shape_profile: %w", qc.Name, err)
	}
	rq.load = load
	if d := qc.DrainSchedule; d != nil {
		if d.Period <= 0 || d.Window <= 0 {
			return nil, fmt.Errorf("solace: queue %q: drain_schedule period and window must be > 0", qc.Name)
		}
		if d.Window > d.Period {
			return nil, fmt.Errorf("solace: queue %q: drain_schedule window %v exceeds its period %v", qc.Name, d.Window, d.Period)
		}
		if d.Phase < 0 || d.Phase >= d.Period {
			return nil, fmt.Errorf("solace: queue %q: drain_schedule phase %v must be in [0, period %v)", qc.Name, d.Phase, d.Period)
		}
		cp := *d
		rq.drain = &cp
	}
	rq.consumeRate = 3 * rq.publishRate
	// A queue declared with no consumers drains nothing unless a drain rate is declared explicitly.
	if qc.Consumers != nil && *qc.Consumers == 0 {
		rq.consumeRate = 0
	}
	if qc.ConsumeMsgsPerSec != nil {
		if *qc.ConsumeMsgsPerSec < 0 {
			return nil, fmt.Errorf("solace: queue %q: consume_msgs_per_sec must be >= 0", qc.Name)
		}
		rq.consumeRate = *qc.ConsumeMsgsPerSec
	}
	if qc.AvgMsgBytes < 0 {
		return nil, fmt.Errorf("solace: queue %q: avg_msg_bytes must be >= 0", qc.Name)
	}
	if qc.AvgMsgBytes > 0 {
		rq.msgBytes = float64(qc.AvgMsgBytes)
	}
	if qc.Consumers != nil {
		if *qc.Consumers < 0 {
			return nil, fmt.Errorf("solace: queue %q: consumers must be >= 0", qc.Name)
		}
		rq.consumers = *qc.Consumers
	}
	if qc.Subscriptions != nil {
		if *qc.Subscriptions < 0 {
			return nil, fmt.Errorf("solace: queue %q: subscriptions must be >= 0", qc.Name)
		}
		rq.subscriptions = *qc.Subscriptions
	}
	if qc.SpoolQuotaMB < 0 {
		return nil, fmt.Errorf("solace: queue %q: spool_quota_mb must be >= 0", qc.Name)
	}
	quotaMB := qc.SpoolQuotaMB
	if quotaMB == 0 {
		quotaMB = defaultQueueQuotaMB
	}
	rq.quotaBytes = quotaMB * mib
	return rq, nil
}

// resolveBoundQueue validates a queue bound to a causal queue resource. Every rate the model
// owns is rejected rather than ignored: a second declaration of the same quantity would silently
// disagree with the resource.
func resolveBoundQueue(qc QueueConfig, set causal.Set) (*resolvedQueue, error) {
	res, _, ok := set.Lookup(qc.Resource)
	if !ok {
		return nil, fmt.Errorf("solace: queue %q: resource %q is not declared (declare it under environments[].resources)", qc.Name, qc.Resource)
	}
	if res.Kind != causal.KindQueue {
		return nil, fmt.Errorf("solace: queue %q: resource %q is kind %q, want %q", qc.Name, qc.Resource, res.Kind, causal.KindQueue)
	}
	for _, f := range []struct {
		name string
		set  bool
		why  string
	}{
		{"publish_msgs_per_sec", qc.PublishMsgsPerSec != nil, "the resource's demand is the publish rate"},
		{"consume_msgs_per_sec", qc.ConsumeMsgsPerSec != nil, "the resource's size and hold_ms set the drain rate"},
		{"rate_scale", qc.RateScale != nil, "the resource's demand is the publish rate"},
		{"shape_profile", qc.ShapeProfile != nil, "the resource's demand carries the load curve"},
		{"drain_schedule", qc.DrainSchedule != nil, "the resource's size and hold_ms set when and how fast it drains"},
	} {
		if f.set {
			return nil, fmt.Errorf("solace: queue %q: %s cannot be set on a queue bound to resource %q (%s)", qc.Name, f.name, qc.Resource, f.why)
		}
	}
	if res.MaxDepth != 0 {
		return nil, fmt.Errorf("solace: queue %q: resource %q declares max_depth; a Solace-bound queue's depth limit is its spool_quota_mb / avg_msg_bytes", qc.Name, qc.Resource)
	}
	rq := &resolvedQueue{name: qc.Name, msgBytes: defaultMsgBytes, consumers: res.Size, subscriptions: 1, resource: qc.Resource}
	if qc.Consumers != nil {
		if *qc.Consumers != res.Size {
			return nil, fmt.Errorf("solace: queue %q: consumers %d disagrees with resource %q size %d (the resource's size is the consumer count; omit consumers)", qc.Name, *qc.Consumers, qc.Resource, res.Size)
		}
	}
	if qc.AvgMsgBytes < 0 {
		return nil, fmt.Errorf("solace: queue %q: avg_msg_bytes must be >= 0", qc.Name)
	}
	if qc.AvgMsgBytes > 0 {
		rq.msgBytes = float64(qc.AvgMsgBytes)
	}
	if qc.Subscriptions != nil {
		if *qc.Subscriptions < 0 {
			return nil, fmt.Errorf("solace: queue %q: subscriptions must be >= 0", qc.Name)
		}
		rq.subscriptions = *qc.Subscriptions
	}
	if qc.SpoolQuotaMB < 0 {
		return nil, fmt.Errorf("solace: queue %q: spool_quota_mb must be >= 0", qc.Name)
	}
	quotaMB := qc.SpoolQuotaMB
	if quotaMB == 0 {
		quotaMB = defaultQueueQuotaMB
	}
	rq.quotaBytes = quotaMB * mib
	if rq.maxDepth() < 1 {
		return nil, fmt.Errorf("solace: queue %q: spool_quota_mb %v holds less than one avg_msg_bytes (%v) message", qc.Name, quotaMB, rq.msgBytes)
	}
	return rq, nil
}

// maxDepth is the most messages the spool quota holds at the average message size.
func (q *resolvedQueue) maxDepth() float64 { return math.Floor(q.quotaBytes / q.msgBytes) }

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// IdentityClaims reports the substrate identities this declaration owns, as (class, value)
// pairs, so the blueprint loader can reject two blueprints declaring the same broker. The
// exporter carries no blueprint label, so (job, instance) is the series identity of every
// broker-level family and the prefix of every per-VPN and per-queue family; one claim per
// broker therefore covers its VPN and queue series. Duck-typed: this package does not import
// the blueprint package.
func (c *Config) IdentityClaims() [][2]string {
	job := c.Job
	if job == "" {
		job = defaultJob
	}
	claims := make([][2]string, 0, len(c.Brokers))
	for _, b := range c.Brokers {
		instance := b.Instance
		if instance == "" {
			instance = b.Name + ":" + defaultPort
		}
		// Quoted so no job/instance pair can produce the same text as another pair.
		claims = append(claims, [2]string{"solace broker", strconv.Quote(job) + " " + strconv.Quote(instance)})
	}
	return claims
}

// FailureTargets reports each distinct declared unbound queue name as a queue-axis failure
// target, in declaration order. Queue names are unique only within a VPN, so one target name
// addresses every unbound queue that carries it. A queue bound to a causal resource is not a
// queue-axis target: its depth and rates belong to the model, so the legacy queue modes cannot
// address it (an incident naming only a bound queue fails to load); the resource is the target.
// Duck-typed by the blueprint resolver.
func (c *Config) FailureTargets() []failuremode.Target {
	var out []failuremode.Target
	seen := map[string]bool{}
	for _, b := range c.Brokers {
		for _, v := range b.VPNs {
			for _, q := range v.Queues {
				if q.Name == "" || q.Resource != "" || seen[q.Name] {
					continue
				}
				seen[q.Name] = true
				out = append(out, failuremode.Target{Name: q.Name, Axis: failuremode.AxisQueue})
			}
		}
	}
	return out
}
