// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
)

// schemaResourcesYAML is a test-only declaration (never a deployable blueprint): one resource per
// consumer shape the control schema lists. The sizes only have to validate; nothing here emits.
const schemaResourcesYAML = `
name: schema-resources
label: schema-resources
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
          - name: enrich-fn
            resource: enrich-fn-concurrency
          - name: legacy-fn
            invocations_per_min: 120
            duration_ms: 50
      sqs:
        queues:
          - name: validation-queue
            collector_name: validation-collector
            message_bytes: 2048
            resource: validation-queue
      agentcore:
        agents: [planner, retriever]
        runtimes:
          - { agent: planner, resource: planner-concurrency }
    cluster:
      type: eks
      name: schema-eks
      node_groups:
        - { name: general, instance_type: m6i.large, desired: 2 }
    databases:
      - {engine: postgres, version: "16.2", name: orders-db, resource: orders-pool}
      - {engine: docdb, version: "5.0", name: records-docdb, resource: records-db, observability: {cloudwatch: true}}
      - {engine: postgres, version: "16.2", name: audit-db}
    resources:
      - { name: partner-api, kind: dependency, latency_ms: 120, slow_factor: 4 }
      - name: enrich-fn-concurrency
        kind: concurrency
        size: 20
        hold_ms: 900
        held_across: partner-api
        demand: { off_peak_rps: 10, peak_rps: 10, load: constant }
      - name: planner-concurrency
        kind: concurrency
        size: 20
        hold_ms: 900
        held_across: partner-api
        demand: { off_peak_rps: 10, peak_rps: 10, load: constant }
      - { name: external-validator, kind: dependency, latency_ms: 350, slow_factor: 3 }
      - name: validation-queue
        kind: queue
        size: 4
        hold_ms: 400
        held_across: external-validator
        demand: { off_peak_rps: 8, peak_rps: 8, load: constant }
      - name: orders-queue
        kind: queue
        size: 4
        hold_ms: 400
        demand: { off_peak_rps: 0, peak_rps: 5, load: constant }
      - { name: orders-db-engine, kind: dependency, latency_ms: 80, slow_factor: 4 }
      - name: orders-pool
        kind: pool
        size: 20
        hold_ms: 100
        held_across: orders-db-engine
        acquire_timeout_ms: 2000
        demand: { off_peak_rps: 50, peak_rps: 50, load: constant }
      - { name: records-db, kind: dependency, latency_ms: 5, slow_factor: 6 }
      - name: api-pool-prod
        kind: pool
        size: 10
        hold_ms: 150
        held_across: partner-api
        acquire_timeout_ms: 1000
        demand: { off_peak_rps: 1, peak_rps: 10 }
workloads:
  - type: app
    name: intake
    runs_on: schema-eks
    traffic: { off_peak_rps: 1, peak_rps: 10 }
    services:
      - { name: api, entry: true, uses: [api-pool], calls: [partner-gateway] }
      - { name: partner-gateway, external: true, uses: [partner-api] }
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
  - name: partner-slow
    effects: [{ mode: dependency_slow, target: partner-api }]
`

func loadSchemaResources(t *testing.T) control.Schema {
	t.Helper()
	res, err := blueprint.Load([]byte(schemaResourcesYAML), Catalog())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	r := New(Sinks{}, Catalog(), Options{})
	if err := r.AddBlueprint(res); err != nil {
		t.Fatalf("AddBlueprint: %v", err)
	}
	return r.ControlSchema()
}

func resourceNamed(t *testing.T, sc control.Schema, name string) control.ResourceInfo {
	t.Helper()
	for _, ri := range sc.Resources {
		if ri.Name == name {
			return ri
		}
	}
	t.Fatalf("schema lists no resource %q: %+v", name, sc.Resources)
	return control.ResourceInfo{}
}

