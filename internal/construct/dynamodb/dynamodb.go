// SPDX-License-Identifier: AGPL-3.0-only

// Package dynamodb models explicit provisioned tables, independently of workloads.
// Names, units and dimension alternatives: signals/cw.md, cw-dynamodb, sourced
// from https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/metrics-dimensions.html.
// This consumer implements remote-write only, not native CloudWatch streams.
package dynamodb

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/state"
)

// Config describes the entire modelled account/region's provisioned inventory.
// No defaults invent traffic, quotas, resource names, capacity or latency.
type Config struct {
	Account Account `yaml:"account"`
	Tables  []Table `yaml:"tables"`
	// set is the blueprint's declared causal resources, injected by the resolver
	// (causal.Consumer) only when the blueprint declares at least one. Only an operation with
	// Resource set reads it.
	set causal.Set `yaml:"-"`
}

var _ causal.Consumer = (*Config)(nil)

// UseResources receives the declared causal resources (causal.Consumer).
func (c *Config) UseResources(s causal.Set) { c.set = s }

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
	// Load selects how the table's operation rates (and GlobalTable replicated
	// writes) follow time. Omitted or "constant" keeps the fixed operator rates.
	// "business" treats each declared rate as the PEAK (plateau) rate and scales it
	// by the blueprint business curve (never above 1, so the provisioned-capacity
	// guard checked at Build still holds). An operation's own load overrides this.
	Load string `yaml:"load"`
}

// Operation models successful service-side requests only (not network/client
// time). Fixed latency, units and returned-items per request are explicit
// operator assumptions, not captured numeric distributions. ReadUnits can be
// fractional (e.g. eventual reads); WriteUnits is capacity, not item bytes.
// ExecuteStatement here means SELECT only. No grouped captured enum is expanded.
// UpdateItem is a separately declared write operation: its requests are customer
// writes like PutItem and consume write capacity at the table, and only
// SuccessfulRequestLatency (TableName+Operation) is emitted for it. An operator
// models an update-heavy table by splitting the write demand between PutItem and
// UpdateItem, never by adding to it.
type Operation struct {
	Name              string  `yaml:"name"`
	RequestsPerSecond float64 `yaml:"requests_per_second"`
	ReadUnits         float64 `yaml:"read_units"`
	WriteUnits        float64 `yaml:"write_units"`
	LatencyMS         float64 `yaml:"latency_ms"`
	ReturnedItems     float64 `yaml:"returned_items"`
	// Load overrides the table's load for this operation: "constant" or "business"
	// (see Table.Load). Omitted inherits the table.
	Load string `yaml:"load"`
	// LatencyMinMS optionally declares the lower per-request latency bound around
	// LatencyMS (the mean), as an explicit operator assumption. Declared together with
	// LatencyMaxMS (both or neither), with LatencyMinMS <= LatencyMS <= LatencyMaxMS.
	// Declared, the reported Minimum/Maximum differ from the Average (and the Average
	// drifts slightly per window) instead of all three equalling LatencyMS. Omitted
	// keeps constant latency.
	LatencyMinMS float64 `yaml:"latency_min_ms"`
	// LatencyMaxMS is the upper per-request latency bound; see LatencyMinMS.
	LatencyMaxMS float64 `yaml:"latency_max_ms"`
	// ThrottledRequestsPerSecond optionally declares requests of this operation rejected
	// for exceeding provisioned throughput, an explicit operator assumption (omitted or
	// zero: no throttling and no throttle series). Throttled requests are separate from
	// requests_per_second: they consume no capacity and have no latency or returned
	// items. Admitted only for GetItem and PutItem, the operations a lab capture of a
	// provisioned table observed. Each throttled request is one sample of 1 in
	// ThrottledRequests (TableName+Operation) and in ReadThrottleEvents (GetItem) or
	// WriteThrottleEvents (PutItem) on TableName; unthrottled requests add no zero
	// samples. Follows the operation's load.
	ThrottledRequestsPerSecond float64 `yaml:"throttled_requests_per_second"`
	// Resource binds the operation to a declared `quota` resource of the blueprint's causal
	// model (environments[].resources), counted in QuotaUnit. The quota is then the operation's
	// whole arrival declaration: its admitted volume is the successful requests (consuming
	// capacity and observing latency) and its rejected volume the throttled requests
	// (ThrottledRequests, and ReadThrottleEvents for GetItem or WriteThrottleEvents for PutItem),
	// read from the shared model, never re-simulated. A bound operation omits
	// requests_per_second, throttled_requests_per_second and load; read_units or write_units,
	// latency and the latency bounds keep their meaning. Admitted only for GetItem and PutItem,
	// the operations whose throttle families a lab capture observed. One resource binds at most
	// one operation. A table whose GetItem and PutItem operations are all bound is not a
	// table-axis target: quota_reduced on the resource throttles it instead. Omitted keeps the
	// declared-rate behaviour, so default output is unchanged.
	Resource string `yaml:"resource"`
	// QuotaUnit is the unit the bound quota's demand and limit_rps count, per second: requests,
	// read_units (GetItem) or write_units (PutItem). Required with resource and only with it.
	// Capacity units convert to requests by the operation's own read_units or write_units per
	// request, the AWS provisioned-capacity rule
	// (https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/provisioned-capacity-mode.html):
	// one RCU is one strongly consistent read per second, or two eventually consistent reads,
	// of an item up to 4 KB, and one WCU is one write per second of an item up to 1 KB, item
	// sizes rounding up to the next unit. A quota in units is the table capacity the operation
	// draws on, so its limit_rps (with the table's other declared demand) must fit the
	// provisioned capacity.
	QuotaUnit string `yaml:"quota_unit"`
}

