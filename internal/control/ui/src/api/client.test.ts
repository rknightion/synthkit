import { test, expect, vi, beforeEach } from "vitest";
import { getJSON, postJSON, delJSON, ApiError } from "./client";

beforeEach(() => { vi.restoreAllMocks(); });

test("getJSON returns parsed JSON for 2xx", async () => {
  vi.stubGlobal("fetch", vi.fn(async () =>
    new Response(JSON.stringify({ volume_multiplier: 1 }), { status: 200 })));
  const s = await getJSON<{ volume_multiplier: number }>("state");
  expect(s.volume_multiplier).toBe(1);
  expect(fetch).toHaveBeenCalledWith("/control/state", expect.anything());
});

test("getJSON throws ApiError with body text on non-2xx", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response("boom", { status: 500 })));
  await expect(getJSON("state")).rejects.toMatchObject({ status: 500, message: "boom" });
});

test("postJSON 401 surfaces the control-token hint", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response("unauthorized", { status: 401 })));
  await expect(postJSON("load", { volume_multiplier: 2 })).rejects.toMatchObject({
    status: 401,
    message: expect.stringContaining("control token"),
  });
});

test("postJSON sends the JSON Content-Type header", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ ok: true }), { status: 200 })));
  await postJSON("load", { volume_multiplier: 2 });
  expect(fetch).toHaveBeenCalledWith(
    "/control/load",
    expect.objectContaining({ headers: { "Content-Type": "application/json" } }),
  );
});

test("delJSON returns parsed JSON and issues a DELETE with same-origin creds", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ status: "removed" }), { status: 200 })));
  const r = await delJSON<{ status: string }>("blueprints/sources?id=x");
  expect(r.status).toBe("removed");
  expect(fetch).toHaveBeenCalledWith(
    "/control/blueprints/sources?id=x",
    expect.objectContaining({ method: "DELETE", credentials: "same-origin" }),
  );
});

test("delJSON throws ApiError on non-2xx", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response("nope", { status: 400 })));
  await expect(delJSON("incidents/bad")).rejects.toMatchObject({ status: 400, message: "nope" });
});
