import { createMemo, createSignal, For, Show, type JSX } from "solid-js";
import { useStore } from "../store/store";
import { postJSON, delJSON, ApiError } from "../api/client";
import { ConfirmButton } from "../shell/ConfirmDialog";
import { ActionError } from "../shell/ActionError";
import type {
  FailureSetting,
  IncidentInfo,
  ModeInfo,
  ScenarioInfo,
  Schema,
  State,
  TargetInfo,
} from "../api/types";

// Ports internal/control/ui.html renderIncidents (the largest legacy view): two tabs
// (on-demand failure/scenario injection · scheduled-incident timeline + form), persisting
// the tab choice to localStorage["synthkit-incidents-tab"]. Reads s().schema (rich operator
// schema, null-guarded), s().state (failures/active_scenarios) and s().incidents (the merged
// declared+runtime IncidentInfo[]). Mutations follow the canonical postJSON/delJSON →
// .then(refresh) .catch(setActionErr) pattern; destructive ones (inject, delete) gate behind
// <ConfirmButton>. The /control/incidents route 404s when no incident source is wired → the
// store records errors["incidents"] and leaves the field undefined → a graceful "unavailable"
// node (NOT the fatal error), exactly as the legacy view degraded.

const TAB_KEY = "synthkit-incidents-tab";
type Tab = "ondemand" | "scheduled";

// ── client-side schedule_spec parser (timeline layout ONLY, not grammar authority) ──
// spec form: "mode@at/dur[#intensity][@scope]". Returns the geometry the 24h track needs.
type Parsed =
  | { type: "daily"; startMin: number; durMin: number; at: string; forStr: string }
  | { type: "interval"; periodMin: number; durMin: number; at: string; forStr: string }
  | { type: "absolute"; isoDate: string; durMin: number; at: string; forStr: string }
  | { type: "unknown"; at?: string; forStr?: string };

function parseDur(s: string): number {
  let m = s.match(/^(\d+(?:\.\d+)?)m$/i);
  if (m) return parseFloat(m[1]);
  m = s.match(/^(\d+(?:\.\d+)?)h(\d+(?:\.\d+)?)m(\d+(?:\.\d+)?)s$/i);
  if (m) return parseFloat(m[1]) * 60 + parseFloat(m[2]) + parseFloat(m[3]) / 60;
  m = s.match(/^(\d+(?:\.\d+)?)h(?:(\d+(?:\.\d+)?)m)?$/i);
  if (m) return parseFloat(m[1]) * 60 + (m[2] ? parseFloat(m[2]) : 0);
  m = s.match(/^(\d+(?:\.\d+)?)s$/i);
  if (m) return parseFloat(m[1]) / 60;
  return 30; // fallback
}

function parseSpec(spec: string): Parsed {
  const atIdx = spec.indexOf("@");
  if (atIdx < 0) return { type: "unknown" };
  const rest = spec.slice(atIdx + 1);
  const slashIdx = rest.lastIndexOf("/");
  if (slashIdx < 0) return { type: "unknown" };
  const at = rest.slice(0, slashIdx).trim();
  let durPart = rest.slice(slashIdx + 1);
  const hashIdx = durPart.indexOf("#");
  if (hashIdx >= 0) durPart = durPart.slice(0, hashIdx);
  const atScopeIdx = durPart.indexOf("@");
  if (atScopeIdx >= 0) durPart = durPart.slice(0, atScopeIdx);
  durPart = durPart.trim();

  const evM = at.match(/^every(\d+)m$/i);
  const evH = at.match(/^every(\d+)h$/i);
  if (evM || evH) {
    const periodMin = evM ? parseInt(evM[1]) : parseInt(evH![1]) * 60;
    return { type: "interval", periodMin, durMin: parseDur(durPart), at, forStr: durPart };
  }
  const absM = at.match(/^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2})/);
  if (absM) return { type: "absolute", isoDate: at, durMin: parseDur(durPart), at, forStr: durPart };
  const hmM = at.match(/^(\d{2}):(\d{2})(?::\d{2})?$/);
  if (hmM) {
    const startMin = parseInt(hmM[1]) * 60 + parseInt(hmM[2]);
    return { type: "daily", startMin, durMin: parseDur(durPart), at, forStr: durPart };
  }
  return { type: "unknown", at, forStr: durPart };
}

const fmtInt = (n: number): string => Number(n).toFixed(2);
const scnId = (s: ScenarioInfo): string => s.blueprint + "/" + s.name;

