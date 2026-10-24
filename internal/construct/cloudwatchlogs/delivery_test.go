// SPDX-License-Identifier: AGPL-3.0-only

package cloudwatchlogs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"gopkg.in/yaml.v3"
)

const deliveryYAML = `log_groups:
- name: group
  events_per_second: 0
  uncompressed_bytes_per_event: 1
  subscription_filters:
  - name: filter
    destination_type: Lambda
    period_observation: {delivery_errors: 3, delivery_throttling: 5, forwarded_log_events: 7, forwarded_bytes: 11}
`

func decodeDelivery(text string) (*Config, error) {
	cfg := &Config{}
	d := yaml.NewDecoder(strings.NewReader(text))
	d.KnownFields(true)
	e := d.Decode(cfg)
	return cfg, e
}
func deliveryBuild(t *testing.T, text string) core.Construct {
	t.Helper()
	cfg, e := decodeDelivery(text)
	if e != nil {
		t.Fatal(e)
	}
	return build(t, cfg)
}
func assertDelivery(t *testing.T, w *capture, due bool) {
	t.Helper()
	if !due {
		if w.calls != 0 {
			t.Fatalf("unexpected stale/replayed observation: %+v", w.batch)
		}
		return
	}
	if w.calls != 1 || len(w.batch) != 20 {
		t.Fatalf("got %d calls/%d series, want 1/20", w.calls, len(w.batch))
	}
	expected := map[string]float64{}
	for base, v := range map[string]float64{"delivery_errors": 3, "delivery_throttling": 5, "forwarded_log_events": 7, "forwarded_bytes": 11} {
		for _, s := range []string{"sum", "average", "maximum", "minimum", "sample_count"} {
			x := v
			if s == "sample_count" {
				x = 1
			}
			expected["aws_logs_"+base+"_"+s] = x
		}
	}
	labels := map[string]string{"account_id": "111122223333", "region": "eu-west-1", "namespace": "AWS/Logs", "job": "cloud/aws/logs", "name": "global", "dimension_LogGroupName": "group", "dimension_FilterName": "filter", "dimension_DestinationType": "Lambda"}
	for _, s := range w.batch {
		v, ok := expected[s.Name]
		if !ok || s.Value != v || s.Kind != promrw.KindGauge || !reflect.DeepEqual(s.Labels, labels) {
			t.Fatalf("wrong observation %+v", s)
		}
		delete(expected, s.Name)
	}
	if len(expected) != 0 {
		t.Fatal(expected)
	}
}

