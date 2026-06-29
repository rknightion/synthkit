import { test, expect, vi, beforeEach } from "vitest";
import { render, fireEvent } from "@solidjs/testing-library";
import userEvent from "@testing-library/user-event";
import { Global } from "./Global";
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

function renderGlobal(store: ControlStore) {
  return render(() => (
    <StoreProvider store={store}>
      <Global />
    </StoreProvider>
  ));
}

// Mock fetch (postJSON uses it) returning the new State so .then(refresh) fires.
// Returns a helper to read the JSON body posted to a given /control/<path>.
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

// flush microtasks so the postJSON promise chain (.then→refresh) settles.
const flush = () => new Promise((r) => setTimeout(r, 0));

// ── distinct states ────────────────────────────────────────────────────────
test("shows a loading state distinct from data", () => {
  const { getByTestId, queryByTestId } = renderGlobal(fakeStore({ loading: true }));
  expect(getByTestId("global-loading")).toBeInTheDocument();
  expect(queryByTestId("global-error")).not.toBeInTheDocument();
});

test("shows a distinct error state when the state GET fails", () => {
  const store = fakeStore({ loading: false, errors: { state: "boom" } });
  const { getByTestId } = renderGlobal(store);
  const err = getByTestId("global-error");
  expect(err).toBeInTheDocument();
  expect(err.textContent).toContain("boom");
});

test("a schema-only error does NOT show the fatal error node (degrades, renders controls)", () => {
  // Fix: errKey() gates on errors["state"] only; a schema error alone must not trigger
  // the fatal error banner — the view should render controls (null-guarded throughout).
  const store = fakeStore({
    loading: false,
    state: defaultState(),
    errors: { schema: "boom" },
    schema: undefined,
  });
  const { queryByTestId } = renderGlobal(store);
  expect(queryByTestId("global-error")).not.toBeInTheDocument();
  // Controls are reachable (blueprint empty-state confirms the section rendered).
  expect(queryByTestId("global-bp-empty")).toBeInTheDocument();
});

test("a missing/non-rich schema degrades gracefully (no crash, no volume panel)", () => {
  // schema undefined: bare Descriptor[] path. Controls null-guard; render must not throw.
  const store = fakeStore({ loading: false, state: defaultState(), schema: undefined });
  const { queryByTestId } = renderGlobal(store);
  expect(queryByTestId("global-vol-shown")).not.toBeInTheDocument();
  expect(queryByTestId("global-bp-empty")).toBeInTheDocument();
});

// ── volume: posts the CLAMPED value to load ──────────────────────────────────
test("volume numeric input commits the CLAMPED value to load", async () => {
  const f = stubFetchOK();
  const store = fakeStore({ loading: false, state: defaultState(), schema: schema() });
  const { getByTestId } = renderGlobal(store);
  const num = getByTestId("global-vol-num") as HTMLInputElement;
  // type above max (10) → must clamp to 10
  fireEvent.change(num, { target: { value: "99" } });
  await flush();
  expect(f.bodyFor("load")).toEqual({ volume_multiplier: 10 });
  expect(store.refresh).toHaveBeenCalled();
});

test("a volume preset chip commits that value to load", async () => {
  const f = stubFetchOK();
  const store = fakeStore({ loading: false, state: defaultState(), schema: schema() });
  const { getByTestId } = renderGlobal(store);
  await userEvent.click(getByTestId("global-vol-preset-5"));
  await flush();
  expect(f.bodyFor("load")).toEqual({ volume_multiplier: 5 });
});

// ── construct-kind toggle posts updated disabled_kinds to kinds ───────────────
test("a construct-kind toggle posts the updated disabled_kinds array to kinds", async () => {
  const f = stubFetchOK();
  const store = fakeStore({
    loading: false,
    state: defaultState({ disabled_kinds: ["rds"] }),
    schema: schema({ kinds: ["ec2", "rds", "k8s_cluster"] }),
  });
  const { getByTestId } = renderGlobal(store);
  // ec2 currently enabled → toggling disables it; rds already disabled stays.
  await userEvent.click(getByTestId("global-kind-ec2"));
  await flush();
  const body = f.bodyFor("kinds") as { disabled_kinds: string[] };
  expect(new Set(body.disabled_kinds)).toEqual(new Set(["rds", "ec2"]));
});

