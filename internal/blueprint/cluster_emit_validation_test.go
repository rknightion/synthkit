// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"fmt"
	"strings"
	"testing"
)

// A collection switch must never hide invalid declarations or bypass strict
// registry/config validation. Disabled declarations remain real input contracts.
func TestClusterEmitDisabledStillValidates(t *testing.T) {
	const input = `name: disabled-validation
shape: business_hours_plateau
environments:
  - name: production
    cloud: {provider: aws, account_id: "100000000001", region: eu-west-1, vpc_id: vpc-test}
    cluster:
      type: eks
      name: disabled-cluster
      emit: false
      node_groups: [{name: worker, instance_type: m6i.large, desired: 1}]
      %s
`
	cases := []struct{ name, declaration, errorText string }{
		{"unknown addon", "addons: [{name: nonexistent_addon}]", "nonexistent_addon"},
		{"unknown nested receiver field", "otel: {misspelled_field: true}", "misspelled_field"},
		{"invalid Fleet Management dependency", "k8s_monitoring: {enabled: false, fleet_management: true}", "fleet_management"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load([]byte(fmt.Sprintf(input, tc.declaration)), testRegistry(t))
			if err == nil {
				t.Fatal("disabled declaration bypassed validation")
			}
			if !strings.Contains(err.Error(), tc.errorText) {
				t.Fatalf("wrong rejection: %v", err)
			}
		})
	}
}