// GET /control/schema lists, per declared resource, the consumers the blueprint binds to it, each
// with the identity its own series carry.
func TestControlSchemaListsBoundConsumersOfEveryResource(t *testing.T) {
	sc := loadSchemaResources(t)
	if len(sc.Resources) != 10 {
		t.Fatalf("resources = %d, want the 10 declared: %+v", len(sc.Resources), sc.Resources)
	}
	cases := []struct {
		resource string
		kind     string
		want     []control.ResourceConsumer
	}{
		{"enrich-fn-concurrency", "concurrency", []control.ResourceConsumer{
			{Kind: "lambda", Name: "prod", Identity: map[string]string{"dimension_FunctionName": "enrich-fn", "account_id": "111122223333", "region": "eu-west-1"}},
		}},
		{"planner-concurrency", "concurrency", []control.ResourceConsumer{
			{Kind: "agentcore", Name: "prod", Identity: map[string]string{"name": "arn:aws:bedrock-agentcore:eu-west-1:111122223333:runtime/planner"}},
		}},
		{"validation-queue", "queue", []control.ResourceConsumer{
			{Kind: "sqs", Name: "prod", Identity: map[string]string{"dimension_QueueName": "validation-queue", "name": "validation-collector", "account_id": "111122223333", "region": "eu-west-1"}},
		}},
		{"orders-queue", "queue", []control.ResourceConsumer{
			{Kind: "solace", Name: "solace", Identity: map[string]string{"instance": "broker-a:9628", "vpn_name": "vpn-1", "queue_name": "orders-q"}},
		}},
		{"orders-pool", "pool", []control.ResourceConsumer{
			{Kind: "rds", Name: "orders-db", Identity: map[string]string{"dimension_DBInstanceIdentifier": "orders-db"}},
		}},
		{"records-db", "dependency", []control.ResourceConsumer{
			{Kind: "docdb", Name: "records-docdb", Identity: map[string]string{"dimension_DBClusterIdentifier": "records-docdb"}},
		}},
		{"partner-api", "dependency", []control.ResourceConsumer{
			{Kind: "app", Name: "intake", Service: "partner-gateway", Callers: []string{"api"},
				Identity: map[string]string{"service": "partner-gateway", "deployment_environment_name": "prod"}},
		}},
		{"api-pool-prod", "pool", []control.ResourceConsumer{
			{Kind: "app", Name: "intake", Service: "api",
				Identity: map[string]string{"service": "api", "deployment_environment_name": "prod"}},
		}},
		{"orders-db-engine", "dependency", []control.ResourceConsumer{}},
		{"external-validator", "dependency", []control.ResourceConsumer{}},
	}
	for _, tc := range cases {
		ri := resourceNamed(t, sc, tc.resource)
		if ri.Blueprint != "schema-resources" || ri.Kind != tc.kind || ri.Env != "prod" {
			t.Errorf("%s = %+v, want blueprint schema-resources, kind %s, env prod", tc.resource, ri, tc.kind)
		}
		if !reflect.DeepEqual(ri.Consumers, tc.want) {
			t.Errorf("%s consumers = %+v, want %+v", tc.resource, ri.Consumers, tc.want)
		}
	}
	if got := resourceNamed(t, sc, "api-pool-prod"); got.Alias != "api-pool" {
		t.Errorf("api-pool-prod alias = %q, want the base name api-pool", got.Alias)
	}
	if got := resourceNamed(t, sc, "orders-pool"); got.HeldAcross != "orders-db-engine" {
		t.Errorf("orders-pool held_across = %q, want orders-db-engine", got.HeldAcross)
	}
	// A function the resource does not bind, and the unbound database, are no consumer of anything.
	for _, ri := range sc.Resources {
		for _, c := range ri.Consumers {
			if c.Identity["dimension_FunctionName"] == "legacy-fn" || c.Identity["dimension_DBInstanceIdentifier"] == "audit-db" {
				t.Errorf("%s lists unbound consumer %+v", ri.Name, c)
			}
		}
	}
}

