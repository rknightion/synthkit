// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rknightion/synthkit/internal/core"
)

// Load strict-parses one blueprint document, validates it (schema + references +
// registry), and resolves topology into fixtures + buildable instances. Every failure
// is loud and names the offending field plus the available alternatives.
func Load(data []byte, reg *core.Registry) (*Resolved, error) {
	var d Decl
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("blueprint: %w", err)
	}
	return loadDecl(&d, data, reg)
}

// LoadNamespaced is Load with the blueprint's name (and label, if explicitly set)
// prefixed by nsPrefix BEFORE resolution, so the determinism seed + all fixture
// identities + the stamped selector label are consistent with the namespaced name.
// nsPrefix is already sanitized by the caller (bpsource.SanitizeNS); "" ⇒ plain Load.
func LoadNamespaced(data []byte, nsPrefix string, reg *core.Registry) (*Resolved, error) {
	var d Decl
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("blueprint: %w", err)
	}
	if nsPrefix != "" {
		d.Name = nsPrefix + "/" + d.Name
		if d.Label != "" {
			d.Label = nsPrefix + "/" + d.Label
		}
	}
	return loadDecl(&d, data, reg)
}

// loadDecl is the shared tail of Load and LoadNamespaced: validate, resolve, stamp Source.
func loadDecl(d *Decl, data []byte, reg *core.Registry) (*Resolved, error) {
	if err := validateDecl(d); err != nil {
		return nil, err
	}
	res, err := resolve(d, reg)
	if err != nil {
		return nil, err
	}
	res.Source = string(data)
	return res, nil
}

