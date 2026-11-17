// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// resourceMetric and outcomesMetric are custom metrics reading the two causal value kinds.
const (
	resourceMetric = `
    custom_metrics:
      - name: pool_in_use
        source: test fixture
        instrument: gauge
        value: {resource: {name: %s, field: in_use}}`
	outcomesMetric = `
    custom_metrics:
      - name: intake_503_total
        source: test fixture
        instrument: counter
        value: {outcomes: {status: ["503"]}}`
)

// A resource or outcomes value reads the workload's causal model and hop outcomes. On an app that
// derives none (no uses, call_policies, worker or traffic.from_resource) it would read 0 forever,
// so Build rejects it naming the service and the metric; a resource value must also name a
// resource declared for the app's environment.
func TestCausalValuesNeedACausalApp(t *testing.T) {
	legacy := func(metric string) string {
		return strings.Replace(legacyIntakeApp, "  - {name: intake-api, entry: true, calls: [records-db, enrich-fn, validation-publisher]}\n",
			"  - name: intake-api\n    entry: true\n    calls: [records-db, enrich-fn, validation-publisher]"+metric+"\n", 1)
	}
	causalWith := func(metric string) string {
		return strings.Replace(intakeApp, "\n    call_policies:\n      enrich-fn:", metric+"\n    call_policies:\n      enrich-fn:", 1)
	}
	for name, tc := range map[string]struct {
		doc, want string
		inject    bool
	}{
		"resource on a legacy app":               {legacy(strings.Replace(resourceMetric, "%s", "intake-db-pool", 1)), `app service "intake-api": metric "pool_in_use"`, true},
		"outcomes on a legacy app":               {legacy(outcomesMetric), `app service "intake-api": metric "intake_503_total"`, true},
		"resource on a legacy app, no resources": {legacy(strings.Replace(resourceMetric, "%s", "intake-db-pool", 1)), "derives no causal outcomes", false},
		"undeclared resource on a causal app":    {causalWith(strings.Replace(resourceMetric, "%s", "nope", 1)), `resource "nope" is not declared for environment "prod"`, true},
	} {
		t.Run(name, func(t *testing.T) {
			var err error
			if tc.inject {
				_, err = tryCausalApps(t, causalResources, tc.doc)
			} else {
				_, err = build(decodeStrict(t, tc.doc), core.Binding{Name: "appa", Env: &fixture.Env{Name: "prod", Weight: 1}, Cluster: coretest.Cluster()})
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("build error %v, want one containing %q", err, tc.want)
			}
		})
	}
	// The same values on a causal app naming declared resources build.
	for _, metric := range []string{strings.Replace(resourceMetric, "%s", "intake-db-pool", 1), outcomesMetric} {
		if _, err := tryCausalApps(t, causalResources, causalWith(metric)); err != nil {
			t.Fatalf("causal app with %s: %v", strings.TrimSpace(metric), err)
		}
	}
}
