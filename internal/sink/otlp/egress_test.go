// SPDX-License-Identifier: AGPL-3.0-only
package otlp

import (
	"context"
	"errors"
	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/sink/httpretry"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHAAttemptAllSharedEgressRedirectAndFence(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1); w.WriteHeader(200) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer source.Close()
	for _, kind := range []string{"traces", "metrics", "logs"} {
		t.Run(kind, func(t *testing.T) {
			g := ha.NewGate()
			_ = g.Activate(context.Background(), func(context.Context) error { return nil })
			d := httpretry.Delivery{Gate: g, HTTPTimeout: time.Second, RetryMaxElapsed: time.Millisecond}
			var e *egress
			switch kind {
			case "traces":
				s := New(source.URL, "user", "token", false)
				s.SetDelivery(d)
				e = &s.eg
			case "metrics":
				s := NewMetrics(source.URL, "user", "token", false)
				s.SetDelivery(d)
				e = &s.eg
			case "logs":
				s := NewLogs(source.URL, "user", "token", false)
				s.SetDelivery(d)
				e = &s.eg
			}
			if err := e.post(context.Background(), nil, 1, "", nil); err == nil {
				t.Fatal("redirect acknowledged as delivery")
			}
			if redirected.Load() != 0 {
				t.Fatal("ungated redirect followed")
			}
			g.Revoke()
			if err := e.post(context.Background(), nil, 1, "", nil); !errors.Is(err, ha.ErrNotLeader) {
				t.Fatal("terminal fence identity lost", err)
			}
		})
	}
}