// validateDecl checks the declaration's internal consistency before resolution.
func validateDecl(d *Decl) error {
	if d.Name == "" {
		return fmt.Errorf("blueprint: `name` is required")
	}
	bad := func(format string, args ...any) error {
		return fmt.Errorf("blueprint %q: %s", d.Name, fmt.Sprintf(format, args...))
	}
	if len(d.Environments) == 0 && len(d.Hosts) == 0 {
		return bad("at least one environment or host is required")
	}
	envNames := map[string]bool{}
	clusterNames := map[string]bool{}
	dbNames := map[string]bool{}
	cacheNames := map[string]bool{}
	for i := range d.Environments {
		e := &d.Environments[i]
		if e.Name == "" {
			return bad("environments[%d]: `name` is required", i)
		}
		if envNames[e.Name] {
			return bad("duplicate environment %q", e.Name)
		}
		envNames[e.Name] = true
		needsCloud := e.Cluster != nil || len(e.Databases) > 0 || len(e.Caches) > 0
		if needsCloud && e.Cloud == nil {
			return bad("environment %q declares cluster/databases/caches but no `cloud` block", e.Name)
		}
		if e.Cloud != nil {
			if e.Cloud.Provider != "aws" {
				return bad("environment %q: cloud.provider %q unsupported (v1 supports: aws)", e.Name, e.Cloud.Provider)
			}
			for f, v := range map[string]string{"account_id": e.Cloud.AccountID, "region": e.Cloud.Region, "vpc_id": e.Cloud.VpcID} {
				if v == "" {
					return bad("environment %q: cloud.%s is required", e.Name, f)
				}
			}
		}
		if e.Cluster != nil {
			if e.Cluster.Type != "eks" {
				return bad("environment %q: cluster.type %q unsupported (v1 supports: eks)", e.Name, e.Cluster.Type)
			}
			if e.Cluster.Name == "" {
				return bad("environment %q: cluster.name is required", e.Name)
			}
			if clusterNames[e.Cluster.Name] {
				return bad("duplicate cluster %q", e.Cluster.Name)
			}
			clusterNames[e.Cluster.Name] = true
			if len(e.Cluster.NodeGroups) == 0 {
				return bad("cluster %q: at least one node_group is required", e.Cluster.Name)
			}
			for _, g := range e.Cluster.NodeGroups {
				if g.Name == "" || g.InstanceType == "" {
					return bad("cluster %q: node_groups entries need name + instance_type", e.Cluster.Name)
				}
			}
		}
		for _, db := range e.Databases {
			if db.Engine != "postgres" && db.Engine != "mysql" && db.Engine != "docdb" && db.Engine != "neptune" {
				return bad("database %q: engine %q unsupported (postgres|mysql|docdb|neptune)", db.Name, db.Engine)
			}
			if db.Name == "" {
				return bad("environment %q: every database needs a name", e.Name)
			}
			if dbNames[db.Name] {
				return bad("duplicate database %q", db.Name)
			}
			dbNames[db.Name] = true
			// Emission switch (observability.cloudwatch / .dbo11y) needs no extra
			// validation for postgres/mysql: both default sensibly. For docdb/neptune,
			// the dbo11y lane is not supported — reject loudly if declared (I2 guard).
			if (db.Engine == "docdb" || db.Engine == "neptune") && db.Observability.dbo11yEnabled() {
				return bad("database %q: engine %q does not support dbo11y", db.Name, db.Engine)
			}
		}
		for _, c := range e.Caches {
			if c.Engine != "redis" {
				return bad("cache %q: engine %q unsupported (redis)", c.Name, c.Engine)
			}
			if c.Name == "" {
				return bad("environment %q: every cache needs a name", e.Name)
			}
			if cacheNames[c.Name] {
				return bad("duplicate cache %q", c.Name)
			}
			cacheNames[c.Name] = true
		}
	}
	wlNames := map[string]bool{}
	for i := range d.Workloads {
		w := &d.Workloads[i]
		if w.Type == "" || w.Name == "" {
			return bad("workloads[%d]: `type` and `name` are required", i)
		}
		if wlNames[w.Name] {
			return bad("duplicate workload %q", w.Name)
		}
		wlNames[w.Name] = true
		// A fanned workload (for_each_env / envs) binds to each target env's cluster, so it sets
		// no runs_on; a non-fanned workload requires one.
		fanned := w.ForEachEnv || len(w.Envs) > 0
		if fanned && w.RunsOn != "" {
			return bad("workload %q: for_each_env workloads must not set `runs_on` (they bind to each env's cluster)", w.Name)
		}
		if !fanned && w.RunsOn == "" {
			return bad("workload %q: `runs_on` is required", w.Name)
		}
	}
	for _, inc := range d.Incidents {
		// An incident fires EITHER a single mode (kind) OR a whole named scenario (scenario) —
		// exactly one (XOR). A scenario-ref carries its own (mode,target) tuples, so kind/target
		// must be empty on it; the resolver expands those effects under the incident's at/for.
		hasKind, hasScenario := inc.Kind != "", inc.Scenario != ""
		if hasKind == hasScenario {
			return bad("incidents entries need exactly one of `kind` or `scenario`")
		}
		if hasScenario && inc.Target != "" {
			return bad("incident scenario %q must not set `target` (the scenario's effects name their own targets)", inc.Scenario)
		}
		if inc.For == "" {
			return bad("incidents entries need `for`")
		}
		// Timing is EITHER `at` (one-shot absolute / daily-recurring HH:MM) OR `every` (interval-
		// recurring) — exactly one (XOR).
		hasAt, hasEvery := inc.At != "", inc.Every != ""
		if hasAt == hasEvery {
			return bad("incidents entries need exactly one of `at` (one-shot/daily) or `every` (interval-recurring)")
		}
		forDur, err := time.ParseDuration(inc.For)
		if err != nil {
			label := inc.Kind
			if hasScenario {
				label = inc.Scenario
			}
			return bad("incident %q: bad duration %q: %v", label, inc.For, err)
		}
		// Interval-recurring: `for` must be shorter than `every` (else the window is always-active
		// and the shape engine would silently drop it). Reject loudly here at the authoring layer.
		if hasEvery {
			everyDur, err := time.ParseDuration(inc.Every)
			if err != nil {
				return bad("incident %q: bad `every` duration %q: %v", inc.Kind, inc.Every, err)
			}
			if forDur >= everyDur {
				return bad("incident %q: `for` (%s) must be shorter than `every` (%s) — otherwise it is always-active", inc.Kind, inc.For, inc.Every)
			}
		}
	}
	return nil
}

