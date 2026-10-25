// SPDX-License-Identifier: AGPL-3.0-only

package integration_test

import (
	"context"
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/shape"
)

// Test-only declaration (not a deployable blueprint): one Solace queue bound to a causal queue
// resource, and one unbound peer queue in the same VPN. The resource is a queue drained by four
// consumers at 400 ms a message (10 msg/s) fed a constant 5 msg/s; a 4 MiB spool quota holds
// 2048 messages of 2048 bytes. A consumer slowdown to 20% of the service rate (2 msg/s) lets the
// backlog grow by 3 msg/s until the quota discards, and drains again once the fault ends.
const solaceCausalBase = `
name: solace-causal
label: solace-causal
environments:
  - name: prod
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-fixture }
    resources:
      - name: orders-queue
        kind: queue
        size: 4
        hold_ms: 400
        demand: { off_peak_rps: 0, peak_rps: 5, load: constant }
integrations:
  solace:
    targets: [QueueDetails, QueueStats, VpnSpool, GlobalStats, Spool]
    brokers:
      - name: broker-a
        vpns:
          - name: vpn-1
            queues:
              - { name: orders-q, resource: orders-queue, avg_msg_bytes: 2048, spool_quota_mb: 4 }
              - { name: audit, publish_msgs_per_sec: 2 }
scenarios:
  - name: orders-consumers-slow
    effects:
      - { mode: consumer_slowdown, target: orders-queue, intensity: 0.8 }
incidents:
  - { scenario: orders-consumers-slow, at: "2026-10-24T09:10", for: 20m }
`

// The same unbound peer queue on its own, with no resources and no scenario.
const solaceCausalPeerOnly = `
name: solace-causal-peer
label: solace-causal-peer
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
              - { name: audit, publish_msgs_per_sec: 2 }
`

var solaceCausalStart = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)

// renderSolace builds the blueprint's single solace construct and ticks it once a minute for n
// minutes from solaceCausalStart with the compiled scheduled incidents, returning every sample by
// minute index keyed name{sorted labels}.
func renderSolace(t *testing.T, text string, n int) []map[string]float64 {
	t.Helper()
	reg := runner.Catalog()
	res, err := blueprint.Load([]byte(text), reg)
	if err != nil {
		t.Fatal(err)
	}
	eng := shape.New("UTC", res.Incidents)
	var out []map[string]float64
	built := 0
	for _, ci := range res.Constructs {
		if ci.Kind != "solace" {
			continue
		}
		built++
		cr, _ := reg.Construct(ci.Kind)
		c, err := cr.Build(ci.Config, ci.Fixtures)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := c.(causal.Advancer); built == 1 && !ok && strings.Contains(text, "resource: orders-queue") {
			t.Fatal("a solace construct with a bound queue must implement causal.Advancer so the runner advances it each master tick")
		}
		for i := 1; i <= n; i++ {
			now := solaceCausalStart.Add(time.Duration(i) * time.Minute)
			if a, ok := c.(causal.Advancer); ok {
				a.AdvanceCausal(now, eng)
			}
			mc := &coretest.MetricCapture{}
			if err := c.Tick(context.Background(), now, &core.World{Shape: eng, Metrics: mc}); err != nil {
				t.Fatal(err)
			}
			m := map[string]float64{}
			for _, s := range mc.All() {
				m[solaceKey(s.Name, s.Labels)] = s.Value
			}
			out = append(out, m)
		}
	}
	if built != 1 {
		t.Fatalf("want one solace construct, built %d", built)
	}
	return out
}

func solaceKey(name string, labels map[string]string) string {
	kv := make([]string, 0, len(labels))
	for k, v := range labels {
		kv = append(kv, k+"="+v)
	}
	sort.Strings(kv)
	return name + "{" + strings.Join(kv, ",") + "}"
}

func solaceSeries(m map[string]float64, name, queue string) float64 {
	for k, v := range m {
		if strings.HasPrefix(k, name+"{") && strings.Contains(k, "queue_name="+queue) {
			return v
		}
	}
	return math.NaN()
}

