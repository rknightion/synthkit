// SPDX-License-Identifier: AGPL-3.0-only

// Package pushstatus records the most recent synthetic-data push outcome per sink so the
// control plane can render sink readiness. It is a SECOND consumer of the pushhook seam
// (selfobs is the first); it imports only stdlib + pushhook, keeping the OTel-SDK ban intact.
package pushstatus

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/rknightion/synthkit/internal/operationalerr"
	"github.com/rknightion/synthkit/internal/pushhook"
	"github.com/rknightion/synthkit/internal/sink/queue"
)

// maxSamples bounds the per-sink sparkline ring: at one push every few seconds this keeps a
// couple of minutes of recent per-push item counts — enough for a trend, bounded in memory.
const maxSamples = 30

// SinkStat is the observed push history for one sink. The 5s tick cadence makes a single
// "last N items" reading near-meaningless, so the panel reads cumulative totals, a rolling
// rate, and a short sparkline of recent per-push item counts instead.
type SinkStat struct {
	Sink          string              `json:"sink"`
	LastAttemptMs int64               `json:"last_attempt_ms"`
	LastSuccessMs int64               `json:"last_success_ms"`
	LastErrorMs   int64               `json:"last_error_ms"`
	LastError     string              `json:"last_error"`
	LastErrorCode operationalerr.Code `json:"last_error_code"`
	Pushes        int64               `json:"pushes"`
	Failures      int64               `json:"failures"`
	LastItems     int                 `json:"last_items"`
	LastStatus    int                 `json:"last_status"`
	TotalItems    int64               `json:"total_items"`  // cumulative items over real (non-dry-run) pushes
	RatePerMin    float64             `json:"rate_per_min"` // rolling items/min over the sample ring (0 when <2 samples)
	Spark         []int               `json:"spark"`        // recent per-push item counts, oldest→newest (cap maxSamples)
	DryRun        bool                `json:"dry_run"`
}

// sample is one real push's item count stamped with its wall-clock millis, kept in the ring
// so Snapshot can derive both the sparkline and a windowed rate.
type sample struct {
	ms    int64
	items int
}

// sinkAgg holds the folded stat plus the bounded sample ring for one sink.
type sinkAgg struct {
	stat         SinkStat
	currentError bool
	samples      []sample
}

// QueueStat is authoritative delivery-queue loss and pressure state for one sink. It is
// deliberately separate from failed push attempts: retries can recover before a queue drops data.
type QueueStat struct {
	Sink            string              `json:"sink"`
	Depth           int                 `json:"depth"`
	BlockedEnqueues int64               `json:"blocked_enqueues"`
	DroppedItems    int64               `json:"dropped_items"`
	LastLossMs      int64               `json:"last_loss_ms"`
	LastRecoveryMs  int64               `json:"last_recovery_ms"`
	CurrentLoss     bool                `json:"current_loss"`
	AffectedShards  int                 `json:"affected_shards"`
	LastErrorCode   operationalerr.Code `json:"last_error_code"`
}

type queueAgg struct {
	stat     QueueStat
	affected map[int]uint64
}

// LaneState is the current delivery classification for a configured sink lane. The state is
// deliberately derived from configuration plus observed push outcomes: receiving an event never
// turns an undeclared sink into a configured or ready lane.
type LaneState string

const (
	LaneUnconfigured LaneState = "unconfigured"
	LaneDisabled     LaneState = "disabled"
	LaneNotAttempted LaneState = "not_attempted"
	LaneError        LaneState = "error"
	LaneStaleSuccess LaneState = "stale_success"
	LaneSuccess      LaneState = "success"
)

// LaneConfig declares a sink lane that the process intends to deliver. FreshAfter is the
// declared emission interval plus the delivery deadline; zero leaves staleness disabled for
// callers that are displaying delivery history rather than evaluating readiness.
type LaneConfig struct {
	Name           string
	Disabled       bool
	DisabledReason string
	FreshAfter     time.Duration
}

// LaneStatus joins a LaneConfig with observed delivery history. State is mutually exclusive;
// the individual fields retain the evidence an operator needs to distinguish a first attempt,
// a recovered error, and a stale prior success.
type LaneStatus struct {
	Name           string              `json:"name"`
	Configured     bool                `json:"configured"`
	Disabled       bool                `json:"disabled"`
	DisabledReason string              `json:"disabled_reason"`
	Attempted      bool                `json:"attempted"`
	LastAttemptMs  int64               `json:"last_attempt_ms"`
	LastSuccessMs  int64               `json:"last_success_ms"`
	LastErrorMs    int64               `json:"last_error_ms"`
	LastError      string              `json:"last_error"`
	LastErrorCode  operationalerr.Code `json:"last_error_code"`
	CurrentError   bool                `json:"current_error"`
	Stale          bool                `json:"stale"`
	State          LaneState           `json:"state"`
	LiveReady      bool                `json:"live_ready"`
}

