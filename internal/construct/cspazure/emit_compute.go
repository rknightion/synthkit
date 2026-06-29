// SPDX-License-Identifier: AGPL-3.0-only

// emit_compute.go — azure_microsoft_compute_virtualmachines_* (extract §1.5 compute)
package cspazure

import (
	"time"

	"github.com/rknightion/synthkit/internal/core"
)

// emitCompute emits the compute sub-signal (Virtual Machines) for one subscription.
// ALL metrics use st.Set — window-gauge invariant (extract §1.3).
func (c *construct) emitCompute(_ time.Time, w *core.World, sub azureSub, bf float64) {
	rg := sub.resourceGroups[1] // rg-compute
	vmNames := []string{"vm-app-01", "vm-app-02", "vm-app-03"}

	for _, vmName := range vmNames {
		lbls := c.baseLabelsFor(sub, rg, "microsoft.compute/virtualmachines", vmName)
		n := w.Shape.Noise(0.12)

		// availability: 0=unavail, 1=avail (seed/anchor metric)
		c.st.Set("azure_microsoft_compute_virtualmachines_vmavailabilitymetric_average_count", lbls, 1)
		c.st.Set("azure_microsoft_compute_virtualmachines_percentage_cpu_average_percent",
			lbls, clamp(20*bf*n, 100))
		c.st.Set("azure_microsoft_compute_virtualmachines_available_memory_bytes_average_bytes",
			lbls, rnd(4*1024*1024*1024*(1-0.3*bf)))
		c.st.Set("azure_microsoft_compute_virtualmachines_cpu_credits_consumed_average_count",
			lbls, rnd(50*bf))
		c.st.Set("azure_microsoft_compute_virtualmachines_cpu_credits_remaining_average_count",
			lbls, rnd(200*(1-0.3*bf)))
		c.st.Set("azure_microsoft_compute_virtualmachines_disk_read_bytes_total_bytes",
			lbls, rnd(100_000_000*bf))
		c.st.Set("azure_microsoft_compute_virtualmachines_disk_write_bytes_total_bytes",
			lbls, rnd(50_000_000*bf))
		c.st.Set("azure_microsoft_compute_virtualmachines_disk_read_operations_sec_average_countpersecond",
			lbls, rnd(200*bf))
		c.st.Set("azure_microsoft_compute_virtualmachines_disk_write_operations_sec_average_countpersecond",
			lbls, rnd(100*bf))
		c.st.Set("azure_microsoft_compute_virtualmachines_inbound_flows_average_count",
			lbls, rnd(500*bf))
		c.st.Set("azure_microsoft_compute_virtualmachines_outbound_flows_average_count",
			lbls, rnd(300*bf))
		c.st.Set("azure_microsoft_compute_virtualmachines_network_in_total_total_bytes",
			lbls, rnd(50_000_000*bf))
		c.st.Set("azure_microsoft_compute_virtualmachines_network_out_total_total_bytes",
			lbls, rnd(20_000_000*bf))
	}
}
