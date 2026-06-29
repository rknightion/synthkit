import type { Snapshot } from "../store/store";

export interface SearchEntry {
  icon: string;
  label: string;
  type: string;
  path: string;
}

// buildSearchIndex projects the live snapshot into the Cmd-K index. Ported from the
// legacy ui.html buildSearchIndex: five categories — blueprints, construct kinds,
// per-blueprint construct instances, config keys, and inventory metric names. Every
// source is null-guarded so a missing schema/config/inventory just yields fewer rows.
export function buildSearchIndex(snap: Snapshot): SearchEntry[] {
  const idx: SearchEntry[] = [];
  const st = snap.state;

  // blueprints: authoritative list = inventory ∪ disabled (mirrors Overview/Nav), sorted.
  const bps = new Set<string>();
  for (const b of snap.inventory?.blueprints ?? []) bps.add(b.blueprint);
  for (const d of st?.disabled_blueprints ?? []) bps.add(d);
  for (const bp of [...bps].sort()) {
    idx.push({ icon: "▦", label: bp, type: "blueprint", path: `/bp/${encodeURIComponent(bp)}` });
  }

  // ALL construct kinds (schema), not just the disabled ones.
  for (const k of snap.schema?.kinds ?? []) {
    idx.push({ icon: "⬡", label: k, type: "kind", path: "/global" });
  }

  // ALL per-blueprint construct instances (schema), routed to the owning blueprint view.
  for (const c of snap.schema?.constructs ?? []) {
    idx.push({
      icon: "⬡",
      label: `${c.kind} · ${c.name}`,
      type: "construct",
      path: `/bp/${encodeURIComponent(c.blueprint)}`,
    });
  }

  // config keys → Config view.
  for (const g of snap.config?.groups ?? []) {
    for (const f of g.fields ?? []) {
      idx.push({ icon: "⚙", label: f.key, type: "config", path: "/config" });
    }
  }

  // inventory metric names (flat, deduped) → X-ray.
  const seen = new Set<string>();
  for (const b of snap.inventory?.blueprints ?? []) {
    for (const cst of b.constructs ?? []) {
      for (const mn of cst.metric_names ?? []) {
        if (!seen.has(mn)) {
          seen.add(mn);
          idx.push({ icon: "📈", label: mn, type: "metric", path: "/xray" });
        }
      }
    }
  }

  return idx;
}
