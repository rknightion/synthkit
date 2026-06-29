// Shared display formatters, extracted VERBATIM from the per-view inline copies so
// behaviour is identical across Overview/Health/Xray/Blueprint (and shell/Status).
// Pinned by fmt.test.ts. Do not change the output of any of these — they are a DRY
// move, not a redesign.

// Compact magnitude: 1.2M / 9.4k / 840. Identical implementation previously inlined
// in Overview, Health, Xray, Blueprint and shell/Status.
export function fmtNum(n: number): string {
  n = Number(n) || 0;
  if (n >= 1e6) return (n / 1e6).toFixed(n >= 1e7 ? 0 : 1).replace(/\.0$/, "") + "M";
  if (n >= 1e3) return (n / 1e3).toFixed(n >= 1e4 ? 0 : 1).replace(/\.0$/, "") + "k";
  return String(Math.round(n));
}

// Bytes → whole-MB string ("30 MB"), falsy ⇒ em dash. Verbatim from Health's fmtHeap.
export function fmtBytes(bytes: number): string {
  if (!bytes) return "—";
  return fmtNum(Math.round(bytes / (1024 * 1024))) + " MB";
}

// Milliseconds → "N.N ms", null/undefined/zero ⇒ em dash. Verbatim from Health's fmtMs.
export function fmtDurMs(ms: number | undefined | null): string {
  if (ms == null || ms === 0) return "—";
  return ms.toFixed(1) + " ms";
}
