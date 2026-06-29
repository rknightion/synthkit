// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"strings"

	dashboardv2 "github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
)

// clusterVarOptions returns the cluster names from the manifest in order.
func clusterVarOptions(m *Manifest) []string {
	out := make([]string, 0, len(m.Clusters))
	for _, c := range m.Clusters {
		out = append(out, c.Name)
	}
	return out
}

// envVarOptions returns the environment names from the manifest in order.
func envVarOptions(m *Manifest) []string {
	out := make([]string, 0, len(m.Environments))
	for _, e := range m.Environments {
		out = append(out, e.Name)
	}
	return out
}

// accountVarOptions returns the cloud account IDs from the manifest in order.
func accountVarOptions(m *Manifest) []string {
	out := make([]string, 0, len(m.Accounts))
	for _, a := range m.Accounts {
		out = append(out, a.ID)
	}
	return out
}

// toVariableOptions converts a string slice into a slice of dashboardv2.VariableOption.
func toVariableOptions(vals []string) []dashboardv2.VariableOption {
	opts := make([]dashboardv2.VariableOption, 0, len(vals))
	for _, v := range vals {
		val := v // capture
		opt := *dashboardv2.NewVariableOption()
		opt.Text = dashboardv2.StringOrArrayOfString{String: &val}
		opt.Value = dashboardv2.StringOrArrayOfString{String: &val}
		opts = append(opts, opt)
	}
	return opts
}

// ClusterVar builds a custom template variable named "cluster" seeded with the estate's
// cluster names. Multi-select and include-all are enabled.
func ClusterVar(m *Manifest) *dashboardv2.CustomVariableBuilder {
	vals := clusterVarOptions(m)
	return dashboardv2.NewCustomVariableBuilder("cluster").
		Label("Cluster").
		Query(strings.Join(vals, ",")).
		Options(toVariableOptions(vals)).
		Multi(true).
		IncludeAll(true)
}

// EnvVar builds a custom template variable named "env" seeded with the estate's
// environment names. Multi-select and include-all are enabled.
func EnvVar(m *Manifest) *dashboardv2.CustomVariableBuilder {
	vals := envVarOptions(m)
	return dashboardv2.NewCustomVariableBuilder("env").
		Label("Environment").
		Query(strings.Join(vals, ",")).
		Options(toVariableOptions(vals)).
		Multi(true).
		IncludeAll(true)
}

// promVarQuery is a minimal cog.Builder[DataQueryKind] for a Prometheus variable query
// (label_values / metric query). The SDK ships no v2 prometheus variable-query builder, so we build
// the DataQueryKind directly, mirroring the prometheus datasource's variable spec (qryType 1 =
// label-values). Datasource is left unset so Grafana resolves the default prometheus datasource —
// the same group-based resolution the panel targets rely on (no hard-coded stack uid).
type promVarQuery struct{ expr string }

func (q promVarQuery) Build() (dashboardv2.DataQueryKind, error) {
	return dashboardv2.DataQueryKind{
		Kind:    "DataQuery",
		Group:   "prometheus",
		Version: "v0",
		Spec: map[string]any{
			"query":   q.expr,
			"refId":   "x",
			"qryType": 1,
		},
	}, nil
}

// LabelValuesVar builds a multi-select Prometheus query template variable backed by a label_values()
// (or metric) expression. allValue is ".*" so the "All" selection regex-matches every value — use
// the variable in selectors as `key=~"$name"`. expr is the FULL query string, e.g.
// `label_values(http_server_request_duration_seconds_count{blueprint="$scenario"}, deployment_environment)`.
// Refreshes on dashboard load, sorted alphabetically.
func LabelValuesVar(name, label, expr string) *dashboardv2.QueryVariableBuilder {
	return dashboardv2.NewQueryVariableBuilder(name).
		Label(label).
		Query(promVarQuery{expr: expr}).
		Refresh(dashboardv2.VariableRefreshOnDashboardLoad).
		Sort(dashboardv2.VariableSortAlphabeticalAsc).
		Multi(true).
		IncludeAll(true).
		AllValue(".*")
}

// ConstVar builds a constant template variable (e.g. blueprint=<name>, infinity_base=<url>) that
// surfaces a fixed value to queries without a visible picker. App-scoped selectors reference it as
// `blueprint="$blueprint"` to pin the dashboard to a specific blueprint's data.
func ConstVar(name, value string) *dashboardv2.ConstantVariableBuilder {
	return dashboardv2.NewConstantVariableBuilder(name).Query(value)
}

// TextVar builds a free-text entry template variable (e.g. a trace_id / correlation_id box) with an
// optional default value.
func TextVar(name, label, dflt string) *dashboardv2.TextVariableBuilder {
	b := dashboardv2.NewTextVariableBuilder(name).Label(label)
	if dflt != "" {
		b = b.Query(dflt)
	}
	return b
}

// AccountVar builds a custom template variable named "account" seeded with the estate's
// cloud account IDs. Multi-select and include-all are enabled.
func AccountVar(m *Manifest) *dashboardv2.CustomVariableBuilder {
	vals := accountVarOptions(m)
	return dashboardv2.NewCustomVariableBuilder("account").
		Label("Account").
		Query(strings.Join(vals, ",")).
		Options(toVariableOptions(vals)).
		Multi(true).
		IncludeAll(true)
}
