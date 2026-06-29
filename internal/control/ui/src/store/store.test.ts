import { test, expect, vi, beforeEach, afterEach } from "vitest";
import { createControlStore } from "./store";
import * as client from "../api/client";

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

test("refresh populates state and clears loading", async () => {
  vi.spyOn(client, "getJSON").mockResolvedValue({ volume_multiplier: 3 } as never);
  const s = createControlStore({ intervalMs: 5000 });
  await s.refresh();
  expect(s.state.state?.volume_multiplier).toBe(3);
  expect(s.state.loading).toBe(false);
});

test("a failing endpoint records an error, others still load", async () => {
  vi.spyOn(client, "getJSON").mockImplementation(async (p: string) => {
    if (p === "state") throw new client.ApiError(500, "boom");
    return {} as never;
  });
  const s = createControlStore();
  await s.refresh();
  expect(s.state.errors["state"]).toBe("boom");
  expect(s.state.loading).toBe(false); // refresh resolved despite the failed endpoint
});

test("refresh populates a multi-segment endpoint under its short key", async () => {
  vi.spyOn(client, "getJSON").mockImplementation(async (p: string) => {
    if (p === "config") return { groups: [{ title: "Sinks", fields: [] }] } as never;
    return {} as never;
  });
  const s = createControlStore();
  await s.refresh();
  // config endpoint stored under s().config
  expect(s.state.config?.groups[0].title).toBe("Sinks");
});

test("an unconfigured endpoint (404 incidents) records errors[key] without throwing", async () => {
  vi.spyOn(client, "getJSON").mockImplementation(async (p: string) => {
    if (p === "incidents") throw new client.ApiError(404, "incidents unavailable");
    return {} as never;
  });
  const s = createControlStore();
  await expect(s.refresh()).resolves.toBeUndefined(); // poll does not throw
  expect(s.state.errors["incidents"]).toBe("incidents unavailable");
  expect(s.state.loading).toBe(false);
});

test("polling skips a tick when shouldPause is true", async () => {
  const spy = vi.spyOn(client, "getJSON").mockResolvedValue({} as never);
  let paused = true;
  const s = createControlStore({ intervalMs: 1000, shouldPause: () => paused });
  s.start();
  await vi.advanceTimersByTimeAsync(1000);
  const whilePaused = spy.mock.calls.length;
  expect(spy.mock.calls.length).toBe(0); // paused tick was skipped, not just "fewer"
  paused = false;
  await vi.advanceTimersByTimeAsync(1000);
  expect(spy.mock.calls.length).toBeGreaterThan(whilePaused);
  s.stop();
});
