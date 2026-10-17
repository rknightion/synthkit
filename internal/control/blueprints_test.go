// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// Synthetic Monitoring admission state failures carry their own 503 codes, not
// state_persist_failed, so an operator can tell a pending provisioner from a failed control write.
func TestLiveErrorMapsSMStateFailures(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{fmt.Errorf("%w: blueprint %q: provisioning pending", ErrLiveSMBusy, "ns/x"), "sm_state_busy"},
		{fmt.Errorf("%w: blueprint %q: load failed", ErrLiveSMUnavailable, "ns/x"), "sm_state_unavailable"},
	} {
		rec := httptest.NewRecorder()
		liveError(rec, tc.err)
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%v: body %q: %v", tc.err, rec.Body.String(), err)
		}
		if rec.Code != http.StatusServiceUnavailable || body["code"] != tc.code || !strings.Contains(body["reason"], "ns/x") {
			t.Fatalf("%v: got %d %v, want 503 code %s naming the blueprint", tc.err, rec.Code, body, tc.code)
		}
	}
}