// Quota units of a bound operation.
const (
	unitRequests   = "requests"
	unitReadUnits  = "read_units"
	unitWriteUnits = "write_units"
)

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
	business                  []bool
	replBusiness              bool
	failures, polls, replicas float64
	throttles                 []float64
	// stormThrottles carries the fractions of incident-driven throttles per operation,
	// separate from the declared baseline so an incident never changes the healthy draw.
	stormThrottles []float64
	// resource is each operation's resolved bound quota name ("" when unbound), and unitsPer
	// the quota units one request of it counts (1 for requests).
	resource []string
	unitsPer []float64
}

const (
	loadConstant = "constant"
	loadBusiness = "business"
	// ampLoad bounds the deterministic per-series reduction below the declared peak,
	// so peer operations sharing the curve do not move rigidly in parallel.
	ampLoad = 0.075
	// ampLatency bounds the per-window drift of mean latency when bounds are declared.
	ampLatency = 0.05
)

func validLoad(v string) bool { return v == "" || v == loadConstant || v == loadBusiness }

type construct struct {
	// model is the compiled causal model; nil when no operation is bound.
	model   *causal.Model
	account Account
	cloud   fixture.Cloud
	tables  []tableState
	last    time.Time
	paused  bool
}

// AxisTable is the failure axis of one declared table, addressed by its TableName.
const AxisTable = failuremode.AxisTable

// ModeThrottleStorm is the table-axis incident: requests of the throttle-captured operations
// (GetItem, PutItem) are additionally rejected for exceeding provisioned throughput. The extra
// throttled requests in a window are intensity x that operation's admitted requests, so
// ThrottledRequests (TableName+Operation) and ReadThrottleEvents/WriteThrottleEvents (TableName)
// rise from absent to positive while it is active. Admitted requests, consumed capacity and
// latency are unchanged, mirroring the Lambda function_throttle_storm. Query, Scan and
// ExecuteStatement were not captured and are unaffected. The magnitude is a synthetic operator
// assumption, not an AWS behaviour curve.
const ModeThrottleStorm = "table_throttle_storm"

// FailureModes is the table-axis vocabulary this construct implements.
var FailureModes = []failuremode.Mode{
	{Name: ModeThrottleStorm, Axis: AxisTable, Help: "table GetItem and PutItem requests are throttled on top of admitted requests (extra ThrottledRequests, ReadThrottleEvents and WriteThrottleEvents = intensity x requests)"},
}

func Registration() core.ConstructReg {
	modes := append(append([]failuremode.Mode(nil), FailureModes...), causal.FailureModes...)
	return core.ConstructReg{Kind: "dynamodb", Doc: "Explicit provisioned DynamoDB tables (remote-write only)", Scope: core.ScopeBlueprint, NewConfig: func() any { return &Config{} }, Build: Build, FailureModes: modes}
}

