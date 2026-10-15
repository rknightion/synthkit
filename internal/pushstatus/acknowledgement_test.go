// SPDX-License-Identifier: AGPL-3.0-only

package pushstatus

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/pushhook"
)

func TestAcknowledgementStatusRetainsEvidenceAndRejectsArbitraryText(t *testing.T) {
	s := NewStore()
	observe := s.Observer()
	good := pushhook.Acknowledgement{Status: pushhook.AckPartial, Unit: "log_records", Expected: 2, Accepted: 1, Rejected: 1}
	observe(context.Background(), pushhook.Event{Sink: "otlplogs", Items: 2, Status: 200, Acknowledgement: good})
	if got := s.Snapshot()[0].LastAcknowledgement; got != good {
		t.Fatalf("lost protocol evidence: %+v", got)
	}
	observe(context.Background(), pushhook.Event{Sink: "otlplogs", DryRun: true})
	if got := s.Snapshot()[0].LastAcknowledgement; got != good {
		t.Fatal("dry run overwrote real receipt")
	}
	for _, bad := range []pushhook.Acknowledgement{
		{Status: pushhook.AckAccepted, Unit: "private receiver diagnostic", Expected: 1, Accepted: 1},
		{Status: pushhook.AckAccepted, Unit: "log_records", Expected: 1, Accepted: 2},
		{Status: pushhook.AckPartial, Unit: "log_records", Expected: 2, Accepted: 2, Rejected: 1},
	} {
		observe(context.Background(), pushhook.Event{Sink: "otlplogs", Acknowledgement: bad})
		got := s.Snapshot()[0].LastAcknowledgement
		if got.Status != pushhook.AckUnknown || got.Accepted != 0 || got.Unit != "" {
			t.Fatalf("unsafe receipt exposed: %+v", got)
		}
		raw, _ := json.Marshal(s.Snapshot())
		if strings.Contains(string(raw), "private") {
			t.Fatal("diagnostic text exposed")
		}
	}
}
