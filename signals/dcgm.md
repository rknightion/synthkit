# NVIDIA DCGM GPU exporter: pinned H100 HGX profile

This is the admitted signal contract for dcgm-exporter 4.8.4 with DCGM 4.6.1,
H100 SXM and driver 595.91.07. The `dcgm` construct implements this narrowly admitted profile.
No other product or driver inherits this profile. Global naming and cardinality
rules are in [the signal canon](00-canon.md).

The exporter tag resolves to `fafd151148052628061a80450b4ee037a5fa0c3c`.
Sources are the [stock counter file](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv),
[alternate DCP file](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/dcp-metrics-included.csv),
and [DCGM field definitions](https://github.com/NVIDIA/DCGM/blob/v4.6.1/dcgmlib/dcgm_fields.h).
The 2026-10-03 HGX captures establish valid/absent fields and at-rest arithmetic;
hardware identifiers are intentionally excluded. The nvidia-smi query capture has
SHA256 `403b1ef416c0de549677eef77ba1c9175d5fcf3eb88270ebb98b88092315bd4d`.

## Stock and DCP

The stock CSV configures 25 scalar roots and the label-only driver-version row.
HGX captures contain 24 families and 192 samples for eight GPUs. Raw Xid is absent
at rest. The no-peer single-GPU capture has 23 families because aggregate NVLink
bandwidth is also absent; that capture does not admit the single-GPU topology.
DCP is an alternate profile with the same active roots, not an additive profile.

| Captured family | DCGM field ID | Type | Unit / semantic value envelope and special cases | Vendor source |
|---|---:|---|---|---|
| `DCGM_FI_DEV_CORRECTABLE_REMAPPED_ROWS` | 394 | counter | Count of correctable-error remapped rows; nonnegative cumulative count. | [CSV L88](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L88); [field alias L3673](https://github.com/NVIDIA/DCGM/blob/v4.6.1/dcgmlib/dcgm_fields.h#L3673) |
| `DCGM_FI_DEV_DEC_UTIL` | 207 | gauge | Decoder utilization, percent; 0–100. | [CSV L26](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L26); [ID L799](https://github.com/NVIDIA/DCGM/blob/v4.6.1/dcgmlib/dcgm_fields.h#L799) |
| `DCGM_FI_DEV_ENC_UTIL` | 206 | gauge | Encoder utilization, percent; 0–100. | [CSV L25](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L25); [ID L794](https://github.com/NVIDIA/DCGM/blob/v4.6.1/dcgmlib/dcgm_fields.h#L794) |
| `DCGM_FI_DEV_FB_FREE` | 251 | gauge | Free framebuffer, MiB; 0–81,081 MiB in this captured profile. | [CSV L51](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L51); [field definition](https://docs.nvidia.com/datacenter/dcgm/latest/dcgm-api/dcgm-api-field-ids.html#c.DCGM_FI_DEV_FB_FREE) |
| `DCGM_FI_DEV_FB_RESERVED` | 253 | gauge | Reserved framebuffer, MiB; 478 MiB observed, constant in captures. | [CSV L53](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L53); [field definition](https://docs.nvidia.com/datacenter/dcgm/latest/dcgm-api/dcgm-api-field-ids.html#c.DCGM_FI_DEV_FB_RESERVED) |
| `DCGM_FI_DEV_FB_USED` | 252 | gauge | Used framebuffer, MiB; 0–81,081 MiB in this captured profile. | [CSV L52](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L52); [field definition](https://docs.nvidia.com/datacenter/dcgm/latest/dcgm-api/dcgm-api-field-ids.html#c.DCGM_FI_DEV_FB_USED) |
| `DCGM_FI_DEV_GPU_TEMP` | 150 | gauge | Core GPU temperature, °C; observed idle 31–34 °C and load up to 58 °C in the captures. These are observations, not a vendor hard limit. | [CSV L11](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L11); [field definition](https://docs.nvidia.com/datacenter/dcgm/latest/dcgm-api/dcgm-api-field-ids.html#c.DCGM_FI_DEV_GPU_TEMP_CELSIUS) |
| `DCGM_FI_DEV_GPU_UTIL` | 203 | gauge | GPU utilization, percent; 0–100. | [CSV L23](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L23); [field alias L3657](https://github.com/NVIDIA/DCGM/blob/v4.6.1/dcgmlib/dcgm_fields.h#L3657) |
| `DCGM_FI_DEV_MEMORY_TEMP` | 140 | gauge | Device memory temperature, °C; observed idle 37–40 °C and load up to 65 °C in captures. Not interchangeable with core GPU temperature. | [CSV L10](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L10); [field definition](https://docs.nvidia.com/datacenter/dcgm/latest/dcgm-api/dcgm-api-field-ids.html#c.DCGM_FI_DEV_MEMORY_TEMP_CELSIUS) |
| `DCGM_FI_DEV_MEM_CLOCK` | 101 | gauge | Memory clock, MHz; observed max 2,619 MHz. | [CSV L7](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L7); [field definition](https://docs.nvidia.com/datacenter/dcgm/latest/dcgm-api/dcgm-api-field-ids.html#c.DCGM_FI_DEV_MEM_CLOCK) |
| `DCGM_FI_DEV_MEM_COPY_UTIL` | 204 | gauge | Memory utilization, percent; 0–100. | [CSV L24](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L24); [field definition](https://github.com/NVIDIA/DCGM/blob/v4.6.1/dcgmlib/dcgm_fields.h#L780) |
| `DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL` | 449 | gauge | Aggregate NVLink throughput, MB/s; no-peer GPU has no sample/family; not an error count. | [CSV L80](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L80); [field alias L3569](https://github.com/NVIDIA/DCGM/blob/v4.6.1/dcgmlib/dcgm_fields.h#L3569) |
| `DCGM_FI_DEV_PCIE_REPLAY_COUNTER` | 202 | counter | PCIe replay/retry count; nonnegative cumulative count. | [CSV L20](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L20); [field alias L3656](https://github.com/NVIDIA/DCGM/blob/v4.6.1/dcgmlib/dcgm_fields.h#L3656) |
| `DCGM_FI_DEV_POWER_USAGE` | 155 | gauge | Device power draw, W; captured configured/enforced/default maximum was 700 W, minimum 200 W. | [CSV L14](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L14); [field alias L3642](https://github.com/NVIDIA/DCGM/blob/v4.6.1/dcgmlib/dcgm_fields.h#L3642) |
| `DCGM_FI_DEV_ROW_REMAP_FAILURE` | 395 | gauge | Whether row remapping failed; boolean encoding 0/1. | [CSV L89](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L89); [field alias L3674](https://github.com/NVIDIA/DCGM/blob/v4.6.1/dcgmlib/dcgm_fields.h#L3674) |
| `DCGM_FI_DEV_SM_CLOCK` | 100 | gauge | SM clock, MHz; observed max 1,980 MHz. | [CSV L6](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L6); [field definition](https://docs.nvidia.com/datacenter/dcgm/latest/dcgm-api/dcgm-api-field-ids.html#c.DCGM_FI_DEV_SM_CLOCK) |
| `DCGM_FI_DEV_TOTAL_ENERGY_CONSUMPTION` | 156 | counter | mJ since driver last reload; cumulative within a driver lifetime, can reset on reload. | [CSV L17](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L17); [field definition](https://github.com/NVIDIA/DCGM/blob/v4.6.1/dcgmlib/dcgm_fields.h#L644) |
| `DCGM_FI_DEV_UNCORRECTABLE_REMAPPED_ROWS` | 393 | counter | Count of uncorrectable-error remapped rows; nonnegative cumulative count. | [CSV L87](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L87); [field alias L3672](https://github.com/NVIDIA/DCGM/blob/v4.6.1/dcgmlib/dcgm_fields.h#L3672) |
| `DCGM_FI_DEV_VGPU_LICENSE_STATUS` | 526 | gauge | DCGM enum: `0 = vgpu is not licensed`, `1 = vgpu is licensed`; capture bare-metal value was 0. | [CSV L84](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L84); [field definition](https://docs.nvidia.com/datacenter/dcgm/latest/dcgm-api/dcgm-api-field-ids.html#c.DCGM_FI_DEV_VGPU_LICENSE_STATUS) |
| `DCGM_FI_PROF_DRAM_ACTIVE` | 1005 | gauge | Ratio of device memory interface cycles active; ratio 0–1. | [CSV L108](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L108); [field alias L3825](https://github.com/NVIDIA/DCGM/blob/v4.6.1/dcgmlib/dcgm_fields.h#L3825) |
| `DCGM_FI_PROF_GR_ENGINE_ACTIVE` | 1001 | gauge | Ratio of time graphics engine active; ratio 0–1. | [CSV L104](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L104); [field alias L3821](https://github.com/NVIDIA/DCGM/blob/v4.6.1/dcgmlib/dcgm_fields.h#L3821) |
| `DCGM_FI_PROF_PCIE_RX_BYTES` | 1010 | gauge | PCIe receive rate in bytes/second; nonnegative rate, not a cumulative byte counter. | [CSV L113](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L113); [field definition](https://github.com/NVIDIA/DCGM/blob/v4.6.1/dcgmlib/dcgm_fields.h#L2594) |
| `DCGM_FI_PROF_PCIE_TX_BYTES` | 1009 | gauge | PCIe transmit rate in bytes/second; nonnegative rate, not a cumulative byte counter. | [CSV L112](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L112); [field definition](https://github.com/NVIDIA/DCGM/blob/v4.6.1/dcgmlib/dcgm_fields.h#L2586) |
| `DCGM_FI_PROF_PIPE_TENSOR_ACTIVE` | 1004 | gauge | Ratio of tensor-pipe cycles active; ratio 0–1. | [CSV L107](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv#L107); [field alias L3824](https://github.com/NVIDIA/DCGM/blob/v4.6.1/dcgmlib/dcgm_fields.h#L3824) |

## Selected fault-detection additions

This selection is synthkit-specific. It adds 17 configured roots to stock and
excludes P2P status. It does not claim every configured row emits a sample.

| Added field | Type | Source / purpose |
|---|---|---|
| `DCGM_FI_DEV_POWER_VIOLATION` | counter | Throttle duration from power constraints; default CSV L30 |
| `DCGM_FI_DEV_THERMAL_VIOLATION` | counter | Throttle duration from thermal constraints; default CSV L31 |
| `DCGM_FI_DEV_ECC_SBE_VOL_TOTAL` | counter | Volatile single-bit ECC; L56 |
| `DCGM_FI_DEV_ECC_DBE_VOL_TOTAL` | counter | Volatile double-bit ECC; L57 |
| `DCGM_FI_DEV_ECC_SBE_AGG_TOTAL` | counter | Aggregate single-bit ECC; L58 |
| `DCGM_FI_DEV_ECC_DBE_AGG_TOTAL` | counter | Aggregate double-bit ECC; L59 |
| `DCGM_FI_DEV_RETIRED_SBE` | counter | Retired pages due to SBE; L62 |
| `DCGM_FI_DEV_RETIRED_DBE` | counter | Retired pages due to DBE; L63 |
| `DCGM_FI_DEV_RETIRED_PENDING` | gauge | Pending retirement boolean; L64 |
| `DCGM_FI_DEV_NVLINK_CRC_ERROR_TOTAL` | counter | Hopper NVLink CRC; L73 |
| `DCGM_FI_DEV_NVLINK_RECOVERY_TOTAL` | counter | Hopper NVLink recovery; L74 |
| `DCGM_FI_DEV_NVLINK_REPLAY_TOTAL` | counter | Hopper NVLink replay; L75 |
| `DCGM_EXP_CLOCK_EVENTS_COUNT` | gauge | Reason-labeled events in configured window; L39 |
| `DCGM_EXP_CLOCK_EVENTS_TOTAL` | counter | Edge-counted reason events since exporter start; L40 |
| `DCGM_EXP_XID_ERRORS_COUNT` | gauge | Xid-labeled events in configured window; L41 |
| `DCGM_EXP_XID_ERRORS_TOTAL` | counter | Cumulative Xid events since exporter start; L42 |
| `DCGM_EXP_GPU_HEALTH_STATUS` | gauge | Eleven watch samples/GPU; L43 |

At rest the selected HGX profile has 46 samples per GPU: 24 stock, 11 ordinary
optional samples and 11 health-watch samples. The latter collapse into one metric
family, so this is 36 observed families. Retired SBE/DBE/pending fields and both
exporter cumulative event totals are absent in the capture. Never synthesize their
zeros to reach a configured-root count. Event-created reason/Xid dimensions change
sample cardinality.

The pinned CSV and DCGM v4.6.1 header define both violation durations in **ns**.
Third-party descriptions in microseconds do not override that source. Energy is
mJ since driver reload. Aggregate ECC totals are persistent; volatile ECC totals
are distinct. Exporter event totals have the exporter's process lifetime. These
lifetimes must not be conflated during resets.

## Labels and omission

GPU samples use `gpu`, `UUID`, `pci_bus_id`, `device`, `modelName`, `hostname` and
`DCGM_FI_DRIVER_VERSION`. Hardware identities come from the declared fixture,
not captured identifiers. Lowercase `hostname` is intentional. The driver row is
a label, never a metric. No serial/version label from the optional all-rows CSV
is implicitly part of stock.

Kubernetes allocation adds `pod`, `namespace` and `container` only while a real
workload owns the GPU. Attribution must disappear on release. The upstream
[transformation](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/internal/pkg/transformation/kubernetes.go#L260-L317)
also supports optional `pod_uid`; a synthetic implementation must not enable it
implicitly or substitute an unrelated identity. Correct attribution requires a
join against actual kube-state-metrics pod output, not just matching label intent.

The [collector](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/internal/pkg/collector/gpu_collector.go#L930-L959)
omits blank/non-OK values; [sentinel conversion](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/internal/pkg/collector/utils.go#L91-L130)
covers unsupported, missing and unavailable values. Omitted values are not zero.

## Event state

Clock `COUNT` samples carry `window_size_in_ms` and, when an observed reason
exists, `clock_event`. The reason vocabulary is `gpu_idle`, `clocks_setting`,
`power_cap`, `hw_slowdown`, `sync_boost`, `sw_thermal`, `hw_thermal`,
`hw_power_brake`, `display_clocks`. A window with no valid source values produces one zero sample without a reason.
Pinned exporter 4.8.4 creates an empty per-entity map for valid zero-mask values
and then emits no COUNT row for that entity; this differs from missing source values. Window count reflects collected field samples, not necessarily
rising transitions. Source: [clock window collector](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/internal/pkg/collector/clock_events_collector.go).

Clock `TOTAL` counts only observed inactive-to-active transitions per reason.
The first observation primes the baseline without counting startup-active reasons.
Unobserved reasons have no total sample; sustained activation is not another
transition every scrape. Source: [cumulative collector](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/internal/pkg/collector/clock_events_total_collector.go#L127-L153).

Xid `COUNT` is windowed with `xid` and `window_size_in_ms`; an empty window has a
zero sample without `xid`. Xid `TOTAL` accumulates observed nonblank, nonzero
values with an `xid` label and no window label. Raw `DCGM_FI_DEV_XID_ERRORS`
represents the last event and adds `err_code` and `err_msg`; these differ from the
numeric `xid` label. Source: [Xid total collector](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/internal/pkg/collector/xid_total_collector.go#L127-L146)
and the raw collector above. No universal thermal/power Xid code is admitted.

## Health metadata

There are eleven watches: `PCIE`, `NVLINK`, `PMU`, `MCU`, `MEM`, `SM`, `INFOROM`,
`THERMAL`, `POWER`, `DRIVER`, `ALL`. Every health sample carries `health_watch`,
`health_error_code`, `health_error_severity` and `health_error_category`.
The error-code label uses the symbolic name, never its integer ID. Healthy
metadata is `DCGM_FR_OK`, `NONE`, `NONE`. Sources:
[exporter health collector](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/internal/pkg/collector/gpu_health_collector.go),
[DCGM metadata table](https://github.com/NVIDIA/DCGM/blob/v4.6.1/dcgmlib/src/dcgm_errors.c).

| Error code | Severity | Category |
|---|---|---|
| DCGM_FR_XID_ERROR | TRIAGE | SOFTWARE_XID |
| DCGM_FR_SBE_VIOLATION | ISOLATE | HARDWARE_MEMORY |
| DCGM_FR_DBE_VIOLATION | ISOLATE | HARDWARE_MEMORY |
| DCGM_FR_ROW_REMAP_FAILURE | RESET | HARDWARE_MEMORY |
| DCGM_FR_CLOCK_THROTTLE_THERMAL | MONITOR | HARDWARE_THERMAL |
| DCGM_FR_THERMAL_VIOLATIONS | MONITOR | HARDWARE_THERMAL |
| DCGM_FR_CLOCK_THROTTLE_POWER | MONITOR | HARDWARE_POWER |
| DCGM_FR_NVLINK_ERROR_THRESHOLD | ISOLATE | HARDWARE_NVLINK |
| DCGM_FR_NVLINK_DOWN | TRIAGE | HARDWARE_NVLINK |
| DCGM_FR_FALLEN_OFF_BUS | ISOLATE | HARDWARE_OTHER |

## Physical and platform boundaries

The captured usable framebuffer is 81,081 MiB; reserved is 478 MiB. Advertised
80 GB is neither usable framebuffer nor reserved memory. Synthetic allocation
conservation is exact; captured rounded free+used+reserved can differ by 1 MiB.
The captured clock maxima are 1,980 MHz SM and 2,619 MHz memory. Configurable
power budgets are 200–700 W; 200 W is not minimum actual device draw.
Core temperature thresholds are specific to this product/driver: operating
reference 87 C, slowdown 89 C, shutdown 95 C. No absolute HBM thermal limit was
established. See [the constraint API contract](../docs/ai-factory/gpu-constraints.md)
for captured provenance and explicit synthetic demand semantics.

A constraint flag alone establishes neither a vendor event reason nor elapsed
violation duration. Collection must define the software/hardware cause and
observation interval explicitly, preserve simultaneous causes, and never infer
a clock response curve. Unavailable GPUs omit achieved clocks; task-required
bus loss removes their GPU series.

NVSwitch exporter exposition and Grace CPU profiles remain pending. Host dcgmi
switch observations do not establish exporter samples or CSV selection. MIG is
explicitly deferred. Unsupported profiles must fail closed, never borrow H100
bounds. No physical endpoint mapping may be invented from exporter labels.

## Implemented model [slug: dcgm-h100]

The integration selects `gpu_pools` and supports `stock`, `dcp` and `fault-detection`.
Only selected eight-GPU HGX nodes with H100 SXM and declared driver 595.91.07 are
admitted. Usable framebuffer must be the explicitly sourced 81,081 MiB declaration;
reserved framebuffer is the captured 478 MiB. Unsupported pools fail construction
rather than producing guessed samples. P2P, MIG, NVSwitch and Grace emission is absent.

Pool `operating.constraints` declares requested clocks, full-load unconstrained power,
a stronger power-incident demand and software-thermal/power caps. Shared allocation
utilization interpolates the demand from the declared idle floor to the full-load
request. Existing budget-bounded actual power and temperature remain the shared
operating model, not a vendor transfer curve. `gpu_power_constraint` is a shared
GPU/node demand incident. `gpu_cooling_fault` is the existing rack/device cause.
The separate fault admission requires baseline worst-case core temperature <=87 C
and fault worst-case <95 C; it does not weaken the legacy thermal capability.

The renderer uses an explicit synthetic HBM/core temperature offset and declared
engine-activity/traffic fractions. These are operator modeling inputs, not hardware
limits or measured throughput. Rates are zero when no such activity is declared.
No shipped blueprint enables this consumer or increases rates to force activity.
The focused HGX verification fixture is outside the shipped blueprint directory.

Cumulative energy (mJ), violation time (ns) and NVLink CRC observations integrate the
previous observed state over elapsed time. The first sample primes with no invented
past duration. This is a piecewise-constant synthetic device model; it does not infer
unobserved real transitions. Exporter observation spacing defaults to the captured
5 seconds and the event window to 300 seconds. Emission remains once per minute.
The captured baseline has no observed clock reason and keeps the zero-count envelope.
After a modeled reason has established valid source values, inactive zero-mask samples
remain valid: when active samples expire, the pinned exporter omits COUNT instead of
creating a zero row. Window clock counts count field observations; totals count rising transitions, and a
startup-active cause primes without creating a transition. Both causes are independent.

`gpu_xid_burst` creates the configured count of the selected sourced Xid at each active
observation. The nonfatal burst selector admits codes 48, 63, 64 and 74, not
GPU-wide fatal code 95. The raw gauge retains the last value; window counts expire independently
of cumulative totals. `dcgm_ecc_error_growth` produces explicitly declared DBE and
uncorrectable-remap collection events, with no claimed universal DBE-to-row ratio.
No driver-reload/reset scenario is implemented: volatile and aggregate ECC remain
separate instruments, and generator restart retains the documented new-run counter
reset behavior. `dcgm_health_failure` selects the sourced memory health failure.
Shared physical NVLink error rates are projected as the declared CRC-error scenario;
recovery/replay stay at their supported healthy zero. Bus loss omits all affected GPU
series. Other GPUs and unavailable-GPU clocks are never filled with placeholders.

Health values are PASS=0, WARN=10 and FAIL=20 from the pinned DCGM header. The pinned
exporter renders the older `CLOCK_THROTTLE_*` error labels, despite newer aliases in
DCGM headers. The all-rows capture itself includes a POWER warning with that label;
its profile arithmetic is not a claim that every captured watch was healthy.

Kubernetes mode attaches `pod`, `namespace`, `container` only to held allocations,
using the shared worker identity; public runner checks join it to actual KSM pod
output and prove omission after release. This is not admission of all GPU compute
collection adapters: the shipped reference compute cluster stays disabled until
its remaining capacity/platform/network/operator contracts are verified.

```yaml signals
family: dcgm_h100_stock
scope: substrate
sink: promrw
labels:
  gpu: <gpu-index>
  UUID: <declared-gpu-uuid>
  pci_bus_id: <declared-pci-bus-id>
  device: <nvidia-device>
  modelName: NVIDIA H100 80GB HBM3
  hostname: <declared-node>
  DCGM_FI_DRIVER_VERSION: 595.91.07
  pod: <allocated-pod; kubernetes mode only, omitted when unheld>
  namespace: <allocated-namespace; kubernetes mode only, omitted when unheld>
  container: <allocated-container; kubernetes mode only, omitted when unheld>
metrics:
  - {root: DCGM_FI_DEV_CORRECTABLE_REMAPPED_ROWS, type: counter, unit: count, v: ok}
  - {root: DCGM_FI_DEV_DEC_UTIL, type: gauge, unit: percent, v: ok}
  - {root: DCGM_FI_DEV_ENC_UTIL, type: gauge, unit: percent, v: ok}
  - {root: DCGM_FI_DEV_FB_FREE, type: gauge, unit: mebibytes, v: ok}
  - {root: DCGM_FI_DEV_FB_RESERVED, type: gauge, unit: mebibytes, v: ok}
  - {root: DCGM_FI_DEV_FB_USED, type: gauge, unit: mebibytes, v: ok}
  - {root: DCGM_FI_DEV_GPU_TEMP, type: gauge, unit: celsius, v: ok}
  - {root: DCGM_FI_DEV_GPU_UTIL, type: gauge, unit: percent, v: ok}
  - {root: DCGM_FI_DEV_MEMORY_TEMP, type: gauge, unit: celsius, v: ok}
  - {root: DCGM_FI_DEV_MEM_CLOCK, type: gauge, unit: megahertz, v: ok}
  - {root: DCGM_FI_DEV_MEM_COPY_UTIL, type: gauge, unit: percent, v: ok}
  - {root: DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL, type: gauge, unit: megabytes_per_second, v: ok}
  - {root: DCGM_FI_DEV_PCIE_REPLAY_COUNTER, type: counter, unit: count, v: ok}
  - {root: DCGM_FI_DEV_POWER_USAGE, type: gauge, unit: watts, v: ok}
  - {root: DCGM_FI_DEV_ROW_REMAP_FAILURE, type: gauge, unit: boolean, v: ok}
  - {root: DCGM_FI_DEV_SM_CLOCK, type: gauge, unit: megahertz, v: ok}
  - {root: DCGM_FI_DEV_TOTAL_ENERGY_CONSUMPTION, type: counter, unit: millijoules, v: ok}
  - {root: DCGM_FI_DEV_UNCORRECTABLE_REMAPPED_ROWS, type: counter, unit: count, v: ok}
  - {root: DCGM_FI_DEV_VGPU_LICENSE_STATUS, type: gauge, unit: status, v: ok}
  - {root: DCGM_FI_DEV_XID_ERRORS, type: gauge, unit: code, v: ok, note: "Absent before an event; err_code and err_msg labels."}
  - {root: DCGM_FI_PROF_DRAM_ACTIVE, type: gauge, unit: ratio, v: ok}
  - {root: DCGM_FI_PROF_GR_ENGINE_ACTIVE, type: gauge, unit: ratio, v: ok}
  - {root: DCGM_FI_PROF_PCIE_RX_BYTES, type: gauge, unit: bytes_per_second, v: ok}
  - {root: DCGM_FI_PROF_PCIE_TX_BYTES, type: gauge, unit: bytes_per_second, v: ok}
  - {root: DCGM_FI_PROF_PIPE_TENSOR_ACTIVE, type: gauge, unit: ratio, v: ok}
```

```yaml signals
family: dcgm_h100_fault
scope: substrate
sink: promrw
labels:
  gpu: <gpu-index>
  UUID: <declared-gpu-uuid>
  pci_bus_id: <declared-pci-bus-id>
  device: <nvidia-device>
  modelName: NVIDIA H100 80GB HBM3
  hostname: <declared-node>
  DCGM_FI_DRIVER_VERSION: 595.91.07
  pod: <allocated-pod; kubernetes mode only, omitted when unheld>
  namespace: <allocated-namespace; kubernetes mode only, omitted when unheld>
  container: <allocated-container; kubernetes mode only, omitted when unheld>
metrics:
  - {root: DCGM_EXP_CLOCK_EVENTS_COUNT, type: gauge, unit: count, v: ok, note: "Optional clock_event; COUNT adds window_size_in_ms; TOTAL exists only after a transition."}
  - {root: DCGM_EXP_CLOCK_EVENTS_TOTAL, type: counter, unit: count, v: ok, note: "Optional clock_event; COUNT adds window_size_in_ms; TOTAL exists only after a transition."}
  - {root: DCGM_EXP_GPU_HEALTH_STATUS, type: gauge, unit: status, v: ok, note: "Eleven watches; health_watch, health_error_code, health_error_severity and health_error_category labels; values 0/10/20."}
  - {root: DCGM_EXP_XID_ERRORS_COUNT, type: gauge, unit: count, v: ok, note: "Optional xid; COUNT adds window_size_in_ms; TOTAL exists only after an event."}
  - {root: DCGM_EXP_XID_ERRORS_TOTAL, type: counter, unit: count, v: ok, note: "Optional xid; COUNT adds window_size_in_ms; TOTAL exists only after an event."}
  - {root: DCGM_FI_DEV_ECC_DBE_AGG_TOTAL, type: counter, unit: count, v: ok}
  - {root: DCGM_FI_DEV_ECC_DBE_VOL_TOTAL, type: counter, unit: count, v: ok}
  - {root: DCGM_FI_DEV_ECC_SBE_AGG_TOTAL, type: counter, unit: count, v: ok}
  - {root: DCGM_FI_DEV_ECC_SBE_VOL_TOTAL, type: counter, unit: count, v: ok}
  - {root: DCGM_FI_DEV_NVLINK_CRC_ERROR_TOTAL, type: counter, unit: count, v: ok}
  - {root: DCGM_FI_DEV_NVLINK_RECOVERY_TOTAL, type: counter, unit: count, v: ok}
  - {root: DCGM_FI_DEV_NVLINK_REPLAY_TOTAL, type: counter, unit: count, v: ok}
  - {root: DCGM_FI_DEV_POWER_VIOLATION, type: counter, unit: nanoseconds, v: ok}
  - {root: DCGM_FI_DEV_THERMAL_VIOLATION, type: counter, unit: nanoseconds, v: ok}
```
