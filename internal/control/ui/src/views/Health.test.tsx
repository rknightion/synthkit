import { test, expect } from "vitest";
import { render, fireEvent } from "@solidjs/testing-library";
import { MemoryRouter, Route } from "@solidjs/router";
import { Health } from "./Health";
import { StoreProvider, type ControlStore, type Snapshot } from "../store/store";
import type { HealthReport } from "../api/types";

function fakeStore(state: Partial<Snapshot>): ControlStore {
  return {
    state: { loading: false, errors: {}, ...state } as Snapshot,
    refresh: async () => {},
    start: () => {},
    stop: () => {},
  };
}

function renderHealth(store: ControlStore) {
  return render(() => (
    <StoreProvider store={store}>
      <MemoryRouter>
        <Route path="/" component={Health} />
      </MemoryRouter>
    </StoreProvider>
  ));
}

// Realistic health snapshot used across tests.
const FAKE_HEALTH: HealthReport = {
  process: { goroutines: 42, heap_bytes: 31457280, gc_count: 7 },
  blueprints: [
    {
      blueprint: "acme",
      cycles: 120,
      dropped_ticks: 0,
      last_cycle_ms: 14.5,
      cycle_spark: [10, 12, 14, 15, 14],
    },
    {
      blueprint: "beta",
      cycles: 80,
      dropped_ticks: 3,
      last_cycle_ms: 200.0,
      cycle_spark: [50, 100, 150, 200],
    },
  ],
  constructs: [
    {
      blueprint: "acme",
      kind: "ec2",
      name: "web",
      ticks: 500,
      errors: 0,
      last_outcome: "ok",
      last_error: "",
      last_tick_ms: 1000,
      last_dur_ms: 1.0,
      p95_dur_ms: 1.0,
      spark: [1, 1, 1],
    },
    {
      blueprint: "acme",
      kind: "rds",
      name: "db",
      ticks: 500,
      errors: 2,
      last_outcome: "error",
      last_error: "connection refused",
      last_tick_ms: 1000,
      last_dur_ms: 20.0,
      p95_dur_ms: 20.0,
      spark: [18, 20, 21],
    },
    {
      blueprint: "beta",
      kind: "ec2",
      name: "app",
      ticks: 300,
      errors: 0,
      last_outcome: "ok",
      last_error: "",
      last_tick_ms: 1000,
      last_dur_ms: 100.0,
      p95_dur_ms: 100.0,
      spark: [90, 100, 110],
    },
  ],
};

// ── distinct state tests ────────────────────────────────────────────────────

test("shows loading state while data has not arrived", () => {
  const store = fakeStore({ loading: true });
  const { getByTestId, queryByTestId } = renderHealth(store);
  expect(getByTestId("health-loading")).toBeInTheDocument();
  expect(queryByTestId("health-empty")).not.toBeInTheDocument();
  expect(queryByTestId("health-error")).not.toBeInTheDocument();
});

test("shows error state when health GET fails", () => {
  const store = fakeStore({ loading: false, errors: { health: "connection refused" } });
  const { getByTestId, queryByTestId } = renderHealth(store);
  const errNode = getByTestId("health-error");
  expect(errNode).toBeInTheDocument();
  expect(errNode.textContent).toContain("connection refused");
  expect(queryByTestId("health-loading")).not.toBeInTheDocument();
  expect(queryByTestId("health-empty")).not.toBeInTheDocument();
});

test("shows empty state when no constructs have ticked yet", () => {
  const store = fakeStore({
    health: { process: { goroutines: 10, heap_bytes: 0, gc_count: 0 }, blueprints: [], constructs: [] },
  });
  const { getByTestId, queryByTestId } = renderHealth(store);
  const emptyNode = getByTestId("health-empty");
  expect(emptyNode).toBeInTheDocument();
  expect(emptyNode.textContent).toContain("No construct tick data yet");
  expect(queryByTestId("health-loading")).not.toBeInTheDocument();
  expect(queryByTestId("health-error")).not.toBeInTheDocument();
});

// ── data state tests ─────────────────────────────────────────────────────────

test("renders process stats from health snapshot", () => {
  const store = fakeStore({ health: FAKE_HEALTH });
  const { getByTestId, getByText } = renderHealth(store);
  const statsBlock = getByTestId("health-process-stats");
  expect(statsBlock).toBeInTheDocument();
  // goroutines: 42 → "42"
  expect(getByText("42")).toBeInTheDocument();
  // gc_count: 7 → "7"
  expect(getByText("7")).toBeInTheDocument();
  // heap_bytes: 31457280 = 30 MB → "30 MB"
  expect(getByText("30 MB")).toBeInTheDocument();
});

test("renders a construct row from the health snapshot", () => {
  const store = fakeStore({ health: FAKE_HEALTH });
  const { getByTestId } = renderHealth(store);
  // Construct: blueprint=acme kind=ec2 name=web
  const row = getByTestId("cst-row-acme-ec2-web");
  expect(row).toBeInTheDocument();
  expect(row.textContent).toContain("web");
  expect(row.textContent).toContain("500");
  // p95 cell for "web" construct: 1.0 ms → "1.0 ms"
  const p95 = getByTestId("p95-cell-web");
  expect(p95.textContent).toBe("1.0 ms");
});

