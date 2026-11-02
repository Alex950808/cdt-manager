// API 层：类型 + fetch 封装（全局唯一 token 处理）
export type Snapshot = {
  status: string;
  stopped_mode: string;
  public_ip: string;
  region_id: string;
  traffic_gb: number | null;
  balance: number;
  currency: string;
  bill_total: number;
  bill_cycle: string;
  refreshed_at: string;
  refresh_error?: string;
};

export type Instance = {
  id: string;
  name: string;
  access_key_id: string;
  access_key_secret: string;
  region_id: string;
  instance_id: string;
  site: string;
  note: string;
  quota_gb: number;
  threshold_gb: number;
  has_secret?: boolean;
  secret_masked?: string;
  snapshot?: Snapshot | null;
};

export type GroupMember = {
  instance_id: string;
  position: number;
  hours: number;
  days: number;
  start_time?: string;
  end_time?: string;
};

export type GroupRun = {
  slot: string;
  phase: string;
  stage: string;
  event_id: number;
  duty_id: string;
  manual: boolean;
  failover: boolean;
  failover_reason: string;
  failover_from: string;
  origin: string;
  attempts: number;
  started_at: string;
  note: string;
  settled: boolean;
  recovered: boolean;
};

export type Group = {
  id: string;
  name: string;
  enabled: boolean;
  failover_enabled: boolean;
  cycle: string;
  anchor_at: string;
  members: GroupMember[];
  run?: GroupRun | null;
  members_view?: Array<{
    instance_id: string;
    name: string;
    status?: string;
    traffic_gb?: number;
    duty?: boolean;
    duty_hours?: number;
    duty_days?: number;
    duty_start?: string;
  }>;
  duty_id?: string;
  phase?: string;
  duty_label?: string;
  next_switch?: string;
};

export type Keepalive = { instance_id: string; name?: string; status?: string; tries?: number };

export type Event = {
  id: number;
  kind: string;
  title: string;
  detail?: string;
  status: string;
  progress?: string[];
  at: string;
  updated_at: string;
};

export type DnsRecord = {
  record: string;
  target_kind: string;
  target_id: string;
  last_ip: string;
  last_status: string;
  last_at: number;
  target_name?: string;
  target_status?: string;
  state?: string;
};

export type Sample = { at: string; gb: number };

export type Settings = Record<string, string>;

async function request<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const token = localStorage.getItem("cdtd_token");
  const res = await fetch(path, {
    ...opts,
    headers: { "Content-Type": "application/json", ...(token ? { Authorization: `Bearer ${token}` } : {}) },
  });
  if (res.status === 401) {
    localStorage.removeItem("cdtd_token");
    window.dispatchEvent(new CustomEvent("cdtd:unauthorized"));
    throw new Error("未登录");
  }
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error((body as { error?: string }).error || res.statusText);
  return body as T;
}

export const api = {
  get: <T>(p: string) => request<T>(p),
  post: <T>(p: string, body?: unknown) => request<T>(p, { method: "POST", body: body != null ? JSON.stringify(body) : undefined }),
  put: <T>(p: string, body: unknown) => request<T>(p, { method: "PUT", body: JSON.stringify(body) }),
  del: <T>(p: string) => request<T>(p, { method: "DELETE" }),
};
