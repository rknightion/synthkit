// SPDX-License-Identifier: AGPL-3.0-only

// emit_networking.go — Azure LB, App Gateway, Front Door, Virtual Networks
// (extract §1.5 networking)
//
// Traps:
//   - VNet metrics end in _count (NO aggregation infix: NOT _average_count/_total_count).
//
// (The predecessor's App Gateway instance="integrations/azure_exporter" trap was RETIRED by SK-16:
// `instance` is now path-determined for every resource — see identity.go.)
//
// ALL metrics use st.Set — window-gauge invariant (extract §1.3).
package cspazure

import (
	"time"

	"github.com/rknightion/synthkit/internal/core"
)

// emitNetworking emits the networking sub-signal for one subscription.
func (c *construct) emitNetworking(_ time.Time, w *core.World, sub azureSub, bf float64) {
	rg := sub.resourceGroups[2] // rg-networking
	n := w.Shape.Noise(0.10)
	subSuffix := sub.subscriptionName[len(sub.subscriptionName)-2:]

	// ── Load Balancer ─────────────────────────────────────────────────────────
	lbName := "lb-" + subSuffix + "-01"
	lbLbls := c.baseLabelsFor(sub, rg, "microsoft.network/loadbalancers", lbName)

	c.st.Set("azure_microsoft_network_loadbalancers_syncount_total_count",
		lbLbls, rnd(5000*bf))
	c.st.Set("azure_microsoft_network_loadbalancers_packetcount_total_count",
		lbLbls, rnd(100_000*bf))
	c.st.Set("azure_microsoft_network_loadbalancers_bytecount_total_bytes",
		lbLbls, rnd(100_000_000*bf))
	c.st.Set("azure_microsoft_network_loadbalancers_snatconnectioncount_total_count",
		lbLbls, rnd(2000*bf))
	c.st.Set("azure_microsoft_network_loadbalancers_usedsnatports_average_count",
		lbLbls, rnd(500*bf*n))
	c.st.Set("azure_microsoft_network_loadbalancers_allocatedsnatports_average_count",
		lbLbls, 1024)

	// ── Application Gateway ────────────────────────────────────────────────────
	// NOTE: the predecessor pinned instance="integrations/azure_exporter" here; under the
	// dual-path model the `instance` label is path-determined (serverless=resourceID,
	// azure_exporter=opaque hash) for ALL resources, so App Gateway no longer carries a
	// special instance value (SK-16 — flagged for signals/cspazure.md [slug: cspazure] cleanup).
	agName := "agw-" + subSuffix + "-01"
	agLbls := c.baseLabelsFor(sub, rg, "microsoft.network/applicationgateways", agName)

	c.st.Set("azure_microsoft_network_applicationgateways_totalrequests_total_count",
		agLbls, rnd(10_000*bf))
	// Seed/anchor metric (extract §1.5).
	c.st.Set("azure_microsoft_network_applicationgateways_failedrequests_total_count",
		agLbls, rnd(50*bf))
	c.st.Set("azure_microsoft_network_applicationgateways_responsestatus_total_count",
		agLbls, rnd(9_900*bf))
	c.st.Set("azure_microsoft_network_applicationgateways_throughput_average_bytespersecond",
		agLbls, rnd(10_000_000*bf))
	c.st.Set("azure_microsoft_network_applicationgateways_applicationgatewaytotaltime_average_milliseconds",
		agLbls, rnd(50*bf))
	c.st.Set("azure_microsoft_network_applicationgateways_currentconnections_total_count",
		agLbls, rnd(200*bf))

	// ── Front Door / CDN Profiles ─────────────────────────────────────────────
	fdName := "fd-" + subSuffix + "-01"
	fdBaseLbls := c.baseLabelsFor(sub, rg, "microsoft.cdn/profiles", fdName)

	c.st.Set("azure_microsoft_cdn_profiles_percentage4xx_average_percent",
		fdBaseLbls, clamp(0.5*bf, 100))
	c.st.Set("azure_microsoft_cdn_profiles_percentage5xx_average_percent",
		fdBaseLbls, clamp(0.2*bf, 100))
	c.st.Set("azure_microsoft_cdn_profiles_requestsize_total_bytes",
		fdBaseLbls, rnd(50_000_000*bf))
	c.st.Set("azure_microsoft_cdn_profiles_responsesize_total_bytes",
		fdBaseLbls, rnd(200_000_000*bf))
	c.st.Set("azure_microsoft_cdn_profiles_totallatency_average_milliseconds",
		fdBaseLbls, rnd(80*bf))
	c.st.Set("azure_microsoft_cdn_profiles_originhealthpercentage_average_percent",
		fdBaseLbls, clamp(100-10*bf, 100))
	c.st.Set("azure_microsoft_cdn_profiles_originlatency_average_milliseconds",
		fdBaseLbls, rnd(50*bf))
	c.st.Set("azure_microsoft_cdn_profiles_originrequestcount_total_count",
		fdBaseLbls, rnd(8_000*bf))

	// requestcount with three dimension labels (extract §C.3/§D.9).
	for _, endpoint := range []string{"ep-api", "ep-static"} {
		for _, country := range []string{"DE", "US"} {
			for _, statusGroup := range []string{"2xx", "4xx", "5xx"} {
				reqBase := 9000.0
				switch statusGroup {
				case "4xx":
					reqBase = 50
				case "5xx":
					reqBase = 20
				}
				fdDimLbls := mergeLabels(fdBaseLbls, c.dim(map[string]string{
					"Endpoint":        endpoint,
					"ClientCountry":   country,
					"HttpStatusGroup": c.statusGroup(statusGroup),
				}))
				c.st.Set("azure_microsoft_cdn_profiles_requestcount_total_count",
					fdDimLbls, rnd(reqBase*bf))
			}
		}
	}

	// ── Virtual Networks (extract §D.12 — NO aggregation suffix) ─────────────
	vnetName := "vnet-" + subSuffix + "-01"
	// VNet metrics carry `region` label (extract §1.5 VNet table).
	vnetBaseLbls := mergeLabels(
		c.baseLabelsFor(sub, rg, "microsoft.network/virtualnetworks", vnetName),
		map[string]string{"region": sub.region},
	)

	// ⚠ TRAP: NO _average_ or _total_ infix — ends _count directly (extract §D.12).
	// Seed/anchor metric.
	c.st.Set("azure_microsoft_network_virtualnetworks_subnets_count", vnetBaseLbls, 3)
	c.st.Set("azure_microsoft_network_virtualnetworks_availableaddresses_count",
		vnetBaseLbls, rnd(200*bf))
	c.st.Set("azure_microsoft_network_virtualnetworks_connectedpeerings_count", vnetBaseLbls, 2)
	c.st.Set("azure_microsoft_network_virtualnetworks_peerings_count", vnetBaseLbls, 2)

	// Per-subnet metrics carry subnet_name (extract §C.3/§D.12).
	for _, subnetName := range []string{"snet-app", "snet-db"} {
		subnetLbls := mergeLabels(vnetBaseLbls, map[string]string{"subnet_name": subnetName})
		c.st.Set("azure_microsoft_network_virtualnetworks_availablesubnetaddresses_count",
			subnetLbls, rnd(50*bf))
		c.st.Set("azure_microsoft_network_virtualnetworks_assignedsubnetaddresses_count",
			subnetLbls, rnd(30*bf))
	}
}
