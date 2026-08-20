import { test, expect, vi, beforeEach } from "vitest";
import { render, fireEvent } from "@solidjs/testing-library";
import userEvent from "@testing-library/user-event";
import { BpManage } from "./BpManage";
import { StoreProvider, type ControlStore, type Snapshot } from "../store/store";
import type {
  PendingChanges,
  SourceView,
  StagedBlueprint,
  ValidationResult,
} from "../api/types";

type LifecycleSource = SourceView & {
  fetched_sha: string;
  fetched_file_count: number;
  effective_names: string[] | null;
  observed_sha: string;
  loaded_sha: string;
  pending_restart: boolean;
  loaded_names: string[] | null;
  skipped: string[] | null;
};

beforeEach(() => {
  vi.restoreAllMocks();
});

// fakeStore stands in for the live polling store: a static Snapshot, inert lifecycle.
function fakeStore(state: Partial<Snapshot>): ControlStore {
  return {
    state: { loading: false, errors: {}, ...state } as Snapshot,
    refresh: vi.fn(async () => {}),
    start: () => {},
    stop: () => {},
  };
}

function renderBpManage(store: ControlStore) {
  return render(() => (
    <StoreProvider store={store}>
      <BpManage />
    </StoreProvider>
  ));
}

const staged = (over: Partial<StagedBlueprint> = {}): StagedBlueprint => ({
  name: "team-a/myapp",
  provenance: "upload",
  source_id: "",
  ...over,
});

const source = (over: Partial<LifecycleSource> = {}): LifecycleSource => ({
  id: "src-1",
  name: "my-org-blueprints",
  namespace: "team-a",
  url: "https://github.com/acme/blueprints.git",
  ref: "refs/heads/main",
  subpath: "blueprints/",
  token_env_var: "MY_GITHUB_TOKEN",
  fetched_sha: "deadbeefcafef00d",
  fetched_file_count: 2,
  effective_names: ["team-a/checkout", "team-a/payments"],
  observed_sha: "deadbeefcafef00d",
  last_fetch_ms: 0,
  last_err: "",
  loaded_sha: "cafebabedeadbeef",
  pending_restart: true,
  loaded_names: ["team-a/checkout"],
  skipped: ["broken.yaml: unknown construct"],
  ...over,
});

const pending = (over: Partial<PendingChanges> = {}): PendingChanges => ({
  added: [],
  removed: [],
  changed: [],
  restart: false,
  ...over,
});

// Mock fetch returning 200 with a configurable JSON body; helpers read the posted body /
// path / method (same harness as Incidents.test.tsx).
function stubFetch(body: unknown = {}) {
  const fn = vi.fn(
    async (_input: RequestInfo | URL, _init?: RequestInit) =>
      new Response(JSON.stringify(body), { status: 200 }),
  );
  vi.stubGlobal("fetch", fn);
  return {
    fn,
    bodyFor(path: string): unknown {
      const call = fn.mock.calls.find((c) => c[0] === `/control/${path}`);
      if (!call) return undefined;
      const raw = call[1]?.body;
      return raw == null ? null : JSON.parse(raw as string);
    },
    pathCalled(path: string): boolean {
      return fn.mock.calls.some((c) => c[0] === `/control/${path}`);
    },
    methodFor(path: string): string | undefined {
      const call = fn.mock.calls.find((c) => c[0] === `/control/${path}`);
      return call?.[1]?.method as string | undefined;
    },
  };
}

// flush microtasks so the postJSON/delJSON promise chain settles.
const flush = () => new Promise((r) => setTimeout(r, 0));

// ── distinct states: loading / error / empty / data ───────────────────────────
test("shows a loading state distinct from data", () => {
  const { getByTestId, queryByTestId } = renderBpManage(fakeStore({ loading: true }));
  expect(getByTestId("bpm-loading")).toBeInTheDocument();
  expect(queryByTestId("bpm-staged-empty")).not.toBeInTheDocument();
});

