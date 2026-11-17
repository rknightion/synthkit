// SPDX-License-Identifier: AGPL-3.0-only

package sqs

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"gopkg.in/yaml.v3"
)

type capture struct{ batch []promrw.Series }

func (w *capture) Write(_ context.Context, b []promrw.Series) error { w.batch = b; return nil }
func cloud() *fixture.Set {
	return &fixture.Set{Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "eu-west-1"}}
}
func queue() Queue {
	return Queue{Name: "jobs", CollectorName: "declared-collector", ArrivalsPerSecond: 1, ReceivesPerSecond: 0.5, MessageBytes: 256, DelaySeconds: 2, ProcessingSeconds: 3}
}
func build(t *testing.T, q Queue) core.Construct {
	t.Helper()
	c, err := Registration().Build(&Config{Queues: []Queue{q}}, cloud())
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func tick(t *testing.T, c core.Construct, at time.Time) map[string]float64 {
	t.Helper()
	w := &capture{}
	if err := c.Tick(context.Background(), at, &core.World{Metrics: w}); err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, s := range w.batch {
		if s.Kind != promrw.KindGauge || math.IsNaN(s.Value) || math.IsInf(s.Value, 0) || s.Value < 0 {
			t.Fatalf("invalid sample %+v", s)
		}
		if s.Labels["dimension_QueueName"] != "jobs" || s.Labels["name"] != "declared-collector" || s.Labels["account_id"] != "111122223333" {
			t.Fatalf("identity %+v", s.Labels)
		}
		for k := range s.Labels {
			if strings.HasPrefix(k, "dimension_") && k != "dimension_QueueName" {
				t.Fatalf("invented dimension %s", k)
			}
		}
		if strings.Contains(s.Name, "quiet_groups") || strings.Contains(s.Name, "noisy_groups") || s.Name == "aws_sqs_info" {
			t.Fatalf("unadmitted family %s", s.Name)
		}
		out[s.Name] = s.Value
	}
	return out
}
func TestRegistrationBuildTick(t *testing.T) {
	c := build(t, queue())
	now := time.Unix(1800000000, 0)
	m := tick(t, c, now)
	if m["aws_sqs_number_of_messages_sent_sum"] != 60 || m["aws_sqs_sent_message_size_sum"] != 60*256 || m["aws_sqs_sent_message_size_sample_count"] != 60 || m["aws_sqs_sent_message_size_average"] != 256 {
		t.Fatalf("wrong event/byte statistics: %v", m)
	}
	if m["aws_sqs_approximate_number_of_messages_visible_maximum"] <= 0 || m["aws_sqs_approximate_age_of_oldest_message_maximum"] <= 0 || m["aws_sqs_approximate_number_of_messages_delayed_maximum"] <= 0 || m["aws_sqs_approximate_number_of_messages_not_visible_maximum"] <= 0 {
		t.Fatal("queue stock/age absent")
	}
	received := m["aws_sqs_number_of_messages_received_sum"]
	deleted := m["aws_sqs_number_of_messages_deleted_sum"]
	if received <= deleted || received > 30 {
		t.Fatalf("delivery/deletion ordering: %v/%v", received, deleted)
	}
	if len(tick(t, c, now)) != 0 || len(tick(t, c, now.Add(-time.Minute))) != 0 {
		t.Fatal("repeated/backward tick invented activity")
	}
	m = tick(t, c, now.Add(24*time.Hour))
	if m["aws_sqs_number_of_messages_sent_sum"] != 60 {
		t.Fatal("elapsed gap replayed unobserved traffic")
	}
}
func TestSparseTrafficAndNoIdleCapacityBank(t *testing.T) {
	q := queue()
	q.ArrivalsPerSecond = 1.0 / 120
	q.ReceivesPerSecond = 1.0 / 120
	q.DelaySeconds = 0
	q.ProcessingSeconds = 0
	c := build(t, q)
	now := time.Unix(1800000000, 0)
	totals := map[string]float64{}
	for i := 0; i < 8; i++ {
		for k, v := range tick(t, c, now.Add(time.Duration(i)*time.Minute)) {
			totals[k] += v
		}
	}
	if totals["aws_sqs_number_of_messages_sent_sum"] != 4 || totals["aws_sqs_number_of_messages_received_sum"] < 3 {
		t.Fatalf("lost sparse residual: %v", totals)
	}
	q.ArrivalsPerSecond = 1.0 / 600
	q.ReceivesPerSecond = 100
	c = build(t, q)
	for i := 0; i < 9; i++ {
		m := tick(t, c, now.Add(time.Duration(i)*time.Minute))
		if m["aws_sqs_number_of_messages_received_sum"] != 0 {
			t.Fatal("zero arrivals created delivery")
		}
	}
	m := tick(t, c, now.Add(9*time.Minute))
	if m["aws_sqs_number_of_messages_received_sum"] != 1 {
		t.Fatal("idle capacity fabricated extra deliveries")
	}
	// Delay builds a visible-stock burst after ten minutes of empty polling.
	// A banked model would drain more than the six current-minute poll tokens.
	q.ArrivalsPerSecond = 1
	q.ReceivesPerSecond = 0.1
	q.DelaySeconds = 600
	c = build(t, q)
	for i := 0; i < 10; i++ {
		m := tick(t, c, now.Add(time.Duration(i)*time.Minute))
		if m["aws_sqs_number_of_empty_receives_sum"] != 6 || m["aws_sqs_number_of_messages_received_sum"] != 0 {
			t.Fatalf("idle poll tokens not spent: %v", m)
		}
	}
	m = tick(t, c, now.Add(10*time.Minute))
	if m["aws_sqs_number_of_messages_received_sum"] != 6 || m["aws_sqs_number_of_empty_receives_sum"] != 0 {
		t.Fatalf("banked idle capacity serviced burst: %v", m)
	}
	q.ArrivalsPerSecond = 0
	q.ReceivesPerSecond = 0
	if len(tick(t, build(t, q), now)) != 0 {
		t.Fatal("inactive queue emitted")
	}
}

// Public config decode -> Registration.Build -> Tick -> real metric sink seam.
func TestYAMLInventoryAndConservation(t *testing.T) {
	const specimen = `queues:
  - name: jobs
    collector_name: declared-collector
    arrivals_per_second: 1
    receives_per_second: 0.5
    message_bytes: 256
    delay_seconds: 2
    processing_seconds: 3
    enrichment:
      service: worker
      owner: platform
`
	cfg := Registration().NewConfig()
	dec := yaml.NewDecoder(strings.NewReader(specimen))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		t.Fatal(err)
	}
	c, err := Registration().Build(cfg, cloud())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1800000000, 0)
	expected := []string{"aws_sqs_approximate_age_of_oldest_message", "aws_sqs_approximate_number_of_messages_delayed", "aws_sqs_approximate_number_of_messages_not_visible", "aws_sqs_approximate_number_of_messages_visible", "aws_sqs_number_of_empty_receives", "aws_sqs_number_of_messages_deleted", "aws_sqs_number_of_messages_received", "aws_sqs_number_of_messages_sent", "aws_sqs_sent_message_size"}
	w := &capture{}
	if err := c.Tick(context.Background(), now, &core.World{Metrics: w}); err != nil {
		t.Fatal(err)
	}
	inventory := map[string]float64{}
	for _, s := range w.batch {
		inventory[s.Name] = s.Value
		if s.Labels["service"] != "worker" || s.Labels["tag_Owner"] != "platform" {
			t.Fatal("declared enrichment lost")
		}
		if _, ok := s.Labels["tag_Capture"]; ok {
			t.Fatal("union-filled P1 into P2")
		}
		if _, ok := s.Labels["tag_Environment"]; ok {
			t.Fatal("omitted tag invented")
		}
	}
	for _, base := range expected {
		for _, suffix := range []string{"_sum", "_average", "_maximum", "_minimum", "_sample_count"} {
			if _, ok := inventory[base+suffix]; !ok {
				t.Fatalf("missing sourced series %s%s", base, suffix)
			}
		}
	}
	if len(inventory) != len(expected)*5 {
		t.Fatalf("unexpected families %v", inventory)
	}
	totalSent := inventory["aws_sqs_number_of_messages_sent_sum"]
	totalReceived := inventory["aws_sqs_number_of_messages_received_sum"]
	totalDeleted := inventory["aws_sqs_number_of_messages_deleted_sum"]
	for i := 1; i <= 120; i++ {
		m := tick(t, c, now.Add(time.Duration(i)*time.Second))
		totalSent += m["aws_sqs_number_of_messages_sent_sum"]
		totalReceived += m["aws_sqs_number_of_messages_received_sum"]
		totalDeleted += m["aws_sqs_number_of_messages_deleted_sum"]
		visible := m["aws_sqs_approximate_number_of_messages_visible_average"]
		delayed := m["aws_sqs_approximate_number_of_messages_delayed_average"]
		flying := m["aws_sqs_approximate_number_of_messages_not_visible_average"]
		if totalSent != totalDeleted+visible+delayed+flying || totalReceived != totalDeleted+flying {
			t.Fatalf("stock conservation at %d: sent=%v received=%v deleted=%v visible=%v delayed=%v inflight=%v", i, totalSent, totalReceived, totalDeleted, visible, delayed, flying)
		}
	}
	// Unknown fair-queue declarations must not silently claim fair-queue modelling.
	dec = yaml.NewDecoder(strings.NewReader("queues:\n  - name: jobs\n    fair_queue: true\n"))
	dec.KnownFields(true)
	if err := dec.Decode(Registration().NewConfig()); err == nil {
		t.Fatal("accepted unsupported fair queue")
	}
}

