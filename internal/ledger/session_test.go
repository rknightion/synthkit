// SPDX-License-Identifier: AGPL-3.0-only

package ledger

import (
	"regexp"
	"testing"
)

// uuidV4Shape matches the canonical 8-4-4-4-12 form with the version nibble pinned to 4 and the
// variant nibble in [89ab] — the shape the Faro collector requires of X-Faro-Session-Id.
var uuidV4Shape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestSessionIDForIsStableWithinABucket(t *testing.T) {
	// The whole point: every request in one window shares a session, so a session spans many
	// page-views instead of being reborn per request.
	a := SessionIDFor("fin-digital-banking", 12345)
	b := SessionIDFor("fin-digital-banking", 12345)
	if a != b {
		t.Fatalf("same (scope,bucket) produced different ids: %q vs %q", a, b)
	}
}

func TestSessionIDForRotatesWithTheBucket(t *testing.T) {
	prev := SessionIDFor("fin-digital-banking", 0)
	seen := map[string]bool{prev: true}
	for bucket := int64(1); bucket < 200; bucket++ {
		id := SessionIDFor("fin-digital-banking", bucket)
		if id == prev {
			t.Fatalf("bucket %d did not rotate the session id (%q)", bucket, id)
		}
		if seen[id] {
			t.Fatalf("bucket %d reused an earlier session id %q", bucket, id)
		}
		seen[id] = true
		prev = id
	}
}

func TestSessionIDForSeparatesScopes(t *testing.T) {
	// Two workloads in the same window must not share a browser session.
	if a, b := SessionIDFor("fin-digital-banking", 7), SessionIDFor("fin-trading-platform", 7); a == b {
		t.Fatalf("different scopes collided at the same bucket: %q", a)
	}
}

func TestSessionIDForIsUUIDShaped(t *testing.T) {
	for _, bucket := range []int64{0, 1, 42, -1, 1 << 40} {
		if id := SessionIDFor("scope", bucket); !uuidV4Shape.MatchString(id) {
			t.Errorf("bucket %d: %q is not v4-shaped (Faro would reject it)", bucket, id)
		}
	}
}
