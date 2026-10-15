# H100 HGX DCGM telemetry

The `dcgm` integration emits the pinned dcgm-exporter 4.8.4 / DCGM 4.6.1 profile
for H100 SXM HGX pools with driver 595.91.07. Select a pool explicitly with
`gpu_pools`; unsupported products, drivers and framebuffer declarations fail closed.
The [signal contract](https://github.com/rknightion/synthkit/blob/main/signals/dcgm.md)
owns field names, types, units, health metadata and profile provenance.

## Configuration

Declare the pool's shared `operating.constraints`: exact `driver_version`, requested
SM/memory clocks, `full_demand_power_w`, `power_fault_demand_w`, and optional
`software_thermal_clock_cap` / `power_clock_cap` objects (`sm`, `memory`, MHz).
Caps are explicit synthetic controller setpoints. They are not a vendor response curve.
See [GPU clock constraints](gpu-constraints.md) for strict causal thresholds.

The integration accepts `profile: stock` (default), `dcp`, or `fault-detection`,
and `mode: host` (default) or `kubernetes`. It requires an explicit finite
`memory_temperature_offset_c`; no HBM maximum is assumed. Optional engine fractions
and PCIe/NVLink full-utilization traffic rates describe the operator's workload,
not a device capacity. Their default zero means no declared activity of that kind.

The [focused fixture](https://github.com/rknightion/synthkit/blob/main/e2e/fixtures/dcgm-hgx.yaml)
shows a complete declared HGX pool and integration. It deliberately exercises a
full-load training allocation and thermal/power incidents outside shipped blueprints.
Do not use its stress inputs as production sizing defaults.

## Profiles and identity

Stock and alternate DCP each have 24 baseline families and samples per GPU. The
selected fault profile has 36 baseline families and 46 samples per GPU because the
health family has eleven watches. Event-created Xid/clock totals expand the inventory.
Raw Xid is absent before an event; retired-page rows omitted in the capture remain
omitted. P2P, MIG, NVSwitch and Grace telemetry is not implemented.

Host identity is fixture-owned. Kubernetes mode adds the actual worker pod,
namespace and container while it owns the GPU, then removes those dimensions on
release. It does not emit `pod_uid`. Public runner tests verify the join against
KSM pod output. Other GPU compute collection adapters still have separate admission
requirements; the shipped AI Factory compute cluster remains `emit: false`.

## Incidents and time

Shared `gpu_cooling_fault` and `gpu_power_constraint` drive the clock constraints;
shared bus-loss and NVLink-error faults preserve topology targeting. Collection-only
`dcgm_ecc_error_growth` and `dcgm_health_failure` model the documented ECC/health
observations. `gpu_xid_burst` uses the configured sourced Xid code and declared burst
size. Modeled Xid codes are 48, 63, 64 and 74; GPU-wide fatal code 95 is not
admitted by the nonfatal burst scenario. See [incidents](../incidents.md#h100-hgx-dcgm-failures) for the observables.

The default collection interval is 5 seconds and event window is 300 seconds,
matching the retained capture; metric emission stays at one minute. Duration and
energy counters integrate the previous observed synthetic state, starting at zero
on the first observation. Window counts and rising-edge totals are different:
startup-active clocks do not create an edge, and expiring a window never clears
a cumulative total. For pinned exporter 4.8.4, an empty source window has a zero
COUNT row, while a window containing valid zero clock masks has no COUNT row.
The model preserves that distinction after its first observed clock reason. A generator restart starts a new model run; driver-reload
and exporter-only restart scenarios are not modeled.

The baseline operating reference remains 87 C. Explicit fault scenarios may exceed
the 89 C slowdown threshold but must stay strictly below 95 C shutdown. Equality
at 89 C and a cap alone do not activate thermal limiting; power requires demand
strictly above the configured budget plus a reducing cap. Both may coexist.
