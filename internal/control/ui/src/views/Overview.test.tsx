import { test, expect, vi, beforeEach } from "vitest";
import { render, fireEvent } from "@solidjs/testing-library";
import { MemoryRouter, Route } from "@solidjs/router";
import { Overview } from "./Overview";
import { StoreProvider, type ControlStore, type Snapshot } from "../store/store";
import type { State } from "../api/types";

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

// fakeStore stands in for the live polling store: a static Snapshot, inert lifecycle.
function fakeStore(state: Partial<Snapshot>): ControlStore {
  return {
    state: { loading: false, errors: {}, ...state } as Snapshot,
    refresh: async () => {},
    start: () => {},
    stop: () => {},
  };
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
  };
}

const flush = () => new Promise((r) => setTimeout(r, 0));

// Overview links blueprint cards via <A> → needs a router context.
function renderOverview(store: ControlStore) {
  return render(() => (
    <StoreProvider store={store}>
      <MemoryRouter>
        <Route path="/" component={Overview} />
      </MemoryRouter>
    </StoreProvider>
  ));
}

test("renders a card per blueprint for a populated snapshot", () => {
  const store = fakeStore({
    state: defaultState(),
    inventory: {
      blueprints: [
        { blueprint: "alpha", distinct_series: 1234, metric_names: 42, label_keys: 9, constructs: [{ kind: "ec2", name: "web" }] },
        { blueprint: "bravo", distinct_series: 56, metric_names: 7, label_keys: 3, constructs: [] },
      ],
      totals: { distinct_series: 1290, constructs: 1, blueprints: 2 },
    },
    status: {
      sinks: [],
      by_blueprint: { alpha: { blueprint: "alpha", total_items: 9000, rate_per_min: 320, spark: [1, 2, 3] } },
      persist: { last_ok_ms: 1, last_error_ms: 0, last_error: "" },
      dry_run: false,
    },
    diagnostics: [],
  });
  const { getByText } = renderOverview(store);
  // Both blueprint names appear as cards.
  expect(getByText("alpha")).toBeInTheDocument();
  expect(getByText("bravo")).toBeInTheDocument();
});

test("shows a distinct empty state when there are no blueprints", () => {
  const store = fakeStore({
    state: defaultState(),
    inventory: { blueprints: [], totals: { distinct_series: 0, constructs: 0, blueprints: 0 } },
    status: { sinks: [], by_blueprint: {}, persist: { last_ok_ms: 0, last_error_ms: 0, last_error: "" }, dry_run: false },
    diagnostics: [],
  });
  const { getByTestId, queryByTestId } = renderOverview(store);
  // The empty node renders; the loading node does NOT.
  expect(getByTestId("overview-empty")).toBeInTheDocument();
  expect(queryByTestId("overview-loading")).not.toBeInTheDocument();
});

test("shows a loading state distinct from the empty state", () => {
  const store = fakeStore({ loading: true });
  const { getByTestId, queryByTestId } = renderOverview(store);
  // The loading node renders; the empty node does NOT (loading != empty).
  expect(getByTestId("overview-loading")).toBeInTheDocument();
  expect(queryByTestId("overview-empty")).not.toBeInTheDocument();
});

test("shows a distinct error state when the inventory GET fails", () => {
  const store = fakeStore({
    loading: false,
    errors: { inventory: "boom" },
    inventory: undefined,
    state: defaultState(),
  });
  const { getByTestId, queryByTestId } = renderOverview(store);
  // The error node renders with the message; the empty node does NOT.
  const errNode = getByTestId("overview-error");
  expect(errNode).toBeInTheDocument();
  expect(errNode.textContent).toContain("boom");
  expect(queryByTestId("overview-empty")).not.toBeInTheDocument();
});

test("shows a first-run hint when the empty state is displayed", () => {
  const store = fakeStore({
    state: defaultState(),
    inventory: { blueprints: [], totals: { distinct_series: 0, constructs: 0, blueprints: 0 } },
    status: { sinks: [], by_blueprint: {}, persist: { last_ok_ms: 0, last_error_ms: 0, last_error: "" }, dry_run: false },
    diagnostics: [],
  });
  const { getByTestId } = renderOverview(store);
  expect(getByTestId("overview-firstrun-hint")).toBeInTheDocument();
  expect(getByTestId("overview-firstrun-hint").textContent).toContain("CONTROL_TOKEN");
});

