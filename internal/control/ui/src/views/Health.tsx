import { createMemo, createSignal, For, Show, type JSX } from "solid-js";
import { useStore } from "../store/store";
// fmtBytes/fmtDurMs are aliased to the legacy local names so call sites stay verbatim.
import { fmtNum, fmtBytes as fmtHeap, fmtDurMs as fmtMs } from "../utils/fmt";
import { selfObsDashboardURL } from "../utils/config";
import type { ConstructHealth, BlueprintHealth } from "../api/types";

// Ports internal/control/ui.html renderHealth: process stats, per-blueprint cycle table,
// per-construct tick table (with p95 NUMERIC sort fix + in-page filter).
// READ-ONLY — no mutations. States: loading → error → empty (no constructs yet) → data.

// Inline SVG sparkline — currentColor stroke, inherits row colour. Flat/empty ⇒ null.
function sparkline(values: number[] | undefined, w = 52, h = 14): string | null {
  const v = (values ?? []).filter((x) => typeof x === "number");
  if (v.length < 2) return null;
  const max = Math.max(...v),
    min = Math.min(...v),
    span = max - min || 1;
  const step = w / (v.length - 1);
  const pts = v
    .map((x, i) => {
      const px = (i * step).toFixed(1);
      const py = (h - 1 - ((x - min) / span) * (h - 2)).toFixed(1);
      return px + "," + py;
    })
    .join(" ");
  return (
    `<svg width="${w}" height="${h}" viewBox="0 0 ${w} ${h}" fill="none" ` +
    `stroke="currentColor" stroke-width="1.2" stroke-linejoin="round" stroke-linecap="round" opacity="0.85">` +
    `<polyline points="${pts}"/></svg>`
  );
}

function sparkTooltip(values: number[] | undefined): string {
  const v = (values ?? []).filter((x) => typeof x === "number");
  if (v.length === 0) return "";
  const latest = Math.round(v[v.length - 1]);
  const min = Math.round(Math.min(...v));
  const max = Math.round(Math.max(...v));
  return `latest: ${latest} · min: ${min} · max: ${max}`;
}

type SortKey = "blueprint" | "kind" | "name" | "ticks" | "errors" | "last_outcome" | "p95_dur_ms";
type SortDir = "asc" | "desc";

function sortConstructs(rows: ConstructHealth[], key: SortKey, dir: SortDir): ConstructHealth[] {
  return [...rows].sort((a, b) => {
    let cmp: number;
    if (key === "p95_dur_ms" || key === "ticks" || key === "errors") {
      // Numeric sort — p95 fix: "1ms" < "20ms" < "100ms" requires numeric key
      cmp = (Number(a[key]) || 0) - (Number(b[key]) || 0);
    } else {
      cmp = String(a[key] ?? "").localeCompare(String(b[key] ?? ""));
    }
    return dir === "asc" ? cmp : -cmp;
  });
}

function constructMatchesFilter(c: ConstructHealth, q: string): boolean {
  if (!q) return true;
  const lq = q.toLowerCase();
  return (
    c.blueprint.toLowerCase().includes(lq) ||
    c.kind.toLowerCase().includes(lq) ||
    c.name.toLowerCase().includes(lq)
  );
}

