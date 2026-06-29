import { For, Show, type JSX } from "solid-js";
import { useStore } from "../store/store";
import { fmtNum } from "../utils/fmt";
import type { ConstructInventory } from "../api/types";

// Ports internal/control/ui.html renderXray: per-construct emission inventory.
// READ-ONLY — no mutations. States: loading → error → empty → data.
// Per-construct cards with expandable <details> drills for metric names, label keys,
// log sources, span services, span attr keys. Capped badge + copy-all per drill.

// One expandable <details> drill: label, items list, copy-all button.
function Drill(props: { label: string; items: string[]; copyId: string }): JSX.Element {
  function copyAll() {
    void navigator.clipboard.writeText(props.items.join("\n"));
  }
  return (
    <details class="xr-drill" data-testid={`drill-${props.copyId}`}>
      <summary class="xr-drill-sum">
        {props.label}
        <span class="xr-drill-count">({props.items.length})</span>
        <button
          class="xr-copy-btn"
          title={`Copy all ${props.label} to clipboard`}
          onClick={(e) => { e.preventDefault(); copyAll(); }}
          data-testid={`copy-${props.copyId}`}
        >
          copy
        </button>
      </summary>
      <pre class="xr-drill-pre">{props.items.join("\n")}</pre>
    </details>
  );
}

// Per-construct card: kind/name header, series count, capped badge, drills.
function ConstructCard(props: { cst: ConstructInventory; bpKey: string }): JSX.Element {
  const cst = props.cst;
  const keyPrefix = `${props.bpKey}:${cst.kind}:${cst.name}`;

  const drills: Array<{ label: string; items: string[]; id: string }> = [];
  if (cst.metric_names?.length)     drills.push({ label: "metric names",      items: cst.metric_names,     id: `${keyPrefix}:metric-names` });
  if (cst.metric_label_keys?.length) drills.push({ label: "metric label keys", items: cst.metric_label_keys, id: `${keyPrefix}:metric-label-keys` });
  if (cst.log_sources?.length)      drills.push({ label: "log sources",        items: cst.log_sources,      id: `${keyPrefix}:log-sources` });
  if (cst.log_label_keys?.length)   drills.push({ label: "log label keys",     items: cst.log_label_keys,   id: `${keyPrefix}:log-label-keys` });
  if (cst.span_services?.length)    drills.push({ label: "span services",      items: cst.span_services,    id: `${keyPrefix}:span-services` });
  if (cst.span_names?.length)       drills.push({ label: "span names",         items: cst.span_names,       id: `${keyPrefix}:span-names` });
  if (cst.span_attr_keys?.length)   drills.push({ label: "span attr keys",     items: cst.span_attr_keys,   id: `${keyPrefix}:span-attr-keys` });

  return (
    <div class="xr-cst" data-testid={`cst-card-${cst.kind}-${cst.name}`}>
      <div class="xr-cst-head">
        <span class="xr-kind">{cst.kind}</span>
        <span class="xr-name">{cst.name}</span>
        <span class="xr-series">
          {fmtNum(cst.distinct_series ?? 0)} series
          <Show when={cst.capped}>
            {" "}
            <span
              class="xr-badge-capped"
              title="Cardinality cap hit — this construct's series count is limited by the configured cap."
              data-testid={`capped-badge-${cst.kind}-${cst.name}`}
            >
              capped
            </span>
          </Show>
        </span>
      </div>
      <Show when={drills.length > 0}>
        <div class="xr-drills">
          <For each={drills}>
            {(d) => <Drill label={d.label} items={d.items} copyId={d.id} />}
          </For>
        </div>
      </Show>
    </div>
  );
}

export function Xray(): JSX.Element {
  const store = useStore();
  const s = () => store.state;

  const inventory = () => s().inventory;
  const totals = () => inventory()?.totals;
  const blueprints = () => inventory()?.blueprints ?? [];
  const hasData = () => blueprints().length > 0;

  return (
    <section>
      <style>{VIEW_CSS}</style>
      <div class="pane-head">
        <h1>X-ray</h1>
        <span class="sub">per-construct emission inventory</span>
      </div>
      <p class="pane-lead">
        Cardinality, metric names, label keys, log sources, and span shapes per construct.
        Internal bookkeeping only — nothing here is emitted on the wire.
      </p>

      <Show
        when={!s().loading}
        fallback={
          <div class="xr-loading" data-testid="xray-loading">
            <span class="xr-spinner" />
            Loading inventory…
          </div>
        }
      >
        <Show
          when={!s().errors["inventory"]}
          fallback={
            <div class="xr-fetch-err" data-testid="xray-error">
              Failed to load inventory: {s().errors["inventory"]}
            </div>
          }
        >
          <Show
            when={hasData()}
            fallback={
              <div class="empty" data-testid="xray-empty">
                No inventory data yet — wait for the first tick cycle.
              </div>
            }
          >
            {/* ── per-blueprint sections ──────────────────────────────────── */}
            <For each={blueprints()}>
              {(bpInv) => (
                <section class="sec" data-testid={`bp-section-${bpInv.blueprint}`}>
                  <div class="sec-label">
                    {bpInv.blueprint}
                    <span class="meta">
                      {fmtNum(bpInv.distinct_series)} series ·{" "}
                      {(bpInv.constructs ?? []).length} constructs ·{" "}
                      {fmtNum(bpInv.metric_names)} metrics
                    </span>
                  </div>

                  <Show
                    when={(bpInv.constructs ?? []).length > 0}
                    fallback={
                      <div class="empty">No constructs in this blueprint yet.</div>
                    }
                  >
                    <div class="xr-cards">
                      <For each={bpInv.constructs}>
                        {(cst) => (
                          <ConstructCard cst={cst} bpKey={bpInv.blueprint} />
                        )}
                      </For>
                    </div>
                  </Show>
                </section>
              )}
            </For>

            {/* ── totals panel (shown only when totals.blueprints > 0) ───── */}
            <Show when={(totals()?.blueprints ?? 0) > 0}>
              {(_) => {
                const tot = totals()!;
                return (
                  <section class="sec" data-testid="xray-totals">
                    <div class="sec-label">Totals</div>
                    <div class="xr-totals">
                      <div class="proc-stat">
                        <div class="pval">{fmtNum(tot.distinct_series ?? 0)}</div>
                        <div class="plbl">total series</div>
                      </div>
                      <div class="proc-stat">
                        <div class="pval">{fmtNum(tot.constructs ?? 0)}</div>
                        <div class="plbl">total constructs</div>
                      </div>
                      <div class="proc-stat">
                        <div class="pval">{fmtNum(tot.blueprints ?? 0)}</div>
                        <div class="plbl">blueprints</div>
                      </div>
                    </div>
                  </section>
                );
              }}
            </Show>
          </Show>
        </Show>
      </Show>
    </section>
  );
}

