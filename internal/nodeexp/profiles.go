// SPDX-License-Identifier: AGPL-3.0-only

package nodeexp

// profiles.go — Appendix A allowlists + keepSet / keepSetCadvisor functions.
//
// Sources:
//   - linuxIntegrationNames:  GC integration Alloy config, prometheus.relabel
//     "integrations_node_exporter" keep rule (plan Appendix A).
//   - windowsIntegrationNames: GC integration Alloy config, prometheus.relabel
//     "integrations_windows_exporter" keep rule (plan Appendix A).
//   - macosIntegrationNames:  GC integration Alloy config, macOS prometheus.relabel
//     keep rule (plan Appendix A).
//   - dockerCadvisorNames:    GC integration Alloy config, prometheus.relabel
//     "integrations_cadvisor" keep rule (plan Appendix A).
//   - k8sNodeNames:           Exact metric names emitted by
//     internal/construct/k8scluster/nodeexporter.go (grepped st.Add/st.Set literals,
//     2026-06-17, before Phase-6 migration).
//   - k8sWindowsNames:        Exact metric names emitted by
//     internal/construct/k8scluster/windowsexporter.go (same grep, same date).
//   - k8sCadvisorNames:       Exact metric names emitted by
//     internal/construct/k8scluster/cadvisor.go (same grep, same date).

