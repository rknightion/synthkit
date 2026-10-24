// SPDX-License-Identifier: AGPL-3.0-only

package sqs

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/shape"
	"gopkg.in/yaml.v3"
)

// A Tuesday (2026-11-03) and the Saturday after it, in the engine's UTC anchor.
var (
	tuesday  = time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	saturday = time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
)

// loadQueue builds a one-queue construct from YAML so the test also proves the
// `load` key decodes strictly (KnownFields), exactly as a blueprint would.
func loadQueue(t *testing.T, body string) *Construct {
	t.Helper()
	cfg := Registration().NewConfig()
	dec := yaml.NewDecoder(strings.NewReader("queues:\n  - name: jobs\n    collector_name: declared-collector\n" + body))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		t.Fatal(err)
	}
	c, err := Registration().Build(cfg, cloud())
	if err != nil {
		t.Fatal(err)
	}
	return c.(*Construct)
}

func tickShaped(t *testing.T, c core.Construct, eng *shape.Engine, at time.Time) map[string]float64 {
	t.Helper()
	w := &capture{}
	if err := c.Tick(context.Background(), at, &core.World{Metrics: w, Shape: eng}); err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, s := range w.batch {
		out[s.Name] = s.Value
	}
	return out
}

// expectedArrivals integrates rate x BusinessFactor over the minute ending at end.
func expectedArrivals(eng *shape.Engine, rate float64, end time.Time) float64 {
	var sum float64
	for s := 1; s <= 60; s++ {
		sum += rate * eng.BusinessFactor(end.Add(-time.Minute).Add(time.Duration(s)*time.Second))
	}
	return sum
}

func TestBusinessLoadScalesArrivalsWithTheBusinessCurve(t *testing.T) {
	eng := shape.New("UTC", nil)
	const body = "    load: business\n    arrivals_per_second: 10\n    receives_per_second: 1000\n    message_bytes: 256\n"
	sent := func(day time.Time, hour int) (got, want float64) {
		end := day.Add(time.Duration(hour)*time.Hour + time.Minute)
		m := tickShaped(t, loadQueue(t, body), eng, end)
		return m["aws_sqs_number_of_messages_sent_sum"], expectedArrivals(eng, 10, end)
	}
	noon, noonWant := sent(tuesday, 12)
	night, nightWant := sent(tuesday, 3)
	weekend, weekendWant := sent(saturday, 12)
	for name, p := range map[string][2]float64{"noon": {noon, noonWant}, "night": {night, nightWant}, "weekend": {weekend, weekendWant}} {
		if math.Abs(p[0]-p[1]) > 1.5 {
			t.Fatalf("%s: sent %v, want business-scaled %v", name, p[0], p[1])
		}
	}
	if !(night < noon/5 && weekend < noon*0.5 && weekend > night) {
		t.Fatalf("curve not followed: noon=%v night=%v weekend=%v", noon, night, weekend)
	}
}

func TestBusinessLoadBuildsBacklogOnlyWhenArrivalsExceedConsumerThroughput(t *testing.T) {
	eng := shape.New("UTC", nil)
	run := func(body string) (visibleAt func(h int) float64, receivedAt func(h int) float64) {
		c := loadQueue(t, body)
		vis := map[int]float64{}
		rec := map[int]float64{}
		for at := tuesday.Add(2 * time.Hour); !at.After(tuesday.Add(30 * time.Hour)); at = at.Add(time.Minute) {
			m := tickShaped(t, c, eng, at)
			h := int(at.Sub(tuesday) / time.Hour)
			if at.Minute() == 0 {
				vis[h] = m["aws_sqs_approximate_number_of_messages_visible_maximum"]
				rec[h] = m["aws_sqs_number_of_messages_received_sum"]
			}
		}
		return func(h int) float64 { return vis[h] }, func(h int) float64 { return rec[h] }
	}

	// Consumers (6/s ceiling) are slower than the daytime peak (10/s): a backlog forms
	// by day, none exists overnight before the ramp, and it drains once arrivals fall.
	const slow = "    load: business\n    arrivals_per_second: 10\n    receives_per_second: 6\n    message_bytes: 256\n"
	vis, rec := run(slow)
	if vis(4) != 0 {
		t.Fatalf("overnight backlog before the business day: %v", vis(4))
	}
	if vis(16) < 10000 {
		t.Fatalf("no daytime backlog while arrivals exceed throughput: %v", vis(16))
	}
	if got := rec(14); got < 6*60-1 || got > 6*60 {
		t.Fatalf("backlogged consumers must run at the ceiling (360/min), got %v", got)
	}
	if !(vis(29) < vis(16)) {
		t.Fatalf("backlog did not drain overnight: 16h=%v next 05h=%v", vis(16), vis(29))
	}

	// Consumers faster than the peak: receives follow arrivals, so no backlog ever forms.
	const fast = "    load: business\n    arrivals_per_second: 3\n    receives_per_second: 6\n    message_bytes: 256\n"
	vis, _ = run(fast)
	for h := 3; h <= 30; h++ {
		if vis(h) != 0 {
			t.Fatalf("backlog at %dh although consumers outrun peak arrivals: %v", h, vis(h))
		}
	}
}

