import { For, Show, type JSX } from "solid-js";
import { A } from "@solidjs/router";
import { useStore } from "../store/store";
import { Icon, type IconName } from "./Icon";

interface NavItem {
  path: string;
  label: string;
  icon: IconName;
}

interface NavGroup {
  label: string;
  items: NavItem[];
}

// Fixed route table (paths relative to the router base). Mirrors the legacy
// rail's sections; per-blueprint links are appended dynamically below.
const GROUPS: NavGroup[] = [
  {
    label: "Views",
    items: [
      { path: "/", label: "Overview", icon: "squares-four" },
      { path: "/config", label: "Config", icon: "gear" },
      { path: "/health", label: "Health", icon: "pulse" },
      { path: "/xray", label: "X-ray", icon: "crosshair" },
    ],
  },
  {
    label: "Global",
    items: [
      { path: "/global", label: "Global controls", icon: "faders" },
      { path: "/schema", label: "Blueprint schema", icon: "book-open" },
    ],
  },
  {
    label: "Chaos",
    items: [{ path: "/incidents", label: "Incidents", icon: "lightning" }],
  },
  {
    label: "Manage",
    items: [{ path: "/blueprints", label: "Custom blueprints", icon: "package" }],
  },
];

export function Nav(): JSX.Element {
  const store = useStore();
  const disabled = () => new Set(store.state.state?.disabled_blueprints ?? []);
  const anyLive = () =>
    (store.state.state?.active_scenarios?.length ?? 0) > 0 ||
    (store.state.state?.runtime_incidents?.length ?? 0) > 0;

  // Authoritative blueprint list (mirrors Overview): the inventory report lists EVERY
  // loaded blueprint (disabled ones keep their constructs with zeroed emission), unioned
  // defensively with disabled_blueprints, then sorted for stable order.
  const blueprintNames = () => {
    const names = new Set<string>();
    for (const b of store.state.inventory?.blueprints ?? []) names.add(b.blueprint);
    for (const d of store.state.state?.disabled_blueprints ?? []) names.add(d);
    return [...names].sort();
  };

  return (
    <nav class="nav">
      <For each={GROUPS}>
        {(group) => (
          <>
            <div class="navlbl">{group.label}</div>
            <For each={group.items}>
              {(item) => (
                <A
                  href={item.path}
                  end={item.path === "/"}
                  class="navi"
                  activeClass="active"
                >
                  <span class="nm">
                    <Icon name={item.icon} class="nav-icon" /> {item.label}
                  </span>
                  <Show when={item.path === "/incidents" && anyLive()}>
                    <span class="status-word live" aria-label="live incident">● LIVE</span>
                  </Show>
                </A>
              )}
            </For>
          </>
        )}
      </For>

      <Show when={blueprintNames().length}>
        <div class="navlbl">Blueprints</div>
        <For each={blueprintNames()}>
          {(bp) => (
            <A
              href={`/bp/${encodeURIComponent(bp)}`}
              class={`navi${disabled().has(bp) ? " disabled" : ""}`}
              activeClass="active"
            >
              <span class="nm">{bp}</span>
            </A>
          )}
        </For>
      </Show>
    </nav>
  );
}
