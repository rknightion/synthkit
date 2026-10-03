// SPDX-License-Identifier: AGPL-3.0-only

// Package syslog models received synthetic syslog records, not a network listener.
// Vocabulary and error semantics: Alloy v1.20.1, Contrib v0.161.0; signals/logs.md.
package syslog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

// Config is selected by the owning consumer's blueprint config. Labels are bounded
// operator assignments, never copied from sender-controlled headers.
type Config struct {
	Receiver             string `yaml:"receiver"` // loki|otel
	ReceiverID           string `yaml:"receiver_id"`
	OnError              string `yaml:"on_error"` // OTel default send
	FullRFC5424          bool   `yaml:"use_rfc5424_message"`
	AllowEmpty           bool   `yaml:"rfc5424_allow_empty_msg"`
	Site                 string `yaml:"site"`
	Device               string `yaml:"device"`
	SourceType           string `yaml:"source_type"`
	Severity             string `yaml:"severity"`
	Service              string `yaml:"service"`
	AddAttributes        bool   `yaml:"add_attributes"`         // OTel optional source connection attributes.
	PreserveConnectionIP bool   `yaml:"preserve_connection_ip"` // Loki operator metadata mapping, not a stream label.
}

func (c Config) Validate() error {
	if c.Receiver != "loki" && c.Receiver != "otel" {
		return fmt.Errorf("syslog: receiver must be loki or otel")
	}
	if c.Receiver == "otel" && c.ReceiverID == "" {
		return errors.New("syslog: otel receiver_id is required")
	}
	switch c.OnError {
	case "", "send", "send_quiet", "drop", "drop_quiet":
	default:
		return errors.New("syslog: unsupported on_error")
	}
	if c.Receiver == "loki" && c.OnError != "" {
		return errors.New("syslog: on_error is OTel-only")
	}
	if c.Receiver == "loki" && c.AddAttributes {
		return errors.New("syslog: add_attributes is OTel-only")
	}
	if c.Receiver == "otel" && (c.FullRFC5424 || c.AllowEmpty || c.PreserveConnectionIP) {
		return errors.New("syslog: RFC5424 forwarding switches are Loki-only")
	}
	return nil
}

// Message contains source-decoded protocol facts. Owning device consumers supply
// synthetic facts, not arbitrary incoming traffic. StructuredData retains nested
// RFC5424 maps. Raw is the original synthetic line (OTel body).
type Message struct {
	Raw                                    string
	Protocol                               string
	Time                                   time.Time
	ObservedTime                           time.Time
	Hostname, Appname, ProcID, MsgID, Text string
	SourceIP                               string // Synthetic connection fact, never inferred from a sender header.
	Priority                               int
	Version                                int
	StructuredData                         map[string]any
	// ParseError models a source decoder failure; it is not a handoff failure.
	ParseError error
}

// Receiver owns cumulative intake state. It is emitter-local and single-threaded.
type Receiver struct {
	cfg Config
	st  *state.State
}

func New(c Config) (*Receiver, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &Receiver{cfg: c, st: state.NewState()}, nil
}

