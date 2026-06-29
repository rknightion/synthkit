import { test, expect, vi, beforeEach } from "vitest";
import { render, fireEvent } from "@solidjs/testing-library";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, createMemoryHistory } from "@solidjs/router";
import { Blueprint } from "./Blueprint";
import { StoreProvider, type ControlStore, type Snapshot } from "../store/store";
import type { Schema, State, TargetInfo } from "../api/types";

beforeEach(() => { vi.restoreAllMocks(); });

// A fully-defaulted control State (matches control.DefaultState()): all slices/maps
// non-nil, VolumeMultiplier 1.0. Tests override only the fields they exercise.
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

// A minimal rich operator schema. Tests override the slices they exercise.
function schema(over: Partial<Schema> = {}): Schema {
  return {
    volume_multiplier: { key: "volume_multiplier", type: "float", help: "Master load multiplier.", default: 1, min: 0, max: 10 },
    blueprints: [],
    modes: [],
    targets: [],
    scenarios: [],
    constructs: [],
    kinds: [],
    ...over,
  };
}

function scalable(over: Partial<TargetInfo["scalable"] & object> = {}): TargetInfo["scalable"] {
  return { dimension: "replicas", min: 1, max: 20, default: 3, current: 3, ...over };
}

// fakeStore stands in for the live polling store: a static Snapshot, inert lifecycle.
// refresh() is a spy so tests can assert the mutation→refresh handshake.
function fakeStore(state: Partial<Snapshot>): ControlStore {
  return {
    state: { loading: false, errors: {}, ...state } as Snapshot,
    refresh: vi.fn(async () => {}),
    start: () => {},
    stop: () => {},
  };
}

// Blueprint reads its name from the /bp/:name route param → needs a router seeded at the
// blueprint path. createMemoryHistory() always starts at "/", so we set the initial entry.
function renderBlueprint(store: ControlStore, name: string) {
  const history = createMemoryHistory();
  history.set({ value: `/bp/${encodeURIComponent(name)}` });
  return render(() => (
    <StoreProvider store={store}>
      <MemoryRouter history={history}>
        <Route path="/bp/:name" component={Blueprint} />
      </MemoryRouter>
    </StoreProvider>
  ));
}

// Mock fetch (postJSON uses it) returning the new State so .then(refresh) fires.
function stubFetchOK() {
  const fn = vi.fn(
    async (_input: RequestInfo | URL, _init?: RequestInit) =>
      new Response(JSON.stringify({}), { status: 200 }),
  );
  vi.stubGlobal("fetch", fn);
  return {
    fn,
    bodyFor(path: string): unknown {
      const call = fn.mock.calls.find((c) => c[0] === `/control/${path}`);
      if (!call) return undefined;
      return JSON.parse(call[1]!.body as string);
    },
    pathCalled(path: string): boolean {
      return fn.mock.calls.some((c) => c[0] === `/control/${path}`);
    },
  };
}

const flush = () => new Promise((r) => setTimeout(r, 0));

// ── data: renders the named blueprint's summary ──────────────────────────────
test("renders the named blueprint's inventory + health summary", () => {
  const store = fakeStore({
    loading: false,
    state: defaultState(),
    inventory: {
      blueprints: [
        { blueprint: "alpha", distinct_series: 1234, metric_names: 42, label_keys: 9, constructs: [{ kind: "ec2", name: "web" }, { kind: "rds", name: "db" }] },
        { blueprint: "bravo", distinct_series: 56, metric_names: 7, label_keys: 3, constructs: [] },
      ],
      totals: { distinct_series: 1290, constructs: 2, blueprints: 2 },
    },
    health: {
      constructs: [],
      blueprints: [{ blueprint: "alpha", cycles: 88, dropped_ticks: 0, last_cycle_ms: 12.3, cycle_spark: [] }],
      process: { goroutines: 0, heap_bytes: 0, gc_count: 0 },
    },
    schema: schema({ blueprints: ["alpha", "bravo"] }),
  });
  const { getByText, getByTestId } = renderBlueprint(store, "alpha");
  // header shows the blueprint name and the summary panel renders.
  expect(getByText("alpha")).toBeInTheDocument();
  const sum = getByTestId("bp-summary");
  expect(sum.textContent).toContain("series");
  expect(sum.textContent).toContain("cycles");
});

// ── unknown blueprint: distinct "not found" node ─────────────────────────────
test("renders a distinct 'unknown blueprint' node when the name isn't loaded", () => {
  const store = fakeStore({
    loading: false,
    state: defaultState(),
    inventory: { blueprints: [{ blueprint: "alpha", distinct_series: 1, metric_names: 1, label_keys: 1, constructs: [] }], totals: { distinct_series: 1, constructs: 0, blueprints: 1 } },
    schema: schema({ blueprints: ["alpha"] }),
  });
  const { getByTestId, queryByTestId } = renderBlueprint(store, "ghost");
  expect(getByTestId("bp-unknown")).toBeInTheDocument();
  expect(queryByTestId("bp-summary")).not.toBeInTheDocument();
});

// ── loading state ────────────────────────────────────────────────────────────
test("shows a loading state distinct from data", () => {
  const { getByTestId, queryByTestId } = renderBlueprint(fakeStore({ loading: true }), "alpha");
  expect(getByTestId("bp-loading")).toBeInTheDocument();
  expect(queryByTestId("bp-error")).not.toBeInTheDocument();
});

// ── error state ──────────────────────────────────────────────────────────────
test("shows a distinct error state when the state GET fails", () => {
  const store = fakeStore({ loading: false, errors: { state: "boom" } });
  const { getByTestId } = renderBlueprint(store, "alpha");
  const err = getByTestId("bp-error");
  expect(err).toBeInTheDocument();
  expect(err.textContent).toContain("boom");
});

