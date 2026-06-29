# Vendored sigil.v1 ingest protos — provenance

These are the **message-type definitions** for the Grafana Sigil (AI Observability) ingest API,
vendored so the `internal/sink/sigil` sink can `protojson`-marshal request bodies with the exact
wire shape the hosted ingest accepts. We generate **message types ONLY** (`protoc --go_out`, NO
`protoc-gen-go-grpc`): the sink ships protojson over HTTP/1.1 (`POST /api/v1/{generations,
workflow-steps,scores}:export`), so no gRPC service stubs and no `google.golang.org/grpc` direct
dependency are introduced — consistent with the RW2/OTLP/Pyroscope hand-encode-over-HTTP convention
(ARCHITECTURE I2).

## Upstream

- Repo: `grafana/sigil` (internal), path `sigil/proto/sigil/v1/`.
- Commit: `f0a35385` (captured 2026-06-30 from `~/repos/sigil`).
- Files: `generation_ingest.proto` (Generation + WorkflowStep + their Export RPCs),
  `evaluation_ingest.proto` (ScoreItem + ExportScores RPC).

## Original upstream sha256 (UpstreamOriginalSHA256)

- generation_ingest.proto: `3770a5fd2cd61c285f3ca759b0884bbf473f66764a75a51c7ecbfb4c07c42fc1`
- evaluation_ingest.proto: `0e1f4481ae73fad6ad015d6453b634a87f3fa327ee008bc51ac643d51b09837c`

## De-vendor edits (wire-neutral)

- `option go_package` re-homed to `github.com/rknightion/synthkit/internal/sink/sigil/v1;sigilv1`.
- Nothing else changed: field numbers, types, enum values, and import lines
  (`google/protobuf/struct.proto`, `google/protobuf/timestamp.proto`) are byte-identical.

## Toolchain

- `protoc` (libprotoc) 35.1
- `protoc-gen-go` v1.36.11 (matches `google.golang.org/protobuf` in go.mod)

## Regeneration

```sh
make sigil-proto   # requires protoc + protoc-gen-go on PATH; see Makefile
```

The service definitions in the `.proto` files (`GenerationIngestService`,
`WorkflowStepIngestService`, `ScoreIngestService`) are intentionally NOT generated as Go stubs —
`protoc-gen-go` alone emits only messages/enums. Do not add `protoc-gen-go-grpc`.

## Drift

Sigil is a private repo with no public release URL, so there is no network drift-check (unlike
`rw-proto-check`). Re-vendor manually from a newer `grafana/sigil` checkout if the ingest contract
changes; update the commit + sha256 above.
