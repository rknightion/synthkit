import { test, expect, vi, beforeEach } from "vitest";
import { render, fireEvent } from "@solidjs/testing-library";
import { MemoryRouter, Route } from "@solidjs/router";
import { StoreProvider, type ControlStore, type Snapshot } from "../store/store";
import { Rail } from "./Rail";
import type { State } from "../api/types";

beforeEach(() => {
  vi.restoreAllMocks();
  vi.stubGlobal("scrollTo", () => {});
});

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

function fakeStore(state: Partial<Snapshot>): ControlStore {
  return {
    state: { loading: false, errors: {}, ...state } as Snapshot,
    refresh: async () => {},
    start: () => {},
    stop: () => {},
  };
}

function stubFetchOK() {
  const fn = vi.fn(
    async (_input: RequestInfo | URL, _init?: RequestInit) =>
      new Response(JSON.stringify({}), { status: 200 }),
  );
  vi.stubGlobal("fetch", fn);
  return fn;
}

const flush = () => new Promise((r) => setTimeout(r, 0));

function renderRail(store: ControlStore) {
  return render(() => (
    <StoreProvider store={store}>
      <MemoryRouter>
        <Route path="/" component={Rail} />
      </MemoryRouter>
    </StoreProvider>
  ));
}

test("Reset posts to /control/reset (body null) after confirm", async () => {
  const fn = stubFetchOK();
  const { getByTestId, getByText } = renderRail(fakeStore({ state: defaultState({ disabled_blueprints: ["a"] }) }));
  fireEvent.click(getByTestId("rail-reset"));
  fireEvent.click(getByText("Reset all"));
  await flush();
  const call = fn.mock.calls.find((c) => c[0] === "/control/reset");
  expect(call).toBeTruthy();
  expect(call![1]!.method).toBe("POST");
  expect(call![1]!.body).toBe("null");
});

test("Reset confirm message reflects the deviation count", () => {
  const dev = renderRail(fakeStore({ state: defaultState({ disabled_blueprints: ["a", "b"], volume_multiplier: 2 }) }));
  fireEvent.click(dev.getByTestId("rail-reset"));
  // 2 disabled + 1 volume deviation = 3
  expect(dev.getByText(/Reset ALL 3 active deviations/)).toBeInTheDocument();
  dev.unmount();

  const clean = renderRail(fakeStore({ state: defaultState() }));
  fireEvent.click(clean.getByTestId("rail-reset"));
  expect(clean.getByText(/already at defaults/)).toBeInTheDocument();
});

test("Reset surfaces an inline error when the POST fails", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response("nope", { status: 500 })));
  const { getByTestId, getByText } = renderRail(fakeStore({ state: defaultState() }));
  fireEvent.click(getByTestId("rail-reset"));
  fireEvent.click(getByText("Reset all"));
  await flush();
  expect(getByTestId("rail-reset-err")).toBeInTheDocument();
});
