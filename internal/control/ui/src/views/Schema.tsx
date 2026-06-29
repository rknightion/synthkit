import { createMemo, createSignal, For, onMount, Show, type JSX } from "solid-js";
import { getJSON, ApiError } from "../api/client";
import type { BpSchemaDoc, BpSchemaSection, BpSchemaField } from "../api/types";

// Ports internal/control/ui.html renderBpSchema: per-section table of the authoring schema
// (key / type / opt / description), with nested fields walked recursively into dotted keys
// (e.g. `workloads[].targets[].port`). Sortable columns + in-page filter.
// READ-ONLY — no mutations. States: loading (spinner while on-mount fetch is in flight);
// error (fetch failed or 404); data (per-section tables). The schema is fetched ONCE on
// mount and cached in a local signal — never refetched on poll cycles.

// ── flattened row (one entry per table row, prefix-dotted) ──────────────────
interface SchemaRow {
  key: string;
  type: string;
  opt: boolean;
  doc: string;
}

// Recursively walk fields into flat rows, building dotted prefix paths.
// Matches the renderBpSchema walk exactly: key = prefix + f.key + (f.repeated ? "[]" : "")
function walkFields(fields: BpSchemaField[], prefix: string): SchemaRow[] {
  const rows: SchemaRow[] = [];
  for (const f of fields ?? []) {
    const key = prefix + f.key + (f.repeated ? "[]" : "");
    let type = f.type ?? "";
    if (f.enum && f.enum.length > 0) type += " ∈ {" + f.enum.join(", ") + "}";
    rows.push({ key, type, opt: !!f.optional, doc: f.doc ?? "" });
    if (f.fields && f.fields.length > 0) {
      rows.push(...walkFields(f.fields, key + "."));
    }
  }
  return rows;
}

type SortKey = "key" | "type" | "doc";
type SortDir = "asc" | "desc";

function sortRows(rows: SchemaRow[], key: SortKey, dir: SortDir): SchemaRow[] {
  return [...rows].sort((a, b) => {
    const cmp = String(a[key]).localeCompare(String(b[key]));
    return dir === "asc" ? cmp : -cmp;
  });
}

function rowMatches(r: SchemaRow, q: string): boolean {
  if (!q) return true;
  const lq = q.toLowerCase();
  return (
    r.key.toLowerCase().includes(lq) ||
    r.type.toLowerCase().includes(lq) ||
    r.doc.toLowerCase().includes(lq)
  );
}