// strictDecode re-decodes a yaml.Node into out with unknown fields rejected
// (yaml.v3 only enforces KnownFields on a Decoder, so the node round-trips through
// bytes). An empty/absent node is a valid empty config.
func strictDecode(node *yaml.Node, out any, where string) error {
	if node == nil || node.Kind == 0 || (node.Kind == yaml.MappingNode && len(node.Content) == 0) {
		return nil
	}
	b, err := yaml.Marshal(node)
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	return nil
}

// ValidateSet enforces cross-blueprint identity uniqueness for everything that
// disambiguates substrate-scoped series (ARCHITECTURE §5): blueprint names + labels,
// cluster names, database names, cache names, workload names. account_id/vpc reuse is
// deliberately allowed (labels, not series identity).
//
// Intentionally NOT checked, because their substrate identity is already blueprint-unique
// by construction: SM checks (config_version is seeded from the blueprint name, so same-named
// checks in two blueprints are still distinct series), Fleet collectors (collector_id is
// blueprint-name-seeded), and Cloudflare (ScopeBlueprint — carries the blueprint label).
// This check reads only fixtures, never construct config, so it stays decoupled from any
// specific construct kind.
//
// A database name is claimed if the DB fans into EITHER lane (RDS CloudWatch OR dbo11y) —
// the dbo11y series (database_observability_*) are substrate-scoped, so a CloudWatch-less
// dbo11y DB still needs cross-blueprint uniqueness. Claims dedup within a blueprint, so a
// DB that emits BOTH lanes (two construct instances naming the same DB) is not a self-collision.
func ValidateSet(set []*Resolved) error {
	type owner struct{ what, name, bp string }
	seen := map[string]owner{}
	claim := func(what, name, bp string) error {
		key := what + ":" + name
		if prev, dup := seen[key]; dup {
			return fmt.Errorf("blueprint identity collision: %s %q declared by both %q and %q — substrate series would collide",
				what, name, prev.bp, bp)
		}
		seen[key] = owner{what, name, bp}
		return nil
	}
	for _, r := range set {
		// Dedup within this blueprint so the same identity claimed by two construct
		// instances (e.g. a DB fanning into both rds and dbo11y) is claimed once.
		local := map[string]bool{}
		claimOnce := func(what, name string) error {
			key := what + ":" + name
			if local[key] {
				return nil
			}
			local[key] = true
			return claim(what, name, r.Name)
		}
		if err := claimOnce("blueprint", r.Name); err != nil {
			return err
		}
		if err := claimOnce("label", r.Label); err != nil {
			return err
		}
		for _, ci := range r.Constructs {
			switch ci.Kind {
			case KindK8sCluster:
				if err := claimOnce("cluster", ci.Fixtures.Cluster.Name); err != nil {
					return err
				}
			case KindRDS, KindDbo11yPostgres, KindDbo11yMySQL:
				if err := claimOnce("database", ci.Fixtures.DB.Name); err != nil {
					return err
				}
			case KindElastiCache:
				if err := claimOnce("cache", ci.Fixtures.Cache.Name); err != nil {
					return err
				}
			case KindHost:
				if err := claimOnce("host", ci.Fixtures.Host.Hostname); err != nil {
					return err
				}
			}
		}
		for _, w := range r.Workloads {
			if err := claimOnce("workload", w.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

// DescribeKinds renders a short human list of registered kinds (CLI help / errors).
func DescribeKinds(reg *core.Registry) string {
	return "constructs: " + strings.Join(reg.ConstructKinds(), ", ") +
		"; workloads: " + strings.Join(reg.WorkloadKinds(), ", ")
}
