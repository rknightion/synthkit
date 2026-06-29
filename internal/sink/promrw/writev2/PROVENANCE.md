# RW2 proto provenance

- Upstream: https://github.com/prometheus/prometheus/blob/v3.12.0/prompb/io/prometheus/write/v2/types.proto
- Tag: v3.12.0
- Commit: 08f9ec4de871ac1a662dd5401daadab1907c2f5e
- UpstreamOriginalSHA256: a34d320a205d86b18000231644d144a899f9b9031ce472cbe62e5dba1bc756d1
- VendoredDegogodSHA256: 1d1167389ed90cff530249e3196fd7381120f49d09584b1e5358ee4791cd642e
- Transformation: removed `import "gogoproto/gogo.proto";` and all `[(gogoproto.*) = ...]` field options (they affect only generated-Go ergonomics, NOT wire bytes); set `option go_package = "github.com/rknightion/synthkit/internal/sink/promrw/writev2;writev2";`. All field numbers and types copied verbatim — the wire contract is unchanged.
- Toolchain: protoc libprotoc 35.0, protoc-gen-go v1.36.11
- Install (regen only): `go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11`
- Regenerate: `make proto`
- Spec: https://prometheus.io/docs/concepts/remote_write_spec_2_0/

## Wire-contract notes

- `TimeSeries.field 6` is `reserved 6` in the upstream (field 6 was used in an experimental period and is now reserved for backward compatibility). There is NO `created_timestamp` field on TimeSeries in v3.12.0. The plan spec mentions `created_timestamp` as a future/deferred addition (Part C).
- `Sample` has three fields: `value=1`, `timestamp=2`, `start_timestamp=3`. The `start_timestamp` field carries the counter/histogram created-time at the sample level in this version.
- `Histogram` carries `start_timestamp=17` for the same purpose at the native-histogram level.
