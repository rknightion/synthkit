// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"embed"
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
	sub, err := fs.Sub(uiDist, "ui/dist")
	if err != nil {
		panic(err) // embed guarantees ui/dist exists at build time
	}
	index, idxErr := fs.ReadFile(sub, "index.html")
	notBuilt := []byte(`<!doctype html><meta charset="utf-8"><title>synthkit control plane</title>` +
		`<body style="font-family:system-ui;background:#0b0c14;color:#e8e9f2;padding:40px">` +
		`<h1>synthkit control plane</h1><p>UI assets not built. Run <code>make ui</code> ` +
		`(or rebuild the Docker image).</p></body>`)
	serveIndex := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if idxErr != nil {
			_, _ = w.Write(notBuilt)
			return
		}
		_, _ = w.Write(index)
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