export function Schema(): JSX.Element {
  // Local-only signals — not the polled store; schema is fetched once and cached.
  const [schema, setSchema] = createSignal<BpSchemaDoc | null>(null);
  const [fetchErr, setFetchErr] = createSignal<string | null>(null);
  const [loading, setLoading] = createSignal(true);

  const [filter, setFilter] = createSignal("");
  const [sortKey, setSortKey] = createSignal<SortKey>("key");
  const [sortDir, setSortDir] = createSignal<SortDir>("asc");

  onMount(() => {
    void getJSON<BpSchemaDoc>("blueprint-schema")
      .then((d) => { setSchema(d ?? { sections: [] }); })
      .catch((e: unknown) => {
        const msg =
          e instanceof ApiError
            ? (e.status === 404 ? "Blueprint schema not configured." : e.message)
            : String(e);
        setFetchErr(msg);
      })
      .finally(() => setLoading(false));
  });

  function toggleSort(k: SortKey) {
    if (sortKey() === k) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(k);
      setSortDir("asc");
    }
  }

  const sortIcon = (k: SortKey) => {
    if (sortKey() !== k) return " ↕";
    return sortDir() === "asc" ? " ↑" : " ↓";
  };

  // Per-section visible rows (filtered + sorted).
  const visibleSections = createMemo((): { section: BpSchemaSection; rows: SchemaRow[] }[] => {
    const doc = schema();
    if (!doc) return [];
    const q = filter().trim();
    return (doc.sections ?? []).map((sec) => {
      const all = walkFields(sec.fields ?? [], "");
      const filtered = q ? all.filter((r) => rowMatches(r, q)) : all;
      return { section: sec, rows: sortRows(filtered, sortKey(), sortDir()) };
    });
  });

  return (
    <section>
      <style>{VIEW_CSS}</style>
      <div class="pane-head">
        <h1>Blueprint schema</h1>
        <span class="sub">every key a blueprint may contain — derived live from the Go types</span>
      </div>
      <p class="pane-lead">
        The complete authoring surface: the blueprint document plus every construct/workload config
        block. Auto-generated and strict-decoded — any key not listed here fails to load.
      </p>

      {/* ── loading ────────────────────────────────────────────────────────── */}
      <Show
        when={!loading()}
        fallback={
          <div class="sc-loading" data-testid="schema-loading">
            <span class="sc-spinner" />
            Loading schema…
          </div>
        }
      >
        {/* ── error ──────────────────────────────────────────────────────────── */}
        <Show
          when={!fetchErr()}
          fallback={
            <div class="sc-fetch-err" data-testid="schema-error">
              {fetchErr()}
            </div>
          }
        >
          {/* ── data ─────────────────────────────────────────────────────────── */}
          {/* filter bar */}
          <div class="sc-filterbar">
              <input
                class="sc-filter"
                type="text"
                placeholder="Filter by key, type, or description…"
                value={filter()}
                onInput={(e) => setFilter(e.currentTarget.value)}
                data-testid="schema-filter"
              />
              <Show when={filter()}>
                <button class="sc-filter-clear" onClick={() => setFilter("")}>✕</button>
              </Show>
            </div>

            <For each={visibleSections()}>
              {({ section: sec, rows }) => (
                <section class="sec">
                  <div class="sec-label">
                    {sec.title}
                    <Show when={sec.path}>
                      <span class="sec-path">{sec.path}</span>
                    </Show>
                    <span class="meta">{(sec.fields ?? []).length} fields</span>
                  </div>
                  <div class="panel">
                    <Show when={sec.doc}>
                      <p class="sec-doc">{sec.doc}</p>
                    </Show>
                    <Show
                      when={rows.length > 0}
                      fallback={
                        <div class="sc-no-match">
                          {filter() ? "No matching fields." : "(no configurable fields)"}
                        </div>
                      }
                    >
                      <table class="sctable">
                        <thead>
                          <tr>
                            <th
                              class={`sc-th sortable${sortKey() === "key" ? " active" : ""}`}
                              onClick={() => toggleSort("key")}
                              data-testid="col-key"
                            >
                              key{sortIcon("key")}
                            </th>
                            <th
                              class={`sc-th sortable${sortKey() === "type" ? " active" : ""}`}
                              onClick={() => toggleSort("type")}
                            >
                              type{sortIcon("type")}
                            </th>
                            <th class="sc-th">opt</th>
                            <th
                              class={`sc-th sortable${sortKey() === "doc" ? " active" : ""}`}
                              onClick={() => toggleSort("doc")}
                            >
                              description{sortIcon("doc")}
                            </th>
                          </tr>
                        </thead>
                        <tbody>
                          <For each={rows}>
                            {(r) => (
                              <tr class="sc-row" data-testid={`schema-row-${r.key}`}>
                                <td class="sc-key">
                                  <code>{r.key}</code>
                                </td>
                                <td class="sc-type">{r.type}</td>
                                <td class="sc-opt">
                                  <Show when={r.opt}>
                                    <span class="opt-chip">opt</span>
                                  </Show>
                                </td>
                                <td class="sc-desc">{r.doc}</td>
                              </tr>
                            )}
                          </For>
                        </tbody>
                      </table>
                    </Show>
                  </div>
                </section>
              )}
            </For>
        </Show>
      </Show>
    </section>
  );
}

