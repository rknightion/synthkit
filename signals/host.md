# Host Exporters (node_exporter / windows_exporter / macos-node + Docker cAdvisor) — ScopeSubstrate

Traditional (non-Kubernetes) machine telemetry, as shipped by **Grafana Alloy integrations**:
the Linux `node_exporter`, the `windows_exporter`, the macOS `node_exporter` build, and the
Docker `cAdvisor` lane. One `host` construct (`internal/construct/host`) is instantiated per
blueprint `hosts:` entry; the metric vocabulary + physics are delegated to the shared
`internal/nodeexp` mechanic lib — the SAME lib the k8s `node-exporter`/`cAdvisor`/`windows-exporter`
families use (see `signals/k8s.md`). The standalone fleet and the k8s profile differ ONLY in the
ambient label envelope and the keep-set: a standalone host carries the substrate identity pair
`(job, instance)` with no pod/namespace/cluster ambient labels.

**ScopeSubstrate.** Every series is disambiguated by `(job, instance)`; `instance` is the
hostname and MUST be unique across blueprints (load-time collision gate). No `blueprint` and no
`cluster` label is ever stamped (see [`00-canon.md`](00-canon.md) `[slug: scoping]`,
`[slug: blueprint-label]`). Cardinality is bounded by estate size, not per-request volume
(`[slug: cardinality]`); an absent dimension is OMITTED, never `""`/`"NA"` (`[slug: shape-rules]`).

