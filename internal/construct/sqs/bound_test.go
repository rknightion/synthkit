// SPDX-License-Identifier: AGPL-3.0-only

package sqs

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
)

// Real-world basis: four consumers each holding a message 400 ms (a 350 ms validator call plus
// handling) serve 10 msg/s against a steady 8 msg/s, so the queue is stable until the validator
// slows.
func boundResources(mutate func(*causal.Resource)) causal.Set {
	q := causal.Resource{
		Name: "jobs-queue", Kind: causal.KindQueue, Size: 4, HoldMS: 400, HeldAcross: "validator",
		Demand: &causal.Demand{OffPeakRPS: 8, PeakRPS: 8, Load: causal.LoadConstant},
	}
	if mutate != nil {
		mutate(&q)
	}
	dep := causal.Resource{Name: "validator", Kind: causal.KindDependency, LatencyMS: 350, SlowFactor: 3}
	return causal.Set{Envs: []causal.Declaration{{Env: "prod", Weight: 1, Resources: []causal.Resource{q, dep}}}}
}

func boundQueue(mutate func(*Queue)) Queue {
	q := Queue{Name: "jobs", CollectorName: "declared-collector", MessageBytes: 256, Resource: "jobs-queue"}
	if mutate != nil {
		mutate(&q)
	}
	return q
}

func buildBound(set causal.Set, qs ...Queue) (core.Construct, error) {
	cfg := &Config{Queues: qs}
	cfg.UseResources(set)
	return Registration().Build(cfg, &fixture.Set{Env: &fixture.Env{Name: "prod"}, Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "eu-west-1"}})
}

func TestBoundQueueRejectsInvalidBinding(t *testing.T) {
	other := boundQueue(func(q *Queue) { q.Name, q.CollectorName = "jobs-2", "declared-collector-2" })
	for name, tc := range map[string]struct {
		set  causal.Set
		qs   []Queue
		want string
	}{
		"no resources declared": {causal.Set{}, []Queue{boundQueue(nil)}, "environments[].resources"},
		"unknown resource":      {boundResources(nil), []Queue{boundQueue(func(q *Queue) { q.Resource = "nope" })}, `"nope"`},
		"resource of wrong kind": {boundResources(nil), []Queue{boundQueue(func(q *Queue) { q.Resource = "validator" })},
			"not a queue"},
		"max_depth": {boundResources(func(r *causal.Resource) { r.MaxDepth = 100 }), []Queue{boundQueue(nil)}, "max_depth"},
		"arrivals_per_second": {boundResources(nil), []Queue{boundQueue(func(q *Queue) { q.ArrivalsPerSecond = 1 })},
			"arrivals_per_second must be omitted"},
		"receives_per_second": {boundResources(nil), []Queue{boundQueue(func(q *Queue) { q.ReceivesPerSecond = 1 })},
			"receives_per_second must be omitted"},
		"processing_seconds": {boundResources(nil), []Queue{boundQueue(func(q *Queue) { q.ProcessingSeconds = 1 })},
			"processing_seconds must be omitted"},
		"delay_seconds": {boundResources(nil), []Queue{boundQueue(func(q *Queue) { q.DelaySeconds = 1 })},
			"delay_seconds must be omitted"},
		"load":                 {boundResources(nil), []Queue{boundQueue(func(q *Queue) { q.Load = "business" })}, "load must be omitted"},
		"no message size":      {boundResources(nil), []Queue{boundQueue(func(q *Queue) { q.MessageBytes = 0 })}, "message_bytes"},
		"resource bound twice": {boundResources(nil), []Queue{boundQueue(nil), other}, "already bound"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := buildBound(tc.set, tc.qs...); err == nil {
				t.Fatal("accepted")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
		})
	}
	if _, err := buildBound(boundResources(nil), boundQueue(nil)); err != nil {
		t.Fatalf("valid binding rejected: %v", err)
	}
}

func TestBoundQueueNeedsTheShapeEngine(t *testing.T) {
	c, err := buildBound(boundResources(nil), boundQueue(nil))
	if err != nil {
		t.Fatal(err)
	}
	err = c.Tick(context.Background(), time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC), &core.World{Metrics: &capture{}})
	if err == nil || !strings.Contains(err.Error(), "shape engine") {
		t.Fatalf("want a shape engine error, got %v", err)
	}
}

