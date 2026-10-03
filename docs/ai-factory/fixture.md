# GPU compute topology fixture

`gpu_compute` declares physical inventory independently of telemetry collection.
It emits nothing by itself. Consumer integrations select canonical inventory with
`gpu_pools`, `gpu_fabrics`, `gpu_storage_clusters`, and `gpu_schedulers`; selectors
are removed before strict consumer configuration decoding. A GPU-consuming
integration requires an explicit, nonempty selection and cannot fan physical
inventory out with `for_each_env` or `envs`.

The generic example is `e2e/fixtures/ai-factory-fixture.yaml`. It models two four-GPU
PCIe servers and one complete NVL72 rack, with count provenance and configurable
operator assumptions beside the declarations. It is a fixture, not a released
vendor-consumer blueprint. Shipped blueprint rates must not be raised to force
emission.

## Inventory and identity

PCIe pools accept four to eight H100 PCIe GPUs per server. The NVL72 shape has
18 four-GPU compute trays, two Grace CPUs per tray, nine two-switch NVSwitch trays,
and one NVLink domain. The additive `hgx` shape admits exactly eight H100 SXM
GPUs per server, four node-local NVSwitches and eighteen mapped links per GPU.
SXM metadata is not permission to install SXM boards in PCIe servers. See
`e2e/fixtures/ai-factory-hgx.yaml` for the HGX loader/topology fixture; it enables
no GPU telemetry consumer.

Rack references are `rack:<site>/<rack>`. Placements must cover each physical node
ordinal exactly once without overlapping rack slots. PCIe and HGX placements
declare positive server RU height and one-based slots; omitted rack height is 42 U.
These dimensions are operator assumptions, not baseboard facts. NVL72 placements
use logical tray ordinals. No physical peer wiring or vendor port numbering is
inferred from inventory counts.

Default hostnames are `<hostname_prefix>-0000`, independent of pool size. GPU UUID,
node/board serial, BMC and tray identities derive from the seed and physical keys,
not declaration position or enabled collectors. Synthetic serials begin with
`SYN`; PCI placement defaults to the explicitly synthetic
`00000000:<0x20+slot>:00.0`. NVL72 two-GPU boards share their board serial without
sharing GPU UUIDs. Explicit sparse overrides are validated for collisions.

A bound GPU node points to the final canonical `Cluster.Nodes` element. Cloudless
`cluster.type: baremetal` requires explicit OS/runtime/version identity, rejects
EKS node groups and CloudWatch enablement, and uses declared CPU/RAM/architecture
through `LookupNodeSpec`. Fixed nodes do not resize when replicas change.
Standalone GPU pools emit no host signals unless matching `hosts` are explicitly
declared. A node cannot emit both standalone and cluster host lanes.

## Join keys

| Relationship | Canonical fixture identity |
|---|---|
| GPU to node | `gpu:<hostname>/<slot>` → `node:<hostname>`; UUID/minor/PCI from that GPU |
| Node to Kubernetes | Canonical `fixture.Node` pointer; worker pod placement uses its final index |
| Node to management | Canonical BMC `device:<hostname>` and independent management IP/serial |
| NIC to Ethernet | `nic:<hostname>/<nic>` → declared switch and `port:<fabric>/<switch>/<port>` |
| Storage client | `client:<storage>/<hostname>` with frontend NIC IP, not management IP |
| NVL72 membership | Domain, owning partition, compute/switch tray and logical switch/port keys |
| HGX membership | Node-qualified domain/partition/switch/port keys; GPU allocation slot remains distinct from physical module ID |
| Workload ownership | Scheduler/project/workload/worker keys → whole GPU keys and held pod identity |
| Shared physical load | `GPUOperatingPoints` GPU, node, rack and workload outputs from one allocation snapshot |

High-cardinality execution IDs are internal lineage/log facts, never real Slurm job
IDs and never metric or stream labels. No fixture injects universal labels.

## HGX source profile and node-local membership

`GPUNodeOverrideSpec.nvlink` is HGX-only, including explicit-null presence checks
on old shapes. Its only admitted profile is `dgxh100_hgxh100-595.91.07` (also the
omission/empty default). `domain` defaults to `default`. Rack-level `nvlink`
remains NVL72-only. HGX makes no Grace CPUs, compute trays or switch trays, and
its rack `DomainKey` remains empty.