export function Health(): JSX.Element {
  const store = useStore();
  const s = () => store.state;

  const [filter, setFilter] = createSignal("");
  const [sortKey, setSortKey] = createSignal<SortKey>("blueprint");
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

  const health = () => s().health;
  const process_ = () => health()?.process;
  const blueprints = (): BlueprintHealth[] => health()?.blueprints ?? [];
  const constructs = (): ConstructHealth[] => health()?.constructs ?? [];

  const visibleConstructs = createMemo((): ConstructHealth[] => {
    const q = filter().trim();
    const rows = constructs().filter((c) => constructMatchesFilter(c, q));
    return sortConstructs(rows, sortKey(), sortDir());
  });

  const hasConstructs = () => constructs().length > 0;

  return (
    <section>
      <style>{VIEW_CSS}</style>
      <div class="pane-head">
        <h1>Health</h1>
        <span class="sub">tick + cycle + process health</span>
      </div>

      <Show when={selfObsDashboardURL(s().config)}>
        <a
          class="selfobs-link"
          data-testid="health-selfobs-link"
          href={selfObsDashboardURL(s().config)}
          target="_blank"
          rel="noopener"
        >
          ↗ Open generator self-obs in Grafana
        </a>
      </Show>

      <Show
        when={!s().loading}
        fallback={
          <div class="h-loading" data-testid="health-loading">
            <span class="h-spinner" />
            Loading health…
          </div>
        }
      >
        <Show
          when={!s().errors["health"]}
          fallback={
            <div class="h-fetch-err" data-testid="health-error">
              Failed to load health: {s().errors["health"]}
            </div>
          }
        >
          {/* ── process stats ─────────────────────────────────────────────── */}
          <Show when={process_()}>
            {(proc) => (
              <section class="sec">
                <div class="sec-label">Process</div>
                <div class="proc-grid" data-testid="health-process-stats">
                  <div class="proc-stat">
                    <div class="pval">{fmtNum(proc().goroutines)}</div>
                    <div class="plbl">goroutines</div>
                  </div>
                  <div class="proc-stat">
                    <div class="pval">{fmtHeap(proc().heap_bytes)}</div>
                    <div class="plbl">heap</div>
                  </div>
                  <div class="proc-stat">
                    <div class="pval">{fmtNum(proc().gc_count)}</div>
                    <div class="plbl">GC cycles</div>
                  </div>
                </div>
              </section>
            )}
          </Show>

          {/* ── per-blueprint cycle table ─────────────────────────────────── */}
          <Show when={blueprints().length > 0}>
            <section class="sec">
              <div class="sec-label">
                Blueprint cycles
                <span class="meta">
                  {blueprints().length} blueprint{blueprints().length === 1 ? "" : "s"}
                </span>
              </div>
              <div class="panel">
                <table class="htable">
                  <thead>
                    <tr>
                      <th class="h-th">blueprint</th>
                      <th class="h-th">cycles</th>
                      <th class="h-th">dropped</th>
                      <th class="h-th">last cycle</th>
                      <th class="h-th">trend</th>
                    </tr>
                  </thead>
                  <tbody>
                    <For each={blueprints()}>
                      {(b) => {
                        const isDropped = () => (b.dropped_ticks ?? 0) > 0;
                        const spark = () => sparkline(b.cycle_spark);
                        return (
                          <tr
                            class={`h-row${isDropped() ? " h-row-err" : ""}`}
                            data-testid={`bp-row-${b.blueprint}`}
                          >
                            <td class="h-mono">{b.blueprint}</td>
                            <td>{String(b.cycles ?? 0)}</td>
                            <td class={isDropped() ? "h-err-val" : ""}>
                              {String(b.dropped_ticks ?? 0)}
                            </td>
                            <td class="h-mono">{fmtMs(b.last_cycle_ms)}</td>
                            <td class="spark-cell">
                              <Show when={spark()}>
                                {/* eslint-disable-next-line solid/no-innerhtml */}
                                <span class="h-spark" title={sparkTooltip(b.cycle_spark)} innerHTML={spark()!} />
                              </Show>
                            </td>
                          </tr>
                        );
                      }}
                    </For>
                  </tbody>
                </table>
              </div>
            </section>
          </Show>

          {/* ── per-construct tick table ──────────────────────────────────── */}
          <section class="sec">
            <div class="sec-label">
              Construct ticks
              <span class="meta">
                {constructs().length} construct{constructs().length === 1 ? "" : "s"}
              </span>
            </div>

            <Show
              when={hasConstructs()}
              fallback={
                <div class="empty" data-testid="health-empty">
                  No construct tick data yet — wait for the first tick cycle.
                </div>
              }
            >
              {/* filter bar */}
              <div class="h-filterbar">
                <input
                  class="h-filter"
                  type="text"
                  placeholder="Filter by blueprint / kind / name…"
                  value={filter()}
                  onInput={(e) => setFilter(e.currentTarget.value)}
                  data-testid="health-filter"
                />
                <Show when={filter()}>
                  <button class="h-filter-clear" onClick={() => setFilter("")}>✕</button>
                </Show>
              </div>

              <div class="panel">
                <table class="htable">
                  <thead>
                    <tr>
                      <th
                        class={`h-th sortable${sortKey() === "blueprint" ? " active" : ""}`}
                        onClick={() => toggleSort("blueprint")}
                      >
                        blueprint{sortIcon("blueprint")}
                      </th>
                      <th
                        class={`h-th sortable${sortKey() === "kind" ? " active" : ""}`}
                        onClick={() => toggleSort("kind")}
                      >
                        kind{sortIcon("kind")}
                      </th>
                      <th
                        class={`h-th sortable${sortKey() === "name" ? " active" : ""}`}
                        onClick={() => toggleSort("name")}
                      >
                        name{sortIcon("name")}
                      </th>
                      <th
                        class={`h-th sortable${sortKey() === "ticks" ? " active" : ""}`}
                        onClick={() => toggleSort("ticks")}
                      >
                        ticks{sortIcon("ticks")}
                      </th>
                      <th
                        class={`h-th sortable${sortKey() === "errors" ? " active" : ""}`}
                        onClick={() => toggleSort("errors")}
                      >
                        errors{sortIcon("errors")}
                      </th>
                      <th
                        class={`h-th sortable${sortKey() === "last_outcome" ? " active" : ""}`}
                        onClick={() => toggleSort("last_outcome")}
                      >
                        last outcome{sortIcon("last_outcome")}
                      </th>
                      <th
                        class={`h-th sortable${sortKey() === "p95_dur_ms" ? " active" : ""}`}
                        onClick={() => toggleSort("p95_dur_ms")}
                        data-testid="col-p95"
                      >
                        p95{sortIcon("p95_dur_ms")}
                      </th>
                      <th class="h-th">trend</th>
                    </tr>
                  </thead>
                  <tbody>
                    <For each={visibleConstructs()}>
                      {(c) => {
                        const spark = () => sparkline(c.spark);
                        const outcomeClass = () =>
                          c.last_outcome === "ok"
                            ? "dot ok"
                            : c.last_outcome === "error"
                              ? "dot err"
                              : "dot muted";
                        return (
                          <tr
                            class="h-row"
                            data-testid={`cst-row-${c.blueprint}-${c.kind}-${c.name}`}
                          >
                            <td class="h-mono">{c.blueprint}</td>
                            <td class="h-mono">{c.kind}</td>
                            <td class="h-mono">{c.name}</td>
                            <td>{String(c.ticks ?? 0)}</td>
                            <td class={c.errors > 0 ? "h-err-val" : ""}>{String(c.errors ?? 0)}</td>
                            <td class="h-outcome-cell">
                              <span class={outcomeClass()} title={c.last_error || c.last_outcome || ""} />
                              <Show when={c.last_outcome === "error" && c.last_error}>
                                {" "}
                                <span class="h-err-msg" title={c.last_error}>
                                  {c.last_error.length > 40
                                    ? c.last_error.slice(0, 40) + "…"
                                    : c.last_error}
                                </span>
                              </Show>
                            </td>
                            {/* p95: data-sort carries the raw number for correct numeric sort order */}
                            <td
                              class="h-mono"
                              data-sort={c.p95_dur_ms ?? 0}
                              data-testid={`p95-cell-${c.name}`}
                            >
                              {fmtMs(c.p95_dur_ms)}
                            </td>
                            <td class="spark-cell">
                              <Show when={spark()}>
                                {/* eslint-disable-next-line solid/no-innerhtml */}
                                <span class="h-spark" title={sparkTooltip(c.spark)} innerHTML={spark()!} />
                              </Show>
                            </td>
                          </tr>
                        );
                      }}
                    </For>
                  </tbody>
                </table>
              </div>
            </Show>
          </section>
        </Show>
      </Show>
    </section>
  );
}

