// SPDX-License-Identifier: AGPL-3.0-only

// Package nettopo is the network-topology-exporter data-plane construct. It synthesizes
// the full `network_topology_*` signal surface of a self-hosted SNMP-based topology
// discovery exporter (github.com/colinedwardwood/network-topology-exporter): device
// inventory, reconciled topology edges, change/conflict events, discovery-cycle &
// SNMP-walker health, graph freshness, OTLP-push accounting, federation hub/spoke
// liveness, SNMP session-pool stats, process self-observability, and Loki change-event
// logs — with no real SNMP poll executing.
//
// Kind: "network_topology"
// Scope: ScopeSubstrate — disambiguated by (job, instance); never stamped with a blueprint label.
// Group: GroupIntegration — declared under a blueprint's `integrations:` section.
// Signals: Metrics + Logs.
// Interval: 60s.
//
// Sub-families are PRESENCE-GATED (active when declared, like cwinfra — no enable flags):
//   - federation hub/spoke families ← role: hub|spoke
//   - SNMP session-pool family       ← session_pool: true
//   - out-of-scope / boundary-obs    ← out_of_scope_neighbours > 0
//   - OTLP-push family               ← otlp_output: true
//
// Signal contract: signals/nettopo.md. Realism mirrored from the exporter source at
// ../network-topology-exporter (captured 2026-06-17).
package nettopo

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/state"
)

// Kind is the registry key for this construct.
const Kind = "network_topology"

// ————————————————————————————————————————————————————————————————————————————
// Wire-value enums (verbatim from the exporter — changing these breaks the contract)
// ————————————————————————————————————————————————————————————————————————————

// Discovery protocol values (Edge.Proto / discovery_proto label / walker label).
const (
	ProtoLLDP       = "lldp"
	ProtoCDP        = "cdp"
	ProtoBGP        = "bgp"
	ProtoOSPF       = "ospf"
	ProtoFDB        = "fdb"
	ProtoISIS       = "isis"
	ProtoMPLSTE     = "mpls_te"
	ProtoConfigured = "configured"
)

// LinkKind values (Edge.LinkKind / link_kind label).
const (
	LinkEthernet = "ethernet"
	LinkMPLSTE   = "mpls-te"
	LinkIP       = "ip"
	LinkLogical  = "logical"
)

// Direction values.
const (
	DirBidirectional  = "bidirectional"
	DirUnidirectional = "unidirectional"
)

// Confidence buckets.
const (
	ConfHigh   = "high"
	ConfMedium = "medium"
	ConfLow    = "low"
)

// Adjacency classification.
const (
	AdjDirect   = "direct"
	AdjIndirect = "indirect"
	AdjUnknown  = "unknown"
)

// Federation roles.
const (
	RoleStandalone = "standalone"
	RoleHub        = "hub"
	RoleSpoke      = "spoke"
)

// ————————————————————————————————————————————————————————————————————————————
// Config (decoded from blueprint `integrations: network_topology:` YAML)
// ————————————————————————————————————————————————————————————————————————————

// Config is the blueprint-declared configuration for one exporter instance.
type Config struct {
	// Instance is the exporter scrape endpoint (e.g. "netobs-dc1:9100"). It is the
	// substrate disambiguator (with Job) and MUST be unique across blueprints. Required.
	Instance string `yaml:"instance"`
	// Job is the Prometheus `job` label. Default: "integrations/network-topology-exporter".
	Job string `yaml:"job"`
	// Role selects federation behaviour: standalone (default) | hub | spoke.
	Role string `yaml:"role"`
	// SpokeID is this spoke's identity (role=spoke); also service.instance.id. Required when role=spoke.
	SpokeID string `yaml:"spoke_id"`
	// Protocols lists which discovery walkers "run". Edges are only produced for listed
	// protocols; walker-outcome families are emitted per listed walker. Default: [lldp, bgp].
	Protocols []string `yaml:"protocols"`

	// Fabric is the optional topology generator.
	Fabric *FabricConfig `yaml:"fabric"`
	// Devices are explicit device declarations that augment/override the generated fabric (matched by id).
	Devices []DeviceConfig `yaml:"devices"`
	// Links are explicit link declarations that augment/override generated links.
	Links []LinkConfig `yaml:"links"`

	// SessionPool gates the snmp_session_pool_* family.
	SessionPool bool `yaml:"session_pool"`
	// OutOfScopeNeighbours sets the steady-state out-of-scope neighbour count (gates the OOS gauge,
	// and the hub boundary-observation series when role=hub).
	OutOfScopeNeighbours int `yaml:"out_of_scope_neighbours"`
	// OTLPOutput gates the otlp_push_total family.
	OTLPOutput bool `yaml:"otlp_output"`
	// Federation carries hub-mode wiring (the spokes this hub aggregates).
	Federation *FederationConfig `yaml:"federation"`
}