func TestGapDoesNotBackfillCompletions(t *testing.T) {
	q := queue()
	q.DelaySeconds = 0
	q.ProcessingSeconds = 30
	q.ReceivesPerSecond = 1
	c := build(t, q)
	now := time.Unix(1800000000, 0)
	first := tick(t, c, now)
	if first["aws_sqs_number_of_messages_deleted_sum"] != 30 {
		t.Fatal("unexpected startup completion count")
	}
	next := tick(t, c, now.Add(24*time.Hour))
	if next["aws_sqs_number_of_messages_deleted_sum"] != 30 {
		t.Fatalf("backfilled completions from the unobserved gap: %v", next["aws_sqs_number_of_messages_deleted_sum"])
	}
}

func TestMasterPauseFreezesTrafficAndRetainsResidual(t *testing.T) {
	now := time.Unix(1800000000, 0)
	sparse := queue()
	sparse.ArrivalsPerSecond = 0.007
	sparse.ReceivesPerSecond = 0.003
	sparse.DelaySeconds = 0
	for name, q := range map[string]Queue{"existing_stock": queue(), "sparse_carry": sparse} {
		t.Run(name, func(t *testing.T) {
			c, control := build(t, q), build(t, q)
			tick(t, c, now)
			tick(t, control, now)
			eng := shape.New("UTC", nil)
			eng.SetVolumeMultiplier(0)
			for i := 1; i <= 10; i++ {
				w := &capture{}
				if err := c.Tick(context.Background(), now.Add(time.Duration(i)*time.Minute), &core.World{Metrics: w, Shape: eng}); err != nil {
					t.Fatal(err)
				}
				if len(w.batch) != 0 {
					t.Fatal("real master pause emitted metrics/traffic")
				}
			}
			eng.SetVolumeMultiplier(1)
			// The first positive observation is only a resume boundary, not proof
			// that any of the unknown pause-to-resume interval was active.
			resume := now.Add(24 * time.Hour)
			boundary := &capture{}
			if err := c.Tick(context.Background(), resume, &core.World{Metrics: boundary, Shape: eng}); err != nil {
				t.Fatal(err)
			}
			if len(boundary.batch) != 0 {
				t.Fatal("resume boundary fabricated unobserved active traffic")
			}
			for i := 1; i <= 3; i++ {
				w := &capture{}
				if err := c.Tick(context.Background(), resume.Add(time.Duration(i)*time.Minute), &core.World{Metrics: w, Shape: eng}); err != nil {
					t.Fatal(err)
				}
				want := tick(t, control, now.Add(time.Duration(i)*time.Minute))
				got := map[string]float64{}
				for _, s := range w.batch {
					got[s.Name] = s.Value
				}
				if len(got) != len(want) {
					t.Fatalf("pause lost residual/observations: got %v want %v", got, want)
				}
				for k, v := range want {
					if got[k] != v {
						t.Fatalf("pause caught up or changed active model: %s got %v want %v", k, got[k], v)
					}
				}
			}
		})
	}
}

