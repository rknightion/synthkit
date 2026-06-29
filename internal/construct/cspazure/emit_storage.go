// SPDX-License-Identifier: AGPL-3.0-only

// emit_storage.go — Azure Blob Storage + Queue Storage (extract §1.5 storage)
//
// ALL metrics use st.Set — window-gauge invariant (extract §1.3).
package cspazure

import (
	"time"

	"github.com/rknightion/synthkit/internal/core"
)

// emitStorage emits the storage sub-signal (Blob + Queue) for one subscription.
func (c *construct) emitStorage(_ time.Time, w *core.World, sub azureSub, bf float64) {
	rg := sub.resourceGroups[3] // rg-storage
	// Storage account name: must be all-lowercase, no hyphens; use subscription suffix.
	saName := "sa" + sub.subscriptionName[len(sub.subscriptionName)-2:] + "01"

	// ── Blob Storage ──────────────────────────────────────────────────────────
	// ARM resource ID for blob services: ends in /blobservices/default per Azure convention.
	// resourceName is the storage account name (the variable picker anchor, extract §1.6).
	// This is a documented exception to the last-segment==resourceName rule: storage
	// sub-services always end in "default" but the meaningful resource is the account.
	blobLbls := c.storageBaseLabels(sub, rg, saName, "blobservices")

	n := w.Shape.Noise(0.10)
	c.st.Set("azure_microsoft_storage_storageaccounts_blobservices_containercount_average_count",
		blobLbls, rnd(20*bf))
	// blobcount + blobcapacity: dimensioned by Tier + BlobType (extract §SK-16 storage).
	// One series per tier; hot tier carries the most data.
	for i, tier := range []string{"hot", "cool", "transactionoptimized", "untiered"} {
		tierFactor := 1.0 / float64(i+1) // hot=1.0, cool=0.5, transactionoptimized=0.33, untiered=0.25
		tierLbls := mergeLabels(blobLbls, c.dim(map[string]string{
			"BlobType": "blockblob",
			"Tier":     tier,
		}))
		c.st.Set("azure_microsoft_storage_storageaccounts_blobservices_blobcount_average_count",
			tierLbls, rnd(500*bf*tierFactor))
		c.st.Set("azure_microsoft_storage_storageaccounts_blobservices_blobcapacity_average_bytes",
			tierLbls, rnd(10*1024*1024*1024*bf*tierFactor))
	}
	c.st.Set("azure_microsoft_storage_storageaccounts_blobservices_indexcapacity_average_bytes",
		blobLbls, rnd(100*1024*1024*bf))
	c.st.Set("azure_microsoft_storage_storageaccounts_blobservices_ingress_total_bytes",
		blobLbls, rnd(100_000_000*bf))
	c.st.Set("azure_microsoft_storage_storageaccounts_blobservices_egress_total_bytes",
		blobLbls, rnd(200_000_000*bf))
	c.st.Set("azure_microsoft_storage_storageaccounts_blobservices_availability_average_percent",
		blobLbls, 99.9)

	// Transactions with dimension_ApiName + dimension_ResponseType (extract §C.3/§D.2).
	for _, apiName := range []string{"GetBlob", "PutBlob", "ListBlobs"} {
		for _, respType := range []string{"Success", "ServerOtherError"} {
			txBase := 1000.0
			if respType != "Success" {
				txBase = 5
			}
			txLbls := mergeLabels(blobLbls, c.dim(map[string]string{
				"ApiName":      apiName,
				"ResponseType": respType,
			}))
			c.st.Set("azure_microsoft_storage_storageaccounts_blobservices_transactions_total_count",
				txLbls, rnd(txBase*bf*n))
		}
	}

	// ── Queue Storage ─────────────────────────────────────────────────────────
	// Same convention as blob: ARM path ends in /queueservices/default; resourceName is saName.
	queueLbls := c.storageBaseLabels(sub, rg, saName, "queueservices")

	// Seed/anchor metric.
	c.st.Set("azure_microsoft_storage_storageaccounts_queueservices_queuecount_average_count",
		queueLbls, rnd(5*bf))
	c.st.Set("azure_microsoft_storage_storageaccounts_queueservices_queuemessagecount_average_count",
		queueLbls, rnd(200*bf))
	c.st.Set("azure_microsoft_storage_storageaccounts_queueservices_queuecapacity_average_bytes",
		queueLbls, rnd(50*1024*1024*bf))
	c.st.Set("azure_microsoft_storage_storageaccounts_queueservices_ingress_total_bytes",
		queueLbls, rnd(10_000_000*bf))
	c.st.Set("azure_microsoft_storage_storageaccounts_queueservices_egress_total_bytes",
		queueLbls, rnd(5_000_000*bf))
	c.st.Set("azure_microsoft_storage_storageaccounts_queueservices_availability_average_percent",
		queueLbls, 99.9)

	// Queue transactions with dimension_ApiName + dimension_ResponseType labels (extract §D.3).
	for _, apiName := range []string{"GetMessages", "PutMessage"} {
		for _, respType := range []string{"Success", "ServerOtherError"} {
			txBase := 500.0
			if respType != "Success" {
				txBase = 2
			}
			txLbls := mergeLabels(queueLbls, c.dim(map[string]string{
				"ApiName":      apiName,
				"ResponseType": respType,
			}))
			c.st.Set("azure_microsoft_storage_storageaccounts_queueservices_transactions_total_count",
				txLbls, rnd(txBase*bf*n))
		}
	}
}
