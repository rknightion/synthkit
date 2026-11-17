// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/operationalerr"
	"github.com/rknightion/synthkit/internal/pushhook"
	nativesigil "github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/httpretry"
)

type ratingCapture struct {
	method, path, rawPath, query string
	header                       http.Header
	body                         []byte
}

// newRatingServer records each request and answers with respond.
func newRatingServer(t *testing.T, respond func(w http.ResponseWriter, r *http.Request, n int)) (*httptest.Server, func() []ratingCapture) {
	t.Helper()
	var mu sync.Mutex
	var captured []ratingCapture
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		captured = append(captured, ratingCapture{r.Method, r.URL.Path, r.URL.RawPath, r.URL.RawQuery, r.Header.Clone(), data})
		n := len(captured)
		mu.Unlock()
		respond(w, r, n)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []ratingCapture {
		mu.Lock()
		defer mu.Unlock()
		return append([]ratingCapture(nil), captured...)
	}
}

func okRating(w http.ResponseWriter, ratingID, conversationID, rating string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"rating":  map[string]any{"rating_id": ratingID, "conversation_id": conversationID, "rating": rating, "created_at": "2026-02-13T12:00:00Z"},
		"summary": map[string]any{"total_count": 1, "good_count": 1, "bad_count": 0, "latest_rating": rating, "latest_rated_at": "2026-02-13T12:00:00Z", "has_bad_rating": false},
	})
}

func newTestRatings(t *testing.T, url string) *Ratings {
	t.Helper()
	r, err := NewRatings(url, "tenant-1", "tok-secret")
	if err != nil {
		t.Fatal(err)
	}
	r.policy = httpretry.Policy{MaxElapsed: 2 * time.Second, InitialDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond, Multiplier: 1, Retryable: r.policy.Retryable}
	return r
}

// TestRatingRequestMatchesSDKTransport pins the request to go/agento11y/rating_test.go at
// SDK 75a1c548 (POST /api/v1/conversations/{id}/ratings, tenant header, the input fields
// as the body) and to the backend route at bc963fb09.
func TestRatingRequestMatchesSDKTransport(t *testing.T) {
	srv, got := newRatingServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		okRating(w, "rat-1", "conv-1", "CONVERSATION_RATING_VALUE_BAD")
	})
	err := newTestRatings(t, srv.URL).WriteConversationRating(context.Background(), " conv-1 ", nativesigil.ConversationRatingInput{
		RatingID: " rat-1 ", Rating: nativesigil.ConversationRatingValueBad, Comment: " wrong answer ",
		Metadata: map[string]any{"channel": "assistant"}, GenerationID: "gen-1", RaterID: "rater-1", Source: "e2e",
	})
	if err != nil {
		t.Fatal(err)
	}
	reqs := got()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	r := reqs[0]
	if r.method != http.MethodPost || r.path != "/api/v1/conversations/conv-1/ratings" {
		t.Fatalf("request = %s %s", r.method, r.path)
	}
	if r.header.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("tenant-1:tok-secret")) || r.header.Get("X-Scope-OrgID") != "tenant-1" || r.header.Get("Content-Type") != "application/json" {
		t.Fatalf("headers = %v", r.header)
	}
	want := `{"rating_id":"rat-1","rating":"CONVERSATION_RATING_VALUE_BAD","comment":"wrong answer","metadata":{"channel":"assistant"},"generation_id":"gen-1","rater_id":"rater-1","source":"e2e"}`
	if !bytes.Equal(canonicalJSON(t, r.body), canonicalJSON(t, []byte(want))) {
		t.Fatalf("body = %s, want %s", r.body, want)
	}
}

func TestRatingBodyOmitsEmptyOptionalFields(t *testing.T) {
	srv, got := newRatingServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		okRating(w, "rat-1", "conv-1", "CONVERSATION_RATING_VALUE_GOOD")
	})
	if err := newTestRatings(t, srv.URL).WriteConversationRating(context.Background(), "conv-1", nativesigil.ConversationRatingInput{RatingID: "rat-1", Rating: nativesigil.ConversationRatingValueGood}); err != nil {
		t.Fatal(err)
	}
	if body := string(got()[0].body); body != `{"rating_id":"rat-1","rating":"CONVERSATION_RATING_VALUE_GOOD"}` {
		t.Fatalf("body = %s; the server decodes with unknown fields disallowed, so only set fields may be sent", body)
	}
}