func TestInitiallyPausedLongGapResumeBoundary(t *testing.T) {
	c := build(t, queue())
	now := time.Unix(1800000000, 0)
	eng := shape.New("UTC", nil)
	eng.SetVolumeMultiplier(0)
	w := &capture{}
	if err := c.Tick(context.Background(), now, &core.World{Metrics: w, Shape: eng}); err != nil {
		t.Fatal(err)
	}
	if len(w.batch) != 0 {
		t.Fatal("initial pause emitted")
	}
	eng.SetVolumeMultiplier(1)
	resume := now.Add(24 * time.Hour)
	if err := c.Tick(context.Background(), resume, &core.World{Metrics: w, Shape: eng}); err != nil {
		t.Fatal(err)
	}
	if len(w.batch) != 0 {
		t.Fatal("initially paused long-gap resume emitted unobserved traffic")
	}
	m := tick(t, c, resume.Add(time.Second))
	if m["aws_sqs_number_of_messages_sent_sum"] != 1 || m["aws_sqs_number_of_messages_received_sum"] != 0 || m["aws_sqs_approximate_number_of_messages_delayed_average"] != 1 || m["aws_sqs_approximate_age_of_oldest_message_maximum"] != 0 {
		t.Fatalf("resume did not start with exactly one active second: %v", m)
	}
}