// ── span-metrics toggle posts span_metrics_blueprints ────────────────────────
test("a span-metrics toggle posts span_metrics_blueprints", async () => {
  const f = stubFetchOK();
  const store = fakeStore({
    loading: false,
    state: defaultState({ span_metrics_blueprints: [] }),
    schema: schema({ blueprints: ["alpha", "bravo"] }),
  });
  const { getByTestId } = renderGlobal(store);
  await userEvent.click(getByTestId("global-sm-alpha"));
  await flush();
  expect(f.bodyFor("spanmetrics")).toEqual({ span_metrics_blueprints: ["alpha"] });
});

// ── single blueprint toggle posts disabled_blueprints ────────────────────────
test("an individual blueprint toggle posts the updated disabled_blueprints", async () => {
  const f = stubFetchOK();
  const store = fakeStore({
    loading: false,
    state: defaultState(),
    schema: schema({ blueprints: ["alpha", "bravo"] }),
  });
  const { getByTestId } = renderGlobal(store);
  await userEvent.click(getByTestId("global-bp-alpha"));
  await flush();
  expect(f.bodyFor("blueprints")).toEqual({ disabled_blueprints: ["alpha"] });
});

// ── bulk all-off is gated by ConfirmButton ───────────────────────────────────
test("bulk 'All off' is gated: confirming posts ALL blueprints; cancelling posts nothing", async () => {
  const f = stubFetchOK();
  const store = fakeStore({
    loading: false,
    state: defaultState(),
    schema: schema({ blueprints: ["alpha", "bravo"] }),
  });
  const { getByText } = renderGlobal(store);

  // Open dialog, then CANCEL — nothing posted.
  await userEvent.click(getByText("All off"));
  await userEvent.click(getByText("Cancel"));
  await flush();
  expect(f.pathCalled("blueprints")).toBe(false);

  // Open again, CONFIRM — posts disabled_blueprints = all.
  await userEvent.click(getByText("All off"));
  await userEvent.click(getByText("Disable all"));
  await flush();
  expect(f.bodyFor("blueprints")).toEqual({ disabled_blueprints: ["alpha", "bravo"] });
});

test("bulk 'All on' posts an empty disabled_blueprints list", async () => {
  const f = stubFetchOK();
  const store = fakeStore({
    loading: false,
    state: defaultState({ disabled_blueprints: ["alpha"] }),
    schema: schema({ blueprints: ["alpha", "bravo"] }),
  });
  const { getByTestId } = renderGlobal(store);
  await userEvent.click(getByTestId("global-bp-all-on"));
  await flush();
  expect(f.bodyFor("blueprints")).toEqual({ disabled_blueprints: [] });
});

// ── unscoped scalable target posts {targetId: clamped count} to scaling ──────
test("an unscoped scalable target posts {targetId: clamped count} to scaling", async () => {
  const f = stubFetchOK();
  const tgt: TargetInfo = { blueprint: "", name: "orphan-pool", axis: "scale", scalable: scalable({ min: 1, max: 8, default: 3, current: 3 }) };
  const store = fakeStore({
    loading: false,
    state: defaultState(),
    schema: schema({ targets: [tgt] }),
  });
  const { getByTestId } = renderGlobal(store);
  const row = getByTestId("global-scale-/orphan-pool");
  const range = row.querySelector("input[type=range]") as HTMLInputElement;
  // above max → clamp to 8; key is the qualified blueprint/name id ("/orphan-pool" here).
  fireEvent.change(range, { target: { value: "50" } });
  await flush();
  expect(f.bodyFor("scaling")).toEqual({ "/orphan-pool": 8 });
});

// ── action error surfaces on mutation failure ────────────────────────────────
test("a mutation failure surfaces the action-error banner", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response("nope", { status: 400 })));
  const store = fakeStore({ loading: false, state: defaultState(), schema: schema() });
  const { getByTestId, findByTestId } = renderGlobal(store);
  await userEvent.click(getByTestId("global-vol-preset-2"));
  const banner = await findByTestId("global-action-error");
  expect(banner.textContent).toContain("nope");
});
