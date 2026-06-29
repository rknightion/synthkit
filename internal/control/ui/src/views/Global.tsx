import { createMemo, createSignal, For, Show, type JSX } from "solid-js";
import { useStore } from "../store/store";
import { postJSON, ApiError } from "../api/client";
import { ConfirmButton } from "../shell/ConfirmDialog";
import { ActionError } from "../shell/ActionError";
import type { Descriptor, Schema, State, TargetInfo } from "../api/types";

// Ports internal/control/ui.html renderGlobal: master volume (range + numeric + preset
// chips, clamped to schema bounds), per-blueprint enable/disable (chips + bulk all-on /
// confirm-gated all-off), per-blueprint span-metrics opt-in, construct-kind enable/disable,
// and the unscoped-scalable-targets count steppers. Every mutation follows the canonical
// postJSON(...).then(refresh).catch(setActionErr) pattern with an action-error banner;
// the destructive bulk-disable-all is wrapped in <ConfirmButton>.
//
// Schema is null-guarded throughout: Snapshot.schema is the rich Schema in the operator UI,
// but the /control/schema route can serve a bare Descriptor[] when no schema source is wired.
// Reads degrade gracefully (s().schema?.volume_multiplier, etc.) rather than crashing.

const fmtMult = (n: number): string => (Number.isInteger(n) ? n + "×" : n.toFixed(1) + "×");

function clamp(v: number, min: number, max: number): number {
  if (Number.isNaN(v)) return min;
  return Math.min(max, Math.max(min, v));
}

// A scalable target's wire key is the qualified "blueprint/name" id (server-stamped owner).
const targetId = (t: TargetInfo): string => t.blueprint + "/" + t.name;

