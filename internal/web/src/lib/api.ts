import type {
  Channel,
  Check,
  CheckSpec,
  DowntimeRow,
  FlipEvent,
  Meta,
  NotificationRow,
  Ping,
  ProjectInfo,
  StatusFeed,
  StatusPageData,
  StreamEvent,
} from "./types";

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(path, {
    ...init,
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  });
  if (resp.status === 204) return undefined as T;
  const text = await resp.text();
  if (!resp.ok) {
    let message = text.slice(0, 300) || `HTTP ${resp.status}`;
    try {
      message = (JSON.parse(text).error as string) ?? message;
    } catch {
      // plain-text errors are already readable
    }
    throw new ApiError(message, resp.status);
  }
  // Some replies are deliberately plain text ("OK" acks, ping bodies), so the
  // shape comes from the response header, never from the request path.
  if (!resp.headers.get("content-type")?.includes("json")) return text as unknown as T;
  return (text ? JSON.parse(text) : undefined) as T;
}

const API = "/api/v3";

export const api = {
  checks: () => request<{ checks: Check[] }>(`${API}/checks/`),
  check: (uuid: string) => request<Check>(`${API}/checks/${uuid}`),
  createCheck: (spec: CheckSpec) => request<Check>(`${API}/checks/`, { method: "POST", body: JSON.stringify(spec) }),
  updateCheck: (uuid: string, spec: CheckSpec) =>
    request<Check>(`${API}/checks/${uuid}`, { method: "POST", body: JSON.stringify(spec) }),
  deleteCheck: (uuid: string) => request<void>(`${API}/checks/${uuid}`, { method: "DELETE" }),
  pause: (uuid: string) => request<Check>(`${API}/checks/${uuid}/pause`, { method: "POST", body: "" }),
  resume: (uuid: string) => request<Check>(`${API}/checks/${uuid}/resume`, { method: "POST", body: "" }),
  pings: (uuid: string) => request<{ pings: Ping[] }>(`${API}/checks/${uuid}/pings/`),
  pingBody: (uuid: string, n: number) => request<string>(`${API}/checks/${uuid}/pings/${n}/body`),
  flips: (uuid: string, seconds: number) =>
    request<{ flips: FlipEvent[] }>(`${API}/checks/${uuid}/flips/?seconds=${seconds}`),
  channels: () => request<{ channels: Channel[] }>("/front/channels"),
  createChannel: (body: Partial<Channel>) => request<Channel>("/front/channels", { method: "POST", body: JSON.stringify(body) }),
  updateChannel: (code: string, body: Partial<Channel>) =>
    request<Channel>(`/front/channels/${code}`, { method: "POST", body: JSON.stringify(body) }),
  deleteChannel: (code: string) => request<void>(`/front/channels/${code}`, { method: "DELETE" }),
  testChannel: (code: string) => request<string>(`/front/channels/${code}/test`, { method: "POST", body: "" }),
  status: () => request<StatusFeed>("/front/status"),
  statusPage: (key: string) =>
    request<StatusPageData>(`/front/status-page/${encodeURIComponent(key)}`),
  project: () => request<ProjectInfo>("/front/project"),
  updateProject: (body: { name?: string; show_slugs?: boolean }) =>
    request<ProjectInfo>("/front/project", { method: "POST", body: JSON.stringify(body) }),
  clearEvents: (uuid: string) =>
    request<string>(`/front/checks/${uuid}/clear-events`, { method: "POST", body: "" }),
  downtimes: (uuid: string, tz: string) =>
    request<{ downtimes: DowntimeRow[] }>(`/front/checks/${uuid}/downtimes?tz=${encodeURIComponent(tz)}`),
  events: (uuid: string) => request<{ events: StreamEvent[] }>(`/front/checks/${uuid}/events`),
  checkStatusText: (uuid: string) =>
    fetch(`/front/checks/${uuid}/status`).then((r) => (r.ok ? r.text() : Promise.reject(new ApiError(`HTTP ${r.status}`, r.status)))),
  notifications: () => request<{ notifications: NotificationRow[] }>("/front/notifications?limit=100"),
  retryNotification: (code: string) =>
    request<string>(`/front/notifications/${code}/retry`, { method: "POST", body: "" }),
  badges: () => request<{ badges: Record<string, Record<string, string>> }>(`${API}/badges/`),
  meta: () => request<Meta>("/front/meta"),
  previewSchedule: (schedule: string, tz: string) =>
    request<{ next: string[] }>("/front/schedule/preview", {
      method: "POST",
      body: JSON.stringify({ schedule, tz }),
    }),
};

// Conditional GET so a 300-check dashboard does not re-parse on every tick.
export async function fetchStatus(etag: string | null): Promise<{ feed: StatusFeed | null; etag: string | null }> {
  const resp = await fetch("/front/status", { headers: etag ? { "If-None-Match": etag } : {} });
  if (resp.status === 304) return { feed: null, etag };
  if (!resp.ok) throw new ApiError(`HTTP ${resp.status}`, resp.status);
  return { feed: (await resp.json()) as StatusFeed, etag: resp.headers.get("etag") };
}