test("an action-error banner surfaces a mutation failure (distinct error state)", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response("nope", { status: 400 })));
  const store = fakeStore({
    loading: false,
    pending: pending(),
    staged: [staged({ name: "team-a/myapp", provenance: "upload" })],
    sources: [],
  });
  const { getByTestId, getByText, findByTestId } = renderBpManage(store);
  // open the confirm dialog, then confirm to fire the (failing) DELETE.
  await userEvent.click(getByTestId("bpm-del-staged-team-a/myapp"));
  await userEvent.click(getByText("Delete"));
  const banner = await findByTestId("bpm-action-error");
  expect(banner.textContent).toContain("nope");
});

test("the empty state renders when no blueprints are staged", () => {
  const store = fakeStore({ loading: false, pending: pending(), staged: [], sources: [] });
  const { getByTestId } = renderBpManage(store);
  const empty = getByTestId("bpm-staged-empty");
  expect(empty.textContent).toContain(
    "No custom or git-sourced blueprints staged",
  );
  expect(empty.textContent).toContain("bundled example");
  expect(empty.textContent).toContain("Paste YAML");
  expect(empty.textContent).toContain("git source");
  expect(empty.textContent).toContain("restart to apply");
});

test("the data state renders staged blueprints with provenance badges", () => {
  const store = fakeStore({
    loading: false,
    pending: pending(),
    staged: [
      staged({ name: "core/host", provenance: "builtin" }),
      staged({ name: "team-a/myapp", provenance: "upload" }),
      staged({ name: "team-b/remote", provenance: "git:my-org-blueprints" }),
    ],
    sources: [],
  });
  const { getByTestId, queryByTestId } = renderBpManage(store);
  expect(getByTestId("bpm-staged-core/host")).toBeInTheDocument();
  expect(getByTestId("bpm-staged-team-a/myapp")).toBeInTheDocument();
  expect(getByTestId("bpm-staged-team-b/remote")).toBeInTheDocument();
  // only the upload one is deletable; builtin + git are not.
  expect(queryByTestId("bpm-del-staged-team-a/myapp")).toBeInTheDocument();
  expect(queryByTestId("bpm-del-staged-core/host")).not.toBeInTheDocument();
  expect(queryByTestId("bpm-del-staged-team-b/remote")).not.toBeInTheDocument();
});

// ── pending banner ────────────────────────────────────────────────────────────
test("the pending-changes banner renders added/removed/changed when present", () => {
  const store = fakeStore({
    loading: false,
    pending: pending({ added: ["team-a/myapp"], changed: ["team-b/remote"], restart: true }),
    staged: [],
    sources: [],
  });
  const { getByTestId, queryByTestId } = renderBpManage(store);
  const banner = getByTestId("bpm-pending");
  expect(banner.textContent).toContain("2 changes pending — restart to apply");
  expect(banner.textContent).toContain("+ team-a/myapp (added)");
  expect(banner.textContent).toContain("~ team-b/remote (changed)");
	  expect(banner.textContent).toContain("docker compose restart synthkit");

  // absent when no pending changes.
  const clean = renderBpManage(
    fakeStore({ loading: false, pending: pending(), staged: [], sources: [] }),
  );
  expect(clean.queryByTestId("bpm-pending")).not.toBeInTheDocument();
});

// ── validate posts {yaml} and renders diagnostics + cardinality ───────────────
test("Validate posts {yaml} and renders the returned diagnostics + cardinality string", async () => {
  const vres: ValidationResult = {
    ok: true,
    name: "myapp",
    cardinality: 1234,
    estimated: false,
    diagnostics: ["WARN: cluster reused"],
  };
  const f = stubFetch(vres);
  const store = fakeStore({ loading: false, pending: pending(), staged: [], sources: [] });
  const { getByTestId } = renderBpManage(store);

  fireEvent.input(getByTestId("bpm-yaml"), { target: { value: "name: myapp\n" } });
  await userEvent.click(getByTestId("bpm-validate"));
  await flush();

  // posted exactly {yaml} (trimmed).
  expect(f.bodyFor("blueprints/validate")).toEqual({ yaml: "name: myapp" });

  const result = getByTestId("bpm-vresult");
  expect(result.textContent).toContain("✓ Valid blueprint: myapp");
  expect(result.textContent).toContain("~1234 series");
  expect(result.textContent).toContain("WARN: cluster reused");
});

