// SPDX-License-Identifier: AGPL-3.0-only
package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestStartupProbeFailClosedAndPublic(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	mutations := 0
	h := NewHandler(store, func(State) { mutations++ }, "private-token")
	for _, tc := range []struct {
		name    string
		fn      func() bool
		want    int
		started bool
	}{
		{"unconfigured", nil, 503, false},
		{"incomplete", func() bool { return false }, 503, false},
		{"complete", func() bool { return true }, 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.SetStartup(tc.fn)
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/control/startup", nil))
			var payload map[string]bool
			err := json.Unmarshal(response.Body.Bytes(), &payload)
			if response.Code != tc.want || err != nil || len(payload) != 1 || payload["started"] != tc.started {
				t.Fatalf("startup: HTTP %d %q (%v)", response.Code, response.Body.String(), err)
			}
		})
	}
	post := httptest.NewRecorder()
	h.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/control/startup", nil))
	if post.Code != http.StatusMethodNotAllowed || mutations != 0 {
		t.Fatalf("startup accepted mutation: HTTP %d, mutations=%d", post.Code, mutations)
	}
	protected := httptest.NewRecorder()
	h.ServeHTTP(protected, httptest.NewRequest(http.MethodGet, "/control/state", nil))
	if protected.Code != http.StatusUnauthorized {
		t.Fatalf("startup public exception exposed protected state: HTTP %d", protected.Code)
	}
}
