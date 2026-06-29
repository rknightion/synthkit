// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import "encoding/json"

// RecordingRule describes a single Grafana managed recording rule.
// IntervalSec is used as the rule group's interval (seconds); only the first
// rule's value in a group is used; zero defaults to 300.
type RecordingRule struct {
	UID         string
	Record      string // recorded metric name
	Datasource  string // "loki" | "mimir" — determines placeholder UID
	Expr        string
	IntervalSec int
}

// AlertRule describes a single Grafana managed alert rule inside a group.
type AlertRule struct {
	UID        string
	Title      string
	Datasource string // "loki" | "mimir"
	Expr       string
	Threshold  float64
	Op         string // "gt", "lt", "gte", "lte", "eq", "neq"
	For        string // e.g. "5m"
	Severity   string
	Labels     map[string]string
	Paused     bool
}

// RuleGroup is a Grafana alert/recording rule group bound to a folder.
type RuleGroup struct {
	Name       string
	FolderUID  string
	Recordings []RecordingRule
	Alerts     []AlertRule
}

// datasourceUID returns the provisioning placeholder UID for a datasource type.
func datasourceUID(ds string) string {
	switch ds {
	case "mimir", "prometheus":
		return "${MIMIR_DATASOURCE_UID}"
	default: // "loki" and anything else
		return "${LOKI_DATASOURCE_UID}"
	}
}

// queryType returns the query type string for a datasource type.
func queryType(ds string) string {
	switch ds {
	case "mimir", "prometheus":
		return "range"
	default:
		return "range"
	}
}

// provisioningRule is the wire format for one rule inside a Grafana provisioning group.
type provisioningRule struct {
	UID          string              `json:"uid,omitempty"`
	Title        string              `json:"title"`
	Condition    string              `json:"condition"`
	Data         []provisioningQuery `json:"data"`
	Record       *provisioningRecord `json:"record,omitempty"`
	NoDataState  string              `json:"noDataState"`
	ExecErrState string              `json:"execErrState"`
	IsPaused     bool                `json:"isPaused"`
	Labels       map[string]string   `json:"labels"`
	Annotations  map[string]string   `json:"annotations"`
	For          string              `json:"for,omitempty"`
}

type provisioningQuery struct {
	RefID             string                 `json:"refId"`
	QueryType         string                 `json:"queryType"`
	RelativeTimeRange relativeTimeRange      `json:"relativeTimeRange"`
	DatasourceUID     string                 `json:"datasourceUid"`
	Model             map[string]interface{} `json:"model"`
}

type relativeTimeRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

type provisioningRecord struct {
	Metric string `json:"metric"`
	From   string `json:"from"`
}

type provisioningGroup struct {
	Name      string             `json:"name"`
	FolderUID string             `json:"folderUID"`
	Interval  int                `json:"interval"`
	Rules     []provisioningRule `json:"rules"`
}

type provisioningOutput struct {
	Groups []provisioningGroup `json:"groups"`
}

// RenderRules marshals a set of rule groups into Grafana provisioning JSON
// (compatible with POST /api/v1/provisioning/alert-rules bulk import).
// scenario is stored as an annotation on every rule for traceability.
func RenderRules(scenario string, groups []RuleGroup) ([]byte, error) {
	out := provisioningOutput{
		Groups: make([]provisioningGroup, 0, len(groups)),
	}

	for _, g := range groups {
		intervalSec := 300
		for _, r := range g.Recordings {
			if r.IntervalSec > 0 {
				intervalSec = r.IntervalSec
				break
			}
		}

		var rules []provisioningRule

		for _, r := range g.Recordings {
			dsUID := datasourceUID(r.Datasource)
			// Recording rules MUST evaluate as instant queries: they record a single
			// sample per series. A range query yields a matrix ("timeseries-multi")
			// that Grafana Cloud remote-write rejects with 422.
			rule := provisioningRule{
				UID:       r.UID,
				Title:     r.Record,
				Condition: "A",
				Data: []provisioningQuery{
					{
						RefID:     "A",
						QueryType: "instant",
						RelativeTimeRange: relativeTimeRange{
							From: 600,
							To:   0,
						},
						DatasourceUID: dsUID,
						Model: map[string]interface{}{
							"expr":      r.Expr,
							"refId":     "A",
							"queryType": "instant",
							"instant":   true,
						},
					},
				},
				Record: &provisioningRecord{
					Metric: r.Record,
					From:   "A",
				},
				NoDataState:  "NoData",
				ExecErrState: "Error",
				IsPaused:     false,
				Labels:       map[string]string{},
				Annotations: map[string]string{
					"scenario": scenario,
				},
			}
			rules = append(rules, rule)
		}

		for _, a := range g.Alerts {
			dsUID := datasourceUID(a.Datasource)
			qt := queryType(a.Datasource)

			labels := map[string]string{
				"severity": a.Severity,
			}
			for k, v := range a.Labels {
				labels[k] = v
			}

			rule := provisioningRule{
				UID:       a.UID,
				Title:     a.Title,
				Condition: "A",
				Data: []provisioningQuery{
					{
						RefID:     "A",
						QueryType: qt,
						RelativeTimeRange: relativeTimeRange{
							From: 600,
							To:   0,
						},
						DatasourceUID: dsUID,
						Model: map[string]interface{}{
							"expr":      a.Expr,
							"refId":     "A",
							"queryType": qt,
						},
					},
				},
				// Record is nil for alert rules
				NoDataState:  "NoData",
				ExecErrState: "Error",
				IsPaused:     a.Paused,
				For:          a.For,
				Labels:       labels,
				Annotations: map[string]string{
					"scenario": scenario,
				},
			}
			rules = append(rules, rule)
		}

		out.Groups = append(out.Groups, provisioningGroup{
			Name:      g.Name,
			FolderUID: g.FolderUID,
			Interval:  intervalSec,
			Rules:     rules,
		})
	}

	return json.MarshalIndent(out, "", "  ")
}