func TestAdmission(t *testing.T) {
	fx := cloud()
	fx.Cloud.CloudWatchExport = "otlp"
	if _, err := Build(&Config{Queues: []Queue{queue()}}, fx); err == nil || !strings.Contains(err.Error(), "otlp") {
		t.Fatal("must explicitly reject native OTLP")
	}
	for _, v := range []float64{-1, math.NaN(), math.Inf(1), 1e20} {
		q := queue()
		q.ArrivalsPerSecond = v
		if _, err := Build(&Config{Queues: []Queue{q}}, cloud()); err == nil {
			t.Fatalf("accepted invalid arrival %v", v)
		}
	}
	q := queue()
	q.CollectorName = ""
	if _, err := Build(&Config{Queues: []Queue{q}}, cloud()); err == nil {
		t.Fatal("invented collector identity")
	}
	other := queue()
	other.CollectorName = "other-collector"
	if _, err := Build(&Config{Queues: []Queue{queue(), other}}, cloud()); err == nil {
		t.Fatal("duplicate queue")
	}
	other = queue()
	other.Name = "other-queue"
	if _, err := Build(&Config{Queues: []Queue{queue(), other}}, cloud()); err == nil {
		t.Fatal("duplicate collector")
	}
	q = queue()
	q.MessageBytes = 256.5
	if _, err := Build(&Config{Queues: []Queue{q}}, cloud()); err == nil {
		t.Fatal("fractional physical bytes admitted")
	}
	if _, err := Build(&Config{}, cloud()); err == nil {
		t.Fatal("missing explicit queues")
	}
}