const VIEW_CSS = `
.pane-lead { font-size:13px; color:var(--dim); margin:0 0 20px; line-height:1.5; }

.sc-loading { display:flex; align-items:center; gap:10px; color:var(--dim); font-size:13px;
  padding:18px; border:1px dashed var(--bd2); border-radius:12px; background:var(--panel2); }
.sc-spinner { width:14px; height:14px; border-radius:50%; border:2px solid var(--bd2);
  border-top-color:var(--acc); animation:sc-spin .7s linear infinite; flex:none; }
@keyframes sc-spin { to { transform:rotate(360deg); } }

.sc-fetch-err { font-size:13px; font-weight:600; color:var(--err);
  background:var(--crit); border:1px solid var(--critbd); border-radius:12px;
  padding:14px 18px; text-align:center; }

.sc-filterbar { display:flex; align-items:center; gap:6px; margin-bottom:18px; }
.sc-filter { flex:1; font:13.5px var(--mono); padding:7px 10px;
  border:1px solid var(--bd); border-radius:8px;
  background:var(--panel2); color:var(--tx); outline:none;
  transition:border-color .12s; }
.sc-filter:focus { border-color:var(--acc); }
.sc-filter-clear { border:none; background:none; color:var(--dim); cursor:pointer;
  font-size:13px; padding:4px 6px; border-radius:6px; transition:color .12s; }
.sc-filter-clear:hover { color:var(--tx); }

.sec { margin-bottom:26px; }
.sec-label { display:flex; align-items:baseline; gap:8px; font:700 11px system-ui; letter-spacing:.8px;
  text-transform:uppercase; color:var(--dim); margin:0 0 10px; flex-wrap:wrap; }
.sec-label .meta { font-weight:500; text-transform:none; letter-spacing:0; font-size:11.5px; }
.sec-path { font:500 10px var(--mono); text-transform:none; letter-spacing:0;
  color:var(--acc); background:color-mix(in srgb, var(--acc) 10%, transparent);
  padding:1px 6px; border-radius:4px; }

.panel { background:var(--panel2); border:1px solid var(--bd); border-radius:10px; overflow:hidden; }

.empty { color:var(--dim); font-size:13px; padding:18px; text-align:center;
  border:1px dashed var(--bd2); border-radius:12px; background:var(--panel2); }
.sc-no-match { color:var(--dim); font-size:13px; padding:14px 16px; }

.sec-doc { font-size:12.5px; color:var(--dim); padding:10px 14px 6px;
  margin:0; border-bottom:1px solid var(--bd2); line-height:1.5; }

.sctable { width:100%; border-collapse:collapse; font-size:13px; }
.sc-th { text-align:left; font:600 11px system-ui; letter-spacing:.5px; text-transform:uppercase;
  color:var(--dim); padding:8px 14px; background:var(--panel2);
  border-bottom:1px solid var(--bd); user-select:none; white-space:nowrap; }
.sc-th.sortable { cursor:pointer; transition:color .12s; }
.sc-th.sortable:hover { color:var(--tx); }
.sc-th.active { color:var(--acc); }
.sc-row:not(:last-child) td { border-bottom:1px solid var(--bd2); }
.sc-row:hover td { background:var(--hover); }
.sc-key { padding:8px 14px; vertical-align:top; white-space:nowrap; }
.sc-key code { font:13px var(--mono); color:var(--acc); background:color-mix(in srgb, var(--acc) 8%, transparent);
  padding:1px 5px; border-radius:4px; }
.sc-type { font:12.5px var(--mono); color:var(--dim); padding:8px 14px; vertical-align:top;
  white-space:nowrap; }
.sc-opt { padding:8px 10px; vertical-align:top; text-align:center; }
.opt-chip { display:inline-flex; font:600 10px system-ui; letter-spacing:.02em;
  color:var(--warn); background:var(--warnbg); border:1px solid var(--warnbd);
  padding:1px 6px; border-radius:10px; }
.sc-desc { font-size:12.5px; color:var(--tx); padding:8px 14px; vertical-align:top;
  line-height:1.45; }
`;