const VIEW_CSS = `
.selfobs-link { display:inline-flex; align-items:center; gap:6px; font:600 12px system-ui;
  color:var(--acc); text-decoration:none; border:1px solid var(--accbd); background:var(--acc2);
  border-radius:8px; padding:6px 13px; margin:0 0 20px; transition:filter .12s; }
.selfobs-link:hover { filter:brightness(.96); text-decoration:underline; }

.h-loading { display:flex; align-items:center; gap:10px; color:var(--dim); font-size:13px;
  padding:18px; border:1px dashed var(--bd2); border-radius:12px; background:var(--panel2); }
.h-spinner { width:14px; height:14px; border-radius:50%; border:2px solid var(--bd2);
  border-top-color:var(--acc); animation:h-spin .7s linear infinite; flex:none; }
@keyframes h-spin { to { transform:rotate(360deg); } }

.h-fetch-err { font-size:13px; font-weight:600; color:var(--err);
  background:var(--crit); border:1px solid var(--critbd); border-radius:12px;
  padding:14px 18px; text-align:center; }

.sec { margin-bottom:26px; }
.sec-label { display:flex; align-items:baseline; gap:8px; font:700 11px system-ui; letter-spacing:.8px;
  text-transform:uppercase; color:var(--dim); margin:0 0 10px; }
.sec-label .meta { font-weight:500; text-transform:none; letter-spacing:0; font-size:11.5px; }

.empty { color:var(--dim); font-size:13px; padding:18px; text-align:center;
  border:1px dashed var(--bd2); border-radius:12px; background:var(--panel2); }

.proc-grid { display:flex; gap:24px; flex-wrap:wrap; background:var(--panel2);
  border:1px solid var(--bd); border-radius:10px; padding:14px 18px; }
.proc-stat { display:flex; flex-direction:column; gap:2px; }
.pval { font:700 22px system-ui; color:var(--acc); letter-spacing:-.5px; }
.plbl { font:11px system-ui; color:var(--dim); }

.panel { background:var(--panel2); border:1px solid var(--bd); border-radius:10px; overflow-x:auto; }

.htable { width:100%; border-collapse:collapse; font-size:13px; }
.h-th { text-align:left; font:600 11px system-ui; letter-spacing:.5px; text-transform:uppercase;
  color:var(--dim); padding:8px 12px; background:var(--panel2);
  border-bottom:1px solid var(--bd); user-select:none; white-space:nowrap; }
.h-th.sortable { cursor:pointer; transition:color .12s; }
.h-th.sortable:hover { color:var(--tx); }
.h-th.active { color:var(--acc); }
.h-row:not(:last-child) td { border-bottom:1px solid var(--bd2); }
.h-row:hover td { background:var(--hover); }
.h-row-err td { background:color-mix(in srgb, var(--crit) 40%, transparent); }
.h-row-err:hover td { background:color-mix(in srgb, var(--crit) 60%, transparent); }
.h-row td { padding:8px 12px; vertical-align:middle; }
.h-mono { font-family:var(--mono); color:var(--tx); }
.h-err-val { color:var(--err); font-weight:700; }
.h-err-msg { color:var(--err); font-size:11px; }
.h-outcome-cell { white-space:nowrap; }
.h-spark { display:inline-flex; color:var(--dim); }
.spark-cell { padding:6px 12px; vertical-align:middle; }

.dot { display:inline-block; width:8px; height:8px; border-radius:50%; vertical-align:middle; }
.dot.ok   { background:var(--ok); }
.dot.err  { background:var(--err); }
.dot.muted { background:var(--bd2); }

.h-filterbar { display:flex; align-items:center; gap:6px; margin-bottom:12px; }
.h-filter { flex:1; font:13.5px var(--mono); padding:7px 10px;
  border:1px solid var(--bd); border-radius:8px;
  background:var(--panel2); color:var(--tx); outline:none;
  transition:border-color .12s; }
.h-filter:focus { border-color:var(--acc); }
.h-filter-clear { border:none; background:none; color:var(--dim); cursor:pointer;
  font-size:13px; padding:4px 6px; border-radius:6px; transition:color .12s; }
.h-filter-clear:hover { color:var(--tx); }
`;