Each host declares an `os` (`linux` | `macos`/`darwin` | `windows`) which picks the exporter
lane, and a `metrics_profile` (`integration` | `full`). The profile is a per-OS keep-set filter
over the lib's vocabulary: **integration** = the cost-controlled Grafana Cloud integration Alloy
allowlist; **full** = a MODEST superset on Linux only (adds the universal exporter-self surface —
`process_*`, `go_*`, `node_exporter_build_info`, `promhttp_*`). Per-OS `job`:
`integrations/node_exporter` (Linux), `integrations/macos-node` (macOS),
`integrations/windows_exporter` (Windows, **underscore**), `integrations/docker` (cAdvisor lane,
gated by the host's `docker` switch). The Docker lane is emitted alongside the OS lane when the
host's `observability.docker` switch is set.

*Provenance: real host telemetry live-captured 2026-06-17 from a reference Grafana Cloud stack
(`docs/superpowers/host-capture.md` — 9 real hosts spanning Linux/FreeBSD, Windows Server 2025,
macOS, + Docker cAdvisor on several Linux hosts) plus the Grafana Cloud integration Alloy
`prometheus.relabel` keep allowlists; synthesized by `internal/construct/host` + `internal/nodeexp`.
Keep-sets: `internal/nodeexp/profiles.go` (`linuxIntegrationNames` / `windowsIntegrationNames` /
`macosIntegrationNames` / `fullLinuxExtraNames`).*

---

## Identity and scoping [slug: host-identity]

**ScopeSubstrate.** The disambiguation pair is `(job, instance)`. `instance` = the declared
hostname (e.g. `host-linux01`, `host-win01`, `host-mac01`); `job` is the per-OS integration job string. The
emitter owns its own `up{instance,job}=1` series per lane — the construct never mints `up`.
A second blueprint declaring the same `instance` is rejected at load as a collision; a different
`instance` produces a disjoint series set.

| OS / lane | `job` | emitter | keep-set |
|---|---|---|---|
| linux | `integrations/node_exporter` | `nodeexp.EmitLinux` | `linuxIntegrationNames` (+`fullLinuxExtraNames` on `full`) |
| macos / darwin | `integrations/macos-node` | `nodeexp.EmitMacOS` | `macosIntegrationNames` |
| windows | `integrations/windows_exporter` | `nodeexp.EmitWindows` | `windowsIntegrationNames` |
| docker (gated) | `integrations/docker` | `nodeexp.EmitContainer` + `EmitMachine` | (native cAdvisor names) |

> ⚠ The `windows` job is `integrations/windows_exporter` (**underscore**) — distinct from the
> k8s windows-exporter family which uses `integrations/windows-exporter` (**hyphen**, see
> `signals/k8s.md [slug: k8s-windows-exporter]`). Standalone-fleet vs k8s is a `job`-string +
> ambient-label difference over the SAME `internal/nodeexp` vocabulary.

> ⚠ The keep-set is an allowlist FILTER: the emitter `set/add`s a wider vocabulary than the
> keep-set permits; only names IN the keep-set reach the sink. Names the emitter computes but
> the integration keep-set drops (e.g. `windows_memory_available_bytes`,
> `windows_memory_committed_bytes`, `windows_os_hostname`, `windows_net_packets_received_total`)
> are NOT emitted. The blocks below document the EMITTED (post-filter) set.

---

## Linux node_exporter [slug: host-node-linux]

`job=integrations/node_exporter`. Base labels `{instance, job}`; per-dimension families add their
own keys (`cpu`/`mode`, `device`, `device`/`fstype`/`mountpoint`, info-series identity keys).
`node_cpu_seconds_total` carries `mode` ∈ {idle, iowait, irq, nice, softirq, steal, system, user}
(8 modes × `cpu` index). The `full` profile is a **modest** superset of `integration`: it ADDS
`process_cpu_seconds_total`, `process_resident_memory_bytes`, `process_open_fds`,
`process_max_fds`, `node_exporter_build_info`, `go_goroutines`, `go_memstats_alloc_bytes`,
`promhttp_metric_handler_requests_total`. Hardware-specific high-cardinality families
(`node_zfs_*`, `node_ethtool_*`, `node_mountstats_*`, `node_hwmon_*`, `node_rapl_*`,
NUMA/THP variants, FreeBSD `node_devstat_*`/`ixl_port_*`/`sfp_*`) are **intentionally OUT OF
SCOPE** for synthetic `full` (non-portable, no fixture topology, would invent device names).

```yaml signals
family: node_exporter_linux
scope: substrate
sink: promrw
labels:
  job: integrations/node_exporter
  instance: <hostname>
  cpu: <index>                 # node_cpu_seconds_total, node_softnet_* (per-CPU)
  mode: idle|iowait|irq|nice|softirq|steal|system|user   # node_cpu_seconds_total
  device: <dev>                # node_disk_*, node_network_*, node_arp_entries, node_filesystem_*
  fstype: <fstype>             # node_filesystem_*
  mountpoint: <path>           # node_filesystem_*
  name: <unit>                 # node_systemd_unit_state (also state, type)
  state: <state>               # node_systemd_unit_state
  type: <type>                 # node_systemd_unit_state
  time_zone: <tz>              # node_time_zone_offset_seconds
  code: <http-code>            # promhttp_metric_handler_requests_total (full only)
  # node_os_info: id,name,pretty_name,version,version_id
  # node_uname_info: domainname,machine,nodename,release,sysname,version
  # node_network_info: address,adminstate,broadcast,device,operstate
  # node_exporter_build_info (full): branch,goarch,goos,goversion,revision,tags,version
metrics:
  - {root: up, type: gauge, unit: bool, v: ok, note: "labels: instance,job; emitter-owned"}
  - {root: node_load1, type: gauge, unit: load, v: ok}
  - {root: node_load5, type: gauge, unit: load, v: ok}
  - {root: node_load15, type: gauge, unit: load, v: ok}
  - {root: node_cpu_seconds_total, type: counter, unit: seconds, v: ok, note: "labels +cpu,mode (8 modes)"}
  - {root: node_context_switches_total, type: counter, unit: count, v: ok}
  - {root: node_intr_total, type: counter, unit: count, v: ok}
  - {root: node_boot_time_seconds, type: gauge, unit: unix_timestamp, v: ok}
  - {root: node_softnet_dropped_total, type: counter, unit: count, v: ok, note: "+cpu"}
  - {root: node_softnet_processed_total, type: counter, unit: count, v: ok, note: "+cpu"}
  - {root: node_softnet_times_squeezed_total, type: counter, unit: count, v: ok, note: "+cpu"}
  - {root: node_memory_MemTotal_bytes, type: gauge, unit: bytes, v: ok, note: "MemInfo flat gauges, no extra labels"}
  - {root: node_memory_MemFree_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_MemAvailable_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Buffers_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Cached_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Active_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Active_anon_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Active_file_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Inactive_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Inactive_anon_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Inactive_file_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_AnonPages_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_AnonHugePages_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Mapped_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Shmem_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_ShmemHugePages_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_ShmemPmdMapped_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Slab_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_SReclaimable_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_SUnreclaim_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Dirty_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Writeback_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_WritebackTmp_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Bounce_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_CommitLimit_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Committed_AS_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_SwapTotal_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_VmallocTotal_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_VmallocUsed_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_VmallocChunk_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_HugePages_Total, type: gauge, unit: count, v: ok}
  - {root: node_memory_HugePages_Free, type: gauge, unit: count, v: ok}
  - {root: node_memory_HugePages_Rsvd, type: gauge, unit: count, v: ok}
  - {root: node_memory_HugePages_Surp, type: gauge, unit: count, v: ok}
  - {root: node_memory_Hugepagesize_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_vmstat_pgfault, type: counter, unit: count, v: ok}
  - {root: node_vmstat_pgmajfault, type: counter, unit: count, v: ok}
  - {root: node_vmstat_pgpgin, type: counter, unit: count, v: ok}
  - {root: node_vmstat_pgpgout, type: counter, unit: count, v: ok}
  - {root: node_vmstat_pswpin, type: counter, unit: count, v: ok}
  - {root: node_vmstat_pswpout, type: counter, unit: count, v: ok}
  - {root: node_vmstat_oom_kill, type: counter, unit: count, v: ok}
  - {root: node_disk_reads_completed_total, type: counter, unit: count, v: ok, note: "+device"}
  - {root: node_disk_writes_completed_total, type: counter, unit: count, v: ok, note: "+device"}
  - {root: node_disk_read_bytes_total, type: counter, unit: bytes, v: ok, note: "+device"}
  - {root: node_disk_written_bytes_total, type: counter, unit: bytes, v: ok, note: "+device"}
  - {root: node_disk_read_time_seconds_total, type: counter, unit: seconds, v: ok, note: "+device"}
  - {root: node_disk_write_time_seconds_total, type: counter, unit: seconds, v: ok, note: "+device"}
  - {root: node_disk_io_time_seconds_total, type: counter, unit: seconds, v: ok, note: "+device"}
  - {root: node_disk_io_time_weighted_seconds_total, type: counter, unit: seconds, v: ok, note: "+device"}
  - {root: node_filesystem_size_bytes, type: gauge, unit: bytes, v: ok, note: "+device,fstype,mountpoint"}
  - {root: node_filesystem_avail_bytes, type: gauge, unit: bytes, v: ok, note: "+device,fstype,mountpoint"}
  - {root: node_filesystem_files, type: gauge, unit: count, v: ok, note: "+device,fstype,mountpoint"}
  - {root: node_filesystem_files_free, type: gauge, unit: count, v: ok, note: "+device,fstype,mountpoint"}
  - {root: node_filesystem_readonly, type: gauge, unit: bool, v: ok, note: "+device,fstype,mountpoint"}
  - {root: node_filesystem_device_error, type: gauge, unit: bool, v: ok, note: "+device,fstype,mountpoint"}
  - {root: node_filefd_allocated, type: gauge, unit: count, v: ok}
  - {root: node_filefd_maximum, type: gauge, unit: count, v: ok}
  - {root: node_network_receive_bytes_total, type: counter, unit: bytes, v: ok, note: "+device"}
  - {root: node_network_transmit_bytes_total, type: counter, unit: bytes, v: ok, note: "+device"}
  - {root: node_network_receive_packets_total, type: counter, unit: count, v: ok, note: "+device"}
  - {root: node_network_transmit_packets_total, type: counter, unit: count, v: ok, note: "+device"}
  - {root: node_network_receive_errs_total, type: counter, unit: count, v: ok, note: "+device"}
  - {root: node_network_transmit_errs_total, type: counter, unit: count, v: ok, note: "+device"}
  - {root: node_network_receive_drop_total, type: counter, unit: count, v: ok, note: "+device"}
  - {root: node_network_transmit_drop_total, type: counter, unit: count, v: ok, note: "+device"}
  - {root: node_network_receive_fifo_total, type: counter, unit: count, v: ok, note: "+device"}
  - {root: node_network_transmit_fifo_total, type: counter, unit: count, v: ok, note: "+device"}
  - {root: node_network_receive_compressed_total, type: counter, unit: count, v: ok, note: "+device"}
  - {root: node_network_transmit_compressed_total, type: counter, unit: count, v: ok, note: "+device"}
  - {root: node_network_receive_multicast_total, type: counter, unit: count, v: ok, note: "+device"}
  - {root: node_network_carrier, type: gauge, unit: bool, v: ok, note: "+device"}
  - {root: node_network_up, type: gauge, unit: bool, v: ok, note: "+device"}
  - {root: node_network_mtu_bytes, type: gauge, unit: bytes, v: ok, note: "+device"}
  - {root: node_network_speed_bytes, type: gauge, unit: bytes, v: ok, note: "+device"}
  - {root: node_network_transmit_queue_length, type: gauge, unit: count, v: ok, note: "+device"}
  - {root: node_network_info, type: gauge, unit: info, v: ok, note: "value=1; labels +address,adminstate,broadcast,device,operstate"}
  - {root: node_netstat_Tcp_InSegs, type: counter, unit: count, v: ok}
  - {root: node_netstat_Tcp_OutSegs, type: counter, unit: count, v: ok}
  - {root: node_netstat_Tcp_InErrs, type: counter, unit: count, v: ok}
  - {root: node_netstat_Tcp_OutRsts, type: counter, unit: count, v: ok}
  - {root: node_netstat_Tcp_RetransSegs, type: counter, unit: count, v: ok}
  - {root: node_netstat_TcpExt_ListenDrops, type: counter, unit: count, v: ok}
  - {root: node_netstat_TcpExt_ListenOverflows, type: counter, unit: count, v: ok}
  - {root: node_netstat_TcpExt_TCPSynRetrans, type: counter, unit: count, v: ok}
  - {root: node_netstat_Udp_InDatagrams, type: counter, unit: count, v: ok}
  - {root: node_netstat_Udp_OutDatagrams, type: counter, unit: count, v: ok}
  - {root: node_netstat_Udp_InErrors, type: counter, unit: count, v: ok}
  - {root: node_netstat_Udp_NoPorts, type: counter, unit: count, v: ok}
  - {root: node_netstat_Udp_RcvbufErrors, type: counter, unit: count, v: ok}
  - {root: node_netstat_Udp_SndbufErrors, type: counter, unit: count, v: ok}
  - {root: node_netstat_Udp6_InDatagrams, type: counter, unit: count, v: ok}
  - {root: node_netstat_Udp6_OutDatagrams, type: counter, unit: count, v: ok}
  - {root: node_netstat_Udp6_InErrors, type: counter, unit: count, v: ok}
  - {root: node_netstat_Udp6_NoPorts, type: counter, unit: count, v: ok}
  - {root: node_netstat_Udp6_RcvbufErrors, type: counter, unit: count, v: ok}
  - {root: node_netstat_Udp6_SndbufErrors, type: counter, unit: count, v: ok}
  - {root: node_netstat_UdpLite_InErrors, type: counter, unit: count, v: ok}
  - {root: node_netstat_Icmp_InMsgs, type: counter, unit: count, v: ok}
  - {root: node_netstat_Icmp_OutMsgs, type: counter, unit: count, v: ok}
  - {root: node_netstat_Icmp_InErrors, type: counter, unit: count, v: ok}
  - {root: node_netstat_Icmp6_InMsgs, type: counter, unit: count, v: ok}
  - {root: node_netstat_Icmp6_OutMsgs, type: counter, unit: count, v: ok}
  - {root: node_netstat_Icmp6_InErrors, type: counter, unit: count, v: ok}
  - {root: node_netstat_IpExt_InOctets, type: counter, unit: bytes, v: ok}
  - {root: node_netstat_IpExt_OutOctets, type: counter, unit: bytes, v: ok}
  - {root: node_sockstat_sockets_used, type: gauge, unit: count, v: ok}
  - {root: node_sockstat_TCP_inuse, type: gauge, unit: count, v: ok}
  - {root: node_sockstat_TCP_alloc, type: gauge, unit: count, v: ok}
  - {root: node_sockstat_TCP_orphan, type: gauge, unit: count, v: ok}
  - {root: node_sockstat_TCP_tw, type: gauge, unit: count, v: ok}
  - {root: node_sockstat_TCP_mem, type: gauge, unit: count, v: ok}
  - {root: node_sockstat_TCP_mem_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_sockstat_TCP6_inuse, type: gauge, unit: count, v: ok}
  - {root: node_sockstat_UDP_inuse, type: gauge, unit: count, v: ok}
  - {root: node_sockstat_UDP_mem, type: gauge, unit: count, v: ok}
  - {root: node_sockstat_UDP_mem_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_sockstat_UDP6_inuse, type: gauge, unit: count, v: ok}
  - {root: node_sockstat_UDPLITE_inuse, type: gauge, unit: count, v: ok}
  - {root: node_sockstat_UDPLITE6_inuse, type: gauge, unit: count, v: ok}
  - {root: node_sockstat_RAW_inuse, type: gauge, unit: count, v: ok}
  - {root: node_sockstat_RAW6_inuse, type: gauge, unit: count, v: ok}
  - {root: node_sockstat_FRAG_inuse, type: gauge, unit: count, v: ok}
  - {root: node_sockstat_FRAG6_inuse, type: gauge, unit: count, v: ok}
  - {root: node_nf_conntrack_entries, type: gauge, unit: count, v: ok}
  - {root: node_nf_conntrack_entries_limit, type: gauge, unit: count, v: ok}
  - {root: node_arp_entries, type: gauge, unit: count, v: ok, note: "+device"}
  - {root: node_md_disks, type: gauge, unit: count, v: ok}
  - {root: node_md_disks_required, type: gauge, unit: count, v: ok}
  - {root: node_systemd_unit_state, type: gauge, unit: bool, v: ok, note: "value=1 on the active state; labels +name,state,type"}
  - {root: node_time_zone_offset_seconds, type: gauge, unit: seconds, v: ok, note: "+time_zone"}
  - {root: node_timex_offset_seconds, type: gauge, unit: seconds, v: ok}
  - {root: node_timex_estimated_error_seconds, type: gauge, unit: seconds, v: ok}
  - {root: node_timex_maxerror_seconds, type: gauge, unit: seconds, v: ok}
  - {root: node_timex_sync_status, type: gauge, unit: bool, v: ok}
  - {root: node_textfile_scrape_error, type: gauge, unit: bool, v: ok}
  - {root: node_os_info, type: gauge, unit: info, v: ok, note: "value=1; labels +id,name,pretty_name,version,version_id"}
  - {root: node_uname_info, type: gauge, unit: info, v: ok, note: "value=1; labels +domainname,machine,nodename,release,sysname,version"}
  # ── full-profile superset additions (ProfileFull only) ──
  - {root: process_cpu_seconds_total, type: counter, unit: seconds, v: ok, note: "full only"}
  - {root: process_resident_memory_bytes, type: gauge, unit: bytes, v: ok, note: "full only"}
  - {root: process_open_fds, type: gauge, unit: count, v: ok, note: "full only"}
  - {root: process_max_fds, type: gauge, unit: count, v: ok, note: "full only"}
  - {root: node_exporter_build_info, type: gauge, unit: info, v: ok, note: "full only; value=1; labels +branch,goarch,goos,goversion,revision,tags,version"}
  - {root: go_goroutines, type: gauge, unit: count, v: ok, note: "full only"}
  - {root: go_memstats_alloc_bytes, type: gauge, unit: bytes, v: ok, note: "full only"}
  - {root: promhttp_metric_handler_requests_total, type: counter, unit: count, v: ok, note: "full only; +code"}
```

---

## macOS node_exporter [slug: host-node-macos]

`job=integrations/macos-node`. The macOS exporter build exposes a macOS-specific memory model
(no Linux `/proc/meminfo` fields): `node_memory_total_bytes` + `compressed`/`internal`/`purgeable`/
`wired` + a direct `swap_used`/`swap_total` split. No netstat/sockstat/vmstat/systemd/conntrack
families exist on macOS (absent, not empty). `node_cpu_seconds_total` modes are the macOS 4-mode
set (idle/nice/system/user). Memory magnitudes are anchored to the captured `host-mac01` host (32 GiB:
wired ≈12%, compressed ≈13%, internal ≈24%, purgeable ≈0.3%, swap_total 1 GiB, swap_used near-zero).
There is NO `full` superset on macOS — the keep-set is the integration set regardless of profile.

> ⚠ Battery/power-supply (`node_power_supply_*`) is present on the real `host-mac01` capture but is
> currently OUT OF SCOPE for the synth (no fixture battery topology) — not emitted.

```yaml signals
family: node_exporter_macos
scope: substrate
sink: promrw
labels:
  job: integrations/macos-node
  instance: <hostname>
  cpu: <index>                 # node_cpu_seconds_total
  mode: idle|nice|system|user  # node_cpu_seconds_total (macOS 4-mode)
  device: <dev>                # node_disk_*, node_network_* (BSD names: disk0, en0, …)
  fstype: <fstype>             # node_filesystem_* (apfs, smbfs, …)
  mountpoint: <path>           # node_filesystem_*
  # node_os_info: id,name,pretty_name,version,version_id
  # node_uname_info: domainname,machine(=arm64),nodename,release,sysname(=Darwin),version
metrics:
  - {root: up, type: gauge, unit: bool, v: ok, note: "emitter-owned"}
  - {root: node_load1, type: gauge, unit: load, v: ok}
  - {root: node_load5, type: gauge, unit: load, v: ok}
  - {root: node_load15, type: gauge, unit: load, v: ok}
  - {root: node_cpu_seconds_total, type: counter, unit: seconds, v: ok, note: "+cpu,mode (4 modes)"}
  - {root: node_boot_time_seconds, type: gauge, unit: unix_timestamp, v: ok}
  - {root: node_memory_total_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_wired_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_compressed_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_internal_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_purgeable_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_swap_total_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_swap_used_bytes, type: gauge, unit: bytes, v: ok, note: "near-zero on macOS (compressed memory)"}
  - {root: node_disk_read_bytes_total, type: counter, unit: bytes, v: ok, note: "+device"}
  - {root: node_disk_written_bytes_total, type: counter, unit: bytes, v: ok, note: "+device"}
  - {root: node_disk_io_time_seconds_total, type: counter, unit: seconds, v: ok, note: "+device"}
  - {root: node_filesystem_size_bytes, type: gauge, unit: bytes, v: ok, note: "+device,fstype,mountpoint"}
  - {root: node_filesystem_avail_bytes, type: gauge, unit: bytes, v: ok, note: "+device,fstype,mountpoint"}
  - {root: node_filesystem_files, type: gauge, unit: count, v: ok, note: "+device,fstype,mountpoint"}
  - {root: node_filesystem_files_free, type: gauge, unit: count, v: ok, note: "+device,fstype,mountpoint"}
  - {root: node_filesystem_readonly, type: gauge, unit: bool, v: ok, note: "+device,fstype,mountpoint"}
  - {root: node_network_receive_bytes_total, type: counter, unit: bytes, v: ok, note: "+device"}
  - {root: node_network_transmit_bytes_total, type: counter, unit: bytes, v: ok, note: "+device"}
  - {root: node_network_receive_packets_total, type: counter, unit: count, v: ok, note: "+device"}
  - {root: node_network_transmit_packets_total, type: counter, unit: count, v: ok, note: "+device"}
  - {root: node_network_receive_errs_total, type: counter, unit: count, v: ok, note: "+device"}
  - {root: node_network_transmit_errs_total, type: counter, unit: count, v: ok, note: "+device"}
  - {root: node_network_receive_drop_total, type: counter, unit: count, v: ok, note: "+device"}
  - {root: node_network_transmit_drop_total, type: counter, unit: count, v: ok, note: "+device"}
  - {root: node_textfile_scrape_error, type: gauge, unit: bool, v: ok}
  - {root: node_os_info, type: gauge, unit: info, v: ok, note: "value=1; +id,name,pretty_name,version,version_id"}
  - {root: node_uname_info, type: gauge, unit: info, v: ok, note: "value=1; +domainname,machine,nodename,release,sysname,version"}
```

---

## Windows windows_exporter [slug: host-windows]

`job=integrations/windows_exporter` (**underscore**). Base labels `{instance, job}`. The keep-set
is the integration allowlist reconciled against the real host-win01 (Windows Server 2025) capture.
`windows_cpu_time_total` carries `core` (live format `"<group>,<core>"`, e.g. `"0,0"`) × `mode`
∈ {idle, user, privileged, interrupt, dpc}. The real metric for service status is
**`windows_service_state{name,state}`** (the phantom `windows_service_status` does NOT exist);
service `state` is the running/stopped terminal-state vocab. Disk status is
**`windows_diskdrive_status{name,status}`** (no underscore split). Pagefile series carry a `file`
label (`C:\pagefile.sys`). There is NO `full` superset on Windows.

> ⚠ Confirmed-NOT-real on the live host-win01 host (the `cs` and `process` collectors are not
> enabled by the chart default): `windows_service_status`, `windows_cs_*`,
> `windows_disk_drive_status` (wrong spelling), `windows_process_*`. These are NOT emitted.
> Several allowlist-described names the emitter computes are also FILTERED OUT by the integration
> keep-set and not emitted (`windows_memory_available_bytes`, `windows_memory_committed_bytes`,
> `windows_os_hostname`, `windows_net_packets_received_total`). Five further OS/system names are
> deferred to live-capture (cantfind PENDING — see below).

```yaml signals
family: windows_exporter_host
scope: substrate
sink: promrw
labels:
  job: integrations/windows_exporter
  instance: <hostname>
  core: <group>,<core>         # windows_cpu_time_total (e.g. "0,0")
  mode: idle|user|privileged|interrupt|dpc   # windows_cpu_time_total
  volume: <vol>                # windows_logical_disk_* (e.g. "C:")
  nic: <adapter>               # windows_net_bytes_received_total/_sent_total
  name: <svc|drive>            # windows_service_state, windows_diskdrive_status
  state: running|stopped|…     # windows_service_state
  status: OK|Degraded|Error|…  # windows_diskdrive_status (healthy=OK)
  file: <pagefile>             # windows_pagefile_* (C:\pagefile.sys)
  timezone: <tz>               # windows_time_timezone (e.g. "GMT Standard Time")
  # windows_os_info: build_number,major_version,minor_version,product,revision,version
metrics:
  - {root: up, type: gauge, unit: bool, v: ok, note: "emitter-owned"}
  - {root: windows_cpu_logical_processor, type: gauge, unit: count, v: ok}
  - {root: windows_cpu_time_total, type: counter, unit: seconds, v: ok, note: "+core,mode (5 modes)"}
  - {root: windows_logical_disk_size_bytes, type: gauge, unit: bytes, v: ok, note: "+volume"}
  - {root: windows_logical_disk_free_bytes, type: gauge, unit: bytes, v: ok, note: "+volume"}
  - {root: windows_logical_disk_read_bytes_total, type: counter, unit: bytes, v: ok, note: "+volume"}
  - {root: windows_logical_disk_write_bytes_total, type: counter, unit: bytes, v: ok, note: "+volume"}
  - {root: windows_diskdrive_status, type: gauge, unit: bool, v: ok, note: "value=1; +name,status (healthy=OK; real name has NO underscore split)"}
  - {root: windows_memory_physical_total_bytes, type: gauge, unit: bytes, v: ok}
  - {root: windows_memory_physical_free_bytes, type: gauge, unit: bytes, v: ok}
  - {root: windows_net_bytes_received_total, type: counter, unit: bytes, v: ok, note: "+nic"}
  - {root: windows_net_bytes_sent_total, type: counter, unit: bytes, v: ok, note: "+nic"}
  - {root: windows_service_state, type: gauge, unit: bool, v: ok, note: "value=1 on the active state; +name,state — REAL metric (windows_service_status is phantom)"}
  - {root: windows_pagefile_limit_bytes, type: gauge, unit: bytes, v: ok, note: "+file"}
  - {root: windows_pagefile_free_bytes, type: gauge, unit: bytes, v: ok, note: "+file"}
  - {root: windows_system_context_switches_total, type: counter, unit: count, v: ok}
  - {root: windows_system_processor_queue_length, type: gauge, unit: count, v: ok}
  - {root: windows_time_computed_time_offset_seconds, type: gauge, unit: seconds, v: ok}
  - {root: windows_time_ntp_round_trip_delay_seconds, type: gauge, unit: seconds, v: ok}
  - {root: windows_time_ntp_client_time_sources, type: gauge, unit: count, v: ok}
  - {root: windows_time_timezone, type: gauge, unit: info, v: ok, note: "value=1; +timezone"}
  - {root: windows_os_info, type: gauge, unit: info, v: ok, note: "value=1; +build_number,major_version,minor_version,product,revision,version"}
```

---

## Docker cAdvisor [slug: host-docker]

`job=integrations/docker`. GATED by the host's `docker` switch. The cAdvisor METRIC series carry
the native cadvisor identity labels **`name`** (container name), **`image`** (`image:tag`), and
**`id`** (`/system.slice/docker-<hash>.scope`) — they do **NOT** carry a `container` label
(`container` is a LOGS-only Alloy relabel, see `[slug: host-logs]`). The cpu metric adds
`cpu="total"`; the per-device fs counters add `device`. `machine_*` series are host-level and
carry only `{instance, job}`.

> ⚠ This is the standalone-Docker lane (no Kubernetes). It carries NO pod/namespace/node labels —
> contrast the k8s cAdvisor family which DOES carry `container`/`pod`/`namespace`/`node`
> (`signals/k8s.md [slug: k8s-cadvisor]`). Same `internal/nodeexp` cadvisor vocabulary, different
> caller-owned label scope (`nodeexp.CadvisorDocker`).

```yaml signals
family: docker_cadvisor
scope: substrate
sink: promrw
labels:
  job: integrations/docker
  instance: <hostname>
  name: <container-name>       # all container_* series (NOT `container` — that is logs-only)
  image: <image:tag>           # all container_* series
  id: /system.slice/docker-<hash>.scope   # all container_* series
  cpu: total                   # container_cpu_usage_seconds_total only
  device: <dev>                # container_fs_reads_total/_writes_total only
metrics:
  - {root: up, type: gauge, unit: bool, v: ok, note: "labels: instance,job; emitter-owned"}
  - {root: container_cpu_usage_seconds_total, type: counter, unit: seconds, v: ok, note: "+name,image,id,cpu=total"}
  - {root: container_memory_usage_bytes, type: gauge, unit: bytes, v: ok, note: "+name,image,id"}
  - {root: container_spec_memory_reservation_limit_bytes, type: gauge, unit: bytes, v: ok, note: "+name,image,id"}
  - {root: container_fs_usage_bytes, type: gauge, unit: bytes, v: ok, note: "+name,image,id"}
  - {root: container_fs_reads_total, type: counter, unit: count, v: ok, note: "+name,image,id,device"}
  - {root: container_fs_writes_total, type: counter, unit: count, v: ok, note: "+name,image,id,device"}
  - {root: container_network_receive_bytes_total, type: counter, unit: bytes, v: ok, note: "+name,image,id"}
  - {root: container_network_transmit_bytes_total, type: counter, unit: bytes, v: ok, note: "+name,image,id"}
  - {root: container_network_receive_errors_total, type: counter, unit: count, v: ok, note: "+name,image,id"}
  - {root: container_network_transmit_errors_total, type: counter, unit: count, v: ok, note: "+name,image,id"}
  - {root: container_network_receive_packets_dropped_total, type: counter, unit: count, v: ok, note: "+name,image,id"}
  - {root: container_network_transmit_packets_dropped_total, type: counter, unit: count, v: ok, note: "+name,image,id"}
  - {root: container_last_seen, type: gauge, unit: unix_timestamp, v: ok, note: "+name,image,id"}
  - {root: machine_memory_bytes, type: gauge, unit: bytes, v: ok, note: "host-level; labels: instance,job only"}
  - {root: machine_scrape_error, type: gauge, unit: bool, v: ok, note: "host-level; labels: instance,job only"}
```

---

## Host logs [slug: host-logs]

Four Loki stream families, one per OS log source type, built by
`internal/construct/host/logs.go` (gated by the host's `logs` switch). High-cardinality fields
(`pid`, `uid`, `command`, `executable`, `event_id`, `provider`, `syslog_identifier`) ride in Loki
**structured metadata** (`Line.Meta`) — NEVER as stream labels (the loki sink asserts this;
violations panic). Stream labels are bounded. The Docker log lane is emitted alongside the OS lane
when `docker` is set.

> ⚠ The Docker LOG stream stamps the container name as a **`container`** stream label (the Alloy
> docker-log discovery relabel — correct here), even though the cAdvisor METRIC series use `name`
> (see `[slug: host-docker]`). The name is re-derived from the metric `name` label to stay in sync.

> ⚠ `boot_id` (Linux) is a SHORT stable hex form (NOT the full UUID) so it is low-cardinality
> enough to be a stream label. The macOS lane drops `pid` entirely (embedded in the body
> `sender[pid]: message` only — matching Alloy `stage.label_drop`).

### Linux systemd-journal stream

```yaml signals
family: host_logs_linux_journal
scope: substrate
sink: loki
stream_labels:
  job: integrations/node_exporter
  instance: <hostname>
  unit: <systemd-unit>          # e.g. sshd.service, docker.service, kernel
  level: info|warning|error
  boot_id: <short-hex>          # stable per host; low-card (NOT a UUID)
  transport: journal
structured_metadata:
  pid: <pid>
  uid: <uid>
  command: <process>            # real journal _COMM, DERIVED FROM unit (not random)
  executable: <path>
  syslog_identifier: <id>
```

> **`command` (journal `_COMM`) is derived from `unit`.** Verified against real
> `integrations/node_exporter` journal streams (2026-06-18). Captured unit→command (and the differing
> syslog_identifier where it diverges):
> `sshd.service`→`sshd-session` (syslog `sshd-session`); `cron.service`→`cron`
> (syslog `CRON`); `crond.service`→`crond` (syslog `CROND`); `docker.service`→`dockerd`;
> `containerd.service`→`containerd`; `NetworkManager.service`→`NetworkManager`;
> `systemd-journald.service`→`systemd-journal`; `systemd-resolved.service`→`systemd-resolve`
> and `systemd-networkd.service`→`systemd-network` (both truncated at the 15-char `_COMM`
> limit); `systemd-udevd.service`→`(udev-worker)`; `systemd-logind.service`→`systemd-logind`;
> `tailscaled.service`→`tailscaled`; `rsyslog.service`→`rsyslogd`; `alloy.service`→`alloy`;
> `kernel`→`kernel`. `command` is NEVER constant across distinct units.

### Windows event-log stream

```yaml signals
family: host_logs_windows_event
scope: substrate
sink: loki
stream_labels:
  job: integrations/windows_exporter
  instance: <hostname>
  level: Information|Warning|Error
  source: Application|System    # event channel
  agent_hostname: <hostname>
structured_metadata:
  event_id: <id>
  provider: <event-source>
```

### macOS unified-log stream

```yaml signals
family: host_logs_macos
scope: substrate
sink: loki
stream_labels:
  job: integrations/macos-node
  instance: <hostname>
  hostname: <hostname>
  sender: <process|subsystem>   # e.g. kernel, syslogd, com.apple.xpc.launchd
structured_metadata: {}         # pid embedded in body "sender[pid]: message" only (Alloy stage.label_drop)
```

### Docker container stdout/stderr stream

```yaml signals
family: host_logs_docker
scope: substrate
sink: loki
stream_labels:
  job: integrations/docker
  instance: <hostname>
  container: <container-name>   # stream-label form (METRIC series use `name`)
  stream: stdout|stderr
structured_metadata: {}
```
