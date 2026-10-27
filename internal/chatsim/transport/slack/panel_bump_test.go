// SPDX-License-Identifier: AGPL-3.0-only

package slack

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack"

	"github.com/rknightion/synthkit/internal/chatsim/engine"
)

// wireLog records each Web API call a real client makes against a test server.
type wireLog struct {
	mu           sync.Mutex
	calls        []string
	n            int
	refuseDelete bool
}

func (w *wireLog) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.mu.Lock()
		defer w.mu.Unlock()
		method := strings.TrimPrefix(r.URL.Path, "/")
		switch method {
		case "chat.postMessage":
			w.n++
			ts := fmt.Sprintf("%d.0", w.n)
			kind := "notice"
			if strings.Contains(r.PostForm.Get("blocks"), PanelBlockID) {
				kind = "panel"
			}
			w.calls = append(w.calls, fmt.Sprintf("post %s %s", kind, ts))
			_, _ = fmt.Fprintf(rw, `{"ok":true,"channel":"D1","ts":%q}`, ts)
			return
		case "chat.delete":
			if w.refuseDelete {
				w.calls = append(w.calls, "chat.delete refused "+r.PostForm.Get("ts"))
				_, _ = rw.Write([]byte(`{"ok":false,"error":"cant_delete_message"}`))
				return
			}
			w.calls = append(w.calls, "chat.delete "+r.PostForm.Get("ts"))
		case "pins.add", "pins.remove":
			ts := r.PostForm.Get("ts")
			if ts == "" {
				ts = r.PostForm.Get("timestamp")
			}
			w.calls = append(w.calls, method+" "+ts)
		case "chat.update":
			w.calls = append(w.calls, "update "+r.PostForm.Get("ts"))
		case "pins.list":
			_, _ = rw.Write([]byte(`{"ok":true,"items":[]}`))
			return
		default:
			w.calls = append(w.calls, "other "+method)
		}
		_, _ = rw.Write([]byte(`{"ok":true,"channel":"D1","ts":"0.0"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNoticeMovesThePanelBelowIt(t *testing.T) {
	var w wireLog
	srv := w.server(t)
	tr := &Transport{api: slack.New("x", slack.OptionAPIURL(srv.URL+"/")), dm: "D1", cfg: Config{Logf: t.Logf}, bumpDelay: 20 * time.Millisecond}
	ctx := context.Background()
	snap := engine.Snapshot{Scenario: "demo", State: engine.StateWaiting}

	if err := tr.Panel(ctx, snap); err != nil {
		t.Fatal(err)
	}
	// A burst of notices moves the panel once, below the last of them.
	for _, n := range []string{"Beat x open.", "Beat y open."} {
		if err := tr.Notify(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	tr.bumpWG.Wait()
	snap.Fired = true
	if err := tr.Panel(ctx, snap); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"post panel 1.0", "pins.add 1.0", // first panel
		"post notice 2.0", "post notice 3.0", // the notices land below it
		"post panel 4.0", "pins.add 4.0", "chat.delete 1.0", // the panel moves under them, once
		"update 4.0", // later state edits the moved panel, not the deleted one
	}
	if got := strings.Join(w.calls, " | "); got != strings.Join(want, " | ") {
		t.Fatalf("calls\n got %s\nwant %s", got, strings.Join(want, " | "))
	}
}

func TestNoticeBeforeAnyPanelPostsOnlyTheNotice(t *testing.T) {
	var w wireLog
	srv := w.server(t)
	tr := &Transport{api: slack.New("x", slack.OptionAPIURL(srv.URL+"/")), dm: "D1", cfg: Config{Logf: t.Logf}}
	if err := tr.Notify(context.Background(), "Ready."); err != nil {
		t.Fatal(err)
	}
	tr.bumpWG.Wait()
	if got := strings.Join(w.calls, " | "); got != "post notice 1.0" {
		t.Fatalf("calls %s", got)
	}
}

func TestPanelThatCannotBeDeletedIsRetired(t *testing.T) {
	w := wireLog{refuseDelete: true}
	srv := w.server(t)
	tr := &Transport{api: slack.New("x", slack.OptionAPIURL(srv.URL+"/")), dm: "D1", cfg: Config{Logf: t.Logf}, bumpDelay: time.Millisecond}
	ctx := context.Background()
	if err := tr.Panel(ctx, engine.Snapshot{Scenario: "demo", State: engine.StateWaiting}); err != nil {
		t.Fatal(err)
	}
	if err := tr.Notify(ctx, "Beat x open."); err != nil {
		t.Fatal(err)
	}
	tr.bumpWG.Wait()
	want := "post panel 1.0 | pins.add 1.0 | post notice 2.0 | post panel 3.0 | pins.add 3.0 | chat.delete refused 1.0 | pins.remove 1.0 | update 1.0"
	if got := strings.Join(w.calls, " | "); got != want {
		t.Fatalf("calls\n got %s\nwant %s", got, want)
	}
}
