import { createSignal } from "solid-js";

const KEY = "synthkit-control-theme";

// Toggles the document-level data-theme attribute and mirrors the choice into
// localStorage under the legacy key, so a reload restores the operator's theme.
export function ThemeToggle() {
  const [theme, setTheme] = createSignal(
    document.documentElement.getAttribute("data-theme") ?? "dark",
  );
  const toggle = () => {
    const next = theme() === "dark" ? "light" : "dark";
    document.documentElement.setAttribute("data-theme", next);
    // Persist best-effort: localStorage can be absent (SSR/jsdom) or throw
    // (private mode, blocked storage) — the theme flip must still happen.
    try {
      globalThis.localStorage?.setItem(KEY, next);
    } catch {
      /* ignore */
    }
    setTheme(next);
  };
  return (
    <button type="button" aria-label="Toggle light/dark theme" onClick={toggle}>
      {theme() === "dark" ? "☾" : "☀"}
    </button>
  );
}
