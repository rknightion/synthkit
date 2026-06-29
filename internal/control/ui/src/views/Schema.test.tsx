import { test, expect, vi, beforeEach } from "vitest";
import { render, fireEvent, waitFor } from "@solidjs/testing-library";
import { Schema } from "./Schema";
import { StoreProvider, type ControlStore, type Snapshot } from "../store/store";
import type { BpSchemaDoc } from "../api/types";

// Schema fetches lazily on mount via getJSON("blueprint-schema") which calls
// global fetch. Mock global.fetch so tests run without a server.

beforeEach(() => {
  vi.restoreAllMocks();
});

// Minimal inert store — Schema doesn't use the polled store, but the component
// tree requires StoreProvider (Rail/Nav read from it indirectly if ever present).
function fakeStore(): ControlStore {
  return {
    state: { loading: false, errors: {} } as Snapshot,
    refresh: vi.fn(async () => {}),
    start: () => {},
    stop: () => {},
  };
}

function renderSchema() {
  const store = fakeStore();
  const result = render(() => (
    <StoreProvider store={store}>
      <Schema />
    </StoreProvider>
  ));
  return result;
}

// Build a mock fetch response from a BpSchemaDoc.
function mockFetchOk(doc: BpSchemaDoc) {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(JSON.stringify(doc), { status: 200, headers: { "Content-Type": "application/json" } }),
  );
}

function mockFetch404(body = "blueprint schema not configured") {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(body, { status: 404 }),
  );
}

function mockFetchNetworkError() {
  vi.spyOn(globalThis, "fetch").mockRejectedValue(new Error("network error"));
}

// ── Realistic fixture ────────────────────────────────────────────────────────

const FAKE_SCHEMA: BpSchemaDoc = {
  sections: [
    {
      title: "Blueprint document",
      path: "(top level)",
      doc: "The blueprint YAML document.",
      group: "blueprint",
      fields: [
        { key: "name", type: "string", doc: "Blueprint name", optional: false },
        { key: "envs", type: "object", repeated: true, doc: "Environment declarations", fields: [
          { key: "name", type: "string", doc: "Environment name" },
          { key: "region", type: "string", doc: "AWS region", optional: true },
        ]},
      ],
    },
    {
      title: "rds config",
      path: "integrations.rds",
      doc: "RDS construct configuration.",
      group: "integration",
      fields: [
        { key: "engine", type: "string", doc: "DB engine", enum: ["postgres", "mysql"] },
        { key: "multi_az", type: "bool", doc: "Enable multi-AZ", optional: true },
      ],
    },
    {
      title: "Empty section",
      path: "integrations.empty",
      group: "integration",
      fields: [],
    },
  ],
};

// ── State tests ──────────────────────────────────────────────────────────────

test("shows spinner while fetch is in flight", async () => {
  // Return a promise that never resolves so we can observe the loading state.
  let _resolve!: (v: Response) => void;
  vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise((res) => { _resolve = res; }));

  const { getByTestId, queryByTestId } = renderSchema();

  // Spinner is immediately visible
  expect(getByTestId("schema-loading")).toBeInTheDocument();
  // Data and error must not be present yet
  expect(queryByTestId("schema-error")).not.toBeInTheDocument();

  // Resolve to avoid leaking the promise
  _resolve(new Response(JSON.stringify({ sections: [] }), { status: 200 }));
});

test("shows data after fetch resolves", async () => {
  mockFetchOk(FAKE_SCHEMA);

  const { queryByTestId, getByTestId } = renderSchema();

  // Spinner first, then data
  expect(getByTestId("schema-loading")).toBeInTheDocument();

  await waitFor(() => expect(queryByTestId("schema-loading")).not.toBeInTheDocument());

  // Filter bar visible in data state
  expect(getByTestId("schema-filter")).toBeInTheDocument();
  // Error must not appear
  expect(queryByTestId("schema-error")).not.toBeInTheDocument();
});

test("shows error state on 404", async () => {
  mockFetch404();

  const { queryByTestId, getByTestId } = renderSchema();

  await waitFor(() => expect(queryByTestId("schema-loading")).not.toBeInTheDocument());

  const errNode = getByTestId("schema-error");
  expect(errNode).toBeInTheDocument();
  expect(errNode.textContent).toContain("not configured");
});

test("shows error state on network failure", async () => {
  mockFetchNetworkError();

  const { queryByTestId, getByTestId } = renderSchema();

  await waitFor(() => expect(queryByTestId("schema-loading")).not.toBeInTheDocument());

  const errNode = getByTestId("schema-error");
  expect(errNode).toBeInTheDocument();
  expect(errNode.textContent).toContain("network error");
});

// ── Schema row rendering ─────────────────────────────────────────────────────

test("renders top-level fields as rows", async () => {
  mockFetchOk(FAKE_SCHEMA);

  const { queryByTestId } = renderSchema();

  await waitFor(() => expect(queryByTestId("schema-loading")).not.toBeInTheDocument());

  // Top-level keys
  expect(queryByTestId("schema-row-name")).toBeInTheDocument();
  expect(queryByTestId("schema-row-envs[]")).toBeInTheDocument();
});

test("walks nested fields into dotted keys", async () => {
  mockFetchOk(FAKE_SCHEMA);

  const { queryByTestId } = renderSchema();

  await waitFor(() => expect(queryByTestId("schema-loading")).not.toBeInTheDocument());

  // envs[] is repeated → children become envs[].name and envs[].region
  expect(queryByTestId("schema-row-envs[].name")).toBeInTheDocument();
  expect(queryByTestId("schema-row-envs[].region")).toBeInTheDocument();
});

