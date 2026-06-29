import "@testing-library/jest-dom/vitest";

// jsdom in this Node/vitest combo does not provide a working localStorage (Node's
// experimental global one is unavailable without --localstorage-file). Views that
// persist UI state (e.g. Incidents' tab choice) need it, so install a minimal
// in-memory polyfill on both window and globalThis for tests.
if (typeof globalThis.localStorage === "undefined") {
  const store = new Map<string, string>();
  const shim: Storage = {
    get length() { return store.size; },
    clear() { store.clear(); },
    getItem(k: string) { return store.has(k) ? store.get(k)! : null; },
    key(i: number) { return Array.from(store.keys())[i] ?? null; },
    removeItem(k: string) { store.delete(k); },
    setItem(k: string, v: string) { store.set(k, String(v)); },
  };
  Object.defineProperty(globalThis, "localStorage", { value: shim, configurable: true });
  if (typeof window !== "undefined") {
    Object.defineProperty(window, "localStorage", { value: shim, configurable: true });
  }
}
