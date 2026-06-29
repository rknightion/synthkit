// SPDX-License-Identifier: AGPL-3.0-only

package promrw

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

// TestRemoteWriteVersionPinned freezes the protocol-version surface we emit. If the spec
// version or content-type message name changes, this fails loudly so we make a conscious
// decision (the "remain aligned" gate).
func TestRemoteWriteVersionPinned(t *testing.T) {
	if RemoteWriteVersion != "2.0.0" {
		t.Fatalf("RemoteWriteVersion drifted to %q — confirm against the RW spec before changing", RemoteWriteVersion)
	}
	if ContentTypeRW2 != "application/x-protobuf;proto=io.prometheus.write.v2.Request" {
		t.Fatalf("ContentTypeRW2 drifted to %q", ContentTypeRW2)
	}
}

// TestVendoredProtoUnchanged detects accidental local edits to the vendored proto. The
// recorded hash MUST match PROVENANCE.md VendoredDegogodSHA256. Hermetic (no network) —
// this catches LOCAL drift only; detecting a NEW upstream release is `make rw-proto-check`.
func TestVendoredProtoUnchanged(t *testing.T) {
	const wantSHA = "1d1167389ed90cff530249e3196fd7381120f49d09584b1e5358ee4791cd642e"
	b, err := os.ReadFile("writev2/types.proto") // path is relative to the package dir (where go test runs)
	if err != nil {
		t.Fatalf("read vendored proto: %v", err)
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != wantSHA {
		t.Fatalf("vendored proto changed without updating wantSHA/PROVENANCE.md (got %s)", got)
	}
}