test("Validate renders an estimated cardinality suffix and a failed verdict on ok:false", async () => {
  const vres: ValidationResult = {
    ok: false,
    name: "",
    cardinality: -1,
    estimated: true,
    diagnostics: ["ERROR: unknown construct kind 'frob'"],
  };
  stubFetch(vres);
  const store = fakeStore({ loading: false, pending: pending(), staged: [], sources: [] });
  const { getByTestId } = renderBpManage(store);

  fireEvent.input(getByTestId("bpm-yaml"), { target: { value: "bad: yaml" } });
  await userEvent.click(getByTestId("bpm-validate"));
  await flush();

  const result = getByTestId("bpm-vresult");
  expect(result.textContent).toContain("✕ Validation failed");
  expect(result.textContent).toContain("ERROR: unknown construct kind 'frob'");
  // cardinality -1 ⇒ no "~ series" string.
  expect(result.textContent).not.toContain("series");
});

test("editing YAML clears a prior validation result and requires a fresh preflight", async () => {
  const f = stubFetch({
    ok: true,
    name: "myapp",
    cardinality: 12,
    estimated: false,
    diagnostics: [],
  } satisfies ValidationResult);
  const store = fakeStore({ loading: false, pending: pending(), staged: [], sources: [] });
  const { getByTestId, queryByTestId } = renderBpManage(store);

  fireEvent.input(getByTestId("bpm-yaml"), { target: { value: "name: myapp" } });
  await userEvent.click(getByTestId("bpm-validate"));
  await flush();
  expect(getByTestId("bpm-vresult")).toBeInTheDocument();

  fireEvent.input(getByTestId("bpm-yaml"), { target: { value: "name: changed" } });
  expect(queryByTestId("bpm-vresult")).not.toBeInTheDocument();
  expect(f.pathCalled("blueprints/validate")).toBe(true);
});

test("Validate blocks an empty-YAML submit with an inline field error (no POST)", async () => {
  const f = stubFetch({});
  const store = fakeStore({ loading: false, pending: pending(), staged: [], sources: [] });
  const { getByTestId } = renderBpManage(store);
  await userEvent.click(getByTestId("bpm-validate"));
  await flush();
  expect(f.pathCalled("blueprints/validate")).toBe(false);
  expect(getByTestId("bpm-field-err").textContent).toContain("YAML is required");
});

// ── Save posts {namespace, name, yaml} and validates name client-side ─────────
test("Save posts {namespace,name,yaml} to blueprints/custom", async () => {
  const f = stubFetch({ status: "staged" });
  const store = fakeStore({ loading: false, pending: pending(), staged: [], sources: [] });
  const { getByTestId } = renderBpManage(store);

  fireEvent.input(getByTestId("bpm-ns"), { target: { value: "team-a" } });
  fireEvent.input(getByTestId("bpm-name"), { target: { value: "myapp" } });
  fireEvent.input(getByTestId("bpm-yaml"), { target: { value: "name: myapp" } });
  await userEvent.click(getByTestId("bpm-save"));
  await flush();

  expect(f.bodyFor("blueprints/custom")).toEqual({
    namespace: "team-a",
    name: "myapp",
    yaml: "name: myapp",
  });
  expect(store.refresh).toHaveBeenCalled();
});

