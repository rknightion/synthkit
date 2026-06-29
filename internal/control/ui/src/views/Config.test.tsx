import { test, expect } from "vitest";
import { render, fireEvent } from "@solidjs/testing-library";
import { MemoryRouter, Route } from "@solidjs/router";
import { Config } from "./Config";
import { StoreProvider, type ControlStore, type Snapshot } from "../store/store";
import type { ConfigView } from "../api/types";

// fakeStore: static snapshot, inert lifecycle.
function fakeStore(state: Partial<Snapshot>): ControlStore {
  return {
    state: { loading: false, errors: {}, ...state } as Snapshot,
    refresh: async () => {},
    start: () => {},
    stop: () => {},
  };
}

// Config has no <A> links, but wrapping in MemoryRouter keeps it consistent
// with Overview and guards against future link additions.
function renderConfig(store: ControlStore) {
  return render(() => (
    <StoreProvider store={store}>
      <MemoryRouter>
        <Route path="/" component={Config} />
      </MemoryRouter>
    </StoreProvider>
  ));
}

// A realistic ConfigView fixture: two groups, one with a secret field.
const FAKE_CONFIG: ConfigView = {
  groups: [
    {
      title: "Metrics",
      fields: [
        { key: "GC_PROM_RW", value: "https://metrics.example.com/api/prom/push", secret: false, configured: true },
        { key: "GC_TOKEN", value: "", secret: true, configured: true },
        { key: "GC_ORG_ID", value: "12345", secret: false, configured: true },
      ],
    },
    {
      title: "Tracing",
      fields: [
        { key: "GC_OTLP_ENDPOINT", value: "https://otlp.example.com", secret: false, configured: true },
        { key: "GC_UNUSED_SECRET", value: "", secret: true, configured: false },
      ],
    },
  ],
};

// ── distinct state tests ────────────────────────────────────────────────────

test("shows loading state while data has not arrived", () => {
  const store = fakeStore({ loading: true });
  const { getByTestId, queryByTestId } = renderConfig(store);
  expect(getByTestId("config-loading")).toBeInTheDocument();
  expect(queryByTestId("config-empty")).not.toBeInTheDocument();
  expect(queryByTestId("config-error")).not.toBeInTheDocument();
});

test("shows error state when config GET fails", () => {
  const store = fakeStore({ loading: false, errors: { config: "connection refused" } });
  const { getByTestId, queryByTestId } = renderConfig(store);
  const errNode = getByTestId("config-error");
  expect(errNode).toBeInTheDocument();
  expect(errNode.textContent).toContain("connection refused");
  expect(queryByTestId("config-loading")).not.toBeInTheDocument();
  expect(queryByTestId("config-empty")).not.toBeInTheDocument();
});

test("shows empty state when config is not available (null/undefined)", () => {
  const store = fakeStore({ loading: false, config: undefined });
  const { getByTestId, queryByTestId } = renderConfig(store);
  expect(getByTestId("config-empty")).toBeInTheDocument();
  expect(queryByTestId("config-loading")).not.toBeInTheDocument();
  expect(queryByTestId("config-error")).not.toBeInTheDocument();
});

// ── data state tests ─────────────────────────────────────────────────────────

test("renders grouped config keys from a config snapshot", () => {
  const store = fakeStore({ config: FAKE_CONFIG });
  const { getAllByText, getByText } = renderConfig(store);
  // Group headings (exact match to avoid collision with value URLs containing "metrics")
  expect(getAllByText("Metrics").length).toBeGreaterThan(0);
  expect(getAllByText("Tracing").length).toBeGreaterThan(0);
  // Non-secret keys and values
  expect(getByText("GC_PROM_RW")).toBeInTheDocument();
  expect(getByText("https://metrics.example.com/api/prom/push")).toBeInTheDocument();
  expect(getByText("GC_ORG_ID")).toBeInTheDocument();
  expect(getByText("12345")).toBeInTheDocument();
});

