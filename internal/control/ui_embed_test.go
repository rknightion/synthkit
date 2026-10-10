// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"encoding/json"
	"github.com/rknightion/synthkit/internal/config"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"

	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestControlBasePathConfig(t *testing.T) {
	for _, prefix := range []string{"", "/x/y", "/synth-kit_v1.0~"} {
		if err := config.ValidateControlBasePath(prefix); err != nil {
			t.Errorf("valid %q: %v", prefix, err)
		}
	}
	for _, prefix := range []string{"/", "x/y", "//evil", "/x/", "/x//y", "/x/../y", "/./x", "/x?y", "/x#y", "/%2f", "/x\\y", "https://evil", "/x\"", "/x\n"} {
		if err := config.ValidateControlBasePath(prefix); err == nil {
			t.Errorf("accepted unsafe prefix %q", prefix)
		}
	}
	path := filepath.Join(t.TempDir(), "config.env")
	if err := os.WriteFile(path, []byte("CONTROL_BASE_PATH=/x/y\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil || cfg.ControlBasePath != "/x/y" {
		t.Fatalf("env file config: %v %v", cfg, err)
	}
	t.Setenv("CONTROL_BASE_PATH", "/trusted")
	cfg, err = config.Load(path)
	if err != nil || cfg.ControlBasePath != "/trusted" {
		t.Fatalf("env override config: %v %v", cfg, err)
	}
	t.Setenv("CONTROL_BASE_PATH", "//evil")
	if _, err = config.Load(path); err == nil {
		t.Fatal("invalid prefix did not fail config loading")
	}
}

func TestBasePathIgnoresForwardedHeaders(t *testing.T) {
	for _, prefix := range []string{"/trusted", "/control"} {
		h := NewHandler(NewStore(""), nil, "").SetBasePath(prefix)
		for _, path := range []string{prefix + "/control/ui", "/control/ui"} {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.Header.Set("X-Forwarded-Prefix", "//evil")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 302 || w.Header().Get("Location") != prefix+"/control/ui/" {
				t.Fatalf("untrusted redirect: %d %s", w.Code, w.Header().Get("Location"))
			}
		}
	}
}

// TestRuntimeBasePath uses a real local HTTP server, including redirects and
// every build-generated asset reference, not only a handler recorder.
func TestRuntimeBasePath(t *testing.T) {
	h := NewHandler(NewStore(""), nil, "")
	setter, ok := any(h).(interface{ SetBasePath(string) *Handler })
	if !ok {
		t.Fatal("handler lacks runtime base-path configuration")
	}
	assetRE := regexp.MustCompile(`(?:src|href)="([^"]+)"`)
	for _, prefix := range []string{"/x/y", "/control", ""} {
		setter.SetBasePath(prefix)
		srv := httptest.NewServer(h)
		client := srv.Client()
		client.Timeout = 5 * time.Second
		res, err := client.Get(srv.URL + prefix + "/control/ui")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		base := prefix + "/control/ui/"
		if res.Request.URL.Path != base || !strings.Contains(string(body), `<base href="`+base+`">`) ||
			!strings.Contains(string(body), `<meta name="control-api-prefix" content="`+prefix+`/control/">`) {
			t.Fatalf("missing runtime URLs or redirect: %s %s", res.Request.URL, body)
		}
		baseURL, _ := url.Parse(srv.URL + base)
		assets := 0
		for _, match := range assetRE.FindAllStringSubmatch(string(body), -1) {
			if !strings.Contains(match[1], "assets/") {
				continue
			}
			assets++
			ref, _ := url.Parse(match[1])
			u := baseURL.ResolveReference(ref)
			if !strings.HasPrefix(u.Path, base+"assets/") {
				t.Fatalf("asset escapes prefix: %s", u)
			}
			a, err := client.Get(u.String())
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(a.Body)
			a.Body.Close()
			if a.StatusCode != 200 || len(data) == 0 || strings.HasPrefix(a.Header.Get("Content-Type"), "text/html") {
				t.Fatalf("asset not served: %s", u)
			}
		}
		// Clean Go checkouts have only dist/.gitkeep. The build-aware proof is
		// mandatory whenever an index exists, and the fallback still has runtime URLs.
		if !strings.Contains(string(body), "UI assets not built") && assets == 0 {
			t.Fatal("build has no asset references")
		}
		state, err := client.Get(srv.URL + prefix + "/control/state")
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(state.Body)
		state.Body.Close()
		var snapshot State
		if err := json.Unmarshal(data, &snapshot); err != nil {
			t.Fatal(err)
		}
		if state.StatusCode != 200 || snapshot.VolumeMultiplier != 1 {
			t.Fatalf("API shape changed: %s", data)
		}
		srv.Close()
	}
}

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
