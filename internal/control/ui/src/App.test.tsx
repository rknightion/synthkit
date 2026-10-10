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

afterEach(() => {
  vi.unstubAllGlobals();
  document.querySelector("base")?.remove();
  document.querySelector('meta[name="control-api-prefix"]')?.remove();
  window.history.replaceState({}, "", "/");
});

// The shell renders and the "/" Overview route mounts. App owns its own Router
// (base derived from server-injected <base>, absent in dev), the StoreProvider,
// and the lifecycle in onMount — this proves the frame composes end-to-end.
test("renders the shell and mounts the Overview route at /", () => {
  const { getByRole } = render(() => <App />);
  // Rail brand is present (shell rendered).
  expect(getByRole("complementary")).toBeInTheDocument(); // <aside class="rail">
  // Overview view mounted at "/" (the view heading, not the nav link).
  expect(getByRole("heading", { name: "Overview", level: 1 })).toBeInTheDocument();
});

test("prefixed deep links render and DOM navigation/assets/API stay under prefix", async () => {
  const base = document.createElement("base");
  base.href = "/x/y/control/ui/";
  document.head.append(base);
  const meta = document.createElement("meta");
  meta.name = "control-api-prefix";
  meta.content = "/x/y/control/";
  document.head.append(meta);
  window.history.replaceState({}, "", "/x/y/control/ui/config");
  const page = render(() => <App />);
  expect(await page.findByRole("heading", { name: "Config", level: 1 })).toBeInTheDocument();
  const overview = page.getByRole("link", { name: /Overview/ });
  // Solid Router canonicalizes the root link without the trailing slash;
  // the server's tested redirect canonicalizes full-page navigation.
  expect(overview.getAttribute("href")).toBe("/x/y/control/ui");
  expect(vi.mocked(fetch).mock.calls.every(([url]) => String(url).startsWith("/x/y/control/"))).toBe(true);
  const script = document.createElement("script");
  script.src = "./assets/app.js";
  expect(new URL(script.src).pathname).toBe("/x/y/control/ui/assets/app.js");
  page.unmount();
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