The complete endpoint projection in `internal/fixture/gpu_hgx_links.csv` comes
from NVIDIA's [Fabric Manager package 595.91.07-1ubuntu1, amd64](https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/x86_64/nvidia-fabricmanager_595.91.07-1ubuntu1_amd64.deb),
member `usr/share/nvidia/nvswitch/dgxh100_hgxh100_topology` (SHA-256
`b185b23035d928ad82ad372b72a707d7e1560a9e653417d47110334b1eacd7f8`).
The audited factual projection hash is
`3ec71bc45a3689d0e6144801b1fbb6aee9c4b09265e60613e4dbf06edd33a302`.
All 144 unique access edges are intra-node; connected port counts are 32/40/40/32.
There are no inferred trunk links or unused connected ports. The parser checks
both structural completeness and the entire canonical source table, since port
swaps can preserve counts. The current, unversioned [Fabric Manager guide](https://docs.nvidia.com/datacenter/tesla/fabric-manager-user-guide/index.html)
Table 11 independently corroborates the map, but is not a driver-branch pin.
The binary, descriptor and proprietary routing data are not shipped.

`GPUOverrideSpec.hgx_module_id` is optional and HGX-only. Omission binds the
fixture slot/minor to the equal module ID **synthetically**, not by captured PCI
order. All eight values must form a permutation of 0..7. Switching two module IDs
changes physical switch endpoints, not GPU UUID/minor or worker allocation slots.
Packaged GPU module IDs and switch physical ordinals are zero-based; vendor table
display IDs, PCI order and DCGM entity IDs are separate namespaces.

For hostname `hgx-node-0000`, site/rack `site-a/hgx-a`, domain `baseboard`:

- Domain: `domain:site-a/hgx-a/hgx/hgx-node-0000/baseboard`.
- Partition: `partition:site-a/hgx-a/hgx/hgx-node-0000/baseboard/default`.
- Switch 0: `nvswitch:site-a/hgx-a/hgx/hgx-node-0000/baseboard/0`.
- Port 40: `nvport:site-a/hgx-a/hgx/hgx-node-0000/baseboard/0/40`.
- GPU slot 0, link 2: `nvlink:hgx-node-0000/0/2`.

Default switch names are `<hostname>-nvsw-<physical ordinal>`; serials use the
existing synthetic `GPUSerial` derivation. Sparse switch overrides cannot replace
the source map. Selected switches require their owning node to be selected, even
when another selected node shares the same rack. The domain belongs to node/rack;
the node points only to its rack, avoiding a domain/node cycle.

Custom partitions replace the default, must be disjoint and cover exactly the
node's eight GPUs. They are logical fault memberships, **not** claims of active
Fabric Manager firmware or VM partitions. Entity mappings require sourced,
unique identifiers within that exact node domain. No default exporter IDs are
supplied. `correlated: false` retains all physical facts; `true` additionally
runs complete-map validation. A mixed HGX/old-pool correlation selection must have
a sourced domain for every selected GPU; HGX cannot launder an unmapped PCIe GPU.
Legacy-only capability behavior is unchanged.

An HGX GPU burst reaches its eighteen mapped edges; port/switch/partition/domain
bursts reach only their exact memberships. Overlapping faults use maximum edge
intensity, not addition. Node-local faults cannot reach another node sharing the
rack; rack cooling intentionally can. No HGX target exists for switch-tray loss.
The fixture remains category-free. The root-approved H100 HGX DCGM diagnostic
policy maps the shared rate to CRC only, cumulatively integrating the per-GPU
edge sum; recovery/replay stay at their sourced baseline. That consumer policy is
not a claim that arbitrary real NVLink errors are CRC-only and does not change
fixture physics or legacy fault contracts.

## Allocation, operating assumptions and failures

`GPUAllocationPlan` is a pure, seeded, minute-bucket lifecycle/ownership function.
Candidates are admitted atomically by descending priority then canonical key,
subject to project quotas and nonoverlapping whole-GPU ownership. Unscheduled
pools are idle. Fatal faults preserve baseline victim lineage and release the
entire victim workload; preemption and quota exhaustion have distinct semantics.
Failure state is window-local, not durable recovery history. Fractional/MIG
ownership, migration and fresh pod names per cycle are unsupported.

`GPUOperatingPoints` consumes that snapshot's captured shared faults. Its private
origin must be the exact topology pointer used to allocate it; serialized,
fabricated or independently reconstructed snapshots are not supported. Matching
key strings do not establish origin.

Demand is constant at zero amplitude (an omitted period is then allowed); any
supplied period must still be valid and positive. Utilization applies declared
cooling/backend/storage/NVLink loss factors. Memory uses demand, not reduced
utilization, and releases with ownership. Unavailable-but-powered GPUs retain idle
watts. Cooling applies to every powered GPU, including idle/unavailable devices.
Node watts sum GPU watts and declared CPU/NIC overhead; rack watts sum member
nodes, explicit device watts and the separate fixed overhead. Liquid return
thermal arithmetic uses declared fluid properties and flow. These are configurable
synthetic equations, not measured vendor transfer curves or throttle codes.

Held workload demand/effective utilization are arithmetic means; request capacity
multiplies the sum of held utilization and memory sums held GPU memory. Released
workloads have zero demand, effective utilization, request rate and memory.

Shared `Targets`/`CoversTarget` use the exact shared mode/axis/subkind matrix and
static graph reachability. A shared mode requires an enabled, registered compatible
consumer covering its exact target. Consumer-local collection modes instead
require explicit enabled registration, canonical-axis agreement and exact selected
collection membership. A profiled PDU may have a local polling outage while
`CoversTarget` remains false for it. Missing profiles and unrelated identities
reject; the consumer builder, not the fixture, validates vendor module support.
Local collection outages never enter shared fault capture, allocation or physics.

## Source and adapter release barriers

A declared cluster can retain its fixture and workload bindings without collecting
it: set `cluster.emit: false`. Omitted or explicit `true` preserves the historical
cluster-derived Kubernetes, EC2, profiling, addon and Fleet Management constructs.
This does not disable other environment resources, workloads or independently
selected integrations. `k8s_monitoring.enabled: false` is not a substrate emission
gate: base Kubernetes families still emit unless `cluster.emit` is false. The
reference compute cluster stays emission-disabled until its capacity/platform/NIC
and other collection adapters pass their own gates.

Advertised memory is decimal capacity metadata, not driver usable framebuffer.
Usable framebuffer always requires an explicit per-GPU source; HGX shape/model
admission never fills it. The accepted H100 SXM / driver `595.91.07` idle values
are 85,019,590,656 usable bytes (81,081 MiB), 501,219,328 reserved bytes and
85,520,809,984 total bytes. Only usable bytes enter this declaration; reserved
and total emission are consumer-owned. This exact idle accounting is not a claim
of conservation in every scrape (captured load differed by 1 MiB).

The sole accepted core-temperature catalogue envelope is H100 SXM / driver
`595.91.07`: slowdown 89 C, shutdown 95 C, maximum operating 87 C. Each thermal
field cites `capture://loop48-prep/gpucap/p5.48xlarge-h100x8/cap/nvsmi-q.txt`,
lines 187-191, hash
`403b1ef416c0de549677eef77ba1c9175d5fcf3eb88270ebb98b88092315bd4d`.
Maximum operating is captured core 32 C plus 55 C T.Limit margin, using the
[current nvidia-smi semantics](https://docs.nvidia.com/deploy/nvidia-smi/index.html)
without representing that current page as branch-pinned. Other products/drivers
and absolute HBM ceilings remain unknown. Consumers must enforce supported
model/driver profiles; valid source metadata alone is not exposition proof.
Capability checks fail closed rather than guessing vendor limits. H100 PCIe/H200 SXM/GB200 per-GPU
NVLink counts and physical GPU-link/vendor-port/entity mappings remain unconfirmed;
a correlated profile requires complete sourced mapping. Synthetic cooling factors
must not be translated into unsourced hardware throttle codes.

Generic topology/physics support does not prove vendor exporter or renderer
acceptance. Root immutable master-tick state/time capture and network-topology
adapters, standalone/cluster driver logs, and operator/KSM capacity/lifecycle
adapters have separate owners and gates. Run:ai exposition/query, other correlated
NVLink profiles, Slurm job-labelled mapping and other thermal/framebuffer envelopes
retain their source/policy barriers. HGX physical inventory is not NVSwitch
exporter exposition or entity-ID admission. Actual DCGM/KSM joins, driver logs and
operator-resource adaptation still require their independent consumer gates; a
stub emitting nothing is not a consumer pass.