// FabricConfig parameterises the deterministic topology generator.
type FabricConfig struct {
	Kind         string   `yaml:"kind"`           // spine_leaf | clos | linear | star
	Spines       int      `yaml:"spines"`         // spine_leaf/clos
	Leaves       int      `yaml:"leaves"`         // spine_leaf/clos
	HostsPerLeaf int      `yaml:"hosts_per_leaf"` // optional access hosts (FDB edges)
	VendorMix    []string `yaml:"vendor_mix"`     // round-robin vendor assignment; default [arista]
	Site         string   `yaml:"site"`           // device `site` label
}

// DeviceConfig is one explicit device declaration.
type DeviceConfig struct {
	ID     string `yaml:"id"`
	Vendor string `yaml:"vendor"`
	// Model is accepted for back-compat parsing of existing blueprints but NOT emitted —
	// the real exporter has no model source (no ENTITY-MIB walk). Setting this field has
	// no effect on emitted metrics or labels.
	Model     string        `yaml:"model"`
	OSVersion string        `yaml:"os_version"`
	Site      string        `yaml:"site"`
	Uptime    time.Duration `yaml:"uptime"`
}

// LinkConfig is one explicit link declaration.
type LinkConfig struct {
	SrcDevice string `yaml:"src_device"`
	SrcPort   string `yaml:"src_port"`
	DstDevice string `yaml:"dst_device"`
	DstPort   string `yaml:"dst_port"`
	Proto     string `yaml:"proto"`
	LinkKind  string `yaml:"link_kind"`
}

// FederationConfig is hub-mode wiring.
type FederationConfig struct {
	Spokes []string `yaml:"spokes"`
}

// resolvedConfig is the fully-defaulted, validated configuration the construct emits from.
// Owned/produced by config.go (resolveConfig).
type resolvedConfig struct {
	instance    string
	job         string
	role        string
	spokeID     string
	protocols   []string // normalized + deduped, in ladder order
	protoSet    map[string]bool
	sessionPool bool
	oosCount    int
	otlpOutput  bool
	spokes      []string // hub federation spokes

	// generator inputs retained for generateGraph.
	fabric  *FabricConfig
	devices []DeviceConfig
	links   []LinkConfig
}

// ————————————————————————————————————————————————————————————————————————————
// Canonical graph model (mirrors exporter internal/discovery types)
// ————————————————————————————————————————————————————————————————————————————

// Device is one resolved network node.
type Device struct {
	ID        string
	Vendor    string
	OSVersion string
	Site      string
	// UptimeBaseSecs is the declared or hash-derived uptime at cold-start; deviceUptimeSecs
	// starts from this value on the first tick and grows, resetting on rare reboots. It is the
	// blueprint-declared `uptime:` for an explicit device, else a deterministic per-device base.
	UptimeBaseSecs float64
}

// Edge is one reconciled topology link.
type Edge struct {
	SrcDevice      string
	SrcPort        string
	DstDevice      string
	DstPort        string
	Proto          string // ProtoLLDP … ProtoConfigured
	LinkKind       string // LinkEthernet …
	Direction      string // DirBidirectional | DirUnidirectional
	Confidence     string
	Adjacency      string
	PrecedenceRank int
	Metadata       map[string]string // bgp.remote_as, mpls_te.admin_status, peer_chassis_mac, degraded, degraded_reason
}