func TestRatingConversationIDIsPathEscaped(t *testing.T) {
	srv, got := newRatingServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		okRating(w, "rat-1", "conv/with space", "CONVERSATION_RATING_VALUE_GOOD")
	})
	if err := newTestRatings(t, srv.URL).WriteConversationRating(context.Background(), "conv/with space", nativesigil.ConversationRatingInput{RatingID: "rat-1", Rating: nativesigil.ConversationRatingValueGood}); err != nil {
		t.Fatal(err)
	}
	r := got()[0]
	if r.path != "/api/v1/conversations/conv/with space/ratings" || r.rawPath != "/api/v1/conversations/conv%2Fwith%20space/ratings" {
		t.Fatalf("path = %q raw = %q; the id must travel as one escaped segment", r.path, r.rawPath)
	}
}

func TestRatingValidationRefusesBeforeAnyRequest(t *testing.T) {
	srv, got := newRatingServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) { t.Error("request sent for an invalid rating") })
	valid := nativesigil.ConversationRatingInput{RatingID: "rat-1", Rating: nativesigil.ConversationRatingValueGood}
	with := func(f func(*nativesigil.ConversationRatingInput)) nativesigil.ConversationRatingInput {
		in := valid
		f(&in)
		return in
	}
	cases := []struct {
		name string
		conv string
		in   nativesigil.ConversationRatingInput
	}{
		{"blank conversation", "  ", valid},
		{"long conversation", strings.Repeat("c", 256), valid},
		{"no rating id", "c", with(func(i *nativesigil.ConversationRatingInput) { i.RatingID = " " })},
		{"long rating id", "c", with(func(i *nativesigil.ConversationRatingInput) { i.RatingID = strings.Repeat("r", 129) })},
		{"bad rating value", "c", with(func(i *nativesigil.ConversationRatingInput) { i.Rating = "GOOD" })},
		{"long comment", "c", with(func(i *nativesigil.ConversationRatingInput) { i.Comment = strings.Repeat("x", 4097) })},
		{"long generation id", "c", with(func(i *nativesigil.ConversationRatingInput) { i.GenerationID = strings.Repeat("g", 256) })},
		{"long rater id", "c", with(func(i *nativesigil.ConversationRatingInput) { i.RaterID = strings.Repeat("r", 256) })},
		{"long source", "c", with(func(i *nativesigil.ConversationRatingInput) { i.Source = strings.Repeat("s", 65) })},
		{"large metadata", "c", with(func(i *nativesigil.ConversationRatingInput) {
			i.Metadata = map[string]any{"k": strings.Repeat("m", 16*1024)}
		})},
		{"unencodable metadata", "c", with(func(i *nativesigil.ConversationRatingInput) { i.Metadata = map[string]any{"k": make(chan int)} })},
	}
	r := newTestRatings(t, srv.URL)
	var events int
	r.Observe = func(context.Context, pushhook.Event) { events++ }
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := r.WriteConversationRating(context.Background(), tc.conv, tc.in); !errors.Is(err, ErrRatingValidation) {
				t.Fatalf("err = %v, want ErrRatingValidation", err)
			}
		})
	}
	if len(got()) != 0 || events != 0 {
		t.Fatalf("requests = %d, events = %d; validation must be silent and local", len(got()), events)
	}
}

func TestRatingBoundariesAreAccepted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in nativesigil.ConversationRatingInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		okRating(w, in.RatingID, strings.Repeat("c", 255), string(in.Rating))
	}))
	t.Cleanup(srv.Close)
	err := newTestRatings(t, srv.URL).WriteConversationRating(context.Background(), strings.Repeat("c", 255), nativesigil.ConversationRatingInput{
		RatingID: strings.Repeat("r", 128), Rating: nativesigil.ConversationRatingValueGood, Comment: strings.Repeat("x", 4096),
		GenerationID: strings.Repeat("g", 255), RaterID: strings.Repeat("a", 255), Source: strings.Repeat("s", 64),
	})
	if err != nil {
		t.Fatalf("limits are inclusive: %v", err)
	}
}