// linuxIntegrationNames is the Appendix-A linux node_exporter integration allowlist,
// split from the |‑delimited keep-regex string.
// Source: GC integration Alloy config, prometheus.relabel "integrations_node_exporter" keep rule.
var linuxIntegrationNames = []string{
	"up",
	"node_arp_entries",
	"node_boot_time_seconds",
	"node_context_switches_total",
	"node_cpu_seconds_total",
	"node_disk_io_time_seconds_total",
	"node_disk_io_time_weighted_seconds_total",
	"node_disk_read_bytes_total",
	"node_disk_read_time_seconds_total",
	"node_disk_reads_completed_total",
	"node_disk_write_time_seconds_total",
	"node_disk_writes_completed_total",
	"node_disk_written_bytes_total",
	"node_filefd_allocated",
	"node_filefd_maximum",
	"node_filesystem_avail_bytes",
	"node_filesystem_device_error",
	"node_filesystem_files",
	"node_filesystem_files_free",
	"node_filesystem_readonly",
	"node_filesystem_size_bytes",
	"node_intr_total",
	"node_load1",
	"node_load15",
	"node_load5",
	"node_md_disks",
	"node_md_disks_required",
	"node_memory_Active_anon_bytes",
	"node_memory_Active_bytes",
	"node_memory_Active_file_bytes",
	"node_memory_AnonHugePages_bytes",
	"node_memory_AnonPages_bytes",
	"node_memory_Bounce_bytes",
	"node_memory_Buffers_bytes",
	"node_memory_Cached_bytes",
	"node_memory_CommitLimit_bytes",
	"node_memory_Committed_AS_bytes",
	"node_memory_DirectMap1G_bytes",
	"node_memory_DirectMap2M_bytes",
	"node_memory_DirectMap4k_bytes",
	"node_memory_Dirty_bytes",
	"node_memory_HugePages_Free",
	"node_memory_HugePages_Rsvd",
	"node_memory_HugePages_Surp",
	"node_memory_HugePages_Total",
	"node_memory_Hugepagesize_bytes",
	"node_memory_Inactive_anon_bytes",
	"node_memory_Inactive_bytes",
	"node_memory_Inactive_file_bytes",
	"node_memory_Mapped_bytes",
	"node_memory_MemAvailable_bytes",
	"node_memory_MemFree_bytes",
	"node_memory_MemTotal_bytes",
	"node_memory_SReclaimable_bytes",
	"node_memory_SUnreclaim_bytes",
	"node_memory_ShmemHugePages_bytes",
	"node_memory_ShmemPmdMapped_bytes",
	"node_memory_Shmem_bytes",
	"node_memory_Slab_bytes",
	"node_memory_SwapTotal_bytes",
	"node_memory_VmallocChunk_bytes",
	"node_memory_VmallocTotal_bytes",
	"node_memory_VmallocUsed_bytes",
	"node_memory_WritebackTmp_bytes",
	"node_memory_Writeback_bytes",
	"node_netstat_Icmp6_InErrors",
	"node_netstat_Icmp6_InMsgs",
	"node_netstat_Icmp6_OutMsgs",
	"node_netstat_Icmp_InErrors",
	"node_netstat_Icmp_InMsgs",
	"node_netstat_Icmp_OutMsgs",
	"node_netstat_IpExt_InOctets",
	"node_netstat_IpExt_OutOctets",
	"node_netstat_TcpExt_ListenDrops",
	"node_netstat_TcpExt_ListenOverflows",
	"node_netstat_TcpExt_TCPSynRetrans",
	"node_netstat_Tcp_InErrs",
	"node_netstat_Tcp_InSegs",
	"node_netstat_Tcp_OutRsts",
	"node_netstat_Tcp_OutSegs",
	"node_netstat_Tcp_RetransSegs",
	"node_netstat_Udp6_InDatagrams",
	"node_netstat_Udp6_InErrors",
	"node_netstat_Udp6_NoPorts",
	"node_netstat_Udp6_OutDatagrams",
	"node_netstat_Udp6_RcvbufErrors",
	"node_netstat_Udp6_SndbufErrors",
	"node_netstat_UdpLite_InErrors",
	"node_netstat_Udp_InDatagrams",
	"node_netstat_Udp_InErrors",
	"node_netstat_Udp_NoPorts",
	"node_netstat_Udp_OutDatagrams",
	"node_netstat_Udp_RcvbufErrors",
	"node_netstat_Udp_SndbufErrors",
	"node_network_carrier",
	"node_network_info",
	"node_network_mtu_bytes",
	"node_network_receive_bytes_total",
	"node_network_receive_compressed_total",
	"node_network_receive_drop_total",
	"node_network_receive_errs_total",
	"node_network_receive_fifo_total",
	"node_network_receive_multicast_total",
	"node_network_receive_packets_total",
	"node_network_speed_bytes",
	"node_network_transmit_bytes_total",
	"node_network_transmit_compressed_total",
	"node_network_transmit_drop_total",
	"node_network_transmit_errs_total",
	"node_network_transmit_fifo_total",
	"node_network_transmit_multicast_total",
	"node_network_transmit_packets_total",
	"node_network_transmit_queue_length",
	"node_network_up",
	"node_nf_conntrack_entries",
	"node_nf_conntrack_entries_limit",
	"node_os_info",
	"node_sockstat_FRAG6_inuse",
	"node_sockstat_FRAG_inuse",
	"node_sockstat_RAW6_inuse",
	"node_sockstat_RAW_inuse",
	"node_sockstat_TCP6_inuse",
	"node_sockstat_TCP_alloc",
	"node_sockstat_TCP_inuse",
	"node_sockstat_TCP_mem",
	"node_sockstat_TCP_mem_bytes",
	"node_sockstat_TCP_orphan",
	"node_sockstat_TCP_tw",
	"node_sockstat_UDP6_inuse",
	"node_sockstat_UDPLITE6_inuse",
	"node_sockstat_UDPLITE_inuse",
	"node_sockstat_UDP_inuse",
	"node_sockstat_UDP_mem",
	"node_sockstat_UDP_mem_bytes",
	"node_sockstat_sockets_used",
	"node_softnet_dropped_total",
	"node_softnet_processed_total",
	"node_softnet_times_squeezed_total",
	"node_systemd_unit_state",
	"node_textfile_scrape_error",
	"node_time_zone_offset_seconds",
	"node_timex_estimated_error_seconds",
	"node_timex_maxerror_seconds",
	"node_timex_offset_seconds",
	"node_timex_sync_status",
	"node_uname_info",
	"node_vmstat_oom_kill",
	"node_vmstat_pgfault",
	"node_vmstat_pgmajfault",
	"node_vmstat_pgpgin",
	"node_vmstat_pgpgout",
	"node_vmstat_pswpin",
	"node_vmstat_pswpout",
	"process_max_fds",
	"process_open_fds",
}

