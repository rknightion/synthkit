import { For, Show, type JSX } from "solid-js";
import { useStore } from "../store/store";
import type { Schema, State } from "../api/types";
import { StatusWord } from "./StatusWord";

interface PostureTag {
  cls: "live" | "mod" | "muted";
  text: string;
}

const fmtMult = (n: number) => (Number.isInteger(n) ? `${n}×` : `${n.toFixed(1)}×`);

// Derives the "current posture" summary from runtime state: active scenarios,
// volume override, per-target scaling, ad-hoc failures, disabled blueprints.
// Ported from the legacy renderPosture: knobs are flagged "modified" RELATIVE to
// the schema default (so an explicit override that equals the default is not noise),
// and scenarios/targets render their friendly schema names when resolvable.
export function deriveTags(state: State | undefined, schema?: Schema): PostureTag[] {
  if (!state) return [];
  const tags: PostureTag[] = [];

  const scnName = (id: string): string =>
    schema?.scenarios?.find((s) => `${s.blueprint}/${s.name}` === id)?.name ?? id;
  for (const id of state.active_scenarios ?? []) {
    tags.push({ cls: "live", text: `scenario: ${scnName(id)}` });
  }

  const volDefault = (schema?.volume_multiplier?.default as number | undefined) ?? 1;
  if (typeof state.volume_multiplier === "number" && state.volume_multiplier !== volDefault) {
    tags.push({ cls: "mod", text: `volume ${fmtMult(state.volume_multiplier)}` });
  }

  for (const [id, n] of Object.entries(state.scaling ?? {})) {
    const t = schema?.targets?.find((x) => `${x.blueprint}/${x.name}` === id);
    if (t && t.scalable && n === t.scalable.default) continue; // at default ⇒ not modified
    tags.push({ cls: "mod", text: `${t ? t.name : id} → ${n}` });
  }

  for (const [mode, f] of Object.entries(state.failures ?? {})) {
    if (f && f.enabled) {
      tags.push({ cls: "mod", text: `ad-hoc: ${mode}${f.scope ? ` ${f.scope}` : " (all)"}` });
    }
  }
  for (const bp of state.disabled_blueprints ?? []) {
    tags.push({ cls: "muted", text: `${bp} disabled` });
  }
  return tags;
}

export function Posture(): JSX.Element {
  const store = useStore();
  const tags = () => deriveTags(store.state.state, store.state.schema);
  return (
    <Show
      when={tags().length}
      fallback={
        <div class="posture clean">
          <div class="ph">Posture</div>
          <div class="ptag muted"><StatusWord status="OK" note="all at baseline" /></div>
        </div>
      }
    >
      <div class="posture">
        <div class="ph">Current posture</div>
        <For each={tags()}>
          {(t) => (
            <div class={`ptag ${t.cls}`}>
              <StatusWord status={t.cls === "live" ? "LIVE" : t.cls === "mod" ? "MOD" : "OFF"} note={t.text} />
            </div>
          )}
        </For>
      </div>
    </Show>
  );
}
