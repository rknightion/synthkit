import type { ConfigView } from "../api/types";

// configValue looks a non-secret config field up by env-var key across all groups,
// returning its literal value (or "" when absent/secret). Mirrors the legacy
// ui.html configVal helper used for the self-obs Grafana deep-link.
export function configValue(config: ConfigView | undefined, key: string): string {
  for (const g of config?.groups ?? []) {
    for (const f of g.fields ?? []) {
      if (f.key === key && !f.secret) return f.value;
    }
  }
  return "";
}

// selfObsDashboardURL returns the deep-link to the generator's self-obs dashboard
// when GC_SELF_GRAFANA_URL is configured, else "". Trailing slashes are trimmed so
// the path joins cleanly.
export function selfObsDashboardURL(config: ConfigView | undefined): string {
  const base = configValue(config, "GC_SELF_GRAFANA_URL").replace(/\/+$/, "");
  return base ? `${base}/d/synthkit-selfobs` : "";
}
