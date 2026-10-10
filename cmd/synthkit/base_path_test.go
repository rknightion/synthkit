// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/ha"
)

// Exercise the lease-mode composition root, not just a manually configured
// control handler. The ordinary-mode composition is exercised by the binary.
func TestHABasePathMounts(t *testing.T) {
	for _, prefix := range []string{"/x/y", "/control", ""} {
		t.Run(prefix, func(t *testing.T) {
			t.Setenv("CONTROL_BASE_PATH", prefix)
			view, _ := readinessHAView(t, ha.NewGate())
			for _, mount := range []string{prefix + "/control/", "/control/"} {
				redirect := httptest.NewRecorder()
				view.handler.ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, mount+"ui", nil))
				if redirect.Code != http.StatusFound || redirect.Header().Get("Location") != prefix+"/control/ui/" {
					t.Fatalf("mount %q redirect: %d %q", mount, redirect.Code, redirect.Header().Get("Location"))
				}
				page := httptest.NewRecorder()
				view.handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, mount+"ui/config", nil))
				if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `<base href="`+prefix+`/control/ui/">`) {
					t.Fatalf("mount %q did not serve the configured console: %d", mount, page.Code)
				}
			}
		})
	}
}
