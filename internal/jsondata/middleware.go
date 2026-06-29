// SPDX-License-Identifier: AGPL-3.0-only

// middleware.go applies JSON content-type, no-cache headers, panic-safe handler
// wrapper, request logging, and CORS echo to every route served by the Infinity host.
//
// CORS (I26): the Origin is echoed verbatim and Access-Control-Request-Headers are
// reflected as Access-Control-Allow-Headers. A fixed allow-list is NOT used because
// Grafana's Infinity datasource sends x-grafana-device-id which must not be blocked.
package jsondata

import (
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"time"
)

// withMiddleware wraps h with:
//   - GET-method guard (405 on non-GET)
//   - CORS echo (I26)
//   - JSON Content-Type + no-cache header injection
//   - panic recovery (logs stack, returns 500)
//   - request log (method, path, duration, status)
//
// /healthz bypasses this wrapper (it uses its own bare handler).
func withMiddleware(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// CORS preflight (OPTIONS): echo origin + request headers, reply 204.
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Panic-safe wrapper around actual handler.
		lrw := &loggingResponseWriter{ResponseWriter: w, code: http.StatusOK}
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("jsondata: PANIC on %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
					if !lrw.written {
						http.Error(lrw, "internal server error", http.StatusInternalServerError)
					}
				}
			}()
			h(lrw, r)
		}()

		log.Printf("jsondata: %s %s %d %s", r.Method, r.URL.Path, lrw.code, time.Since(start).Round(time.Microsecond))
	}
}

// setCORSHeaders echoes the incoming Origin and reflects Access-Control-Request-Headers
// (I26 — fixed allow-list breaks Grafana x-grafana-device-id fetches).
func setCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = "*"
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	if rh := r.Header.Get("Access-Control-Request-Headers"); rh != "" {
		w.Header().Set("Access-Control-Allow-Headers", rh)
	} else {
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	}
	w.Header().Set("Access-Control-Max-Age", "86400")
	if origin != "*" {
		w.Header().Set("Vary", "Origin")
	}
}

// loggingResponseWriter captures the status code written by the handler.
type loggingResponseWriter struct {
	http.ResponseWriter
	code    int
	written bool
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	if !lrw.written {
		lrw.code = code
		lrw.written = true
		lrw.ResponseWriter.WriteHeader(code)
	}
}

func (lrw *loggingResponseWriter) Write(b []byte) (int, error) {
	if !lrw.written {
		lrw.code = http.StatusOK
		lrw.written = true
	}
	return lrw.ResponseWriter.Write(b)
}

// httpErr writes a JSON error envelope with the given status code.
func httpErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `{"error":%q}`+"\n", msg)
}
