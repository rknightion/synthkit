// SPDX-License-Identifier: AGPL-3.0-only

package blueprint_test

import (
	"reflect"
	"testing"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/runner"
)

// twoEnvConsumersYAML is a test-only declaration (never a deployable blueprint): two environments
// in different accounts and regions each declare a function and a queue with the same names, each
// bound to that environment's own resource.
const twoEnvConsumersYAML = `
name: two-env-consumers
label: two-env-consumers
environments:
  - name: prod
    cloud:
      provider: aws
      account_id: "111122223333"
      region: eu-west-1
      vpc_id: vpc-prod
      cloudwatch: {albs: 0, s3_buckets: 0, nlb: false, ebs: false, nat_gateway: false, eks: false, firehose: false, private_link: false}
      lambda:
        functions:
          - { name: enrich-fn, resource: enrich-conc }
      sqs:
        queues:
          - { name: work-queue, collector_name: work-collector, message_bytes: 2048, resource: work-q }
    resources:
      - { name: enrich-conc-prod, kind: concurrency, size: 20, hold_ms: 900, demand: { off_peak_rps: 10, peak_rps: 10, load: constant } }
      - { name: work-q-prod, kind: queue, size: 4, hold_ms: 400, demand: { off_peak_rps: 8, peak_rps: 8, load: constant } }
  - name: dev
    weight: 0.25
    cloud:
      provider: aws
      account_id: "444455556666"
      region: us-east-2
      vpc_id: vpc-dev
      cloudwatch: {albs: 0, s3_buckets: 0, nlb: false, ebs: false, nat_gateway: false, eks: false, firehose: false, private_link: false}
      lambda:
        functions:
          - { name: enrich-fn, resource: enrich-conc }
      sqs:
        queues:
          - { name: work-queue, collector_name: work-collector, message_bytes: 2048, resource: work-q }
    resources:
      - { name: enrich-conc-dev, kind: concurrency, size: 20, hold_ms: 900, demand: { off_peak_rps: 10, peak_rps: 10, load: constant } }
      - { name: work-q-dev, kind: queue, size: 4, hold_ms: 400, demand: { off_peak_rps: 8, peak_rps: 8, load: constant } }
`

// A function or queue is identified by its account and region as well as its name: two
// environments declaring a same-named function or queue each list only their own as the bound
// consumer of their own resource, and the identities differ so a query built from them selects
// one environment's series and not the other's.
func TestSameNamedLambdaAndSQSConsumersAreScopedToAccountAndRegion(t *testing.T) {
	r, err := blueprint.Load([]byte(twoEnvConsumersYAML), runner.Catalog())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := map[string]blueprint.ResourceConsumer{
		"enrich-conc-prod": {Kind: "lambda", Name: "prod", Identity: map[string]string{"dimension_FunctionName": "enrich-fn", "account_id": "111122223333", "region": "eu-west-1"}},
		"enrich-conc-dev":  {Kind: "lambda", Name: "dev", Identity: map[string]string{"dimension_FunctionName": "enrich-fn", "account_id": "444455556666", "region": "us-east-2"}},
		"work-q-prod":      {Kind: "sqs", Name: "prod", Identity: map[string]string{"dimension_QueueName": "work-queue", "name": "work-collector", "account_id": "111122223333", "region": "eu-west-1"}},
		"work-q-dev":       {Kind: "sqs", Name: "dev", Identity: map[string]string{"dimension_QueueName": "work-queue", "name": "work-collector", "account_id": "444455556666", "region": "us-east-2"}},
	}
	seen := 0
	for _, res := range r.Resources {
		w, ok := want[res.Name]
		if !ok {
			t.Errorf("unexpected resource %q", res.Name)
			continue
		}
		seen++
		if len(res.Consumers) != 1 || !reflect.DeepEqual(res.Consumers[0], w) {
			t.Errorf("%s consumers = %+v, want only %+v", res.Name, res.Consumers, w)
		}
	}
	if seen != len(want) {
		t.Fatalf("saw %d of %d resources: %+v", seen, len(want), r.Resources)
	}
}