test("secret field shows '● set' chip when configured and NEVER renders the value", () => {
  const store = fakeStore({ config: FAKE_CONFIG });
  const { getByTestId, queryByText } = renderConfig(store);
  // The key GC_TOKEN has secret:true, configured:true — chip must say "● set"
  const chip = getByTestId("secret-chip-GC_TOKEN");
  expect(chip).toBeInTheDocument();
  expect(chip.textContent).toBe("● set");
  // The value field is "" for secrets — but we ensure the chip class is correct
  expect(chip.className).toContain("set");
  // The raw (empty) value must never appear as a rendered value cell text
  // (The value is "" so there is nothing to leak — but asserting the chip text
  // is correct and the value "" is absent from the chip's text is the key check.)
  expect(chip.textContent).not.toContain("unset");
});

test("secret field shows '○ not set' chip when not configured", () => {
  const store = fakeStore({ config: FAKE_CONFIG });
  const { getByTestId } = renderConfig(store);
  const chip = getByTestId("secret-chip-GC_UNUSED_SECRET");
  expect(chip).toBeInTheDocument();
  expect(chip.textContent).toBe("○ not set");
  expect(chip.className).toContain("unset");
});

test("secret field with a hypothetical non-empty value does NOT render the value string", () => {
  const configWithLeakCanary: ConfigView = {
    groups: [{
      title: "Security",
      fields: [
        // Even if value were somehow populated on the wire, we must never render it
        { key: "CANARY_SECRET", value: "super-secret-sentinel-value", secret: true, configured: true },
      ],
    }],
  };
  const store = fakeStore({ config: configWithLeakCanary });
  const { queryByText } = renderConfig(store);
  // The sentinel value must NOT appear anywhere in the rendered DOM
  expect(queryByText("super-secret-sentinel-value")).not.toBeInTheDocument();
});

// ── filter tests ─────────────────────────────────────────────────────────────

test("filter input narrows visible rows by key substring", () => {
  const store = fakeStore({ config: FAKE_CONFIG });
  const { getByTestId, queryByTestId } = renderConfig(store);
  const filterInput = getByTestId("config-filter") as HTMLInputElement;

  // Before filtering: all rows visible
  expect(getByTestId("cfg-row-GC_PROM_RW")).toBeInTheDocument();
  expect(getByTestId("cfg-row-GC_TOKEN")).toBeInTheDocument();
  expect(getByTestId("cfg-row-GC_ORG_ID")).toBeInTheDocument();

  // Type a substring that matches only GC_PROM_RW and GC_TOKEN (both contain "GC_")
  // but also matches all. Use "PROM" to narrow to just one row.
  fireEvent.input(filterInput, { target: { value: "PROM" } });

  expect(getByTestId("cfg-row-GC_PROM_RW")).toBeInTheDocument();
  expect(queryByTestId("cfg-row-GC_TOKEN")).not.toBeInTheDocument();
  expect(queryByTestId("cfg-row-GC_ORG_ID")).not.toBeInTheDocument();
});

test("filter input narrows rows by value substring", () => {
  const store = fakeStore({ config: FAKE_CONFIG });
  const { getByTestId, queryByTestId } = renderConfig(store);
  const filterInput = getByTestId("config-filter") as HTMLInputElement;

  // Filter by value substring "12345" — matches GC_ORG_ID
  fireEvent.input(filterInput, { target: { value: "12345" } });

  expect(getByTestId("cfg-row-GC_ORG_ID")).toBeInTheDocument();
  expect(queryByTestId("cfg-row-GC_PROM_RW")).not.toBeInTheDocument();
});

test("filter hides all rows when no match", () => {
  const store = fakeStore({ config: FAKE_CONFIG });
  const { getByTestId, queryByTestId } = renderConfig(store);
  const filterInput = getByTestId("config-filter") as HTMLInputElement;

  fireEvent.input(filterInput, { target: { value: "NOMATCH_XYZ" } });

  // All rows absent
  expect(queryByTestId("cfg-row-GC_PROM_RW")).not.toBeInTheDocument();
  expect(queryByTestId("cfg-row-GC_TOKEN")).not.toBeInTheDocument();
  expect(queryByTestId("cfg-row-GC_ORG_ID")).not.toBeInTheDocument();
});
