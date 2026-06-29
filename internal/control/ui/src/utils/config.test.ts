import { test, expect } from "vitest";
import { configValue, selfObsDashboardURL } from "./config";
import type { ConfigView } from "../api/types";

const cfg: ConfigView = {
  groups: [
    { title: "Self-obs", fields: [
      { key: "GC_SELF_GRAFANA_URL", value: "https://staff.grafana.net/", secret: false, configured: true },
      { key: "GC_SELF_TOKEN", value: "", secret: true, configured: true },
    ] },
  ],
};

test("configValue returns a non-secret field value by key", () => {
  expect(configValue(cfg, "GC_SELF_GRAFANA_URL")).toBe("https://staff.grafana.net/");
});

test("configValue never leaks a secret value", () => {
  expect(configValue(cfg, "GC_SELF_TOKEN")).toBe("");
});

test("configValue returns '' for unknown keys / undefined config", () => {
  expect(configValue(cfg, "NOPE")).toBe("");
  expect(configValue(undefined, "GC_SELF_GRAFANA_URL")).toBe("");
});

test("selfObsDashboardURL trims trailing slash and appends the dashboard path", () => {
  expect(selfObsDashboardURL(cfg)).toBe("https://staff.grafana.net/d/synthkit-selfobs");
});

test("selfObsDashboardURL returns '' when GC_SELF_GRAFANA_URL is unset", () => {
  expect(selfObsDashboardURL({ groups: [] })).toBe("");
  expect(selfObsDashboardURL(undefined)).toBe("");
});
