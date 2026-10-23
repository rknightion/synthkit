// SPDX-License-Identifier: AGPL-3.0-only

// Package dynamodb models explicit provisioned tables, independently of workloads.
// Names, units and dimension alternatives: signals/cw.md, cw-dynamodb, sourced
// from https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/metrics-dimensions.html.
// This consumer implements remote-write only, not native CloudWatch streams.
package dynamodb

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

// Config describes the entire modelled account/region's provisioned inventory.
// No defaults invent traffic, quotas, resource names, capacity or latency.
type Config struct {
	Account Account `yaml:"account"`
	Tables  []Table `yaml:"tables"`
}

// Account quotas are operator assumptions in capacity units/second (Count).
// All four must be explicit and positive; these are not service-default claims.
type Account struct {
	MaxReads       float64 `yaml:"max_reads"`
	MaxWrites      float64 `yaml:"max_writes"`
	MaxTableReads  float64 `yaml:"max_table_reads"`
	MaxTableWrites float64 `yaml:"max_table_writes"`
}

// Table has a vendor TableName and a separate, explicit collector name. No ARN
// constructor or metadata enrichment is inferred. All tables are provisioned.
type Table struct {
	Name              string  `yaml:"name"`
	CollectorName     string  `yaml:"collector_name"`
	ProvisionedReads  float64 `yaml:"provisioned_reads"`
	ProvisionedWrites float64 `yaml:"provisioned_writes"`
	// Omitted selects TableName-only. Customer selects customer-origin writes;
	// GlobalTable selects replicated writes, excluding customer-origin writes.
	// The declared operation write demand belongs exclusively to that source.
	WriteSource string `yaml:"write_source"`
	// Replication is required only with GlobalTable; replica writes are not
	// successful customer PutItem API calls and produce no PutItem latency.
	Replication                  *Replication `yaml:"replication"`
	ConditionalFailuresPerSecond float64      `yaml:"conditional_failures_per_second"`
	Operations                   []Operation  `yaml:"operations"`
	Stream                       *Stream      `yaml:"stream"`
}

// Operation models successful service-side requests only (not network/client
// time). Fixed latency, units and returned-items per request are explicit
// operator assumptions, not captured numeric distributions. ReadUnits can be
// fractional (e.g. eventual reads); WriteUnits is capacity, not item bytes.
// ExecuteStatement here means SELECT only. No grouped captured enum is expanded.
type Operation struct {
	Name              string  `yaml:"name"`
	RequestsPerSecond float64 `yaml:"requests_per_second"`
	ReadUnits         float64 `yaml:"read_units"`
	WriteUnits        float64 `yaml:"write_units"`
	LatencyMS         float64 `yaml:"latency_ms"`
	ReturnedItems     float64 `yaml:"returned_items"`
}

// Stream explicitly declares the Streams identity and successful GetRecords
// polling model. Zero returned records is a valid successful empty poll.
type Stream struct {
	Label             string  `yaml:"label"`
	RequestsPerSecond float64 `yaml:"requests_per_second"`
	RecordsPerRequest float64 `yaml:"records_per_request"`
	BytesPerRecord    float64 `yaml:"bytes_per_record"`
	LatencyMS         float64 `yaml:"latency_ms"`
}

// Replication models applied replica write events independently of customer API calls.
type Replication struct {
	WritesPerSecond float64 `yaml:"writes_per_second"`
	UnitsPerWrite   float64 `yaml:"units_per_write"`
}

type tableState struct {
	table                     Table
	carry                     []float64
	failures, polls, replicas float64
}
type construct struct {
	account Account
	cloud   fixture.Cloud
	tables  []tableState
	last    time.Time
	paused  bool
}

func Registration() core.ConstructReg {
	return core.ConstructReg{Kind: "dynamodb", Doc: "Explicit provisioned DynamoDB tables (remote-write only)", Scope: core.ScopeBlueprint, NewConfig: func() any { return &Config{} }, Build: Build}
}

