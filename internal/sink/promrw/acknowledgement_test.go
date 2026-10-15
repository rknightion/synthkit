// SPDX-License-Identifier: AGPL-3.0-only

package promrw

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/snappy"
	"github.com/rknightion/synthkit/internal/pushhook"
	"github.com/rknightion/synthkit/internal/sink/promrw/writev2"
	"google.golang.org/protobuf/proto"
)

func TestRW2WrittenCountsRejectFalseSuccess(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("X-Prometheus-Remote-Write-Samples-Written", "0")
		w.Header().Set("X-Prometheus-Remote-Write-Histograms-Written", "0")
		w.Header().Set("X-Prometheus-Remote-Write-Exemplars-Written", "0")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	sink := New(server.URL, "test", "test", false, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := sink.Write(ctx, []Series{{Name: "ack_proof", Value: 1, T: time.Unix(1, 0)}}); err == nil {
		t.Fatal("RW2 zero written samples was reported as success")
	}
	if calls.Load() != 1 {
		t.Fatalf("nonretryable 2xx receipt retried %d times", calls.Load())
	}
}

func TestRW2AcknowledgementIncludesHistogramAndExemplar(t *testing.T) {
	for _, tc := range []struct {
		name               string
		status             int
		headers            map[string]string
		want               pushhook.AckStatus
		accepted, rejected int64
		bad                bool
	}{
		{"full", 204, map[string]string{"Samples": "1", "Histograms": "1", "Exemplars": "1"}, pushhook.AckAccepted, 3, 0, false},
		{"missing-exemplar", 204, map[string]string{"Samples": "1", "Histograms": "1", "Exemplars": "0"}, pushhook.AckPartial, 2, 1, true},
		{"missing-nonempty-categories", 200, map[string]string{"Samples": "1"}, pushhook.AckPartial, 1, 2, true},
		{"legacy-unverified", 204, nil, pushhook.AckUnknown, 0, 0, false},
		{"async-unverified", 202, nil, pushhook.AckDeferred, 0, 0, false},
		{"async-partial-counts", 202, map[string]string{"Samples": "1"}, pushhook.AckDeferred, 1, 0, false},
		{"bad-count", 204, map[string]string{"Samples": "-1"}, pushhook.AckUnknown, 0, 0, true},
		{"over-count", 204, map[string]string{"Samples": "2"}, pushhook.AckUnknown, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				compressed, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				raw, err := snappy.Decode(nil, compressed)
				if err != nil {
					t.Error(err)
					return
				}
				var request writev2.Request
				if err := proto.Unmarshal(raw, &request); err != nil {
					t.Error(err)
					return
				}
				var samples, histograms, exemplars int
				for _, s := range request.Timeseries {
					samples += len(s.Samples)
					histograms += len(s.Histograms)
					exemplars += len(s.Exemplars)
				}
				if samples != 1 || histograms != 1 || exemplars != 1 {
					t.Errorf("encoded request counts %d/%d/%d", samples, histograms, exemplars)
				}
				for kind, value := range tc.headers {
					w.Header().Set("X-Prometheus-Remote-Write-"+kind+"-Written", value)
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			sink := New(server.URL, "test", "test", false, nil)
			var event pushhook.Event
			sink.Observe = func(_ context.Context, e pushhook.Event) { event = e }
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err := sink.Write(ctx, []Series{
				{Name: "ack_scalar", Value: 1, T: time.Unix(1, 0), Exemplars: []Exemplar{{Value: 1, T: time.Unix(1, 0), Labels: map[string]string{"trace_id": "00000000000000000000000000000001"}}}},
				{Name: "ack_histogram", Kind: KindHistogram, T: time.Unix(1, 0), Native: &NativeHistogram{Schema: 0, Count: 1, ZeroCount: 1}},
			})
			if (err != nil) != tc.bad || calls.Load() != 1 {
				t.Fatalf("err=%v calls=%d", err, calls.Load())
			}
			a := event.Acknowledgement
			if a.Status != tc.want || a.Expected != 3 || a.Accepted != tc.accepted || a.Rejected != tc.rejected || a.Unit != "rw2_elements" {
				t.Fatalf("acknowledgement %+v", a)
			}
		})
	}
}