// TestSolaceBoundQueueFollowsCausalModel loads the blueprint through the real loader and checks
// that the bound queue's depth, spool usage, spooled, delivered and discarded series are the
// causal model's backlog, backlog x avg_msg_bytes, admitted, served and rejected: flat while
// healthy, growing while the consumers are slowed, pinned at the spool quota with discards once
// it is full, and draining back to empty after the fault ends.
func TestSolaceBoundQueueFollowsCausalModel(t *testing.T) {
	const minutes = 50
	got := renderSolace(t, solaceCausalBase, minutes)

	// The reference model: the same declaration the construct compiles, with the depth limit its
	// spool quota implies (4 MiB / 2048 B = 2048 messages), advanced with the same engine.
	res, err := blueprint.Load([]byte(solaceCausalBase), runner.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	eng := shape.New("UTC", res.Incidents)
	model, err := causal.Compile(causal.Set{Envs: []causal.Declaration{{Env: "prod", Weight: 1, Resources: []causal.Resource{{
		Name: "orders-queue", Kind: causal.KindQueue, Size: 4, HoldMS: 400, MaxDepth: 2048,
		Demand: &causal.Demand{PeakRPS: 5, Load: causal.LoadConstant},
	}}}}})
	if err != nil {
		t.Fatal(err)
	}

	const q = "orders-q"
	var cumAdmitted, cumRejected, cumServed float64
	prev := solaceCausalStart
	peakDepth, discardsAfterFault, sawCap := 0.0, 0.0, false
	for i := 1; i <= minutes; i++ {
		now := solaceCausalStart.Add(time.Duration(i) * time.Minute)
		model.Advance(now, eng)
		w, ok := model.Window("orders-queue", prev, now)
		if !ok || w.Steps != 12 {
			t.Fatalf("minute %d: window %+v ok=%v", i, w, ok)
		}
		prev = now
		cumAdmitted += w.Admitted
		cumRejected += w.Rejected
		cumServed += w.Served

		m := got[i-1]
		depth := solaceSeries(m, "solace_queue_spool_usage_msgs", q)
		bytes := solaceSeries(m, "solace_queue_spool_usage_bytes", q)
		if want := math.Round(w.BacklogEnd); depth != want {
			t.Fatalf("minute %d: depth %v, model backlog %v", i, depth, want)
		}
		// The counter-typed level is written through state.Add; the series value is the level.
		if bytes != depth*2048 {
			t.Fatalf("minute %d: spool bytes %v, want depth %v x 2048", i, bytes, depth)
		}
		if quota := solaceSeries(m, "solace_queue_spool_quota_bytes", q); bytes > quota {
			t.Fatalf("minute %d: spool usage %v exceeds the quota %v", i, bytes, quota)
		}
		// Cumulative counters track the model's cumulative volumes to within one message
		// (whole messages are emitted; the fraction carries to the next tick).
		for name, want := range map[string]float64{
			"solace_queue_msg_spooled":              cumAdmitted,
			"solace_queue_msg_spool_usage_exceeded": cumRejected,
			"solace_queue_msg_total_deleted":        cumServed,
		} {
			if v := solaceSeries(m, name, q); math.Abs(v-want) >= 1 {
				t.Fatalf("minute %d: %s = %v, model cumulative %v", i, name, v, want)
			}
		}
		active := i >= 10 && i < 30
		switch {
		case i < 10:
			if depth != 0 || solaceSeries(m, "solace_queue_msg_spool_usage_exceeded", q) != 0 {
				t.Fatalf("minute %d: healthy queue holds a backlog or discards (%v)", i, depth)
			}
			if i > 1 {
				if d := solaceSeries(m, "solace_queue_msg_spooled", q) - solaceSeries(got[i-2], "solace_queue_msg_spooled", q); d != 300 {
					t.Fatalf("minute %d: healthy publishes %v per minute, want 300 (5 msg/s)", i, d)
				}
			}
		case active:
			if depth < peakDepth {
				t.Fatalf("minute %d: depth fell during the fault: %v -> %v", i, peakDepth, depth)
			}
			peakDepth = depth
			if depth == 2048 {
				sawCap = true
				if bytes != 4*1048576 {
					t.Fatalf("minute %d: full queue holds %v bytes, want the 4 MiB quota", i, bytes)
				}
			}
		case i >= 38:
			if depth != 0 {
				t.Fatalf("minute %d: queue did not drain after the fault: depth %v", i, depth)
			}
			if i > 38 {
				d := solaceSeries(m, "solace_queue_msg_spool_usage_exceeded", q) - solaceSeries(got[i-2], "solace_queue_msg_spool_usage_exceeded", q)
				discardsAfterFault += d
			}
		}
	}
	if peakDepth < 100 || !sawCap {
		t.Fatalf("fault never grew the backlog to the spool quota (peak depth %v, cap seen %v)", peakDepth, sawCap)
	}
	if cumRejected < 1 {
		t.Fatal("full spool never discarded")
	}
	if discardsAfterFault != 0 {
		t.Fatalf("discards continued after the fault drained: %v", discardsAfterFault)
	}
	// The VPN and broker roll-ups agree with the queue (the unbound peer holds no backlog).
	last := got[minutes-1]
	if vpn, qd := solaceVPNSpool(last), solaceSeries(last, "solace_queue_spool_usage_msgs", q)+solaceSeries(last, "solace_queue_spool_usage_msgs", "audit"); vpn != qd {
		t.Fatalf("vpn spool msgs %v, queues sum %v", vpn, qd)
	}
}

func solaceVPNSpool(m map[string]float64) float64 {
	for k, v := range m {
		if strings.HasPrefix(k, "solace_vpn_spool_usage_msgs{") {
			return v
		}
	}
	return math.NaN()
}

// The unbound peer queue is unchanged by a bound neighbour and by the fault on it: its series
// equal those of a blueprint that declares only the peer, at every tick.
func TestSolaceUnboundQueueUnchangedBesideBoundQueue(t *testing.T) {
	const minutes = 40
	with := renderSolace(t, solaceCausalBase, minutes)
	only := renderSolace(t, solaceCausalPeerOnly, minutes)
	compared := 0
	for i := range with {
		for k, v := range only[i] {
			if !strings.Contains(k, "queue_name=audit") {
				continue
			}
			compared++
			if with[i][k] != v {
				t.Fatalf("minute %d: unbound series %s changed %v -> %v", i+1, k, v, with[i][k])
			}
		}
	}
	if compared == 0 {
		t.Fatal("no unbound series compared")
	}
}

// Unbound queues keep their legacy modes; bound queues are driven by the model alone.
func TestSolaceBoundQueueRejections(t *testing.T) {
	boundLine := `- { name: orders-q, resource: orders-queue, avg_msg_bytes: 2048, spool_quota_mb: 4 }`
	vpn2 := `
          - name: vpn-2
            queues:
              - { name: orders-q, resource: orders-queue, avg_msg_bytes: 2048, spool_quota_mb: 4 }`
	cases := []struct{ name, from, to, want string }{
		{"resource not declared", `resource: orders-queue,`, `resource: nope-queue,`, `"nope-queue"`},
		{"wrong kind", "kind: queue\n", "kind: concurrency\n", `"concurrency"`},
		{"publish rate", `resource: orders-queue,`, `resource: orders-queue, publish_msgs_per_sec: 3,`, "publish_msgs_per_sec"},
		{"consume rate", `resource: orders-queue,`, `resource: orders-queue, consume_msgs_per_sec: 3,`, "consume_msgs_per_sec"},
		{"rate scale", `resource: orders-queue,`, `resource: orders-queue, rate_scale: 2,`, "rate_scale"},
		{"shape profile", `resource: orders-queue,`, `resource: orders-queue, shape_profile: {floor: 0.2},`, "shape_profile"},
		{"resource max depth", "size: 4\n", "size: 4\n        max_depth: 100\n", "max_depth"},
		{"consumers disagree", `resource: orders-queue,`, `resource: orders-queue, consumers: 2,`, "consumers"},
		{"spool below one message", `spool_quota_mb: 4 }`, `spool_quota_mb: 0.0001 }`, "spool_quota_mb"},
		{"same queue name in two vpns", boundLine, boundLine + vpn2, `"orders-queue"`},
		{"two names on one resource", boundLine, boundLine + "\n              - { name: orders-copy, resource: orders-queue }", `"orders-queue"`},
		{"legacy consumer stall", `mode: consumer_slowdown, target: orders-queue`, `mode: queue_consumer_stall, target: orders-q`, `orders-q`},
		{"legacy publish drop", `mode: consumer_slowdown, target: orders-queue`, `mode: queue_publish_drop, target: orders-q`, `orders-q`},
	}
	for _, c := range cases {
		text := strings.Replace(solaceCausalBase, c.from, c.to, 1)
		if text == solaceCausalBase {
			t.Fatalf("%s: mutation %q did not apply", c.name, c.from)
		}
		reg := runner.Catalog()
		res, err := blueprint.Load([]byte(text), reg)
		if err == nil {
			// Build-time rejections surface when the construct is built from the loaded config.
			for _, ci := range res.Constructs {
				if ci.Kind != "solace" {
					continue
				}
				cr, _ := reg.Construct(ci.Kind)
				_, err = cr.Build(ci.Config, ci.Fixtures)
			}
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error containing %s", c.name, err, c.want)
			continue
		}
		t.Logf("%s: %v", c.name, err)
	}
	// And the legacy modes still work on an unbound queue next to the bound one.
	text := strings.Replace(solaceCausalBase, `mode: consumer_slowdown, target: orders-queue`, `mode: queue_consumer_stall, target: audit`, 1)
	if _, err := blueprint.Load([]byte(text), runner.Catalog()); err != nil {
		t.Fatalf("legacy mode on the unbound queue rejected: %v", err)
	}
}
