import { createMemo, createSignal, For, Show, type JSX } from "solid-js";
import { useParams } from "@solidjs/router";
import { useStore } from "../store/store";
import { postJSON, getText, ApiError } from "../api/client";
import { ConfirmButton } from "../shell/ConfirmDialog";
import { ActionError } from "../shell/ActionError";
import { fmtNum } from "../utils/fmt";
import type {
  BlueprintHealth,
  BlueprintInventory,
  BlueprintMetaInfo,
  ConstructInfo,
  MetaFields,
  Schema,
  State,
  TargetInfo,
} from "../api/types";

// Ports internal/control/ui.html renderBlueprint: per-blueprint header with an
// enable/disable toggle (ConfirmButton; disabled→warning banner), human-facing metadata
// (description/tags/owner/category/links + per-env), an inventory+health summary, live
// service scaling sliders (clamped to schema bounds), per-construct disable toggles, and a
// lazily-fetched raw-YAML <details>. The blueprint name comes from the /bp/:name route
// param. Every mutation follows the canonical postJSON(...).then(refresh).catch(setActionErr)
// pattern with an action-error banner; the destructive disable is wrapped in <ConfirmButton>.
//
// Schema is null-guarded throughout (the bare Descriptor[] path serves an array with no
// .blueprints/.constructs — treated as absent). Metadata may be missing for any blueprint.

function clamp(v: number, min: number, max: number): number {
  if (Number.isNaN(v)) return min;
  return Math.min(max, Math.max(min, v));
}

// A scalable target's / construct's wire keys are the server-stamped qualified ids.
const targetId = (t: TargetInfo): string => t.blueprint + "/" + t.name;
const cstKey = (x: ConstructInfo): string => x.blueprint + "/" + x.kind + ":" + x.name;

// A meta block carries content if any human field is populated (mirrors legacy guards).
function metaHasContent(m: MetaFields | undefined): boolean {
  if (!m) return false;
  return !!(m.description || (m.tags?.length ?? 0) || m.owner || m.category || Object.keys(m.links ?? {}).length);
}

