import { For, Show, type JSX } from "solid-js";
import { useStore } from "../store/store";
import type { FleetStat, SinkStat } from "../api/types";
import { fmtNum } from "../utils/fmt";

// Always-visible rail telemetry surface (ported from the legacy ui.html renderStatus,
// ~785-842): per-sink readiness rows, the Fleet (FM) lifecycle line, persist health, and
// a prominent DRY-RUN badge. Lives in the rail so it shows on EVERY view (the rewrite had
// only consumed `status` inside Overview — this restores parity).
//
// Reads useStore().state.status (StatusReport). When status is undefined (not yet polled
// or a 404) it renders a muted "status unavailable" line rather than crashing.

// Sinks carry their wire name; the panel shows the operator-facing signal class instead,
// plus the unit each sink's "items" actually counts. Unknown sinks fall back to raw name.
const SINK_LABELS: Record<string, string> = {
  promrw: "metrics",
  loki: "logs",
  otlp: "traces",
  faro: "rum",
};
const SINK_UNITS: Record<string, string> = {
  promrw: "series",
  loki: "lines",
  otlp: "spans",
  faro: "beacons",
};

// Convert an epoch-millis stamp into a compact "Ns ago" string (0/unset ⇒ "never").
function agoMs(ms: number): string {
  if (!ms) return "never";
  const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
  if (s < 60) return s + "s ago";
  const m = Math.round(s / 60);
  if (m < 60) return m + "m ago";
  return Math.round(m / 60) + "h ago";
}

// sparkline(values) → an inline <svg> string (currentColor stroke, inherits row colour).
// Flat/empty/<2 points ⇒ null (same shape as Overview's sparkline).
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

type RowCls = "ok" | "err" | "muted";

// One emitter (sink or FM): a header line (dot + name + "ago") and a muted stats sub-line
// (stat strings joined by " · ", an optional red fail suffix, an optional trailing spark).
function EmitterRow(props: {
  name: string;
  cls: RowCls;
  ago: string;
  stats: string[];
  spark?: number[];
  failText?: string;
}): JSX.Element {
  const sv = () => sparkline(props.spark);
  return (
    <div class={`emit ${props.cls}`}>
      <div class="ehead">
        <span class={`dot ${props.cls}`} />
        <span class="name">{props.name}</span>
        <Show when={props.ago}>
          <span class="ago">{props.ago}</span>
        </Show>
      </div>
      <div class="esub">
        <span>{props.stats.join(" · ")}</span>
        <Show when={props.failText}>
          <span class="efail">{props.failText}</span>
        </Show>
        <Show when={sv()}>
          {/* eslint-disable-next-line solid/no-innerhtml */}
          <span class="spark" title={sparkTooltip(props.spark)} innerHTML={sv()!} />
        </Show>
      </div>
    </div>
  );
}

// Per-sink readiness derivation (verbatim from the legacy renderStatus): a sink is failed
// when it carries a last_error whose stamp is at least as recent as its last success; ok
// once it has ever succeeded; muted (pending) when it has neither pushed nor errored.
function sinkRow(s: SinkStat): JSX.Element {
  const name = SINK_LABELS[s.sink] || s.sink;
  const unit = SINK_UNITS[s.sink] || "items";

  // Never pushed, never errored → pending.
  if (!s.last_success_ms && !s.last_error_ms) {
    return <EmitterRow name={name} cls="muted" ago="" stats={["pending"]} />;
  }

  const failed = !!s.last_error && s.last_error_ms >= s.last_success_ms;
  const cls: RowCls = failed ? "err" : s.last_success_ms ? "ok" : "muted";
  const ago = agoMs(failed ? s.last_error_ms : s.last_success_ms);

  const stats = [fmtNum(s.total_items) + " " + unit];
  if (s.rate_per_min > 0) stats.push("~" + fmtNum(s.rate_per_min) + "/min");
  stats.push(fmtNum(s.pushes) + " pushes");
  const failText = s.failures > 0 ? " · " + fmtNum(s.failures) + " failed" : undefined;

  return (
    <>
      <EmitterRow name={name} cls={cls} ago={ago} stats={stats} spark={s.spark} failText={failText} />
      <Show when={failed && s.last_error}>
        <div class="esub esub-err">↳ {s.last_error}</div>
      </Show>
    </>
  );
}

