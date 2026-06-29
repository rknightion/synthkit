// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSPAHandlerServesIndexAndClientRouteFallback(t *testing.T) {
	h := spaHandler()
	for _, path := range []string{"/control/ui/", "/control/ui/incidents", "/control/ui/bp/foo"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200 (no redirect)", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("%s: content-type %q, want text/html", path, ct)
		}
	}
}
