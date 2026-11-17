// SPDX-License-Identifier: AGPL-3.0-only

package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// Fixture-only constant demand ensures both model windows and real mint/project paths are
// observed in one minute. The rule is application validation, not an execution failure.
const handledLambdaYAML = `
name: handled-rejection
label: handled-rejection
timezone: UTC
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
          - {name: validation-function, resource: execution-capacity, error_fraction: 0, zero_valued_samples: true}
    cluster:
      type: eks
      name: handled-fixture
      emit: false
      node_groups: [{name: general, instance_type: m6i.large, desired: 1}]
    resources:
      - name: execution-capacity
        kind: concurrency
        size: 10
        hold_ms: 100
        held_across: execution-dependency
        demand: {off_peak_rps: 2, peak_rps: 2, load: constant}
      - {name: execution-dependency, kind: dependency, latency_ms: 50}
      - {name: completeness-rule, kind: rule, reject_share: 1, error: {status: 422, type: RecordIncomplete, message: "mandatory field missing"}}
workloads:
  - type: app
    name: validator
    runs_on: handled-fixture
    traffic:
      off_peak_rps: 2
      peak_rps: 2
      error_mix:
        - {status: 422, code: RECORD_INCOMPLETE, weight: 1}
        - {status: 502, code: EXECUTION_UNAVAILABLE, weight: 1}
    services:
      - name: validation-function
        type: worker
        entry: true
        error_rate: 0
        uses: [execution-capacity, completeness-rule]
        calls: [dependency-node]
        spans:
          - name_template: validate record
            attributes:
              fixture.result: {ref: outcome}
              fixture.code: {ref: error_code}
      - {name: dependency-node, type: worker, uses: [execution-dependency], error_rate: 0}
scenarios:
  - name: reject
    effects: [{mode: rule_regression, target: completeness-rule, intensity: 1}]
  - name: fail
    effects: [{mode: dependency_unavailable, target: execution-dependency, intensity: 1}]
incidents:
  - {scenario: reject, at: "2026-06-15T09:00", for: 10m}
  - {scenario: fail, at: "2026-06-15T09:20", for: 10m}
`

