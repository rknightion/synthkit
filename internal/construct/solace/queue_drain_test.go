// SPDX-License-Identifier: AGPL-3.0-only

package solace_test

// queue_drain_test.go - proof that a queue can drain on a schedule. The config is decoded from
// YAML exactly as the loader decodes a construct config, built, and ticked once a minute with no
// shape engine (business factor 1, no per-series wander), so the arrivals are exactly 120 a minute
// and every depth is an exact count.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/solace"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
)

// drainYAML declares one queue publishing 2 msg/s that can drain 30 msg/s (1800 a minute) while
// its consumers run.
func drainYAML(schedule string) string {
	return "targets: [QueueDetails, QueueStats]\nbrokers:\n  - name: broker-a\n    vpns:\n      - name: vpn-1\n        queues:\n" +
		"          - name: q-batch\n            publish_msgs_per_sec: 2\n            consume_msgs_per_sec: 30\n" + schedule +
		"          - name: q-live\n            publish_msgs_per_sec: 2\n            consume_msgs_per_sec: 30\n"
}

type drainSample struct{ depth, spooled, deleted float64 }

// drainRun ticks minute 1..n from start and returns each queue's depth and cumulative counters.
func drainRun(t *testing.T, src string, start time.Time, n int, step time.Duration) map[string][]drainSample {
	t.Helper()
	c, err := solace.Build(decodeStrict(t, src), nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	out := map[string][]drainSample{}
	for i := 1; i <= n; i++ {
		mc := &coretest.MetricCapture{} // one capture per tick: Find returns every batch it saw
		w := &core.World{Metrics: mc}   // no shape engine: business factor 1, no per-series wander
		if err := c.Tick(context.Background(), start.Add(time.Duration(i)*step), w); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
		for _, q := range []string{"q-batch", "q-live"} {
			var s drainSample
			for name, dst := range map[string]*float64{
				"solace_queue_spool_usage_msgs": &s.depth, "solace_queue_msg_spooled": &s.spooled, "solace_queue_msg_total_deleted": &s.deleted,
			} {
				for _, m := range mc.Find(name) {
					if m.Labels["queue_name"] == q {
						*dst = m.Value
					}
				}
			}
			out[q] = append(out[q], s)
		}
	}
	return out
}

func TestScheduledDrainMakesASawtooth(t *testing.T) {
	// Hourly windows: open from minute 20 to minute 30 of each epoch hour.
	start := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	const hours = 4
	got := drainRun(t, drainYAML("            drain_schedule: {period: 1h, window: 10m, phase: 20m}\n"), start, hours*60, time.Minute)
	batch, live := got["q-batch"], got["q-live"]
	at := func(minute int) drainSample { return batch[minute-1] }

	for h := 0; h < hours; h++ {
		base := h * 60
		// Minute m's tick covers (m-1, m]. Rising: the minutes whose window ends by 20 or starts
		// at/after 30 are closed, so depth gains the minute's 120 arrivals and nothing is deleted.
		for m := base + 1; m <= base+20; m++ {
			if m == 1 {
				continue
			}
			if d := at(m).depth - at(m-1).depth; d != 120 {
				t.Fatalf("minute %d (closed): depth moved %v, want +120", m, d)
			}
			if d := at(m).deleted - at(m-1).deleted; d != 0 {
				t.Fatalf("minute %d (closed): %v messages deleted", m, d)
			}
		}
		for m := base + 31; m <= base+60; m++ {
			if d := at(m).depth - at(m-1).depth; d != 120 {
				t.Fatalf("minute %d (closed): depth moved %v, want +120", m, d)
			}
			if d := at(m).deleted - at(m-1).deleted; d != 0 {
				t.Fatalf("minute %d (closed): %v messages deleted", m, d)
			}
		}
		// The backlog the window finds is every arrival since the last window ended.
		wantPeak := float64(20 * 120)
		if h > 0 {
			wantPeak = float64((60 - 30 + 20) * 120)
		}
		if got := at(base + 20).depth; got != wantPeak {
			t.Fatalf("hour %d: depth at window open = %v, want %v", h, got, wantPeak)
		}
		// Draining: each window minute clears 1800 of the backlog (the 2400-message first backlog in
		// two minutes, a 6000-message later one in four), then the consumers keep up with arrivals.
		if got := at(base + 21).depth; got != wantPeak+120-1800 {
			t.Fatalf("hour %d minute 21: depth %v, want %v", h, got, wantPeak+120-1800)
		}
		for m := base + 25; m <= base+30; m++ {
			if at(m).depth != 0 {
				t.Fatalf("minute %d (inside window): depth %v, want 0", m, at(m).depth)
			}
		}
		// Every window ends empty, so what the queue spooled it has delivered.
		if e := at(base + 30); e.spooled != e.deleted {
			t.Fatalf("hour %d window end: spooled %v, deleted %v", h, e.spooled, e.deleted)
		}
	}
	// Per period (window end to window end) the consumers deliver exactly the arrivals.
	for h := 1; h < hours; h++ {
		a, b := at(h*60-30), at(h*60+30)
		if b.deleted-a.deleted != b.spooled-a.spooled || b.spooled-a.spooled != 60*120 {
			t.Fatalf("period %d: deleted %v, spooled %v, want both 7200", h, b.deleted-a.deleted, b.spooled-a.spooled)
		}
	}
	// The unscheduled peer in the same VPN keeps draining continuously.
	for i, s := range live {
		if s.depth != 0 {
			t.Fatalf("minute %d: q-live depth %v, want 0", i+1, s.depth)
		}
	}
}

func TestScheduledDrainHonoursPartialWindows(t *testing.T) {
	// A 90 s window opening 30 s into the minute never lines up with a 60 s tick. 100 msg/s is
	// enough that one window clears an hour of arrivals (9000 against 7200 and the live feed).
	start := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	src := strings.Replace(drainYAML("            drain_schedule: {period: 1h, window: 90s, phase: 20m30s}\n"), "consume_msgs_per_sec: 30", "consume_msgs_per_sec: 100", 1)
	const minutes = 3 * 60
	batch := drainRun(t, src, start, minutes, time.Minute)["q-batch"]

	open := func(from, to time.Duration) time.Duration { // independent reference for the overlap
		var total time.Duration
		for k := time.Duration(0); k*time.Hour < to+time.Hour; k++ {
			lo, hi := k*time.Hour+20*time.Minute+30*time.Second, k*time.Hour+20*time.Minute+30*time.Second+90*time.Second
			total += max(min(hi, to)-max(lo, from), 0)
		}
		return total
	}
	var deleted float64
	for m := 1; m <= minutes; m++ {
		from, to := time.Duration(m-1)*time.Minute, time.Duration(m)*time.Minute
		d := batch[m-1].deleted - deleted
		deleted = batch[m-1].deleted
		if open(from, to) == 0 && d != 0 {
			t.Fatalf("minute %d is outside every window but delivered %v", m, d)
		}
		if open(from, to) > 0 && d == 0 {
			t.Fatalf("minute %d overlaps a window but delivered nothing", m)
		}
		// Capacity is 100 msg/s for the open seconds only.
		if limit := 100 * open(from, to).Seconds(); d > limit {
			t.Fatalf("minute %d delivered %v, over the %v the open window allows", m, d, limit)
		}
	}
	// The windows close on minutes 22, 82 and 142: each leaves an empty queue.
	for _, m := range []int{22, 82, 142} {
		if batch[m-1].depth != 0 {
			t.Fatalf("minute %d, window closed: depth %v, want 0", m, batch[m-1].depth)
		}
	}
	// Two minutes before the next opening the backlog is a whole window-gap of arrivals.
	if got := batch[80-1].depth; got != float64((80-22)*120) {
		t.Fatalf("minute 80 depth %v, want %v", got, (80-22)*120)
	}
}

func TestDrainScheduleEpochAligned(t *testing.T) {
	// The same wall clock gives the same windows however far into a run it is reached: a run
	// started mid-period and one started at the boundary agree on the minute the window opens.
	src := drainYAML("            drain_schedule: {period: 1h, window: 10m, phase: 20m}\n")
	midRun := drainRun(t, src, time.Date(2026, 6, 12, 7, 41, 0, 0, time.UTC), 90, time.Minute)["q-batch"]
	// 08:20 is minute 39 of this run: depth gains until the 08:20 tick, then falls.
	if midRun[39-1].deleted != 0 || midRun[40-1].deleted == 0 {
		t.Fatalf("window did not open at 08:20: deleted by 08:20 = %v, by 08:21 = %v", midRun[39-1].deleted, midRun[40-1].deleted)
	}
}

func TestDrainScheduleValidation(t *testing.T) {
	cases := map[string]string{
		"zero period":       "{period: 0s, window: 1m}",
		"missing period":    "{window: 1m}",
		"zero window":       "{period: 1h, window: 0s}",
		"missing window":    "{period: 1h}",
		"window > period":   "{period: 10m, window: 11m}",
		"negative phase":    "{period: 1h, window: 5m, phase: -1m}",
		"phase = period":    "{period: 1h, window: 5m, phase: 1h}",
		"negative period":   "{period: -1h, window: 5m}",
		"negative window":   "{period: 1h, window: -5m}",
		"phase past period": "{period: 1h, window: 5m, phase: 90m}",
	}
	for name, sched := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := decodeStrict(t, drainYAML("            drain_schedule: "+sched+"\n"))
			if _, err := solace.Build(cfg, nil); err == nil {
				t.Fatalf("Build accepted drain_schedule %s", sched)
			} else if !strings.Contains(err.Error(), "drain_schedule") {
				t.Fatalf("error %q does not name drain_schedule", err)
			}
		})
	}
	// window == period is an always-open schedule and is accepted, as is a phase of zero.
	for _, ok := range []string{"{period: 1h, window: 1h}", "{period: 1h, window: 5m}"} {
		if _, err := solace.Build(decodeStrict(t, drainYAML("            drain_schedule: "+ok+"\n")), nil); err != nil {
			t.Fatalf("Build rejected %s: %v", ok, err)
		}
	}
}
