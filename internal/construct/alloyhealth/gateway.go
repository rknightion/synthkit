// SPDX-License-Identifier: AGPL-3.0-only

package alloyhealth

import (
	"fmt"
	"math"
	"net"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
	"github.com/rknightion/synthkit/internal/syslog"
)

// GatewayConfig declares a bounded operator pool, not vendor-discovered topology.
// All volume and sizing values are explicit operator assumptions; no emission floors.
type GatewayConfig struct {
	Pool                string          `yaml:"pool"`                   // Bounded cluster_name for this Alloy pool.
	Site                string          `yaml:"site"`                   // Documentation-only operator site, never a vendor label.
	PoolSize            int             `yaml:"pool_size"`              // Must equal the declared member inventory (2..16).
	Members             []GatewayMember `yaml:"members"`                // Declared virtual processes, not inferred GPU hosts.
	QueueCapacity       int             `yaml:"queue_capacity"`         // Requests/batches per member and signal; modeled persistence, not disk bytes.
	DrainRequestsPerMin float64         `yaml:"drain_requests_per_min"` // Successful send service capacity per signal and member.
	Inputs              []GatewayInput  `yaml:"inputs"`                 // Explicit incoming batches and constant items per batch.
	ScrapeTargets       int             `yaml:"scrape_targets"`         // Pool-wide bounded targets divided over live scrape-role members.
	SourceVictim        int             `yaml:"source_victim"`          // Zero-based local member for loss, reload and same-process source gap.
}

// GatewayMember configures one process and its roles. Roles are never emitted as labels.
type GatewayMember struct {
	Instance string   `yaml:"instance"` // Explicit scrape host:port (unique in this pool).
	Job      string   `yaml:"job"`      // Bounded operator scrape job.
	Roles    []string `yaml:"roles"`    // otlp_receive, central_scrape, syslog, cloud_forward.
}

// GatewayInput describes one signal's uniform per-member intake.
type GatewayInput struct {
	Signal          string  `yaml:"signal"`            // traces, metrics or logs.
	RequestsPerMin  float64 `yaml:"requests_per_min"`  // Incoming batches per enabled input role, shaped by business factor.
	ItemsPerRequest int     `yaml:"items_per_request"` // Spans, points or records per fixed-size modeled request.
}

type gatewayMember struct {
	cfg      GatewayMember
	st       *state.State
	queued   map[string]float64
	syslog   *syslog.Receiver
	assigned int
}
type gatewayPool struct {
	cfg     GatewayConfig
	members []gatewayMember
	last    time.Time
}

func gatewayModes() []failuremode.Mode {
	var out []failuremode.Mode
	for _, entry := range []struct{ name, help string }{
		{"gateway_instance_loss", "Configured member unavailable; live scrape members redistribute targets"},
		{"gateway_wan_outage", "Send attempts fail; retained request queues grow and later drain"},
		{"gateway_queue_overflow", "Unavailable egress fills bounded queues; excess requests rejected"},
		{"gateway_config_reload_failure", "Configured member remote configuration load fails"},
		{"gateway_cloud_credential_expired", "Cloud send attempts fail (generic authentication failure, no expiry-specific series)"},
		{"gateway_source_gap", "Omit configured member's same-process remotecfg data, retain healthy component observations"},
	} {
		out = append(out, failuremode.Mode{Name: entry.name, Axis: failuremode.AxisCluster, Help: entry.help})
	}
	return out
}

