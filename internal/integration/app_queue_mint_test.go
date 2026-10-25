// SPDX-License-Identifier: AGPL-3.0-only

package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/shape"
)

// appQueueMintYAML is a test-only declaration (never a deployable blueprint): a queue drained by
// a bound function's concurrency resource, and a worker app that consumes the same queue. The
// concurrency limit is far above the offered load, so every served message is admitted and the
// function's Invocations and the worker's requests count the same events. %s is the queue's
// constant arrival rate in messages per second.
const appQueueMintYAML = `
name: app-queue-mint
label: app-queue-mint
environments:
  - name: prod
    cloud:
      provider: aws
      account_id: "111122223333"
      region: eu-west-1
      vpc_id: vpc-fixture
      cloudwatch: {albs: 0, s3_buckets: 0, nlb: false, ebs: false, nat_gateway: false, eks: false, firehose: false, private_link: false}
      lambda:
        functions:
          - name: drain-fn
            resource: drain-concurrency
    cluster:
      type: eks
      name: queue-mint-eks
      node_groups:
        - { name: general, instance_type: m6i.xlarge, desired: 2 }
    resources:
      - { name: drain-dependency, kind: dependency, latency_ms: 100, slow_factor: 4 }
      - name: drain-queue
        kind: queue
        size: 1
        hold_ms: 300
        held_across: drain-dependency
        demand: { off_peak_rps: %[1]s, peak_rps: %[1]s, load: constant }
      - name: drain-concurrency
        kind: concurrency
        size: 200
        hold_ms: 300
        held_across: drain-dependency
        demand: { from: drain-queue }
scenarios:
  - name: slow
    effects: [{ mode: dependency_slow, target: drain-dependency, intensity: 1 }]
incidents:
  - { scenario: slow, at: "2026-10-24T11:00", for: 30m }
workloads:
  - type: app
    name: drain-app
    runs_on: queue-mint-eks
    traffic: { from_resource: drain-queue }
    services:
      - { name: drain-worker, entry: true, worker: true }
`

// Every window of the bound function's Invocations equals the number of requests the worker app
// mints over the same window, over several hours and at fractional rates, so the app's logs and
// traces agree with CloudWatch. A dependency_slow window lowers the queue's served rate part-way
// through; the equality holds across it.
func TestFromResourceAppMintsTheBoundFunctionsInvocations(t *testing.T) {
	for _, rate := range []string{"0.37", "2.9", "0.05"} {
		t.Run("rate="+rate, func(t *testing.T) {
			reg := runner.Catalog()
			res, err := blueprint.Load([]byte(fmt.Sprintf(appQueueMintYAML, rate)), reg)
			if err != nil {
				t.Fatal(err)
			}
			eng := shape.New("UTC", res.Incidents)

			var fn core.Construct
			for _, ci := range res.Constructs {
				if ci.Kind != "lambda" {
					continue
				}
				cr, _ := reg.Construct(ci.Kind)
				if fn, err = cr.Build(ci.Config, ci.Fixtures); err != nil {
					t.Fatal(err)
				}
			}
			if fn == nil {
				t.Fatal("no lambda construct")
			}
			var worker core.Workload
			for _, wi := range res.Workloads {
				if wi.Kind != "app" {
					continue
				}
				wr, _ := reg.Workload(wi.Kind)
				b := core.Binding{Name: wi.Name, Seed: res.Name + ":" + wi.Name, Replicas: wi.Replicas, Env: wi.Env, Cluster: wi.Cluster, Calls: wi.Calls, Databases: wi.Databases}
				if worker, err = wr.Build(wi.Config, b); err != nil {
					t.Fatal(err)
				}
			}
			if worker == nil {
				t.Fatal("no app workload")
			}

			const tick = 15 * time.Second
			led := ledger.New(eng, 0, 0)
			led.SetTickSeconds(tick.Seconds())
			led.AddMinter(worker.Minter())

			// The function's first window is the minute before its first tick; the app's ticks
			// start one tick into that same minute so both count from the same instant.
			start := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
			var minted float64
			windows, slowedWindows, nonzero := 0, 0, 0
			for now := start.Add(-time.Minute + tick); now.Before(start.Add(4 * time.Hour)); now = now.Add(tick) {
				for _, c := range []any{worker, fn} {
					if a, ok := c.(causal.Advancer); ok {
						a.AdvanceCausal(now, eng)
					}
				}
				minted += float64(len(led.Mint(now)))
				if now.Sub(start)%time.Minute != 0 || now.Before(start) {
					continue
				}
				mc := &coretest.MetricCapture{}
				if err := fn.Tick(context.Background(), now, &core.World{Shape: eng, Metrics: mc}); err != nil {
					t.Fatal(err)
				}
				var invocations float64
				for _, s := range mc.All() {
					if s.Name == "aws_lambda_invocations_sum" && s.Labels["dimension_FunctionName"] == "drain-fn" {
						invocations = s.Value
					}
				}
				windows++
				if minted != invocations {
					t.Fatalf("window ending %s: the app minted %v requests, the function counted %v invocations", now.Format(time.RFC3339), minted, invocations)
				}
				if invocations > 0 {
					nonzero++
				}
				if now.After(start.Add(60*time.Minute)) && now.Before(start.Add(90*time.Minute)) {
					slowedWindows++
				}
				minted = 0
			}
			if windows != 240 || slowedWindows == 0 || nonzero == 0 {
				t.Fatalf("checked %d windows (%d during the incident, %d non-empty), want 240 windows over four hours", windows, slowedWindows, nonzero)
			}
		})
	}
}
