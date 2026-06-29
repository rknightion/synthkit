// SPDX-License-Identifier: AGPL-3.0-only

// emit_messaging.go — Azure Event Hub Namespaces + Service Bus Namespaces
// (extract §1.5 messaging)
//
// ALL metrics use st.Set — window-gauge invariant (extract §1.3).
package cspazure

import (
	"time"

	"github.com/rknightion/synthkit/internal/core"
)

// emitMessaging emits the messaging sub-signal for one subscription.
func (c *construct) emitMessaging(_ time.Time, w *core.World, sub azureSub, bf float64) {
	rg := sub.resourceGroups[4] // rg-messaging
	n := w.Shape.Noise(0.10)
	subSuffix := sub.subscriptionName[len(sub.subscriptionName)-2:]

	// ── Event Hub Namespace ────────────────────────────────────────────────────
	ehnName := "ehn-" + subSuffix + "-01"
	ehnBaseLbls := c.baseLabelsFor(sub, rg, "microsoft.eventhub/namespaces", ehnName)

	// Namespace-level metrics (no dimensionEntityName).
	c.st.Set("azure_microsoft_eventhub_namespaces_activeconnections_maximum_count",
		ehnBaseLbls, rnd(100*bf))
	c.st.Set("azure_microsoft_eventhub_namespaces_connectionsopened_maximum_count",
		ehnBaseLbls, rnd(20*bf))
	c.st.Set("azure_microsoft_eventhub_namespaces_connectionsclosed_maximum_count",
		ehnBaseLbls, rnd(15*bf))
	// Seed/anchor metric.
	c.st.Set("azure_microsoft_eventhub_namespaces_incomingrequests_total_count",
		ehnBaseLbls, rnd(5_000*bf*n))
	c.st.Set("azure_microsoft_eventhub_namespaces_successfulrequests_total_count",
		ehnBaseLbls, rnd(4_900*bf))
	c.st.Set("azure_microsoft_eventhub_namespaces_throttledrequests_total_count",
		ehnBaseLbls, rnd(10*bf))
	c.st.Set("azure_microsoft_eventhub_namespaces_usererrors_total_count",
		ehnBaseLbls, rnd(5*bf))
	c.st.Set("azure_microsoft_eventhub_namespaces_servererrors_total_count",
		ehnBaseLbls, rnd(2*bf))
	c.st.Set("azure_microsoft_eventhub_namespaces_incomingbytes_total_bytes",
		ehnBaseLbls, rnd(50_000_000*bf))
	c.st.Set("azure_microsoft_eventhub_namespaces_outgoingbytes_total_bytes",
		ehnBaseLbls, rnd(60_000_000*bf))

	// Per-hub metrics with dimension_EntityName (extract §C.3/§D.7).
	for _, hubName := range []string{"hub-events", "hub-telemetry", "hub-audit"} {
		hubLbls := mergeLabels(ehnBaseLbls, c.dim(map[string]string{"EntityName": hubName}))
		c.st.Set("azure_microsoft_eventhub_namespaces_incomingmessages_total_count",
			hubLbls, rnd(2000*bf))
		c.st.Set("azure_microsoft_eventhub_namespaces_outgoingmessages_total_count",
			hubLbls, rnd(1900*bf))
		c.st.Set("azure_microsoft_eventhub_namespaces_capturedmessages_total_count",
			hubLbls, rnd(500*bf))
	}

	// ── Service Bus Namespace ──────────────────────────────────────────────────
	sbnName := "sbn-" + subSuffix + "-01"
	sbnBaseLbls := c.baseLabelsFor(sub, rg, "microsoft.servicebus/namespaces", sbnName)

	// Namespace-level metrics.
	c.st.Set("azure_microsoft_servicebus_namespaces_incomingmessages_total_count",
		sbnBaseLbls, rnd(3_000*bf))
	c.st.Set("azure_microsoft_servicebus_namespaces_outgoingmessages_total_count",
		sbnBaseLbls, rnd(2_900*bf))
	c.st.Set("azure_microsoft_servicebus_namespaces_incomingrequests_total_count",
		sbnBaseLbls, rnd(4_000*bf))
	c.st.Set("azure_microsoft_servicebus_namespaces_successfulrequests_total_count",
		sbnBaseLbls, rnd(3_900*bf))
	c.st.Set("azure_microsoft_servicebus_namespaces_activeconnections_total_count",
		sbnBaseLbls, rnd(50*bf))
	c.st.Set("azure_microsoft_servicebus_namespaces_usererrors_total_count",
		sbnBaseLbls, rnd(3*bf))
	c.st.Set("azure_microsoft_servicebus_namespaces_servererrors_total_count",
		sbnBaseLbls, rnd(1*bf))

	// Seed/anchor metric for SB (extract §E.3/§D.8).
	c.st.Set("azure_microsoft_servicebus_namespaces_messages_average_count",
		sbnBaseLbls, rnd(2_900*bf))

	// Per-entity metrics with dimension_EntityName (extract §C.3/§D.8).
	for _, queueName := range []string{"orders-queue", "notifications-queue"} {
		queueLbls := mergeLabels(sbnBaseLbls, c.dim(map[string]string{"EntityName": queueName}))
		c.st.Set("azure_microsoft_servicebus_namespaces_activemessages_average_count",
			queueLbls, rnd(100*bf))
		c.st.Set("azure_microsoft_servicebus_namespaces_size_average_bytes",
			queueLbls, rnd(10_000_000*bf))
	}
}
