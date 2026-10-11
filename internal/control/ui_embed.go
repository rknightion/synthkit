// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"bytes"
	"embed"
	"encoding/json"
	"html"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:ui/dist
var uiDist embed.FS

// spaHandler serves the embedded SolidJS build under /control/ui/. Real asset paths are
// served from the embedded FS; everything else (client-side routes) falls back to index.html.
// When no build is present (clean checkout — only dist/.gitkeep), it serves a "not built" page
// so the Go gate stays green without a Node build.
func spaHandler() http.Handler {
	return spaHandlerWithBase(func() string { return "" }, func() map[string]string { return nil })
}

func spaHandlerWithBase(basePath func() string, managedFeatures func() map[string]string) http.Handler {
	sub, err := fs.Sub(uiDist, "ui/dist")
	if err != nil {
		panic(err) // embed guarantees ui/dist exists at build time
	}
	index, idxErr := fs.ReadFile(sub, "index.html")
	notBuilt := []byte(`<!doctype html><html><head><meta charset="utf-8"><title>synthkit control plane</title></head>` +
		`<body style="font-family:system-ui;background:#0b0c14;color:#e8e9f2;padding:40px">` +
		`<h1>synthkit control plane</h1><p>UI assets not built. Run <code>just ui</code> ` +
		`(or rebuild the Docker image).</p></body>`)
	serveIndex := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		page := index
		if idxErr != nil {
			page = notBuilt
		}
		// The head insertion precedes all relative build assets and modulepreloads.
		// HTML-escape even though config validation admits only safe path segments.
		prefix := html.EscapeString(basePath())
		runtime := `<base href="` + prefix + `/control/ui/">` +
			`<meta name="control-api-prefix" content="` + prefix + `/control/">`
		if features := managedFeatures(); len(features) > 0 {
			var encoded bytes.Buffer
			encoder := json.NewEncoder(&encoded)
			encoder.SetEscapeHTML(false) // escape the HTML attribute once, below
			_ = encoder.Encode(features) // string map cannot fail to marshal
			runtime += `<meta name="control-managed-features" content="` + html.EscapeString(strings.TrimSpace(encoded.String())) + `">`
		}
		_, _ = w.Write([]byte(strings.Replace(string(page), "<head>", "<head>"+runtime, 1)))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/control/ui/")
		if p == "" || p == "index.html" {
			serveIndex(w)
			return
		}
		if st, statErr := fs.Stat(sub, p); statErr == nil && !st.IsDir() {
			http.ServeFileFS(w, r, sub, p) // sets content-type from the extension
			return
		}
		serveIndex(w) // SPA fallback for client routes
	})
}
