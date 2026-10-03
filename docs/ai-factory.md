# AI Factory coverage

The reference estate is assembled incrementally. The shipped `ai-factory` blueprint
currently emits its CPU management substrate and sparse platform automation. GPU
inventory is declarable but its compute cluster remains `emit: false`; declaring
hardware does not establish a collection adapter or vendor telemetry profile.

## Available building blocks

- [Shared syslog mechanic](ai-factory/syslog.md): source-shaped decoded records and
  the real Alloy receiver-health surfaces. The library is not a raw network listener
  or parser. Receiver-health metrics do not imply that device logs are CLI-wired.
- [GPU topology](../ARCHITECTURE.md): PCIe, NVL72 and additive eight-GPU H100 SXM HGX
  inventory. The HGX switch/link map and qualified hardware limits are sourced;
  other product capabilities remain nullable where unconfirmed. Physical inventory
  alone does not establish DCGM or NVSwitch exporter support.
- [Gateway pools](ai-factory/alloyhealth.md): an optional declared Alloy process pool
  with role-specific receiver, queue, peer, scrape and remote-configuration health.
  The pilot uses a two-member HA pool. Queues retain synthetic requests for the
  emitter lifetime, not an actual disk WAL; generic send failures do not uniquely
  identify expired credentials. Member-local remote-configuration data can be omitted
  while independent component health and the other member remain observable.
- [Platform automation](ai-factory/automation.md): low-frequency operational runs,
  with the real-world basis beside each rate. A short live window can legitimately
  contain no trace arrival; do not raise those rates to force readiness.

The gateway's health instruments are Prometheus Remote-Write metrics describing
modeled intake and forwarding. Their presence is not proof of native OTLP metrics,
OTLP logs or a real forwarded trace payload.

## Source and implementation barriers

The following consumers are not enabled in this reference estate:

| Consumer | Barrier and resume boundary |
| --- | --- |
| DCGM | Supported sustained thermal/power limiting needs a shared, reviewed model of requested/achieved clocks and active constraint causes. Temperature at its limit or power equal to its budget does not by itself establish a clock event or violation duration. |
| GPU driver logs | Resume after the source-correct DCGM consumer lands; topology alone does not prove Xid/SXid/NVRM log projection. |
| GPU and Network Operator adapters | Resume after DCGM and the driver-log disposition, with actual GPU resource and pod-identity join evidence. Only that evidence can justify enabling compute-cluster collection. |
| Full GPU reference blueprint | Its prerequisite DCGM landing is unmet. The current increment is not completion of the GPU reference estate. |

Unconfirmed NVSwitch exposition, Grace profiles and absolute HBM thermal limits remain
explicit source barriers. No unsupported metric or hardware envelope is emitted to
fill them.

## Verification limits

Local public-loader/runner checks, source-inventory comparisons and integrated CI
cover the shipped building blocks. A partial historical backend readback found the
receiver-health metrics and CPU node/event data, but the deployment's configured OTLP
lane had no attempt and global readiness was not green. A later final-candidate live
run was blocked before startup by expired operator authentication. Neither is a full
live verification of the current gateway estate; repeat the exact-source build and
backend readback after restoring the authorized credential access.