export function Status(): JSX.Element {
  const store = useStore();
  const status = () => store.state.status;
  const sinks = () => status()?.sinks ?? [];

  // FM lifecycle row — only when the controller is actually registering/heartbeating
  // (hidden in metrics-only mode, where no controller runs).
  const fleet = (): FleetStat | undefined => {
    const f = status()?.fleet;
    if (f && (f.registered > 0 || f.heartbeats > 0 || f.last_error)) return f;
    return undefined;
  };

  const persist = () => status()?.persist;

  return (
    <div class="posture status-panel">
      <style>{STATUS_CSS}</style>
      <Show
        when={status()}
        fallback={<div class="ptag muted">status unavailable</div>}
      >
        {(st) => (
          <>
            <div class="ph">
              Telemetry
              <Show when={st().dry_run}>
                {" "}
                <span class="badge">dry run</span>
              </Show>
            </div>

            <Show when={sinks().length === 0}>
              <div class="ptag muted">{st().dry_run ? "no pushes (dry run)" : "no pushes yet"}</div>
            </Show>
            <For each={sinks()}>{(s) => sinkRow(s)}</For>

            <Show when={fleet()}>
              {(f) => {
                const failed = !!f().last_error && f().last_error_ms >= f().last_ok_ms;
                const cls: RowCls = failed ? "err" : f().last_ok_ms ? "ok" : "muted";
                const ago = agoMs(failed ? f().last_error_ms : f().last_ok_ms);
                const stats = [
                  fmtNum(f().registered) + " collectors",
                  fmtNum(f().heartbeats) + " heartbeats",
                ];
                const failText = f().failures > 0 ? " · " + fmtNum(f().failures) + " failed" : undefined;
                return (
                  <>
                    <EmitterRow name="fm" cls={cls} ago={ago} stats={stats} failText={failText} />
                    <Show when={failed && f().last_error}>
                      <div class="esub esub-err">↳ {f().last_error}</div>
                    </Show>
                  </>
                );
              }}
            </Show>

            <Show when={persist()?.last_error}>
              <div class="ptag err">
                <span class="dot err" />
                persist: error — {persist()!.last_error}
              </div>
            </Show>
            <Show when={!persist()?.last_error && persist()?.last_ok_ms}>
              <div class="ptag ok">
                <span class="dot ok" />
                persist: ok {agoMs(persist()!.last_ok_ms)}
              </div>
            </Show>
          </>
        )}
      </Show>

      <div class="auth-note" data-testid="auth-note">
        Mutations require the control token (user <code>control</code>), if configured.
      </div>
    </div>
  );
}

// Panel-scoped styles ported from the legacy ui.html status block (.emit/.ehead/.esub/.dot/
// .badge/.spark/.efail). The container reuses the shell .posture/.ph/.ptag tokens; the
// emitter-row classes were view-scoped in the legacy file so they live here, mirroring
// Overview's in-component VIEW_CSS convention. All colours read the design tokens.
const STATUS_CSS = `
.status-panel { margin-top: 14px; }
.status-panel .ph .badge { display:inline-block; font:700 9.5px system-ui; letter-spacing:.5px;
  text-transform:uppercase; padding:1px 6px; border-radius:6px; background:var(--acc); color:#fff;
  vertical-align:middle; }
.status-panel .dot { display:inline-block; width:7px; height:7px; border-radius:50%; flex:none; }
.status-panel .dot.ok { background:var(--ok); }
.status-panel .dot.err { background:var(--err); }
.status-panel .dot.muted { background:var(--dim); }
.status-panel .ptag.ok { color:var(--ok); }
.status-panel .ptag.err { color:var(--err); }
.status-panel .emit { margin:6px 0; }
.status-panel .emit .ehead { display:flex; align-items:center; gap:6px; font-size:11.5px;
  font-weight:600; line-height:1.3; }
.status-panel .emit .ehead .name { font-weight:700; }
.status-panel .emit .ehead .ago { margin-left:auto; color:var(--dim); font-weight:500;
  font-size:10.5px; white-space:nowrap; }
.status-panel .emit.ok .name { color:var(--ok); }
.status-panel .emit.err .name { color:var(--err); }
.status-panel .emit.muted .name { color:var(--dim); }
.status-panel .emit .esub { display:flex; align-items:center; gap:5px; margin:1px 0 0 13px;
  color:var(--dim); font-size:10.5px; font-weight:500; }
.status-panel .emit .esub.esub-err { color:var(--err); }
.status-panel .emit .esub .spark { margin-left:auto; display:inline-flex; }
.status-panel .emit .esub .spark svg { display:block; }
.status-panel .emit .esub .efail { color:var(--err); font-weight:600; }
.status-panel .auth-note { margin-top:10px; font-size:10px; color:var(--dim); line-height:1.4; }
.status-panel .auth-note code { font-family:monospace; font-size:10px; }
`;