// The wire form: consumers is an array even when empty, identity is a plain string map, and the
// customer projection keeps its curated subset.
func TestControlSchemaResourcesWireForm(t *testing.T) {
	sc := loadSchemaResources(t)
	b, err := json.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Resources []map[string]json.RawMessage `json:"resources"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Resources) == 0 {
		t.Fatalf("schema JSON has no resources (%d bytes)", len(b))
	}
	for _, ri := range doc.Resources {
		if string(ri["consumers"]) == "null" || ri["consumers"] == nil {
			t.Errorf("resource %s has null consumers", ri["name"])
		}
	}
	if cs := control.CustomerSchema(sc); len(cs.Resources) != 0 {
		t.Errorf("customer schema exposes resources: %+v", cs.Resources)
	}
}

// A blueprint that declares no resource serializes exactly the keys it did before the field existed.
func TestControlSchemaWithoutResourcesIsUnchanged(t *testing.T) {
	r, _, _, _, _, _, _ := newTestRunner(t)
	if err := r.AddBlueprint(resolvedWithIncidentModel("t")); err != nil {
		t.Fatal(err)
	}
	sc := r.ControlSchema()
	if sc.Resources != nil {
		t.Fatalf("resources = %+v, want none", sc.Resources)
	}
	b, err := json.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		t.Fatal(err)
	}
	var got []string
	for k := range keys {
		got = append(got, k)
	}
	slices.Sort(got)
	want := []string{"blueprint_meta", "blueprints", "constructs", "kinds", "modes", "scenarios", "targets", "volume_multiplier"}
	if !slices.Equal(got, want) {
		t.Fatalf("schema keys = %v, want %v", got, want)
	}
}

// A base-name use resolves per environment: each environment's app lists itself, with its own
// environment label, as the consumer of that environment's resource.
func TestControlSchemaResolvesBaseNameUsesPerEnvironment(t *testing.T) {
	const doc = `
name: schema-envs
label: schema-envs
environments:
  - name: prod
    cloud: {provider: aws, account_id: "111122223333", region: eu-west-1, vpc_id: vpc-prod}
    cluster:
      type: eks
      name: prod-eks
      node_groups:
        - { name: general, instance_type: m6i.large, desired: 2 }
    resources:
      - { name: api-pool-prod, kind: pool, size: 10, hold_ms: 20, acquire_timeout_ms: 1000, demand: { off_peak_rps: 1, peak_rps: 10 } }
  - name: dev
    cloud: {provider: aws, account_id: "111122223334", region: eu-west-1, vpc_id: vpc-dev}
    cluster:
      type: eks
      name: dev-eks
      node_groups:
        - { name: general, instance_type: m6i.large, desired: 2 }
    resources:
      - { name: api-pool-dev, kind: pool, size: 5, hold_ms: 20, acquire_timeout_ms: 1000, demand: { off_peak_rps: 1, peak_rps: 10 } }
workloads:
  - type: app
    name: intake-prod
    runs_on: prod-eks
    traffic: { off_peak_rps: 1, peak_rps: 10 }
    services:
      - { name: api, entry: true, uses: [api-pool] }
  - type: app
    name: intake-dev
    runs_on: dev-eks
    traffic: { off_peak_rps: 1, peak_rps: 10 }
    services:
      - { name: api, entry: true, uses: [api-pool] }
`
	res, err := blueprint.Load([]byte(doc), Catalog())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	r := New(Sinks{}, Catalog(), Options{})
	if err := r.AddBlueprint(res); err != nil {
		t.Fatal(err)
	}
	sc := r.ControlSchema()
	for env, workload := range map[string]string{"prod": "intake-prod", "dev": "intake-dev"} {
		ri := resourceNamed(t, sc, "api-pool-"+env)
		want := []control.ResourceConsumer{{Kind: "app", Name: workload, Service: "api",
			Identity: map[string]string{"service": "api", "deployment_environment_name": env}}}
		if ri.Env != env || ri.Alias != "api-pool" || !reflect.DeepEqual(ri.Consumers, want) {
			t.Errorf("%s = %+v, want env %s, alias api-pool and consumers %+v", ri.Name, ri, env, want)
		}
	}
}
