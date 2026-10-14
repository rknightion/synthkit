# GPU clock constraints

`fixture.GPUConstraintPoints(topology, allocation, demands)` is an additive pure
API. It calls `GPUOperatingPoints` for allocation provenance, validation,
availability, configured power budgets and synthetic core temperatures. It does
not change that snapshot, topology, allocation or existing telemetry output.
Demands cover every GPU exactly once, including unavailable GPUs. Output follows
the existing physical snapshot order and retains its timestamp.

Each demand declares the exact driver version, requested SM and memory clocks in
MHz, unconstrained synthetic power demand in watts, and optional thermal and power
clock caps. A cap contains both clocks, must be finite and nonnegative, and must
not exceed either requested clock. Equal caps are valid but cannot activate a
limiter. Requests must be finite and nonnegative; supported-profile requests must
fit the captured clock maxima. Power demand can exceed a budget: that crossing is
an explicit scenario input, not a measured vendor power curve.

Only `h100_sxm_80gb` with driver `595.91.07` has a supported profile. Its captured
bounds are SM 1980 MHz, memory 2619 MHz, configured power 200–700 W, core slowdown
89 °C, shutdown 95 °C and maximum operating reference 87 °C. The latter two are
retained facts, not activation thresholds. No HBM thermal bound is established.
A matching profile rejects configured power budgets outside its captured range.

Thermal activation requires core temperature strictly greater than 89 °C and a
thermal cap that reduces at least one requested clock. Power activation requires
unconstrained demand strictly greater than the configured budget and a reducing
power cap. Both flags can coexist. Achieved clocks are the componentwise minimum
of requested clocks and caps whose causes are active. There is no interpolation,
clock/power transfer function, shutdown simulation or duration accumulation.
Equality, a cap alone and cooling derating alone cannot activate a limiter.

An unsupported model/driver returns a nil profile, no active flags and nil achieved
clocks. An unavailable GPU also has nil achieved clocks and inactive flags, while
retaining a supported profile when applicable. Consumers must preserve this
absence rather than emitting zero clocks or borrowing another driver's limits.
The profile lookup returns a fresh value; caller changes cannot alter later calls.

## Provenance

These bounds come from the accepted DCGM contract revision 3: NVIDIA DCGM 4.6.1,
dcgm-exporter 4.8.4 (commit `fafd151148052628061a80450b4ee037a5fa0c3c`), H100 SXM,
driver 595.91.07, captured 2026-10-03. The source nvidia-smi query capture SHA256 is
`403b1ef416c0de549677eef77ba1c9175d5fcf3eb88270ebb98b88092315bd4d`.
This hash identifies the captured query, not a public documentation page.

The captured 32 °C core temperature plus 55 °C T.Limit margin establishes the
87 °C operating reference; the query separately reports slowdown 89 °C and
shutdown 95 °C. Interpretation uses the
[current NVIDIA nvidia-smi documentation](https://docs.nvidia.com/deploy/nvidia-smi/index.html),
fetched 2026-10-03, which is unversioned and does not establish another driver's
envelope. Field meanings are recorded in the
[DCGM 4.6.1 field header](https://github.com/NVIDIA/DCGM/blob/v4.6.1/dcgmlib/dcgm_fields.h)
and the [exporter 4.8.4 counter catalogue](https://github.com/NVIDIA/dcgm-exporter/blob/4.8.4/etc/default-counters.csv).
Synthetic requested clocks and caps are operator scenario assumptions, not
captured vendor responses. Unsupported products and drivers remain gaps.
