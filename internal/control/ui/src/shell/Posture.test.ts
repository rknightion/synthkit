import { test, expect } from "vitest";
import { deriveTags } from "./Posture";
import type { Schema, State } from "../api/types";

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

const schema: Schema = {
  volume_multiplier: { key: "volume_multiplier", type: "float", help: "", default: 1, min: 0, max: 10 },
  blueprints: ["bp"],
  modes: [],
  targets: [{ blueprint: "bp", name: "web", axis: "replicas", scalable: { dimension: "replicas", min: 1, max: 10, default: 3, current: 3 } }],
  scenarios: [{ blueprint: "bp", name: "Black Friday", title: "BF", summary: "", effects: [], active: false }],
  constructs: [],
  kinds: [],
};

test("a scaling value equal to the schema default is NOT flagged as modified", () => {
  const tags = deriveTags(defaultState({ scaling: { "bp/web": 3 } }), schema);
  expect(tags.find((t) => t.text.includes("web"))).toBeUndefined();
});

test("a scaling value off the schema default IS flagged, with the friendly target name", () => {
  const tags = deriveTags(defaultState({ scaling: { "bp/web": 7 } }), schema);
  const t = tags.find((x) => x.cls === "mod")!;
  expect(t.text).toBe("web → 7");
});

test("active scenario shows its friendly schema name", () => {
  const tags = deriveTags(defaultState({ active_scenarios: ["bp/Black Friday"] }), schema);
  expect(tags[0].text).toBe("scenario: Black Friday");
});

test("volume equal to schema default is not flagged", () => {
  const tags = deriveTags(defaultState({ volume_multiplier: 1 }), schema);
  expect(tags.find((t) => t.text.startsWith("volume"))).toBeUndefined();
});

test("degrades to raw ids when schema is absent (no crash)", () => {
  const tags = deriveTags(defaultState({ scaling: { "bp/web": 7 }, active_scenarios: ["bp/x"] }), undefined);
  expect(tags.find((t) => t.text === "bp/web → 7")).toBeTruthy();
  expect(tags.find((t) => t.text === "scenario: bp/x")).toBeTruthy();
});
