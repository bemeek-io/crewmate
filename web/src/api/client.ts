export class ApiError extends Error {
  code: string;
  status: number;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("X-Crewmate", "1");
  if (init.body) headers.set("Content-Type", "application/json");
  const res = await fetch(path, {
    ...init,
    headers,
    credentials: "same-origin",
  });
  if (res.status === 204) return undefined as T;
  let body: unknown;
  try {
    body = await res.json();
  } catch {
    body = {};
  }
  if (!res.ok) {
    const err = (body as { error?: { code?: string; message?: string } })?.error;
    const apiErr = new ApiError(res.status, err?.code ?? "unknown", err?.message ?? res.statusText);
    if (res.status === 401 && !location.pathname.startsWith("/login")) {
      window.dispatchEvent(new CustomEvent("crewmate:unauthenticated"));
    }
    throw apiErr;
  }
  return body as T;
}

export const get = <T>(path: string) => api<T>(path);
export const post = <T>(path: string, body?: unknown) =>
  api<T>(path, { method: "POST", body: body === undefined ? "{}" : JSON.stringify(body) });
export const patch = <T>(path: string, body: unknown) =>
  api<T>(path, { method: "PATCH", body: JSON.stringify(body) });
export const del = <T>(path: string, body?: unknown) =>
  api<T>(path, { method: "DELETE", body: body === undefined ? undefined : JSON.stringify(body) });

export function fmtCents(cents: number, signed = false): string {
  const abs = Math.abs(cents) / 100;
  const s = abs.toLocaleString(undefined, { style: "currency", currency: "USD" });
  if (cents < 0) return signed ? `−${s}` : s;
  return signed ? `+${s}` : s;
}
