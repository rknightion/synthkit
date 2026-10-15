---
title: Checkpoint Foundations
description: Protocol acknowledgement and optional filesystem primitives; runtime takeover and backfill are not enabled yet.
---

# Checkpoint foundations

This increment adds protocol acknowledgement evidence, loss-aware flush barriers and an
optional filesystem persistence primitive. It does **not** enable counter restoration,
durable delivery queues, takeover replay or historical backfill. Generator restart still
starts a new cumulative-state run. No checkpoint environment variables, chart volumes or
standalone Docker mount requirements are introduced.

Existing Kubernetes ConfigMaps continue to hold small control-state documents. They are
not repurposed for per-series state or serialized telemetry payloads. The selected future
checkpoint direction is an operator-owned shared filesystem, enabled only after runtime
state coverage, transaction boundaries and actual shared-mount semantics are verified.

## Protocol acknowledgement

`GET /control/status` includes `last_acknowledgement` on each sink-history entry. The object
contains a closed `status`, `unit`, `expected`, `accepted` and `rejected` count. No receiver
diagnostic text is exposed. Counts describe one final push attempt, not cumulative unique
backend ingestion. `last_success_ms` and lane readiness retain their existing transport
meaning; neither is a checkpoint watermark.

| Status | Meaning |
|---|---|
| `accepted` | The receiver's protocol response acknowledges the complete requested item set. |
| `partial` | Some requested items were accepted and others explicitly rejected. |
| `rejected` | The protocol response explicitly accounts for rejection of the complete item set. |
| `deferred` | Asynchronous acceptance has not supplied complete written-count evidence. |
| `unverified` | The response does not establish complete acceptance, or this lane has no acknowledgement classifier. |

OTLP traces, metrics and logs parse bounded protobuf or JSON export responses. A standard
HTTP 200 full-success response acknowledges the request. Partial rejection is an error and
is never retried; warnings with zero rejected items remain successful. Malformed, negative,
oversized or over-count responses cannot become accepted receipts. JSON unknown fields are
ignored per the protocol, but ambiguous duplicate fields are rejected. Response reads are
bounded to 4 MiB. Legacy non-200 2xx transport behavior remains compatible while acknowledgement
is unverified or deferred. Units are spans, data points or log records.

Remote Write v2 checks the written sample, histogram and exemplar headers against the actual
encoded request. Its unit is `rw2_elements`, the sum of those three categories, which can
differ from the existing logical-series `last_items` count. Missing categories can mean zero
on non-202 responses when written headers are present. A legacy success with no written
headers stays transport-successful but explicitly unverified. HTTP 202 with missing or
incomplete counts is deferred, not rejected and not blindly retried. A contradictory 2xx
written count fails the push without retrying an already partially accepted request.

These are receiver-acceptance facts, not final Grafana ingestion/readback proof. Other sink
lanes remain unverified by this classifier. Backend queries remain necessary when validating
a deployment. Sources: [OTLP response semantics](https://opentelemetry.io/docs/specs/otlp/)
and [Remote Write v2 written-count semantics](https://prometheus.io/docs/specs/prw/).

## Flush barriers

A delivery queue retains the first failure from background sends until the next explicit
`Flush` barrier reports it. A later successful batch cannot erase that failure. The next
reporting interval starts after that barrier, and concurrent barriers serialize with a
context-bounded wait. Producers must still be quiescent while flushing.

This change does not add a WAL or resend discarded batches. Queue dropped-item accounting
counts items removed after failed sends; it can conservatively exceed known remote rejection
when a batch was partially accepted or its outcome was ambiguous. Protocol counts are kept
separately. `DrainJoined` proves sender termination, not successful remote acceptance.

## Optional filesystem primitive

`internal/checkpoint.FileStore` is a developer-facing building block, not a configured runtime
backend. It implements a small pluggable store interface and stores opaque caller-versioned bytes
in immutable SHA-256-named payload files
and publishes a checksummed manifest through fsynced atomic rename. Files are mode 0600 in
a dedicated directory created as mode 0700; existing directories must not be group/world
writable. Existing unbound data, unsupported format, destination or
configuration mismatch, corrupt data, and symlink payloads fail closed.

A binding identifies deployment, destination/configuration fingerprints and payload format.
It contains no credentials. Acquiring an epoch preserves the prior payload, but an initialized
manifest without a payload is not a restored counter snapshot. `Read.Exists=false` means an
observably empty directory; it does not prove that this logical deployment has never had
a checkpoint. Runtime enrollment must distinguish an explicitly authorized first run from
a lost/replaced volume. Read failures never become empty state.

Mutations require the existing HA authority gate. A nil-gate handle opens only existing
storage and cannot create a directory, epoch or payload; `AlwaysLeader` is
only appropriate for explicitly single-writer non-HA use. A store instance acquires one epoch.
Both epoch and revision participate in CAS, and an old instance cannot adopt a newer epoch
merely by re-reading its manifest. The actual lease-backed gate must authorize epoch acquisition
and each write; an incremented integer alone is not leadership authority.

Linux and macOS use an exclusive filesystem lock. Its wait is bounded by the caller's deadline
and an internal five-second ceiling. Filesystem syscalls themselves are not interruptible by
that context: the actual shared provider must support reliable locking, hard links, atomic
rename and file/directory fsync, and must be validated with the runtime crash-fence contract
before multi-node HA is admitted. Unsupported platforms reject the backend. Local subprocess
proofs are not proof of a Kubernetes RWX provider under network failure.

Explicit limits bound payload bytes, retained unique-inode file content and directory entries.
They are not a filesystem-block usage estimate. There is no automatic pruning or garbage
collection. Capacity failures preserve the last published manifest. Failed or cancelled publications retain temporary files
for inspection and count against capacity. An interrupted first epoch acquisition leaves
unbound data and requires explicit operator recovery; automatic reinitialization is refused.
There is no automatic cleanup on cancellation; successful immutable publication removes only
the byte-identical temporary hard link. An I/O failure after publication may be ambiguous:
re-read the manifest rather than assuming rollback or blindly retrying.

## Remaining runtime work

Every selected emitter and auxiliary state path still needs explicit versioned snapshot/restore
support. Exact serialized payloads, post-tick state and scheduler positions must commit before
sending, with durable per-fragment outcomes and legitimate leader epochs. Replaying from old
randomized ticks is not equivalent to replaying committed bytes. Partial rejection must become
an explicit gap, not a whole-request retry. Enrollment evidence outside a potentially lost
payload mount is still required before runtime initialization is admitted. Unknown/deferred
acceptance cannot advance a durable
watermark.

Full takeover, timestamp-bounded backfill, retention/compaction and out-of-order acceptance on
the selected live stack remain separate acceptance work. No existing deployment is migrated
or silently opted into these foundations.