// A bound queue emits only the names the model backs: no NotVisible, Delayed or EmptyReceives.
func TestBoundQueueEmitsOnlyModelBackedFamilies(t *testing.T) {
	c, err := buildBound(boundResources(nil), boundQueue(nil))
	if err != nil {
		t.Fatal(err)
	}
	eng := shape.New("UTC", nil)
	got := tickShaped(t, c, eng, time.Date(2026, 10, 24, 10, 1, 0, 0, time.UTC))
	for _, fam := range []string{"number_of_messages_sent", "number_of_messages_received", "number_of_messages_deleted", "sent_message_size", "approximate_number_of_messages_visible", "approximate_age_of_oldest_message"} {
		if _, ok := got["aws_sqs_"+fam+"_sum"]; !ok {
			t.Errorf("missing %s in %v", fam, got)
		}
	}
	for name := range got {
		for _, banned := range []string{"not_visible", "delayed", "empty_receives"} {
			if strings.Contains(name, banned) {
				t.Errorf("bound queue emitted %s", name)
			}
		}
	}
	if got["aws_sqs_number_of_messages_sent_sum"] != 480 || got["aws_sqs_number_of_messages_received_sum"] != 480 {
		t.Errorf("steady 8 msg/s must send and receive 480 per minute: %v", got)
	}
	if got["aws_sqs_sent_message_size_sum"] != 480*256 {
		t.Errorf("size sum %v, want 480 messages of 256 bytes", got["aws_sqs_sent_message_size_sum"])
	}
}

// Only a bound queue reads the model: an unbound queue beside it is exactly the legacy queue,
// and a blueprint that declares resources but binds nothing builds no model.
func TestUnboundQueueIsUnchangedBesideResources(t *testing.T) {
	legacy := queue()
	want := tick(t, build(t, legacy), time.Date(2026, 10, 24, 10, 1, 0, 0, time.UTC))
	c, err := buildBound(boundResources(nil), legacy)
	if err != nil {
		t.Fatal(err)
	}
	if c.(*Construct).model != nil {
		t.Fatal("an unbound construct compiled a model")
	}
	got := tick(t, c, time.Date(2026, 10, 24, 10, 1, 0, 0, time.UTC))
	if len(got) != len(want) {
		t.Fatalf("series differ: %v vs %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s: %v, want legacy %v", k, got[k], v)
		}
	}
}

// consumer_slowdown on the queue resource and the runner's AdvanceCausal hook both reach a
// bound construct; faults are read at the hook's instants.
func TestBoundQueueAdvancesThroughTheHookAndFollowsConsumerSlowdown(t *testing.T) {
	c, err := buildBound(boundResources(nil), boundQueue(nil))
	if err != nil {
		t.Fatal(err)
	}
	eng := shape.New("UTC", []string{"consumer_slowdown@2026-10-24T10:00/30m#1@jobs-queue"})
	c.(causal.Advancer).AdvanceCausal(time.Date(2026, 10, 24, 10, 10, 0, 0, time.UTC), eng)
	got := tickShaped(t, c, eng, time.Date(2026, 10, 24, 10, 10, 0, 0, time.UTC))
	// Intensity 1 stops the consumers: nothing is received while the backlog grows 8 msg/s.
	if got["aws_sqs_number_of_messages_received_sum"] != 0 {
		t.Fatalf("stopped consumers still received: %v", got)
	}
	if v := got["aws_sqs_approximate_number_of_messages_visible_maximum"]; v < 4700 {
		t.Fatalf("backlog %v after 10 stopped minutes, want about 4800", v)
	}
}

// A bound queue is identified by the account and region its series carry as well as its queue
// name and collector name, and a label the context does not carry is absent, never empty.
func TestResourceBindingsIdentityCarriesAccountAndRegion(t *testing.T) {
	cfg := &Config{Queues: []Queue{boundQueue(nil), {Name: "plain", CollectorName: "plain-collector"}}}
	got := cfg.ResourceBindings(causal.BindContext{Env: "prod", Region: "eu-west-1", AccountID: "111122223333"})
	want := []causal.Binding{{Resource: "jobs-queue", Identity: map[string]string{
		"dimension_QueueName": "jobs", "name": "declared-collector", "account_id": "111122223333", "region": "eu-west-1",
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bindings = %+v, want %+v", got, want)
	}
	other := cfg.ResourceBindings(causal.BindContext{Region: "us-east-2", AccountID: "444455556666"})
	if reflect.DeepEqual(got[0].Identity, other[0].Identity) {
		t.Fatalf("same-named queue in another account and region has the same identity: %+v", other[0].Identity)
	}
	bare := cfg.ResourceBindings(causal.BindContext{})
	if !reflect.DeepEqual(bare[0].Identity, map[string]string{"dimension_QueueName": "jobs", "name": "declared-collector"}) {
		t.Fatalf("no cloud fixture: identity %+v, want the queue and collector names alone", bare[0].Identity)
	}
}
