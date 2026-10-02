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
and one NVLink domain. SXM catalogue products remain available as metadata, not as
permission to install SXM boards in PCIe servers.

Rack references are `rack:<site>/<rack>`. Placements must cover each physical node
ordinal exactly once without overlapping rack slots. PCIe placements declare RU
height; NVL72 placements use logical tray ordinals. No physical peer wiring or
vendor port numbering is inferred from inventory counts.

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
| Workload ownership | Scheduler/project/workload/worker keys → whole GPU keys and held pod identity |
| Shared physical load | `GPUOperatingPoints` GPU, node, rack and workload outputs from one allocation snapshot |

High-cardinality execution IDs are internal lineage/log facts, never real Slurm job
IDs and never metric or stream labels. No fixture injects universal labels.

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

Advertised memory is decimal capacity metadata, not driver usable framebuffer.
Unknown usable memory and temperature thresholds remain absent. Capability checks
fail closed rather than guessing vendor limits. H100 PCIe/H200 SXM/GB200 per-GPU
NVLink counts and physical GPU-link/vendor-port/entity mappings remain unconfirmed;
a correlated profile requires complete sourced mapping. Synthetic cooling factors
must not be translated into unsourced hardware throttle codes.

Generic topology/physics support does not prove vendor exporter or renderer
acceptance. Root immutable master-tick state/time capture and network-topology
adapters, standalone/cluster driver logs, and operator/KSM capacity/lifecycle
adapters have separate owners and gates. Run:ai exposition/query, correlated NVLink
profiles, Slurm job-labelled mapping, real thermal envelopes and usable framebuffer
retain their source/policy barriers.