export function Blueprint(): JSX.Element {
  const store = useStore();
  const s = () => store.state;
  const params = useParams();
  // @solidjs/router ≥0.16 types params as string | undefined; the /bp/:name route always
  // provides it, and the unknown-blueprint guard handles the impossible empty case.
  const name = () => params.name ?? "";

  // The rich Schema only when the operator schema is wired; otherwise undefined.
  const schema = (): Schema | undefined => {
    const sc = s().schema as Schema | undefined;
    return sc && !Array.isArray(sc) ? sc : undefined;
  };
  const state = (): State | undefined => s().state;

  // ── derived (null-guarded) selectors ────────────────────────────────────────
  const disabledSet = () => new Set(state()?.disabled_blueprints ?? []);
  const disabledConstructSet = () => new Set(state()?.disabled_constructs ?? []);
  const disabledKindSet = () => new Set(state()?.disabled_kinds ?? []);
  const isDisabled = () => disabledSet().has(name());

  const inv = (): BlueprintInventory | undefined =>
    s().inventory?.blueprints?.find((b) => b.blueprint === name());
  const hlt = (): BlueprintHealth | undefined =>
    s().health?.blueprints?.find((b) => b.blueprint === name());
  const meta = (): BlueprintMetaInfo | undefined =>
    schema()?.blueprint_meta?.find((b) => b.name === name());

  const scalables = (): TargetInfo[] =>
    (schema()?.targets ?? []).filter((t) => t.scalable && t.blueprint === name());
  const constructs = (): ConstructInfo[] =>
    (schema()?.constructs ?? []).filter((x) => x.blueprint === name());

  // effective live count: explicit override wins, else schema's current.
  const liveCount = (t: TargetInfo): number => {
    const id = targetId(t);
    const sc = state()?.scaling;
    return sc && id in sc ? sc[id] : (t.scalable?.current ?? 0);
  };

  // A blueprint is "known" if it shows up in the schema list, the inventory, or state's
  // disabled list (a disabled bp still keeps its constructs in the inventory report).
  const known = (): boolean => {
    const n = name();
    return (
      (schema()?.blueprints ?? []).includes(n) ||
      !!inv() ||
      !!hlt() ||
      disabledSet().has(n)
    );
  };

  // ── action error (mutation failures surface here, not silently swallowed) ────
  const [actionErr, setActionErr] = createSignal<string>();
  const runMutation = (p: Promise<unknown>) =>
    void p
      .then(() => { setActionErr(undefined); return store.refresh(); })
      .catch((e: unknown) => setActionErr(e instanceof ApiError ? e.message : String(e)));

  // ── mutations (each replaces the FULL list exactly as legacy renderBlueprint did) ──
  function toggleBlueprint() {
    const dis = disabledSet();
    dis.has(name()) ? dis.delete(name()) : dis.add(name());
    runMutation(postJSON("blueprints", { disabled_blueprints: [...dis] }));
  }
  function commitScaling(t: TargetInfo, count: number) {
    const sc = t.scalable;
    const cv = sc ? clamp(Math.round(count), sc.min, sc.max) : Math.round(count);
    runMutation(postJSON("scaling", { [targetId(t)]: cv }));
  }
  function toggleConstruct(x: ConstructInfo) {
    const dis = disabledConstructSet();
    const key = cstKey(x);
    dis.has(key) ? dis.delete(key) : dis.add(key);
    runMutation(postJSON("constructs", { disabled_constructs: [...dis] }));
  }

  // ── lazy YAML source (text/plain, NOT JSON) — fetched on first <details> open ──
  const [src, setSrc] = createSignal<string>("loading…");
  let srcLoaded = false;
  function onYamlToggle(e: Event) {
    const d = e.currentTarget as HTMLDetailsElement;
    if (!d.open || srcLoaded) return;
    srcLoaded = true;
    getText("blueprint?blueprint=" + encodeURIComponent(name()))
      .then((t) => setSrc(t))
      .catch((err: unknown) =>
        setSrc("Failed to load source: " + (err instanceof ApiError ? err.message : String(err))));
  }

  const loading = () => s().loading;
  // Error if the controlling reads are unavailable (cannot render the view at all).
  const errKey = (): string | undefined => s().errors["state"] || s().errors["inventory"];

  return (
    <section>
      <style>{VIEW_CSS}</style>

      <Show
        when={!loading()}
        fallback={
          <div class="bp-loading" data-testid="bp-loading" role="status">
            <span class="bp-spinner" />
            Loading blueprint…
          </div>
        }
      >
        <Show
          when={!errKey()}
          fallback={
            <div class="bp-fetch-err" data-testid="bp-error" role="alert">
              Failed to load blueprint: {errKey()}
            </div>
          }
        >
          <Show
            when={known()}
            fallback={
              <div class="bp-unknown" data-testid="bp-unknown">
                <h1>Unknown blueprint</h1>
                <p>
                  No blueprint named <code>{name()}</code> is loaded. It may have been removed,
                  disabled at boot, or the name is wrong.
                </p>
              </div>
            }
          >
            {/* ── header + enable/disable toggle ──────────────────────────── */}
            <div class="pane-head">
              <h1>{name()}</h1>
              <span class="sub">
                {scalables().length} scalable service{scalables().length === 1 ? "" : "s"} ·{" "}
                {constructs().length} construct{constructs().length === 1 ? "" : "s"}
              </span>
              <ConfirmButton
                class={"bp-hd-toggle" + (isDisabled() ? " off" : "")}
                label={isDisabled() ? "Disabled" : "Enabled"}
                confirmLabel={isDisabled() ? "Enable" : "Disable"}
                message={
                  (isDisabled() ? "Enable" : "Disable") +
                  ` blueprint "${name()}"? ` +
                  (isDisabled()
                    ? "Its instances resume ticking on the next tick."
                    : "Its instances stop ticking (counters resume from state when re-enabled).")
                }
                onConfirm={() => toggleBlueprint()}
              />
            </div>

            {/* action error banner (mutation .catch; cleared on next successful action) */}
            <ActionError
              message={actionErr}
              testid="bp-action-error"
              onDismiss={() => setActionErr(undefined)}
            />

            {/* disabled warning banner */}
            <Show when={isDisabled()}>
              <div class="bp-banner warn" data-testid="bp-disabled-banner">
                <span>⚠ This blueprint is disabled — not currently emitting telemetry.</span>
                <button type="button" class="bp-banner-act" onClick={() => toggleBlueprint()}>
                  Re-enable
                </button>
              </div>
            </Show>

            {/* ── human-facing metadata ───────────────────────────────────── */}
            <Show when={meta()}>{(m) => <BpMeta m={m()} />}</Show>

            {/* ── inventory + health summary ──────────────────────────────── */}
            <Show when={inv() || hlt()}>
              <div class="bp-summary" data-testid="bp-summary">
                <div class="bp-inv-row">
                  <Show when={inv()}>
                    {(ib) => (
                      <>
                        <Stat val={fmtNum(ib().distinct_series ?? 0)} lbl="series" />
                        <Stat val={fmtNum((ib().constructs ?? []).length)} lbl="constructs" />
                        <Stat val={fmtNum(ib().metric_names ?? 0)} lbl="metric names" />
                        <Stat val={fmtNum(ib().label_keys ?? 0)} lbl="label keys" />
                      </>
                    )}
                  </Show>
                  <Show when={hlt()}>
                    {(hb) => (
                      <>
                        <Stat val={fmtNum(hb().cycles ?? 0)} lbl="cycles" />
                        <Stat val={hb().last_cycle_ms ? hb().last_cycle_ms.toFixed(1) + " ms" : "—"} lbl="last cycle" />
                      </>
                    )}
                  </Show>
                </div>
                <Show when={(hlt()?.dropped_ticks ?? 0) > 0}>
                  <div class="bp-inv-drop">
                    ⚠ {hlt()!.dropped_ticks} dropped tick{hlt()!.dropped_ticks === 1 ? "" : "s"}
                  </div>
                </Show>
              </div>
            </Show>

            <div class={isDisabled() ? "bp-dimmed" : ""}>
              {/* ── live service scaling ───────────────────────────────────── */}
              <section class="sec">
                <div class="sec-label">
                  Live service scaling
                  <Show when={scalables().length}>
                    <span class="meta">node count cascades automatically</span>
                  </Show>
                </div>
                <div class="panel">
                  <Show
                    when={scalables().length > 0}
                    fallback={
                      <div class="empty" data-testid="bp-scale-empty">
                        No live-scalable workloads in this blueprint.
                      </div>
                    }
                  >
                    <h3>Pod counts</h3>
                    <p class="gh">
                      Set the live replica count of scalable workloads, bounded per target. The
                      backend cascades node count.
                    </p>
                    <For each={scalables()}>
                      {(t) => {
                        const sc = t.scalable!;
                        const cur = () => liveCount(t);
                        return (
                          <div class="scl" data-testid={`bp-scale-${targetId(t)}`}>
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
                  </Show>
                </div>
              </section>

              {/* ── constructs (per-instance enable/disable) ───────────────── */}
              <section class="sec">
                <div class="sec-label">
                  Constructs<span class="meta">{constructs().length} in this blueprint</span>
                </div>
                <div class="panel">
                  <Show
                    when={constructs().length > 0}
                    fallback={
                      <div class="empty" data-testid="bp-construct-empty">
                        This blueprint built no constructs.
                      </div>
                    }
                  >
                    <h3>Construct instances</h3>
                    <p class="gh">
                      Disable an individual construct instance (counters persist; re-enable
                      resumes). A greyed instance is disabled globally via its kind — toggle that
                      under Global controls.
                    </p>
                    <div class="cstlist">
                      <For each={constructs()}>
                        {(x) => {
                          const off = () => disabledConstructSet().has(cstKey(x));
                          const kindOff = () => disabledKindSet().has(x.kind);
                          return (
                            <button
                              type="button"
                              class={"bpchip" + (off() || kindOff() ? " off" : "")}
                              disabled={kindOff()}
                              title={kindOff() ? "disabled globally via kind: " + x.kind : x.kind + " · " + x.name}
                              data-testid={`bp-construct-${cstKey(x)}`}
                              onClick={() => toggleConstruct(x)}
                            >
                              <span class="st" />
                              {x.kind} · {x.name}
                            </button>
                          );
                        }}
                      </For>
                    </div>
                  </Show>
                </div>
              </section>

              {/* ── blueprint source (resolved constructs + lazy raw YAML) ──── */}
              <section class="sec">
                <div class="sec-label">
                  Blueprint<span class="meta">what it built · what was declared</span>
                </div>
                <div class="panel">
                  <h3>Resolved constructs</h3>
                  <p class="gh">
                    The constructs this blueprint's declarations fanned into (kind · instance name).
                  </p>
                  <Show
                    when={constructs().length > 0}
                    fallback={<div class="empty">No constructs.</div>}
                  >
                    <div class="reslist">
                      <For each={constructs()}>
                        {(x) => (
                          <div>
                            <span class="k">{x.kind}</span> {x.name}
                          </div>
                        )}
                      </For>
                    </div>
                  </Show>
                  <details class="srcwrap" data-testid="bp-yaml" onToggle={onYamlToggle}>
                    <summary>View blueprint YAML source</summary>
                    <pre class="srcpre" data-testid="bp-yaml-pre">{src()}</pre>
                  </details>
                </div>
              </section>
            </div>
          </Show>
        </Show>
      </Show>
    </section>
  );
}

// Inventory/health stat cell.
function Stat(props: { val: string; lbl: string }): JSX.Element {
  return (
    <div class="bp-inv-stat">
      <div class="iv">{props.val}</div>
      <div class="il">{props.lbl}</div>
    </div>
  );
}

// AttrChips renders the category/owner/tags/links chips for a meta block (ported from the
// legacy attrChips helper). Shared by the blueprint-level and per-env metadata rows.
function AttrChips(props: { m: MetaFields }): JSX.Element {
  const links = createMemo(() => Object.entries(props.m.links ?? {}));
  return (
    <>
      <Show when={props.m.category}>
        <span class="tag cat">{props.m.category}</span>
      </Show>
      <Show when={props.m.owner}>
        <span class="tag owner">owner: {props.m.owner}</span>
      </Show>
      <For each={props.m.tags ?? []}>{(t) => <span class="tag">{t}</span>}</For>
      <For each={links()}>
        {([n, url]) => (
          <a href={url} target="_blank" rel="noopener">
            {n} ↗
          </a>
        )}
      </For>
    </>
  );
}

// BpMeta renders the description + attr chips + per-env metadata (ported from renderBpMeta).
function BpMeta(props: { m: BlueprintMetaInfo }): JSX.Element {
  const envs = createMemo(() => (props.m.environments ?? []).filter((e) => metaHasContent(e)));
  const hasContent = createMemo(() => metaHasContent(props.m) || envs().length > 0);
  return (
    <Show when={hasContent()}>
      <div class="bpmeta">
        <Show when={props.m.description}>
          <div class="desc">{props.m.description}</div>
        </Show>
        <Show when={(props.m.tags?.length ?? 0) || props.m.owner || props.m.category || Object.keys(props.m.links ?? {}).length}>
          <div class="attrs">
            <AttrChips m={props.m} />
          </div>
        </Show>
        <Show when={envs().length > 0}>
          <div class="envs">
            <For each={envs()}>
              {(e) => (
                <div class="env">
                  <b>{e.name}</b>
                  <Show when={e.description}>
                    <span class="ed">{e.description}</span>
                  </Show>
                  <Show when={(e.tags?.length ?? 0) || e.owner || e.category || Object.keys(e.links ?? {}).length}>
                    <div class="attrs">
                      <AttrChips m={e} />
                    </div>
                  </Show>
                </div>
              )}
            </For>
          </div>
        </Show>
      </div>
    </Show>
  );
}

const VIEW_CSS = `
.bp-loading { display:flex; align-items:center; gap:10px; color:var(--dim); font-size:13px;
  padding:18px; border:1px dashed var(--bd2); border-radius:12px; background:var(--panel2); }
.bp-spinner { width:14px; height:14px; border-radius:50%; border:2px solid var(--bd2);
  border-top-color:var(--acc); animation:bp-spin .7s linear infinite; flex:none; }
@keyframes bp-spin { to { transform:rotate(360deg); } }

.bp-fetch-err { font-size:13px; font-weight:600; color:var(--err);
  background:var(--crit); border:1px solid var(--critbd); border-radius:12px;
  padding:14px 18px; text-align:center; }
.bp-unknown { padding:24px; border:1px dashed var(--bd2); border-radius:12px; background:var(--panel2); }
.bp-unknown h1 { font:800 20px system-ui; color:var(--tx); margin:0 0 8px; }
.bp-unknown p { font-size:13px; color:var(--dim); line-height:1.5; margin:0; }
.bp-unknown code { font:12.5px var(--mono); color:var(--acc); }

.pane-head .bp-hd-toggle { margin-left:auto; align-self:center; display:inline-flex; align-items:center;
  gap:7px; font:600 12.5px system-ui; padding:6px 12px; border:1px solid var(--okbd); border-radius:8px;
  background:var(--okbg); color:var(--ok); cursor:pointer; transition:filter .12s; }
.pane-head .bp-hd-toggle:hover { filter:brightness(1.06); }
.pane-head .bp-hd-toggle.off { border-color:var(--bd); background:var(--panel2); color:var(--dim); }

.bp-banner { display:flex; align-items:center; gap:12px; border-radius:10px; padding:10px 16px;
  font-size:13px; font-weight:600; margin:0 0 18px; }
.bp-banner.warn { background:var(--warnbg); border:1px solid var(--warnbd); color:var(--warn); }
.bp-banner-act { margin-left:auto; font:600 12px system-ui; padding:4px 12px; border:1px solid var(--warnbd);
  border-radius:8px; background:transparent; color:var(--warn); cursor:pointer; }
.bp-banner-act:hover { filter:brightness(1.1); }

.bpmeta { background:var(--panel2); border:1px solid var(--bd); border-radius:12px;
  padding:14px 16px; margin:0 0 18px; }
.bpmeta .desc { font-size:13px; color:var(--tx); line-height:1.55; margin-bottom:10px; }
.bpmeta .attrs { display:flex; flex-wrap:wrap; align-items:center; gap:7px; }
.bpmeta .tag { font:600 11px system-ui; padding:3px 9px; border-radius:20px; border:1px solid var(--bd);
  background:var(--panel); color:var(--dim); }
.bpmeta .tag.cat { border-color:var(--accbd); background:var(--accbg); color:var(--acc); }
.bpmeta .tag.owner { color:var(--tx); }
.bpmeta .attrs a { font:600 11px system-ui; color:var(--acc); text-decoration:none; }
.bpmeta .attrs a:hover { text-decoration:underline; }
.bpmeta .envs { margin-top:12px; display:flex; flex-direction:column; gap:10px;
  border-top:1px solid var(--bd2); padding-top:12px; }
.bpmeta .env { display:flex; flex-wrap:wrap; align-items:center; gap:8px; }
.bpmeta .env b { font:700 12.5px system-ui; color:var(--tx); }
.bpmeta .env .ed { font-size:12px; color:var(--dim); }

.bp-summary { margin:0 0 22px; }
.bp-inv-row { display:flex; flex-wrap:wrap; gap:22px; padding:14px 18px; background:var(--card-grad);
  border:1px solid var(--bd); border-radius:12px; box-shadow:var(--card-shadow); }
.bp-inv-stat .iv { font:700 19px system-ui; color:var(--acc); letter-spacing:-.5px; }
.bp-inv-stat .il { font:11px system-ui; color:var(--dim); }
.bp-inv-drop { margin-top:8px; font-size:12.5px; font-weight:600; color:var(--warn); }

.bp-dimmed { opacity:.55; }

.sec { margin-bottom:26px; }
.sec-label { display:flex; align-items:baseline; gap:8px; font:700 11px system-ui; letter-spacing:.8px;
  text-transform:uppercase; color:var(--dim); margin:0 0 10px; }
.sec-label .meta { font-weight:500; text-transform:none; letter-spacing:0; font-size:11.5px; }

.panel { background:var(--panel2); border:1px solid var(--bd); border-radius:12px; padding:16px 18px; }
.panel h3 { font:700 14px system-ui; color:var(--tx); margin:0 0 6px; }
.panel .gh { font-size:12.5px; color:var(--dim); margin:0 0 14px; line-height:1.5; }

.empty { color:var(--dim); font-size:13px; padding:14px; text-align:center;
  border:1px dashed var(--bd2); border-radius:10px; background:var(--panel); }

.scl { display:flex; align-items:center; gap:14px; padding:8px 0; }
.scl:not(:last-child) { border-bottom:1px solid var(--bd2); }
.scl .nm { display:flex; flex-direction:column; min-width:180px; }
.scl .nm b { font:600 13px system-ui; color:var(--tx); }
.scl .nm small { font:11.5px var(--mono); color:var(--dim); }
.scl .vrange { flex:1; accent-color:var(--acc); cursor:pointer; }
.scl .ct { font:700 16px system-ui; color:var(--tx); min-width:36px; text-align:right; }
.scl .ct.chg { color:var(--acc); }

.cstlist { display:flex; flex-wrap:wrap; gap:8px; }
.bpchip { display:inline-flex; align-items:center; gap:7px; font:600 12.5px system-ui;
  padding:6px 12px; border:1px solid var(--okbd); border-radius:8px; background:var(--okbg);
  color:var(--ok); cursor:pointer; transition:all .12s; }
.bpchip:hover:not(:disabled) { filter:brightness(1.06); }
.bpchip:disabled { cursor:default; opacity:.7; }
.bpchip .st { width:8px; height:8px; border-radius:50%; background:var(--ok); flex:none; }
.bpchip.off { border-color:var(--bd); background:var(--panel); color:var(--dim); }
.bpchip.off .st { background:var(--bd2); }

.reslist { display:flex; flex-direction:column; gap:4px; margin-bottom:14px; }
.reslist div { font:12.5px var(--mono); color:var(--tx); }
.reslist .k { color:var(--acc); font-weight:700; }

.srcwrap { margin-top:6px; border-top:1px solid var(--bd2); padding-top:10px; }
.srcwrap summary { font:600 12.5px system-ui; color:var(--acc); cursor:pointer; user-select:none; }
.srcwrap summary:hover { text-decoration:underline; }
.srcpre { margin-top:10px; max-height:420px; overflow:auto; font:12px var(--mono); color:var(--tx);
  background:var(--panel); border:1px solid var(--bd); border-radius:8px; padding:12px 14px;
  white-space:pre; }
`;
