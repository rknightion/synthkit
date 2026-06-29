import { test, expect, vi, beforeEach } from "vitest";
import { render, fireEvent } from "@solidjs/testing-library";
import userEvent from "@testing-library/user-event";
import { Incidents } from "./Incidents";
import { StoreProvider, type ControlStore, type Snapshot } from "../store/store";
import type { IncidentInfo, ModeInfo, Schema, State, TargetInfo } from "../api/types";

beforeEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
});

// A fully-defaulted control State (matches control.DefaultState()). Tests override the
// few fields they exercise.
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
    volume_multiplier: { key: "volume_multiplier", type: "float", help: "", default: 1, min: 0, max: 10 },
    blueprints: [],
    modes: [],
    targets: [],
    scenarios: [],
    constructs: [],
    kinds: [],
    ...over,
  };
}

const mode = (name: string, axis = "app", help = ""): ModeInfo => ({ name, axis, help });
const target = (over: Partial<TargetInfo>): TargetInfo => ({
  blueprint: "",
  name: "t",
  axis: "app",
  scalable: null,
  ...over,
});
const incident = (over: Partial<IncidentInfo>): IncidentInfo => ({
  source: "declared",
  id: "",
  blueprint: "alpha",
  mode: "latency",
  target: "",
  at: "",
  for: "",
  schedule_spec: "latency@12:00/30m",
  intensity: 0.5,
  active_now: false,
  ...over,
});

// fakeStore stands in for the live polling store: a static Snapshot, inert lifecycle.
function fakeStore(state: Partial<Snapshot>): ControlStore {
  return {
    state: { loading: false, errors: {}, ...state } as Snapshot,
    refresh: vi.fn(async () => {}),
    start: () => {},
    stop: () => {},
  };
}

function renderIncidents(store: ControlStore) {
  return render(() => (
    <StoreProvider store={store}>
      <Incidents />
    </StoreProvider>
  ));
}

// Mock fetch (postJSON/delJSON use it) returning 200; returns helpers to read the
// posted JSON body and to check a path/method was called.
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
    methodFor(path: string): string | undefined {
      const call = fn.mock.calls.find((c) => c[0] === `/control/${path}`);
      return call?.[1]?.method as string | undefined;
    },
  };
}

// flush microtasks so the postJSON/delJSON promise chain (.then→refresh) settles.
const flush = () => new Promise((r) => setTimeout(r, 0));

// ── distinct states ──────────────────────────────────────────────────────────
test("shows a loading state distinct from data", () => {
  const { getByTestId, queryByTestId } = renderIncidents(fakeStore({ loading: true }));
  expect(getByTestId("incidents-loading")).toBeInTheDocument();
  expect(queryByTestId("incidents-error")).not.toBeInTheDocument();
});

test("shows a distinct fatal error state when the schema GET fails", () => {
  const store = fakeStore({ loading: false, errors: { schema: "boom" } });
  const { getByTestId } = renderIncidents(store);
  const err = getByTestId("incidents-error");
  expect(err).toBeInTheDocument();
  expect(err.textContent).toContain("boom");
});

test("an incidents 404 renders the graceful unavailable node, NOT the fatal error", async () => {
  const store = fakeStore({
    loading: false,
    state: defaultState(),
    schema: schema({ blueprints: ["alpha"], modes: [mode("latency")] }),
    errors: { incidents: "incidents unavailable: no incident source configured" },
  });
  const { getByText, queryByTestId, getByTestId } = renderIncidents(store);
  // switch to the scheduled tab where incidents live
  await userEvent.click(getByText("Scheduled"));
  expect(getByTestId("incidents-unavailable")).toBeInTheDocument();
  expect(queryByTestId("incidents-error")).not.toBeInTheDocument();
});

// ── tabs: persist to localStorage + render the other panel ───────────────────
test("tab switch persists the choice to localStorage and renders the other panel", async () => {
  const store = fakeStore({
    loading: false,
    state: defaultState(),
    schema: schema({ blueprints: ["alpha"], modes: [mode("latency")] }),
    incidents: [],
  });
  const { getByText, getByTestId, queryByTestId } = renderIncidents(store);

  // defaults to on-demand
  expect(getByTestId("incidents-panel-ondemand")).toBeInTheDocument();
  expect(queryByTestId("incidents-panel-scheduled")).not.toBeInTheDocument();

  await userEvent.click(getByText("Scheduled"));
  expect(getByTestId("incidents-panel-scheduled")).toBeInTheDocument();
  expect(queryByTestId("incidents-panel-ondemand")).not.toBeInTheDocument();
  expect(localStorage.getItem("synthkit-incidents-tab")).toBe("scheduled");
});

