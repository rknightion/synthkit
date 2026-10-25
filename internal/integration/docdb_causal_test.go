// SPDX-License-Identifier: AGPL-3.0-only

package integration_test

import (
	"context"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/shape"
)

// loadDocDBCausal loads the fixture through the real loader and builds every docdb construct,
// the way the composition root does: bindings are validated by the construct's own Build.
func loadDocDBCausal(t *testing.T, mutate func(string) string) (*blueprint.Resolved, error) {
	t.Helper()
	data, err := os.ReadFile("../construct/docdb/testdata/docdb-dependency-slow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if mutate != nil {
		text = mutate(text)
	}
	reg := runner.Catalog()
	res, err := blueprint.Load([]byte(text), reg)
	if err != nil {
		return nil, err
	}
	for _, ci := range res.Constructs {
		if ci.Kind != "docdb" {
			continue
		}
		cr, _ := reg.Construct(ci.Kind)
		if _, err := cr.Build(ci.Config, ci.Fixtures); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// docdbMinute is one tick's emitted values for one cluster.
type docdbMinute struct{ read, write, conns, cpu float64 }

// runDocDB ticks every docdb construct of the loaded blueprint once a minute for two hours of a
// shape engine that schedules entries, and returns each cluster's per-minute values by name.
func runDocDB(t *testing.T, res *blueprint.Resolved, entries []string, legacyConfig bool) map[string]map[int]docdbMinute {
	t.Helper()
	eng := shape.New("UTC", entries)
	reg := runner.Catalog()
	out := map[string]map[int]docdbMinute{}
	start := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	for _, ci := range res.Constructs {
		if ci.Kind != "docdb" {
			continue
		}
		cr, _ := reg.Construct(ci.Kind)
		cfg := ci.Config
		if legacyConfig {
			cfg = cr.NewConfig() // a fresh Config is the unbound legacy path
		}
		c, err := cr.Build(cfg, ci.Fixtures)
		if err != nil {
			t.Fatal(err)
		}
		out[ci.Name] = map[int]docdbMinute{}
		for i := 1; i <= 120; i++ {
			mc := &coretest.MetricCapture{}
			if err := c.Tick(context.Background(), start.Add(time.Duration(i)*time.Minute), &core.World{Shape: eng, Metrics: mc}); err != nil {
				t.Fatal(err)
			}
			var m docdbMinute
			for _, s := range mc.All() {
				if s.Labels["dimension_Role"] != "WRITER" {
					continue
				}
				switch s.Name {
				case "aws_docdb_read_latency_average":
					m.read = s.Value
				case "aws_docdb_write_latency_average":
					m.write = s.Value
				case "aws_docdb_database_connections_average":
					m.conns = s.Value
				case "aws_docdb_cpuutilization_average":
					m.cpu = s.Value
				}
			}
			out[ci.Name][i] = m
		}
	}
	if len(out) != 3 {
		t.Fatalf("want three docdb constructs, built %d", len(out))
	}
	return out
}

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b)) }

