// SPDX-License-Identifier: AGPL-3.0-only

package host

import (
	"strings"
	"testing"
)

// TestDockerHashNonRepeating asserts the container id hash is a proper 64-char
// hex string with no 16-char period (regression: the old impl only varied the
// low 4 bits keyed by i mod 16, producing a 16-hex block repeated 4x). It must be
// deterministic across calls and differ by name.
func TestDockerHashNonRepeating(t *testing.T) {
	const seed = "bp:host:camden"
	h := dockerHash(seed, "nginx")

	if len(h) != 64 {
		t.Fatalf("dockerHash length = %d, want 64", len(h))
	}
	if strings.Trim(h, "0123456789abcdef") != "" {
		t.Errorf("dockerHash contains non-hex chars: %q", h)
	}

	// No 16-char period: each consecutive 16-char block must not all be equal.
	b0, b1, b2, b3 := h[0:16], h[16:32], h[32:48], h[48:64]
	if b0 == b1 && b1 == b2 && b2 == b3 {
		t.Errorf("dockerHash is a 16-char block repeated 4x (period-16 bug): %q", h)
	}
	// Stronger: first 16 must differ from the next 16.
	if b0 == b1 {
		t.Errorf("dockerHash first 16 chars == next 16 chars (period-16): %q", h)
	}

	// Deterministic across calls.
	if h2 := dockerHash(seed, "nginx"); h2 != h {
		t.Errorf("dockerHash not deterministic: %q != %q", h, h2)
	}

	// Differs by name.
	if other := dockerHash(seed, "postgres"); other == h {
		t.Errorf("dockerHash(seed, nginx) == dockerHash(seed, postgres): %q", h)
	}
	// Differs by seed.
	if other := dockerHash("bp:host:host-b", "nginx"); other == h {
		t.Errorf("dockerHash insensitive to seed: %q", h)
	}
}
