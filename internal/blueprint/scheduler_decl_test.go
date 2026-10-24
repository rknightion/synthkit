// SPDX-License-Identifier: AGPL-3.0-only

package blueprint_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
)

// schedulerBlueprint is an offline test declaration, not a deployable blueprint.
func schedulerBlueprint(envs ...string) []byte {
	var b strings.Builder
	b.WriteString("name: sched-test\nlabel: sched-test\nenvironments:\n")
	for _, e := range envs {
		b.WriteString(e)
	}
	return []byte(b.String())
}

func schedulerEnv(name, region, scheduler string) string {
	return "  - name: " + name + "\n    cloud:\n      provider: aws\n      account_id: \"111122223333\"\n      region: " + region + "\n" +
		"      vpc_id: vpc-fixture\n" +
		"      cloudwatch: {albs: 0, s3_buckets: 0, nlb: false, ebs: false, nat_gateway: false, eks: false, firehose: false, private_link: false}\n" +
		scheduler
}

const schedulerBlock = "      scheduler:\n        schedules:\n          - {name: nightly, group: reports, rate: \"rate(1 minute)\"}\n          - {name: beat, rate: \"rate(1 minute)\"}\n"

func TestSchedulerDeclarationBuildsAndEmits(t *testing.T) {
	reg := runner.Catalog()
	res, err := blueprint.Load(schedulerBlueprint(schedulerEnv("prod", "eu-west-1", schedulerBlock)), reg)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ci := range res.Constructs {
		if ci.Kind == "scheduler" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("scheduler instances = %d, want 1", n)
	}
	mc := &coretest.MetricCapture{}
	r := runner.New(runner.Sinks{Metrics: mc}, reg, runner.Options{SendShards: 1, SendDrainDeadline: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer r.DrainQueues(ctx)
	if err := r.AddBlueprint(res); err != nil {
		t.Fatal(err)
	}
	if err := r.RunOnce(ctx, time.Unix(1700000000, 0)); err != nil {
		t.Fatal(err)
	}
	groups := map[string]float64{}
	for _, s := range mc.All() {
		if s.Name == "aws_scheduler_invocation_attempt_count_sum" {
			if s.Labels["blueprint"] != "sched-test" {
				t.Fatalf("writer did not stamp blueprint: %v", s.Labels)
			}
			groups[s.Labels["dimension_ScheduleGroup"]] = s.Value
		}
	}
	if groups["reports"] != 1 || groups["default"] != 1 || groups[""] != 2 || len(groups) != 3 {
		t.Fatalf("attempt series %v", groups)
	}
}

func TestSchedulerDeclarationAdmission(t *testing.T) {
	reg := runner.Catalog()
	if _, err := blueprint.Load(schedulerBlueprint(schedulerEnv("a", "eu-west-1", schedulerBlock), schedulerEnv("b", "eu-west-1", schedulerBlock)), reg); err == nil || !strings.Contains(err.Error(), "declaration collision") {
		t.Fatalf("same account/region declared twice: %v", err)
	}
	if _, err := blueprint.Load(schedulerBlueprint(schedulerEnv("a", "eu-west-1", schedulerBlock), schedulerEnv("b", "us-east-1", schedulerBlock)), reg); err != nil {
		t.Fatalf("different regions are distinct: %v", err)
	}
	unknown := "      scheduler:\n        schedules:\n          - {name: x, rate: \"rate(1 hour)\", cron: \"0 * * * ?\"}\n"
	if _, err := blueprint.Load(schedulerBlueprint(schedulerEnv("a", "eu-west-1", unknown)), reg); err == nil {
		t.Fatal("unknown field accepted")
	}
	res, err := blueprint.Load(schedulerBlueprint(strings.Replace(schedulerEnv("a", "eu-west-1", schedulerBlock), "provider: aws\n", "provider: aws\n      cloudwatch_export: otlp\n", 1)), reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.New(runner.Sinks{}, reg, runner.Options{}).AddBlueprint(res); err == nil || !strings.Contains(err.Error(), "remote_write") {
		t.Fatalf("native admission must fail explicitly: %v", err)
	}
}
