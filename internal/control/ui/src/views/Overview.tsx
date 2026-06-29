import { createMemo, createSignal, For, Show, type JSX } from "solid-js";
import { A } from "@solidjs/router";
import { useStore } from "../store/store";
import { postJSON, ApiError } from "../api/client";
import { ConfirmButton } from "../shell/ConfirmDialog";
import { ActionError } from "../shell/ActionError";
import { fmtNum } from "../utils/fmt";
import { selfObsDashboardURL } from "../utils/config";
import type {
  BlueprintEmission,
  BlueprintInventory,
  Diagnostic,
  SinkStat,
} from "../api/types";

// Canonical per-view template (the shape Phase-2 views copy): read useStore().state,
// render with <Show>/<For>, surface distinct loading / empty states, and mutate via
// postJSON(...).then(store.refresh) with destructive actions wrapped in <ConfirmButton>.
//
// Ports internal/control/ui.html renderOverview: verdict banner, load-time diagnostics,
// and the per-blueprint emission grid (series / constructs / metrics / rate + sparkline +
// enable-disable toggle). The legacy toggle had no confirm; this one does (review fix).

const SINK_LABELS: Record<string, string> = {
  promrw: "metrics",
  loki: "logs",
  otlp: "traces",
  faro: "rum",
};


function sparkTooltip(values: number[] | undefined): string {
  const v = (values ?? []).filter((x) => typeof x === "number");
  if (v.length === 0) return "";
  const latest = Math.round(v[v.length - 1]);
  const min = Math.round(Math.min(...v));
  const max = Math.round(Math.max(...v));
  return `latest: ${latest} · min: ${min} · max: ${max}`;
}

// sparkline(values) → an inline <svg> element (currentColor stroke, inherits row colour).
// A 5s tick makes any single reading noise; the trend is the signal. Flat/empty ⇒ null.
function sparkline(values: number[] | undefined, w = 52, h = 14): JSX.Element | null {
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
    <svg
      width={w}
      height={h}
      viewBox={`0 0 ${w} ${h}`}
      fill="none"
      stroke="currentColor"
      stroke-width="1.2"
      stroke-linejoin="round"
      stroke-linecap="round"
      opacity="0.85"
    >
      <polyline points={pts} />
    </svg>
  );
}

type Verdict = "green" | "amber" | "red";

