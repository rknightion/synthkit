// SPDX-License-Identifier: AGPL-3.0-only

// Package coretest provides the shared test harness for construct/workload lanes:
// capturing sink writers, a ready World, and standard fixtures. Test-only.
package coretest

import (
	"context"
	"sort"
	"strconv"
	"sync"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/faro"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// MetricCapture is a core.MetricWriter that records every batch. It is concurrency-safe: the runner
// shares one capture across all per-blueprint goroutines (mirroring the real shared sinks), so Write
// and the readers are mutex-guarded.
type MetricCapture struct {
	mu      sync.Mutex
	Batches [][]promrw.Series
}

func (c *MetricCapture) Write(_ context.Context, batch []promrw.Series) error {
	cp := make([]promrw.Series, len(batch))
	copy(cp, batch)
	c.mu.Lock()
	c.Batches = append(c.Batches, cp)
	c.mu.Unlock()
	return nil
}

// All flattens every captured batch. (Names/Find/LabelKeys derive from All, so the lock lives here.)
func (c *MetricCapture) All() []promrw.Series {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []promrw.Series
	for _, b := range c.Batches {
		out = append(out, b...)
	}
	return out
}

// Names returns the sorted distinct series names captured.
func (c *MetricCapture) Names() []string {
	set := map[string]bool{}
	for _, s := range c.All() {
		set[s.Name] = true
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Find returns every captured sample of one series name.
func (c *MetricCapture) Find(name string) []promrw.Series {
	var out []promrw.Series
	for _, s := range c.All() {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out
}

// Exemplars flattens every exemplar across all captured series of one metric name.
// (Write deep-copies the batch, so Series.Exemplars are already captured.)
func (c *MetricCapture) Exemplars(name string) []promrw.Exemplar {
	var out []promrw.Exemplar
	for _, s := range c.Find(name) {
		out = append(out, s.Exemplars...)
	}
	return out
}

// LabelKeys returns the sorted union of label keys seen on a series name.
func (c *MetricCapture) LabelKeys(name string) []string {
	set := map[string]bool{}
	for _, s := range c.Find(name) {
		for k := range s.Labels {
			set[k] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// LogCapture is a core.LogWriter that records every stream. Concurrency-safe (shared across the
// runner's per-blueprint goroutines).
type LogCapture struct {
	mu      sync.Mutex
	Streams []loki.Stream
}

func (c *LogCapture) Write(_ context.Context, streams []loki.Stream) error {
	c.mu.Lock()
	c.Streams = append(c.Streams, streams...)
	c.mu.Unlock()
	return nil
}

// TraceCapture is a core.TraceWriter that records every resource block. Concurrency-safe (shared
// across the runner's per-blueprint goroutines).
type TraceCapture struct {
	mu        sync.Mutex
	Resources []otlp.Resource
}

func (c *TraceCapture) Write(_ context.Context, resources []otlp.Resource) error {
	c.mu.Lock()
	c.Resources = append(c.Resources, resources...)
	c.mu.Unlock()
	return nil
}

// RUMCapture is a core.RUMSink that records every beacon. It is concurrency-safe
// (shared across the runner's per-blueprint goroutines, mirroring the real Faro sink).
type RUMCapture struct {
	mu       sync.Mutex
	Payloads []faro.Payload
}

func (c *RUMCapture) Write(_ context.Context, payloads []faro.Payload) error {
	cp := make([]faro.Payload, len(payloads))
	copy(cp, payloads)
	c.mu.Lock()
	c.Payloads = append(c.Payloads, cp...)
	c.mu.Unlock()
	return nil
}

// All returns a snapshot of every captured beacon.
func (c *RUMCapture) All() []faro.Payload {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]faro.Payload, len(c.Payloads))
	copy(out, c.Payloads)
	return out
}

// World returns a ready test World wired to the given captures (any may be nil) with
// a default shape engine (Europe/Zurich, no incidents).
func World(m *MetricCapture, l *LogCapture, t *TraceCapture) *core.World {
	w := &core.World{Shape: shape.New("", nil), EmitSpanMetrics: true}
	if m != nil {
		w.Metrics = m
	}
	if l != nil {
		w.Logs = l
	}
	if t != nil {
		w.Traces = t
	}
	return w
}

// Env returns a standard production test environment.
func Env() *fixture.Env { return &fixture.Env{Name: "prod", Weight: 1.0} }

// Cloud returns a standard test cloud account with two NAT gateways.
func Cloud() *fixture.Cloud {
	return &fixture.Cloud{
		Provider:  "aws",
		AccountID: "111122223333",
		Region:    "us-east-1",
		VpcID:     "vpc-0test0001",
		NATGatewayIDs: []string{
			fixture.NATGatewayID("test", "prod", "nat", "0"),
			fixture.NATGatewayID("test", "prod", "nat", "1"),
		},
	}
}

// Cluster returns a standard 3-node test cluster with one 2-replica workload placed.
func Cluster() *fixture.Cluster {
	env, cloud := Env(), Cloud()
	cl := &fixture.Cluster{
		Name:  "test-prod-use1",
		Type:  "eks",
		Env:   env,
		Cloud: cloud,
		K8sMonitoring: fixture.K8sMonitoring{
			Enabled: true, ChartVersion: "4.1.4",
			Alloy: true, AlloyVersion: "v1.16.3",
			OpenCost: true, Kepler: true,
		},
	}
	for n := range 3 {
		ip := fixture.PrivateIP("test", cl.Name, "general", strconv.Itoa(n))
		cl.Nodes = append(cl.Nodes, fixture.Node{
			InstanceID:   fixture.EC2InstanceID("test", cl.Name, "general", strconv.Itoa(n)),
			Hostname:     fixture.NodeHostname(ip, cloud.Region),
			PrivateIP:    ip,
			InstanceType: "m6i.xlarge",
			NodeGroup:    "general",
		})
	}
	cl.Workloads = []fixture.Workload{{
		Name:      "test-api",
		Namespace: "test-api",
		Replicas:  2,
		PodNames:  []string{fixture.PodName("test", "test-api", 0), fixture.PodName("test", "test-api", 1)},
		NodeIdx:   []int{0, 1},
		// HPA + PVC are opt-in (realism); the default test workload declares both so the storage and
		// autoscaler families stay exercised.
		HasHPA:       true,
		VolumeClaims: []string{"data"},
	}}
	return cl
}

// DB returns a standard test database fixture for an engine ("postgres"|"mysql").
func DB(engine string) *fixture.DB {
	name := "test-db"
	host := name + ".abc123def456.us-east-1.rds.amazonaws.com"
	key := "postgresql://" + host + ":5432/app"
	version := "16.2"
	if engine == "mysql" {
		key = "tcp(" + host + ":3306)/app"
		version = "8.0.36"
	}
	db := &fixture.DB{
		Engine:        engine,
		EngineVersion: version,
		Name:          name,
		ServerID:      fixture.ServerID("test", name),
		InstanceKey:   key,
		Databases:     []string{"app"},
		Env:           Env(),
		Cloud:         Cloud(),
	}
	for i := range 5 {
		id := fixture.MySQLDigest("test", name, strconv.Itoa(i))
		if engine != "mysql" {
			id = fixture.PostgresQueryID("test", name, strconv.Itoa(i))
		}
		db.Queries = append(db.Queries, fixture.Query{
			ID: id, Text: "SELECT * FROM users WHERE id = ?", Tables: []string{"users"}, Slow: i == 0,
		})
	}
	return db
}

// Cache returns a standard test cache fixture.
func Cache() *fixture.Cache {
	return &fixture.Cache{
		Engine: "redis", EngineVersion: "7.1", Name: "test-sessions",
		NodeIDs: []string{"test-sessions-0001"}, Env: Env(), Cloud: Cloud(),
	}
}