test("the persisted tab choice is the initial tab on mount", () => {
  localStorage.setItem("synthkit-incidents-tab", "scheduled");
  const store = fakeStore({
    loading: false,
    state: defaultState(),
    schema: schema({ blueprints: ["alpha"], modes: [mode("latency")] }),
    incidents: [],
  });
  const { getByTestId } = renderIncidents(store);
  expect(getByTestId("incidents-panel-scheduled")).toBeInTheDocument();
});

// ── on-demand: inject is ConfirmButton-gated and posts the right failures body ─
test("global inject is ConfirmButton-gated; confirming posts failures {[mode]:{enabled,intensity,scope}}", async () => {
  const f = stubFetchOK();
  const store = fakeStore({
    loading: false,
    state: defaultState(),
    schema: schema({
      blueprints: ["alpha"],
      modes: [mode("latency", "app")],
      targets: [target({ blueprint: "alpha", name: "checkout", axis: "app" })],
    }),
  });
  const { getByTestId, getByText } = renderIncidents(store);

  // set intensity to a known value, scope to a known target
  fireEvent.input(getByTestId("incidents-global-intensity"), { target: { value: "0.75" } });
  fireEvent.change(getByTestId("incidents-global-scope"), { target: { value: "checkout" } });

  // open confirm, then CANCEL — nothing posted.
  await userEvent.click(getByTestId("incidents-global-inject"));
  await userEvent.click(getByText("Cancel"));
  await flush();
  expect(f.pathCalled("failures")).toBe(false);

  // open again, CONFIRM — posts the merge body for this mode.
  await userEvent.click(getByTestId("incidents-global-inject"));
  await userEvent.click(getByText("Inject failure"));
  await flush();
  expect(f.bodyFor("failures")).toEqual({
    latency: { enabled: true, intensity: 0.75, scope: "checkout" },
  });
  expect(store.refresh).toHaveBeenCalled();
});

// ── on-demand: disabling an active failure posts the disable body ─────────────
test("disabling an active failure posts failures {[mode]:{enabled:false,...}}", async () => {
  const f = stubFetchOK();
  const store = fakeStore({
    loading: false,
    state: defaultState({ failures: { latency: { enabled: true, intensity: 0.4, scope: "" } } }),
    schema: schema({ blueprints: ["alpha"], modes: [mode("latency")] }),
  });
  const { getByTestId } = renderIncidents(store);
  await userEvent.click(getByTestId("incidents-fdisable-latency"));
  await flush();
  expect(f.bodyFor("failures")).toEqual({
    latency: { enabled: false, intensity: 0.4, scope: "" },
  });
});

// ── on-demand: a scenario toggle posts the replace body to scenarios ──────────
test("a scenario toggle posts active_scenarios to scenarios (replace)", async () => {
  const f = stubFetchOK();
  const store = fakeStore({
    loading: false,
    state: defaultState({ active_scenarios: [] }),
    schema: schema({
      blueprints: ["alpha"],
      modes: [mode("latency")],
      scenarios: [
        { blueprint: "alpha", name: "outage", title: "Outage", summary: "", effects: [], active: false },
      ],
    }),
  });
  const { getByTestId } = renderIncidents(store);
  await userEvent.click(getByTestId("incidents-scn-alpha/outage"));
  await flush();
  expect(f.bodyFor("scenarios")).toEqual({ active_scenarios: ["alpha/outage"] });
});

// ── scheduled: incident delete is ConfirmButton-gated and calls delJSON ───────
test("a runtime incident delete is ConfirmButton-gated and calls DELETE incidents/<id>", async () => {
  const f = stubFetchOK();
  const store = fakeStore({
    loading: false,
    state: defaultState(),
    schema: schema({ blueprints: ["alpha"], modes: [mode("latency")] }),
    incidents: [
      incident({ source: "runtime", id: "rt-abc", schedule_spec: "latency@12:00/30m" }),
    ],
  });
  const { getByText, getByTestId } = renderIncidents(store);
  await userEvent.click(getByText("Scheduled"));

  // open confirm, CANCEL — nothing deleted.
  await userEvent.click(getByTestId("incidents-del-rt-abc"));
  await userEvent.click(getByText("Cancel"));
  await flush();
  expect(f.pathCalled("incidents/rt-abc")).toBe(false);

  // open again, CONFIRM — DELETE fires.
  await userEvent.click(getByTestId("incidents-del-rt-abc"));
  await userEvent.click(getByText("Delete"));
  await flush();
  expect(f.pathCalled("incidents/rt-abc")).toBe(true);
  expect(f.methodFor("incidents/rt-abc")).toBe("DELETE");
  expect(store.refresh).toHaveBeenCalled();
});

