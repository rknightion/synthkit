import { Show, createSignal, type JSX } from "solid-js";
import { A } from "@solidjs/router";
import { useStore } from "../store/store";
import { postJSON, ApiError } from "../api/client";
import { ConfirmButton } from "./ConfirmDialog";
import { Posture } from "./Posture";
import { Status } from "./Status";
import { Nav } from "./Nav";
import { ThemeToggle } from "./ThemeToggle";
import { Search } from "./Search";
import type { State } from "../api/types";

// deviationCount mirrors the legacy resetAll tally: how many knobs are off baseline.
// Used only to phrase the confirm prompt; the reset itself always clears everything.
function deviationCount(state: State | undefined): number {
  if (!state) return 0;
  const def = 1; // volume baseline (schema default is 1×; the schema view confirms it)
  return (
    (state.active_scenarios?.length ?? 0) +
    Object.keys(state.scaling ?? {}).length +
    Object.values(state.failures ?? {}).filter((f) => f?.enabled).length +
    (state.disabled_blueprints?.length ?? 0) +
    (state.disabled_kinds?.length ?? 0) +
    (state.disabled_constructs?.length ?? 0) +
    (state.span_metrics_blueprints?.length ?? 0) +
    (state.volume_multiplier !== def ? 1 : 0)
  );
}

// The left sidebar: brand, quick actions, current-posture summary, nav, and the
// always-mounted Cmd-K search overlay (it listens globally and renders only when
// open). Single responsibility: compose the rail; each piece owns its own logic.
export function Rail(): JSX.Element {
  const store = useStore();
  const [resetErr, setResetErr] = createSignal<string>();

  // POST /control/reset (body null) — clears every runtime knob to baseline. Ported
  // from the legacy resetAll; confirm-gated, errors surface inline below the buttons.
  function doReset() {
    void postJSON("reset", null)
      .then(() => { setResetErr(undefined); return store.refresh(); })
      .catch((e: unknown) => setResetErr(e instanceof ApiError ? e.message : String(e)));
  }
  const resetMsg = () => {
    const n = deviationCount(store.state.state);
    return n
      ? `Reset ALL ${n} active deviation${n === 1 ? "" : "s"} back to defaults?`
      : "Everything is already at defaults. Reset anyway?";
  };

  return (
    <aside class="rail">
      <A href="/" end class="rail-brand" title="Back to Overview">
        synth<span>kit</span>
      </A>
      <p class="rail-tagline">runtime control plane</p>

      <div class="rail-btns">
        <button type="button" title="Reload state" onClick={() => void store.refresh()}>
          ↻ Refresh
        </button>
        <ConfirmButton
          testid="rail-reset"
          label="↺ Reset"
          confirmLabel="Reset all"
          message={resetMsg()}
          onConfirm={doReset}
        />
        <ThemeToggle />
      </div>
      <Show when={resetErr()}>
        <div class="rail-reset-err" data-testid="rail-reset-err">
          Reset failed: {resetErr()}
        </div>
      </Show>

      <Posture />
      <Nav />
      <Status />
      <Search />
    </aside>
  );
}
