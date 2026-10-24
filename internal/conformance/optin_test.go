// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
)

const optInBlockDoc = "```yaml signals opt-in\n" +
	"family: aws_example\n" +
	"scope: blueprint\n" +
	"sink: promrw\n" +
	"opt_in: cloud.example.items[].failure_fraction\n" +
	"source: \"[slug: cw-example]\"\n" +
	"stats: [_sum, _sample_count]\n" +
	"dimension_profiles:\n" +
	"  account: []\n" +
	"  item: [dimension_Item]\n" +
	"metrics:\n" +
	"  - {root: failure_count, type: gauge, unit: count, profiles: [account, item]}\n" +
	"```\n"

func TestOptInBlockIsRecordedAndResolves(t *testing.T) {
	contract, err := ParseSignals(map[string]string{"signals/cw.md": optInBlockDoc})
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.ParseGaps) != 0 || contract.YAMLBlocks != 1 || len(contract.OptIn) != 1 {
		t.Fatalf("gaps=%v blocks=%d opt-in=%d", contract.ParseGaps, contract.YAMLBlocks, len(contract.OptIn))
	}
	block := contract.OptIn[0]
	if block.Field != "cloud.example.items[].failure_fraction" || block.Source != "[slug: cw-example]" || block.Family != "aws_example" {
		t.Fatalf("block = %+v", block)
	}
	if len(block.Names) != 2 || !equalStrings(block.Names["aws_example_failure_count_sum"], []string{"", "dimension_Item"}) {
		t.Fatalf("names = %v", block.Names)
	}
	dump, err := ParseDump(strings.NewReader("== metrics: series name → label keys ==\n" +
		"aws_example_failure_count_sum  {[job]}\n" +
		"aws_example_failure_count_sample_count  {[job]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if r := Compare(contract, dump); r.Resolved != 2 || r.OptInBlocks != 1 {
		t.Fatalf("report = %+v", r)
	}
}

func TestOptInFormatGaps(t *testing.T) {
	defaultBlock := "```yaml signals\nfamily: aws_example\nsink: promrw\nstats: [_sum, _sample_count]\nmetrics:\n  - {root: failure_count, type: gauge}\n```\n"
	for name, tc := range map[string]struct{ doc, want string }{
		"unknown tag":          {strings.Replace(optInBlockDoc, "signals opt-in", "signals optin", 1), `unknown yaml signals tag "optin"`},
		"opt_in in default":    {strings.Replace(optInBlockDoc, "signals opt-in", "signals", 1), "opt_in key in a default block"},
		"missing opt_in field": {strings.Replace(optInBlockDoc, "opt_in: cloud.example.items[].failure_fraction\n", "", 1), "without an opt_in blueprint field"},
		"missing source":       {strings.Replace(optInBlockDoc, "source: \"[slug: cw-example]\"\n", "", 1), "without a source"},
		"unknown profile":      {strings.Replace(optInBlockDoc, "profiles: [account, item]", "profiles: [account, queue]", 1), `unknown profile "queue"`},
		"pattern root":         {strings.Replace(optInBlockDoc, "root: failure_count", "root: failure_*", 1), "is not one concrete name"},
		"also a default name":  {defaultBlock + optInBlockDoc, "opt-in name aws_example_failure_count_sum is also in a default block"},
		"no metrics in opt-in": {strings.Replace(optInBlockDoc, "  - {root: failure_count, type: gauge, unit: count, profiles: [account, item]}\n", "", 1), "without metrics"},
	} {
		t.Run(name, func(t *testing.T) {
			contract, err := ParseSignals(map[string]string{"signals/cw.md": tc.doc})
			if err != nil {
				t.Fatal(err)
			}
			for _, gap := range contract.ParseGaps {
				if strings.Contains(gap.Error, tc.want) {
					return
				}
			}
			t.Fatalf("gaps %v do not contain %q", contract.ParseGaps, tc.want)
		})
	}
}

func TestCheckOptInEmission(t *testing.T) {
	contract, err := ParseSignals(map[string]string{"signals/cw.md": optInBlockDoc})
	if err != nil {
		t.Fatal(err)
	}
	block := contract.OptIn[0]
	full := []EmittedSeries{}
	for _, n := range []string{"aws_example_failure_count_sum", "aws_example_failure_count_sample_count"} {
		full = append(full,
			EmittedSeries{n, map[string]string{"job": "cloud/aws/example"}},
			EmittedSeries{n, map[string]string{"job": "cloud/aws/example", "dimension_Item": "a"}})
	}
	if got := CheckOptInEmission(block, full, nil); len(got) != 0 {
		t.Fatalf("complete emission reported %v", got)
	}
	for name, tc := range map[string]struct {
		enabled, disabled []EmittedSeries
		want              string
	}{
		"name not emitted":    {full[2:], nil, "aws_example_failure_count_sum not emitted with cloud.example.items[].failure_fraction set"},
		"profile not emitted": {append(full[:1:1], full[2:]...), nil, "aws_example_failure_count_sum profile {dimension_Item} not emitted"},
		"emitted by default":  {full, full[:1], "aws_example_failure_count_sum emitted without cloud.example.items[].failure_fraction"},
	} {
		t.Run(name, func(t *testing.T) {
			got := CheckOptInEmission(block, tc.enabled, tc.disabled)
			for _, p := range got {
				if strings.Contains(p, tc.want) {
					return
				}
			}
			t.Fatalf("problems %v do not contain %q", got, tc.want)
		})
	}
	// Labels outside every declared profile (job, account_id) are not dimensions; a
	// key set of declared profile keys matching none of the metric's profiles is.
	block.ProfileKeys["dimension_Region"] = true
	extra := EmittedSeries{"aws_example_failure_count_sum", map[string]string{"job": "j", "dimension_Item": "a", "dimension_Region": "r"}}
	got := CheckOptInEmission(block, append(full, extra), nil)
	if len(got) != 1 || !strings.Contains(got[0], "undeclared profile {dimension_Item,dimension_Region}") {
		t.Fatalf("got %v", got)
	}
	// An undeclared CloudWatch dimension is still part of the compared key set.
	stray := EmittedSeries{"aws_example_failure_count_sum", map[string]string{"dimension_Item": "a", "dimension_IndexName": "i"}}
	got = CheckOptInEmission(block, append(full, stray), nil)
	if len(got) != 1 || !strings.Contains(got[0], "undeclared profile {dimension_IndexName,dimension_Item}") {
		t.Fatalf("got %v", got)
	}
}

