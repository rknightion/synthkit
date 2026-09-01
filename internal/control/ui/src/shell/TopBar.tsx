/* SPDX-License-Identifier: Apache-2.0 */
import { createSignal, type JSX } from "solid-js";
import { useLocation } from "@solidjs/router";
import { postJSON, ApiError } from "../api/client";
import { useStore } from "../store/store";
import type { State } from "../api/types";
import { ConfirmButton } from "./ConfirmDialog";
import { Icon } from "./Icon";
import { ThemeToggle } from "./ThemeToggle";

function deviationCount(state: State | undefined): number {
  if (!state) return 0;
  return (state.active_scenarios?.length ?? 0) + Object.keys(state.scaling ?? {}).length +
    Object.values(state.failures ?? {}).filter((f) => f?.enabled).length +
    (state.disabled_blueprints?.length ?? 0) + (state.disabled_kinds?.length ?? 0) +
    (state.disabled_constructs?.length ?? 0) + (state.span_metrics_blueprints?.length ?? 0) +
    (state.volume_multiplier !== 1 ? 1 : 0);
}

function routeTitle(path: string): string {
  if (path.startsWith("/bp/")) {
    const raw = path.slice(4);
    try { return decodeURIComponent(raw); } catch { return raw; }
  }
  return ({ "/": "Overview", "/config": "Config", "/health": "Health", "/xray": "X-ray", "/global": "Global controls", "/incidents": "Incidents", "/schema": "Blueprint schema", "/blueprints": "Custom blueprints" } as Record<string, string>)[path] ?? "Control";
}

export function TopBar(): JSX.Element {
  const store = useStore();
  const location = useLocation();
  const [resetErr, setResetErr] = createSignal<string>();
  const title = () => routeTitle(location.pathname);
  const resetMsg = () => {
    const count = deviationCount(store.state.state);
    return count ? `Reset ALL ${count} active deviation${count === 1 ? "" : "s"} back to defaults?` : "Everything is already at defaults. Reset anyway?";
  };
  const reset = () => void postJSON("reset", null).then(() => store.refresh()).catch((e: unknown) => setResetErr(e instanceof ApiError ? e.message : String(e)));
  return <header class="topbar">
    <span class="crumb">control / {location.pathname === "/" ? "overview" : location.pathname.slice(1)}</span>
    <div class={`topbar-title${location.pathname.startsWith("/bp/") ? " mono" : ""}`}>{title()}</div>
    <div class="top-actions">
      <span class="polled">polled {store.state.loading ? "now" : "recently"}</span>
      <button class="icon-btn" type="button" aria-label="Refresh control state" onClick={() => void store.refresh()}><Icon name="arrows-clockwise" /></button>
      <ThemeToggle />
      <ConfirmButton class="destructive" testid="rail-reset" label="Reset all" confirmLabel="Reset all" message={resetMsg()} onConfirm={reset} />
      <button class="primary-action" type="button" onClick={() => window.dispatchEvent(new Event("synthkit:open-search"))}>Search</button>
    </div>
    {resetErr() && <span class="topbar-error" data-testid="rail-reset-err" role="alert">Reset failed: {resetErr()}</span>}
  </header>;
}