// Receive renders source-decoded records through the real core writer boundary.
// OTel handoff success/failure is based on Write, not on parsing. Quiet modes
// swallow parsing errors but never a downstream writer error.
func (r *Receiver) Receive(ctx context.Context, m Message, w *core.World) error {
	if w == nil {
		return errors.New("syslog: world is required")
	}
	parseErr := m.ParseError
	if parseErr == nil && (m.Protocol != "rfc5424" && m.Protocol != "rfc3164" || m.Priority < 0 || m.Priority > 191) {
		parseErr = errors.New("syslog: invalid decoded protocol or priority")
	}
	if r.cfg.Receiver == "loki" {
		if m.Raw == "" {
			r.st.Add("loki_source_syslog_empty_messages_total", nil, 1)
			return nil
		}
		if parseErr != nil {
			r.st.Add("loki_source_syslog_parsing_errors_total", nil, 1)
			return parseErr
		}
		if m.Text == "" {
			r.st.Add("loki_source_syslog_empty_messages_total", nil, 1)
			if m.Protocol != "rfc5424" || !r.cfg.AllowEmpty {
				return nil
			}
		}
		if w.Logs == nil {
			return errors.New("syslog: Loki writer is required")
		}
		labels := map[string]string{}
		for k, v := range map[string]string{"site": r.cfg.Site, "device": r.cfg.Device, "source_type": r.cfg.SourceType, "severity": r.cfg.Severity, "service": r.cfg.Service} {
			if v != "" {
				labels[k] = v
			}
		}
		body := m.Text
		if m.Protocol == "rfc5424" && r.cfg.FullRFC5424 {
			body = m.Raw
		}
		var meta map[string]string
		if r.cfg.PreserveConnectionIP && m.SourceIP != "" {
			// Explicit operator mapping from Alloy's internal connection key to
			// metadata, rather than retaining a high-cardinality stream label.
			meta = map[string]string{"__syslog_connection_ip_address": m.SourceIP}
		}
		if err := w.Logs.Write(ctx, []loki.Stream{{Labels: labels, Lines: []loki.Line{{T: m.Time, Body: body, Meta: meta}}}}); err != nil {
			return err
		}
		r.st.Add("loki_source_syslog_entries_total", nil, 1)
		return nil
	}
	mode := r.cfg.OnError
	if mode == "" {
		mode = "send"
	}
	if parseErr != nil && (mode == "drop" || mode == "drop_quiet") {
		if mode == "drop_quiet" {
			return nil
		}
		return parseErr
	}
	if w.OTLPLogs == nil {
		return errors.New("syslog: OTel writer is required")
	}
	rec := otlp.LogRecord{Time: m.Time, ObservedTime: m.ObservedTime, Body: m.Raw}
	if parseErr == nil {
		rec.Attrs = map[string]any{"priority": m.Priority, "facility": m.Priority / 8, "facility_text": facilities[m.Priority/8]}
		for k, v := range map[string]string{"hostname": m.Hostname, "appname": m.Appname, "proc_id": m.ProcID, "msg_id": m.MsgID, "message": m.Text} {
			if v != "" {
				rec.Attrs[k] = v
			}
		}
		if m.Protocol == "rfc5424" {
			rec.Attrs["version"] = m.Version
			if len(m.StructuredData) > 0 {
				rec.Attrs["structured_data"] = m.StructuredData
			}
		}
		sev := m.Priority % 8
		rec.Severity = severities[sev]
		rec.SeverityText = severityText[sev]
	} else {
		rec.Time = m.ObservedTime
	}
	if r.cfg.AddAttributes && m.SourceIP != "" {
		if rec.Attrs == nil {
			rec.Attrs = map[string]any{}
		}
		rec.Attrs["net.peer.ip"] = m.SourceIP
	}
	handoff := w.OTLPLogs.Write(ctx, []otlp.LogResource{{Records: []otlp.LogRecord{rec}}})
	r.Handoff(1, handoff)
	if handoff != nil {
		return handoff
	}
	if mode == "send_quiet" {
		return nil
	}
	return parseErr
}

// Handoff records the default receiverhelper behavior: all downstream errors are
// refused with newReceiverMetrics disabled; failed remains zero. No parse counter.
func (r *Receiver) Handoff(records float64, downstream error) {
	labels := map[string]string{"receiver": r.cfg.ReceiverID}
	name := "otelcol_receiver_accepted_log_records_total"
	if downstream != nil {
		name = "otelcol_receiver_refused_log_records_total"
	}
	r.st.Add(name, labels, records)
}

// Healthy advances a synthetic successful intake snapshot without fabricating logs.
// The owning health consumer supplies its declared aggregate traffic assumption.
func (r *Receiver) Healthy(records float64) {
	if r.cfg.Receiver == "otel" {
		r.Handoff(records, nil)
	} else {
		r.st.Add("loki_source_syslog_entries_total", nil, records)
	}
}

// Contrib v0.161.0 parser.go severityMapping/severityText. OTel Error3=19,
// Error2=18, Info2=10; these are protocol promotions, not arbitrary log levels.
var severities = [8]otlp.Severity{21, 19, 18, 17, 13, 10, 9, 5}
var severityText = [8]string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"}
var facilities = [24]string{"kern", "user", "mail", "daemon", "auth", "syslog", "lpr", "news", "uucp", "cron", "authpriv", "ftp", "ntp", "security", "console", "cron2", "local0", "local1", "local2", "local3", "local4", "local5", "local6", "local7"}

// Health returns exactly three default receiver counters. Scrape labels are
// attached separately by the owner; there are no Loki instrument dimensions.
func (r *Receiver) Health(now time.Time, scrape map[string]string) []promrw.Series {
	names := []string{"loki_source_syslog_entries_total", "loki_source_syslog_parsing_errors_total", "loki_source_syslog_empty_messages_total"}
	labels := map[string]string{}
	if r.cfg.Receiver == "otel" {
		names = []string{"otelcol_receiver_accepted_log_records_total", "otelcol_receiver_refused_log_records_total", "otelcol_receiver_failed_log_records_total"}
		labels["receiver"] = r.cfg.ReceiverID
	}
	for _, n := range names {
		r.st.Add(n, labels, 0)
	}
	out := r.st.Collect(now)
	for i := range out {
		cloned := map[string]string{}
		for k, v := range out[i].Labels {
			cloned[k] = v
		}
		for k, v := range scrape {
			if v != "" {
				cloned[k] = v
			}
		}
		out[i].Labels = cloned
	}
	return out
}

// Keep sink types on this public mechanic seam; consumers need no adapters.
var _ core.LogWriter = (*loki.Sink)(nil)
var _ core.OTLPLogWriter = (*otlp.LogsSink)(nil)