// windowsIntegrationNames is the windows_exporter integration keepset, RECONCILED against
// the real a homelab reference WINSRV capture (host-capture.md, 2026-06-17). Only names the
// capture CONFIRMS present on the real Windows Server 2025 host are kept.
//
// Capture-driven corrections (host-capture.md "What is NOT present (WINSRV)"):
//   - windows_service_status REMOVED → it is a PHANTOM. The real metric is
//     windows_service_state{name,state}. Added windows_service_state.
//   - windows_cs_logical_processors / windows_cs_physical_memory_bytes REMOVED — the `cs`
//     collector is absent on the real host (windows_cs_* does NOT exist; system info lives
//     in windows_os_info + windows_system_*).
//   - windows_disk_drive_status REMOVED → the real name is windows_diskdrive_status{name,status}
//     (no underscore split). Added windows_diskdrive_status.
//   - windows_os_paging_limit_bytes / windows_os_physical_memory_free_bytes / windows_os_timezone
//     REMOVED — NOT in the capture's OS section (only windows_os_hostname + windows_os_info).
//   - windows_system_system_up_time REMOVED — NOT in the capture's System section.
//   - windows_system_boot_time_timestamp_seconds REMOVED — capture has only the (non _seconds)
//     windows_system_boot_time_timestamp.
//   - windows_cpu_interrupts_total kept (capture CPU section confirms it).
//   - windows_pagefile_free_bytes / windows_pagefile_limit_bytes confirmed (label `file`).
//   - windows_time_* trimmed to the four the capture's Time section confirms.
var windowsIntegrationNames = []string{
	"up",
	"windows_cpu_interrupts_total",
	"windows_cpu_logical_processor",
	"windows_cpu_time_total",
	"windows_diskdrive_status",
	"windows_logical_disk_avg_read_requests_queued",
	"windows_logical_disk_avg_write_requests_queued",
	"windows_logical_disk_free_bytes",
	"windows_logical_disk_idle_seconds_total",
	"windows_logical_disk_read_bytes_total",
	"windows_logical_disk_read_seconds_total",
	"windows_logical_disk_reads_total",
	"windows_logical_disk_size_bytes",
	"windows_logical_disk_write_bytes_total",
	"windows_logical_disk_write_seconds_total",
	"windows_logical_disk_writes_total",
	"windows_memory_physical_free_bytes",
	"windows_memory_physical_total_bytes",
	"windows_net_bytes_received_total",
	"windows_net_bytes_sent_total",
	"windows_net_packets_outbound_discarded_total",
	"windows_net_packets_outbound_errors_total",
	"windows_net_packets_received_discarded_total",
	"windows_net_packets_received_errors_total",
	"windows_net_packets_received_unknown_total",
	"windows_os_info",
	"windows_pagefile_free_bytes",
	"windows_pagefile_limit_bytes",
	"windows_service_state",
	"windows_system_boot_time_timestamp",
	"windows_system_context_switches_total",
	"windows_system_processor_queue_length",
	"windows_time_computed_time_offset_seconds",
	"windows_time_ntp_client_time_sources",
	"windows_time_ntp_round_trip_delay_seconds",
	"windows_time_timezone",
}

// macosIntegrationNames is the Appendix-A macOS node_exporter integration allowlist,
// split from the |‑delimited keep-regex string.
// Source: GC integration Alloy config, macOS prometheus.relabel keep rule (plan Appendix A).
var macosIntegrationNames = []string{
	"up",
	"node_boot_time_seconds",
	"node_cpu_seconds_total",
	"node_disk_io_time_seconds_total",
	"node_disk_read_bytes_total",
	"node_disk_written_bytes_total",
	"node_filesystem_avail_bytes",
	"node_filesystem_files",
	"node_filesystem_files_free",
	"node_filesystem_readonly",
	"node_filesystem_size_bytes",
	"node_load1",
	"node_load15",
	"node_load5",
	"node_memory_compressed_bytes",
	"node_memory_internal_bytes",
	"node_memory_purgeable_bytes",
	"node_memory_swap_total_bytes",
	"node_memory_swap_used_bytes",
	"node_memory_total_bytes",
	"node_memory_wired_bytes",
	"node_network_receive_bytes_total",
	"node_network_receive_drop_total",
	"node_network_receive_errs_total",
	"node_network_receive_packets_total",
	"node_network_transmit_bytes_total",
	"node_network_transmit_drop_total",
	"node_network_transmit_errs_total",
	"node_network_transmit_packets_total",
	"node_os_info",
	"node_textfile_scrape_error",
	"node_uname_info",
}