// OOSNeighbour is one out-of-scope neighbour observation (LD-11).
type OOSNeighbour struct {
	ReportingDevice string
	ReportingPort   string
	NeighbourHint   string
	Proto           string
}

// Graph is the reconciled topology snapshot the construct emits from.
type Graph struct {
	Devices []Device
	Edges   []Edge
	OOS     []OOSNeighbour
}

// ————————————————————————————————————————————————————————————————————————————
// Construct
// ————————————————————————————————————————————————————————————————————————————

// Construct is one exporter-instance emitter. The resolved graph is fixed at Build time
// (deterministic from seed+instance); per-tick dynamics (uptime, churn counters, walker
// outcomes, durations) are derived from `now` + the shape engine, so there is no
// cross-tick mutable state beyond the cumulative counters held in st.
type Construct struct {
	rc    resolvedConfig
	graph Graph
	st    *state.State
	// seed is the blueprint seed resolved at Build time; used by dynamics helpers
	// (deviceUptimeSecs, osVersionIndex) so they key on the same entropy as graph generation.
	seed string
	// bootTime is the wall-clock of this instance's first Tick — the "process boot" /
	// first-discovery-cycle anchor. Zero until the first buildData call. Drives the
	// cold-start topology-discovery burst and the warm-up→steady churn decay, mirroring a
	// real exporter that adds its entire graph in cycle one then settles to rare changes.
	bootTime time.Time
}

// NewConfig returns an empty *Config for the YAML decoder.
func NewConfig() any { return &Config{} }

// Build validates cfg (*Config), resolves defaults, and generates the deterministic graph.
func Build(cfg any, fx *fixture.Set) (core.Construct, error) {
	c, ok := cfg.(*Config)
	if !ok {
		return nil, fmt.Errorf("nettopo: Build called with %T, want *Config", cfg)
	}
	seed := ""
	if fx != nil {
		seed = fx.Seed
	}
	rc, err := resolveConfig(c, seed)
	if err != nil {
		return nil, err
	}
	return &Construct{
		rc:    rc,
		graph: generateGraph(rc, seed),
		st:    state.NewState(),
		seed:  seed,
	}, nil
}

func (c *Construct) Kind() string                { return Kind }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics, core.Logs} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// Tick renders one 60s cycle: topology data + change/conflict logs (buildData) and the
// discovery-health / freshness / self-obs / gated families (buildHealth) both accumulate
// into c.st; the collected batch is written once.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	streams := c.buildData(now, w.Shape)
	c.buildHealth(now, w.Shape)

	series := c.st.Collect(now)
	if len(series) > 0 && w.Metrics != nil {
		if err := w.Metrics.Write(ctx, series); err != nil {
			return fmt.Errorf("nettopo: metrics write: %w", err)
		}
	}
	if len(streams) > 0 && w.Logs != nil {
		if err := w.Logs.Write(ctx, streams); err != nil {
			return fmt.Errorf("nettopo: logs write: %w", err)
		}
	}
	return nil
}

// baseLabels returns the substrate identity stamped on every series: {job, instance}.
// (No blueprint label — ScopeSubstrate.)
func (c *Construct) baseLabels() map[string]string {
	return map[string]string{"job": c.rc.job, "instance": c.rc.instance}
}

// seriesVar returns a stable-but-living per-series multiplier ≈ 1±amp (Spread baseline ×
// slow Wander drift). Returns 1.0 when eng is nil. Mirrors sm.go's seriesVar.
func (c *Construct) seriesVar(eng *shape.Engine, now time.Time, key string, amp float64) float64 {
	if eng == nil {
		return 1.0
	}
	return eng.Spread(key, amp) * eng.Wander(key, now, amp*0.4)
}
