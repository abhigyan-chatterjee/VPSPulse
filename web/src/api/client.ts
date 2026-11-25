import type {
  AuthStatus,
  HostMetricPoint,
  Incident,
  NotificationChannel,
  Probe,
  SettingsInfo,
  VPS,
} from "../types";

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

let unauthorizedHandler: (() => void) | null = null;

export function setUnauthorizedHandler(fn: (() => void) | null) {
  unauthorizedHandler = fn;
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {};
  if (init?.body !== undefined) {
    headers["Content-Type"] = "application/json";
  }

  const res = await fetch(path, {
    credentials: "include",
    ...init,
    headers: {
      ...headers,
      ...(init?.headers as Record<string, string> | undefined),
    },
  });

  const text = await res.text();
  let data: Record<string, unknown> = {};
  if (text) {
    try {
      data = JSON.parse(text) as Record<string, unknown>;
    } catch {
      data = {};
    }
  }

  if (res.status === 401 && path !== "/api/v1/auth/login" && path !== "/api/v1/auth/password") {
    unauthorizedHandler?.();
  }

  if (!res.ok) {
    const msg =
      typeof data.error === "string" && data.error
        ? data.error
        : `Request failed (${res.status}${res.statusText ? " " + res.statusText : ""})`;
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

export const api = {
  authStatus: () => req<AuthStatus>("/api/v1/auth/status"),
  setupAdmin: (username: string, password: string) =>
    req<{ username: string; authenticated: boolean }>("/api/v1/auth/setup", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    }),
  login: (username: string, password: string) =>
    req<{ username: string; authenticated: boolean }>("/api/v1/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    }),
  logout: () => req<{ ok: boolean }>("/api/v1/auth/logout", { method: "POST", body: "{}" }),
  changePassword: (current_password: string, new_password: string) =>
    req<{ ok: boolean }>("/api/v1/auth/password", {
      method: "POST",
      body: JSON.stringify({ current_password, new_password }),
    }),

  listVPS: () => req<VPS[]>("/api/v1/vps"),
  getVPS: (id: string) => req<VPS>(`/api/v1/vps/${encodeURIComponent(id)}`),
  createVPS: (name: string, tag: string) =>
    req<{ vps: VPS; token: string }>("/api/v1/vps", {
      method: "POST",
      body: JSON.stringify({ name, tag }),
    }),
  updateVPS: (
    id: string,
    patch: Partial<Pick<VPS, "name" | "tag" | "cpu_warn" | "ram_warn" | "disk_warn" | "interval_seconds">>,
  ) =>
    req<VPS>(`/api/v1/vps/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: JSON.stringify(patch),
    }),
  deleteVPS: (id: string) => req<{ ok: boolean }>(`/api/v1/vps/${encodeURIComponent(id)}`, { method: "DELETE" }),
  rotateVPSToken: (id: string) =>
    req<{ token: string }>(`/api/v1/vps/${encodeURIComponent(id)}/rotate-token`, {
      method: "POST",
      body: "{}",
    }),
  vpsMetrics: (id: string, range: string) =>
    req<HostMetricPoint[]>(`/api/v1/vps/${encodeURIComponent(id)}/metrics?range=${encodeURIComponent(range)}`),
  setWatchedUnits: (id: string, units: string[]) =>
    req<VPS>(`/api/v1/vps/${encodeURIComponent(id)}/systemd`, {
      method: "PUT",
      body: JSON.stringify({ units }),
    }),
  updateContainerPolicy: (id: string, name: string, body: { muted?: boolean; pinned?: boolean }) =>
    req<VPS>(`/api/v1/vps/${encodeURIComponent(id)}/containers/${encodeURIComponent(name)}`, {
      method: "PATCH",
      body: JSON.stringify(body),
    }),

  listProbes: (vpsId?: string) =>
    req<Probe[]>(vpsId !== undefined ? `/api/v1/probes?vps_id=${encodeURIComponent(vpsId)}` : "/api/v1/probes"),
  createProbe: (probe: Partial<Probe>) =>
    req<Probe>("/api/v1/probes", {
      method: "POST",
      body: JSON.stringify(probe),
    }),
  updateProbe: (id: string, probe: Partial<Probe>) =>
    req<Probe>(`/api/v1/probes/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: JSON.stringify(probe),
    }),
  deleteProbe: (id: string) =>
    req<{ ok: boolean }>(`/api/v1/probes/${encodeURIComponent(id)}`, { method: "DELETE" }),
  checkProbeNow: (id: string) =>
    req<{ result: unknown; probe: Probe }>(`/api/v1/probes/${encodeURIComponent(id)}/check`, {
      method: "POST",
      body: "{}",
    }),

  listIncidents: (status = "all") =>
    req<Incident[]>(`/api/v1/incidents?status=${encodeURIComponent(status)}`),
  ackIncident: (id: string) =>
    req<{ ok: boolean }>(`/api/v1/incidents/${encodeURIComponent(id)}/ack`, { method: "POST", body: "{}" }),

  listChannels: () => req<NotificationChannel[]>("/api/v1/channels"),
  createChannel: (ch: Partial<NotificationChannel>) =>
    req<NotificationChannel>("/api/v1/channels", {
      method: "POST",
      body: JSON.stringify(ch),
    }),
  updateChannel: (id: string, ch: Partial<NotificationChannel>) =>
    req<NotificationChannel>(`/api/v1/channels/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: JSON.stringify(ch),
    }),
  deleteChannel: (id: string) =>
    req<{ ok: boolean }>(`/api/v1/channels/${encodeURIComponent(id)}`, { method: "DELETE" }),
  testChannel: (id: string) =>
    req<{ ok: boolean }>(`/api/v1/channels/${encodeURIComponent(id)}/test`, { method: "POST", body: "{}" }),

  getSettings: () => req<SettingsInfo>("/api/v1/settings"),
  updateSettings: (patch: Record<string, string>) =>
    req<SettingsInfo>("/api/v1/settings", { method: "PATCH", body: JSON.stringify(patch) }),
};

export async function copyText(text: string): Promise<boolean> {
  if (navigator.clipboard && typeof navigator.clipboard.writeText === "function") {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // fall through to textarea fallback
    }
  }
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}

export function shellQuote(s: string): string {
  return `'${s.replace(/'/g, `'\\''`)}'`;
}

export function buildInstallCommand(rawBaseUrl: string, token: string): string {
  let safeUrl = window.location.origin;
  try {
    const u = new URL(rawBaseUrl || window.location.origin);
    if (u.protocol === "http:" || u.protocol === "https:") {
      safeUrl = u.origin + u.pathname.replace(/\/+$/, "");
    }
  } catch {
    // fallback to origin
  }
  return `curl -fsSL ${shellQuote(`${safeUrl}/install.sh`)} | sudo sh -s -- \\\n  --url ${shellQuote(safeUrl)} --token ${shellQuote(token)}`;
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = bytes;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 10 ? v.toFixed(1) : v.toFixed(2)} ${units[i]}`;
}

export function formatUptime(sec: number): string {
  if (!Number.isFinite(sec) || sec <= 0) return "0s";
  if (sec < 60) return `${Math.floor(sec)}s`;
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

export function formatRelativeTime(tsSec: number): string {
  if (!Number.isFinite(tsSec) || tsSec <= 0) return "never";
  const diff = Math.max(0, Math.floor(Date.now() / 1000) - tsSec);
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  return `${Math.floor(diff / 86400)}d ago`;
}
