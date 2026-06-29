import { createMemo, createSignal, For, Show, type JSX } from "solid-js";
import { useStore } from "../store/store";
import type { ConfigGroup, ConfigField } from "../api/types";

// Ports internal/control/ui.html renderConfig: grouped redacted-config table with
// secret chip (● set / ○ not set), in-page filter, and click-sortable key/value columns.
// READ-ONLY — no mutations. States: loading → error → empty → data (grouped table).

type SortKey = "key" | "value";
type SortDir = "asc" | "desc";

function sortFields(fields: ConfigField[], key: SortKey, dir: SortDir): ConfigField[] {
  return [...fields].sort((a, b) => {
    const av = key === "key" ? a.key : (a.secret ? (a.configured ? "● set" : "○ not set") : a.value);
    const bv = key === "key" ? b.key : (b.secret ? (b.configured ? "● set" : "○ not set") : b.value);
    const cmp = av.localeCompare(bv);
    return dir === "asc" ? cmp : -cmp;
  });
}

function fieldMatchesFilter(f: ConfigField, q: string): boolean {
  if (!q) return true;
  const lq = q.toLowerCase();
  if (f.key.toLowerCase().includes(lq)) return true;
  if (!f.secret && f.value.toLowerCase().includes(lq)) return true;
  return false;
}

