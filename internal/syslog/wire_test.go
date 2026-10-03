// SPDX-License-Identifier: AGPL-3.0-only
package syslog

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// Catches invalid wire envelopes, lost RFC headers/SD, and treating downstream
// refusal as a parse failure. Facts derive from Alloy v1.20.1 docs lines 165/169,
// not a decoder implemented here. These are vendor example identities, not captures.
func TestRFCWireAndHandoff(t *testing.T) {
	at := time.Date(2025, 12, 18, 0, 33, 0, 0, time.UTC)
	cases := []Message{
		{Protocol: "rfc5424", Raw: `<165>1 2025-12-18T00:33:00Z web01 nginx - - [audit@123 id="456"] Login failed`, Time: at, ObservedTime: at, Priority: 165, Version: 1, Hostname: "web01", Appname: "nginx", Text: "Login failed", StructuredData: map[string]any{"audit@123": map[string]any{"id": "456"}}},
		{Protocol: "rfc3164", Raw: `<34>Oct 11 22:14:15 my-server-01 sshd[1234]: Failed password for root from 192.168.1.10 port 22 ssh2`, Time: time.Date(2025, 10, 11, 22, 14, 15, 0, time.UTC), ObservedTime: at, Priority: 34, Hostname: "my-server-01", Appname: "sshd", ProcID: "1234", Text: "Failed password for root from 192.168.1.10 port 22 ssh2"},
	}
	for _, m := range cases {
		t.Run(m.Protocol, func(t *testing.T) {
			var lokiBody struct {
				Streams []struct {
					Stream map[string]string   `json:"stream"`
					Values [][]json.RawMessage `json:"values"`
				} `json:"streams"`
			}
			var record *logspb.LogRecord
			reject := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if reject {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				zr, err := gzip.NewReader(r.Body)
				if err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				defer zr.Close()
				raw, err := io.ReadAll(zr)
				if err != nil {
					t.Error(err)
					return
				}
				if r.URL.Path == "/v1/logs" {
					n, typ, k := protowire.ConsumeTag(raw)
					if n != 1 || typ != protowire.BytesType || k < 0 {
						t.Error("bad OTLP envelope")
						return
					}
					payload, k := protowire.ConsumeBytes(raw[k:])
					if k < 0 {
						t.Error("bad OTLP length")
						return
					}
					rl := &logspb.ResourceLogs{}
					if err := proto.Unmarshal(payload, rl); err != nil {
						t.Error(err)
						return
					}
					if len(rl.ScopeLogs) != 1 || len(rl.ScopeLogs[0].LogRecords) != 1 {
						t.Error("unexpected records")
						return
					}
					record = rl.ScopeLogs[0].LogRecords[0]
				} else {
					if err := json.Unmarshal(raw, &lokiBody); err != nil {
						t.Error(err)
					}
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()
			w := &core.World{Logs: loki.New(srv.URL, "", "", false), OTLPLogs: otlp.NewLogs(srv.URL, "", "", false)}
			lr, _ := New(Config{Receiver: "loki", FullRFC5424: true, Site: "site-a", Device: "device-a", SourceType: "appliance", Severity: "notice", Service: "syslog"})
			or, _ := New(Config{Receiver: "otel", ReceiverID: "syslog"})
			if err := lr.Receive(context.Background(), m, w); err != nil {
				t.Fatal(err)
			}
			if len(lokiBody.Streams) != 1 || len(lokiBody.Streams[0].Stream) != 5 {
				t.Fatalf("unbounded labels: %+v", lokiBody)
			}
			var body string
			if err := json.Unmarshal(lokiBody.Streams[0].Values[0][1], &body); err != nil {
				t.Fatal(err)
			}
			want := m.Text
			if m.Protocol == "rfc5424" {
				want = m.Raw
			}
			if body != want {
				t.Fatalf("body lost: %s", body)
			}
			if err := or.Receive(context.Background(), m, w); err != nil {
				t.Fatal(err)
			}
			if record == nil || record.Body.GetStringValue() != m.Raw || record.TimeUnixNano != uint64(m.Time.UnixNano()) {
				t.Fatalf("bad record: %+v", record)
			}
			attrs := map[string]any{}
			for _, kv := range record.Attributes {
				attrs[kv.Key] = kv.Value
			}
			if _, ok := attrs["severity"]; ok {
				t.Fatal("severity was not promoted")
			}
			if m.Protocol == "rfc5424" {
				if record.SeverityNumber != 10 || record.SeverityText != "notice" {
					t.Fatal("RFC5424 severity mapping")
				}
				for _, kv := range record.Attributes {
					if kv.Key == "structured_data" {
						outer := kv.Value.GetKvlistValue().Values
						if len(outer) != 1 || outer[0].Key != "audit@123" || outer[0].Value.GetKvlistValue().Values[0].Value.GetStringValue() != "456" {
							t.Fatal("nested SD lost")
						}
					}
				}
				if attrs["structured_data"] == nil || attrs["version"] == nil {
					t.Fatal("RFC5424 facts missing")
				}
			} else {
				if record.SeverityNumber != 18 || record.SeverityText != "crit" {
					t.Fatal("RFC3164 severity mapping")
				}
				for _, kv := range record.Attributes {
					if kv.Key == "proc_id" && kv.Value.GetStringValue() != "1234" {
						t.Fatal("PID lost")
					}
				}
				if attrs["proc_id"] == nil {
					t.Fatal("PID missing")
				}
			}
			reject = true
			bad := m
			bad.ParseError = errors.New("source decoder failure")
			// Quiet parsing still returns the real downstream failure.
			quiet, _ := New(Config{Receiver: "otel", ReceiverID: "syslog", OnError: "send_quiet"})
			if err := quiet.Receive(context.Background(), bad, w); err == nil {
				t.Fatal("downstream failure swallowed")
			}
			if err := or.Receive(context.Background(), m, w); err == nil {
				t.Fatal("writer failure swallowed")
			}
			for _, s := range or.Health(at, nil) {
				want := float64(0)
				if s.Name == "otelcol_receiver_accepted_log_records_total" || s.Name == "otelcol_receiver_refused_log_records_total" {
					want = 1
				}
				if s.Value != want {
					t.Fatalf("handoff truth: %+v", s)
				}
			}
		})
	}
}

// Catches confusing empty messages with parse failures and forwarding RFC3164
// empty MSG under the RFC5424-only allow switch.
func TestEmptyMessages(t *testing.T) {
	for _, protocol := range []string{"rfc5424", "rfc3164"} {
		for _, allow := range []bool{false, true} {
			sink := loki.New("", "", "", true)
			sink.Capture = true
			sink.Quiet = true
			r, _ := New(Config{Receiver: "loki", AllowEmpty: allow})
			m := Message{Protocol: protocol, Raw: "synthetic empty MSG header", Priority: 34, Time: time.Now()}
			if err := r.Receive(context.Background(), m, &core.World{Logs: sink}); err != nil {
				t.Fatal(err)
			}
			want := 0
			if protocol == "rfc5424" && allow {
				want = 1
			}
			if len(sink.Captured()) != want {
				t.Fatal("incorrect empty forwarding")
			}
			// A transport-empty input is dropped even with allow_empty enabled.
			m.Raw = ""
			if err := r.Receive(context.Background(), m, &core.World{Logs: sink}); err != nil {
				t.Fatal(err)
			}
			for _, s := range r.Health(m.Time, nil) {
				if len(s.Labels) != 0 {
					t.Fatal("labelled Loki instrument")
				}
				if s.Name == "loki_source_syslog_empty_messages_total" && s.Value != 2 {
					t.Fatalf("not cumulative: %+v", s)
				}
			}
		}
	}
}