// A cluster bound to a declared `dependency` resource, loaded through the real loader and
// catalog, has its latency families scaled by the dependency's latency factor: dependency_slow
// raises them six-fold while it is active and they recover afterwards. A cluster bound to a
// `pool` held across that dependency follows the same factor and takes its connection count
// from the pool's in-use connections, which rise as the hold stretches. An unbound cluster in
// the same blueprint is unchanged by the incident and equals the legacy path.
func TestDocDBBoundClusterFollowsDependencySlowAndRecovers(t *testing.T) {
	res, err := loadDocDBCausal(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Incidents) != 1 || !strings.HasPrefix(res.Incidents[0], "dependency_slow@2026-10-24T10:30/10m#1@records-db") {
		t.Fatalf("compiled incidents %v", res.Incidents)
	}
	healthy := runDocDB(t, res, nil, false)
	faulted := runDocDB(t, res, res.Incidents, false)
	legacy := runDocDB(t, res, res.Incidents, true)

	// The unbound control: no resource, so the incident reaches it not at all, and its output is
	// the legacy path's, minute for minute.
	for i := 1; i <= 120; i++ {
		if faulted["docdb-plain"][i] != healthy["docdb-plain"][i] || faulted["docdb-plain"][i] != legacy["docdb-plain"][i] {
			t.Fatalf("minute %d: unbound cluster %+v with the incident, %+v without, %+v on a fresh config", i,
				faulted["docdb-plain"][i], healthy["docdb-plain"][i], legacy["docdb-plain"][i])
		}
	}

	for i := 1; i <= 120; i++ {
		plain, dep, pool := healthy["docdb-plain"][i], healthy["docdb-dependency"][i], healthy["docdb-pool"][i]
		// Healthy, the latency factor is 1, so a bound cluster's latency and untouched families equal
		// the unbound cluster's; a dependency-bound cluster keeps the baseline connections.
		if dep.read != plain.read || dep.write != plain.write || dep.cpu != plain.cpu || dep.conns != plain.conns {
			t.Fatalf("minute %d healthy: dependency-bound %+v, unbound %+v", i, dep, plain)
		}
		if pool.read != plain.read || pool.write != plain.write || pool.cpu != plain.cpu {
			t.Fatalf("minute %d healthy: pool-bound %+v, unbound %+v", i, pool, plain)
		}
		// 100 rps holding a connection 10 ms is 1.0 busy connection.
		if !near(pool.conns, 1.0) {
			t.Fatalf("minute %d healthy: pool-bound connections %v, want 1.0", i, pool.conns)
		}
	}

	// Minute i covers (10:00+i-1m, 10:00+i m]; the fault is active over [10:30, 10:40), so minutes
	// 31 to 39 lie wholly inside it and minutes 30 and 40 each hold one step of the other state.
	for i := 1; i <= 29; i++ {
		if faulted["docdb-dependency"][i] != healthy["docdb-dependency"][i] || faulted["docdb-pool"][i] != healthy["docdb-pool"][i] {
			t.Fatalf("minute %d before the fault changed", i)
		}
	}
	for i := 31; i <= 39; i++ {
		plain := faulted["docdb-plain"][i]
		for _, name := range []string{"docdb-dependency", "docdb-pool"} {
			m := faulted[name][i]
			if !near(m.read, 6*plain.read) || !near(m.write, 6*plain.write) {
				t.Fatalf("minute %d %s: latency read %v write %v, want 6x the unbound %v / %v", i, name, m.read, m.write, plain.read, plain.write)
			}
			if m.cpu != plain.cpu {
				t.Fatalf("minute %d %s: cpu %v changed from %v (no sourced link to latency)", i, name, m.cpu, plain.cpu)
			}
		}
		if faulted["docdb-dependency"][i].conns != plain.conns {
			t.Fatalf("minute %d: dependency-bound connections %v moved from the baseline %v", i, faulted["docdb-dependency"][i].conns, plain.conns)
		}
		// The held-across dependency stretches the 10 ms hold to 35 ms: 3.5 busy connections.
		if !near(faulted["docdb-pool"][i].conns, 3.5) {
			t.Fatalf("minute %d: pool-bound connections %v, want 3.5", i, faulted["docdb-pool"][i].conns)
		}
	}
	// The edge minutes blend the two states strictly between them.
	for _, i := range []int{30, 40} {
		r := faulted["docdb-dependency"][i].read / faulted["docdb-plain"][i].read
		if !(r > 1.0000001 && r < 5.9999999) {
			t.Fatalf("minute %d: latency ratio %v, want a blend between 1 and 6", i, r)
		}
		c := faulted["docdb-pool"][i].conns
		if !(c > 1.0000001 && c < 3.4999999) {
			t.Fatalf("minute %d: pool connections %v, want a blend between 1 and 3.5", i, c)
		}
	}
	for i := 41; i <= 120; i++ {
		if faulted["docdb-dependency"][i] != healthy["docdb-dependency"][i] || faulted["docdb-pool"][i] != healthy["docdb-pool"][i] {
			t.Fatalf("minute %d after the fault: dependency %+v pool %+v, want recovered to %+v / %+v", i,
				faulted["docdb-dependency"][i], faulted["docdb-pool"][i], healthy["docdb-dependency"][i], healthy["docdb-pool"][i])
		}
	}
}