test("Save rejects a name containing '/' or '__' client-side (no POST)", async () => {
  const f = stubFetch({});
  const store = fakeStore({ loading: false, pending: pending(), staged: [], sources: [] });
  const { getByTestId } = renderBpManage(store);
  fireEvent.input(getByTestId("bpm-yaml"), { target: { value: "name: x" } });
  fireEvent.input(getByTestId("bpm-name"), { target: { value: "bad/name" } });
  await userEvent.click(getByTestId("bpm-save"));
  await flush();
  expect(f.pathCalled("blueprints/custom")).toBe(false);
  expect(getByTestId("bpm-field-err").textContent).toContain('"/" or "__"');
});

// ── SECURITY: token value never rendered; only the env-var NAME ───────────────
test("the sources panel renders only the token env-var NAME — never a secret token value", () => {
  const store = fakeStore({
    loading: false,
    pending: pending(),
    staged: [],
    // a SourceView has NO secret token field by design; provide the env-var name only.
    sources: [source({ token_env_var: "MY_GITHUB_TOKEN" })],
  });
  const { getByTestId, container } = renderBpManage(store);

  // the env-var NAME is shown.
  expect(getByTestId("bpm-source-tokenenv-src-1").textContent).toContain(
    "token env var: MY_GITHUB_TOKEN",
  );

  // there is NO password/secret input anywhere on the page (add-form included).
  expect(container.querySelector('input[type="password"]')).toBeNull();
  // the token-env input is a plain text NAME field, not a value field.
  expect(getByTestId("bpm-src-tokenenv").getAttribute("type")).toBe("text");
});

test("Add source validates required lifecycle identity and then fetches it", async () => {
  const f = stubFetch({ status: "ok" });
  const store = fakeStore({ loading: false, pending: pending(), staged: [], sources: [] });
  const { getByTestId } = renderBpManage(store);

  fireEvent.input(getByTestId("bpm-src-id"), { target: { value: "my-org" } });
  fireEvent.input(getByTestId("bpm-src-name"), { target: { value: "My org blueprints" } });
  fireEvent.input(getByTestId("bpm-src-url"), { target: { value: "https://x/blueprints.git" } });
  fireEvent.input(getByTestId("bpm-src-ref"), { target: { value: "refs/heads/main" } });
  fireEvent.input(getByTestId("bpm-src-subpath"), { target: { value: "blueprints///" } });
  fireEvent.input(getByTestId("bpm-src-namespace"), { target: { value: "team-a" } });
  fireEvent.input(getByTestId("bpm-src-tokenenv"), { target: { value: "MY_GITHUB_TOKEN" } });
  await userEvent.click(getByTestId("bpm-src-add"));
  await flush();

  const body = f.bodyFor("blueprints/sources") as Record<string, unknown>;
  expect(body).toEqual({
    id: "my-org",
    name: "My org blueprints",
    url: "https://x/blueprints.git",
    ref: "refs/heads/main",
    subpath: "blueprints",
    namespace: "team-a",
    token_env_var: "MY_GITHUB_TOKEN",
  });
	  expect(f.pathCalled("blueprints/sources/fetch?id=my-org")).toBe(true);
  // never a raw-token key.
  expect(body).not.toHaveProperty("token");
});

test("the add-source form validates id, name, HTTPS URL, namespace, and ref before posting", async () => {
  const f = stubFetch({});
  const store = fakeStore({ loading: false, pending: pending(), staged: [], sources: [] });
  const { getByTestId } = renderBpManage(store);
  await userEvent.click(getByTestId("bpm-src-add"));
  await flush();
  expect(f.pathCalled("blueprints/sources")).toBe(false);
  expect(getByTestId("bpm-src-err").textContent).toContain("Source ID is required");
});

