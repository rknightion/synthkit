import { afterEach, beforeEach, test, expect, vi } from "vitest";
import { render } from "@solidjs/testing-library";
import App from "./App";

const liveSnapshot = {
  state: { volume_multiplier: 1, active_scenarios: [], failures: {}, scaling: {}, disabled_blueprints: [], disabled_constructs: [], disabled_kinds: [], span_metrics_blueprints: [] },
  status: { sinks: [], queues: [], by_blueprint: {}, persist: { last_ok_ms: 0, last_error_ms: 0, last_error: "" }, dry_run: false },
  inventory: { blueprints: [{ blueprint: "alpha", distinct_series: 1, metric_names: 1, label_keys: 1, constructs: [] }], totals: { distinct_series: 1, constructs: 0, blueprints: 1 } },
  health: { process: { goroutines: 1, heap_bytes: 1, gc_count: 0 }, blueprints: [], constructs: [] },
  diagnostics: [],
  schema: { modes: [], targets: [], scenarios: [], constructs: [], kinds: [], volume_multiplier: { key: "volume_multiplier", type: "number", help: "", default: 1 } },
  config: { groups: [] },
  incidents: [],
  pending: { added: [], removed: [], changed: [], restart: false },
  staged: [],
  sources: [],
} as const;

beforeEach(() => {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const path = new URL(String(input), window.location.origin).pathname;
    const key = path.split("/").at(-1)!;
    const data = key === "pending" ? liveSnapshot.pending
      : key === "staged" ? liveSnapshot.staged
      : key === "sources" ? liveSnapshot.sources
      : liveSnapshot[key as keyof typeof liveSnapshot] ?? {};
    return new Response(JSON.stringify(data), { status: 200, headers: { "Content-Type": "application/json" } });
  }));
});

afterEach(() => vi.unstubAllGlobals());

// The shell renders and the "/" Overview route mounts. App owns its own Router
// (base derived from import.meta.env.BASE_URL, "" under vitest), the StoreProvider,
// and the lifecycle in onMount — this proves the frame composes end-to-end.
test("renders the shell and mounts the Overview route at /", () => {
  const { getByRole } = render(() => <App />);
  // Rail brand is present (shell rendered).
  expect(getByRole("complementary")).toBeInTheDocument(); // <aside class="rail">
  // Overview view mounted at "/" (the view heading, not the nav link).
  expect(getByRole("heading", { name: "Overview", level: 1 })).toBeInTheDocument();
});

test.each([
  ["/", /Overview/],
  ["/config", /Config/],
  ["/health", /Health/],
  ["/xray", /X-ray/],
  ["/global", /Global controls/],
  ["/bp/alpha", /alpha/],
  ["/incidents", /Incidents/],
  ["/schema", /Blueprint schema/],
  ["/blueprints", /Custom blueprints/],
])("renders %s against one live control snapshot", async (path, heading) => {
  window.history.pushState({}, "", path);
  const page = render(() => <App />);
  expect(await page.findByRole("heading", { name: heading, level: 1 })).toBeInTheDocument();
  expect(fetch).toHaveBeenCalled();
  page.unmount();
});