export function Incidents(): JSX.Element {
  const store = useStore();
  const s = () => store.state;

  // Rich operator schema only (bare Descriptor[] arrives as an array — treat as absent).
  const schema = (): Schema | undefined => {
    const sc = s().schema as Schema | undefined;
    return sc && !Array.isArray(sc) ? sc : undefined;
  };
  const state = (): State | undefined => s().state;

  const blueprints = (): string[] => schema()?.blueprints ?? [];
  const modes = (): ModeInfo[] => schema()?.modes ?? [];
  const targets = (): TargetInfo[] => schema()?.targets ?? [];
  const scenarios = (): ScenarioInfo[] => schema()?.scenarios ?? [];
  const disabledSet = () => new Set(state()?.disabled_blueprints ?? []);
  const activeScnSet = () => new Set(state()?.active_scenarios ?? []);
  const scenariosFor = (bp: string): ScenarioInfo[] => scenarios().filter((x) => x.blueprint === bp);
  const failures = (): Record<string, FailureSetting> => state()?.failures ?? {};
  const incidents = (): IncidentInfo[] | undefined => s().incidents;
  const incidentsUnavailable = () => incidents() === undefined && !!s().errors["incidents"];

  // ── tab state (persisted) ──────────────────────────────────────────────────
  // localStorage can be absent (SSR/jsdom) or throw (private mode, blocked storage);
  // persist best-effort, matching the defensive pattern in shell/ThemeToggle.tsx.
  const initialTab = (): Tab => (globalThis.localStorage?.getItem(TAB_KEY) === "scheduled" ? "scheduled" : "ondemand");
  const [tab, setTabSig] = createSignal<Tab>(initialTab());
  const setTab = (t: Tab) => {
    try {
      globalThis.localStorage?.setItem(TAB_KEY, t);
    } catch {
      /* ignore */
    }
    setTabSig(t);
  };

  // ── action error (mutation failures surface here, not silently swallowed) ────
  const [actionErr, setActionErr] = createSignal<string>();
  const runMutation = (p: Promise<unknown>): Promise<boolean> =>
    p
      .then(() => { setActionErr(undefined); return store.refresh().then(() => true); })
      .catch((e: unknown) => { setActionErr(e instanceof ApiError ? e.message : String(e)); return false; });

  // ── mutations (bodies verbatim from renderIncidents + the http.go handlers) ──
  // failures: {"<mode>":{enabled,intensity,scope}} — MERGE (one mode at a time here).
  const injectFailure = (mode: string, intensity: number, scope: string) =>
    void runMutation(postJSON("failures", { [mode]: { enabled: true, intensity, scope } }));
  const setFailureEnabled = (mode: string, f: FailureSetting, enabled: boolean) =>
    void runMutation(postJSON("failures", { [mode]: { enabled, intensity: f.intensity, scope: f.scope || "" } }));
  // scenarios: {"active_scenarios":[...]} — REPLACE.
  const toggleScenario = (sc: ScenarioInfo, activate: boolean) => {
    const set = activeScnSet();
    const id = scnId(sc);
    activate ? set.add(id) : set.delete(id);
    void runMutation(postJSON("scenarios", { active_scenarios: [...set] }));
  };
  // incidents/<id> — DELETE one runtime incident.
  const deleteIncident = (id: string) => void runMutation(delJSON("incidents/" + encodeURIComponent(id)));

  const loading = () => s().loading;
  // Fatal error only when the schema itself failed (controls cannot render). The incidents
  // 404 is NOT fatal — it degrades to the graceful "unavailable" node in the scheduled tab.
  const errKey = (): string | undefined => s().errors["schema"];

  return (
    <section>
      <style>{VIEW_CSS}</style>
      <div class="pane-head">
        <h1>Incidents</h1>
        <span class="sub">inject failures &amp; scenarios on demand, or schedule incidents over time</span>
      </div>

      <Show
        when={!loading()}
        fallback={
          <div class="i-loading" data-testid="incidents-loading" role="status">
            <span class="i-spinner" />
            Loading incidents…
          </div>
        }
      >
        <Show
          when={!errKey()}
          fallback={
            <div class="i-fetch-err" data-testid="incidents-error" role="alert">
              Failed to load controls: {errKey()}
            </div>
          }
        >
          {/* action error banner (mutation .catch; cleared on next successful action) */}
          <ActionError
            message={actionErr}
            testid="incidents-action-error"
            onDismiss={() => setActionErr(undefined)}
          />

          {/* tabbar */}
          <div class="tabbar">
            <button
              class={tab() === "ondemand" ? "tab active" : "tab"}
              data-testid="incidents-tab-ondemand"
              onClick={() => setTab("ondemand")}
            >
              On-demand
            </button>
            <button
              class={tab() === "scheduled" ? "tab active" : "tab"}
              data-testid="incidents-tab-scheduled"
              onClick={() => setTab("scheduled")}
            >
              Scheduled
            </button>
          </div>

          <Show when={tab() === "ondemand"}>
            <div class="tabbody" data-testid="incidents-panel-ondemand">
              <OnDemand
                blueprints={blueprints()}
                disabledSet={disabledSet()}
                modes={modes()}
                targets={targets()}
                failures={failures()}
                scenariosFor={scenariosFor}
                activeScnSet={activeScnSet()}
                injectFailure={injectFailure}
                setFailureEnabled={setFailureEnabled}
                toggleScenario={toggleScenario}
              />
            </div>
          </Show>

          <Show when={tab() === "scheduled"}>
            <div class="tabbody" data-testid="incidents-panel-scheduled">
              <Scheduled
                blueprints={blueprints()}
                modes={modes()}
                targets={targets()}
                incidents={incidents()}
                unavailable={incidentsUnavailable()}
                deleteIncident={deleteIncident}
                onSchedule={(body) => runMutation(postJSON("incidents", body))}
              />
            </div>
          </Show>
        </Show>
      </Show>
    </section>
  );
}

// ── target/scope option helpers (mirror legacy rebuildTargetSel{Global,Bp}) ─────
function targetsByAxis(targets: TargetInfo[], axis: string): TargetInfo[] {
  const seen = new Set<string>();
  const out: TargetInfo[] = [];
  for (const t of targets) if (t.axis === axis && !seen.has(t.name)) { seen.add(t.name); out.push(t); }
  return out;
}

// ════════════════════════════════════════════════════════════════════════════
// ON-DEMAND tab
// ════════════════════════════════════════════════════════════════════════════
interface OnDemandProps {
  blueprints: string[];
  disabledSet: Set<string>;
  modes: ModeInfo[];
  targets: TargetInfo[];
  failures: Record<string, FailureSetting>;
  scenariosFor: (bp: string) => ScenarioInfo[];
  activeScnSet: Set<string>;
  injectFailure: (mode: string, intensity: number, scope: string) => void;
  setFailureEnabled: (mode: string, f: FailureSetting, enabled: boolean) => void;
  toggleScenario: (sc: ScenarioInfo, activate: boolean) => void;
}