export function Config(): JSX.Element {
  const store = useStore();
  const s = () => store.state;

  const [filter, setFilter] = createSignal("");
  const [sortKey, setSortKey] = createSignal<SortKey>("key");
  const [sortDir, setSortDir] = createSignal<SortDir>("asc");

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

  // Visible groups with their fields filtered + sorted. Groups with zero visible fields
  // are preserved but show an "(no matching fields)" row so the heading stays discoverable.
  const visibleGroups = createMemo((): { group: ConfigGroup; fields: ConfigField[] }[] => {
    const cfg = s().config;
    if (!cfg) return [];
    const q = filter().trim();
    return (cfg.groups ?? []).map((g) => ({
      group: g,
      fields: sortFields(
        (g.fields ?? []).filter((f) => fieldMatchesFilter(f, q)),
        sortKey(),
        sortDir(),
      ),
    }));
  });

  return (
    <section>
      <style>{VIEW_CSS}</style>
      <div class="pane-head">
        <h1>Config</h1>
        <span class="sub">redacted runtime configuration</span>
      </div>
      <p class="pane-lead">
        All resolved configuration values. Secret fields show set/unset status only — values are
        never transmitted.
      </p>

      <Show
        when={!s().loading}
        fallback={
          <div class="cfg-loading" data-testid="config-loading">
            <span class="cfg-spinner" />
            Loading config…
          </div>
        }
      >
        <Show
          when={!s().errors["config"]}
          fallback={
            <div class="cfg-fetch-err" data-testid="config-error">
              Failed to load config: {s().errors["config"]}
            </div>
          }
        >
          <Show
            when={s().config}
            fallback={
              <div class="empty" data-testid="config-empty">
                Config not available.
              </div>
            }
          >
            {/* filter bar */}
            <div class="cfg-filterbar">
              <input
                class="cfg-filter"
                type="text"
                placeholder="Filter by key or value…"
                value={filter()}
                onInput={(e) => setFilter(e.currentTarget.value)}
                data-testid="config-filter"
              />
              <Show when={filter()}>
                <button class="cfg-filter-clear" onClick={() => setFilter("")}>✕</button>
              </Show>
            </div>

            <For each={visibleGroups()}>
              {({ group, fields }) => (
                <section class="sec">
                  <div class="sec-label">
                    {group.title}
                    <span class="meta">{fields.length} fields</span>
                  </div>
                  <div class="panel">
                    <Show
                      when={fields.length > 0}
                      fallback={
                        <div class="cfg-no-match">
                          {filter() ? "No matching fields." : "(no fields)"}
                        </div>
                      }
                    >
                      <table class="cfgtable">
                        <thead>
                          <tr>
                            <th
                              class={`cfg-th sortable${sortKey() === "key" ? " active" : ""}`}
                              onClick={() => toggleSort("key")}
                            >
                              key{sortIcon("key")}
                            </th>
                            <th
                              class={`cfg-th sortable${sortKey() === "value" ? " active" : ""}`}
                              onClick={() => toggleSort("value")}
                            >
                              value{sortIcon("value")}
                            </th>
                          </tr>
                        </thead>
                        <tbody>
                          <For each={fields}>
                            {(f) => (
                              <tr class="cfg-row" data-testid={`cfg-row-${f.key}`}>
                                <td class="cfg-key">{f.key}</td>
                                <td class="cfg-val">
                                  <Show
                                    when={f.secret}
                                    fallback={
                                      <Show
                                        when={f.value}
                                        fallback={<span class="empty-val">—</span>}
                                      >
                                        {f.value}
                                      </Show>
                                    }
                                  >
                                    <span
                                      class={`secret-chip ${f.configured ? "set" : "unset"}`}
                                      data-testid={`secret-chip-${f.key}`}
                                    >
                                      {f.configured ? "● set" : "○ not set"}
                                    </span>
                                  </Show>
                                </td>
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
      </Show>
    </section>
  );
}

const VIEW_CSS = `
.pane-lead { font-size:13px; color:var(--dim); margin:0 0 20px; line-height:1.5; }

.cfg-loading { display:flex; align-items:center; gap:10px; color:var(--dim); font-size:13px;
  padding:18px; border:1px dashed var(--bd2); border-radius:12px; background:var(--panel2); }
.cfg-spinner { width:14px; height:14px; border-radius:50%; border:2px solid var(--bd2);
  border-top-color:var(--acc); animation:cfg-spin .7s linear infinite; flex:none; }
@keyframes cfg-spin { to { transform:rotate(360deg); } }

.cfg-fetch-err { font-size:13px; font-weight:600; color:var(--err);
  background:var(--crit); border:1px solid var(--critbd); border-radius:12px;
  padding:14px 18px; text-align:center; }

.cfg-filterbar { display:flex; align-items:center; gap:6px; margin-bottom:18px; }
.cfg-filter { flex:1; font:13.5px var(--mono); padding:7px 10px;
  border:1px solid var(--bd); border-radius:8px;
  background:var(--panel2); color:var(--tx); outline:none;
  transition:border-color .12s; }
.cfg-filter:focus { border-color:var(--acc); }
.cfg-filter-clear { border:none; background:none; color:var(--dim); cursor:pointer;
  font-size:13px; padding:4px 6px; border-radius:6px; transition:color .12s; }
.cfg-filter-clear:hover { color:var(--tx); }

.sec { margin-bottom:26px; }
.sec-label { display:flex; align-items:baseline; gap:8px; font:700 11px system-ui; letter-spacing:.8px;
  text-transform:uppercase; color:var(--dim); margin:0 0 10px; }
.sec-label .meta { font-weight:500; text-transform:none; letter-spacing:0; font-size:11.5px; }

.panel { background:var(--panel2); border:1px solid var(--bd); border-radius:10px; overflow:hidden; }

.empty { color:var(--dim); font-size:13px; padding:18px; text-align:center;
  border:1px dashed var(--bd2); border-radius:12px; background:var(--panel2); }
.cfg-no-match { color:var(--dim); font-size:13px; padding:14px 16px; }

.cfgtable { width:100%; border-collapse:collapse; font-size:13px; }
.cfg-th { text-align:left; font:600 11px system-ui; letter-spacing:.5px; text-transform:uppercase;
  color:var(--dim); padding:8px 14px; background:var(--panel2);
  border-bottom:1px solid var(--bd); user-select:none; }
.cfg-th.sortable { cursor:pointer; transition:color .12s; }
.cfg-th.sortable:hover { color:var(--tx); }
.cfg-th.active { color:var(--acc); }
.cfg-row:not(:last-child) td { border-bottom:1px solid var(--bd2); }
.cfg-row:hover td { background:var(--hover); }
.cfg-key { font:13px var(--mono); color:var(--tx); padding:9px 14px; font-weight:600;
  white-space:nowrap; vertical-align:middle; }
.cfg-val { font:13px var(--mono); color:var(--dim); padding:9px 14px; word-break:break-all;
  vertical-align:middle; }

.empty-val { color:var(--bd2); font-style:italic; }
.secret-chip { display:inline-flex; align-items:center; gap:4px; font:600 11.5px system-ui;
  padding:3px 8px; border-radius:20px; letter-spacing:.02em; }
.secret-chip.set   { background:var(--okbg); color:var(--ok); border:1px solid var(--okbd); }
.secret-chip.unset { background:var(--panel2); color:var(--dim); border:1px solid var(--bd); }
`;
