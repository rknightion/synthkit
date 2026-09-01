import { For, Show, createSignal, onCleanup, onMount, type JSX } from "solid-js";
import { useNavigate } from "@solidjs/router";
import { useStore } from "../store/store";
import { buildSearchIndex, type SearchEntry } from "./searchIndex";
import { Icon } from "./Icon";

// Simple subsequence fuzzy match: every char of query appears, in order, in the
// target. Ported verbatim from the legacy UI.
function fuzzyMatch(query: string, target: string): boolean {
  const q = query.toLowerCase();
  const t = target.toLowerCase();
  let qi = 0;
  for (let ti = 0; ti < t.length && qi < q.length; ti++) {
    if (t[ti] === q[qi]) qi++;
  }
  return qi === q.length;
}

// Cmd/Ctrl-K quick-search overlay. The index (see ./searchIndex) covers blueprints,
// construct kinds, per-blueprint construct instances, config keys, and inventory
// metric names — ported from the legacy UI's five-category buildSearchIndex.
export function Search(): JSX.Element {
  const store = useStore();
  const navigate = useNavigate();
  const [open, setOpen] = createSignal(false);
  const [query, setQuery] = createSignal("");
  const [sel, setSel] = createSignal(-1);
  let inputRef: HTMLInputElement | undefined;

  const results = (): SearchEntry[] => {
    const idx = buildSearchIndex(store.state);
    const q = query().trim();
    const matches = q ? idx.filter((e) => fuzzyMatch(q, e.label)) : idx.slice(0, 8);
    return matches.slice(0, 12);
  };

  const openSearch = () => {
    setQuery("");
    setSel(-1);
    setOpen(true);
    queueMicrotask(() => inputRef?.focus());
  };
  const close = () => setOpen(false);

  const go = (e: SearchEntry) => {
    close();
    navigate(e.path);
  };

  const move = (delta: number) => {
    const n = results().length;
    if (!n) return;
    setSel((s) => Math.max(0, Math.min(n - 1, s + delta)));
  };

  const onKeyDown = (e: KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "k") {
      e.preventDefault();
      if (open()) close();
      else openSearch();
      return;
    }
    if (!open()) return;
    if (e.key === "Escape") { close(); return; }
    if (e.key === "ArrowDown") { e.preventDefault(); move(1); return; }
    if (e.key === "ArrowUp") { e.preventDefault(); move(-1); return; }
    if (e.key === "Enter") {
      e.preventDefault();
      const rows = results();
      const i = sel() >= 0 && sel() < rows.length ? sel() : 0;
      if (rows[i]) go(rows[i]);
    }
  };

  onMount(() => {
    window.addEventListener("keydown", onKeyDown);
    window.addEventListener("synthkit:open-search", openSearch);
    onCleanup(() => {
      window.removeEventListener("keydown", onKeyDown);
      window.removeEventListener("synthkit:open-search", openSearch);
    });
  });

  return (
    <Show when={open()}>
      <div
        class="search-overlay"
        role="dialog"
        aria-modal="true"
        aria-label="Quick search"
        onClick={(e) => {
          if (e.target === e.currentTarget) close();
        }}
      >
        <div class="search-box">
          <div class="search-input-row">
            <Icon name="magnifying-glass" />
            <input
			  ref={inputRef}
			  class="search-input"
			  type="text"
			  role="combobox"
			  aria-controls="search-results"
			  aria-expanded="true"
			  placeholder="Search blueprints, kinds, constructs, config, metrics…"
			  autocomplete="off"
			  spellcheck={false}
			  value={query()}
			  onInput={(e) => {
			    setSel(-1);
			    setQuery(e.currentTarget.value);
			  }}
			  aria-activedescendant={sel() >= 0 ? `search-result-${sel()}` : undefined}
			/>
            <span class="search-count">{results().length}</span>
          </div>
          <div class="search-results" id="search-results" role="listbox">
            <Show
              when={results().length}
              fallback={
                <Show when={query().trim()}>
                  <div class="search-empty">No results for “{query()}”</div>
                </Show>
              }
            >
              <For each={results()}>
                {(item, i) => (
                  <div
                    class={`search-result${i() === sel() ? " sel" : ""}`}
                    id={`search-result-${i()}`}
					role="option"
					aria-selected={i() === sel()}
                    tabindex="-1"
                    onClick={() => go(item)}
                  >
                    <Icon name={item.icon as "squares-four" | "cube" | "hexagon" | "gear" | "chart-line"} class="sr-icon" />
                    <span class="sr-label">{item.label}</span>
                    <span class="sr-type">{item.type}</span>
                  </div>
                )}
              </For>
            </Show>
          </div>
          <div class="search-hint">
            <span>↑↓ navigate · ↵ open</span>
            <span>esc close</span>
          </div>
        </div>
      </div>
    </Show>
  );
}
