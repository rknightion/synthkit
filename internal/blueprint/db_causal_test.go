// SPDX-License-Identifier: AGPL-3.0-only

package blueprint_test

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/sink/loki"
)

// Test-only fixed demand: 60 transactions/s held for 20 ms offers 1.2 busy connections.
// A 15 ms dependency slowed twelvefold stretches hold to 185 ms, exceeding this ten-slot pool.
// This deliberately deterministic population is not a deployable blueprint.
const dbCausalYAML = `name: db-causal
label: db-causal
shape: business_hours_plateau
timezone: UTC
environments:
  - name: prod
    weight: 1
    cloud:
      provider: aws
      account_id: "111122223333"
      region: eu-west-1
      vpc_id: vpc-db-causal
      cloudwatch: {albs: 0, s3_buckets: 0, nlb: false, ebs: false, nat_gateway: false, eks: false, firehose: false, private_link: false}
    resources:
      - {name: db-engine, kind: dependency, latency_ms: 15, slow_factor: 12}
      - name: db-pool
        kind: pool
        size: 10
        hold_ms: 20
        held_across: db-engine
        acquire_timeout_ms: 3000
        demand: {off_peak_rps: 60, peak_rps: 60, load: constant}
    databases:
      - {name: bound-db, engine: postgres, resource: db-pool, observability: {cloudwatch: true, dbo11y: true, digests: 5}}
      - {name: sibling-db, engine: postgres, observability: {cloudwatch: true, dbo11y: true, digests: 5}}
`

type dbMinute struct {
	cwConnections, dboConnections, readLatency, writeLatency float64
	seconds, sampleDuration                                  float64
	output                                                   []string
}

var dbDurationField = regexp.MustCompile(`(?:^| )(?:query_time|elapsed_time)=([^ ]+)`)

