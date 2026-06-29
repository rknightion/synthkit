import { test, expect } from "vitest";
import { render } from "@solidjs/testing-library";
import { StoreProvider, type ControlStore } from "../store/store";
import type { SinkStat, StatusReport } from "../api/types";
import { Status } from "./Status";

// A static store standing in for the live one — Status reads only state.status.
// No polling; refresh/start/stop are inert. Tests pass a StatusReport (or omit it
// to exercise the null-guard).
function storeWith(status?: StatusReport): ControlStore {
  return {
    state: { loading: false, errors: {}, status },
    refresh: async () => {},
    start: () => {},
    stop: () => {},
  };
}

// Minimal SinkStat factory — only the fields the panel reads matter; the rest
// default to zero/empty so each test states just its variable.
function sink(over: Partial<SinkStat>): SinkStat {
  return {
    sink: "promrw",
    last_success_ms: 0,
    last_error_ms: 0,
    last_error: "",
    pushes: 0,
    failures: 0,
    last_items: 0,
    last_status: 0,
    total_items: 0,
    rate_per_min: 0,
    spark: [],
    dry_run: false,
    ...over,
  };
}

const now = Date.now();

function persist() {
  return { last_ok_ms: now, last_error_ms: 0, last_error: "" };
}

test("renders a row per sink with an ok/err indicator and surfaces the failing sink's failure count", () => {
  const status: StatusReport = {
    sinks: [
      sink({ sink: "promrw", last_success_ms: now, pushes: 10, total_items: 1234 }),
      sink({
        sink: "loki",
        last_error: "connection refused",
        last_error_ms: now,
        last_success_ms: now - 60_000,
        pushes: 4,
        failures: 3,
      }),
    ],
    dry_run: false,
    persist: persist(),
  };
  const { getByText, container } = render(() => (
    <StoreProvider store={storeWith(status)}>
      <Status />
    </StoreProvider>
  ));

  // Sinks render under their operator-facing signal-class labels.
  expect(getByText("metrics")).toBeInTheDocument(); // promrw
  expect(getByText("logs")).toBeInTheDocument(); // loki

  // ok/err indicator present per row: one ok dot (promrw), one err row (loki).
  expect(container.querySelector(".emit.ok")).not.toBeNull();
  const errRow = container.querySelector(".emit.err");
  expect(errRow).not.toBeNull();

  // The failing sink's failure count surfaces, and its last error is shown.
  expect(getByText(/3 failed/)).toBeInTheDocument();
  expect(getByText(/connection refused/)).toBeInTheDocument();
});

test("renders the prominent dry-run badge when dry_run is true", () => {
  const status: StatusReport = {
    sinks: [sink({ sink: "promrw", last_success_ms: now, total_items: 1 })],
    dry_run: true,
    persist: persist(),
  };
  const { getByText } = render(() => (
    <StoreProvider store={storeWith(status)}>
      <Status />
    </StoreProvider>
  ));
  expect(getByText("dry run")).toBeInTheDocument();
});

test("omits the dry-run badge when dry_run is false", () => {
  const status: StatusReport = {
    sinks: [sink({ sink: "promrw", last_success_ms: now, total_items: 1 })],
    dry_run: false,
    persist: persist(),
  };
  const { queryByText } = render(() => (
    <StoreProvider store={storeWith(status)}>
      <Status />
    </StoreProvider>
  ));
  expect(queryByText("dry run")).toBeNull();
});

test("status undefined renders the muted fallback without crashing", () => {
  const { getByText, container } = render(() => (
    <StoreProvider store={storeWith(undefined)}>
      <Status />
    </StoreProvider>
  ));
  // No emitter rows; a muted unavailable line instead of a crash.
  expect(container.querySelector(".emit")).toBeNull();
  expect(getByText(/status unavailable/i)).toBeInTheDocument();
});

test("renders the fleet (FM) line when fleet is registering or heartbeating", () => {
  const status: StatusReport = {
    sinks: [sink({ sink: "promrw", last_success_ms: now, total_items: 1 })],
    fleet: { registered: 3, heartbeats: 42, last_ok_ms: now, last_error_ms: 0, last_error: "", failures: 0, dry_run: false },
    dry_run: false,
    persist: persist(),
  };
  const { getByText } = render(() => (
    <StoreProvider store={storeWith(status)}>
      <Status />
    </StoreProvider>
  ));
  expect(getByText("fm")).toBeInTheDocument();
  expect(getByText(/3 collectors/)).toBeInTheDocument();
});

test("renders the passive auth note always (with and without status)", () => {
  // With status present
  const { getByTestId } = render(() => (
    <StoreProvider store={storeWith({ sinks: [], dry_run: false, persist: persist() })}>
      <Status />
    </StoreProvider>
  ));
  const note = getByTestId("auth-note");
  expect(note).toBeInTheDocument();
  expect(note.textContent).toMatch(/control token/i);
  expect(note.textContent).toMatch(/if configured/i);
});

test("renders a persist-error line when persist health carries an error", () => {
  const status: StatusReport = {
    sinks: [],
    dry_run: false,
    persist: { last_ok_ms: 0, last_error_ms: now, last_error: "disk full" },
  };
  const { getByText } = render(() => (
    <StoreProvider store={storeWith(status)}>
      <Status />
    </StoreProvider>
  ));
  expect(getByText(/persist: error — disk full/)).toBeInTheDocument();
});
