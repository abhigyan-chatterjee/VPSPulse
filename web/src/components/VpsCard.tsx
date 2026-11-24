import type { ServiceTick, VPS } from "../types";
import { formatBytes, formatUptime } from "../api/client";
import { HeartbeatBars } from "./HeartbeatBars";
import { Sparkline } from "./Sparkline";

interface VpsCardProps {
  vps: VPS;
  onSelect: (id: string) => void;
}

export function vpsHasIssue(v: VPS): boolean {
  if (v.status === "offline") return true;
  const cpu = v.latest_metric?.cpu_percent ?? 0;
  const ram = v.latest_metric?.ram_percent ?? 0;
  const disk = v.latest_metric?.disk_percent ?? 0;
  if (cpu >= v.cpu_warn || ram >= v.ram_warn || disk >= v.disk_warn) return true;
  if (
    v.containers.some(
      (c) =>
        !c.muted &&
        (c.health === "unhealthy" ||
          c.state === "restarting" ||
          c.oom_killed ||
          (c.state === "exited" && c.exit_code !== 0) ||
          (c.pinned && c.state !== "running")),
    )
  ) {
    return true;
  }
  if (v.systemd_units.some((u) => u.active_state === "failed" || (u.watched && u.active_state !== "active"))) {
    return true;
  }
  if (v.probes.some((p) => p.status === "down" || (p.tls_expiry_days !== null && p.tls_expiry_days < 14))) {
    return true;
  }
  return false;
}

function barColor(pct: number, warnAt: number) {
  if (pct >= warnAt) return "bg-[var(--error)]";
  if (pct >= warnAt - 15) return "bg-[var(--warning)]";
  return "bg-[var(--success)]";
}

function textColor(pct: number, warnAt: number) {
  if (pct >= warnAt) return "text-[var(--error)]";
  if (pct >= warnAt - 15) return "text-[var(--warning)]";
  return "text-[var(--fg)]";
}