// FailureTargets reports each declared table name as a table-axis failure target. The blueprint
// resolver duck-types it; this package does not import the blueprint package. A table with a
// bound operation and no unbound GetItem or PutItem is omitted: its throttles come from its
// quotas, so a table-axis incident naming it is an unknown target at load instead of a silent
// no-op. A table with no bound operation is always a target.
func (c *Config) FailureTargets() []failuremode.Target {
	out := make([]failuremode.Target, 0, len(c.Tables))
	for _, t := range c.Tables {
		bound, storm := false, false
		for _, o := range t.Operations {
			if o.Resource != "" {
				bound = true
			} else if o.Name == "GetItem" || o.Name == "PutItem" {
				storm = true
			}
		}
		if bound && !storm {
			continue
		}
		out = append(out, failuremode.Target{Name: t.Name, Axis: AxisTable})
	}
	return out
}

// stormIntensity is the table's throttle-storm intensity this tick; zero when none is active.
func stormIntensity(w *core.World, now time.Time, table string) float64 {
	if w == nil || w.Shape == nil {
		return 0
	}
	if on, inten := w.Shape.Eval(now, ModeThrottleStorm, table); on {
		return inten
	}
	return 0
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
	bound := map[string]string{}
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
		if !validLoad(t.Load) {
			return nil, fmt.Errorf("dynamodb: load must be omitted, constant or business")
		}
		if t.WriteSource != "" && t.WriteSource != "Customer" && t.WriteSource != "GlobalTable" {
			return nil, fmt.Errorf("dynamodb: write_source must be omitted, Customer or GlobalTable")
		}
		if !number(t.ConditionalFailuresPerSecond) {
			return nil, fmt.Errorf("dynamodb: invalid conditional failure rate")
		}
		seen := map[string]bool{}
		var demandR, demandW float64
		resources, unitsPer := make([]string, len(t.Operations)), make([]float64, len(t.Operations))
		for j, o := range t.Operations {
			if seen[o.Name] {
				return nil, fmt.Errorf("dynamodb: duplicate operation %q", o.Name)
			}
			seen[o.Name] = true
			for _, v := range []float64{o.RequestsPerSecond, o.ReadUnits, o.WriteUnits, o.LatencyMS, o.ReturnedItems, o.LatencyMinMS, o.LatencyMaxMS, o.ThrottledRequestsPerSecond} {
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
			case "PutItem", "UpdateItem":
				if o.ReadUnits != 0 || o.WriteUnits == 0 || o.ReturnedItems != 0 {
					return nil, fmt.Errorf("dynamodb: %s requires write_units only, no ReturnedItemCount", o.Name)
				}
			default:
				return nil, fmt.Errorf("dynamodb: unsupported operation %q", o.Name)
			}
			if o.ThrottledRequestsPerSecond != 0 && o.Name != "GetItem" && o.Name != "PutItem" {
				return nil, fmt.Errorf("dynamodb: throttled_requests_per_second is admitted only for GetItem and PutItem")
			}
			if o.ThrottledRequestsPerSecond != 0 && o.Name == "PutItem" && t.WriteSource == "GlobalTable" {
				return nil, fmt.Errorf("dynamodb: GlobalTable excludes customer PutItem operations, including throttled ones")
			}
			if o.LatencyMS == 0 {
				return nil, fmt.Errorf("dynamodb: positive service latency required")
			}
			if !validLoad(o.Load) {
				return nil, fmt.Errorf("dynamodb: load must be omitted, constant or business")
			}
			if (o.LatencyMinMS == 0) != (o.LatencyMaxMS == 0) || (o.LatencyMinMS != 0 && (o.LatencyMinMS > o.LatencyMS || o.LatencyMS > o.LatencyMaxMS)) {
				return nil, fmt.Errorf("dynamodb: latency_min_ms and latency_max_ms must be declared together with latency_min_ms <= latency_ms <= latency_max_ms")
			}
			if o.Resource != "" {
				res, limit, per, err := validateBinding(cfg, t.Name, o, fx, bound)
				if err != nil {
					return nil, err
				}
				bound[res] = t.Name + "/" + o.Name
				resources[j], unitsPer[j] = res, per
				// The quota admits at most limit_rps of its unit; the capacity that draws on
				// the table is that many requests' units.
				demandR += limit / per * o.ReadUnits
				demandW += limit / per * o.WriteUnits
				continue
			}
			if o.QuotaUnit != "" {
				return nil, fmt.Errorf("dynamodb: table %q operation %s: quota_unit requires resource", t.Name, o.Name)
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
		ts := tableState{table: t, carry: make([]float64, len(t.Operations)), throttles: make([]float64, len(t.Operations)), stormThrottles: make([]float64, len(t.Operations)), business: make([]bool, len(t.Operations)), replBusiness: t.Load == loadBusiness, resource: resources, unitsPer: unitsPer}
		for j, o := range t.Operations {
			ts.business[j] = o.Load == loadBusiness || (o.Load == "" && t.Load == loadBusiness)
		}
		c.tables = append(c.tables, ts)
		totalR += t.ProvisionedReads
		totalW += t.ProvisionedWrites
	}
	if totalR > cfg.Account.MaxReads || totalW > cfg.Account.MaxWrites {
		return nil, fmt.Errorf("dynamodb: total provisioned capacity exceeds account quota")
	}
	if len(bound) > 0 {
		m, err := causal.Compile(cfg.set)
		if err != nil {
			return nil, fmt.Errorf("dynamodb: causal model: %w", err)
		}
		c.model = m
	}
	return c, nil
}

// validateBinding checks one bound operation against the injected causal set and returns the
// resolved quota name, its limit_rps and the quota units one request of the operation counts.
func validateBinding(cfg *Config, table string, o Operation, fx *fixture.Set, bound map[string]string) (string, float64, float64, error) {
	where := fmt.Sprintf("dynamodb: table %q operation %s resource %q", table, o.Name, o.Resource)
	if o.Name != "GetItem" && o.Name != "PutItem" {
		return "", 0, 0, fmt.Errorf("%s: resource binding is admitted only for GetItem and PutItem", where)
	}
	var omit []string
	for _, v := range []struct {
		name string
		set  bool
	}{
		{"requests_per_second", o.RequestsPerSecond != 0},
		{"throttled_requests_per_second", o.ThrottledRequestsPerSecond != 0},
		{"load", o.Load != ""},
	} {
		if v.set {
			omit = append(omit, v.name)
		}
	}
	if len(omit) > 0 {
		return "", 0, 0, fmt.Errorf("%s: the quota supplies its requests, throttles and load: omit %s", where, strings.Join(omit, ", "))
	}
	var per float64
	switch o.QuotaUnit {
	case unitRequests:
		per = 1
	case unitReadUnits:
		if o.Name != "GetItem" {
			return "", 0, 0, fmt.Errorf("%s: quota_unit read_units is admitted only for GetItem", where)
		}
		per = o.ReadUnits
	case unitWriteUnits:
		if o.Name != "PutItem" {
			return "", 0, 0, fmt.Errorf("%s: quota_unit write_units is admitted only for PutItem", where)
		}
		per = o.WriteUnits
	default:
		return "", 0, 0, fmt.Errorf("%s: quota_unit %q must be requests, read_units or write_units", where, o.QuotaUnit)
	}
	if cfg.set.Empty() {
		return "", 0, 0, fmt.Errorf("%s: the blueprint declares no environments[].resources", where)
	}
	env := ""
	if fx.Env != nil {
		env = fx.Env.Name
	}
	r, _, ok := cfg.set.Resolve(o.Resource, env)
	if !ok {
		return "", 0, 0, fmt.Errorf("%s is not a declared resource", where)
	}
	if r.Kind != causal.KindQuota {
		return "", 0, 0, fmt.Errorf("%s is a %s resource; want kind quota", where, r.Kind)
	}
	if other, dup := bound[r.Name]; dup {
		return "", 0, 0, fmt.Errorf("dynamodb: operations %s and %s/%s bind the same resource %q; a quota binds one operation", other, table, o.Name, r.Name)
	}
	return r.Name, r.LimitRPS, per, nil
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

// loadFactor is the opt-in business-curve multiplier in [0,1]. The declared rate is
// the peak, so the product of the clamped curve and two per-series reductions (a
// stable Spread baseline and a slow Wander drift, both only ever below 1) never
// exceeds it and the Build-time provisioned-capacity guard keeps holding. It is 1
// when no shape engine is wired. Deterministic in (key, now): no RNG draws.
func loadFactor(w *core.World, now time.Time, key string) float64 {
	if w == nil || w.Shape == nil {
		return 1
	}
	bf := math.Min(1, math.Max(0, w.Shape.BusinessFactor(now)))
	return bf * (w.Shape.Spread(key, ampLoad) - ampLoad) * (w.Shape.Wander(key, now, ampLoad*0.4) - ampLoad*0.4)
}

// latencyStats returns the CloudWatch statistics for n successful requests. Without
// declared bounds latency is constant (Average=Minimum=Maximum=LatencyMS). With
// bounds the window mean drifts deterministically within +/-ampLatency of LatencyMS
// (kept inside the bounds) and the extremes approach the declared bounds as the
// sample count grows: one request has Min=Max=Average. Sum is always n*Average, and
// the extremes stay realizable for that sum.
func latencyStats(o Operation, n float64, now time.Time, key string) cw.StatSet {
	if o.LatencyMaxMS == 0 {
		return cw.StatSet{Sum: n * o.LatencyMS, Average: o.LatencyMS, Maximum: o.LatencyMS, Minimum: o.LatencyMS, SampleCount: n}
	}
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%s|%d", key, now.Truncate(time.Minute).Unix())
	u := float64(h.Sum64()%20_001)/10_000.0 - 1.0
	avg := math.Min(o.LatencyMaxMS, math.Max(o.LatencyMinMS, o.LatencyMS*(1+ampLatency*u)))
	if n < 2 {
		return cw.StatSet{Sum: n * avg, Average: avg, Maximum: avg, Minimum: avg, SampleCount: n}
	}
	q := 1 - 1/n
	hi := avg + (o.LatencyMaxMS-avg)*q
	lo := avg - (avg-o.LatencyMinMS)*q
	hi = math.Min(hi, n*avg-(n-1)*lo)
	lo = math.Max(lo, n*avg-(n-1)*hi)
	// Floating-point rounding can cross the extremes by a few ulps when the mean sits
	// on a bound; the mean always lies between them.
	lo, hi = math.Min(lo, avg), math.Max(hi, avg)
	return cw.StatSet{Sum: n * avg, Average: avg, Maximum: hi, Minimum: lo, SampleCount: n}
}

func (c *construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	if !c.last.IsZero() && !now.After(c.last) {
		return nil
	}
	if w == nil || w.Metrics == nil {
		return fmt.Errorf("dynamodb: metric writer required")
	}
	c.advance(now, w.Shape)
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
	// Nonzero master volume retains the fixed operator rate model unless a table or
	// operation opts in with load: business, which scales the declared (peak) rate
	// by the blueprint business curve. Overload semantics are not admitted.
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
	// The observation window is (from, now]: the causal model's grid steps inside it supply a
	// bound operation's counts.
	from := now.Add(-time.Duration(seconds * float64(time.Second)))
	var provisionR, provisionW, maxR, maxW float64
	for i := range c.tables {
		ts := &c.tables[i]
		t := ts.table
		l := c.labels(t.CollectorName)
		l["dimension_TableName"] = t.Name
		var readSum, writeSum, readPeak, writePeak, readMin, writeMin, readThrottled, writeThrottled float64
		storm := stormIntensity(w, now, t.Name)
		for j, o := range t.Operations {
			var n, throttled float64
			if res := ts.resource[j]; res != "" {
				// Bound: the quota's admitted and rejected volumes over the window, in its unit,
				// converted to whole requests through the ordinary carries.
				n, throttled = c.boundWindow(ts, j, res, from, now)
			} else {
				rate, throttleRate := o.RequestsPerSecond, o.ThrottledRequestsPerSecond
				if ts.business[j] {
					f := loadFactor(w, now, "dynamodb|"+t.Name+"|"+o.Name)
					rate *= f
					throttleRate *= f
				}
				n = take(rate, seconds, &ts.carry[j])
				// Rejected requests: no capacity consumed and no successful-request series.
				throttled = take(throttleRate, seconds, &ts.throttles[j])
				if storm > 0 && (o.Name == "GetItem" || o.Name == "PutItem") {
					throttled += take(n*storm, 1, &ts.stormThrottles[j])
				}
			}
			if throttled > 0 {
				op := c.labels(t.CollectorName)
				op["dimension_TableName"] = t.Name
				op["dimension_Operation"] = o.Name
				cw.EmitStats(st, "aws_dynamodb_throttled_requests", op, cw.StatSet{Sum: throttled, Average: 1, Maximum: 1, Minimum: 1, SampleCount: throttled})
				if o.Name == "GetItem" {
					readThrottled += throttled
				} else {
					writeThrottled += throttled
				}
			}
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
			cw.EmitStats(st, "aws_dynamodb_successful_request_latency", op, latencyStats(o, n, now, "dynamodb|"+t.Name+"|"+o.Name))
			if o.Name == "Query" || o.Name == "Scan" || o.Name == "ExecuteStatement" {
				// Count: fixed returned-item count per select request, including empty responses.
				cw.EmitStats(st, "aws_dynamodb_returned_item_count", op, cw.StatSet{Sum: n * o.ReturnedItems, Average: o.ReturnedItems, Maximum: o.ReturnedItems, Minimum: o.ReturnedItems, SampleCount: n})
			}
		}
		if readThrottled > 0 {
			cw.EmitStats(st, "aws_dynamodb_read_throttle_events", l, cw.StatSet{Sum: readThrottled, Average: 1, Maximum: 1, Minimum: 1, SampleCount: readThrottled})
		}
		if writeThrottled > 0 {
			cw.EmitStats(st, "aws_dynamodb_write_throttle_events", l, cw.StatSet{Sum: writeThrottled, Average: 1, Maximum: 1, Minimum: 1, SampleCount: writeThrottled})
		}
		// Count: synthetic one-per-second capacity observations, including idle zero
		// samples. This explicit sampling assumption is not a captured AWS cadence.
		samples := math.Ceil(seconds)
		// All operation schedules align their extra observations to leading slots;
		// the extrema are therefore realizable, not arbitrary multipliers.
		if r := t.Replication; r != nil {
			rate := r.WritesPerSecond
			if ts.replBusiness {
				rate *= loadFactor(w, now, "dynamodb|"+t.Name+"|replication")
			}
			n := take(rate, seconds, &ts.replicas)
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

// staticClock stands in for the shape engine when a tick has none (unit tests): no faults, full
// business factor and volume.
type staticClock struct{}

func (staticClock) Eval(time.Time, string, string) (bool, float64) { return false, 0 }
func (staticClock) Factor(time.Time, float64, bool) float64        { return 1 }
func (staticClock) VolumeMultiplier() float64                      { return 1 }

var _ causal.Advancer = (*construct)(nil)

// AdvanceCausal advances the construct's model to now (causal.Advancer); the runner calls it on
// every master tick so every consumer samples live faults at the same instants. A construct with
// no bound operation holds no model and ignores it.
func (c *construct) AdvanceCausal(now time.Time, clk causal.Clock) {
	if c.model != nil {
		c.model.Advance(now, clk)
	}
}

// advance brings the model to now before a Tick reads its window (idempotent after the runner's
// AdvanceCausal).
func (c *construct) advance(now time.Time, eng *shape.Engine) {
	if c.model == nil {
		return
	}
	if eng != nil {
		c.model.Advance(now, eng)
		return
	}
	c.model.Advance(now, staticClock{})
}

// boundWindow reads operation j's quota over (from, to] and returns its successful and throttled
// requests. The quota counts its unit; dividing by the units one request counts gives requests,
// and the operation's carries keep fractions so rates below one request per window still accrue.
func (c *construct) boundWindow(ts *tableState, j int, resource string, from, to time.Time) (n, throttled float64) {
	win, ok := c.model.Window(resource, from, to)
	if !ok {
		return 0, 0
	}
	per := ts.unitsPer[j]
	return take(win.Admitted/per, 1, &ts.carry[j]), take(win.Rejected/per, 1, &ts.throttles[j])
}
