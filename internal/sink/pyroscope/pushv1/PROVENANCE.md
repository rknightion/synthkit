# push.v1 proto provenance

- Upstream: github.com/grafana/pyroscope api/push/v1/push.proto (PushRequest, RawProfileSeries, RawSample); LabelPair co-located from api/types/v1/types.proto
- Tag: weekly-f175-5e935f850
- Commit: 5e935f85088d6eb267d458890d637f86913eb77f
- UpstreamOriginalSHA256: 4a41481181f4b17aea9a3fabf847e6e8f3ae18512b72102bbb490b9a9764fc0b (push.proto)
- VendoredSHA256: 217d4abab151f282483561e3f8830dd53f9dcef787f3f744191ba3a5cb0be148
- Transformation: (1) stripped `import "gnostic/openapi/v3/annotations.proto"` and all `(gnostic.openapi.v3.*)` field options from push.proto and types.proto (documentation-only, do not affect wire bytes); (2) co-located `LabelPair` message from api/types/v1/types.proto into this file, resolving `types.v1.LabelPair` references to local `LabelPair` (field numbers unchanged: name=1, value=2); (3) dropped `annotations` field (field 3) from RawProfileSeries — the upstream comment states annotations are dropped by distributors and not used in the push API — reserved field number 3 for wire compatibility; (4) dropped `service PusherService` and `message PushResponse` — we hand-POST via HTTP, no generated gRPC client needed; (5) set `option go_package = "synthkit/internal/sink/pyroscope/pushv1;pushv1";`.
- Toolchain: protoc libprotoc 35.0, protoc-gen-go v1.36.11
- Install (regen only): `go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11`
- Regenerate: `make pyroscope-proto`
- Spec: docs/superpowers/specs/2026-06-15-pyroscope-profiling-signal-type-design.md