export function VpsCard({ vps, onSelect }: VpsCardProps) {
  const m = vps.latest_metric;
  const cpu = Math.round(m?.cpu_percent ?? 0);
  const ramPct = Math.round(m?.ram_percent ?? 0);
  const diskPct = Math.round(m?.disk_percent ?? 0);
  const ramUsedGB = ((m?.ram_used_bytes ?? 0) / 1024 ** 3).toFixed(1);
  const ramTotalGB = ((m?.ram_total_bytes ?? 0) / 1024 ** 3).toFixed(0);
  const isOffline = vps.status === "offline";
  const isPending = vps.status === "pending";
  const warn = vpsHasIssue(vps);

  const badgeCls = isOffline
    ? "bg-[var(--error)]/15 text-[var(--error)]"
    : isPending
      ? "bg-[var(--secondary)] text-[var(--muted)]"
      : warn
        ? "bg-[var(--warning)]/15 text-[var(--warning)]"
        : "bg-[var(--success)]/15 text-[var(--success)]";
  const badgeText = isOffline ? "Offline" : isPending ? "Waiting agent" : warn ? "Warning" : "Online";
  const dotCls = isOffline
    ? "bg-[var(--error)]"
    : isPending
      ? "bg-[var(--muted)]"
      : warn
        ? "bg-[var(--warning)]"
        : "bg-[var(--success)]";

  interface CompactRow {
    key: string;
    kind: string;
    name: string;
    status: "up" | "warn" | "down";
    meta: string;
    metaWarn?: boolean;
    uptime: string;
    ticks: ServiceTick[];
  }

  const rows: CompactRow[] = [
    ...vps.probes.map((p) => ({
      key: `prb-${p.id}`,
      kind: p.type.toUpperCase(),
      name: p.name,
      status: (p.status === "down" ? "down" : p.status === "degraded" ? "warn" : "up") as "up" | "warn" | "down",
      meta:
        p.tls_expiry_days !== null && p.tls_expiry_days < 14
          ? `TLS ${p.tls_expiry_days}d`
          : p.status === "pending"
            ? "pending"
            : `${p.latency_ms}ms`,
      metaWarn: p.tls_expiry_days !== null && p.tls_expiry_days < 14,
      uptime: `${p.uptime_24h.toFixed(1)}%`,
      ticks: p.ticks,
    })),
    ...vps.containers
      .filter((c) => !c.muted && (c.pinned || !(c.state === "exited" && c.exit_code === 0)))
      .map((c) => {
        const isBad =
          c.health === "unhealthy" ||
          c.state === "restarting" ||
          c.oom_killed ||
          (c.state === "exited" && c.exit_code !== 0) ||
          c.state === "missing";
        return {
          key: `dkr-${c.name}`,
          kind: "Docker",
          name: c.name,
          status: (isBad ? "warn" : "up") as "up" | "warn" | "down",
          meta: c.health && c.health !== "none" ? c.health : c.state,
          metaWarn: isBad,
          uptime: `${c.uptime_24h.toFixed(1)}%`,
          ticks: c.ticks,
        };
      }),
    ...vps.systemd_units.map((u) => {
      const bad = u.active_state === "failed" || (u.watched && u.active_state !== "active");
      return {
        key: `sys-${u.name}`,
        kind: "systemd",
        name: u.name,
        status: (bad ? "down" : "up") as "up" | "warn" | "down",
        meta: u.active_state,
        metaWarn: bad,
        uptime: `${u.uptime_24h.toFixed(1)}%`,
        ticks: u.ticks,
      };
    }),
  ].slice(0, 4);

  return (
    <article
      onClick={() => onSelect(vps.id)}
      className={`rounded-xl border ${
        isOffline
          ? "border-[var(--error)]/60"
          : warn
            ? "border-[var(--warning)]/60"
            : "border-[var(--border)]"
      } bg-[var(--card)] hover:border-[var(--primary)]/70 transition-colors cursor-pointer overflow-hidden flex flex-col`}
    >
      {/* Header */}
      <div className="px-4 py-3.5 border-b border-[var(--card-border)] flex items-center justify-between gap-2">
        <div className="flex items-center gap-2.5 min-w-0">
          <span className={`h-2.5 w-2.5 rounded-full ${dotCls} shrink-0`} />
          <div className="min-w-0">
            <div className="flex items-center gap-2 flex-wrap">
              <h3 className="text-sm font-semibold text-[var(--fg)] truncate">{vps.name}</h3>
              {vps.tag && (
                <span className="px-1.5 py-0.5 rounded text-xs font-mono bg-[var(--secondary)] text-[var(--muted)]">
                  {vps.tag}
                </span>
              )}
            </div>
            <div className="text-xs text-[var(--muted)] truncate">
              {vps.os ? `${vps.os} · ${vps.arch} · ${vps.ip}` : "Run install command to connect agent"}
            </div>
          </div>
        </div>
        <div className="text-right shrink-0">
          <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium ${badgeCls}`}>
            {badgeText}
          </span>
          <div className="text-xs text-[var(--muted)] font-mono tabular-nums mt-0.5">
            {vps.uptime_sec > 0 ? `up ${formatUptime(vps.uptime_sec)}` : "—"}
          </div>
        </div>
      </div>

      {/* Resource Usage */}
      <div className="px-4 py-3.5 space-y-3">
        <div className="flex items-center justify-between gap-3">
          <div className="flex-1 space-y-1.5">
            <div className="flex items-center justify-between text-xs">
              <span className="text-[var(--muted)]">
                CPU{" "}
                <span className="font-mono">
                  ({vps.cpu_cores || 1}c · load {(m?.load_1 ?? 0).toFixed(2)})
                </span>
              </span>
              <span className={`font-mono tabular-nums font-medium ${textColor(cpu, vps.cpu_warn)}`}>{cpu}%</span>
            </div>
            <div className="h-1.5 w-full rounded-full bg-[var(--secondary)] overflow-hidden">
              <div
                className={`h-full rounded-full ${barColor(cpu, vps.cpu_warn)} transition-all duration-300`}
                style={{ width: `${Math.min(100, cpu)}%` }}
              />
            </div>
          </div>
          <Sparkline
            values={vps.sparkline || []}
            color={cpu >= vps.cpu_warn - 10 ? "var(--warning)" : "var(--chart-1)"}
          />
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-1.5">
            <div className="flex items-center justify-between text-xs">
              <span className="text-[var(--muted)]">RAM</span>
              <span className={`font-mono tabular-nums ${textColor(ramPct, vps.ram_warn)}`}>
                {m?.ram_total_bytes ? `${ramUsedGB}/${ramTotalGB}G (${ramPct}%)` : `${ramPct}%`}
              </span>
            </div>
            <div className="h-1.5 w-full rounded-full bg-[var(--secondary)] overflow-hidden">
              <div
                className={`h-full rounded-full ${barColor(ramPct, vps.ram_warn)}`}
                style={{ width: `${Math.min(100, ramPct)}%` }}
              />
            </div>
          </div>

          <div className="space-y-1.5">
            <div className="flex items-center justify-between text-xs">
              <span className="text-[var(--muted)]">Disk</span>
              <span className={`font-mono tabular-nums ${textColor(diskPct, vps.disk_warn)}`}>{diskPct}%</span>
            </div>
            <div className="h-1.5 w-full rounded-full bg-[var(--secondary)] overflow-hidden">
              <div
                className={`h-full rounded-full ${barColor(diskPct, vps.disk_warn)}`}
                style={{ width: `${Math.min(100, diskPct)}%` }}
              />
            </div>
          </div>
        </div>

        <div className="flex items-center justify-between text-xs text-[var(--muted)] pt-0.5">
          <span className="font-mono tabular-nums">
            Net ↓ {formatBytes(m?.net_rx ?? 0)}/s · ↑ {formatBytes(m?.net_tx ?? 0)}/s
          </span>
          <span>
            {vps.containers.length} Docker · {vps.systemd_units.length} systemd · {vps.probes.length} Probes
          </span>
        </div>
      </div>

      {/* Grouped Services */}
      <div className="px-4 py-3 bg-[var(--bg)]/50 border-t border-[var(--card-border)] space-y-2 flex-1">
        {rows.length === 0 ? (
          <div className="text-xs text-[var(--muted)] py-1">No probes, containers, or watched units yet.</div>
        ) : (
          rows.map((s) => (
            <div key={s.key} className="flex items-center justify-between gap-2 text-xs">
              <div className="flex items-center gap-2 min-w-0">
                <span
                  className={`h-2 w-2 rounded-full shrink-0 ${
                    s.status === "up"
                      ? "bg-[var(--success)]"
                      : s.status === "warn"
                        ? "bg-[var(--warning)]"
                        : "bg-[var(--error)]"
                  }`}
                />
                <span className="font-medium truncate text-[var(--fg)]">{s.name}</span>
                <span className="px-1.5 py-0.5 rounded text-xs font-mono bg-[var(--secondary)] text-[var(--muted)] shrink-0">
                  {s.kind}
                </span>
              </div>
              <div className="flex items-center gap-2.5 shrink-0">
                <span
                  className={`font-mono tabular-nums ${
                    s.metaWarn ? "text-[var(--warning)] font-medium" : "text-[var(--muted)]"
                  }`}
                >
                  {s.meta}
                </span>
                <span
                  className={`font-mono tabular-nums w-12 text-right ${
                    s.status === "up" ? "text-[var(--success)]" : "text-[var(--warning)]"
                  }`}
                >
                  {s.uptime}
                </span>
                <div className="hidden sm:block">
                  <HeartbeatBars ticks={s.ticks} count={16} />
                </div>
              </div>
            </div>
          ))
        )}
      </div>
    </article>
  );
}
