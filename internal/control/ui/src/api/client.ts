// Injected by the server from trusted configuration. The fallback preserves
// Vite dev and test URLs; no location/header inference influences API routing.
export function apiURL(path: string): string {
  const prefix = document.querySelector<HTMLMetaElement>('meta[name="control-api-prefix"]')?.content ?? "/control/";
  return `${prefix}${path}`;
}

export class ApiError extends Error {
  constructor(public status: number, message: string) { super(message); this.name = "ApiError"; }
}

async function parseOrThrow<T>(res: Response): Promise<T> {
  if (!res.ok) {
    const text = (await res.text()).trim();
    const msg = res.status === 401
      ? (text || "unauthorized") + ' — username is "control", password is the control token.'
      : (text || `HTTP ${res.status}`);
    throw new ApiError(res.status, msg);
  }
  return (await res.json()) as T;
}

export function getJSON<T>(path: string): Promise<T> {
  return fetch(apiURL(path), { credentials: "same-origin" }).then(parseOrThrow<T>);
}

// getText fetches a text/plain endpoint (e.g. GET /control/blueprint?blueprint=… raw YAML
// source — NOT JSON). Throws ApiError on non-2xx so callers share the .catch story.
export function getText(path: string): Promise<string> {
  return fetch(apiURL(path), { credentials: "same-origin" }).then(async (res) => {
    const text = await res.text();
    if (!res.ok) throw new ApiError(res.status, (text.trim() || `HTTP ${res.status}`));
    return text;
  });
}

export function postJSON<T>(path: string, body: unknown): Promise<T> {
  return fetch(apiURL(path), {
    method: "POST", credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  }).then(parseOrThrow<T>);
}

export function delJSON<T>(path: string): Promise<T> {
  return fetch(apiURL(path), {
    method: "DELETE", credentials: "same-origin",
  }).then(parseOrThrow<T>);
}