func TestDeliveryProgressPauseJitterAndAttempt(t *testing.T) {
	now := time.Unix(1800000000, 0)
	c := deliveryBuild(t, deliveryYAML)
	eng := &shape.Engine{}
	eng.SetVolumeMultiplier(2.5) // Authored values must never be amplitude-scaled.
	assertDelivery(t, tick(t, c, now, eng), true)
	assertDelivery(t, tick(t, c, now, eng), false)
	assertDelivery(t, tick(t, c, now.Add(-time.Second), eng), false)
	// Integer nanoseconds carry exactly to the boundary, without epsilon or drift.
	for i := 1; i <= 599; i++ {
		assertDelivery(t, tick(t, c, now.Add(time.Duration(i)*100*time.Millisecond), eng), false)
	}
	assertDelivery(t, tick(t, c, now.Add(time.Minute-time.Nanosecond), eng), false)
	assertDelivery(t, tick(t, c, now.Add(time.Minute), eng), true)
	assertDelivery(t, tick(t, c, now.Add(80*time.Second), eng), false) // earn 20s
	eng.SetVolumeMultiplier(0)
	assertDelivery(t, tick(t, c, now.Add(90*time.Second), eng), false)
	assertDelivery(t, tick(t, c, now.Add(24*time.Hour), eng), false)
	eng.SetVolumeMultiplier(.25)
	at := now.Add(25 * time.Hour)
	assertDelivery(t, tick(t, c, at, eng), false) // silent resume retains the 20s
	assertDelivery(t, tick(t, c, at.Add(40*time.Second-time.Nanosecond), eng), false)
	assertDelivery(t, tick(t, c, at.Add(40*time.Second), eng), true)
	assertDelivery(t, tick(t, c, at.Add(60*time.Second), eng), false) // earn 20s again
	at = at.Add(48 * time.Hour)
	assertDelivery(t, tick(t, c, at, eng), true) // only NEW progress capped; retain 20s, no replay
	assertDelivery(t, tick(t, c, at.Add(40*time.Second-time.Nanosecond), eng), false)
	assertDelivery(t, tick(t, c, at.Add(40*time.Second), eng), true)
	assertDelivery(t, tick(t, c, at.Add(40*time.Second+time.Nanosecond), eng), false)
	// A failed attempt consumes the period. Neither same-time nor later subperiod
	// ticks retry it; a newly earned period can attempt again.
	sentinel := errors.New("publication failed")
	failed := &capture{err: sentinel}
	at = at.Add(100 * time.Second)
	if e := c.Tick(context.Background(), at, &core.World{Metrics: failed, Shape: eng}); !errors.Is(e, sentinel) {
		t.Fatal(e)
	}
	assertDelivery(t, &capture{batch: failed.batch, calls: failed.calls}, true)
	assertDelivery(t, tick(t, c, at, eng), false)
	assertDelivery(t, tick(t, c, at.Add(time.Second), eng), false)
	assertDelivery(t, tick(t, c, at.Add(time.Minute), eng), true)
	// Initial real pause admits no bootstrap at resume.
	eng.SetVolumeMultiplier(0)
	c = deliveryBuild(t, deliveryYAML)
	assertDelivery(t, tick(t, c, now, eng), false)
	eng.SetVolumeMultiplier(1)
	assertDelivery(t, tick(t, c, at, eng), false)
	assertDelivery(t, tick(t, c, at.Add(time.Minute-time.Nanosecond), eng), false)
	assertDelivery(t, tick(t, c, at.Add(time.Minute), eng), true)
}

func TestDeliveryZeroIndependentFiltersAndOwnedCopies(t *testing.T) {
	// No conservation rule: forwarded/error/throttle events may exceed zero intake
	// and each other; compressed bytes are an unrelated explicit observation.
	cfg, e := decodeDelivery(deliveryYAML)
	if e != nil {
		t.Fatal(e)
	}
	c := build(t, cfg)
	*cfg.LogGroups[0].SubscriptionFilters[0].PeriodObservation.ForwardedBytes = 999
	cfg.LogGroups[0].SubscriptionFilters[0].Name = "changed"
	w := tick(t, c, time.Unix(1800000000, 0), nil)
	assertDelivery(t, w, true)
	for _, s := range w.batch {
		s.Labels["dimension_FilterName"] = "writer-mutated"
	}
	assertDelivery(t, tick(t, c, time.Unix(1800000060, 0), nil), true)
	text := strings.NewReplacer("delivery_errors: 3", "delivery_errors: 0", "delivery_throttling: 5", "delivery_throttling: 0", "forwarded_log_events: 7", "forwarded_log_events: 0", "forwarded_bytes: 11", "forwarded_bytes: 0").Replace(deliveryYAML)
	w = tick(t, deliveryBuild(t, text), time.Unix(1800000000, 0), nil)
	if len(w.batch) != 20 {
		t.Fatal("explicit zero omitted", w)
	}
	for _, s := range w.batch {
		want := 0.0
		if strings.HasSuffix(s.Name, "_sample_count") {
			want = 1
		}
		if s.Value != want {
			t.Fatal(s)
		}
	}
	// Same filter name under distinct group identities is valid; within a group it is not.
	cfg, e = decodeDelivery(deliveryYAML)
	if e != nil {
		t.Fatal(e)
	}
	second := cfg.LogGroups[0]
	second.Name = "other-group"
	cfg.LogGroups = append(cfg.LogGroups, second)
	w = tick(t, build(t, cfg), time.Unix(1800000000, 0), nil)
	if len(w.batch) != 40 {
		t.Fatal(len(w.batch))
	}
	seen := map[string]bool{}
	for _, s := range w.batch {
		key := s.Name + "|" + s.Labels["dimension_LogGroupName"]
		if seen[key] {
			t.Fatal("duplicate", s)
		}
		seen[key] = true
	}
}