// optInFixtures maps each catalogue opt_in field to a test-only blueprint that
// enables it where OPT_IN appears; the disabled run removes that text. Rates here are
// test populations sized to emit inside one tick, not plausible deployment values,
// and never belong in blueprints/. Every opt-in block in signals/ needs an entry and
// every entry needs a block, so a new opt-in block cannot ship unexercised.
var optInFixtures = map[string]struct{ blueprint, optIn string }{
	"cloud.dynamodb.tables[].operations[].throttled_requests_per_second": {
		blueprint: `name: optin-dynamodb
label: optin-dynamodb
environments:
  - name: prod
    cloud:
      provider: aws
      account_id: "111122223333"
      region: us-east-1
      vpc_id: vpc-fixture
      cloudwatch: {albs: 0, s3_buckets: 0, nlb: false, ebs: false, nat_gateway: false, eks: false, firehose: false, private_link: false}
      dynamodb:
        account: {max_reads: 1000, max_writes: 1000, max_table_reads: 500, max_table_writes: 500}
        tables:
          - name: fixture-orders
            collector_name: declared-orders-collector
            provisioned_reads: 10
            provisioned_writes: 10
            operations:
              - {name: GetItem, requests_per_second: 1, read_units: 1, latency_ms: 4OPT_IN}
              - {name: PutItem, requests_per_second: 1, write_units: 1, latency_ms: 3OPT_IN}
`,
		optIn: ", throttled_requests_per_second: 1",
	},
	"cloud.scheduler.schedules[].target_error_fraction": {
		blueprint: `name: optin-scheduler
label: optin-scheduler
environments:
  - name: prod
    cloud:
      provider: aws
      account_id: "111122223333"
      region: us-east-1
      vpc_id: vpc-fixture
      cloudwatch: {albs: 0, s3_buckets: 0, nlb: false, ebs: false, nat_gateway: false, eks: false, firehose: false, private_link: false}
      scheduler:
        schedules:
          - {name: fixture-sync, group: fixture-ops, rate: "rate(1 minute)"OPT_IN}
`,
		optIn: ", target_error_fraction: 1",
	},
}

func emitOnce(t *testing.T, doc string) []EmittedSeries {
	t.Helper()
	reg := runner.Catalog()
	res, err := blueprint.Load([]byte(doc), reg)
	if err != nil {
		t.Fatal(err)
	}
	mc := &coretest.MetricCapture{}
	r := runner.New(runner.Sinks{Metrics: mc}, reg, runner.Options{SendShards: 1, SendDrainDeadline: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.AddBlueprint(res); err != nil {
		t.Fatal(err)
	}
	if err := r.RunOnce(ctx, time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	r.DrainQueues(ctx)
	var out []EmittedSeries
	for _, s := range mc.All() {
		out = append(out, EmittedSeries{Name: s.Name, Labels: s.Labels})
	}
	return out
}

// The committed catalogue's opt-in blocks must name only bases the engine emits when
// the block's field is set, with exactly the declared profiles, and none by default.
func TestOptInCatalogueMatchesEngine(t *testing.T) {
	paths, err := filepath.Glob("../../signals/*.md")
	if err != nil || len(paths) == 0 {
		t.Fatalf("signals documents: %v %v", paths, err)
	}
	files := map[string]string{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		files[p] = string(b)
	}
	contract, err := ParseSignals(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.ParseGaps) != 0 {
		t.Fatalf("catalogue parse gaps: %v", contract.ParseGaps)
	}
	covered := map[string]bool{}
	for _, block := range contract.OptIn {
		fx, ok := optInFixtures[block.Field]
		if !ok {
			t.Errorf("%s %s: no engine fixture enables %s", block.File, block.Family, block.Field)
			continue
		}
		covered[block.Field] = true
		if !strings.Contains(fx.blueprint, "OPT_IN") {
			t.Fatalf("fixture for %s has no OPT_IN marker", block.Field)
		}
		enabled := emitOnce(t, strings.ReplaceAll(fx.blueprint, "OPT_IN", fx.optIn))
		disabled := emitOnce(t, strings.ReplaceAll(fx.blueprint, "OPT_IN", ""))
		for _, problem := range CheckOptInEmission(block, enabled, disabled) {
			t.Errorf("%s %s: %s", block.File, block.Family, problem)
		}
	}
	for field := range optInFixtures {
		if !covered[field] {
			t.Errorf("engine fixture for %s has no opt-in catalogue block", field)
		}
	}
}