const VIEW_CSS = `
.xr-loading { display:flex; align-items:center; gap:10px; color:var(--dim); font-size:13px;
  padding:18px; border:1px dashed var(--bd2); border-radius:12px; background:var(--panel2); }
.xr-spinner { width:14px; height:14px; border-radius:50%; border:2px solid var(--bd2);
  border-top-color:var(--acc); animation:xr-spin .7s linear infinite; flex:none; }
@keyframes xr-spin { to { transform:rotate(360deg); } }

.xr-fetch-err { font-size:13px; font-weight:600; color:var(--err);
  background:var(--crit); border:1px solid var(--critbd); border-radius:12px;
  padding:14px 18px; text-align:center; }

.sec { margin-bottom:26px; }
.sec-label { display:flex; align-items:baseline; gap:8px; font:700 11px system-ui; letter-spacing:.8px;
  text-transform:uppercase; color:var(--dim); margin:0 0 12px; }
.sec-label .meta { font-weight:500; text-transform:none; letter-spacing:0; font-size:11.5px; }

.empty { color:var(--dim); font-size:13px; padding:18px; text-align:center;
  border:1px dashed var(--bd2); border-radius:12px; background:var(--panel2); }

.pane-lead { color:var(--dim); font-size:13px; margin:0 0 22px; line-height:1.5; }

/* ── construct cards ──────────────────────────────────────────────────────── */
.xr-cards { display:flex; flex-direction:column; gap:8px; }

.xr-cst { background:var(--panel2); border:1px solid var(--bd); border-radius:10px;
  overflow:hidden; }
.xr-cst-head { display:flex; align-items:baseline; gap:10px; padding:10px 14px;
  border-bottom:1px solid var(--bd2); flex-wrap:wrap; }
.xr-kind { font:600 11px var(--mono); letter-spacing:.04em; text-transform:uppercase;
  color:var(--acc); background:color-mix(in srgb, var(--acc) 12%, transparent);
  border:1px solid color-mix(in srgb, var(--acc) 25%, transparent);
  border-radius:5px; padding:2px 6px; }
.xr-name { font:700 13.5px var(--mono); color:var(--tx); flex:1; }
.xr-series { font:13px system-ui; color:var(--dim); display:flex; align-items:baseline; gap:5px; }
.xr-badge-capped { font:700 10px system-ui; text-transform:uppercase; letter-spacing:.4px;
  color:var(--warn); background:var(--warnbg); border:1px solid var(--warnbd);
  border-radius:5px; padding:2px 6px; cursor:default; }

/* ── drills ───────────────────────────────────────────────────────────────── */
.xr-drills { padding:6px 0; }
.xr-drill { border-top:1px solid var(--bd2); }
.xr-drill:first-child { border-top:none; }
.xr-drill-sum { display:flex; align-items:center; gap:6px; padding:7px 14px;
  cursor:pointer; font:600 12px system-ui; color:var(--tx); list-style:none;
  user-select:none; transition:background .1s; }
.xr-drill-sum::-webkit-details-marker { display:none; }
.xr-drill[open] .xr-drill-sum { background:color-mix(in srgb, var(--acc) 6%, transparent); }
.xr-drill-sum:hover { background:var(--hover); }
.xr-drill-count { font-weight:500; color:var(--dim); font-size:11.5px; }
.xr-copy-btn { margin-left:auto; font:600 10px system-ui; text-transform:uppercase; letter-spacing:.3px;
  border:1px solid var(--bd); background:var(--panel2); color:var(--dim); border-radius:5px;
  padding:2px 8px; cursor:pointer; transition:border-color .12s, color .12s; }
.xr-copy-btn:hover { border-color:var(--acc); color:var(--acc); }
.xr-drill-pre { margin:0; padding:8px 14px 12px; font:12.5px/1.55 var(--mono); color:var(--tx);
  white-space:pre-wrap; word-break:break-all; background:var(--bg); border-top:1px solid var(--bd2); }

/* ── totals ───────────────────────────────────────────────────────────────── */
.xr-totals { display:flex; gap:24px; flex-wrap:wrap; background:var(--panel2);
  border:1px solid var(--bd); border-radius:10px; padding:14px 18px; }
.proc-stat { display:flex; flex-direction:column; gap:2px; }
.pval { font:700 22px system-ui; color:var(--acc); letter-spacing:-.5px; }
.plbl { font:11px system-ui; color:var(--dim); }
`;
