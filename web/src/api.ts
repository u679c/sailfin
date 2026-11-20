export type Row = Record<string, any>;
let csrf = "";
export function setCSRF(value: string) {
  csrf = value;
}
export async function api<T = Row[]>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const headers = new Headers(options.headers);
  headers.set("Content-Type", "application/json");
  if (csrf) headers.set("X-CSRF-Token", csrf);
  const res = await fetch(`/api/v1${path}`, {
    ...options,
    headers,
    credentials: "same-origin",
  });
  if (!res.ok) {
    let message = `HTTP ${res.status}`;
    try {
      message = (await res.json()).error || message;
    } catch {}
    if (res.status === 401 && !path.includes("/auth/"))
      window.dispatchEvent(new Event("sailfin:unauthorized"));
    throw new Error(message);
  }
  const data = await res.json();
  if (Array.isArray(data) && res.headers.has("X-Has-More")) {
    Object.defineProperty(data, "hasMore", { value: res.headers.get("X-Has-More") === "true" });
  }
  if (Array.isArray(data) && res.headers.has("X-Total-Count")) {
    Object.defineProperty(data, "total", { value: Number(res.headers.get("X-Total-Count")) });
  }
  return data;
}
export async function backup() {
  const res = await fetch("/api/v1/system/backup", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    credentials: "same-origin",
  });
  if (!res.ok) throw new Error("备份失败，请检查 Core 日志");
  const url = URL.createObjectURL(await res.blob());
  const a = document.createElement("a");
  a.href = url;
  a.download = "sailfin-backup.db";
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
export function parse<T = any>(value: unknown, fallback: T): T {
  if (typeof value !== "string") return fallback;
  try {
    return JSON.parse(value);
  } catch {
    return fallback;
  }
}
export const when = (v: unknown) =>
  v
    ? new Date(Number(v)).toLocaleString("zh-CN", { hour12: false })
    : "尚未上报";
export const number = (v: unknown) => Number(v || 0).toLocaleString("zh-CN");