function OnDemand(props: OnDemandProps): JSX.Element {
  const activeBps = (): string[] => props.blueprints.filter((b) => !props.disabledSet.has(b));
  const disabledBps = (): string[] => props.blueprints.filter((b) => props.disabledSet.has(b));

  return (
    <>
      {/* ── global un-scoped failure injection ──────────────────────────────── */}
      <section class="sec">
        <div class="sec-label">
          Global failure injection<span class="meta">fires across all blueprints</span>
        </div>
        <div class="panel">
          <h3>Inject an un-scoped failure mode</h3>
          <p class="gh">Un-scoped failures apply to all instances of the target axis across every blueprint.</p>
          <Show
            when={props.modes.length > 0}
            fallback={<div class="empty" data-testid="incidents-global-nomodes">No failure modes in schema.</div>}
          >
            <FailureForm
              idPrefix="global"
              modes={props.modes}
              scopeOptions={(axis) => [
                { value: axis + ":*", label: "All " + axis + " (" + axis + ":*)" },
                { value: "", label: "All instances of this axis (empty scope)" },
                ...targetsByAxis(props.targets, axis).map((t) => ({ value: t.name, label: t.name })),
              ]}
              onInject={props.injectFailure}
            />
            {/* active un-scoped failures (scope empty) */}
            <UnscopedActiveFailures failures={props.failures} setFailureEnabled={props.setFailureEnabled} />
          </Show>
        </div>
      </section>

      {/* ── per-blueprint groups ────────────────────────────────────────────── */}
      <Show
        when={props.blueprints.length > 0}
        fallback={<div class="empty" data-testid="incidents-ondemand-empty">No blueprints loaded.</div>}
      >
        <For each={activeBps()}>
          {(bp) => (
            <BpGroup
              bp={bp}
              dimmed={false}
              modes={props.modes}
              targets={props.targets}
              failures={props.failures}
              scenarios={props.scenariosFor(bp)}
              activeScnSet={props.activeScnSet}
              injectFailure={props.injectFailure}
              setFailureEnabled={props.setFailureEnabled}
              toggleScenario={props.toggleScenario}
            />
          )}
        </For>
        <Show when={disabledBps().length > 0}>
          <details>
            <summary class="i-disclose">
              Show {disabledBps().length} disabled blueprint{disabledBps().length === 1 ? "" : "s"}
            </summary>
            <For each={disabledBps()}>
              {(bp) => (
                <BpGroup
                  bp={bp}
                  dimmed={true}
                  modes={props.modes}
                  targets={props.targets}
                  failures={props.failures}
                  scenarios={props.scenariosFor(bp)}
                  activeScnSet={props.activeScnSet}
                  injectFailure={props.injectFailure}
                  setFailureEnabled={props.setFailureEnabled}
                  toggleScenario={props.toggleScenario}
                />
              )}
            </For>
          </details>
        </Show>
      </Show>
    </>
  );
}

// active un-scoped failures (scope === "") with a disable/re-enable link each.
function UnscopedActiveFailures(props: {
  failures: Record<string, FailureSetting>;
  setFailureEnabled: (mode: string, f: FailureSetting, enabled: boolean) => void;
}): JSX.Element {
  const rows = (): [string, FailureSetting][] =>
    Object.entries(props.failures).filter(([, f]) => f && !f.scope);
  return (
    <Show when={rows().length > 0}>
      <div class="activef">
        <div class="sec-label" style="margin-top:4px">Active / known un-scoped modes</div>
        <For each={rows()}>
          {([mode, f]) => (
            <div class={"fitem" + (f.enabled ? "" : " disabled")}>
              <span>{mode + " · (all) · " + fmtInt(f.intensity) + (f.enabled ? "" : " · disabled")}</span>
              <span class="sp">
                <a
                  data-testid={"incidents-fdisable-" + mode}
                  onClick={() => props.setFailureEnabled(mode, f, !f.enabled)}
                >
                  {f.enabled ? "disable" : "re-enable"}
                </a>
              </span>
            </div>
          )}
        </For>
      </div>
    </Show>
  );
}

// ── per-blueprint group: scenarios + scoped failure injection + active list ────
interface BpGroupProps {
  bp: string;
  dimmed: boolean;
  modes: ModeInfo[];
  targets: TargetInfo[];
  failures: Record<string, FailureSetting>;
  scenarios: ScenarioInfo[];
  activeScnSet: Set<string>;
  injectFailure: (mode: string, intensity: number, scope: string) => void;
  setFailureEnabled: (mode: string, f: FailureSetting, enabled: boolean) => void;
  toggleScenario: (sc: ScenarioInfo, activate: boolean) => void;
}