// BlueprintEmission is the observed push history rolled up per blueprint name (cross-sink).
type BlueprintEmission struct {
	Blueprint  string  `json:"blueprint"`
	TotalItems int64   `json:"total_items"`
	RatePerMin float64 `json:"rate_per_min"` // rolling items/min over the sample ring (0 when <2 samples)
	Spark      []int   `json:"spark"`        // recent per-push item counts, oldest→newest (cap maxSamples)
}

// bpAgg holds folded per-blueprint stats plus the bounded sample ring.
type bpAgg struct {
	bp      string
	total   int64
	samples []sample
}

// Store folds push events into per-sink stats. Safe for concurrent observe/read.
type Store struct {
	mu          sync.Mutex
	bySink      map[string]*sinkAgg
	byBlueprint map[string]*bpAgg
	byQueue     map[string]*queueAgg
	lanes       map[string]LaneConfig
	now         func() time.Time
}

// NewStore returns an empty store using the wall clock.
func NewStore() *Store {
	return &Store{
		bySink: map[string]*sinkAgg{}, byBlueprint: map[string]*bpAgg{},
		byQueue: map[string]*queueAgg{}, lanes: map[string]LaneConfig{}, now: time.Now,
	}
}

// ConfigureLanes replaces the declared delivery lanes while retaining observed history. A lane
// is configured by its presence in configs; disabled lanes are deliberate (for example DRY_RUN),
// not missing data. Empty names are ignored because no pushhook event can address them.
func (s *Store) ConfigureLanes(configs []LaneConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lanes := make(map[string]LaneConfig, len(configs))
	for _, cfg := range configs {
		if cfg.Name == "" {
			continue
		}
		lanes[cfg.Name] = cfg
	}
	s.lanes = lanes
}

// Observer returns a pushhook.Observer that folds each event into the per-sink stat.
func (s *Store) Observer() pushhook.Observer {
	return func(_ context.Context, ev pushhook.Event) {
		s.mu.Lock()
		defer s.mu.Unlock()
		a := s.bySink[ev.Sink]
		if a == nil {
			a = &sinkAgg{stat: SinkStat{Sink: ev.Sink}}
			s.bySink[ev.Sink] = a
		}
		st := &a.stat
		st.LastItems = ev.Items
		st.LastStatus = ev.Status
		st.DryRun = ev.DryRun
		if ev.DryRun {
			return // dry-run is not a real push; don't count, stamp, or sample
		}
		st.Pushes++
		st.TotalItems += int64(ev.Items)
		ms := s.now().UnixMilli()
		st.LastAttemptMs = ms
		a.samples = append(a.samples, sample{ms: ms, items: ev.Items})
		if len(a.samples) > maxSamples {
			a.samples = a.samples[len(a.samples)-maxSamples:]
		}
		code := operationalerr.Normalize(ev.ErrorCode)
		if code != operationalerr.CodeNone {
			st.Failures++
			st.LastErrorMs = ms
			st.LastErrorCode = code
			st.LastError = operationalerr.Message(code)
			a.currentError = true
			return
		}
		st.LastSuccessMs = ms
		a.currentError = false

		// Per-blueprint fold (additive; skipped when Blueprint is empty — substrate/unscoped).
		if ev.Blueprint != "" {
			b := s.byBlueprint[ev.Blueprint]
			if b == nil {
				b = &bpAgg{bp: ev.Blueprint}
				s.byBlueprint[ev.Blueprint] = b
			}
			b.total += int64(ev.Items)
			b.samples = append(b.samples, sample{ms: ms, items: ev.Items})
			if len(b.samples) > maxSamples {
				b.samples = b.samples[len(b.samples)-maxSamples:]
			}
		}
	}
}

// EnqueueBlocked implements queue.Observer and records pressure without retaining durations or
// caller-controlled text.
func (s *Store) EnqueueBlocked(sink string, _ time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.queueAgg(sink)
	a.stat.BlockedEnqueues++
}

// FlushObserved implements queue.Observer. Recovery is shard-aware: a success only clears a loss
// previously recorded for the same shard, and only a later completion sequence can clear it.
func (s *Store) FlushObserved(ev queue.FlushEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.queueAgg(ev.Sink)
	wasLoss := len(a.affected) > 0
	code := operationalerr.Normalize(ev.Code)
	if ev.Dropped > 0 {
		a.stat.DroppedItems += int64(ev.Dropped)
		a.stat.LastLossMs = s.now().UnixMilli()
		a.stat.LastErrorCode = code
		a.affected[ev.Shard] = ev.Sequence
	} else if code == operationalerr.CodeNone {
		if lossSeq, ok := a.affected[ev.Shard]; ok && ev.Sequence > lossSeq {
			delete(a.affected, ev.Shard)
		}
	}
	a.stat.CurrentLoss = len(a.affected) > 0
	a.stat.AffectedShards = len(a.affected)
	if wasLoss && !a.stat.CurrentLoss {
		a.stat.LastRecoveryMs = s.now().UnixMilli()
	}
}

