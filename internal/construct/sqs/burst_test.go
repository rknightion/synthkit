// SPDX-License-Identifier: AGPL-3.0-only

package sqs

import (
	"math"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/shape"
)

// The factors over one period sum to the period in seconds for any duty, including an on-window
// that ends inside a second, so the mean arrival volume is preserved.
func TestBurstFactorsPreserveTheMeanOverAPeriod(t *testing.T) {
	q := Queue{Name: "jobs", ArrivalsPerSecond: 1}
	for _, d := range []Burst{
		{Period: "30m", Duty: 0.25},
		{Period: "90s", Duty: 0.333, Phase: "7s"},
		{Period: "61s", Duty: 0.1234567, Phase: "60.5s"},
		{Period: "1s", Duty: 0.5},
		{Period: "1h", Duty: 1},
	} {
		b, err := d.compile(q)
		if err != nil {
			t.Fatal(err)
		}
		t0 := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
		var sum float64
		n := int(time.Duration(b.period) / time.Second)
		for s := 1; s <= n; s++ {
			sum += b.factor(t0.Add(time.Duration(s) * time.Second))
		}
		// Sub-second period remainders land in the next second; the sum over whole periods is exact.
		whole := time.Duration(n) * time.Second
		if time.Duration(b.period) != whole {
			for s := n + 1; float64(s) <= float64(b.period*10)/float64(time.Second); s++ {
				sum += b.factor(t0.Add(time.Duration(s) * time.Second))
			}
			whole = time.Duration(b.period) * 10
		}
		if want := whole.Seconds(); math.Abs(sum-want) > 1e-6 {
			t.Fatalf("%+v: factors sum to %v over %s, want %v", d, sum, whole, want)
		}
	}
}

// Burst periods are aligned to the Unix epoch plus phase, with no dependence on where the
// construct started.
func TestBurstOnWindowIsEpochAligned(t *testing.T) {
	b, err := (&Burst{Period: "30m", Duty: 0.25, Phase: "2m"}).compile(Queue{Name: "jobs", ArrivalsPerSecond: 1})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Unix(1800*1000, 0) // an epoch-aligned period boundary
	on := func(offset time.Duration) bool { return b.factor(base.Add(offset+time.Second)) > 0 }
	for off, want := range map[time.Duration]bool{
		0: false, 119 * time.Second: false, 120 * time.Second: true, 569 * time.Second: true,
		570 * time.Second: false, 1799 * time.Second: false, 1800*time.Second + 120*time.Second: true,
	} {
		if on(off) != want {
			t.Fatalf("offset %s: on=%v, want %v", off, on(off), want)
		}
	}
}

// Master-paused time is added back, so the burst follows the wall clock like the business curve.
func TestBurstFollowsWallClockAcrossPause(t *testing.T) {
	eng := shape.New("UTC", nil)
	const body = "    arrivals_per_second: 2\n    receives_per_second: 100\n    message_bytes: 8\n    burst: {period: 30m, duty: 0.25}\n"
	c := loadQueue(t, body)
	// 20 minutes of pause have been observed: the unpaused cohort clock reads 09:45 (off-window)
	// while the wall clock reads 10:05, inside the on-window of the 10:00 period.
	wall := time.Date(2026, 10, 24, 10, 5, 0, 0, time.UTC)
	c.started, c.last, c.pausedFor = true, wall.Add(-time.Minute), 20*time.Minute
	if got := tickShaped(t, c, eng, wall)["aws_sqs_number_of_messages_sent_sum"]; got < 479 || got > 481 {
		t.Fatalf("sent %v, want 8 msg/s x 60 s from the wall-clock on-window", got)
	}
}