function BpGroup(props: BpGroupProps): JSX.Element {
  const bpTargetNames = (): Set<string> =>
    new Set(props.targets.filter((t) => t.blueprint === props.bp).map((t) => t.name));
  // failures scoped to one of this blueprint's targets.
  const scopedFails = (): [string, FailureSetting][] =>
    Object.entries(props.failures).filter(([, f]) => f && bpTargetNames().has(f.scope));

  return (
    <section class="sec">
      <div class="sec-label">
        {props.bp}
        <span class="meta">
          {props.dimmed ? "disabled" : props.scenarios.length + " scenario" + (props.scenarios.length === 1 ? "" : "s")}
        </span>
      </div>
      <div class={"panel" + (props.dimmed ? " dimmed" : "")}>
        {/* scenarios */}
        <Show
          when={props.scenarios.length > 0}
          fallback={<div class="empty">No incident scenarios for this blueprint.</div>}
        >
          <p class="gh" style="margin-bottom:10px">Curated cross-construct bundles — click to fire live.</p>
          <div class="pgrid">
            <For each={props.scenarios}>
              {(sc) => {
                const active = () => props.activeScnSet.has(scnId(sc));
                return (
                  <div class={"pc" + (active() ? " on" : "")}>
                    <div class="hd">
                      <span class="ti">{sc.title || sc.name}</span>
                      <Show when={active()}>
                        <span class="pc-badge"><span class="blip" />LIVE</span>
                      </Show>
                    </div>
                    <Show when={sc.summary}>
                      <div class="su">{sc.summary}</div>
                    </Show>
                    <div class="fx">
                      <For each={sc.effects ?? []}>
                        {(e) => (
                          <div class="row">
                            <span class="m">{e.mode}</span>
                            <span class="arr">→</span>
                            <span class="t" title={e.target}>{e.target || "(all)"}</span>
                            <span class="i">{"@" + fmtInt(e.intensity).replace(/0$/, "")}</span>
                          </div>
                        )}
                      </For>
                    </div>
                    <button
                      class="pc-btn"
                      data-testid={"incidents-scn-" + scnId(sc)}
                      onClick={() => props.toggleScenario(sc, !active())}
                    >
                      {active() ? "Deactivate" : "Activate"}
                    </button>
                  </div>
                );
              }}
            </For>
          </div>
        </Show>

        {/* scoped failure injection */}
        <Show when={props.modes.length > 0}>
          <div class="sec-label" style="margin-top:18px">Scoped failure injection</div>
          <FailureForm
            idPrefix={"bp-" + props.bp}
            modes={props.modes}
            scopeOptions={(axis) => [
              { value: "", label: "— blueprint-wide (all targets) —" },
              ...props.targets
                .filter((t) => t.blueprint === props.bp && t.axis === axis)
                .map((t) => ({ value: t.name, label: t.name })),
            ]}
            onInject={props.injectFailure}
          />
          <div class="i-hint">A failure mode can target one scope at a time; use a scenario for multi-target.</div>
          <Show when={scopedFails().length > 0}>
            <div class="activef">
              <div class="sec-label" style="margin-top:4px">Active failures here</div>
              <For each={scopedFails()}>
                {([mode, f]) => (
                  <div class={"fitem" + (f.enabled ? "" : " disabled")}>
                    <span>{mode + " · " + (f.scope || "(all)") + " · " + fmtInt(f.intensity) + (f.enabled ? "" : " · disabled")}</span>
                    <span class="sp">
                      <a onClick={() => props.setFailureEnabled(mode, f, !f.enabled)}>
                        {f.enabled ? "disable" : "re-enable"}
                      </a>
                    </span>
                  </div>
                )}
              </For>
            </div>
          </Show>
        </Show>
      </div>
    </section>
  );
}

// ── reusable failure-injection form (mode/scope/intensity + ConfirmButton inject) ──
interface ScopeOpt { value: string; label: string }
function FailureForm(props: {
  idPrefix: string;
  modes: ModeInfo[];
  scopeOptions: (axis: string) => ScopeOpt[];
  onInject: (mode: string, intensity: number, scope: string) => void;
}): JSX.Element {
  // group modes by axis for <optgroup>.
  const byAxis = createMemo((): [string, ModeInfo[]][] => {
    const m: Record<string, ModeInfo[]> = {};
    for (const md of props.modes) (m[md.axis] ||= []).push(md);
    return Object.entries(m);
  });
  const [mode, setMode] = createSignal(props.modes[0]?.name ?? "");
  const [intensity, setIntensity] = createSignal(0.5);
  const axisOf = (name: string): string => props.modes.find((m) => m.name === name)?.axis ?? "";
  const opts = (): ScopeOpt[] => props.scopeOptions(axisOf(mode()));
  const [scope, setScope] = createSignal(opts()[0]?.value ?? "");

  // keep scope valid when the mode (hence axis) changes.
  const onModeChange = (name: string) => {
    setMode(name);
    setScope(props.scopeOptions(axisOf(name))[0]?.value ?? "");
  };

  return (
    <div class="fail">
      <div>
        <label>Failure mode</label>
        <select
          data-testid={"incidents-" + props.idPrefix + "-mode"}
          onChange={(e) => onModeChange(e.currentTarget.value)}
        >
          <For each={byAxis()}>
            {([axis, ms]) => (
              <optgroup label={axis}>
                <For each={ms}>{(m) => <option value={m.name} title={m.help}>{m.name}</option>}</For>
              </optgroup>
            )}
          </For>
        </select>
      </div>
      <div>
        <label>Target / scope</label>
        <select
          data-testid={"incidents-" + props.idPrefix + "-scope"}
          value={scope()}
          onChange={(e) => setScope(e.currentTarget.value)}
        >
          <For each={opts()}>{(o) => <option value={o.value}>{o.label}</option>}</For>
        </select>
      </div>
      <div>
        <label>Intensity — {fmtInt(intensity())}</label>
        <div class="iwrap">
          <input
            type="range"
            min="0"
            max="1"
            step="0.05"
            value={intensity()}
            data-testid={"incidents-" + props.idPrefix + "-intensity"}
            onInput={(e) => setIntensity(Number(e.currentTarget.value))}
          />
        </div>
      </div>
      <div>
        <ConfirmButton
          class="go"
          label="Inject"
          confirmLabel="Inject failure"
          testid={"incidents-" + props.idPrefix + "-inject"}
          message={
            `Inject failure "${mode()}"` +
            (scope() ? ` scoped to "${scope()}"` : " (un-scoped)") +
            ` at intensity ${fmtInt(intensity())}? This affects live synthetic telemetry immediately.`
          }
          onConfirm={() => props.onInject(mode(), intensity(), scope())}
        />
      </div>
    </div>
  );
}