test("renders enum values as part of the type cell", async () => {
  mockFetchOk(FAKE_SCHEMA);

  const { getByTestId } = renderSchema();

  await waitFor(() => expect(getByTestId("schema-filter")).toBeInTheDocument());

  const engineRow = getByTestId("schema-row-engine");
  expect(engineRow.textContent).toContain("postgres");
  expect(engineRow.textContent).toContain("mysql");
  expect(engineRow.textContent).toContain("∈");
});

test("renders section titles", async () => {
  mockFetchOk(FAKE_SCHEMA);

  const { getByTestId, container } = renderSchema();

  // Wait until the spinner is gone (data has loaded)
  await waitFor(() => expect(getByTestId("schema-filter")).toBeInTheDocument());

  // Section titles are rendered as text nodes inside .sec-label divs.
  // getAllByText searches text content of all elements; the title text may appear
  // multiple times (e.g. heading + meta count) so we just require at least one match.
  const labels = container.querySelectorAll(".sec-label");
  const labelTexts = Array.from(labels).map((el) => el.textContent ?? "");
  expect(labelTexts.some((t) => t.includes("Blueprint document"))).toBe(true);
  expect(labelTexts.some((t) => t.includes("rds config"))).toBe(true);
});

test("empty section shows '(no configurable fields)' message", async () => {
  mockFetchOk(FAKE_SCHEMA);

  const { getByText } = renderSchema();

  await waitFor(() => {
    expect(getByText("(no configurable fields)")).toBeInTheDocument();
  });
});

// ── Filter tests ─────────────────────────────────────────────────────────────

test("filter input narrows visible rows by key substring", async () => {
  mockFetchOk(FAKE_SCHEMA);

  const { getByTestId, queryByTestId } = renderSchema();

  await waitFor(() => expect(getByTestId("schema-filter")).toBeInTheDocument());

  // Before filtering: nested rows visible
  expect(queryByTestId("schema-row-envs[].name")).toBeInTheDocument();
  expect(queryByTestId("schema-row-envs[].region")).toBeInTheDocument();
  expect(queryByTestId("schema-row-engine")).toBeInTheDocument();

  fireEvent.input(getByTestId("schema-filter"), { target: { value: "engine" } });

  // Only the engine row should remain (key "engine" matches "engine")
  expect(queryByTestId("schema-row-engine")).toBeInTheDocument();
  expect(queryByTestId("schema-row-envs[].name")).not.toBeInTheDocument();
});

test("filter matches against description/doc text", async () => {
  mockFetchOk(FAKE_SCHEMA);

  const { getByTestId, queryByTestId } = renderSchema();

  await waitFor(() => expect(getByTestId("schema-filter")).toBeInTheDocument());

  // Filter by description text unique to the region field
  fireEvent.input(getByTestId("schema-filter"), { target: { value: "AWS region" } });

  expect(queryByTestId("schema-row-envs[].region")).toBeInTheDocument();
  expect(queryByTestId("schema-row-engine")).not.toBeInTheDocument();
});

test("filter matches against type text", async () => {
  mockFetchOk(FAKE_SCHEMA);

  const { getByTestId, queryByTestId } = renderSchema();

  await waitFor(() => expect(getByTestId("schema-filter")).toBeInTheDocument());

  // Filter by "bool" type — only multi_az has type "bool"
  fireEvent.input(getByTestId("schema-filter"), { target: { value: "bool" } });

  expect(queryByTestId("schema-row-multi_az")).toBeInTheDocument();
  expect(queryByTestId("schema-row-name")).not.toBeInTheDocument();
});

test("filter shows 'No matching fields.' when nothing matches", async () => {
  mockFetchOk(FAKE_SCHEMA);

  const { getByTestId, queryAllByText, getAllByText } = renderSchema();

  await waitFor(() => expect(getByTestId("schema-filter")).toBeInTheDocument());

  // Before filtering: no "No matching fields." messages
  expect(queryAllByText("No matching fields.").length).toBe(0);

  fireEvent.input(getByTestId("schema-filter"), { target: { value: "NOMATCH_XYZ_999" } });

  // Every section shows the no-match message (one per section with data)
  expect(getAllByText("No matching fields.").length).toBeGreaterThan(0);
});

// ── Sort tests ───────────────────────────────────────────────────────────────

test("clicking the key column header toggles sort direction", async () => {
  mockFetchOk(FAKE_SCHEMA);

  const { getByTestId, getAllByTestId, container } = renderSchema();

  await waitFor(() => expect(getByTestId("schema-filter")).toBeInTheDocument());

  // There is one col-key header per section that has rows. Use the first one.
  const keyCol = getAllByTestId("col-key")[0];

  // Default state: key ↑ (asc)
  expect(keyCol.textContent).toContain("↑");

  // First click: toggle to desc
  fireEvent.click(keyCol);
  // Re-query after reactivity update
  await waitFor(() => {
    expect(container.querySelector("[data-testid='col-key']")!.textContent).toContain("↓");
  });

  // Second click: back to asc
  fireEvent.click(container.querySelector("[data-testid='col-key']")!);
  await waitFor(() => {
    expect(container.querySelector("[data-testid='col-key']")!.textContent).toContain("↑");
  });
});