func newGateway(cfg *GatewayConfig, syscfg *syslog.Config) (*gatewayPool, error) {
	if cfg == nil {
		return nil, nil
	}
	if cfg.Pool == "" || cfg.Site == "" || len(cfg.Pool) > 63 || len(cfg.Site) > 63 {
		return nil, fmt.Errorf("alloyhealth: gateway pool/site must be nonempty bounded names")
	}
	if cfg.PoolSize < 2 || cfg.PoolSize > 16 || cfg.PoolSize != len(cfg.Members) {
		return nil, fmt.Errorf("alloyhealth: gateway pool_size must equal 2..16 declared members")
	}
	if cfg.SourceVictim < 0 || cfg.SourceVictim >= len(cfg.Members) {
		return nil, fmt.Errorf("alloyhealth: gateway source_victim outside member inventory")
	}
	if cfg.QueueCapacity < 1 || cfg.QueueCapacity > 100000 || !gatewayFinite(cfg.DrainRequestsPerMin) || cfg.DrainRequestsPerMin <= 0 {
		return nil, fmt.Errorf("alloyhealth: gateway requires bounded positive queue capacity and finite positive drain rate")
	}
	if cfg.ScrapeTargets < 0 || cfg.ScrapeTargets > 10000 {
		return nil, fmt.Errorf("alloyhealth: gateway scrape_targets must be 0..10000")
	}
	p := &gatewayPool{cfg: *cfg}
	seen := map[string]bool{}
	scrape, forward, syscount := 0, 0, 0
	for _, m := range cfg.Members {
		host, port, err := net.SplitHostPort(m.Instance)
		if err != nil || host == "" || port == "" || len(m.Instance) > 255 || m.Job == "" || len(m.Job) > 63 || seen[m.Instance] {
			return nil, fmt.Errorf("alloyhealth: gateway requires unique bounded job/instance host:port identities")
		}
		seen[m.Instance] = true
		roles := map[string]bool{}
		if len(m.Roles) == 0 {
			return nil, fmt.Errorf("alloyhealth: gateway member requires roles")
		}
		for _, role := range m.Roles {
			if roles[role] {
				return nil, fmt.Errorf("alloyhealth: duplicate gateway role %q", role)
			}
			roles[role] = true
			switch role {
			case "otlp_receive":
			case "central_scrape":
				scrape++
			case "cloud_forward":
				forward++
			case "syslog":
				syscount++
			default:
				return nil, fmt.Errorf("alloyhealth: unknown gateway role %q", role)
			}
		}
		member := gatewayMember{cfg: m, st: state.NewState(), queued: map[string]float64{}}
		if roles["syslog"] {
			if syscfg == nil {
				return nil, fmt.Errorf("alloyhealth: gateway syslog role requires syslog config")
			}
			recv, err := syslog.New(*syscfg)
			if err != nil {
				return nil, err
			}
			member.syslog = recv
		}
		p.members = append(p.members, member)
	}
	if cfg.ScrapeTargets > 0 && scrape == 0 {
		return nil, fmt.Errorf("alloyhealth: gateway scrape_targets requires central_scrape role")
	}
	if forward == 0 {
		return nil, fmt.Errorf("alloyhealth: gateway requires cloud_forward role")
	}
	if syscfg != nil && syscount == 0 {
		return nil, fmt.Errorf("alloyhealth: gateway syslog config requires syslog role")
	}
	signals := map[string]bool{}
	for _, input := range cfg.Inputs {
		if input.Signal != "traces" && input.Signal != "metrics" && input.Signal != "logs" {
			return nil, fmt.Errorf("alloyhealth: unknown gateway signal %q", input.Signal)
		}
		if signals[input.Signal] || !gatewayFinite(input.RequestsPerMin) || input.RequestsPerMin < 0 || input.RequestsPerMin > 100000 || input.ItemsPerRequest < 1 || input.ItemsPerRequest > 1000000 {
			return nil, fmt.Errorf("alloyhealth: gateway inputs require unique signals, bounded nonnegative rates and positive item counts")
		}
		signals[input.Signal] = true
	}
	if len(cfg.Inputs) == 0 {
		return nil, fmt.Errorf("alloyhealth: gateway requires explicit inputs")
	}
	return p, nil
}
func gatewayFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func (m *gatewayMember) role(role string) bool {
	for _, r := range m.cfg.Roles {
		if r == role {
			return true
		}
	}
	return false
}
func gatewaySuffix(signal string) string {
	switch signal {
	case "traces":
		return "spans"
	case "metrics":
		return "metric_points"
	default:
		return "log_records"
	}
}