// ════════════════════════════════════════════════════════════════════════════
// SCHEDULED tab
// ════════════════════════════════════════════════════════════════════════════
interface ScheduleBody {
  blueprint: string;
  mode: string;
  target: string;
  at: string;
  for: string;
  intensity: number;
}
interface ScheduledProps {
  blueprints: string[];
  modes: ModeInfo[];
  targets: TargetInfo[];
  incidents: IncidentInfo[] | undefined;
  unavailable: boolean;
  deleteIncident: (id: string) => void;
  onSchedule: (body: ScheduleBody) => Promise<boolean>;
}

function Scheduled(props: ScheduledProps): JSX.Element {
  const byBp = createMemo((): [string, IncidentInfo[]][] => {
    const m: Record<string, IncidentInfo[]> = {};
    for (const inc of props.incidents ?? []) (m[inc.blueprint] ||= []).push(inc);
    return Object.entries(m);
  });
  const nowMin = new Date().getHours() * 60 + new Date().getMinutes();
  const hasIncidents = () => (props.incidents?.length ?? 0) > 0;

  return (
    <>
      {/* unavailable / empty states */}
      <Show when={props.unavailable}>
        <div class="i-unavail" data-testid="incidents-unavailable">
          incidents unavailable (server has no incident source configured)
        </div>
      </Show>
      <Show when={!props.unavailable && !hasIncidents()}>
        <div class="i-unavail" data-testid="incidents-scheduled-empty">
          No incidents declared or scheduled yet.
        </div>
      </Show>

      <Show when={hasIncidents()}>
        <For each={byBp()}>
          {([bp, bpIncs]) => {
            // track bands (declared daily/interval only — runtime daily/interval drop to the list).
            const bands = (): JSX.Element[] => {
              const out: JSX.Element[] = [];
              for (const inc of bpIncs) {
                const p = parseSpec(inc.schedule_spec);
                const source = inc.source || "declared";
                if (source === "runtime") continue;
                if (p.type === "daily") {
                  const startPct = (p.startMin / 1440) * 100;
                  const widthPct = Math.min((p.durMin / 1440) * 100, 100 - startPct);
                  out.push(
                    <div
                      class={"inc-band " + source + (inc.active_now ? " active" : "")}
                      style={`left:${startPct.toFixed(2)}%;width:${widthPct.toFixed(2)}%`}
                      title={`${inc.mode} · ${inc.target || "all"} · ${inc.at || p.at} for ${inc.for || p.forStr}${inc.active_now ? " · ACTIVE" : ""}`}
                    >
                      {(inc.active_now ? "● " : "") + inc.mode}
                    </div>,
                  );
                } else if (p.type === "interval") {
                  const durPct = (p.durMin / 1440) * 100;
                  const periodPct = (p.periodMin / 1440) * 100;
                  if (periodPct > 0) {
                    for (let start = 0; start < 100; start += periodPct) {
                      out.push(
                        <div
                          class={"inc-band " + source + (inc.active_now ? " active" : "")}
                          style={`left:${start.toFixed(2)}%;width:${Math.min(durPct, periodPct * 0.9).toFixed(2)}%`}
                          title={`${inc.mode} · every ${p.periodMin}m for ${p.forStr}`}
                        />,
                      );
                    }
                  }
                }
              }
              return out;
            };
            // dated/unknown rows (absolute + unknown spec types).
            const datedRows = (): IncidentInfo[] =>
              bpIncs.filter((i) => {
                const t = parseSpec(i.schedule_spec).type;
                return t !== "daily" && t !== "interval";
              });
            // runtime daily/interval rows (their authoritative delete lives here).
            const listRows = (): IncidentInfo[] =>
              bpIncs.filter((i) => {
                if ((i.source || "declared") !== "runtime") return false;
                const t = parseSpec(i.schedule_spec).type;
                return t === "daily" || t === "interval";
              });

            return (
              <section class="sec">
                <div class="sec-label">
                  {bp}<span class="meta">{bpIncs.length} incident{bpIncs.length === 1 ? "" : "s"}</span>
                </div>
                <div class="inc-track-wrap">
                  <div class="inc-track-label">24h daily window</div>
                  <div class="inc-track">
                    <div class="inc-now" style={`left:${((nowMin / 1440) * 100).toFixed(2)}%`} />
                    {bands()}
                  </div>
                </div>

                {/* dated / unknown rows */}
                <For each={datedRows()}>
                  {(inc) => {
                    const p = parseSpec(inc.schedule_spec);
                    const source = inc.source || "declared";
                    let timeLabel = inc.at || p.at || inc.schedule_spec;
                    let timeStatus = "";
                    if (p.type === "absolute" && p.isoDate) {
                      const d = new Date(p.isoDate).getTime();
                      const end = d + p.durMin * 60000;
                      const now = Date.now();
                      timeStatus = now < d ? "upcoming" : now > end ? "past" : "active";
                      timeLabel = p.isoDate + " for " + p.forStr;
                    }
                    return (
                      <div class={"inc-dated" + (inc.active_now ? " active-now" : "")}>
                        <span class={"inc-badge " + source}>{source}</span>
                        <Show when={inc.active_now}><span class="inc-dot">●</span></Show>
                        <span class="inc-spec">{inc.mode + " · " + (inc.target || "all") + " · " + timeLabel}</span>
                        <Show when={timeStatus}><span class="inc-status">{timeStatus}</span></Show>
                        <Show when={source === "runtime" && inc.id}>
                          <ConfirmButton
                            class="inc-del"
                            label="✕"
                            confirmLabel="Delete"
                            testid={"incidents-del-" + inc.id}
                            message={`Delete runtime incident "${inc.mode}" on ${bp}?`}
                            onConfirm={() => props.deleteIncident(inc.id)}
                          />
                        </Show>
                      </div>
                    );
                  }}
                </For>

                {/* runtime daily/interval rows */}
                <Show when={listRows().length > 0}>
                  <div class="inc-listwrap">
                    <For each={listRows()}>
                      {(inc) => {
                        const source = inc.source || "declared";
                        return (
                          <div class="inc-row">
                            <span class={"inc-badge " + source}>{source}</span>
                            <Show when={inc.active_now}><span class="inc-dot">●</span></Show>
                            <span class="inc-spec">{inc.mode + " · " + (inc.target || "all") + " · " + inc.schedule_spec}</span>
                            <Show when={inc.id}>
                              <ConfirmButton
                                class="inc-del"
                                label="✕"
                                confirmLabel="Delete"
                                testid={"incidents-del-" + inc.id}
                                message={`Delete runtime incident "${inc.mode}" on ${bp}?`}
                                onConfirm={() => props.deleteIncident(inc.id)}
                              />
                            </Show>
                          </div>
                        );
                      }}
                    </For>
                  </div>
                </Show>
              </section>
            );
          }}
        </For>
      </Show>

      {/* schedule form */}
      <div class="sec-label" style="margin:22px 0 10px">Schedule a runtime incident</div>
      <ScheduleForm
        blueprints={props.blueprints}
        modes={props.modes}
        targets={props.targets}
        onSchedule={props.onSchedule}
      />
    </>
  );
}