// ── scheduled: schedule form validates at + for before posting ────────────────
test("the schedule form rejects a submit with an empty duration (no POST; inline message)", async () => {
  const f = stubFetchOK();
  const store = fakeStore({
    loading: false,
    state: defaultState(),
    schema: schema({ blueprints: ["alpha"], modes: [mode("latency")] }),
    incidents: [],
  });
  const { getByText, getByTestId } = renderIncidents(store);
  await userEvent.click(getByText("Scheduled"));

  // clear the duration field so `for` is empty → validation must block the POST.
  fireEvent.change(getByTestId("incidents-sched-for"), { target: { value: "" } });
  await userEvent.click(getByTestId("incidents-sched-submit"));
  await flush();
  expect(f.pathCalled("incidents")).toBe(false);
  expect(getByTestId("incidents-sched-err").textContent).toContain("required");
});

test("the schedule form posts {blueprint,mode,target,at,for,intensity} when at+for present", async () => {
  const f = stubFetchOK();
  const store = fakeStore({
    loading: false,
    state: defaultState(),
    schema: schema({
      blueprints: ["alpha"],
      modes: [mode("latency", "app")],
      targets: [target({ blueprint: "alpha", name: "checkout", axis: "app" })],
    }),
    incidents: [],
  });
  const { getByText, getByTestId } = renderIncidents(store);
  await userEvent.click(getByText("Scheduled"));

  fireEvent.change(getByTestId("incidents-sched-at"), { target: { value: "08:30" } });
  fireEvent.change(getByTestId("incidents-sched-for"), { target: { value: "45m" } });
  fireEvent.input(getByTestId("incidents-sched-intensity"), { target: { value: "0.6" } });
  await userEvent.click(getByTestId("incidents-sched-submit"));
  await flush();
  expect(f.bodyFor("incidents")).toEqual({
    blueprint: "alpha",
    mode: "latency",
    target: "",
    at: "08:30",
    for: "45m",
    intensity: 0.6,
  });
  expect(store.refresh).toHaveBeenCalled();
});

// ── scheduled: schedule form clears on successful submit ─────────────────────
test("the schedule form clears its fields after a successful submit", async () => {
  const f = stubFetchOK();
  const store = fakeStore({
    loading: false,
    state: defaultState(),
    schema: schema({
      blueprints: ["alpha"],
      modes: [mode("latency", "app")],
    }),
    incidents: [],
  });
  const { getByText, getByTestId } = renderIncidents(store);
  await userEvent.click(getByText("Scheduled"));

  // Set a non-default duration value
  fireEvent.change(getByTestId("incidents-sched-for"), { target: { value: "2h" } });

  // Submit
  await userEvent.click(getByTestId("incidents-sched-submit"));
  await flush();

  // After success the duration field should be back to the default "30m"
  expect((getByTestId("incidents-sched-for") as HTMLInputElement).value).toBe("30m");
});

// ── scheduled: schedule form does NOT clear its fields on a failed submit ─────
test("the schedule form does NOT clear its fields after a failed submit", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response("bad request", { status: 400 })));
  const store = fakeStore({
    loading: false,
    state: defaultState(),
    schema: schema({
      blueprints: ["alpha"],
      modes: [mode("latency", "app")],
    }),
    incidents: [],
  });
  const { getByText, getByTestId, findByTestId } = renderIncidents(store);
  await userEvent.click(getByText("Scheduled"));

  // Set a non-default duration value
  fireEvent.change(getByTestId("incidents-sched-for"), { target: { value: "2h" } });

  // Submit — the POST fails with a 400
  await userEvent.click(getByTestId("incidents-sched-submit"));
  await flush();

  // Fields must NOT be reset — duration should still be "2h"
  expect((getByTestId("incidents-sched-for") as HTMLInputElement).value).toBe("2h");
  // AND the action-error banner must be visible
  const banner = await findByTestId("incidents-action-error");
  expect(banner).toBeInTheDocument();
});

// ── action error surfaces on mutation failure ────────────────────────────────
test("a mutation failure surfaces the action-error banner", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response("nope", { status: 400 })));
  const store = fakeStore({
    loading: false,
    state: defaultState({ failures: { latency: { enabled: true, intensity: 0.4, scope: "" } } }),
    schema: schema({ blueprints: ["alpha"], modes: [mode("latency")] }),
  });
  const { getByTestId, findByTestId } = renderIncidents(store);
  await userEvent.click(getByTestId("incidents-fdisable-latency"));
  const banner = await findByTestId("incidents-action-error");
  expect(banner.textContent).toContain("nope");
});
