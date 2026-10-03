// SPDX-License-Identifier: AGPL-3.0-only
package syslog

import (
	"context"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"testing"
	"time"
)

// Catches losing configured connection facts or indexing source IP as a stream label.
func TestConnectionRetention(t *testing.T) {
	at := time.Date(2026, 10, 11, 22, 14, 15, 0, time.UTC)
	m := Message{Protocol: "rfc5424", Raw: "<165>1 2025-12-18T00:33:00Z web01 nginx - - [audit@123 id=\"456\"] Login failed", Text: "Login failed", Priority: 165, Version: 1, Time: at, SourceIP: "192.0.2.10"}
	ls := loki.New("", "", "", true)
	ls.Capture = true
	ls.Quiet = true
	os := otlp.NewLogs("", "", "", true)
	os.Capture = true
	os.Quiet = true
	lr, _ := New(Config{Receiver: "loki", FullRFC5424: true, PreserveConnectionIP: true})
	or, _ := New(Config{Receiver: "otel", ReceiverID: "syslog", AddAttributes: true})
	w := &core.World{Logs: ls, OTLPLogs: os}
	if err := lr.Receive(context.Background(), m, w); err != nil {
		t.Fatal(err)
	}
	if err := or.Receive(context.Background(), m, w); err != nil {
		t.Fatal(err)
	}
	line := ls.Captured()[0].Lines[0]
	if line.Body != m.Raw || line.Meta["__syslog_connection_ip_address"] != "192.0.2.10" {
		t.Fatalf("missing retained source facts: %+v", line)
	}
	if len(ls.Captured()[0].Labels) != 0 {
		t.Fatal("source facts indexed")
	}
	if os.Captured()[0].Records[0].Attrs["net.peer.ip"] != "192.0.2.10" {
		t.Fatal("missing optional peer IP")
	}
}