// dockerCadvisorNames is the Appendix-A Docker cadvisor integration allowlist,
// split from the |‑delimited keep-regex string.
// Source: GC integration Alloy config, prometheus.relabel "integrations_cadvisor" keep rule.
var dockerCadvisorNames = []string{
	"container_cpu_usage_seconds_total",
	"container_fs_reads_total",
	"container_fs_usage_bytes",
	"container_fs_writes_total",
	"container_last_seen",
	"container_memory_usage_bytes",
	"container_network_receive_bytes_total",
	"container_network_receive_errors_total",
	"container_network_receive_packets_dropped_total",
	"container_network_transmit_bytes_total",
	"container_network_transmit_errors_total",
	"container_network_transmit_packets_dropped_total",
	"container_spec_memory_reservation_limit_bytes",
	"machine_memory_bytes",
	"machine_scrape_error",
	"up",
}

// k8sNodeNames is the exact set of metric names emitted by the pre-migration
// internal/construct/k8scluster/nodeexporter.go. RECONCILED 2026-06-18 against the
// full EmitLinux emission (Phase-6 migration): the original grep of st.Add/st.Set
// LITERALS missed the range-loop families (node_memory_* MemInfo gauges,
// node_netstat_*, node_sockstat_*, node_vmstat_*, plus node_load1/5/15 and the core
// node_memory_* gauges), which the source emitted via slice iteration. The set is now
// the source's complete emission MINUS the integration/full-only extras the source
// never wrote (up, go_*, promhttp_*, node_md_disks[_required], node_systemd_unit_state).
// This is the authoritative ProfileK8s keepset for the node/linux lane; the migration
// -dump diff against blueprints/k8s-minimal.yaml is the exhaustive gate.
var k8sNodeNames = []string{
	"node_arp_entries",
	"node_boot_time_seconds",
	"node_context_switches_total",
	"node_cpu_guest_seconds_total",
	"node_cpu_online",
	"node_cpu_seconds_total",
	"node_disk_io_time_seconds_total",
	"node_disk_io_time_weighted_seconds_total",
	"node_disk_read_bytes_total",
	"node_disk_read_time_seconds_total",
	"node_disk_reads_completed_total",
	"node_disk_write_time_seconds_total",
	"node_disk_writes_completed_total",
	"node_disk_written_bytes_total",
	"node_exporter_build_info",
	"node_filefd_allocated",
	"node_filefd_maximum",
	"node_filesystem_avail_bytes",
	"node_filesystem_device_error",
	"node_filesystem_files",
	"node_filesystem_files_free",
	"node_filesystem_free_bytes",
	"node_filesystem_mount_info",
	"node_filesystem_purgeable_bytes",
	"node_filesystem_readonly",
	"node_filesystem_size_bytes",
	"node_intr_total",
	"node_load1",
	"node_load15",
	"node_load5",
	"node_memory_Active_anon_bytes",
	"node_memory_Active_bytes",
	"node_memory_Active_file_bytes",
	"node_memory_AnonHugePages_bytes",
	"node_memory_AnonPages_bytes",
	"node_memory_Bounce_bytes",
	"node_memory_Buffers_bytes",
	"node_memory_Cached_bytes",
	"node_memory_CmaFree_bytes",
	"node_memory_CmaTotal_bytes",
	"node_memory_CommitLimit_bytes",
	"node_memory_Committed_AS_bytes",
	"node_memory_Dirty_bytes",
	"node_memory_HardwareCorrupted_bytes",
	"node_memory_HugePages_Free",
	"node_memory_HugePages_Rsvd",
	"node_memory_HugePages_Surp",
	"node_memory_HugePages_Total",
	"node_memory_Hugepagesize_bytes",
	"node_memory_Inactive_anon_bytes",
	"node_memory_Inactive_bytes",
	"node_memory_Inactive_file_bytes",
	"node_memory_KernelStack_bytes",
	"node_memory_Mapped_bytes",
	"node_memory_MemAvailable_bytes",
	"node_memory_MemFree_bytes",
	"node_memory_MemTotal_bytes",
	"node_memory_Mlocked_bytes",
	"node_memory_NFS_Unstable_bytes",
	"node_memory_PageTables_bytes",
	"node_memory_Percpu_bytes",
	"node_memory_Shmem_bytes",
	"node_memory_ShmemHugePages_bytes",
	"node_memory_ShmemPmdMapped_bytes",
	"node_memory_Slab_bytes",
	"node_memory_SReclaimable_bytes",
	"node_memory_SUnreclaim_bytes",
	"node_memory_SwapCached_bytes",
	"node_memory_SwapFree_bytes",
	"node_memory_SwapTotal_bytes",
	"node_memory_Unevictable_bytes",
	"node_memory_VmallocChunk_bytes",
	"node_memory_VmallocTotal_bytes",
	"node_memory_VmallocUsed_bytes",
	"node_memory_Writeback_bytes",
	"node_memory_WritebackTmp_bytes",
	"node_memory_Zswap_bytes",
	"node_memory_Zswapped_bytes",
	"node_netstat_Icmp_InErrors",
	"node_netstat_Icmp_InMsgs",
	"node_netstat_Icmp_OutMsgs",
	"node_netstat_Icmp6_InErrors",
	"node_netstat_Icmp6_InMsgs",
	"node_netstat_Icmp6_OutMsgs",
	"node_netstat_IpExt_InOctets",
	"node_netstat_IpExt_OutOctets",
	"node_netstat_Tcp_InErrs",
	"node_netstat_Tcp_InSegs",
	"node_netstat_Tcp_OutRsts",
	"node_netstat_Tcp_OutSegs",
	"node_netstat_Tcp_RetransSegs",
	"node_netstat_TcpExt_ListenDrops",
	"node_netstat_TcpExt_ListenOverflows",
	"node_netstat_TcpExt_TCPSynRetrans",
	"node_netstat_Udp_InDatagrams",
	"node_netstat_Udp_InErrors",
	"node_netstat_Udp_NoPorts",
	"node_netstat_Udp_OutDatagrams",
	"node_netstat_Udp_RcvbufErrors",
	"node_netstat_Udp_SndbufErrors",
	"node_netstat_Udp6_InDatagrams",
	"node_netstat_Udp6_InErrors",
	"node_netstat_Udp6_NoPorts",
	"node_netstat_Udp6_OutDatagrams",
	"node_netstat_Udp6_RcvbufErrors",
	"node_netstat_Udp6_SndbufErrors",
	"node_netstat_UdpLite_InErrors",
	"node_network_carrier",
	"node_network_info",
	"node_network_mtu_bytes",
	"node_network_receive_bytes_total",
	"node_network_receive_compressed_total",
	"node_network_receive_drop_total",
	"node_network_receive_errs_total",
	"node_network_receive_fifo_total",
	"node_network_receive_multicast_total",
	"node_network_receive_packets_total",
	"node_network_speed_bytes",
	"node_network_transmit_bytes_total",
	"node_network_transmit_compressed_total",
	"node_network_transmit_drop_total",
	"node_network_transmit_errs_total",
	"node_network_transmit_fifo_total",
	"node_network_transmit_packets_total",
	"node_network_transmit_queue_length",
	"node_network_up",
	"node_nf_conntrack_entries",
	"node_nf_conntrack_entries_limit",
	"node_os_info",
	"node_procs_running",
	"node_sockstat_FRAG_inuse",
	"node_sockstat_FRAG6_inuse",
	"node_sockstat_RAW_inuse",
	"node_sockstat_RAW6_inuse",
	"node_sockstat_sockets_used",
	"node_sockstat_TCP_alloc",
	"node_sockstat_TCP_inuse",
	"node_sockstat_TCP_mem",
	"node_sockstat_TCP_mem_bytes",
	"node_sockstat_TCP_orphan",
	"node_sockstat_TCP_tw",
	"node_sockstat_TCP6_inuse",
	"node_sockstat_UDP_inuse",
	"node_sockstat_UDP_mem",
	"node_sockstat_UDP_mem_bytes",
	"node_sockstat_UDP6_inuse",
	"node_sockstat_UDPLITE_inuse",
	"node_sockstat_UDPLITE6_inuse",
	"node_softnet_dropped_total",
	"node_softnet_processed_total",
	"node_softnet_times_squeezed_total",
	"node_textfile_scrape_error",
	"node_time_zone_offset_seconds",
	"node_timex_estimated_error_seconds",
	"node_timex_maxerror_seconds",
	"node_timex_offset_seconds",
	"node_timex_sync_status",
	"node_uname_info",
	"node_vmstat_oom_kill",
	"node_vmstat_pgfault",
	"node_vmstat_pgmajfault",
	"node_vmstat_pgpgin",
	"node_vmstat_pgpgout",
	"node_vmstat_pswpin",
	"node_vmstat_pswpout",
	"process_cpu_seconds_total",
	"process_max_fds",
	"process_open_fds",
	"process_resident_memory_bytes",
}

