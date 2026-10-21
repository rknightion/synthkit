# Vendored Agent Observability ingest protos — provenance

These are the **message-type definitions** for the Grafana Sigil (AI Observability) ingest API,
vendored so the `internal/sink/sigil` sink can `protojson`-marshal request bodies with the exact
wire shape the hosted ingest accepts. We generate **message types ONLY** (`protoc --go_out`, NO
`protoc-gen-go-grpc`): the sink ships protojson over HTTP/1.1 (`POST /api/v1/{generations,
workflow-steps,scores}:export`), so no gRPC service stubs and no `google.golang.org/grpc` direct
dependency are introduced — consistent with the RW2/OTLP/Pyroscope hand-encode-over-HTTP convention
(ARCHITECTURE I2).

## Upstream

- Repo: `grafana/sigil` (internal), path `sigil/proto/sigil/v1/`.
- Commit: `bc963fb09` (pinned source inspected 2026-10-21).
- Generation namespace: `agento11y.v1`; evaluation namespace: `sigil.v1`.
  The backend retains legacy generation gRPC aliases; HTTP routes are unchanged.
- Files: `generation_ingest.proto` (Generation + WorkflowStep + their Export RPCs),
  `evaluation_ingest.proto` (ScoreItem + ExportScores RPC).

## Original upstream sha256 (UpstreamOriginalSHA256)

- generation_ingest.proto: `35f3eb3f5943ce0fa89edf063961972b30ff32eb85798ded2d60f5e35ec5b5f4`
- evaluation_ingest.proto: `ffa86baf11ef3c9dc336396eba6256e5fbc3b787702587e5c435c4706af00837`

## De-vendor edits (wire-neutral)

- `option go_package` re-homed to `github.com/rknightion/synthkit/internal/sink/sigil/v1;sigilv1`.
- Nothing else changed: field numbers, types, enum values, and import lines
  (`google/protobuf/struct.proto`, `google/protobuf/timestamp.proto`) are byte-identical.

## Toolchain

- `protoc` (libprotoc) 36.2
- `protoc-gen-go` v1.36.11 (matches `google.golang.org/protobuf` in go.mod)

## Regeneration

```sh
just sigil-proto   # requires protoc + protoc-gen-go on PATH
```

The service definitions in the `.proto` files (`GenerationIngestService`,
`WorkflowStepIngestService`, `ScoreIngestService`) are intentionally NOT generated as Go stubs —
`protoc-gen-go` alone emits only messages/enums. Do not add `protoc-gen-go-grpc`.

## Drift

Sigil is a private repo with no public release URL, so there is no network drift-check (unlike
`rw-proto-check`). Re-vendor manually from a newer `grafana/sigil` checkout if the ingest contract
changes; update the commit + sha256 above.