func TestBusinessLoadKeepsStockConservationAndIsDeterministic(t *testing.T) {
	eng := shape.New("UTC", nil)
	const body = "    load: business\n    arrivals_per_second: 5\n    receives_per_second: 3\n    message_bytes: 128\n    delay_seconds: 2\n    processing_seconds: 3\n"
	a, b := loadQueue(t, body), loadQueue(t, body)
	var sent, deleted float64
	for at := tuesday.Add(8*time.Hour + time.Minute); at.Before(tuesday.Add(12 * time.Hour)); at = at.Add(time.Minute) {
		ma, mb := tickShaped(t, a, eng, at), tickShaped(t, b, eng, at)
		for k, v := range ma {
			if mb[k] != v {
				t.Fatalf("non-deterministic %s at %s: %v vs %v", k, at, v, mb[k])
			}
		}
		sent += ma["aws_sqs_number_of_messages_sent_sum"]
		deleted += ma["aws_sqs_number_of_messages_deleted_sum"]
		stock := a.queues[0].visible.total + a.queues[0].delayed.total + a.queues[0].flying.total
		if sent != deleted+stock {
			t.Fatalf("conservation at %s: sent=%v deleted=%v stock=%v", at, sent, deleted, stock)
		}
		if _, ok := ma["aws_sqs_number_of_empty_receives_sum"]; ok {
			t.Fatalf("business load invented empty receive polling: %v", ma)
		}
	}
}

func TestDefaultLoadIgnoresTheBusinessCurve(t *testing.T) {
	eng := shape.New("UTC", nil)
	const body = "    arrivals_per_second: 10\n    receives_per_second: 6\n    message_bytes: 256\n"
	for _, at := range []time.Time{tuesday.Add(12*time.Hour + time.Minute), tuesday.Add(3*time.Hour + time.Minute), saturday.Add(12*time.Hour + time.Minute)} {
		m := tickShaped(t, loadQueue(t, body), eng, at)
		if m["aws_sqs_number_of_messages_sent_sum"] != 600 {
			t.Fatalf("default queue must stay constant at %s, sent %v", at, m["aws_sqs_number_of_messages_sent_sum"])
		}
	}
}

func TestBusinessLoadFollowsWallClockAcrossPause(t *testing.T) {
	eng := shape.New("UTC", nil)
	const body = "    load: business\n    arrivals_per_second: 10\n    receives_per_second: 1000\n    message_bytes: 256\n"
	c := loadQueue(t, body)
	// 15 hours of master pause have been observed. The model clock reads 12:xx
	// (plateau) while the wall clock reads 03:xx (overnight floor): arrivals must
	// follow the wall clock the shape engine is defined on.
	end := tuesday.Add(24*time.Hour + 3*time.Hour + 2*time.Minute)
	c.started, c.last, c.pausedFor = true, end.Add(-time.Minute), 15*time.Hour
	got := tickShaped(t, c, eng, end)["aws_sqs_number_of_messages_sent_sum"]
	want := expectedArrivals(eng, 10, end)
	if want > 100 || math.Abs(got-want) > 1.5 {
		t.Fatalf("sent %v, want wall-clock business-scaled %v", got, want)
	}
}

func TestRejectsUnknownLoad(t *testing.T) {
	cfg := Registration().NewConfig()
	dec := yaml.NewDecoder(strings.NewReader("queues:\n  - name: jobs\n    collector_name: declared-collector\n    load: random\n    arrivals_per_second: 1\n    message_bytes: 8\n"))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		// Base: `load` is not a known key at all, which must not read as a pass.
		t.Fatalf("load key must decode so Build can reject its value: %v", err)
	}
	if _, err := Registration().Build(cfg, cloud()); err == nil {
		t.Fatal("accepted unknown load")
	}
}

func TestAcceptsExplicitConstantLoad(t *testing.T) {
	cfg := Registration().NewConfig()
	dec := yaml.NewDecoder(strings.NewReader("queues:\n  - name: jobs\n    collector_name: declared-collector\n    load: constant\n    arrivals_per_second: 1\n    message_bytes: 8\n"))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, err := Registration().Build(cfg, cloud()); err != nil {
		t.Fatalf("rejected the documented default load: %v", err)
	}
}