export function Global(): JSX.Element {
  const store = useStore();
  const s = () => store.state;

  // The rich Schema only when the operator schema is wired; otherwise undefined (bare
  // Descriptor[] arrives as an array, which has no .blueprints/.kinds — treat as absent).
  const schema = (): Schema | undefined => {
    const sc = s().schema as Schema | undefined;
    return sc && !Array.isArray(sc) ? sc : undefined;
  };
  const state = (): State | undefined => s().state;

  // ── derived (null-guarded) selectors, mirroring legacy renderGlobal helpers ──
  const blueprints = (): string[] => schema()?.blueprints ?? [];
  const kinds = (): string[] => schema()?.kinds ?? [];
  const disabledSet = () => new Set(state()?.disabled_blueprints ?? []);
  const spanMetricsSet = () => new Set(state()?.span_metrics_blueprints ?? []);
  const disabledKindSet = () => new Set(state()?.disabled_kinds ?? []);
  const scalableTargets = (): TargetInfo[] => (schema()?.targets ?? []).filter((t) => t.scalable);
  const unscopedScalable = (): TargetInfo[] => scalableTargets().filter((t) => !t.blueprint);
  // effective live count for a scalable target: explicit override wins, else schema's current.
  const liveCount = (t: TargetInfo): number => {
    const id = targetId(t);
    const sc = state()?.scaling;
    return sc && id in sc ? sc[id] : (t.scalable?.current ?? 0);
  };

  const volDesc = (): Descriptor | undefined => schema()?.volume_multiplier;
  const volume = (): number => state()?.volume_multiplier ?? 1;

  // ── action error (mutation failures surface here, not silently swallowed) ────
  const [actionErr, setActionErr] = createSignal<string>();
  const runMutation = (p: Promise<unknown>) =>
    void p
      .then(() => { setActionErr(undefined); return store.refresh(); })
      .catch((e: unknown) => setActionErr(e instanceof ApiError ? e.message : String(e)));

  // Local volume slider/input state (commit-on-change; mirrors legacy range↔num sync).
  const [volDraft, setVolDraft] = createSignal<number | undefined>();
  const volShown = (): number => volDraft() ?? volume();

  // ── mutations (each clamps/replaces exactly as legacy renderGlobal did) ──────
  function commitVolume(v: number) {
    const d = volDesc();
    const cv = d ? clamp(v, d.min, d.max) : v;
    setVolDraft(cv);
    runMutation(postJSON("load", { volume_multiplier: cv }));
  }
  function toggleBlueprint(bp: string) {
    const dis = disabledSet();
    dis.has(bp) ? dis.delete(bp) : dis.add(bp);
    runMutation(postJSON("blueprints", { disabled_blueprints: [...dis] }));
  }
  function setAllBlueprints(enabled: boolean) {
    if (!blueprints().length) return;
    runMutation(postJSON("blueprints", { disabled_blueprints: enabled ? [] : [...blueprints()] }));
  }
  function toggleSpanMetrics(bp: string) {
    const sm = spanMetricsSet();
    sm.has(bp) ? sm.delete(bp) : sm.add(bp);
    runMutation(postJSON("spanmetrics", { span_metrics_blueprints: [...sm] }));
  }
  function toggleKind(k: string) {
    const dis = disabledKindSet();
    dis.has(k) ? dis.delete(k) : dis.add(k);
    runMutation(postJSON("kinds", { disabled_kinds: [...dis] }));
  }
  function commitScaling(t: TargetInfo, count: number) {
    const sc = t.scalable;
    const cv = sc ? clamp(Math.round(count), sc.min, sc.max) : Math.round(count);
    runMutation(postJSON("scaling", { [targetId(t)]: cv }));
  }

  // Preset chips: legacy [0,1,2,5,10] filtered to the descriptor's bounds.
  const presets = createMemo((): number[] => {
    const d = volDesc();
    if (!d) return [];
    return [0, 1, 2, 5, 10].filter((x) => x >= d.min && x <= d.max);
  });

  const loading = () => s().loading;
  // Fatal error only when state itself failed (schema errors degrade via null-guards).
  const errKey = (): string | undefined => s().errors["state"];

  return (
    <section>
      <style>{VIEW_CSS}</style>
      <div class="pane-head">
        <h1>Global controls</h1>
        <span class="sub">apply across every blueprint</span>
      </div>
      <p class="pane-lead">
        Master load, blueprint enablement, span-metrics opt-in, and construct-kind toggles. Per-blueprint
        incident scenarios and scaling live under each blueprint in the sidebar.
      </p>

      <Show
        when={!loading()}
        fallback={
          <div class="g-loading" data-testid="global-loading" role="status">
            <span class="g-spinner" />
            Loading controls…
          </div>
        }
      >
        <Show
          when={!errKey()}
          fallback={
            <div class="g-fetch-err" data-testid="global-error" role="alert">
              Failed to load controls: {errKey()}
            </div>
          }
        >
          {/* action error banner (mutation .catch; cleared on next successful action) */}
          <ActionError
            message={actionErr}
            testid="global-action-error"
            onDismiss={() => setActionErr(undefined)}
          />

          {/* ── master volume ────────────────────────────────────────────── */}
          <Show when={volDesc()}>
            {(d) => (
              <section class="sec">
                <div class="sec-label">Master volume</div>
                <div class="panel">
                  <h3>Volume multiplier</h3>
                  <p class="gh">{d().help}</p>
                  <div class="vrow">
                    <span class="vbig" data-testid="global-vol-shown">{fmtMult(volShown())}</span>
                    <input
                      class="vrange"
                      type="range"
                      min={d().min}
                      max={d().max}
                      step="0.1"
                      value={volShown()}
                      data-testid="global-vol-range"
                      onInput={(e) => setVolDraft(Number(e.currentTarget.value))}
                      onChange={(e) => commitVolume(Number(e.currentTarget.value))}
                    />
                    <input
                      class="vnum"
                      type="number"
                      min={d().min}
                      max={d().max}
                      step="0.1"
                      value={volShown()}
                      data-testid="global-vol-num"
                      onChange={(e) => commitVolume(Number(e.currentTarget.value))}
                    />
                  </div>
                  <div class="chips">
                    <For each={presets()}>
                      {(p) => (
                        <button
                          class={volume() === p ? "chip cur" : "chip"}
                          data-testid={`global-vol-preset-${p}`}
                          onClick={() => commitVolume(p)}
                        >
                          {p === 1 ? "1× (default)" : p + "×"}
                        </button>
                      )}
                    </For>
                  </div>
                </div>
              </section>
            )}
          </Show>

          {/* ── blueprints enable/disable ────────────────────────────────── */}
          <section class="sec">
            <div class="sec-label">
              Blueprints<span class="meta">{blueprints().length} loaded</span>
            </div>
            <div class="panel">
              <h3>Enabled blueprints</h3>
              <p class="gh">
                Disabled blueprints stop emitting synthetic telemetry. Toggle to enable/disable live.
              </p>
              <Show when={blueprints().length > 0}>
                <div class="bulkbtns">
                  <button
                    class="bulkbtn"
                    disabled={disabledSet().size === 0}
                    title="Enable every blueprint"
                    data-testid="global-bp-all-on"
                    onClick={() => setAllBlueprints(true)}
                  >
                    All on
                  </button>
                  <ConfirmButton
                    class="bulkbtn"
                    label="All off"
                    confirmLabel="Disable all"
                    message="Disable EVERY blueprint? This turns synthetic telemetry off across the whole generator."
                    onConfirm={() => setAllBlueprints(false)}
                  />
                </div>
              </Show>
              <Show
                when={blueprints().length > 0}
                fallback={<div class="empty" data-testid="global-bp-empty">No blueprints loaded.</div>}
              >
                <div class="bpchips">
                  <For each={blueprints()}>
                    {(bp) => {
                      const off = () => disabledSet().has(bp);
                      return (
                        <button
                          class={off() ? "bpchip off" : "bpchip"}
                          data-testid={`global-bp-${bp}`}
                          onClick={() => toggleBlueprint(bp)}
                        >
                          <span class="st" />
                          {bp}
                        </button>
                      );
                    }}
                  </For>
                </div>
              </Show>
            </div>
          </section>

          {/* ── per-blueprint span-metrics opt-in ────────────────────────── */}
          <section class="sec">
            <div class="sec-label">
              Span-metrics emission<span class="meta">{blueprints().length} blueprints</span>
            </div>
            <div class="panel">
              <h3>Synthkit span-metrics opt-in</h3>
              <p class="gh">
                Opt each blueprint IN to emit synthkit's own backend spanmetrics + service-graph series
                (traces_spanmetrics_* / traces_service_graph_*). Default OFF — Grafana Cloud
                metrics-generator / beyla produce the same families from the trace stream; enable here
                only when those are absent.
              </p>
              <Show
                when={blueprints().length > 0}
                fallback={<div class="empty" data-testid="global-sm-empty">No blueprints loaded.</div>}
              >
                <div class="bpchips">
                  <For each={blueprints()}>
                    {(bp) => {
                      const on = () => spanMetricsSet().has(bp);
                      return (
                        <button
                          class={on() ? "bpchip" : "bpchip off"}
                          title={
                            on()
                              ? "Synthkit emitting span-metrics for " + bp
                              : "Deferring span-metrics to metrics-generator/beyla for " + bp
                          }
                          data-testid={`global-sm-${bp}`}
                          onClick={() => toggleSpanMetrics(bp)}
                        >
                          <span class="st" />
                          {bp}
                        </button>
                      );
                    }}
                  </For>
                </div>
              </Show>
            </div>
          </section>

          {/* ── construct kinds enable/disable (global) ──────────────────── */}
          <section class="sec">
            <div class="sec-label">
              Construct kinds<span class="meta">{kinds().length} kinds</span>
            </div>
            <div class="panel">
              <h3>Enabled construct kinds</h3>
              <p class="gh">
                Disable a construct kind to stop it emitting across EVERY blueprint. Per-instance toggles
                live under each blueprint.
              </p>
              <Show
                when={kinds().length > 0}
                fallback={<div class="empty" data-testid="global-kinds-empty">No construct kinds loaded.</div>}
              >
                <div class="bpchips">
                  <For each={kinds()}>
                    {(k) => {
                      const off = () => disabledKindSet().has(k);
                      return (
                        <button
                          class={off() ? "bpchip off" : "bpchip"}
                          data-testid={`global-kind-${k}`}
                          onClick={() => toggleKind(k)}
                        >
                          <span class="st" />
                          {k}
                        </button>
                      );
                    }}
                  </For>
                </div>
              </Show>
            </div>
          </section>

          {/* ── unscoped scalable targets (fallback bucket) ──────────────── */}
          <Show when={unscopedScalable().length > 0}>
            <section class="sec">
              <div class="sec-label">
                Scaling — unscoped<span class="meta">targets not matched to a blueprint by name</span>
              </div>
              <div class="panel">
                <h3>Scalable targets</h3>
                <p class="gh">These scalable targets did not match any blueprint name prefix.</p>
                <For each={unscopedScalable()}>
                  {(t) => {
                    const sc = t.scalable!;
                    const cur = () => liveCount(t);
                    return (
                      <div class="scl" data-testid={`global-scale-${targetId(t)}`}>
                        <span class="nm">
                          <b>{t.name}</b>
                          <small>{sc.dimension} · def {sc.default} · {sc.min}–{sc.max}</small>
                        </span>
                        <input
                          class="vrange"
                          type="range"
                          min={sc.min}
                          max={sc.max}
                          step="1"
                          value={cur()}
                          onChange={(e) => commitScaling(t, Number(e.currentTarget.value))}
                        />
                        <span class={cur() !== sc.default ? "ct chg" : "ct"}>{cur()}</span>
                      </div>
                    );
                  }}
                </For>
              </div>
            </section>
          </Show>
        </Show>
      </Show>
    </section>
  );
}

