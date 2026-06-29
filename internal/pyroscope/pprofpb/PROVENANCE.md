# pprof Profile proto provenance

- Upstream: github.com/grafana/pyroscope api/google/v1/profile.proto
- Tag: weekly-f175-5e935f850
- Commit: 5e935f85088d6eb267d458890d637f86913eb77f
- UpstreamOriginalSHA256: 06472c503fb99cc947e7db0a5b7ed22930b1f905e5a5a150aad4d1a7264fb4d6
- VendoredSHA256: 097a32613b6e44441887a9c199ea8741693590980bdfe8ddcb3e7216e6f3a696
- Transformation: set `option go_package = "synthkit/internal/pyroscope/pprofpb;pprofpb";`. Added a comment header noting the vendored provenance. All message field numbers, types, and wire contract are identical to upstream. No gogoproto or other external options were present in the original — no stripping required.
- Toolchain: protoc libprotoc 35.0, protoc-gen-go v1.36.11
- Install (regen only): `go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11`
- Regenerate: `make pyroscope-proto`
- Spec: docs/superpowers/specs/2026-06-15-pyroscope-profiling-signal-type-design.md