func (s *Store) queueAgg(sink string) *queueAgg {
	a := s.byQueue[sink]
	if a == nil {
		a = &queueAgg{stat: QueueStat{Sink: sink}, affected: map[int]uint64{}}
		s.byQueue[sink] = a
	}
	return a
}

// SnapshotQueues joins folded historical state with the caller's live depth readings.
func (s *Store) SnapshotQueues(depths map[string]int) []QueueStat {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make(map[string]struct{}, len(s.byQueue)+len(depths))
	for name := range s.byQueue {
		names[name] = struct{}{}
	}
	for name := range depths {
		names[name] = struct{}{}
	}
	out := make([]QueueStat, 0, len(names))
	for name := range names {
		st := QueueStat{Sink: name, Depth: depths[name]}
		if a := s.byQueue[name]; a != nil {
			st = a.stat
			st.Depth = depths[name]
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sink < out[j].Sink })
	return out
}

// SnapshotLanes returns every configured lane plus any observed-but-undeclared lane, sorted by
// name. An observed event is evidence of an attempt only; it cannot invent configuration or make
// a missing entry green. Callers that use this for readiness should supply FreshAfter on every
// live lane as one full emission interval plus the delivery deadline.
func (s *Store) SnapshotLanes() []LaneStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	names := make(map[string]struct{}, len(s.lanes)+len(s.bySink))
	for name := range s.lanes {
		names[name] = struct{}{}
	}
	for name := range s.bySink {
		names[name] = struct{}{}
	}
	now := s.now()
	out := make([]LaneStatus, 0, len(names))
	for name := range names {
		cfg, configured := s.lanes[name]
		var stat SinkStat
		var currentError bool
		if a := s.bySink[name]; a != nil {
			stat = a.stat
			currentError = a.currentError
		}
		out = append(out, foldLane(name, configured, cfg, stat, currentError, now))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func foldLane(name string, configured bool, cfg LaneConfig, stat SinkStat, currentError bool, now time.Time) LaneStatus {
	st := LaneStatus{
		Name:           name,
		Configured:     configured,
		Disabled:       configured && cfg.Disabled,
		DisabledReason: cfg.DisabledReason,
		Attempted:      stat.Pushes > 0,
		LastAttemptMs:  stat.LastAttemptMs,
		LastSuccessMs:  stat.LastSuccessMs,
		LastErrorMs:    stat.LastErrorMs,
		LastError:      stat.LastError,
		LastErrorCode:  stat.LastErrorCode,
	}
	st.CurrentError = currentError
	st.Stale = st.LastSuccessMs > 0 && cfg.FreshAfter > 0 && now.Sub(time.UnixMilli(st.LastSuccessMs)) > cfg.FreshAfter
	switch {
	case !configured:
		st.State = LaneUnconfigured
	case st.Disabled:
		st.State = LaneDisabled
	case !st.Attempted:
		st.State = LaneNotAttempted
	case st.CurrentError:
		st.State = LaneError
	case st.Stale:
		st.State = LaneStaleSuccess
	default:
		st.State = LaneSuccess
		st.LiveReady = true
	}
	return st
}

// SnapshotByBlueprint returns per-blueprint emission totals, keyed by blueprint name.
// Purely additive: all existing per-sink behaviour is byte-identical.
func (s *Store) SnapshotByBlueprint() map[string]BlueprintEmission {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]BlueprintEmission, len(s.byBlueprint))
	for bp, b := range s.byBlueprint {
		spark := make([]int, len(b.samples))
		for i, sm := range b.samples {
			spark[i] = sm.items
		}
		out[bp] = BlueprintEmission{
			Blueprint:  bp,
			TotalItems: b.total,
			RatePerMin: ratePerMin(b.samples),
			Spark:      spark,
		}
	}
	return out
}

// Snapshot returns a copy of every sink's current stat (with sparkline + rolling rate filled),
// sorted by sink name.
func (s *Store) Snapshot() []SinkStat {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SinkStat, 0, len(s.bySink))
	for _, a := range s.bySink {
		st := a.stat
		st.Spark = make([]int, len(a.samples))
		for i, sm := range a.samples {
			st.Spark[i] = sm.items
		}
		st.RatePerMin = ratePerMin(a.samples)
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sink < out[j].Sink })
	return out
}

// ratePerMin derives an items-per-minute rate from the ring over the span between the first and
// last sample. Only the items delivered WITHIN that span count — i.e. samples[1:], the pushes that
// occurred after the window opened (the first sample's items were delivered before first.ms, in an
// interval the window doesn't cover). Counting all N samples over the N-1 intervals would
// over-report by N/(N-1). Returns 0 with fewer than two samples (no span) or a non-positive span.
func ratePerMin(samples []sample) float64 {
	if len(samples) < 2 {
		return 0
	}
	spanMs := samples[len(samples)-1].ms - samples[0].ms
	if spanMs <= 0 {
		return 0
	}
	var total int64
	for _, sm := range samples[1:] {
		total += int64(sm.items)
	}
	return float64(total) / (float64(spanMs) / 1000) * 60
}