func TestRatingServerOutcomes(t *testing.T) {
	in := nativesigil.ConversationRatingInput{RatingID: "rat-1", Rating: nativesigil.ConversationRatingValueGood}
	cases := []struct {
		name     string
		respond  func(http.ResponseWriter, *http.Request, int)
		check    func(error) bool
		requests int
	}{
		{"conflict", func(w http.ResponseWriter, _ *http.Request, _ int) { http.Error(w, "idempotency conflict", 409) },
			func(err error) bool { return errors.Is(err, ErrRatingConflict) }, 1},
		{"server validation", func(w http.ResponseWriter, _ *http.Request, _ int) { http.Error(w, "rating id is too long", 400) },
			func(err error) bool {
				return errors.Is(err, ErrRatingValidation) && !strings.Contains(err.Error(), "too long")
			}, 1},
		{"ratings disabled", func(w http.ResponseWriter, r *http.Request, _ int) { http.NotFound(w, r) },
			func(err error) bool {
				return err != nil && !errors.Is(err, ErrRatingConflict) && !errors.Is(err, ErrRatingValidation)
			}, 1},
		{"unauthorized", func(w http.ResponseWriter, _ *http.Request, _ int) { http.Error(w, "authentication required", 401) },
			func(err error) bool { return err != nil }, 1},
		{"retries 503 then succeeds", func(w http.ResponseWriter, _ *http.Request, n int) {
			if n == 1 {
				http.Error(w, "busy", 503)
				return
			}
			okRating(w, "rat-1", "conv-1", "CONVERSATION_RATING_VALUE_GOOD")
		}, func(err error) bool { return err == nil }, 2},
		{"undecodable 200", func(w http.ResponseWriter, _ *http.Request, _ int) { _, _ = w.Write([]byte("nope")) },
			func(err error) bool { return err != nil }, 1},
		{"echo of another rating", func(w http.ResponseWriter, _ *http.Request, _ int) {
			okRating(w, "rat-other", "conv-1", "CONVERSATION_RATING_VALUE_GOOD")
		}, func(err error) bool { return err != nil }, 1},
		{"echo of another conversation", func(w http.ResponseWriter, _ *http.Request, _ int) {
			okRating(w, "rat-1", "conv-other", "CONVERSATION_RATING_VALUE_GOOD")
		}, func(err error) bool { return err != nil }, 1},
		{"echo of another value", func(w http.ResponseWriter, _ *http.Request, _ int) {
			okRating(w, "rat-1", "conv-1", "CONVERSATION_RATING_VALUE_BAD")
		}, func(err error) bool { return err != nil }, 1},
		{"202 is not an acceptance receipt", func(w http.ResponseWriter, _ *http.Request, _ int) {
			w.WriteHeader(202)
			_, _ = w.Write([]byte(`{"rating":{}}`))
		}, func(err error) bool { return err != nil }, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := newRatingServer(t, tc.respond)
			err := newTestRatings(t, srv.URL).WriteConversationRating(context.Background(), "conv-1", in)
			if !tc.check(err) {
				t.Fatalf("err = %v", err)
			}
			if n := len(got()); n != tc.requests {
				t.Fatalf("requests = %d, want %d", n, tc.requests)
			}
			if err != nil && strings.Contains(err.Error(), "authentication required") {
				t.Fatalf("error carries server text: %v", err)
			}
		})
	}
}

func TestRatingNotLeaderAndCancellation(t *testing.T) {
	srv, got := newRatingServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		okRating(w, "rat-1", "conv-1", "CONVERSATION_RATING_VALUE_GOOD")
	})
	in := nativesigil.ConversationRatingInput{RatingID: "rat-1", Rating: nativesigil.ConversationRatingValueGood}
	r := newTestRatings(t, srv.URL)
	r.SetDelivery(httpretry.Delivery{Gate: notLeaderGate{}, HTTPTimeout: time.Second, RetryMaxElapsed: time.Second})
	if err := r.WriteConversationRating(context.Background(), "conv-1", in); !errors.Is(err, ha.ErrNotLeader) {
		t.Fatalf("fenced err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := newTestRatings(t, srv.URL).WriteConversationRating(ctx, "conv-1", in); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled err = %v", err)
	}
	if len(got()) != 0 {
		t.Fatalf("requests = %d; a fenced or cancelled write sends nothing", len(got()))
	}
}

