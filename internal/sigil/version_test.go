// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// expectedVersion replicates the EffectiveVersion computation so tests are self-checking.
func expectedVersion(systemPrompt string) string {
	type envelope struct {
		CanonicalVersion int    `json:"canonical_version"`
		SystemPrompt     string `json:"system_prompt"`
	}
	b, err := json.Marshal(envelope{CanonicalVersion: 3, SystemPrompt: systemPrompt})
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestEffectiveVersion(t *testing.T) {
	const prompt = "you are a helpful assistant"
	got := EffectiveVersion(prompt)

	// Must have the "sha256:" prefix and a 64-hex digest.
	if !strings.HasPrefix(got, "sha256:") {
		t.Fatalf("missing sha256: prefix: %q", got)
	}
	const wantLen = len("sha256:") + 64
	if len(got) != wantLen {
		t.Fatalf("want len %d, got %d: %q", wantLen, len(got), got)
	}
	hexPart := got[len("sha256:"):]
	for _, c := range hexPart {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("non-hex char %q in digest: %q", c, got)
		}
	}

	// Must be deterministic.
	if got2 := EffectiveVersion(prompt); got != got2 {
		t.Fatalf("not deterministic: %q vs %q", got, got2)
	}

	// Must differ for a different prompt.
	if got == EffectiveVersion("different prompt") {
		t.Fatal("collision: different prompts produced the same version")
	}

	// Must match the hand-computed expected digest.
	want := expectedVersion(prompt)
	if got != want {
		t.Fatalf("digest mismatch\n  got  %q\n  want %q", got, want)
	}
}

func TestEffectiveVersionKnownDigest(t *testing.T) {
	// Fix a specific input and verify the exact expected output so we catch any silent
	// algorithm change (e.g. struct field reorder, key rename, canonical_version bump).
	const fixedPrompt = "You are a coding assistant."
	got := EffectiveVersion(fixedPrompt)
	want := expectedVersion(fixedPrompt)
	if got != want {
		t.Fatalf("known-digest mismatch\n  got  %q\n  want %q", got, want)
	}
	// The prefix must be present regardless of which exact digest it is.
	if !strings.HasPrefix(got, "sha256:") || len(got) != len("sha256:")+64 {
		t.Fatalf("bad form: %q", got)
	}
}

func TestEffectiveVersionEmptyPrompt(t *testing.T) {
	got := EffectiveVersion("")
	if !strings.HasPrefix(got, "sha256:") || len(got) != len("sha256:")+64 {
		t.Fatalf("bad form for empty prompt: %q", got)
	}
	// Empty and non-empty must differ.
	if got == EffectiveVersion("nonempty") {
		t.Fatal("empty and non-empty prompt collide")
	}
}
