export interface AuthStatus {
  needs_setup: boolean;
  authenticated: boolean;
  username: string;
  demo_mode: boolean;
  public_url: string;
}

export interface ServiceTick {
  ts: number;
  status: string; // "up" | "warn" | "down"
  latency_ms?: number;
}

export interface DiskReport {
  mount: string;
  fs: string;
  used_bytes: number;
  total_bytes: number;
  used_percent: number;
}

export interface HostMetricPoint {
  ts: number;
  cpu_percent: number;
  cpu_max?: number;
  load_1: number;
  ram_percent: number;
  ram_used_bytes?: number;
  ram_total_bytes?: number;
  swap_used_bytes?: number;
  swap_total_bytes?: number;
  disk_percent: number;
  disk_used_bytes?: number;
  disk_total_bytes?: number;
  net_rx: number;
  net_tx: number;
}

export interface DockerContainer {
  vps_id: string;
  name: string;
  container_id: string;
  image: string;
  state: string;
  health: string;
  exit_code: number;
  oom_killed: boolean;
  started_at: string;
  restart_count: number;
  cpu_percent: number;
  mem_used_bytes: number;
  mem_limit_bytes: number;
  muted: boolean;
  pinned: boolean;
  updated_at: number;
  uptime_24h: number;
  ticks: ServiceTick[];
}

export interface SystemdUnit {
  vps_id: string;
  name: string;
  active_state: string;
  sub_state: string;
  mem_bytes: number;
  active_since: string;
  auto_failed: boolean;
  watched: boolean;
  updated_at: number;
  uptime_24h: number;
  ticks: ServiceTick[];
}

export interface Probe {
  id: string;
  vps_id: string | null;
  name: string;
  type: "http" | "tcp" | "icmp" | string;
  target: string;
  method: string;
  expected_min: number;
  expected_max: number;
  keyword: string;
  timeout_sec: number;
  interval_sec: number;
  failure_threshold: number;
  enabled: boolean;
  created_at: number;
  status: string;
  latency_ms: number;
  tls_expiry_days: number | null;
  last_error: string;
  last_checked_at: number;
  uptime_24h: number;
  uptime_7d: number;
  uptime_30d: number;
  ticks: ServiceTick[];
}

export interface VPS {
  id: string;
  name: string;
  tag: string;
  status: string; // "online" | "offline" | "pending"
  last_seen_at: number;
  hostname: string;
  os: string;
  kernel: string;
  arch: string;
  ip: string;
  uptime_sec: number;
  cpu_cores: number;
  agent_version: string;
  docker_available: boolean;
  systemd_available: boolean;
  discoverable_units: string[];
  watched_units: string[];
  disks: DiskReport[];
  cpu_warn: number;
  ram_warn: number;
  disk_warn: number;
  interval_seconds: number;
  created_at: number;
  latest_metric?: HostMetricPoint;
  sparkline?: number[];
  containers: DockerContainer[];
  systemd_units: SystemdUnit[];
  probes: Probe[];
}

export interface Incident {
  id: string;
  incident_key: string;
  vps_id: string | null;
  severity: string;
  title: string;
  detail: string;
  status: "open" | "acknowledged" | "resolved" | string;
  created_at: number;
  updated_at: number;
  resolved_at: number;
}

export interface NotificationChannel {
  id: string;
  name: string;
  type: "discord" | "telegram" | "slack" | "webhook" | string;
  config: Record<string, string>;
  events: {
    offline: boolean;
    service: boolean;
    resource: boolean;
    tls: boolean;
  };
  enabled: boolean;
  created_at: number;
}

export interface SettingsInfo {
  public_url: string;
  demo_mode: boolean;
  default_cpu_warn: string;
  default_ram_warn: string;
  default_disk_warn: string;
  db_size_bytes: number;
  db_table_counts: Record<string, number>;
  retention_policy: string;
}
