// SPDX-License-Identifier: AGPL-3.0-only

package semconv

import "testing"

// TestNamesAreLaw guards the exact string spellings — these are the contract the emit lanes and
// dashboards share. A typo here is a silent cross-surface drift. A slice (not a map) because the
// flat canon keys deliberately share values across the metric-label and resource-attr forms.
func TestNamesAreLaw(t *testing.T) {
	cases := []struct{ got, want string }{
		{LabelDeploymentEnvironmentName, "deployment_environment_name"},
		{LabelServiceName, "service_name"},
		{LabelServiceNamespace, "service_namespace"},
		{LabelServiceVersion, "service_version"},
		{LabelContext, "context"},
		{LabelUseCase, "use_case"},
		{LabelTeam, "team"},
		{AttrDeploymentEnvironmentName, "deployment.environment.name"},
		{AttrServiceName, "service.name"},
		{AttrServiceNamespace, "service.namespace"},
		{AttrServiceVersion, "service.version"},
		{AttrContext, "context"},
		{AttrUseCase, "use_case"},
		{AttrTeam, "team"},
		{AttrCorrelationID, "app.correlation_id"},
		{FieldCorrelationID, "correlation_id"},
		{KeyPortkeyTraceID, "portkey_trace_id"},
		{KeyTraceparent, "traceparent"},
		{TeamAcmeAI, "Acme AI"},
		{ContextPlatform, "Platform"},
		{ContextContentGen, "ContentGen"},
		{ContextDataGen, "DataGen"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("semconv constant = %q, want %q", c.got, c.want)
		}
	}
}