// k8sWindowsNames is the exact set of metric names emitted by
// internal/construct/k8scluster/windowsexporter.go, enumerated by grepping
// st.Add/st.Set literals on 2026-06-17 (before Phase-6 migration).
// This is the authoritative ProfileK8s keepset for the windows lane.
var k8sWindowsNames = []string{
	// counters (st.Add)
	"windows_cpu_time_total",
	"windows_logical_disk_read_bytes_total",
	"windows_logical_disk_write_bytes_total",
	"windows_net_bytes_received_total",
	"windows_net_bytes_sent_total",
	"windows_net_packets_received_total",
	// gauges (st.Set)
	"windows_cpu_logical_processor",
	"windows_logical_disk_free_bytes",
	"windows_logical_disk_size_bytes",
	"windows_memory_available_bytes",
	"windows_memory_committed_bytes",
	"windows_memory_physical_free_bytes",
	"windows_memory_physical_total_bytes",
	"windows_os_hostname",
	"windows_os_info",
}

// k8sCadvisorNames is the exact set of metric names emitted by
// internal/construct/k8scluster/cadvisor.go, enumerated by grepping
// st.Add/st.Set literals on 2026-06-17 (before Phase-6 migration).
// This is the authoritative CadvisorK8s keepset.
var k8sCadvisorNames = []string{
	// counters (st.Add)
	"container_cpu_cfs_periods_total",
	"container_cpu_cfs_throttled_periods_total",
	"container_cpu_usage_seconds_total",
	"container_fs_reads_bytes_total",
	"container_fs_reads_total",
	"container_fs_writes_bytes_total",
	"container_fs_writes_total",
	"container_network_receive_bytes_total",
	"container_network_receive_packets_dropped_total",
	"container_network_receive_packets_total",
	"container_network_transmit_bytes_total",
	"container_network_transmit_packets_dropped_total",
	"container_network_transmit_packets_total",
	// gauges (st.Set)
	"container_memory_cache",
	"container_memory_rss",
	"container_memory_swap",
	"container_memory_usage_bytes",
	"container_memory_working_set_bytes",
	"machine_memory_bytes",
}

