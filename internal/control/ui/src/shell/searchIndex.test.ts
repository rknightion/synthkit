import { test, expect } from "vitest";
import { buildSearchIndex } from "./searchIndex";
import type { Snapshot } from "../store/store";
import type { State } from "../api/types";

function defaultState(over: Partial<State> = {}): State {
  return {
    volume_multiplier: 1,
    disabled_blueprints: [],
    failures: {},
    active_scenarios: [],
    scaling: {},
    disabled_constructs: [],
    disabled_kinds: [],
    span_metrics_blueprints: [],
    runtime_incidents: [],
    blueprint_sources: [],
    ...over,
  };
}

function snap(over: Partial<Snapshot> = {}): Snapshot {
  return { loading: false, errors: {}, state: defaultState(), ...over } as Snapshot;
}

test("indexes blueprints (inventory ∪ disabled), kinds, constructs, config keys, metric names", () => {
  const idx = buildSearchIndex(
    snap({
      state: defaultState({ disabled_blueprints: ["zeta"] }),
      inventory: {
        blueprints: [
          {
            blueprint: "alpha",
            distinct_series: 1,
            metric_names: 2,
            label_keys: 1,
            constructs: [{ kind: "ec2", name: "web", metric_names: ["cpu_total", "mem_total"] }],
          },
        ],
        totals: { distinct_series: 1, constructs: 1, blueprints: 1 },
      },
      schema: {
        volume_multiplier: { key: "volume_multiplier", type: "float", help: "", default: 1, min: 0, max: 10 },
        blueprints: ["alpha"],
        modes: [],
        targets: [],
        scenarios: [],
        constructs: [{ blueprint: "alpha", kind: "rds", name: "db", enabled: true }],
        kinds: ["ec2", "rds"],
      },
      config: { groups: [{ title: "Sinks", fields: [{ key: "GC_PROM_RW", value: "x", secret: false, configured: true }] }] },
    }),
  );
  const byType = (t: string) => idx.filter((e) => e.type === t).map((e) => e.label);

  // blueprints: inventory ∪ disabled, sorted
  expect(byType("blueprint")).toEqual(["alpha", "zeta"]);
  // ALL schema kinds (not just disabled)
  expect(byType("kind")).toEqual(["ec2", "rds"]);
  // ALL schema constructs, labelled "kind · name", routed to the owning blueprint
  const c = idx.find((e) => e.type === "construct")!;
  expect(c.label).toBe("rds · db");
  expect(c.path).toBe("/bp/alpha");
  // config keys → Config view
  const cfg = idx.find((e) => e.type === "config")!;
  expect(cfg.label).toBe("GC_PROM_RW");
  expect(cfg.path).toBe("/config");
  // inventory metric names → X-ray
  const metrics = byType("metric");
  expect(metrics).toContain("cpu_total");
  expect(metrics).toContain("mem_total");
  expect(idx.find((e) => e.type === "metric")!.path).toBe("/xray");
});

test("dedupes metric names across constructs and blueprints", () => {
  const idx = buildSearchIndex(
    snap({
      inventory: {
        blueprints: [
          { blueprint: "a", distinct_series: 0, metric_names: 0, label_keys: 0, constructs: [{ kind: "k", name: "1", metric_names: ["dup", "x"] }] },
          { blueprint: "b", distinct_series: 0, metric_names: 0, label_keys: 0, constructs: [{ kind: "k", name: "2", metric_names: ["dup", "y"] }] },
        ],
        totals: { distinct_series: 0, constructs: 2, blueprints: 2 },
      },
    }),
  );
  const dups = idx.filter((e) => e.type === "metric" && e.label === "dup");
  expect(dups.length).toBe(1);
});

test("degrades gracefully when schema/config/inventory are absent", () => {
  const idx = buildSearchIndex(snap({ state: defaultState({ disabled_blueprints: ["only"] }) }));
  expect(idx.map((e) => e.label)).toEqual(["only"]);
});
