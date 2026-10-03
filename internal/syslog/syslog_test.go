// SPDX-License-Identifier: AGPL-3.0-only
package syslog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// Catches header loss, incorrect Loki full-line defaults, and high-card stream promotion.
func TestRFCShapes(t *testing.T) {
	for _, protocol := range []string{"rfc5424", "rfc3164"} {
		t.Run(protocol, func(t *testing.T) {
			at := time.Date(2026, 10, 11, 22, 14, 15, 0, time.UTC)
			m := Message{Protocol: protocol, Time: at, ObservedTime: at, Hostname: "device-a", Appname: "agent", ProcID: "123", MsgID: "event-a", Text: "State changed", Priority: 165, Version: 1, StructuredData: map[string]any{"example@99999": map[string]any{"test": "value"}}}
			m.Raw = "<165>1 2026-10-11T22:14:15Z device-a agent 123 event-a [example@99999 test=\"value\"] State changed"
			if protocol == "rfc3164" {
				m.Raw = "<165>Oct 11 22:14:15 device-a agent[123]: State changed"
				m.MsgID = ""
				m.StructuredData = nil
			}
			ls := loki.New("", "", "", true)
			ls.Capture = true
			ls.Quiet = true
			os := otlp.NewLogs("", "", "", true)
			os.Capture = true
			os.Quiet = true
			world := &core.World{Logs: ls, OTLPLogs: os}
			lr, err := New(Config{Receiver: "loki", Site: "site-a", Device: "device-a"})
			if err != nil {
				t.Fatal(err)
			}
			if err = lr.Receive(context.Background(), m, world); err != nil {
				t.Fatal(err)
			}
			streams := ls.Captured()
			if len(streams) != 1 || streams[0].Lines[0].Body != m.Text {
				t.Fatalf("Loki body: %+v", streams)
			}
			if len(streams[0].Labels) != 2 {
				t.Fatalf("sender fields became labels: %v", streams[0].Labels)
			}
			or, err := New(Config{Receiver: "otel", ReceiverID: "syslog"})
			if err != nil {
				t.Fatal(err)
			}
			if err = or.Receive(context.Background(), m, world); err != nil {
				t.Fatal(err)
			}
			blocks := os.Captured()
			if len(blocks) != 1 {
				t.Fatalf("OTel blocks: %+v", blocks)
			}
			rec := blocks[0].Records[0]
			if rec.Body != m.Raw || rec.Severity != 10 || rec.SeverityText != "notice" {
				t.Fatalf("OTel record: %+v", rec)
			}
			if rec.Attrs["proc_id"] != "123" {
				t.Fatalf("header lost: %+v", rec.Attrs)
			}
			if _, ok := rec.Attrs["severity"]; ok {
				t.Fatal("severity not promoted")
			}
			if protocol == "rfc5424" {
				if _, ok := rec.Attrs["structured_data"].(map[string]any); !ok {
					t.Fatal("nested SD flattened")
				}
			}
			health := or.Health(at, nil)
			if len(health) != 3 {
				t.Fatalf("health count %d", len(health))
			}
		})
	}
}

// Catches confusing parse errors with OTel handoff failure, dropping default-send
// records, failing to swallow quiet errors, and Loki's missing drop/error count.
func TestMalformed(t *testing.T) {
	at := time.Date(2026, 10, 11, 22, 14, 15, 0, time.UTC)
	m := Message{Raw: "malformed synthetic message", ObservedTime: at, ParseError: errors.New("invalid syslog")}
	for _, mode := range []string{"", "send", "send_quiet", "drop", "drop_quiet"} {
		t.Run(mode, func(t *testing.T) {
			sink := otlp.NewLogs("", "", "", true)
			sink.Capture = true
			sink.Quiet = true
			r, err := New(Config{Receiver: "otel", ReceiverID: "syslog", OnError: mode})
			if err != nil {
				t.Fatal(err)
			}
			err = r.Receive(context.Background(), m, &core.World{OTLPLogs: sink})
			quiet := mode == "send_quiet" || mode == "drop_quiet"
			if (err == nil) != quiet {
				t.Fatalf("error %v, mode %s", err, mode)
			}
			send := mode == "" || mode == "send" || mode == "send_quiet"
			if (len(sink.Captured()) == 1) != send {
				t.Fatalf("wrong forwarding: %+v", sink.Captured())
			}
			if send {
				rec := sink.Captured()[0].Records[0]
				if rec.Body != "malformed synthetic message" || !rec.Time.Equal(at) || len(rec.Attrs) != 0 || rec.Severity != 0 {
					t.Fatalf("unparsed record changed: %+v", rec)
				}
			}
			h := r.Health(at, nil)
			for _, s := range h {
				if _, ok := s.Labels["transport"]; ok {
					t.Fatal("empty transport emitted")
				}
				if s.Name == "otelcol_receiver_accepted_log_records_total" && ((s.Value == 1) != send) {
					t.Fatalf("accepted %+v", s)
				}
			}
		})
	}
	sink := loki.New("", "", "", true)
	sink.Capture = true
	sink.Quiet = true
	r, _ := New(Config{Receiver: "loki"})
	if err := r.Receive(context.Background(), m, &core.World{Logs: sink}); err == nil {
		t.Fatal("missing Loki parser error")
	}
	if len(sink.Captured()) != 0 {
		t.Fatal("Loki forwarded malformed input")
	}
	for _, s := range r.Health(at, nil) {
		if s.Name == "loki_source_syslog_parsing_errors_total" && s.Value != 1 {
			t.Fatalf("parse counter %+v", s)
		}
	}
}