export function Overview(): JSX.Element {
  const store = useStore();
  const s = () => store.state;

  const loading = () => s().loading;
  const state = () => s().state;
  const inventory = () => s().inventory;
  const status = () => s().status;
  const diagnostics = (): Diagnostic[] => s().diagnostics ?? [];
  const selfObsURL = () => selfObsDashboardURL(s().config);

  const disabledSet = () => new Set(state()?.disabled_blueprints ?? []);

  // Authoritative blueprint list: the inventory report lists EVERY loaded blueprint
  // (disabled ones keep their constructs with zeroed emission — runner/inventory.go),
  // unioned defensively with disabled_blueprints, then sorted for stable order.
  const blueprintNames = (): string[] => {
    const names = new Set<string>();
    for (const b of inventory()?.blueprints ?? []) names.add(b.blueprint);
    for (const d of state()?.disabled_blueprints ?? []) names.add(d);
    return [...names].sort();
  };
  const hasBlueprints = () => blueprintNames().length > 0;

  const invByName = (): Record<string, BlueprintInventory> => {
    const m: Record<string, BlueprintInventory> = {};
    for (const b of inventory()?.blueprints ?? []) m[b.blueprint] = b;
    return m;
  };
  const emByName = (): Record<string, BlueprintEmission> => status()?.by_blueprint ?? {};

  // ── action error (mutation failures surface here, not silently swallowed) ────
  const [actionErr, setActionErr] = createSignal<string>();

  // ── verdict banner (mirrors the legacy reason-collection + level logic) ──────
  const reasons = createMemo((): string[] => {
    const out: string[] = [];
    const st = status();
    if (st) {
      for (const sk of st.sinks ?? ([] as SinkStat[])) {
        if (sk.last_error && sk.last_error_ms && sk.last_error_ms > (sk.last_success_ms || 0)) {
          out.push((SINK_LABELS[sk.sink] || sk.sink) + " sink failing: " + sk.last_error);
        }
      }
      if (st.persist?.last_error) out.push("persist error: " + st.persist.last_error);
    }
    const h = s().health;
    if (h) {
      let dropped = 0;
      for (const b of h.blueprints ?? []) dropped += b.dropped_ticks || 0;
      if (dropped > 0) out.push(dropped + " dropped tick" + (dropped === 1 ? "" : "s") + " across blueprints");
      let cstErrors = 0;
      for (const c of h.constructs ?? []) cstErrors += c.errors || 0;
      if (cstErrors > 0) out.push(cstErrors + " construct error" + (cstErrors === 1 ? "" : "s") + " recorded");
    }
    const dis = disabledSet();
    if (dis.size > 0) out.push([...dis].join(", ") + " blueprint" + (dis.size === 1 ? "" : "s") + " disabled");
    const diags = diagnostics();
    if (diags.length) {
      const errs = diags.filter((d) => d.level === "error").length;
      const warns = diags.length - errs;
      const parts: string[] = [];
      if (errs) parts.push(errs + " blueprint" + (errs === 1 ? "" : "s") + " failed to load");
      if (warns) parts.push(warns + " config warning" + (warns === 1 ? "" : "s"));
      if (parts.length) out.push(parts.join(", ") + " — see Diagnostics below");
    }
    return out;
  });

  const level = (): Verdict => {
    const rs = reasons();
    // A blueprint that failed to load is never "minor" — escalate straight to red.
    const hadLoadError = diagnostics().some((d) => d.level === "error");
    if (rs.length === 0) return "green";
    return hadLoadError || rs.length > 2 ? "red" : "amber";
  };
  const ICONS: Record<Verdict, string> = { green: "✓", amber: "⚠", red: "✕" };
  const TITLES: Record<Verdict, string> = {
    green: "All systems healthy",
    amber: "Minor issues detected",
    red: "Issues require attention",
  };

  function postDisabled(next: string[]) {
    void postJSON("blueprints", { disabled_blueprints: next })
      .then(() => { setActionErr(undefined); return store.refresh(); })
      .catch((e: unknown) => setActionErr(e instanceof ApiError ? e.message : String(e)));
  }

  // ── enable/disable one blueprint (full replace list; confirm-gated) ──────────
  function toggleBlueprint(bp: string) {
    const dis = disabledSet();
    dis.has(bp) ? dis.delete(bp) : dis.add(bp);
    postDisabled([...dis]);
  }

  // ── bulk enable/disable every loaded blueprint (full-list replace) ───────────
  // Ported from legacy ui.html setAllBlueprints/bulkBlueprintBtns. "All on" empties
  // the disabled list; "All off" disables every loaded name. Each is greyed out when
  // already in that state (legacy parity); "All off" is confirm-gated (destructive).
  const allDisabled = () => blueprintNames().length > 0 && disabledSet().size === blueprintNames().length;
  const noneDisabled = () => disabledSet().size === 0;
  function setAllBlueprints(enabled: boolean) {
    if (blueprintNames().length === 0) return;
    postDisabled(enabled ? [] : blueprintNames());
  }

  return (
    <section>
      <style>{VIEW_CSS}</style>
      <div class="pane-head">
        <h1>Overview</h1>
        <span class="sub">generator runtime at a glance</span>
      </div>

      <Show
        when={!loading()}
        fallback={
          <div class="ov-loading" data-testid="overview-loading" role="status" aria-live="polite">
            <span class="ov-spinner" aria-hidden="true" />
            Loading runtime…
          </div>
        }
      >
        {/* verdict banner */}
        <div class={`verdict ${level()}`}>
          <span class="vicon" aria-hidden="true">
            {ICONS[level()]}
          </span>
          <div class="vbody">
            <div class="vtitle">{TITLES[level()]}</div>
            <Show when={reasons().length > 0}>
              <ul class="vreasons">
                <For each={reasons()}>{(r) => <li>{r}</li>}</For>
              </ul>
            </Show>
          </div>
        </div>

        {/* self-obs deep-link (shown only when GC_SELF_GRAFANA_URL is configured) */}
        <Show when={selfObsURL()}>
          <a
            class="selfobs-link"
            data-testid="overview-selfobs-link"
            href={selfObsURL()}
            target="_blank"
            rel="noopener"
          >
            ↗ Open generator self-obs in Grafana
          </a>
        </Show>

        {/* action error banner (mutation .catch; dismissed on next successful action) */}
        <ActionError
          message={actionErr}
          testid="overview-action-error"
          onDismiss={() => setActionErr(undefined)}
        />

        {/* load-time diagnostics (only when present) */}
        <Show when={diagnostics().length > 0}>
          <section class="sec">
            <div class="sec-label">
              Diagnostics
              <span class="meta">
                {diagnostics().length} load-time {diagnostics().length === 1 ? "issue" : "issues"}
              </span>
            </div>
            <div class="ov-diaglist">
              <For each={diagnostics()}>
                {(d) => (
                  <div class={`ov-diag ${d.level === "error" ? "err" : "warn"}`}>
                    <span class="ov-diag-lvl">{d.level === "error" ? "ERROR" : "WARN"}</span>
                    <span class="ov-diag-src">
                      {d.source}
                      {d.category ? " · " + d.category : ""}
                    </span>
                    <span class="ov-diag-msg">{d.message}</span>
                  </div>
                )}
              </For>
            </div>
          </section>
        </Show>

        {/* per-blueprint emission grid: loading → error → empty → grid */}
        <Show
          when={hasBlueprints()}
          fallback={
            <Show
              when={!s().errors["inventory"]}
              fallback={
                <div class="ov-fetch-err" data-testid="overview-error" role="alert">
                  Failed to load inventory: {s().errors["inventory"]}
                </div>
              }
            >
              <div class="empty" data-testid="overview-empty">
                <div class="empty-title">No blueprints loaded.</div>
                <div class="empty-hint" data-testid="overview-firstrun-hint">
                  synthkit generates structured synthetic telemetry for any blueprint declared in <code>blueprints/*.yaml</code>.
                  To make changes, authenticate as username <code>control</code> with the value of <code>CONTROL_TOKEN</code>.
                </div>
              </div>
            </Show>
          }
        >
          <section class="sec">
            <div class="sec-label">
              Blueprints
              <span class="meta">{blueprintNames().length} loaded</span>
              <div class="bulkbtns">
                <button
                  type="button"
                  class="bulkbtn"
                  data-testid="overview-bulk-on"
                  disabled={noneDisabled()}
                  title="Enable every blueprint"
                  onClick={() => setAllBlueprints(true)}
                >
                  All on
                </button>
                <ConfirmButton
                  class="bulkbtn"
                  testid="overview-bulk-off"
                  label="All off"
                  disabled={allDisabled()}
                  confirmLabel="Disable all"
                  message={`Disable all ${blueprintNames().length} blueprints? Every instance stops ticking (counters resume from state when re-enabled).`}
                  onConfirm={() => setAllBlueprints(false)}
                />
              </div>
            </div>
            <div class="bpgrid">
              <For each={blueprintNames()}>
                {(bp) => {
                  const ib = () => invByName()[bp];
                  const em = () => emByName()[bp];
                  const isLive = () => !disabledSet().has(bp);
                  const spark = () => sparkline(em()?.spark);
                  return (
                    <div class="bpc">
                      <div class="bpc-head">
                        <A href={`/bp/${encodeURIComponent(bp)}`} class="bpc-name" title={bp}>
                          {bp}
                        </A>
                        <div class="bpc-headright">
                          <span class={`bpc-dot ${isLive() ? "live" : "idle"}`} />
                          <ConfirmButton
                            class={`bpc-toggle${isLive() ? "" : " off"}`}
                            label={isLive() ? "On" : "Off"}
                            confirmLabel={isLive() ? "Disable" : "Enable"}
                            message={
                              (isLive() ? "Disable" : "Enable") +
                              ` blueprint "${bp}"? ` +
                              (isLive()
                                ? "Its instances stop ticking (counters resume from state when re-enabled)."
                                : "Its instances resume ticking on the next tick.")
                            }
                            onConfirm={() => toggleBlueprint(bp)}
                          />
                        </div>
                      </div>
                      <div class="bpc-stats">
                        <div class="bpc-stat">
                          <div class="bpc-val">{fmtNum(ib()?.distinct_series ?? 0)}</div>
                          <div class="bpc-lbl">series</div>
                        </div>
                        <div class="bpc-stat">
                          <div class="bpc-val">{fmtNum((ib()?.constructs ?? []).length)}</div>
                          <div class="bpc-lbl">constructs</div>
                        </div>
                        <div class="bpc-stat">
                          <div class="bpc-val">{fmtNum(ib()?.metric_names ?? 0)}</div>
                          <div class="bpc-lbl">metrics</div>
                        </div>
                        <div class="bpc-stat">
                          <div class="bpc-val">
                            {(em()?.rate_per_min ?? 0) > 0 ? "~" + fmtNum(em()!.rate_per_min) + "/m" : "—"}
                          </div>
                          <div class="bpc-lbl">rate</div>
                        </div>
                      </div>
                      <Show when={spark()}>
                        <div class="bpc-spark" title={sparkTooltip(em()?.spark)}>{spark()}</div>
                      </Show>
                    </div>
                  );
                }}
              </For>
            </div>
          </section>
        </Show>
      </Show>
    </section>
  );
}

// View-scoped styles, ported from the legacy ui.html <style> block for the Overview
// classes (.verdict / .sec / .bpgrid / .bpc / .empty) plus loading + diagnostics rows.
// Kept in-component (the single global stylesheet src/theme/tokens.css carries only the
// shell chrome). All colours read the polished design tokens so light/dark just work.
const VIEW_CSS = `
.verdict { display:flex; gap:12px; align-items:flex-start; border-radius:12px; padding:14px 18px;
  font-size:13.5px; font-weight:600; margin:0 0 22px; }
.verdict.green { background:var(--okbg); border:1px solid var(--okbd); color:var(--ok); }
.verdict.amber { background:var(--warnbg); border:1px solid var(--warnbd); color:var(--warn); }
.verdict.red   { background:var(--crit);   border:1px solid var(--critbd); color:var(--err); }
.verdict .vicon { font-size:20px; flex:none; line-height:1; }
.verdict .vbody { flex:1; }
.verdict .vtitle { font-weight:800; font-size:15px; margin-bottom:4px; }
.verdict .vreasons { font-weight:500; font-size:12.5px; margin-top:4px; list-style:none; padding:0; }
.verdict .vreasons li::before { content:"• "; }

.selfobs-link { display:inline-flex; align-items:center; gap:6px; font:600 12px system-ui;
  color:var(--acc); text-decoration:none; border:1px solid var(--accbd); background:var(--acc2);
  border-radius:8px; padding:6px 13px; margin:0 0 20px; transition:filter .12s; }
.selfobs-link:hover { filter:brightness(.96); text-decoration:underline; }

.sec { margin-bottom:26px; }
.sec-label { display:flex; align-items:baseline; gap:8px; font:700 11px system-ui; letter-spacing:.8px;
  text-transform:uppercase; color:var(--dim); margin:0 0 12px; }
.sec-label .meta { font-weight:500; text-transform:none; letter-spacing:0; font-size:11.5px; }
.bulkbtns { display:flex; gap:8px; margin-left:auto; }
.bulkbtn { font:600 11px system-ui; text-transform:none; letter-spacing:0; border:1px solid var(--bd);
  background:var(--panel2); color:var(--tx); border-radius:8px; padding:4px 11px; cursor:pointer;
  transition:border-color .12s; }
.bulkbtn:hover:not(:disabled) { border-color:var(--acc); }
.bulkbtn:disabled { opacity:.4; cursor:default; }

.empty { color:var(--dim); font-size:13px; padding:18px; text-align:center;
  border:1px dashed var(--bd2); border-radius:12px; background:var(--panel2); }
.empty-title { font-weight:700; margin-bottom:6px; }
.empty-hint { font-size:12px; color:var(--dim); margin-top:6px; line-height:1.55; }
.empty-hint code { font-family:var(--mono); font-size:11px; background:var(--panel2); padding:1px 4px; border-radius:4px; }

.ov-fetch-err { font-size:13px; font-weight:600; color:var(--err);
  background:var(--crit); border:1px solid var(--critbd); border-radius:12px;
  padding:14px 18px; text-align:center; }

.ov-loading { display:flex; align-items:center; gap:10px; color:var(--dim); font-size:13px;
  padding:18px; border:1px dashed var(--bd2); border-radius:12px; background:var(--panel2); }
.ov-spinner { width:14px; height:14px; border-radius:50%; border:2px solid var(--bd2);
  border-top-color:var(--acc); animation:ov-spin .7s linear infinite; flex:none; }
@keyframes ov-spin { to { transform:rotate(360deg); } }

.ov-diaglist { display:flex; flex-direction:column; gap:6px; }
.ov-diag { display:flex; gap:10px; align-items:baseline; padding:8px 10px; border-radius:6px;
  background:var(--panel2); border-left:3px solid var(--warn); }
.ov-diag.err { border-left-color:var(--err); }
.ov-diag-lvl { font:600 11px system-ui; letter-spacing:.04em; color:var(--warn); flex:none; }
.ov-diag.err .ov-diag-lvl { color:var(--err); }
.ov-diag-src { font:11.5px var(--mono); color:var(--dim); flex:none; }
.ov-diag-msg { flex:1; color:var(--tx); font-weight:500; }

.bpgrid { display:grid; grid-template-columns:repeat(auto-fill,minmax(230px,1fr)); gap:14px; }
.bpc { background:var(--card-grad); border:1px solid var(--bd); border-radius:12px; padding:14px 16px;
  box-shadow:var(--card-shadow); transition:border-color .12s, box-shadow .12s; }
.bpc:hover { border-color:var(--acc); box-shadow:0 3px 12px var(--shadow2); }
.bpc .bpc-head { display:flex; justify-content:space-between; align-items:center; gap:8px; margin-bottom:10px; }
.bpc .bpc-name { font-weight:700; font-size:13.5px; color:var(--tx); text-decoration:none;
  overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.bpc .bpc-name:hover { color:var(--acc); text-decoration:underline; }
.bpc .bpc-headright { display:flex; align-items:center; gap:8px; flex:none; }
.bpc .bpc-dot { width:8px; height:8px; border-radius:50%; flex:none; }
.bpc .bpc-dot.live { background:var(--ok); animation:pulse 1.3s infinite; }
.bpc .bpc-dot.idle { background:var(--bd2); }
.bpc .bpc-toggle { font:700 10px system-ui; text-transform:uppercase; letter-spacing:.4px;
  border:1px solid var(--okbd); background:var(--okbg); color:var(--ok); border-radius:6px;
  padding:3px 9px; cursor:pointer; transition:filter .12s; }
.bpc .bpc-toggle.off { border-color:var(--bd); background:var(--panel2); color:var(--dim); }
.bpc .bpc-toggle:hover { filter:brightness(1.08); }
.bpc .bpc-stats { display:grid; grid-template-columns:1fr 1fr; gap:6px 12px; margin-bottom:8px; }
.bpc .bpc-stat .bpc-val { font:700 18px system-ui; color:var(--acc); letter-spacing:-.5px; }
.bpc .bpc-stat .bpc-lbl { font:11px system-ui; color:var(--dim); }
.bpc .bpc-spark { color:var(--dim); }
`;