// A rule fault is provenance, not proof that every caller handled its failed operation.
// Exercise the loader and runner with two unabsorbing worker callers above the rule origin.
func TestHandledRejectionIsLocalToNestedOperation(t *testing.T) {
	text := strings.Replace(handledLambdaYAML, "\n    name: validator\n", "\n    name: nested-validation\n", 1)
	text = strings.Replace(text, "uses: [execution-capacity, completeness-rule]", "uses: [execution-capacity]", 1)
	text = strings.Replace(text, "calls: [dependency-node]", "calls: [middle]", 1)
	text = strings.Replace(text, "- {name: dependency-node, type: worker, uses: [execution-dependency], error_rate: 0}", `- {name: middle, type: worker, calls: [validator], error_rate: 0}
      - name: validator
        type: worker
        error_rate: 0
        uses: [completeness-rule]
        spans:
          - name_template: reject incomplete record
            attributes:
              fixture.result: {ref: outcome}
              fixture.code: {ref: error_code}`, 1)
	reg := runner.Catalog()
	res, err := blueprint.Load([]byte(text), reg)
	if err != nil {
		t.Fatal(err)
	}
	capture := &coretest.TraceCapture{}
	r := runner.New(runner.Sinks{Traces: capture, Metrics: &coretest.MetricCapture{}}, reg, runner.Options{})
	if err := r.AddBlueprint(res); err != nil {
		t.Fatal(err)
	}
	if err := r.RunOnce(context.Background(), time.Date(2026, 6, 15, 9, 5, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	spans := map[string]otlp.Span{}
	owners := map[string]string{}
	for _, resource := range capture.Resources {
		for _, sp := range resource.Spans {
			spans[sp.SpanID] = sp
			owners[sp.SpanID], _ = resource.Attrs["service.name"].(string)
		}
	}
	origins := 0
	for _, origin := range spans {
		if owners[origin.SpanID] != "validator" || origin.Kind != otlp.KindServer {
			continue
		}
		origins++
		if origin.Status != otlp.StatusUnset || origin.StatusMsg != "" || origin.Attrs["fixture.result"] != "client_error" || origin.Attrs["fixture.code"] != "RECORD_INCOMPLETE" {
			t.Fatalf("rule origin lost handled result/authored marker: %+v", origin)
		}
		if _, ok := origin.Attrs["error.type"]; ok {
			t.Fatalf("handled origin has error.type: %+v", origin)
		}
		// Follow the actual pre-minted parents: validator <- middle CLIENT <- middle
		// SERVER <- entry CLIENT <- entry root. All four callers failed, unabsorbed.
		parent := origin.ParentID
		for i, expected := range []struct {
			owner, name string
			kind        otlp.SpanKind
		}{
			{"middle", "call validator", otlp.KindClient},
			{"middle", "middle", otlp.KindServer},
			{"validation-function", "call middle", otlp.KindClient},
			{"validation-function", "validate record", otlp.KindConsumer},
		} {
			sp, ok := spans[parent]
			if !ok || owners[parent] != expected.owner || sp.Name != expected.name || sp.Kind != expected.kind || sp.TraceID != origin.TraceID {
				t.Fatalf("ancestor %d wrong name/parenting: %+v", i, sp)
			}
			if sp.Status != otlp.StatusError || sp.Attrs["error.type"] != "422" {
				t.Fatalf("unabsorbed caller %s lost ERROR/error evidence: %+v", expected.owner, sp)
			}
			for k := range sp.Attrs {
				if strings.HasPrefix(k, "http.") {
					t.Fatalf("worker caller carries HTTP attribute %s", k)
				}
			}
			parent = sp.ParentID
		}
		if parent != "" {
			t.Fatal("entry root acquired an unexpected parent")
		}
	}
	if origins == 0 {
		t.Fatal("no nested rule operations projected")
	}
}

func TestHandledRejectionBoundLambdaErrors(t *testing.T) {
	for _, tc := range []struct {
		name           string
		minute         int
		status         otlp.StatusCode
		code           string
		executionError bool
	}{
		{"handled", 5, otlp.StatusUnset, "RECORD_INCOMPLETE", false},
		{"execution failure", 25, otlp.StatusError, "EXECUTION_UNAVAILABLE", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := runner.Catalog()
			res, err := blueprint.Load([]byte(handledLambdaYAML), reg)
			if err != nil {
				t.Fatal(err)
			}
			mc, tcapture := &coretest.MetricCapture{}, &coretest.TraceCapture{}
			r := runner.New(runner.Sinks{Metrics: mc, Traces: tcapture}, reg, runner.Options{})
			if err := r.AddBlueprint(res); err != nil {
				t.Fatal(err)
			}
			end := time.Date(2026, 6, 15, 9, tc.minute, 0, 0, time.UTC)
			ctx := context.Background()
			for i := 11; i >= 1; i-- {
				if err := r.MasterTick(ctx, end.Add(-time.Duration(i)*5*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.RunOnce(ctx, end); err != nil {
				t.Fatal(err)
			}
			m := lambdaFamilies(mc, "validation-function")
			// Require the actual Errors series: absence is not zero, and the execution control
			// must produce Errors using the very same shared model, not a fabricated span.
			found := false
			for _, s := range mc.Find("aws_lambda_errors_sum") {
				if s.Labels["dimension_FunctionName"] == "validation-function" {
					found = true
				}
			}
			if !found || m.invocations <= 0 || m.throttles != 0 {
				t.Fatalf("missing real Lambda traffic/errors: %+v, errors present=%v", m, found)
			}
			if tc.executionError {
				if m.errors <= 0 {
					t.Fatalf("execution failure must increment Lambda Errors: %+v", m)
				}
			} else if m.errors != 0 {
				t.Fatalf("handled rejection increments Lambda Errors: %+v", m)
			}
			roots := 0
			for _, resource := range tcapture.Resources {
				if resource.Attrs["service.name"] != "validation-function" {
					continue
				}
				for _, sp := range resource.Spans {
					if sp.Name != "validate record" {
						continue
					}
					roots++
					if sp.Status != tc.status || sp.Attrs["fixture.code"] != tc.code {
						t.Fatalf("root disagrees with operation: %+v", sp)
					}
					if !tc.executionError {
						if sp.StatusMsg != "" {
							t.Fatalf("handled description: %+v", sp)
						}
						if _, ok := sp.Attrs["error.type"]; ok {
							t.Fatalf("handled error.type: %+v", sp)
						}
					}
					for k := range sp.Attrs {
						if strings.HasPrefix(k, "http.") {
							t.Fatalf("worker HTTP attr %s", k)
						}
					}
				}
			}
			if roots == 0 {
				t.Fatal("no actual Build/Mint/Project roots")
			}
		})
	}
}
