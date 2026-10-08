// SPDX-License-Identifier: AGPL-3.0-only
package fleet

import (
	"context"
	"errors"
	"fmt"
	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/sink/httpretry"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHAEveryFleetOperationRedirectAndFence(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1); w.WriteHeader(200) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer source.Close()
	g := ha.NewGate()
	_ = g.Activate(context.Background(), func(context.Context) error { return nil })
	c := NewClient(source.URL, "user", "token")
	c.SetDelivery(httpretry.Delivery{Gate: g, HTTPTimeout: time.Second}, ha.Bounded{Gate: g, Timeout: time.Second, Margin: time.Second, Crash: func() { t.Error("unexpected watchdog") }})
	col := Collector{ID: "collector"}
	operations := []func() error{func() error { return c.RegisterCollector(context.Background(), col) }, func() error { _, err := c.GetConfig(context.Background(), col); return err }, func() error { return c.UnregisterCollector(context.Background(), col.ID) }}
	for _, op := range operations {
		if err := op(); err == nil {
			t.Fatal("redirect credited as successful Fleet operation")
		}
	}
	if redirected.Load() != 0 {
		t.Fatal("Fleet followed ungated redirect")
	}
	g.Revoke()
	for _, op := range operations {
		if err := op(); !errors.Is(err, ha.ErrNotLeader) {
			t.Fatal("Fleet fence identity lost", err)
		}
	}
}
func TestHAFleetAggregateCleanupSharesDeadlineAndJoins(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		select {
		case <-time.After(20 * time.Millisecond):
			w.WriteHeader(200)
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	g := ha.NewGate()
	_ = g.Activate(context.Background(), func(context.Context) error { return nil })
	c := NewController(Config{FMURL: srv.URL, Token: "token", Delivery: httpretry.Delivery{Gate: g, HTTPTimeout: time.Second}, Bounded: ha.Bounded{Gate: g, Timeout: 50 * time.Millisecond, Margin: 100 * time.Millisecond, Crash: func() { t.Error("unjoined cleanup") }}})
	for i := 0; i < 10; i++ {
		c.registered[fmt.Sprint(i)] = true
	}
	if err := c.Cleanup(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("aggregate cleanup restarted individual deadlines", err)
	}
	n := requests.Load()
	time.Sleep(30 * time.Millisecond)
	if n < 1 || n >= 10 || requests.Load() != n {
		t.Fatalf("aggregate cleanup detached workers: before=%d after=%d", n, requests.Load())
	}
	if err := g.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}