// Drive the real public loader, catalog, composition, master clock and queued output. RunOnce is
// this runner's public step API (there is no Step method); it flushes real recording sink edges.
func runDBCausal(t *testing.T, engine, incident string, legacy, disabled bool) [][2]dbMinute {
	t.Helper()
	reg := runner.Catalog()
	text := strings.ReplaceAll(dbCausalYAML, "engine: postgres", "engine: "+engine) + incident
	res, err := blueprint.Load([]byte(text), reg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	instances := map[string]string{}
	for _, ci := range res.Constructs {
		if strings.HasPrefix(ci.Kind, "dbo11y_") {
			instances[ci.Name] = ci.Fixtures.DB.InstanceKey
		}
	}
	// Consumer discovery must expose both lanes with their actual, different join selectors.
	for _, resource := range res.Resources {
		if resource.Name != "db-pool" {
			continue
		}
		if len(resource.Consumers) != 2 {
			t.Fatalf("pool consumers: %+v", resource.Consumers)
		}
		for _, c := range resource.Consumers {
			key := "dimension_DBInstanceIdentifier"
			if strings.HasPrefix(c.Kind, "dbo11y_") {
				key = "db_instance_identifier"
			}
			if c.Identity[key] != "bound-db" {
				t.Fatalf("consumer identity: %+v", c)
			}
		}
	}
	mc, lc := &coretest.MetricCapture{}, &coretest.LogCapture{}
	r := runner.New(runner.Sinks{Metrics: mc, Logs: lc}, reg, runner.Options{})
	if err := r.AddBlueprint(res); err != nil {
		t.Fatalf("AddBlueprint: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	defer r.DrainQueues(ctx)
	start := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	var result [][2]dbMinute
	for minute := 0; minute <= 5; minute++ {
		ctl := control.State{VolumeMultiplier: 1}
		if legacy {
			ctl.Failures = map[string]control.FailureSetting{}
			for _, mode := range []string{"slow_query_storm", "connection_saturation", "lock_contention", "replication_lag"} {
				ctl.Failures[mode] = control.FailureSetting{Enabled: true, Intensity: 1, Scope: "bound-db"}
			}
		}
		if disabled && minute == 1 {
			ctl.DisabledKinds = []string{"dbo11y_" + engine}
		}
		r.ApplyControl(ctl)
		now := start.Add(time.Duration(minute) * time.Minute)
		if minute > 0 {
			// Uneven caller spacing, including master ticks at which neither metric lane emits.
			for _, sec := range []int{4, 13, 27, 42, 55} {
				if err := r.MasterTick(ctx, now.Add(-time.Minute).Add(time.Duration(sec)*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
		}
		mc.Batches, lc.Streams = nil, nil // preceding RunOnce joined every queued write
		if err := r.RunOnce(ctx, now); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		var pair [2]dbMinute
		for i, name := range []string{"bound-db", "sibling-db"} {
			out := &pair[i]
			seen := map[string]bool{}
			for _, s := range mc.All() {
				cw := s.Labels["dimension_DBInstanceIdentifier"] == name
				dbo := s.Labels["instance"] == instances[name]
				if !cw && !dbo {
					continue
				}
				b, err := json.Marshal(s)
				if err != nil {
					t.Fatal(err)
				}
				out.output = append(out.output, string(b))
				seen[s.Name] = true
				switch s.Name {
				case "aws_rds_database_connections_average":
					out.cwConnections = s.Value
				case "aws_rds_read_latency_average":
					out.readLatency = s.Value
				case "aws_rds_write_latency_average":
					out.writeLatency = s.Value
				case "pg_stat_database_numbackends", "mysql_global_status_threads_connected":
					out.dboConnections = s.Value
				case "pg_stat_statements_seconds_total", "mysql_perf_schema_events_statements_seconds_total":
					out.seconds += s.Value
				}
				if dbo && s.Labels["blueprint"] != "" {
					t.Fatal("dbo blueprint label leaked")
				}
				for k := range s.Labels {
					if strings.Contains(k, "causal") || k == "resource" {
						t.Fatalf("model label leaked: %s", k)
					}
				}
			}
			conn, seconds := "pg_stat_database_numbackends", "pg_stat_statements_seconds_total"
			if engine == "mysql" {
				conn, seconds = "mysql_global_status_threads_connected", "mysql_perf_schema_events_statements_seconds_total"
			}
			for _, required := range []string{"aws_rds_database_connections_average", "aws_rds_read_latency_average", "aws_rds_write_latency_average", conn, seconds} {
				if disabled && minute == 1 && (required == conn || required == seconds) {
					continue
				}
				if !seen[required] {
					t.Fatalf("%s: missing %s at minute %d", name, required, minute)
				}
			}
			for _, stream := range lc.Streams {
				if stream.Labels["instance"] != instances[name] {
					continue
				}
				if stream.Labels["blueprint"] != "" {
					t.Fatal("dbo stream blueprint label leaked")
				}
				for _, entry := range stream.Lines {
					// Canonicalize each entry separately: delivery batches and stream order are not contracts.
					b, err := json.Marshal(struct {
						Labels map[string]string
						Entry  loki.Line
					}{stream.Labels, entry})
					if err != nil {
						t.Fatal(err)
					}
					out.output = append(out.output, string(b))
					if stream.Labels["op"] != "query_sample" {
						continue
					}
					m := dbDurationField.FindStringSubmatch(entry.Body)
					if len(m) != 2 {
						t.Fatalf("sample lacks duration: %s", entry.Body)
					}
					d, err := time.ParseDuration(strings.Trim(m[1], `"`))
					if err != nil {
						t.Fatal(err)
					}
					if d <= 0 {
						t.Fatal("nonpositive sample duration")
					}
					out.sampleDuration += d.Seconds()
				}
			}
			slices.Sort(out.output)
			if !(disabled && minute == 1) && out.sampleDuration <= 0 {
				t.Fatalf("%s lacks query samples", name)
			}
		}
		result = append(result, pair)
	}
	return result
}

func dbNear(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9*math.Max(1, math.Abs(want)) {
		t.Errorf("%s: got %.12g, want %.12g", what, got, want)
	}
}

func TestDBCausalBothLanesThroughLoader(t *testing.T) {
	for _, engine := range []string{"postgres", "mysql"} {
		t.Run(engine, func(t *testing.T) {
			base := runDBCausal(t, engine, "", false, false)
			slowIncident := "incidents:\n  - {kind: dependency_slow, target: db-engine, at: \"12:01\", for: 2m}\n"
			slow := runDBCausal(t, engine, slowIncident, false, false)
			pool := runDBCausal(t, engine, "incidents:\n  - {kind: pool_exhausted, target: db-pool, at: \"12:01\", for: 2m}\n", false, false)
			legacy := runDBCausal(t, engine, "", true, false)
			resumed := runDBCausal(t, engine, slowIncident, false, true)
			for i := range base {
				for name, run := range map[string][][2]dbMinute{"slow": slow, "pool": pool, "legacy": legacy} {
					if !slices.Equal(run[i][1].output, base[i][1].output) {
						t.Errorf("%s: unbound sibling changed at minute %d", name, i)
					}
					dbNear(t, name+" same-minute pool connections", run[i][0].dboConnections, run[i][0].cwConnections)
				}
				if !slices.Equal(legacy[i][0].output, base[i][0].output) {
					t.Errorf("direct legacy injection moved bound output at minute %d", i)
				}
			}
			// Full-fault minute vs matching healthy clock/seed: both cloud latency gauges, sample durations
			// and *increments*, not accumulated counter ratios, must move together.
			b, hot := base[2][0], slow[2][0]
			ratio := hot.sampleDuration / b.sampleDuration
			if ratio <= 2 || hot.readLatency <= b.readLatency || hot.writeLatency <= b.writeLatency {
				t.Fatalf("no coherent onset: base %+v hot %+v", b, hot)
			}
			dbNear(t, "statement counter delta scales with query duration", hot.seconds-slow[1][0].seconds, ratio*(b.seconds-base[1][0].seconds))
			// Native CW base latencies are 0.8 and 0.5 ms; equal noise cancels in this paired run.
			dbNear(t, "CW read latency response", hot.readLatency-b.readLatency, 0.0008*(ratio-1))
			dbNear(t, "CW write latency response", hot.writeLatency-b.writeLatency, 0.0005*(ratio-1))
			if hot.dboConnections <= b.dboConnections {
				t.Error("slow held-across dependency did not fill pool")
			}
			if pool[2][0].dboConnections >= b.dboConnections {
				t.Error("pool capacity loss did not reduce busy connections")
			}
			dbNear(t, "pool loss alone does not invent query latency", pool[2][0].sampleDuration, b.sampleDuration)
			dbNear(t, "pool loss alone does not invent statement time", pool[2][0].seconds-pool[1][0].seconds, b.seconds-base[1][0].seconds)
			t.Logf("%s: healthy/hot/recovered pool connections=%g/%g/%g; sample duration ratio=%g; statement-second deltas=%g/%g/%g; CW read seconds=%g/%g/%g", engine, b.dboConnections, hot.dboConnections, slow[4][0].dboConnections, ratio, b.seconds-base[1][0].seconds, hot.seconds-slow[1][0].seconds, slow[4][0].seconds-slow[3][0].seconds, b.readLatency, hot.readLatency, slow[4][0].readLatency)
			for _, run := range [][][2]dbMinute{slow, pool} {
				recovered := run[4][0]
				dbNear(t, "recovered connections", recovered.dboConnections, base[4][0].dboConnections)
				dbNear(t, "recovered read latency", recovered.readLatency, base[4][0].readLatency)
				dbNear(t, "recovered write latency", recovered.writeLatency, base[4][0].writeLatency)
				dbNear(t, "recovered query duration", recovered.sampleDuration, base[4][0].sampleDuration)
				dbNear(t, "recovered counter delta", recovered.seconds-run[3][0].seconds, base[4][0].seconds-base[3][0].seconds)
			}
			for _, i := range []int{2, 4, 5} {
				dbNear(t, "resumed dbo connection history", resumed[i][0].dboConnections, slow[i][0].dboConnections)
				dbNear(t, "resumed cross-lane connections", resumed[i][0].dboConnections, resumed[i][0].cwConnections)
				dbNear(t, "resumed sample duration", resumed[i][0].sampleDuration, slow[i][0].sampleDuration)
			}
		})
	}
}

func TestDBCausalAdmissionGuards(t *testing.T) {
	for _, engine := range []string{"postgres", "mysql"} {
		text := strings.ReplaceAll(dbCausalYAML, "engine: postgres", "engine: "+engine)
		cases := map[string]string{
			"cloudwatch required": strings.Replace(text, "cloudwatch: true", "cloudwatch: false", 1),
			"legacy incident":     text + "incidents:\n  - {kind: slow_query_storm, target: bound-db, at: \"12:01\", for: 2m}\n",
			"legacy scenario":     text + "scenarios:\n  - {name: invalid, effects: [{mode: lock_contention, target: bound-db}]}\n",
		}
		for name, invalid := range cases {
			t.Run(engine+"/"+name, func(t *testing.T) {
				_, err := blueprint.Load([]byte(invalid), runner.Catalog())
				want := "unknown target"
				if name == "cloudwatch required" {
					want = "needs the cloudwatch lane"
				}
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("want %s: %v", want, err)
				}
			})
		}
		for _, resource := range []string{"absent-resource", "bad-kind"} {
			invalid := strings.Replace(text, "resource: db-pool", "resource: "+resource, 1)
			if resource == "bad-kind" {
				invalid = strings.Replace(invalid, "    resources:\n", "    resources:\n      - {name: bad-kind, kind: queue, size: 2, hold_ms: 20, demand: {off_peak_rps: 1, peak_rps: 1}}\n", 1)
			}
			res, err := blueprint.Load([]byte(invalid), runner.Catalog())
			if err != nil {
				t.Fatal(err)
			} // binding validation belongs to registered Build
			r := runner.New(runner.Sinks{}, runner.Catalog(), runner.Options{})
			if err := r.AddBlueprint(res); err == nil {
				t.Fatalf("accepted binding %s", resource)
			}
		}
	}
	for _, engine := range []string{"docdb", "neptune"} {
		_, err := blueprint.Load([]byte(strings.ReplaceAll(dbCausalYAML, "engine: postgres", "engine: "+engine)), runner.Catalog())
		if err == nil || !strings.Contains(err.Error(), "dbo11y") || !strings.Contains(err.Error(), engine) {
			t.Fatalf("unsupported %s dbo: %v", engine, err)
		}
	}
}