// A bound cluster is moved by the resource model alone: the loader rejects a database-axis
// failure mode scheduled against it (it names no target), while the same mode against the
// unbound cluster in the same blueprint still loads.
func TestDocDBBoundClusterRejectsLegacyDatabaseModes(t *testing.T) {
	for _, mode := range []string{"slow_query_storm", "connection_saturation"} {
		for _, target := range []string{"docdb-dependency", "docdb-pool"} {
			t.Run(mode+"/"+target, func(t *testing.T) {
				_, err := loadDocDBCausal(t, func(s string) string {
					return strings.Replace(s, "{mode: dependency_slow, target: records-db, intensity: 1}", "{mode: "+mode+", target: "+target+", intensity: 1}", 1)
				})
				if err == nil {
					t.Fatal("loader accepted a legacy database mode against a bound cluster")
				}
				if !strings.Contains(err.Error(), mode) || !strings.Contains(err.Error(), target) {
					t.Fatalf("error %q does not name the mode %q and the target %q", err, mode, target)
				}
			})
		}
		t.Run(mode+"/unbound", func(t *testing.T) {
			if _, err := loadDocDBCausal(t, func(s string) string {
				return strings.Replace(s, "{mode: dependency_slow, target: records-db, intensity: 1}", "{mode: "+mode+", target: docdb-plain, intensity: 1}", 1)
			}); err != nil {
				t.Fatalf("an unbound cluster must keep its database modes: %v", err)
			}
		})
	}
	// The wildcard reaches the unbound cluster only.
	res, err := loadDocDBCausal(t, func(s string) string {
		return strings.Replace(s, "{mode: dependency_slow, target: records-db, intensity: 1}", "{mode: slow_query_storm, target: \"database:*\", intensity: 1}", 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Incidents) != 1 || !strings.Contains(res.Incidents[0], "@docdb-plain") || strings.Contains(res.Incidents[0], "docdb-dependency") || strings.Contains(res.Incidents[0], "docdb-pool") {
		t.Fatalf("database:* expanded to %v, want the unbound cluster alone", res.Incidents)
	}
}

// The loader rejects a binding it cannot honour.
func TestDocDBBoundClusterDeclarationRejections(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(string) string
		want   string
	}{
		"resource that is not declared": {
			func(s string) string {
				return strings.Replace(s, "resource: records-db,", "resource: no-such-resource,", 1)
			},
			"no-such-resource",
		},
		"resource of the wrong kind": {
			func(s string) string {
				return strings.Replace(s, "resource: records-db,", "resource: intake-quota,", 1)
			},
			"quota",
		},
		"cloudwatch lane off": {
			func(s string) string {
				return strings.Replace(s, "resource: records-db, observability: {cloudwatch: true}", "resource: records-db, observability: {cloudwatch: false}", 1)
			},
			"cloudwatch",
		},
		"engine without binding support": {
			func(s string) string {
				return strings.Replace(s, "{engine: docdb, version: \"5.0\", name: docdb-plain, observability", "{engine: neptune, version: \"1.3\", name: docdb-plain, resource: records-db, observability", 1)
			},
			"does not support resource binding",
		},
		"no resources declared at all": {
			func(s string) string {
				return s[:strings.Index(s, "    resources:")]
			},
			"declares no environments[].resources",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadDocDBCausal(t, tc.mutate); err == nil {
				t.Fatal("loader accepted the declaration")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name %q", err, tc.want)
			}
		})
	}
	if _, err := loadDocDBCausal(t, nil); err != nil {
		t.Fatalf("the unmutated declaration must load: %v", err)
	}
}