// ── schedule form: blueprint/mode/target + at/for/intensity (validate at+for) ──
function ScheduleForm(props: {
  blueprints: string[];
  modes: ModeInfo[];
  targets: TargetInfo[];
  onSchedule: (body: ScheduleBody) => Promise<boolean>;
}): JSX.Element {
  if (!props.blueprints.length || !props.modes.length) {
    return <div class="panel"><div class="empty">No blueprints or failure modes available.</div></div>;
  }

  const byAxis = createMemo((): [string, ModeInfo[]][] => {
    const m: Record<string, ModeInfo[]> = {};
    for (const md of props.modes) (m[md.axis] ||= []).push(md);
    return Object.entries(m);
  });

  type SchedType = "daily" | "absolute" | "interval";
  const [bp, setBp] = createSignal(props.blueprints[0]);
  const [mode, setMode] = createSignal(props.modes[0]?.name ?? "");
  const [schedType, setSchedType] = createSignal<SchedType>("daily");
  const [dailyAt, setDailyAt] = createSignal("12:00");
  const [absAt, setAbsAt] = createSignal("");
  const [intervalAt, setIntervalAt] = createSignal("30m");
  const [forDur, setForDur] = createSignal("30m");
  const [intensity, setIntensity] = createSignal(0.5);
  const [err, setErr] = createSignal("");

  const axisOf = (name: string): string => props.modes.find((m) => m.name === name)?.axis ?? "";
  // blueprint-scoped target options for the chosen mode's axis.
  const scopeOpts = (): ScopeOpt[] => [
    { value: "", label: "— blueprint-wide (all targets) —" },
    ...props.targets
      .filter((t) => t.blueprint === bp() && t.axis === axisOf(mode()))
      .map((t) => ({ value: t.name, label: t.name })),
  ];
  const [target, setTarget] = createSignal("");

  const buildAt = (): string => {
    if (schedType() === "daily") return dailyAt() || "00:00";
    if (schedType() === "absolute") return absAt() || "";
    const v = intervalAt().trim();
    return v.startsWith("every") ? v : "every" + v;
  };

  const submit = () => {
    setErr("");
    const at = buildAt();
    const dur = forDur().trim();
    if (!at || !dur) { setErr("at and for are required"); return; }
    props.onSchedule({ blueprint: bp(), mode: mode(), target: target(), at, for: dur, intensity: intensity() })
      .then((ok) => {
        if (ok) {
          setBp(props.blueprints[0]);
          setMode(props.modes[0]?.name ?? "");
          setSchedType("daily");
          setDailyAt("12:00");
          setAbsAt("");
          setIntervalAt("30m");
          setForDur("30m");
          setIntensity(0.5);
          setTarget(scopeOpts()[0]?.value ?? "");
        }
      });
  };

  return (
    <div class="panel">
      <div class="sched-grid2">
        <div>
          <label class="sched-lbl">Blueprint</label>
          <select data-testid="incidents-sched-blueprint" onChange={(e) => setBp(e.currentTarget.value)}>
            <For each={props.blueprints}>{(b) => <option value={b}>{b}</option>}</For>
          </select>
        </div>
        <div>
          <label class="sched-lbl">Failure mode</label>
          <select
            data-testid="incidents-sched-mode"
            onChange={(e) => { setMode(e.currentTarget.value); setTarget(scopeOpts()[0]?.value ?? ""); }}
          >
            <For each={byAxis()}>
              {([axis, ms]) => (
                <optgroup label={axis}>
                  <For each={ms}>{(m) => <option value={m.name} title={m.help}>{m.name}</option>}</For>
                </optgroup>
              )}
            </For>
          </select>
        </div>
      </div>

      <div style="margin-bottom:12px">
        <label class="sched-lbl">Target</label>
        <select
          data-testid="incidents-sched-target"
          value={target()}
          onChange={(e) => setTarget(e.currentTarget.value)}
        >
          <For each={scopeOpts()}>{(o) => <option value={o.value}>{o.label}</option>}</For>
        </select>
      </div>

      <div style="margin-bottom:12px">
        <label class="sched-lbl">Schedule</label>
        <div class="sched-row">
          <input type="radio" name="i-sched-type" checked={schedType() === "daily"} onChange={() => setSchedType("daily")} />
          <span>Daily HH:MM</span>
          <input
            type="time"
            class="sched-input"
            value={dailyAt()}
            data-testid="incidents-sched-at"
            onChange={(e) => setDailyAt(e.currentTarget.value)}
          />
        </div>
        <div class="sched-row">
          <input type="radio" name="i-sched-type" checked={schedType() === "absolute"} onChange={() => setSchedType("absolute")} />
          <span>Absolute date/time</span>
          <input
            type="datetime-local"
            class="sched-input"
            value={absAt()}
            data-testid="incidents-sched-abs"
            onChange={(e) => setAbsAt(e.currentTarget.value)}
          />
        </div>
        <div class="sched-row">
          <input type="radio" name="i-sched-type" checked={schedType() === "interval"} onChange={() => setSchedType("interval")} />
          <span>Interval (every…)</span>
          <input
            type="text"
            class="sched-input"
            placeholder="e.g. 30m or 2h"
            value={intervalAt()}
            data-testid="incidents-sched-interval"
            onChange={(e) => setIntervalAt(e.currentTarget.value)}
          />
        </div>
      </div>

      <div class="sched-durrow">
        <div>
          <label class="sched-lbl">Duration</label>
          <input
            type="text"
            class="sched-input"
            placeholder="30m"
            value={forDur()}
            data-testid="incidents-sched-for"
            onChange={(e) => setForDur(e.currentTarget.value)}
          />
        </div>
        <div>
          <label class="sched-lbl">Intensity — {fmtInt(intensity())}</label>
          <input
            type="range"
            min="0"
            max="1"
            step="0.05"
            value={intensity()}
            data-testid="incidents-sched-intensity"
            onInput={(e) => setIntensity(Number(e.currentTarget.value))}
          />
        </div>
      </div>

      <Show when={err()}>
        <div class="field-err" data-testid="incidents-sched-err">{err()}</div>
      </Show>
      <button class="go" data-testid="incidents-sched-submit" onClick={submit}>+ Schedule incident</button>
    </div>
  );
}

