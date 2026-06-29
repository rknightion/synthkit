import { test, expect, vi, beforeEach } from "vitest";
import { render } from "@solidjs/testing-library";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route } from "@solidjs/router";
import { StoreProvider, type ControlStore } from "../store/store";
import { Nav } from "./Nav";

// @solidjs/router schedules a deferred window.scrollTo (scroll-to-hash) after
// navigation; jsdom does not implement it and logs a benign "Not implemented"
// to stderr. Stub it out so test output is pristine.
beforeEach(() => {
  vi.stubGlobal("scrollTo", () => {});
});

// A static store standing in for the live one — Nav reads disabled/live state
// from it. No polling; refresh/start/stop are inert.
function staticStore(): ControlStore {
  return {
    state: {
      loading: false,
      errors: {},
      state: {
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
      },
    },
    refresh: async () => {},
    start: () => {},
    stop: () => {},
  };
}

test("nav links drive the route table — navigating to /incidents mounts that view", async () => {
  const store = staticStore();
  const { getByText, getByRole } = render(() => (
    <StoreProvider store={store}>
      <MemoryRouter root={(p) => (<><Nav />{p.children}</>)}>
        <Route path="/" component={() => <h1>Overview view</h1>} />
        <Route path="/incidents" component={() => <h1>Incidents view</h1>} />
      </MemoryRouter>
    </StoreProvider>
  ));
  // Root mounts first.
  expect(getByText("Overview view")).toBeInTheDocument();
  // Click the Incidents nav link → the Incidents route mounts. The link's text is
  // "💥 Incidents" (icon + label across nodes), so match by accessible name.
  await userEvent.click(getByRole("link", { name: /Incidents/ }));
  expect(getByText("Incidents view")).toBeInTheDocument();
});
