// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSourceViewNoToken asserts that SourceView never serializes a raw token field —
// only the env-var NAME (token_env_var) is present in the wire shape.
func TestSourceViewNoToken(t *testing.T) {
	b, err := json.Marshal(SourceView{ID: "s1", TokenEnvVar: "GIT_TOKEN"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	// "token" appears inside "token_env_var" — check with word-boundary logic: a standalone
	// "token" key would be surrounded by quotes and a colon, e.g. `"token":`.
	if strings.Contains(s, `"token":`) {
		t.Fatalf("SourceView must never serialize a raw token field; got: %s", s)
	}
	if !strings.Contains(s, "token_env_var") {
		t.Fatalf("expected token_env_var in serialized SourceView; got: %s", s)
	}
}

// TestSetBlueprintAdminChains verifies SetBlueprintAdmin returns *Handler (compile + chain check).
func TestSetBlueprintAdminChains(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	h := NewHandler(store, nil, "")
	// SetBlueprintAdmin must accept a nil BlueprintAdmin (nil interface value) and return *Handler.
	got := h.SetBlueprintAdmin(nil)
	if got != h {
		t.Fatal("SetBlueprintAdmin must return the same *Handler for chaining")
	}
}
