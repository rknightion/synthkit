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
        last_error: "raw canary must not render",
        last_error_code: "transport",
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

  // The failing sink's failure count surfaces with closed text, not raw status input.
  expect(getByText(/3 failed/)).toBeInTheDocument();
  expect(getByText(/transport failed/)).toBeInTheDocument();
  expect(container.textContent).not.toContain("raw canary");
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

test("renders current queue loss separately from recovered historical drops", () => {
  const status: StatusReport = {
    sinks: [],
    queues: [
      { sink: "promrw", depth: 7, blocked_enqueues: 2, dropped_items: 5, last_loss_ms: now,
        last_recovery_ms: 0, current_loss: true, affected_shards: 1, last_error_code: "transport" },
      { sink: "loki", depth: 0, blocked_enqueues: 1, dropped_items: 3, last_loss_ms: now - 60_000,
        last_recovery_ms: now, current_loss: false, affected_shards: 0, last_error_code: "rejected" },
    ],
    dry_run: false,
    persist: persist(),
  };
  const { getByText, container } = render(() => (
    <StoreProvider store={storeWith(status)}><Status /></StoreProvider>
  ));
  expect(getByText("metrics queue")).toBeInTheDocument();
  expect(getByText("logs queue")).toBeInTheDocument();
  expect(getByText(/5 dropped · current loss/)).toBeInTheDocument();
  expect(getByText(/3 dropped · recovered/)).toBeInTheDocument();
  expect(container.querySelectorAll(".emit.err").length).toBe(1);
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
    fleet: { registered: 3, heartbeats: 42, last_ok_ms: now, last_error_ms: 0, last_error: "", last_error_code: "", failures: 0, dry_run: false },
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

test("renders every optional lane disposition and the SM restart handoff", () => {
  const status: StatusReport = {
    sinks: [], dry_run: false, persist: persist(),
    optional_lanes: [
      { lane: "rum", requested: true, state: "enabled", reason: "enabled", declaration: "satisfied", emitter: "satisfied", verification: "verified", missing_fields: [] },
      { lane: "fleet_metrics", requested: false, state: "disabled", reason: "intentionally_disabled", declaration: "not_required", emitter: "not_required", verification: "not_required", missing_fields: [] },
      { lane: "fleet_registration", requested: false, state: "disabled", reason: "intentionally_disabled", declaration: "not_required", emitter: "not_required", verification: "not_required", missing_fields: [] },
      { lane: "synthetic_monitoring", requested: true, state: "partial", reason: "emitter_missing", declaration: "satisfied", emitter: "missing", verification: "not_attempted", missing_fields: ["GC_SM_TOKEN"] },
      { lane: "self_observability", requested: true, state: "enabled", reason: "enabled", declaration: "not_required", emitter: "satisfied", verification: "not_required", missing_fields: [] },
      { lane: "process_profiling", requested: false, state: "disabled", reason: "intentionally_disabled", declaration: "not_required", emitter: "not_required", verification: "not_required", missing_fields: [] },
      { lane: "sigil", requested: false, state: "disabled", reason: "intentionally_disabled", declaration: "not_required", emitter: "not_required", verification: "not_required", missing_fields: [] },
      { lane: "private_git", requested: true, state: "unsupported", reason: "unsupported_runtime", declaration: "satisfied", emitter: "missing", verification: "failed", missing_fields: [] },
      { lane: "synthetic_profiles", requested: false, state: "disabled", reason: "intentionally_disabled", declaration: "not_required", emitter: "not_required", verification: "not_required", missing_fields: [] },
    ],
  };
  const { getByText, getByTestId } = render(() => (
    <StoreProvider store={storeWith(status)}><Status /></StoreProvider>
  ));
  expect(getByText("Optional lanes")).toBeInTheDocument();
  expect(getByTestId("optional-lane-rum")).toHaveTextContent("enabled");
  expect(getByTestId("optional-lane-synthetic_monitoring")).toHaveTextContent("GC_SM_TOKEN");
  expect(getByText(/provision\/apply, then restart synthkit/)).toBeInTheDocument();
  expect(getByTestId("optional-lane-private_git")).toHaveTextContent("unsupported");
  expect(document.querySelectorAll("[data-testid^='optional-lane-']")).toHaveLength(9);
});

test("renders Fleet failures from the closed code and ignores raw last_error", () => {
  const status: StatusReport = {
    sinks: [], dry_run: false, persist: persist(),
    fleet: { registered: 0, heartbeats: 1, failures: 1, last_ok_ms: 0, last_error_ms: now,
      last_error: "Bearer raw-secret", last_error_code: "authentication", dry_run: false },
  };
  const { getByText, container } = render(() => (
    <StoreProvider store={storeWith(status)}><Status /></StoreProvider>
  ));
  expect(getByText(/authentication failed/)).toBeInTheDocument();
  expect(container.textContent).not.toContain("raw-secret");
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