// build advances only this pool's modeled state. Omission occurs here, before the
// public writer/inventory. A source gap never changes process/component health.
func (p *gatewayPool) build(now time.Time, factor float64, cluster string, eng *shape.Engine, syslogPerMin float64) []promrw.Series {
	// One initial 60s interval; subsequent ticks account for elapsed time. Duplicate
	// or backwards observations do not create traffic. State persists across losses.
	minutes := 1.0
	if !p.last.IsZero() {
		minutes = math.Max(0, now.Sub(p.last).Minutes())
	}
	if p.last.IsZero() || now.After(p.last) {
		p.last = now
	}
	active := func(name string) bool { return eng.Active(now, name, cluster) }
	loss := active("gateway_instance_loss")
	gap := active("gateway_source_gap")
	reload := active("gateway_config_reload_failure")
	blocked := active("gateway_wan_outage") || active("gateway_queue_overflow") || active("gateway_cloud_credential_expired")
	liveScrape := []int{}
	liveSyslog, liveOTLP, liveForward := 0, 0, 0
	for i := range p.members {
		m := &p.members[i]
		if loss && i == p.cfg.SourceVictim {
			continue
		}
		if m.role("central_scrape") {
			liveScrape = append(liveScrape, i)
		}
		if m.syslog != nil {
			liveSyslog++
		}
		if m.role("otlp_receive") {
			liveOTLP++
		}
		if m.role("cloud_forward") {
			liveForward++
		}
	}
	assignments := map[int]int{}
	for target := 0; target < p.cfg.ScrapeTargets && len(liveScrape) > 0; target++ {
		assignments[liveScrape[target%len(liveScrape)]]++
	}
	var out []promrw.Series
	for i := range p.members {
		m := &p.members[i]
		base := map[string]string{"cluster": cluster, "job": m.cfg.Job, "instance": m.cfg.Instance}
		alive := !(loss && i == p.cfg.SourceVictim)
		up := 0.0
		if alive {
			up = 1
		}
		m.st.Set("up", base, up)
		if !alive {
			// Unavailable process has only the scrape failure witness; persistent queues
			// survive in local model state, and counters do not reset on publication loss.
			for _, s := range m.st.Collect(now) {
				if s.Name == "up" && len(s.Labels) == len(base) {
					out = append(out, s)
				}
			}
			continue
		}
		health := copyLabels(base)
		health["controller_id"] = "gateway"
		health["health_type"] = "healthy"
		// One configured component per role in this simplified controller graph.
		m.st.Set("alloy_component_controller_running_components", health, float64(len(m.cfg.Roles)))
		peers := copyLabels(base)
		peers["cluster_name"] = p.cfg.Pool
		for _, status := range []string{"viewer", "participant", "terminating"} {
			l := copyLabels(peers)
			l["state"] = status
			v := 0.0
			if status == "participant" {
				v = float64(len(p.members))
				if loss {
					v--
				}
			}
			m.st.Set("cluster_node_peers", l, v)
		}
		n := float64(len(p.members))
		if loss {
			n--
		}
		m.st.Set("cluster_node_gossip_alive_peers", peers, n)
		// The service continues polling even during a collection/publication gap.
		failed := reload && i == p.cfg.SourceVictim
		success := 1.0
		failures := 0.0
		if failed {
			success = 0
			failures = minutes
		}
		m.st.Set("remotecfg_last_load_successful", base, success)
		m.st.Add("remotecfg_load_attempts_total", base, minutes)
		m.st.Add("remotecfg_load_failures_total", base, failures)
		if m.role("central_scrape") {
			labels := copyLabels(base)
			labels["component_id"] = "prometheus.scrape.gateway"
			assigned := assignments[i]
			// Count targets leaving this member only (the vendor counter counts moves
			// to another member). Initial placement is not a move.
			moved := math.Max(0, float64(m.assigned-assigned))
			m.assigned = assigned
			m.st.Set("prometheus_scrape_targets_gauge", labels, float64(assigned))
			m.st.Add("prometheus_scrape_targets_moved_total", labels, moved)
		}
		for _, input := range p.cfg.Inputs {
			requests := 0.0
			if m.role("otlp_receive") {
				requests = input.RequestsPerMin * factor * minutes
				labels := copyLabels(base)
				labels["receiver"] = "otlp/gateway"
				labels["transport"] = "grpc"
				suffix := gatewaySuffix(input.Signal)
				m.st.Add("otelcol_receiver_accepted_"+suffix+"_total", labels, requests*float64(input.ItemsPerRequest))
				m.st.Add("otelcol_receiver_refused_"+suffix+"_total", labels, 0)
				m.st.Add("otelcol_receiver_failed_"+suffix+"_total", labels, 0)
			}
			if input.Signal == "metrics" && m.role("central_scrape") {
				requests += input.RequestsPerMin * factor * minutes * float64(assignments[i])
			}
			if input.Signal == "logs" && m.syslog != nil && liveSyslog > 0 {
				requests += syslogPerMin * factor * minutes / float64(liveSyslog*input.ItemsPerRequest)
			}
			if !m.role("cloud_forward") {
				continue
			}
			// Internal role wiring sends all live pool intake to live forwarding
			// members evenly, permitting separately assigned roles without a receiver's
			// fictitious exporter queue. Incoming load on a lost receiver is not buffered.
			requests = input.RequestsPerMin * factor * minutes * float64(liveOTLP)
			if input.Signal == "metrics" && len(liveScrape) > 0 {
				requests += input.RequestsPerMin * factor * minutes * float64(p.cfg.ScrapeTargets)
			}
			if input.Signal == "logs" && liveSyslog > 0 {
				requests += syslogPerMin * factor * minutes / float64(input.ItemsPerRequest)
			}
			requests /= float64(liveForward)
			exporter := copyLabels(base)
			exporter["exporter"] = "otlphttp/gateway"
			queue := copyLabels(exporter)
			queue["data_type"] = input.Signal
			room := math.Max(0, float64(p.cfg.QueueCapacity)-m.queued[input.Signal])
			admitted := math.Min(room, requests)
			dropped := requests - admitted
			m.queued[input.Signal] += admitted
			sent, failedItems := 0.0, 0.0
			attempted := math.Min(m.queued[input.Signal], p.cfg.DrainRequestsPerMin*minutes)
			if blocked {
				failedItems = attempted * float64(input.ItemsPerRequest)
			} else {
				sent = attempted * float64(input.ItemsPerRequest)
				m.queued[input.Signal] -= attempted
			}
			suffix := gatewaySuffix(input.Signal)
			m.st.Add("otelcol_exporter_sent_"+suffix+"_total", exporter, sent)
			m.st.Add("otelcol_exporter_send_failed_"+suffix+"_total", exporter, failedItems)
			m.st.Add("otelcol_exporter_enqueue_failed_"+suffix+"_total", exporter, dropped*float64(input.ItemsPerRequest))
			m.st.Set("otelcol_exporter_queue_size", queue, m.queued[input.Signal])
			m.st.Set("otelcol_exporter_queue_capacity", queue, float64(p.cfg.QueueCapacity))
		}
		if m.syslog != nil && liveSyslog > 0 {
			m.syslog.Healthy(syslogPerMin * factor * minutes / float64(liveSyslog))
			out = append(out, m.syslog.Health(now, base)...)
		}
		for _, s := range m.st.Collect(now) {
			if gap && i == p.cfg.SourceVictim && strings.HasPrefix(s.Name, "remotecfg_") {
				continue
			}
			out = append(out, s)
		}
	}
	return out
}