func TestRatingObserverReportsOutcomesWithoutContent(t *testing.T) {
	in := nativesigil.ConversationRatingInput{RatingID: "rat-1", Rating: nativesigil.ConversationRatingValueGood, Comment: "SECRET-COMMENT"}
	cases := []struct {
		name       string
		respond    func(http.ResponseWriter, *http.Request, int)
		wantItems  int
		wantStatus int
		wantCode   operationalerr.Code
	}{
		{"ok", func(w http.ResponseWriter, _ *http.Request, _ int) {
			okRating(w, "rat-1", "conv-1", "CONVERSATION_RATING_VALUE_GOOD")
		}, 1, 200, operationalerr.CodeNone},
		{"conflict", func(w http.ResponseWriter, _ *http.Request, _ int) { http.Error(w, "SECRET-COMMENT", 409) }, 0, 409, operationalerr.CodeRejected},
		{"unauthorized", func(w http.ResponseWriter, _ *http.Request, _ int) { http.Error(w, "SECRET-COMMENT", 401) }, 0, 401, operationalerr.CodeAuthentication},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newRatingServer(t, tc.respond)
			r := newTestRatings(t, srv.URL)
			var events []pushhook.Event
			r.Observe = func(_ context.Context, ev pushhook.Event) { events = append(events, ev) }
			_ = r.WriteConversationRating(context.Background(), "conv-1", in)
			if len(events) != 1 {
				t.Fatalf("events = %d, want 1", len(events))
			}
			ev := events[0]
			if ev.Sink != "sigil_ratings" || ev.Items != tc.wantItems || ev.Status != tc.wantStatus || ev.ErrorCode != tc.wantCode || ev.DryRun {
				t.Fatalf("event = %+v", ev)
			}
		})
	}
}

// TestRatingReadBackRoute pins the GET list route at backend bc963fb09: path, limit and
// cursor query, and the {items, next_cursor} envelope.
func TestRatingReadBackRoute(t *testing.T) {
	srv, got := newRatingServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		_, _ = w.Write([]byte(`{"items":[{"rating_id":"rat-2","conversation_id":"conv-1","rating":"CONVERSATION_RATING_VALUE_BAD","comment":"c","metadata":{"k":"v"},"rater_id":"u","source":"s","generation_id":"g","created_at":"2026-02-13T12:00:00Z"}],"next_cursor":"1"}`))
	})
	r := newTestRatings(t, srv.URL)
	var events []pushhook.Event
	r.Observe = func(_ context.Context, ev pushhook.Event) { events = append(events, ev) }
	page, err := r.ListConversationRatings(context.Background(), "conv-1", 10, "0")
	if err != nil {
		t.Fatal(err)
	}
	req := got()[0]
	if req.method != http.MethodGet || req.path != "/api/v1/conversations/conv-1/ratings" || req.rawPath != "" {
		t.Fatalf("request = %s %s", req.method, req.path)
	}
	if req.query != "cursor=0&limit=10" || len(req.body) != 0 {
		t.Fatalf("query = %q body = %q", req.query, req.body)
	}
	if req.header.Get("Authorization") == "" || req.header.Get("X-Scope-OrgID") != "tenant-1" || req.header.Get("Content-Type") != "" {
		t.Fatalf("headers = %v", req.header)
	}
	if page.NextCursor != "1" || len(page.Items) != 1 {
		t.Fatalf("page = %+v", page)
	}
	item := page.Items[0]
	if item.RatingID != "rat-2" || item.Rating != "CONVERSATION_RATING_VALUE_BAD" || item.Comment != "c" || item.Metadata["k"] != "v" || item.RaterID != "u" || item.Source != "s" || item.GenerationID != "g" ||
		!item.CreatedAt.Equal(time.Date(2026, 2, 13, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("item = %+v", item)
	}
	if len(events) != 1 || events[0].Sink != "sigil_ratings" || events[0].Items != 1 || events[0].Status != 200 {
		t.Fatalf("events = %+v", events)
	}
}

func TestRatingReadBackOmitsUnsetQueryAndMapsErrors(t *testing.T) {
	var status atomic.Int32
	status.Store(200)
	srv, got := newRatingServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		if s := int(status.Load()); s != 200 {
			http.Error(w, "x", s)
			return
		}
		_, _ = w.Write([]byte(`{"items":[],"next_cursor":""}`))
	})
	r := newTestRatings(t, srv.URL)
	if page, err := r.ListConversationRatings(context.Background(), "conv-1", 0, ""); err != nil || len(page.Items) != 0 || page.NextCursor != "" {
		t.Fatalf("page = %+v, err = %v", page, err)
	}
	if q := got()[0].query; q != "" {
		t.Fatalf("query = %q, want none", q)
	}
	status.Store(400)
	if _, err := r.ListConversationRatings(context.Background(), "conv-1", 5, "bad"); !errors.Is(err, ErrRatingValidation) {
		t.Fatalf("400 err = %v", err)
	}
	status.Store(404)
	if _, err := r.ListConversationRatings(context.Background(), "conv-1", 5, ""); err == nil {
		t.Fatal("404 (ratings disabled) must be an error")
	}
	if _, err := r.ListConversationRatings(context.Background(), " ", 5, ""); !errors.Is(err, ErrRatingValidation) {
		t.Fatalf("blank id err = %v", err)
	}
}
