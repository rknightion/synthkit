// SPDX-License-Identifier: AGPL-3.0-only

package integration_test

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/runner"
)

// Test-only declaration (not a deployable blueprint): a batch queue whose consumers drain on a
// schedule beside an unscheduled peer at the same publish rate. The windows are 10 minutes long
// every 30, opening 15 minutes in, so with the 09:00 start they run 09:15-09:25, 09:45-09:55 and
// so on. The batch consumers can move 40 msg/s, ten times what a period's arrivals need, so each
// window clears its backlog. The rates are the construct's own defaults for a master-data feed
// (2 msg/s at the business peak).
func solaceDrainBlueprint(batchQueue bool) string {
	queues := "              - { name: audit, publish_msgs_per_sec: 2 }\n"
	if batchQueue {
		queues = "              - { name: batch, publish_msgs_per_sec: 2, consume_msgs_per_sec: 40, drain_schedule: { period: 30m, window: 10m, phase: 15m } }\n" + queues
	}
	return `
name: solace-drain
label: solace-drain
environments:
  - name: prod
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-fixture }
integrations:
  solace:
    targets: [QueueDetails, QueueStats, VpnSpool, GlobalStats, Spool]
    brokers:
      - name: broker-a
        vpns:
          - name: vpn-1
            queues:
` + queues
}

// TestSolaceScheduledDrainMakesASawtooth loads the blueprint through the real loader and checks
// that the batch queue's depth rises between windows, empties inside each one, and delivers each
// period's arrivals, while the unscheduled peer is exactly what it is without the batch queue.
func TestSolaceScheduledDrainMakesASawtooth(t *testing.T) {
	const minutes = 180
	got := renderSolace(t, solaceDrainBlueprint(true), minutes)
	alone := renderSolace(t, solaceDrainBlueprint(false), minutes)

	// Minute m's tick covers (m-1, m] after 09:00, so the windows (minutes 15-25 of every 30)
	// are the ticks 16 to 25 of each period.
	depth := func(m int) float64 { return solaceSeries(got[m-1], "solace_queue_spool_usage_msgs", "batch") }
	spooled := func(m int) float64 { return solaceSeries(got[m-1], "solace_queue_msg_spooled", "batch") }
	deleted := func(m int) float64 { return solaceSeries(got[m-1], "solace_queue_msg_total_deleted", "batch") }

	peak := 0.0
	for p := 0; p < minutes/30; p++ {
		b := p * 30 // period p covers minutes b+1..b+30; the window is minutes b+16..b+25
		// Rising: nothing is delivered before the window opens, and the backlog only grows.
		for m := b + 1; m <= b+15; m++ {
			if m == 1 {
				if deleted(m) != 0 {
					t.Fatalf("minute 1 (before window): %v messages delivered", deleted(m))
				}
				continue
			}
			if deleted(m) != deleted(m-1) {
				t.Fatalf("minute %d (before window): deliveries moved %v -> %v", m, deleted(m-1), deleted(m))
			}
			if depth(m) < depth(m-1) {
				t.Fatalf("minute %d (before window): depth fell %v -> %v", m, depth(m-1), depth(m))
			}
		}
		if depth(b+15) <= depth(b+1)+10 {
			t.Fatalf("period %d: depth barely rose before the window (%v -> %v)", p, depth(b+1), depth(b+15))
		}
		peak = max(peak, depth(b+15))
		// Falling: the first window minute delivers, and the queue is empty well inside the window.
		if depth(b+16) >= depth(b+15) {
			t.Fatalf("period %d: depth did not fall in the first window minute (%v -> %v)", p, depth(b+15), depth(b+16))
		}
		for m := b + 18; m <= b+25; m++ {
			if depth(m) != 0 {
				t.Fatalf("minute %d (inside window): depth %v, want 0", m, depth(m))
			}
		}
		// Idle again after the window closes.
		for m := b + 26; m <= b+30; m++ {
			if deleted(m) != deleted(b+25) {
				t.Fatalf("minute %d (after window): deliveries moved %v -> %v", m, deleted(b+25), deleted(m))
			}
		}
		// The window ends empty, so everything spooled so far has been delivered; per period the
		// consumers deliver exactly the arrivals.
		if spooled(b+25) != deleted(b+25) {
			t.Fatalf("period %d window end: spooled %v, deleted %v", p, spooled(b+25), deleted(b+25))
		}
		if p > 0 {
			ds, dd := spooled(b+25)-spooled(b-5), deleted(b+25)-deleted(b-5)
			if ds != dd || ds == 0 {
				t.Fatalf("period %d: delivered %v of %v arrivals", p, dd, ds)
			}
		}
	}
	if peak < 100 {
		t.Fatalf("backlog never built up (peak %v)", peak)
	}

	// The VPN spool roll-up follows the batch queue's sawtooth (the peer holds no backlog).
	if v, q := solaceVPNSpool(got[15-1]), depth(15)+solaceSeries(got[15-1], "solace_queue_spool_usage_msgs", "audit"); v != q || v == 0 {
		t.Fatalf("vpn spool msgs %v, queues sum %v", v, q)
	}

	// The unscheduled peer is untouched by its neighbour's schedule: every series carrying its
	// queue_name is identical with and without the batch queue.
	n := 0
	for i := range got {
		for k, v := range got[i] {
			if !strings.Contains(k, "queue_name=audit") {
				continue
			}
			n++
			if w, ok := alone[i][k]; !ok || w != v {
				t.Fatalf("minute %d: peer series %s = %v, alone %v (present %v)", i+1, k, v, w, ok)
			}
		}
	}
	if n == 0 {
		t.Fatal("no peer series compared")
	}
}

// TestSolaceDrainScheduleOnBoundQueueIsRejected: a queue bound to a causal resource drains by the
// resource's model, so it cannot also declare a schedule.
func TestSolaceDrainScheduleOnBoundQueueIsRejected(t *testing.T) {
	text := strings.Replace(solaceCausalBase,
		"{ name: orders-q, resource: orders-queue, avg_msg_bytes: 2048, spool_quota_mb: 4 }",
		"{ name: orders-q, resource: orders-queue, avg_msg_bytes: 2048, spool_quota_mb: 4, drain_schedule: { period: 1h, window: 10m } }", 1)
	reg := runner.Catalog()
	res, err := blueprint.Load([]byte(text), reg)
	if err == nil {
		built := 0
		for _, ci := range res.Constructs {
			if ci.Kind != "solace" {
				continue
			}
			cr, _ := reg.Construct(ci.Kind)
			if _, err = cr.Build(ci.Config, ci.Fixtures); err != nil {
				break
			}
			built++
		}
		if err == nil {
			t.Fatalf("a bound queue with drain_schedule loaded and built (%d solace constructs)", built)
		}
	}
	if !strings.Contains(err.Error(), "drain_schedule") || !strings.Contains(err.Error(), "orders-queue") {
		t.Fatalf("error does not name drain_schedule and the resource: %v", err)
	}
}
