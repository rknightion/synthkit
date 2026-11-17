// SPDX-License-Identifier: AGPL-3.0-only

package solace_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/solace"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/shape"
)

// These are queue arithmetic fixtures, not deployment rate recommendations.
func fractionalConfig(queues ...solace.QueueConfig) *solace.Config {
	return &solace.Config{Brokers: []solace.BrokerConfig{{Name: "broker", VPNs: []solace.VPNConfig{{Name: "vpn", Queues: queues}}}}}
}

func TestFractionalTrafficConservation(t *testing.T) {
	for _, spacing := range []time.Duration{time.Minute, 17 * time.Second} {
		t.Run(spacing.String(), func(t *testing.T) {
			cfg := fractionalConfig(
				solace.QueueConfig{Name: "sparse", PublishMsgsPerSec: f64(.003), ConsumeMsgsPerSec: f64(0), AvgMsgBytes: 1000},
				solace.QueueConfig{Name: "draining", PublishMsgsPerSec: f64(.03), ConsumeMsgsPerSec: f64(.004), AvgMsgBytes: 1000},
				// Two messages fit; subsequent integral arrivals must be discarded, not retained as debt.
				solace.QueueConfig{Name: "full", PublishMsgsPerSec: f64(.003), ConsumeMsgsPerSec: f64(0), AvgMsgBytes: 500000, SpoolQuotaMB: 1},
			)
			c := build(t, cfg)
			elapsed := 0.0
			for i := 0; i < 100; i++ {
				mc := &coretest.MetricCapture{}
				if err := c.Tick(context.Background(), testNow.Add(time.Duration(i)*spacing), &core.World{Metrics: mc}); err != nil {
					t.Fatal(err)
				}
				secs := spacing.Seconds()
				if i == 0 {
					secs = 60
				}
				elapsed += secs
				var sumRx, sumTx, sumDepth, sumDiscard float64
				for j, q := range cfg.Brokers[0].VPNs[0].Queues {
					labels := map[string]string{"queue_name": q.Name}
					value := func(name string) float64 { return last(t, mc, name, labels).Value }
					rx, tx, depth, discard := value("solace_queue_msg_spooled"), value("solace_queue_msg_total_deleted"), value("solace_queue_spool_usage_msgs"), value("solace_queue_msg_spool_usage_exceeded")
					want := *q.PublishMsgsPerSec * elapsed
					if err := want - rx - discard; math.Abs(err) >= 1+1e-9 {
						t.Fatalf("tick %d %s arrivals=%v want=%v (error must be <1)", i, q.Name, rx+discard, want)
					}
					if j == 1 {
						if err := *q.ConsumeMsgsPerSec*elapsed - tx; math.Abs(err) >= 1+1e-9 {
							t.Errorf("tick %d drain=%v capacity=%v (error must be <1)", i, tx, *q.ConsumeMsgsPerSec*elapsed)
						}
					}
					if depth != rx-tx || depth < 0 || rx != math.Trunc(rx) || tx != math.Trunc(tx) || discard != math.Trunc(discard) {
						t.Fatalf("nonintegral or nonconserving queue %s: rx=%v tx=%v depth=%v discarded=%v", q.Name, rx, tx, depth, discard)
					}
					if value("solace_queue_spool_usage_bytes") != depth*float64(q.AvgMsgBytes) || value("solace_queue_byte_spooled") != rx*float64(q.AvgMsgBytes) {
						t.Fatal("byte conservation failed")
					}
					sumRx += rx
					sumTx += tx
					sumDepth += depth
					sumDiscard += discard
				}
				for name, want := range map[string]float64{"solace_vpn_rx_msgs_total": sumRx, "solace_system_rx_msgs_total": sumRx, "solace_vpn_tx_msgs_total": sumTx, "solace_system_tx_msgs_total": sumTx, "solace_vpn_spool_usage_msgs": sumDepth, "solace_system_spool_usage_msgs": sumDepth, "solace_vpn_rx_discarded_msgs_total": sumDiscard, "solace_system_total_rx_discards": sumDiscard} {
					if got := last(t, mc, name, nil).Value; got != want {
						t.Fatalf("%s=%v want %v", name, got, want)
					}
				}
			}
		})
	}
}

func TestFractionalPauseAndIdleCapacity(t *testing.T) {
	c := build(t, fractionalConfig(
		solace.QueueConfig{Name: "sparse", PublishMsgsPerSec: f64(.003), ConsumeMsgsPerSec: f64(0)},
		solace.QueueConfig{Name: "backlog", PublishMsgsPerSec: f64(.04), ConsumeMsgsPerSec: f64(.004)},
	))
	engine := shape.New("UTC", nil)
	totals := map[string]float64{}
	for i := 0; i < 105; i++ {
		// Pause with fractional arrivals pending, drain the existing backlog, then resume.
		volume := 0.0
		if i < 4 || i == 104 {
			volume = 1
		}
		engine.SetVolumeMultiplier(volume)
		mc := &coretest.MetricCapture{}
		if err := c.Tick(context.Background(), testNow.Add(time.Duration(i)*time.Minute), &core.World{Metrics: mc, Shape: engine}); err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{"sparse", "backlog"} {
			labels := map[string]string{"queue_name": q}
			rx := last(t, mc, "solace_queue_msg_spooled", labels).Value
			if i >= 4 && i < 104 && rx != totals[q] {
				t.Fatalf("paused %s arrivals advanced: %v -> %v", q, totals[q], rx)
			}
			totals[q] = rx
		}
		if i == 103 && last(t, mc, "solace_queue_spool_usage_msgs", map[string]string{"queue_name": "backlog"}).Value != 0 {
			t.Fatal("fractional consumer never drained paused backlog")
		}
		if i == 104 {
			// A 0.24-message window cannot delete a whole message after an idle period.
			labels := map[string]string{"queue_name": "backlog"}
			rx := last(t, mc, "solace_queue_msg_spooled", labels).Value
			tx := last(t, mc, "solace_queue_msg_total_deleted", labels).Value
			if rx-tx < 1 {
				t.Fatal("idle consumer capacity banked into resumed traffic")
			}
		}
	}
}