// View-scoped styles, ported from the legacy ui.html <style> block for the incidents
// classes (.tabbar/.tab/.inc-*/.fail/.activef/.pc/.pgrid) plus loading + error rows.
const VIEW_CSS = `
.i-loading { display:flex; align-items:center; gap:10px; color:var(--dim); font-size:13px;
  padding:18px; border:1px dashed var(--bd2); border-radius:12px; background:var(--panel2); }
.i-spinner { width:14px; height:14px; border-radius:50%; border:2px solid var(--bd2);
  border-top-color:var(--acc); animation:i-spin .7s linear infinite; flex:none; }
@keyframes i-spin { to { transform:rotate(360deg); } }
.i-fetch-err { font-size:13px; font-weight:600; color:var(--err); background:var(--crit);
  border:1px solid var(--critbd); border-radius:12px; padding:14px 18px; text-align:center; }
.i-unavail { color:var(--dim); font-size:13px; padding:18px 0; }
.i-hint { color:var(--dim); font-size:11.5px; margin-top:6px; }
.i-disclose { cursor:pointer; font:600 12px system-ui; color:var(--dim); margin:10px 0; }

.tabbar { display:flex; gap:4px; margin:8px 0 18px; border-bottom:1px solid var(--bd); }
.tab { padding:6px 14px; cursor:pointer; border:none; border-bottom:2px solid transparent;
  color:var(--dim); background:none; font:600 13px system-ui; }
.tab.active { color:var(--tx); border-bottom-color:var(--acc); }
.tabbody {}

.sec { margin-bottom:26px; }
.sec-label { display:flex; align-items:baseline; gap:8px; font:700 11px system-ui; letter-spacing:.8px;
  text-transform:uppercase; color:var(--dim); margin:0 0 10px; }
.sec-label .meta { font-weight:500; text-transform:none; letter-spacing:0; font-size:11.5px; }

.panel { background:var(--panel2); border:1px solid var(--bd); border-radius:12px; padding:16px 18px; }
.panel.dimmed { opacity:.55; }
.panel h3 { font:700 14px system-ui; color:var(--tx); margin:0 0 6px; }
.panel .gh { font-size:12.5px; color:var(--dim); margin:0 0 14px; line-height:1.5; }

.empty { color:var(--dim); font-size:13px; padding:14px; text-align:center;
  border:1px dashed var(--bd2); border-radius:10px; background:var(--panel); }
.field-err { color:var(--err); font-size:11.5px; margin:6px 0; min-height:0; }

.fail { display:grid; grid-template-columns:1.2fr 1.5fr 1fr auto; gap:12px; align-items:end; }
@media (max-width:760px) { .fail { grid-template-columns:1fr 1fr; } }
.fail label { font:600 11px system-ui; color:var(--dim); display:block; margin-bottom:5px; }
.fail select, .fail input[type=range] { width:100%; }
.fail select, .fail .iwrap { font:13px system-ui; border:1px solid var(--bd); border-radius:8px;
  padding:8px 9px; background:var(--panel); color:var(--tx); }
.fail .iwrap { display:flex; align-items:center; gap:8px; }
.fail .iwrap input { flex:1; }
.go { background:var(--err); color:#fff; border:1px solid var(--err); font:600 13px system-ui;
  border-radius:8px; padding:9px 16px; cursor:pointer; white-space:nowrap; }
.go:hover { filter:brightness(1.06); }

.activef { margin-top:16px; display:flex; flex-direction:column; gap:7px; }
.activef .fitem { display:flex; align-items:center; gap:10px; font:12px var(--mono); padding:7px 11px;
  border:1px solid var(--critbd); border-radius:8px; background:var(--crit); }
.activef .fitem.disabled { background:var(--panel2); border-color:var(--bd); color:var(--dim); }
.activef .fitem .sp { margin-left:auto; }
.activef .fitem a { color:var(--acc); cursor:pointer; text-decoration:none; font-family:system-ui; font-weight:600; }
.activef .fitem a:hover { text-decoration:underline; }

.pgrid { display:grid; grid-template-columns:repeat(auto-fill,minmax(290px,1fr)); gap:15px; }
.pc { background:var(--panel); border:1px solid var(--bd); border-radius:14px; padding:16px; position:relative; }
.pc.on { border-color:var(--critbd); box-shadow:0 4px 18px rgba(224,36,36,.13); }
.pc .hd { display:flex; justify-content:space-between; align-items:flex-start; gap:8px; }
.pc .ti { font-weight:700; font-size:14.5px; line-height:1.3; }
.pc .pc-badge { font:700 9px system-ui; letter-spacing:.5px; color:var(--err); white-space:nowrap;
  display:flex; align-items:center; gap:4px; }
.pc .pc-badge .blip { width:6px; height:6px; border-radius:50%; background:var(--err); animation:pulse 1.3s infinite; }
.pc .su { color:var(--dim); font-size:12.5px; line-height:1.45; margin:6px 0 12px; }
.pc .fx { font-family:var(--mono); font-size:11px; line-height:1.5; margin-bottom:14px; display:flex;
  flex-direction:column; gap:3px; }
.pc .fx .row { display:flex; gap:6px; align-items:center; }
.pc .fx .m { color:var(--acc); font-weight:600; } .pc .fx .arr { color:var(--dim); }
.pc .fx .t { color:var(--tx); overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.pc .fx .i { color:var(--dim); margin-left:auto; flex:none; }
.pc .pc-btn { display:block; width:100%; text-align:center; font:600 13px system-ui; padding:9px;
  border:1px solid var(--bd); border-radius:9px; background:var(--panel2); color:var(--tx); cursor:pointer; }
.pc .pc-btn:hover { border-color:var(--acc); }
.pc.on .pc-btn { background:var(--err); border-color:var(--err); color:#fff; }
.pc.on .pc-btn:hover { filter:brightness(1.05); }

.inc-track-wrap { margin-bottom:14px; }
.inc-track-label { font:700 12px system-ui; color:var(--tx); margin-bottom:6px; }
.inc-track { position:relative; height:28px; background:var(--panel2); border:1px solid var(--bd);
  border-radius:8px; overflow:hidden; }
.inc-band { position:absolute; top:3px; bottom:3px; border-radius:4px; opacity:.75; display:flex;
  align-items:center; padding:0 4px; font:700 9px system-ui; white-space:nowrap; overflow:hidden; cursor:default; }
.inc-band.declared { background:var(--acc); color:#fff; }
.inc-band.runtime { background:var(--err); color:#fff; }
.inc-band.active { opacity:1; box-shadow:0 0 0 2px var(--err); }
.inc-now { position:absolute; top:0; bottom:0; width:2px; background:var(--acc); opacity:.7; }
.inc-dated { margin:4px 0; padding:6px 10px; border:1px solid var(--bd); border-radius:8px;
  font:12px system-ui; display:flex; align-items:center; gap:8px; }
.inc-dated.active-now { border-color:var(--critbd); background:var(--crit); }
.inc-row { display:flex; align-items:center; gap:8px; padding:5px 0; border-bottom:1px solid var(--panel2); font:12px system-ui; }
.inc-row:last-child { border-bottom:none; }
.inc-listwrap { margin-top:8px; }
.inc-badge { font:700 9px system-ui; letter-spacing:.4px; text-transform:uppercase; padding:1px 6px; border-radius:5px; }
.inc-badge.declared { background:var(--acc2); color:var(--acc); border:1px solid var(--accbd); }
.inc-badge.runtime { background:var(--crit); color:var(--err); border:1px solid var(--critbd); }
.inc-dot { color:var(--err); animation:pulse 1.3s infinite; }
.inc-spec { flex:1; font-family:var(--mono); font-size:11.5px; }
.inc-status { font:11px system-ui; color:var(--dim); }
.inc-del { margin-left:auto; background:none; border:none; cursor:pointer; color:var(--err); font-size:15px; padding:0 4px; }

.sched-grid2 { display:grid; grid-template-columns:1fr 1fr; gap:12px; margin-bottom:12px; }
.sched-lbl { font:600 11px system-ui; color:var(--dim); display:block; margin-bottom:5px; }
.sched-row { display:flex; align-items:center; gap:8px; margin-top:6px; }
.sched-input { border:1px solid var(--bd); border-radius:7px; padding:6px 8px; background:var(--panel);
  color:var(--tx); font:13px system-ui; }
.sched-durrow { display:flex; gap:16px; align-items:center; margin-bottom:12px; }
.panel select { font:13px system-ui; border:1px solid var(--bd); border-radius:8px; padding:8px 9px;
  background:var(--panel); color:var(--tx); width:100%; }
`;
