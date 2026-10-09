// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"net"
	"net/http"
	"sync"
)

// haHTTPDrain closes connections that have no active handler before joining the
// HTTP server. net/http.Shutdown otherwise gives StateNew connections a grace
// period to send their first headers; that grace can outlive the global drain
// budget. An incomplete request must not prevent a positively joined handoff.
// Active handlers still join through Shutdown, and mutations retain gate admission.
type haHTTPDrain struct {
	mu       sync.Mutex
	draining bool
	idle     map[net.Conn]struct{}
}

func (d *haHTTPDrain) connState(c net.Conn, state http.ConnState) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch state {
	case http.StateNew, http.StateIdle:
		if d.draining {
			_ = c.Close()
			return
		}
		if d.idle == nil {
			d.idle = make(map[net.Conn]struct{})
		}
		d.idle[c] = struct{}{}
	case http.StateActive, http.StateHijacked, http.StateClosed:
		delete(d.idle, c)
	}
}

func (d *haHTTPDrain) shutdown(ctx context.Context, srv *http.Server) error {
	d.mu.Lock()
	d.draining = true
	for c := range d.idle {
		_ = c.Close()
		delete(d.idle, c)
	}
	d.mu.Unlock()
	// Connections accepted concurrently with shutdown also pass through connState.
	// Never use Server.Close here: it would drop active handlers rather than join.
	return srv.Shutdown(ctx)
}