// Build takes an owned copy: subsequent config/fixture mutation cannot alter a
// running instance or bypass transport/identity validation.
func Build(raw any, fx *fixture.Set) (core.Construct, error) {
	cfg, ok := raw.(*Config)
	if !ok || cfg == nil {
		return nil, fmt.Errorf("dynamodb: expected *Config")
	}
	if fx == nil || fx.Cloud == nil || strings.TrimSpace(fx.Cloud.AccountID) == "" || strings.TrimSpace(fx.Cloud.Region) == "" {
		return nil, fmt.Errorf("dynamodb: explicit cloud account and region required")
	}
	if mode := fx.Cloud.CloudWatchExportMode(); mode != "remote_write" {
		return nil, fmt.Errorf("dynamodb: CloudWatch export %q unsupported; only remote_write is admitted (otlp not implemented)", mode)
	}
	for _, v := range []float64{cfg.Account.MaxReads, cfg.Account.MaxWrites, cfg.Account.MaxTableReads, cfg.Account.MaxTableWrites} {
		if !number(v) || v == 0 {
			return nil, fmt.Errorf("dynamodb: account quotas must be finite positive capacity units <= 1e9")
		}
	}
	if cfg.Account.MaxTableReads > cfg.Account.MaxReads || cfg.Account.MaxTableWrites > cfg.Account.MaxWrites {
		return nil, fmt.Errorf("dynamodb: table quota exceeds account quota")
	}
	c := &construct{account: cfg.Account, cloud: *fx.Cloud}
	names, collectors := map[string]bool{}, map[string]bool{}
	var totalR, totalW float64
	for _, t := range cfg.Tables {
		if !identity(t.Name) || !identity(t.CollectorName) || t.CollectorName == "global" || names[t.Name] || collectors[t.CollectorName] {
			return nil, fmt.Errorf("dynamodb: unique nonblank table and non-global collector_name required")
		}
		names[t.Name] = true
		collectors[t.CollectorName] = true
		if !number(t.ProvisionedReads) || !number(t.ProvisionedWrites) || t.ProvisionedReads == 0 || t.ProvisionedWrites == 0 || t.ProvisionedReads > cfg.Account.MaxTableReads || t.ProvisionedWrites > cfg.Account.MaxTableWrites {
			return nil, fmt.Errorf("dynamodb: table %q provisioned capacity invalid or above quota", t.Name)
		}
		if t.WriteSource != "" && t.WriteSource != "Customer" && t.WriteSource != "GlobalTable" {
			return nil, fmt.Errorf("dynamodb: write_source must be omitted, Customer or GlobalTable")
		}
		if !number(t.ConditionalFailuresPerSecond) {
			return nil, fmt.Errorf("dynamodb: invalid conditional failure rate")
		}
		seen := map[string]bool{}
		var demandR, demandW float64
		for _, o := range t.Operations {
			if seen[o.Name] {
				return nil, fmt.Errorf("dynamodb: duplicate operation %q", o.Name)
			}
			seen[o.Name] = true
			for _, v := range []float64{o.RequestsPerSecond, o.ReadUnits, o.WriteUnits, o.LatencyMS, o.ReturnedItems} {
				if !number(v) {
					return nil, fmt.Errorf("dynamodb: invalid finite nonnegative operation assumption")
				}
			}
			if math.Trunc(o.ReturnedItems) != o.ReturnedItems {
				return nil, fmt.Errorf("dynamodb: returned_items must be a whole nonnegative item count")
			}
			switch o.Name {
			case "GetItem":
				if o.WriteUnits != 0 || o.ReadUnits == 0 || o.ReturnedItems != 0 {
					return nil, fmt.Errorf("dynamodb: GetItem requires read_units only, no ReturnedItemCount")
				}
			case "Query", "Scan", "ExecuteStatement":
				if o.WriteUnits != 0 || o.ReadUnits == 0 {
					return nil, fmt.Errorf("dynamodb: select operations require read_units only")
				}
			case "PutItem":
				if o.ReadUnits != 0 || o.WriteUnits == 0 || o.ReturnedItems != 0 {
					return nil, fmt.Errorf("dynamodb: PutItem requires write_units only, no ReturnedItemCount")
				}
			default:
				return nil, fmt.Errorf("dynamodb: unsupported operation %q", o.Name)
			}
			if o.LatencyMS == 0 {
				return nil, fmt.Errorf("dynamodb: positive service latency required")
			}
			demandR += o.RequestsPerSecond * o.ReadUnits
			demandW += o.RequestsPerSecond * o.WriteUnits
		}
		if t.WriteSource == "GlobalTable" {
			if demandW != 0 || t.Replication == nil {
				return nil, fmt.Errorf("dynamodb: GlobalTable requires replication and excludes customer write operations")
			}
			r := *t.Replication
			if !number(r.WritesPerSecond) || !number(r.UnitsPerWrite) || r.UnitsPerWrite == 0 {
				return nil, fmt.Errorf("dynamodb: invalid replicated write assumptions")
			}
			demandW = r.WritesPerSecond * r.UnitsPerWrite
			t.Replication = &r
		} else if t.Replication != nil {
			return nil, fmt.Errorf("dynamodb: replication requires explicit GlobalTable source")
		}
		if demandR > t.ProvisionedReads || demandW > t.ProvisionedWrites {
			return nil, fmt.Errorf("dynamodb: successful demand exceeds provisioned capacity")
		}
		if t.Stream != nil {
			s := *t.Stream
			if !identity(s.Label) {
				return nil, fmt.Errorf("dynamodb: stream label required")
			}
			for _, v := range []float64{s.RequestsPerSecond, s.RecordsPerRequest, s.BytesPerRecord, s.LatencyMS} {
				if !number(v) {
					return nil, fmt.Errorf("dynamodb: invalid stream assumption")
				}
			}
			if math.Trunc(s.RecordsPerRequest) != s.RecordsPerRequest || math.Trunc(s.BytesPerRecord) != s.BytesPerRecord {
				return nil, fmt.Errorf("dynamodb: records_per_request and bytes_per_record must be whole nonnegative values")
			}
			// Real GetRecords limits: <=1000 records and <=1 MiB returned per call.
			if s.LatencyMS == 0 || s.RecordsPerRequest > 1000 || s.RecordsPerRequest*s.BytesPerRecord > 1048576 {
				return nil, fmt.Errorf("dynamodb: stream latency or GetRecords response size invalid")
			}
			t.Stream = &s
		}
		t.Operations = append([]Operation(nil), t.Operations...)
		c.tables = append(c.tables, tableState{table: t, carry: make([]float64, len(t.Operations))})
		totalR += t.ProvisionedReads
		totalW += t.ProvisionedWrites
	}
	if totalR > cfg.Account.MaxReads || totalW > cfg.Account.MaxWrites {
		return nil, fmt.Errorf("dynamodb: total provisioned capacity exceeds account quota")
	}
	return c, nil
}
func number(v float64) bool                    { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1e9 }
func identity(v string) bool                   { return strings.TrimSpace(v) != "" && strings.TrimSpace(v) == v }
func (*construct) Kind() string                { return "dynamodb" }
func (*construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (*construct) Interval() time.Duration     { return time.Minute }
func (c *construct) labels(name string) map[string]string {
	return map[string]string{"account_id": c.cloud.AccountID, "region": c.cloud.Region, "namespace": "AWS/DynamoDB", "job": "cloud/aws/dynamodb", "name": name}
}

// take keeps arrivals already earned during observed active windows, not idle
// service capacity. Gaps cap new accrual without discarding prior fractions.
func take(rate, seconds float64, carry *float64) float64 {
	v := rate*seconds + *carry
	n := math.Floor(v)
	*carry = v - n
	return n
}

func (c *construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	if !c.last.IsZero() && !now.After(c.last) {
		return nil
	}
	if w == nil || w.Metrics == nil {
		return fmt.Errorf("dynamodb: metric writer required")
	}
	// The runner still calls paused constructs. Suppress the entire batch,
	// including capacity/account stocks, and never accrue pause-time traffic.
	// Retain only fractions earned during previously observed active windows.
	if w.Shape != nil && w.Shape.Paused() {
		c.last, c.paused = now, true
		return nil
	}
	if c.paused {
		// This first nonpaused observation establishes the active boundary: the
		// preceding interval has no evidence of activity and is not replayed.
		c.last, c.paused = now, false
		return nil
	}
	// Nonzero master volume deliberately retains the fixed operator rate model;
	// scaling/diurnal/overload semantics are not admitted by this consumer.
	seconds := 60.0
	if !c.last.IsZero() {
		seconds = now.Sub(c.last).Seconds()
		if seconds > 60 {
			// Observe only the current capped window, never replay the gap.
			// Already-earned fractions remain valid even across cadence jitter
			// or a long gap; losing them would starve legitimate sparse traffic.
			seconds = 60
		}
	}
	// A new state per reporting window withholds sparse families rather than
	// accidentally retaining the prior window's observations in state.Collect.
	st := state.NewState()
	var provisionR, provisionW, maxR, maxW float64
	for i := range c.tables {
		ts := &c.tables[i]
		t := ts.table
		l := c.labels(t.CollectorName)
		l["dimension_TableName"] = t.Name
		var readSum, writeSum, readPeak, writePeak, readMin, writeMin float64
		for j, o := range t.Operations {
			n := take(o.RequestsPerSecond, seconds, &ts.carry[j])
			readSum += n * o.ReadUnits
			writeSum += n * o.WriteUnits
			// Evenly scheduled requests across second-sized capacity observations.
			readPeak += math.Ceil(n/math.Ceil(seconds)) * o.ReadUnits
			writePeak += math.Ceil(n/math.Ceil(seconds)) * o.WriteUnits
			readMin += math.Floor(n/math.Ceil(seconds)) * o.ReadUnits
			writeMin += math.Floor(n/math.Ceil(seconds)) * o.WriteUnits
			if n == 0 {
				continue
			}
			op := c.labels(t.CollectorName)
			op["dimension_TableName"] = t.Name
			op["dimension_Operation"] = o.Name
			// Milliseconds: one observation per successful service request.
			cw.EmitStats(st, "aws_dynamodb_successful_request_latency", op, cw.StatSet{Sum: n * o.LatencyMS, Average: o.LatencyMS, Maximum: o.LatencyMS, Minimum: o.LatencyMS, SampleCount: n})
			if o.Name == "Query" || o.Name == "Scan" || o.Name == "ExecuteStatement" {
				// Count: fixed returned-item count per select request, including empty responses.
				cw.EmitStats(st, "aws_dynamodb_returned_item_count", op, cw.StatSet{Sum: n * o.ReturnedItems, Average: o.ReturnedItems, Maximum: o.ReturnedItems, Minimum: o.ReturnedItems, SampleCount: n})
			}
		}
		// Count: synthetic one-per-second capacity observations, including idle zero
		// samples. This explicit sampling assumption is not a captured AWS cadence.
		samples := math.Ceil(seconds)
		// All operation schedules align their extra observations to leading slots;
		// the extrema are therefore realizable, not arbitrary multipliers.
		if r := t.Replication; r != nil {
			n := take(r.WritesPerSecond, seconds, &ts.replicas)
			writeSum = n * r.UnitsPerWrite
			writePeak = math.Ceil(n/samples) * r.UnitsPerWrite
			writeMin = math.Floor(n/samples) * r.UnitsPerWrite
		}
		cw.EmitStats(st, "aws_dynamodb_consumed_read_capacity_units", l, cw.StatSet{Sum: readSum, Average: readSum / samples, Maximum: readPeak, Minimum: readMin, SampleCount: samples})
		wl := c.labels(t.CollectorName)
		wl["dimension_TableName"] = t.Name
		if t.WriteSource != "" {
			wl["dimension_Source"] = t.WriteSource
		}
		cw.EmitStats(st, "aws_dynamodb_consumed_write_capacity_units", wl, cw.StatSet{Sum: writeSum, Average: writeSum / samples, Maximum: writePeak, Minimum: writeMin, SampleCount: samples})
		failures := take(t.ConditionalFailuresPerSecond, seconds, &ts.failures)
		if failures > 0 {
			cw.EmitStats(st, "aws_dynamodb_conditional_check_failed_requests", l, cw.StatSet{Sum: failures, Average: 1, Maximum: 1, Minimum: 1, SampleCount: failures})
		}
		if s := t.Stream; s != nil {
			n := take(s.RequestsPerSecond, seconds, &ts.polls)
			if n > 0 {
				sl := c.labels(t.CollectorName)
				sl["dimension_TableName"] = t.Name
				sl["dimension_Operation"] = "GetRecords"
				sl["dimension_StreamLabel"] = s.Label
				bytes := s.RecordsPerRequest * s.BytesPerRecord
				cw.EmitStats(st, "aws_dynamodb_returned_bytes", sl, cw.StatSet{Sum: n * bytes, Average: bytes, Maximum: bytes, Minimum: bytes, SampleCount: n})
				cw.EmitStats(st, "aws_dynamodb_returned_records_count", sl, cw.StatSet{Sum: n * s.RecordsPerRequest, Average: s.RecordsPerRequest, Maximum: s.RecordsPerRequest, Minimum: s.RecordsPerRequest, SampleCount: n})
				cw.EmitStats(st, "aws_dynamodb_successful_request_latency", sl, cw.StatSet{Sum: n * s.LatencyMS, Average: s.LatencyMS, Maximum: s.LatencyMS, Minimum: s.LatencyMS, SampleCount: n})
			}
		}
		provisionR += t.ProvisionedReads
		provisionW += t.ProvisionedWrites
		maxR = math.Max(maxR, t.ProvisionedReads)
		maxW = math.Max(maxW, t.ProvisionedWrites)
	}
	// Account/region Count and Percent stocks: one observation of configured quota
	// and complete declared provisioned inventory per reporting window. No tags or
	// table dimensions; ratios are provisioned/quota, NOT consumed/provisioned.
	a := c.account
	l := c.labels("global")
	cw.EmitStats(st, "aws_dynamodb_account_max_reads", l, cw.StatSet{Sum: a.MaxReads, Average: a.MaxReads, Maximum: a.MaxReads, Minimum: a.MaxReads, SampleCount: 1})
	cw.EmitStats(st, "aws_dynamodb_account_max_writes", l, cw.StatSet{Sum: a.MaxWrites, Average: a.MaxWrites, Maximum: a.MaxWrites, Minimum: a.MaxWrites, SampleCount: 1})
	cw.EmitStats(st, "aws_dynamodb_account_max_table_level_reads", l, cw.StatSet{Sum: a.MaxTableReads, Average: a.MaxTableReads, Maximum: a.MaxTableReads, Minimum: a.MaxTableReads, SampleCount: 1})
	cw.EmitStats(st, "aws_dynamodb_account_max_table_level_writes", l, cw.StatSet{Sum: a.MaxTableWrites, Average: a.MaxTableWrites, Maximum: a.MaxTableWrites, Minimum: a.MaxTableWrites, SampleCount: 1})
	rp, wp := 100*provisionR/a.MaxReads, 100*provisionW/a.MaxWrites
	mr, mw := 100*maxR/a.MaxTableReads, 100*maxW/a.MaxTableWrites
	cw.EmitStats(st, "aws_dynamodb_account_provisioned_read_capacity_utilization", l, cw.StatSet{Sum: rp, Average: rp, Maximum: rp, Minimum: rp, SampleCount: 1})
	cw.EmitStats(st, "aws_dynamodb_account_provisioned_write_capacity_utilization", l, cw.StatSet{Sum: wp, Average: wp, Maximum: wp, Minimum: wp, SampleCount: 1})
	cw.EmitStats(st, "aws_dynamodb_max_provisioned_table_read_capacity_utilization", l, cw.StatSet{Sum: mr, Average: mr, Maximum: mr, Minimum: mr, SampleCount: 1})
	cw.EmitStats(st, "aws_dynamodb_max_provisioned_table_write_capacity_utilization", l, cw.StatSet{Sum: mw, Average: mw, Maximum: mw, Minimum: mw, SampleCount: 1})
	c.last = now
	return w.Metrics.Write(ctx, st.Collect(now))
}