const VIEW_CSS = `
.pane-lead { font-size:13px; color:var(--dim); margin:0 0 20px; line-height:1.5; }

.g-loading { display:flex; align-items:center; gap:10px; color:var(--dim); font-size:13px;
  padding:18px; border:1px dashed var(--bd2); border-radius:12px; background:var(--panel2); }
.g-spinner { width:14px; height:14px; border-radius:50%; border:2px solid var(--bd2);
  border-top-color:var(--acc); animation:g-spin .7s linear infinite; flex:none; }
@keyframes g-spin { to { transform:rotate(360deg); } }

.g-fetch-err { font-size:13px; font-weight:600; color:var(--err);
  background:var(--crit); border:1px solid var(--critbd); border-radius:12px;
  padding:14px 18px; text-align:center; }

.sec { margin-bottom:26px; }
.sec-label { display:flex; align-items:baseline; gap:8px; font:700 11px system-ui; letter-spacing:.8px;
  text-transform:uppercase; color:var(--dim); margin:0 0 10px; }
.sec-label .meta { font-weight:500; text-transform:none; letter-spacing:0; font-size:11.5px; }

.panel { background:var(--panel2); border:1px solid var(--bd); border-radius:12px; padding:16px 18px; }
.panel h3 { font:700 14px system-ui; color:var(--tx); margin:0 0 6px; }
.panel .gh { font-size:12.5px; color:var(--dim); margin:0 0 14px; line-height:1.5; }

.empty { color:var(--dim); font-size:13px; padding:14px; text-align:center;
  border:1px dashed var(--bd2); border-radius:10px; background:var(--panel); }

.vrow { display:flex; align-items:center; gap:14px; margin-bottom:14px; }
.vbig { font:800 22px system-ui; color:var(--acc); letter-spacing:-.5px; min-width:64px; }
.vrange { flex:1; accent-color:var(--acc); cursor:pointer; }
.vnum { width:80px; font:13.5px var(--mono); padding:6px 9px; border:1px solid var(--bd);
  border-radius:8px; background:var(--panel); color:var(--tx); outline:none; }
.vnum:focus { border-color:var(--acc); }

.chips { display:flex; flex-wrap:wrap; gap:8px; }
.chip { font:600 12px system-ui; padding:5px 12px; border:1px solid var(--bd); border-radius:20px;
  background:var(--panel); color:var(--dim); cursor:pointer; transition:all .12s; }
.chip:hover { border-color:var(--acc); color:var(--tx); }
.chip.cur { border-color:var(--okbd); background:var(--okbg); color:var(--ok); }

.bulkbtns { display:flex; gap:8px; margin-bottom:14px; }
.bulkbtn { font:600 12px system-ui; padding:5px 12px; border:1px solid var(--bd); border-radius:8px;
  background:var(--panel); color:var(--tx); cursor:pointer; transition:all .12s; }
.bulkbtn:hover:not(:disabled) { border-color:var(--acc); }
.bulkbtn:disabled { opacity:.45; cursor:default; }

.bpchips { display:flex; flex-wrap:wrap; gap:8px; }
.bpchip { display:inline-flex; align-items:center; gap:7px; font:600 12.5px system-ui;
  padding:6px 12px; border:1px solid var(--okbd); border-radius:8px; background:var(--okbg);
  color:var(--ok); cursor:pointer; transition:all .12s; }
.bpchip:hover { filter:brightness(1.06); }
.bpchip .st { width:8px; height:8px; border-radius:50%; background:var(--ok); flex:none; }
.bpchip.off { border-color:var(--bd); background:var(--panel); color:var(--dim); }
.bpchip.off .st { background:var(--bd2); }

.scl { display:flex; align-items:center; gap:14px; padding:8px 0; }
.scl:not(:last-child) { border-bottom:1px solid var(--bd2); }
.scl .nm { display:flex; flex-direction:column; min-width:180px; }
.scl .nm b { font:600 13px system-ui; color:var(--tx); }
.scl .nm small { font:11.5px var(--mono); color:var(--dim); }
.scl .vrange { flex:1; }
.scl .ct { font:700 16px system-ui; color:var(--tx); min-width:36px; text-align:right; }
.scl .ct.chg { color:var(--acc); }
`;