test("the sources panel separates fetched, loaded, pending, and skipped state", () => {
  const store = fakeStore({ loading: false, pending: pending({ restart: true }), staged: [], sources: [source()] });
  const { getByTestId } = renderBpManage(store);
  const row = getByTestId("bpm-source-src-1");
  expect(row.textContent).toContain("fetched sha: deadbeef");
  expect(row.textContent).toContain("2 files fetched");
  expect(row.textContent).toContain("effective: team-a/checkout, team-a/payments");
  expect(row.textContent).toContain("loaded sha: cafebabe");
  expect(row.textContent).toContain("restart pending");
  expect(row.textContent).toContain("loaded: team-a/checkout");
  expect(row.textContent).toContain("skipped: broken.yaml: unknown construct");
});

test("the sources panel safely ignores null lifecycle lists", () => {
  const store = fakeStore({
    loading: false,
    pending: pending(),
    staged: [],
    sources: [source({ effective_names: null, loaded_names: null, skipped: null })],
  });
  const { getByTestId } = renderBpManage(store);
  const row = getByTestId("bpm-source-src-1");
  expect(row.textContent).not.toContain("effective:");
  expect(row.textContent).not.toContain("loaded:");
  expect(row.textContent).not.toContain("skipped:");
});

// ── delete actions are ConfirmButton-gated and hit the right DELETE paths ──────
test("a staged-blueprint delete is ConfirmButton-gated and DELETEs blueprints/custom?name=", async () => {
  const f = stubFetch({ status: "removed" });
  const store = fakeStore({
    loading: false,
    pending: pending(),
    staged: [staged({ name: "team-a/myapp", provenance: "upload" })],
    sources: [],
  });
  const { getByTestId, getByText } = renderBpManage(store);

  // open confirm, CANCEL — nothing deleted.
  await userEvent.click(getByTestId("bpm-del-staged-team-a/myapp"));
  await userEvent.click(getByText("Cancel"));
  await flush();
  expect(f.pathCalled("blueprints/custom?name=team-a%2Fmyapp")).toBe(false);

  // open again, CONFIRM — DELETE fires with the URL-encoded name.
  await userEvent.click(getByTestId("bpm-del-staged-team-a/myapp"));
  await userEvent.click(getByText("Delete"));
  await flush();
  expect(f.pathCalled("blueprints/custom?name=team-a%2Fmyapp")).toBe(true);
  expect(f.methodFor("blueprints/custom?name=team-a%2Fmyapp")).toBe("DELETE");
  expect(store.refresh).toHaveBeenCalled();
});

test("a source delete is ConfirmButton-gated and DELETEs blueprints/sources?id=", async () => {
  const f = stubFetch({ status: "removed" });
  const store = fakeStore({
    loading: false,
    pending: pending(),
    staged: [],
    sources: [source({ id: "src-1" })],
  });
  const { getByTestId, getByText } = renderBpManage(store);

  // open confirm, CANCEL — nothing deleted.
  await userEvent.click(getByTestId("bpm-source-del-src-1"));
  await userEvent.click(getByText("Cancel"));
  await flush();
  expect(f.pathCalled("blueprints/sources?id=src-1")).toBe(false);

  // open again, CONFIRM — DELETE fires.
  await userEvent.click(getByTestId("bpm-source-del-src-1"));
  await userEvent.click(getByText("Delete"));
  await flush();
  expect(f.pathCalled("blueprints/sources?id=src-1")).toBe(true);
  expect(f.methodFor("blueprints/sources?id=src-1")).toBe("DELETE");
});

test("a source Fetch-now POSTs blueprints/sources/fetch?id=", async () => {
  const f = stubFetch({ status: "fetched" });
  const store = fakeStore({
    loading: false,
    pending: pending(),
    staged: [],
    sources: [source({ id: "src-1" })],
  });
  const { getByTestId } = renderBpManage(store);
  await userEvent.click(getByTestId("bpm-source-fetch-src-1"));
  await flush();
  expect(f.pathCalled("blueprints/sources/fetch?id=src-1")).toBe(true);
  expect(f.methodFor("blueprints/sources/fetch?id=src-1")).toBe("POST");
  expect(store.refresh).toHaveBeenCalled();
});