// fullLinuxExtraNames is the delta above the linux integration set for ProfileFull.
// These are the standard node_exporter self-metrics present under include_exporter_metrics
// (the universal exporter-internal surface). They are CONFIRMED present on every real
// node_exporter instance in a homelab reference host capture (host-capture.md: "promhttp_*,
// scrape_*, go_*" + node_exporter_build_info).
//
// INTENTIONALLY MODEST: the real full delta on a homelab reference host is huge and host-hardware-specific
// (ZFS node_zfs_* ~250 series, ~600 node_ethtool_* series, node_mountstats_nfs_*, RAPL,
// hwmon sensors, NUMA/THP vmstat, etc. — see host-capture.md "node_exporter FULL DELTA").
// synthkit does NOT synthesize those hardware/driver families: they are non-portable across
// the declared fleet and would invent device topology we have no fixture for. The synthetic
// `full` profile therefore = integration ∪ this small universal self-metric set. The
// hardware-specific families are deliberately OUT OF SCOPE for synthetic full.
var fullLinuxExtraNames = []string{
	"process_cpu_seconds_total",
	"process_resident_memory_bytes",
	"node_exporter_build_info",
	"go_goroutines",
	"go_memstats_alloc_bytes",
	"promhttp_metric_handler_requests_total",
}

func makeSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

func mergeSet(a, b []string) map[string]bool {
	m := make(map[string]bool, len(a)+len(b))
	for _, n := range a {
		m[n] = true
	}
	for _, n := range b {
		m[n] = true
	}
	return m
}

// keepSet returns the set of node/windows/macos metric names to keep for the given Profile.
//
// ProfileIntegration returns the linux integration allowlist (Appendix A). Callers
// emitting macOS series must call macosKeepSet instead, which returns the macOS-specific
// memory subset (e.g. node_memory_total_bytes rather than node_memory_MemTotal_bytes).
//
// ProfileFull returns the linux integration set union a small capture-confirmed delta
// (standard node_exporter self-metrics). When host-capture.md is populated (Phase 5),
// the delta will be expanded to the full homelab reference broad-Alloy surface.
//
// ProfileK8s returns the exact name set emitted by k8scluster/nodeexporter.go today
// (grepped 2026-06-17). Callers emitting k8s Windows series must call keepSetWindows.
func keepSet(prof Profile) map[string]bool {
	switch prof {
	case ProfileIntegration:
		return makeSet(linuxIntegrationNames)
	case ProfileFull:
		// integration ∪ capture-confirmed delta.
		// TODO(phase5): expand delta from host-capture.md.
		return mergeSet(linuxIntegrationNames, fullLinuxExtraNames)
	case ProfileK8s:
		return makeSet(k8sNodeNames)
	default:
		return makeSet(linuxIntegrationNames)
	}
}

// macosKeepSet returns the set of metric names to keep for a macOS emitter under the
// given Profile. The macOS integration allowlist differs from linux in the memory
// family (node_memory_total_bytes vs node_memory_MemTotal_bytes, etc.), so it is
// a separate function rather than a branch inside keepSet.
//
// MacOS hosts never use ProfileK8s (no k8s macOS nodes in scope); that case falls
// back to the macOS integration set.
func macosKeepSet(prof Profile) map[string]bool {
	switch prof {
	case ProfileIntegration, ProfileFull, ProfileK8s:
		// macOS full == integration for now (no capture delta yet).
		return makeSet(macosIntegrationNames)
	default:
		return makeSet(macosIntegrationNames)
	}
}

// keepSetWindows returns the metric name keepset for the windows lane.
//
// ProfileK8s returns the exact names from k8scluster/windowsexporter.go (grepped 2026-06-17).
// ProfileIntegration / ProfileFull return the Appendix-A windows integration allowlist.
func keepSetWindows(prof Profile) map[string]bool {
	switch prof {
	case ProfileK8s:
		return makeSet(k8sWindowsNames)
	default:
		return makeSet(windowsIntegrationNames)
	}
}

// keepSetCadvisor returns the set of cadvisor metric names to keep for the given CadvisorProfile.
//
// CadvisorDocker returns the Appendix-A docker integration allowlist.
// CadvisorK8s returns the exact name set emitted by k8scluster/cadvisor.go today
// (grepped 2026-06-17).
func keepSetCadvisor(prof CadvisorProfile) map[string]bool {
	switch prof {
	case CadvisorDocker:
		return makeSet(dockerCadvisorNames)
	case CadvisorK8s:
		return makeSet(k8sCadvisorNames)
	default:
		return makeSet(dockerCadvisorNames)
	}
}