// ── p95 numeric sort test ────────────────────────────────────────────────────

test("p95 column sorts numerically (1ms < 20ms < 100ms), not lexically", () => {
  // Default sort is blueprint asc → acme/ec2/web (p95=1), acme/rds/db (p95=20), beta/ec2/app (p95=100)
  // Click p95 col to sort asc by p95 — should be: web(1) < db(20) < app(100)
  const store = fakeStore({ health: FAKE_HEALTH });
  const { getByTestId, getAllByTestId } = renderHealth(store);

  const p95Col = getByTestId("col-p95");
  fireEvent.click(p95Col); // sort p95 asc

  // All p95 cells in DOM order
  const cells = getAllByTestId(/^p95-cell-/);
  const names = cells.map((c) => c.getAttribute("data-testid")!.replace("p95-cell-", ""));
  // With numeric asc: 1 < 20 < 100 → web, db, app
  // With lexical asc: "1.0" < "100" < "20" → web, app, db  ← the legacy bug
  expect(names[0]).toBe("web");   // p95 = 1.0
  expect(names[1]).toBe("db");    // p95 = 20.0
  expect(names[2]).toBe("app");   // p95 = 100.0
});

test("p95 column sorts numerically descending", () => {
  const store = fakeStore({ health: FAKE_HEALTH });
  const { getByTestId, getAllByTestId } = renderHealth(store);

  const p95Col = getByTestId("col-p95");
  fireEvent.click(p95Col); // asc
  fireEvent.click(p95Col); // desc

  const cells = getAllByTestId(/^p95-cell-/);
  const names = cells.map((c) => c.getAttribute("data-testid")!.replace("p95-cell-", ""));
  expect(names[0]).toBe("app");   // p95 = 100.0 (largest first)
  expect(names[1]).toBe("db");    // p95 = 20.0
  expect(names[2]).toBe("web");   // p95 = 1.0
});

// ── dropped_ticks > 0 gets red class ────────────────────────────────────────

test("blueprint row with dropped_ticks > 0 carries the error class", () => {
  const store = fakeStore({ health: FAKE_HEALTH });
  const { getByTestId } = renderHealth(store);

  const droppedRow = getByTestId("bp-row-beta");
  expect(droppedRow.className).toContain("h-row-err");

  const cleanRow = getByTestId("bp-row-acme");
  expect(cleanRow.className).not.toContain("h-row-err");
});

// ── filter narrows construct rows ────────────────────────────────────────────

test("filter input narrows construct rows by blueprint substring", () => {
  const store = fakeStore({ health: FAKE_HEALTH });
  const { getByTestId, queryByTestId } = renderHealth(store);

  const filterInput = getByTestId("health-filter") as HTMLInputElement;

  // Before: all three construct rows present
  expect(getByTestId("cst-row-acme-ec2-web")).toBeInTheDocument();
  expect(getByTestId("cst-row-acme-rds-db")).toBeInTheDocument();
  expect(getByTestId("cst-row-beta-ec2-app")).toBeInTheDocument();

  // Filter "beta" → only beta row
  fireEvent.input(filterInput, { target: { value: "beta" } });
  expect(queryByTestId("cst-row-acme-ec2-web")).not.toBeInTheDocument();
  expect(queryByTestId("cst-row-acme-rds-db")).not.toBeInTheDocument();
  expect(getByTestId("cst-row-beta-ec2-app")).toBeInTheDocument();
});

test("filter input narrows construct rows by kind substring", () => {
  const store = fakeStore({ health: FAKE_HEALTH });
  const { getByTestId, queryByTestId } = renderHealth(store);

  const filterInput = getByTestId("health-filter") as HTMLInputElement;

  // Filter "rds" → only rds row
  fireEvent.input(filterInput, { target: { value: "rds" } });
  expect(queryByTestId("cst-row-acme-ec2-web")).not.toBeInTheDocument();
  expect(getByTestId("cst-row-acme-rds-db")).toBeInTheDocument();
  expect(queryByTestId("cst-row-beta-ec2-app")).not.toBeInTheDocument();
});

test("filter input narrows construct rows by name substring", () => {
  const store = fakeStore({ health: FAKE_HEALTH });
  const { getByTestId, queryByTestId } = renderHealth(store);

  const filterInput = getByTestId("health-filter") as HTMLInputElement;

  // Filter "app" → only app row
  fireEvent.input(filterInput, { target: { value: "app" } });
  expect(queryByTestId("cst-row-acme-ec2-web")).not.toBeInTheDocument();
  expect(queryByTestId("cst-row-acme-rds-db")).not.toBeInTheDocument();
  expect(getByTestId("cst-row-beta-ec2-app")).toBeInTheDocument();
});

test("filter with no match hides all construct rows", () => {
  const store = fakeStore({ health: FAKE_HEALTH });
  const { getByTestId, queryByTestId } = renderHealth(store);

  const filterInput = getByTestId("health-filter") as HTMLInputElement;
  fireEvent.input(filterInput, { target: { value: "NOMATCH_XYZ" } });

  expect(queryByTestId("cst-row-acme-ec2-web")).not.toBeInTheDocument();
  expect(queryByTestId("cst-row-acme-rds-db")).not.toBeInTheDocument();
  expect(queryByTestId("cst-row-beta-ec2-app")).not.toBeInTheDocument();
});
