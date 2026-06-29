// SPDX-License-Identifier: AGPL-3.0-only

// emit_logs.go — Azure Event Hubs log stream (extract §1.5 logs sub-signal)
//
// Stream labels: job="integrations/azure_event_hubs", topic="<hub_name>"
// Body: raw Azure Monitor JSON record (NOT logfmt).
package cspazure

import (
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/sink/loki"
)

// logsForSub returns Azure Monitor log streams for one subscription.
// One stream per Event Hub (3 hubs per namespace) with raw JSON lines.
func (c *construct) logsForSub(now time.Time, sub azureSub) []loki.Stream {
	rg := sub.resourceGroups[4] // rg-messaging
	subSuffix := sub.subscriptionName[len(sub.subscriptionName)-2:]
	ehnNS := "ehn-" + subSuffix + "-01"
	hubNames := []string{"hub-events", "hub-telemetry", "hub-audit"}

	categories := []string{"AuditEvent", "WorkflowRuntime", "AppServiceHTTPLogs"}
	opNames := []string{
		"Microsoft.EventHub/namespaces/Write",
		"Microsoft.EventHub/namespaces/Read",
		"Microsoft.Logic/workflows/workflowTriggerStarted",
	}
	levels := []string{"Information", "Warning", "Error"}

	resourceID := fmt.Sprintf(
		"/subscriptions/%s/resourcegroups/%s/providers/microsoft.eventhub/namespaces/%s",
		sub.subscriptionID, rg, ehnNS,
	)

	var streams []loki.Stream
	for i, hubName := range hubNames {
		labels := map[string]string{
			"job":   "integrations/azure_event_hubs",
			"topic": hubName,
		}
		lines := make([]loki.Line, 3)
		for j := 0; j < 3; j++ {
			cat := categories[(i+j)%len(categories)]
			op := opNames[(i+j)%len(opNames)]
			level := levels[j%len(levels)]
			body := fmt.Sprintf(
				`{"time":%q,"resourceId":%q,"category":%q,"operationName":%q,"level":%q,"location":%q}`,
				now.UTC().Format(time.RFC3339Nano),
				resourceID,
				cat,
				op,
				level,
				sub.region,
			)
			lines[j] = loki.Line{T: now, Body: body}
		}
		streams = append(streams, loki.Stream{Labels: labels, Lines: lines})
	}
	return streams
}