func TestDeliveryValidation(t *testing.T) {
	for _, tc := range []struct{ old, replacement string }{
		{"name: filter", "name: ''"}, {"name: filter", "name: ' padded '"},
		{"destination_type: Lambda", "destination_type: lambda"}, {"destination_type: Lambda", "destination_type: Kinesis"},
		{"destination_type: Lambda", ""}, {"period_observation: {delivery_errors: 3, delivery_throttling: 5, forwarded_log_events: 7, forwarded_bytes: 11}", ""},
		{"forwarded_bytes: 11", "forwarded_bytes: 11, ratio: 0.5"},
		{"destination_type: Lambda", "destination_type: Lambda\n    tags: {}"},
	} {
		cfg, e := decodeDelivery(strings.Replace(deliveryYAML, tc.old, tc.replacement, 1))
		if e == nil {
			_, e = Build(cfg, cloud())
		}
		if e == nil {
			t.Fatal("accepted invalid input", tc)
		}
	}
	for _, field := range []string{"delivery_errors: 3", "delivery_throttling: 5", "forwarded_log_events: 7", "forwarded_bytes: 11"} {
		for _, v := range []string{"null", ".nan", ".inf", "-.inf", "-1", "0.5", "1e100", "9007199254740992"} {
			cfg, e := decodeDelivery(strings.Replace(deliveryYAML, field, strings.Split(field, ":")[0]+": "+v, 1))
			if e == nil {
				_, e = Build(cfg, cloud())
			}
			if e == nil {
				t.Fatalf("accepted %s = %s", field, v)
			}
		}
	}
	cfg, e := decodeDelivery(deliveryYAML)
	if e != nil {
		t.Fatal(e)
	}
	f := cfg.LogGroups[0].SubscriptionFilters[0]
	cfg.LogGroups[0].SubscriptionFilters = append(cfg.LogGroups[0].SubscriptionFilters, f)
	if _, e = Build(cfg, cloud()); e == nil {
		t.Fatal("duplicate filter accepted")
	}
	cfg.LogGroups[0].SubscriptionFilters = nil
	for i := 0; i < 100; i++ {
		copy := f
		copy.Name = fmt.Sprintf("filter-%d", i)
		cfg.LogGroups[0].SubscriptionFilters = append(cfg.LogGroups[0].SubscriptionFilters, copy)
	}
	p := &cfg.LogGroups[0].SubscriptionFilters[0].PeriodObservation
	p.DeliveryErrors = ptr(60000001)
	p.DeliveryThrottling = ptr(60000001)
	p.ForwardedLogEvents = ptr(60000001)
	p.ForwardedBytes = ptr(3932160065536)
	c := build(t, cfg)
	w := tick(t, c, time.Unix(1800000000, 0), nil)
	if len(w.batch) != 2000 {
		t.Fatal(len(w.batch))
	}
	for _, s := range w.batch {
		if math.IsNaN(s.Value) || math.IsInf(s.Value, 0) {
			t.Fatal(s)
		}
	}
	p.ForwardedBytes = ptr(3932160065537)
	if _, e = Build(cfg, cloud()); e == nil {
		t.Fatal("byte bound exceeded")
	}
	p.ForwardedBytes = ptr(11)
	p.DeliveryErrors = ptr(60000002)
	if _, e = Build(cfg, cloud()); e == nil {
		t.Fatal("event bound exceeded")
	}
	p.DeliveryErrors = ptr(3)
	cfg.LogGroups = append(cfg.LogGroups, LogGroup{Name: "second", EventsPerSecond: ptr(0), UncompressedBytesPerEvent: ptr(1), SubscriptionFilters: []SubscriptionFilter{f}})
	if _, e = Build(cfg, cloud()); e == nil {
		t.Fatal("aggregate filter bound exceeded")
	}
}