test("shows the self-obs Grafana deep-link only when GC_SELF_GRAFANA_URL is set", () => {
  const base = {
    state: defaultState(),
    inventory: { blueprints: [], totals: { distinct_series: 0, constructs: 0, blueprints: 0 } },
    status: { sinks: [], by_blueprint: {}, persist: { last_ok_ms: 1, last_error_ms: 0, last_error: "" }, dry_run: false },
    diagnostics: [],
  };
  // absent → no link
  const noLink = renderOverview(fakeStore(base));
  expect(noLink.queryByTestId("overview-selfobs-link")).not.toBeInTheDocument();
  noLink.unmount();

  // present → link with the dashboard path
  const withLink = renderOverview(
    fakeStore({
      ...base,
      config: { groups: [{ title: "Self-obs", fields: [{ key: "GC_SELF_GRAFANA_URL", value: "https://staff.example/", secret: false, configured: true }] }] },
    }),
  );
  const a = withLink.getByTestId("overview-selfobs-link");
  expect(a).toHaveAttribute("href", "https://staff.example/d/synthkit-selfobs");
});

// ── bulk All on / All off (recovered from legacy ui.html bulkBlueprintBtns) ──
function bulkSnapshot(disabled: string[]): ControlStore {
  return fakeStore({
    state: defaultState({ disabled_blueprints: disabled }),
    inventory: {
      blueprints: [
        { blueprint: "alpha", distinct_series: 1, metric_names: 1, label_keys: 1, constructs: [] },
        { blueprint: "bravo", distinct_series: 1, metric_names: 1, label_keys: 1, constructs: [] },
      ],
      totals: { distinct_series: 2, constructs: 0, blueprints: 2 },
    },
    status: { sinks: [], by_blueprint: {}, persist: { last_ok_ms: 1, last_error_ms: 0, last_error: "" }, dry_run: false },
    diagnostics: [],
  });
}

test("All off disables every blueprint (full-list replace)", async () => {
  const f = stubFetchOK();
  const { getByTestId, getByText } = renderOverview(bulkSnapshot([]));
  // "All off" is confirm-gated (destructive): trigger then confirm.
  fireEvent.click(getByTestId("overview-bulk-off"));
  fireEvent.click(getByText("Disable all"));
  await flush();
  expect(f.bodyFor("blueprints")).toEqual({ disabled_blueprints: ["alpha", "bravo"] });
});

test("All on re-enables every blueprint (empties the disabled list)", async () => {
  const f = stubFetchOK();
  const { getByTestId } = renderOverview(bulkSnapshot(["alpha", "bravo"]));
  fireEvent.click(getByTestId("overview-bulk-on"));
  await flush();
  expect(f.bodyFor("blueprints")).toEqual({ disabled_blueprints: [] });
});

test("All on is disabled when none are disabled; All off disabled when all are", () => {
  const allOn = renderOverview(bulkSnapshot([]));
  expect(allOn.getByTestId("overview-bulk-on")).toBeDisabled();
  expect(allOn.getByTestId("overview-bulk-off")).not.toBeDisabled();
  allOn.unmount();

  const allOff = renderOverview(bulkSnapshot(["alpha", "bravo"]));
  expect(allOff.getByTestId("overview-bulk-on")).not.toBeDisabled();
  expect(allOff.getByTestId("overview-bulk-off")).toBeDisabled();
});

test("bulk buttons are absent when no blueprints are loaded", () => {
  const store = fakeStore({
    state: defaultState(),
    inventory: { blueprints: [], totals: { distinct_series: 0, constructs: 0, blueprints: 0 } },
    status: { sinks: [], by_blueprint: {}, persist: { last_ok_ms: 0, last_error_ms: 0, last_error: "" }, dry_run: false },
    diagnostics: [],
  });
  const { queryByTestId } = renderOverview(store);
  expect(queryByTestId("overview-bulk-on")).not.toBeInTheDocument();
  expect(queryByTestId("overview-bulk-off")).not.toBeInTheDocument();
});
