// SPDX-License-Identifier: AGPL-3.0-only

package synthkit

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/chatsim/engine"
)

// A loopback httptest control plane whose active_scenarios the test flips.
func TestRisingAndFallingEdges(t *testing.T) {
	var mu sync.Mutex
	states := []string{
		`{"active_scenarios":["bp/other"],"volume_multiplier":1}`, // baseline: inactive
		`{"active_scenarios":["bp/other","bp/storm"]}`,            // rising edge
		`{"active_scenarios":["bp/storm"]}`,                       // unchanged
		`{"active_scenarios":[]}`,                                 // falling edge
	}
	i := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "control" || pass != "tok" || r.URL.Path != "/control/state" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mu.Lock()
		s := states[min(i, len(states)-1)]
		i++
		mu.Unlock()
		_, _ = w.Write([]byte(s))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := make(chan engine.Event, 4)
	p := &Poller{BaseURL: srv.URL, Token: "tok", Scenario: "bp/storm", Interval: 10 * time.Millisecond}
	go func() { _ = p.Run(ctx, out) }()
	for _, want := range []engine.EventKind{engine.EvSynthkitActivated, engine.EvSynthkitDeactivated} {
		select {
		case ev := <-out:
			if ev.Kind != want {
				t.Fatalf("got %s, want %s", ev.Kind, want)
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", want)
		}
	}
}

func TestAlreadyActiveAtBaselineDoesNotFire(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"active_scenarios":["bp/storm"]}`))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	out := make(chan engine.Event, 4)
	p := &Poller{BaseURL: srv.URL, Scenario: "bp/storm", Interval: 10 * time.Millisecond}
	_ = p.Run(ctx, out)
	if len(out) != 0 {
		t.Fatalf("an already-active scenario fired %d events", len(out))
	}
}

func TestSetActivePostsTheScenarioMutation(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+user+":"+pass+" "+string(body))
		if strings.HasSuffix(r.URL.Path, "/deactivate") {
			http.Error(w, "unknown scenario", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	p := &Poller{BaseURL: srv.URL + "/", Token: "tok", Scenario: "bp/storm"}
	if err := p.SetActive(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := p.SetActive(context.Background(), false); err == nil || !strings.Contains(err.Error(), "HTTP 400: unknown scenario") {
		t.Fatalf("want the 400 body in the error, got %v", err)
	}
	want := []string{
		`POST /control/scenarios/activate control:tok {"scenario":"bp/storm"}`,
		`POST /control/scenarios/deactivate control:tok {"scenario":"bp/storm"}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