// ── scaling: posts {bp/target: clamped count} to scaling ─────────────────────
test("a scaling change posts {blueprint/target: clamped count} to scaling", async () => {
  const f = stubFetchOK();
  const tgt: TargetInfo = { blueprint: "alpha", name: "web", axis: "scale", scalable: scalable({ min: 1, max: 8, default: 3, current: 3 }) };
  const store = fakeStore({
    loading: false,
    state: defaultState(),
    inventory: { blueprints: [{ blueprint: "alpha", distinct_series: 1, metric_names: 1, label_keys: 1, constructs: [] }], totals: { distinct_series: 1, constructs: 0, blueprints: 1 } },
    schema: schema({ blueprints: ["alpha"], targets: [tgt] }),
  });
  const { getByTestId } = renderBlueprint(store, "alpha");
  const row = getByTestId("bp-scale-alpha/web");
  const range = row.querySelector("input[type=range]") as HTMLInputElement;
  // above max → clamp to 8; key is the qualified blueprint/name id.
  fireEvent.change(range, { target: { value: "50" } });
  await flush();
  expect(f.bodyFor("scaling")).toEqual({ "alpha/web": 8 });
  expect(store.refresh).toHaveBeenCalled();
});

// ── construct toggle: posts updated disabled_constructs ──────────────────────
test("a construct toggle posts the updated disabled_constructs (blueprint/kind:name keys)", async () => {
  const f = stubFetchOK();
  const store = fakeStore({
    loading: false,
    state: defaultState({ disabled_constructs: ["alpha/rds:db"] }),
    inventory: { blueprints: [{ blueprint: "alpha", distinct_series: 1, metric_names: 1, label_keys: 1, constructs: [] }], totals: { distinct_series: 1, constructs: 0, blueprints: 1 } },
    schema: schema({
      blueprints: ["alpha"],
      constructs: [
        { blueprint: "alpha", kind: "ec2", name: "web", enabled: true },
        { blueprint: "alpha", kind: "rds", name: "db", enabled: false },
      ],
    }),
  });
  const { getByTestId } = renderBlueprint(store, "alpha");
  // ec2:web currently enabled → toggling disables it; rds:db already disabled stays.
  await userEvent.click(getByTestId("bp-construct-alpha/ec2:web"));
  await flush();
  const body = f.bodyFor("constructs") as { disabled_constructs: string[] };
  expect(new Set(body.disabled_constructs)).toEqual(new Set(["alpha/rds:db", "alpha/ec2:web"]));
});

// ── blueprint enable/disable toggle posts disabled_blueprints ────────────────
test("disabling this blueprint via ConfirmButton posts disabled_blueprints", async () => {
  const f = stubFetchOK();
  const store = fakeStore({
    loading: false,
    state: defaultState(),
    inventory: { blueprints: [{ blueprint: "alpha", distinct_series: 1, metric_names: 1, label_keys: 1, constructs: [] }], totals: { distinct_series: 1, constructs: 0, blueprints: 1 } },
    schema: schema({ blueprints: ["alpha", "bravo"] }),
  });
  const { getByText } = renderBlueprint(store, "alpha");
  await userEvent.click(getByText("Enabled"));   // ConfirmButton label → opens confirm
  await userEvent.click(getByText("Disable"));   // confirm label
  await flush();
  expect(f.bodyFor("blueprints")).toEqual({ disabled_blueprints: ["alpha"] });
});

// ── disabled blueprint shows a warning banner ────────────────────────────────
test("a disabled blueprint shows the warning banner", () => {
  const store = fakeStore({
    loading: false,
    state: defaultState({ disabled_blueprints: ["alpha"] }),
    inventory: { blueprints: [{ blueprint: "alpha", distinct_series: 1, metric_names: 1, label_keys: 1, constructs: [] }], totals: { distinct_series: 1, constructs: 0, blueprints: 1 } },
    schema: schema({ blueprints: ["alpha"] }),
  });
  const { getByTestId } = renderBlueprint(store, "alpha");
  expect(getByTestId("bp-disabled-banner")).toBeInTheDocument();
});

// ── YAML <details>: lazy text fetch on open, renders returned text ───────────
test("opening the YAML <details> fetches the source text and renders it", async () => {
  const fn = vi.fn(async (input: RequestInfo | URL) => {
    if (String(input).startsWith("/control/blueprint?")) {
      return new Response("kind: blueprint\nname: alpha\n", { status: 200, headers: { "Content-Type": "text/plain" } });
    }
    return new Response("{}", { status: 200 });
  });
  vi.stubGlobal("fetch", fn);
  const store = fakeStore({
    loading: false,
    state: defaultState(),
    inventory: { blueprints: [{ blueprint: "alpha", distinct_series: 1, metric_names: 1, label_keys: 1, constructs: [] }], totals: { distinct_series: 1, constructs: 0, blueprints: 1 } },
    schema: schema({ blueprints: ["alpha"] }),
  });
  const { getByTestId } = renderBlueprint(store, "alpha");
  const details = getByTestId("bp-yaml") as HTMLDetailsElement;
  // jsdom doesn't auto-fire toggle on open=true assignment; set + dispatch.
  details.open = true;
  fireEvent(details, new Event("toggle"));
  await flush();
  // the source endpoint was fetched with the blueprint query…
  expect(fn.mock.calls.some((c) => String(c[0]) === "/control/blueprint?blueprint=alpha")).toBe(true);
  // …and the returned text rendered into the <pre>.
  expect(getByTestId("bp-yaml-pre").textContent).toContain("name: alpha");
});
