import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowLeft, Check, Copy, Play, Plus, RefreshCw, Trash2 } from "lucide-react";
import { api, buildInstallCommand, copyText, formatBytes, formatRelativeTime, formatUptime } from "../api/client";
import type { HostMetricPoint, VPS } from "../types";
import { AreaChart } from "../components/AreaChart";
import { HeartbeatBars } from "../components/HeartbeatBars";
import { isContainerUnhealthy, probeStatusDot, vpsHasIssue } from "../components/VpsCard";

interface VpsDetailPageProps {
  vps: VPS;
  publicUrl: string;
  onBack: () => void;
  onUpdated: (vps: VPS) => void;
  onDeleted: (id: string) => void;
  onAddProbe: (vpsId: string) => void;
  onToast: (msg: string) => void;
}

export function VpsDetailPage({
  vps,
  publicUrl,
  onBack,
  onUpdated,
  onDeleted,
  onAddProbe,
  onToast,
}: VpsDetailPageProps) {
  const [range, setRange] = useState<"1h" | "24h" | "7d" | "30d">("24h");
  const [tab, setTab] = useState<"probes" | "docker" | "systemd" | "settings">("probes");
  const [metrics, setMetrics] = useState<HostMetricPoint[]>([]);
  const [metricsErr, setMetricsErr] = useState<string | null>(null);
  const [unitChoice, setUnitChoice] = useState("");
  const [customUnit, setCustomUnit] = useState("");

  // Settings form state
  const [name, setName] = useState(vps.name);
  const [tag, setTag] = useState(vps.tag);
  const [cpuWarn, setCpuWarn] = useState(vps.cpu_warn);
  const [ramWarn, setRamWarn] = useState(vps.ram_warn);
  const [diskWarn, setDiskWarn] = useState(vps.disk_warn);
  const [rotatedToken, setRotatedToken] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [busy, setBusy] = useState(false);
  const copyTimerRef = useRef<number | null>(null);
  const lastRollupFetchRef = useRef<number>(0);

  useEffect(() => {
    return () => {
      if (copyTimerRef.current !== null) window.clearTimeout(copyTimerRef.current);
    };
  }, []);

  useEffect(() => {
    if ((range === "7d" || range === "30d") && Date.now() - lastRollupFetchRef.current < 60_000 && metrics.length > 0) {
      return;
    }
    let cancelled = false;
    api
      .vpsMetrics(vps.id, range)
      .then((pts) => {
        if (!cancelled) {
          setMetrics(pts);
          setMetricsErr(null);
          lastRollupFetchRef.current = Date.now();
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setMetrics([]);
          setMetricsErr(err instanceof Error ? err.message : "Failed to load metrics");
        }
      });
    return () => {
      cancelled = true;
    };
  }, [vps.id, range, vps.last_seen_at]);

  const isOffline = vps.status === "offline";
  const isPending = vps.status === "pending";
  const warn = vpsHasIssue(vps);
  const failedUnits = vps.systemd_units.filter((u) => u.active_state === "failed");
  const availableToAdd = vps.discoverable_units.filter((u) => !vps.watched_units.includes(u));
  const effectiveUnitChoice = availableToAdd.includes(unitChoice) ? unitChoice : (availableToAdd[0] || "");

  const timestamps = metrics.map((m) => m.ts);
  const cpuSeries = metrics.map((m) => Number(m.cpu_percent.toFixed(1)));
  const ramSeries = metrics.map((m) => Number(m.ram_percent.toFixed(1)));
  const diskSeries = metrics.map((m) => Number(m.disk_percent.toFixed(1)));

  // Auto-scale network throughput unit (B/s, KB/s, MB/s) so low-traffic hosts remain readable.
  const { netUnit, netRxSeries, netTxSeries } = useMemo(() => {
    const maxBytes = metrics.reduce((acc, m) => Math.max(acc, m.net_rx || 0, m.net_tx || 0), 0);
    if (maxBytes >= 1024 * 1024) {
      return {
        netUnit: " MB/s",
        netRxSeries: metrics.map((m) => Number(((m.net_rx || 0) / (1024 * 1024)).toFixed(2))),
        netTxSeries: metrics.map((m) => Number(((m.net_tx || 0) / (1024 * 1024)).toFixed(2))),
      };
    }
    if (maxBytes >= 1024) {
      return {
        netUnit: " KB/s",
        netRxSeries: metrics.map((m) => Number(((m.net_rx || 0) / 1024).toFixed(1))),
        netTxSeries: metrics.map((m) => Number(((m.net_tx || 0) / 1024).toFixed(1))),
      };
    }
    return {
      netUnit: " B/s",
      netRxSeries: metrics.map((m) => Math.round(m.net_rx || 0)),
      netTxSeries: metrics.map((m) => Math.round(m.net_tx || 0)),
    };
  }, [metrics]);

  const handleToggleContainer = async (containerName: string, patch: { muted?: boolean; pinned?: boolean }) => {
    try {
      const updated = await api.updateContainerPolicy(vps.id, containerName, patch);
      onUpdated(updated);
      onToast(`Updated policy for ${containerName}`);
    } catch (err) {
      onToast(err instanceof Error ? err.message : "Failed to update policy");
    }
  };

  const handleAddWatchedUnit = async () => {
    const unit = (customUnit.trim() || effectiveUnitChoice).trim();
    if (!unit) return;
    try {
      const next = Array.from(new Set([...vps.watched_units, unit]));
      const updated = await api.setWatchedUnits(vps.id, next);
      setCustomUnit("");
      setUnitChoice("");
      onUpdated(updated);
      onToast(`Watching ${unit} (syncs on next heartbeat)`);
    } catch (err) {
      onToast(err instanceof Error ? err.message : "Failed to watch unit");
    }
  };

  const handleRemoveWatchedUnit = async (unit: string) => {
    try {
      const next = vps.watched_units.filter((u) => u !== unit);
      const updated = await api.setWatchedUnits(vps.id, next);
      onUpdated(updated);
      onToast(`Removed ${unit} from watch list`);
    } catch (err) {
      onToast(err instanceof Error ? err.message : "Failed to update watched units");
    }
  };

  const handleCheckProbeNow = async (probeId: string) => {
    try {
      await api.checkProbeNow(probeId);
      const fresh = await api.getVPS(vps.id);
      onUpdated(fresh);
      onToast("Probe check completed");
    } catch (err) {
      onToast(err instanceof Error ? err.message : "Probe check failed");
    }
  };

  const handleDeleteProbe = async (probeId: string, probeName: string) => {
    if (!confirm(`Delete probe "${probeName}"?`)) return;
    try {
      await api.deleteProbe(probeId);
      const fresh = await api.getVPS(vps.id);
      onUpdated(fresh);
      onToast("Probe deleted");
    } catch (err) {
      onToast(err instanceof Error ? err.message : "Failed to delete probe");
    }
  };

  const handleSaveSettings = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      const updated = await api.updateVPS(vps.id, {
        name,
        tag,
        cpu_warn: Number(cpuWarn),
        ram_warn: Number(ramWarn),
        disk_warn: Number(diskWarn),
      });
      onUpdated(updated);
      onToast("VPS settings saved");
    } catch (err) {
      onToast(err instanceof Error ? err.message : "Failed to save settings");
    } finally {
      setBusy(false);
    }
  };

  const handleRotateToken = async () => {
    if (!confirm("Rotate agent token? The existing agent will stop authenticating until updated.")) return;
    setBusy(true);
    try {
      const res = await api.rotateVPSToken(vps.id);
      setRotatedToken(res.token);
      onToast("Agent token rotated");
    } catch (err) {
      onToast(err instanceof Error ? err.message : "Failed to rotate token");
    } finally {
      setBusy(false);
    }
  };

  const handleDeleteVps = async () => {
    if (!confirm(`Delete ${vps.name} and all its stored metrics?`)) return;
    setBusy(true);
    try {
      await api.deleteVPS(vps.id);
      onDeleted(vps.id);
    } catch (err) {
      onToast(err instanceof Error ? err.message : "Failed to delete VPS");
    } finally {
      setBusy(false);
    }
  };

  const installSnippet = rotatedToken ? buildInstallCommand(publicUrl, rotatedToken) : "";

  return (
    <div className="space-y-5">
      {/* Header */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
        <div className="space-y-1.5">
          <button
            onClick={onBack}
            className="inline-flex items-center gap-1.5 text-xs text-[var(--muted)] hover:text-[var(--fg)] cursor-pointer"
          >
            <ArrowLeft className="w-3.5 h-3.5" /> Back to Overview
          </button>
          <div className="flex items-center gap-2.5 flex-wrap">
            <span
              className={`h-3 w-3 rounded-full ${
                isOffline
                  ? "bg-[var(--error)]"
                  : isPending
                    ? "bg-[var(--muted)]"
                    : warn
                      ? "bg-[var(--warning)]"
                      : "bg-[var(--success)]"
              }`}
            />
            <h1 className="text-lg font-semibold">{vps.name}</h1>
            {vps.tag && (
              <span className="px-2 py-0.5 rounded text-xs font-mono bg-[var(--secondary)] text-[var(--muted)]">
                {vps.tag}
              </span>
            )}
            <span
              className={`px-2 py-0.5 rounded-full text-xs font-medium ${
                isOffline
                  ? "bg-[var(--error)]/15 text-[var(--error)]"
                  : isPending
                    ? "bg-[var(--secondary)] text-[var(--muted)]"
                    : warn
                      ? "bg-[var(--warning)]/15 text-[var(--warning)]"
                      : "bg-[var(--success)]/15 text-[var(--success)]"
              }`}
            >
              {isOffline ? "Offline" : isPending ? "Waiting agent" : warn ? "Attention needed" : "Healthy"}
            </span>
          </div>
          <p className="text-xs text-[var(--muted)] font-mono">
            {vps.os || "Pending"} · kernel {vps.kernel || "—"} · {vps.arch || "—"} · IP {vps.ip || "—"} · up{" "}
            {formatUptime(vps.uptime_sec)} · seen {formatRelativeTime(vps.last_seen_at)} · agent v
            {vps.agent_version || "—"}
          </p>
        </div>

        <div className="flex items-center gap-1 p-1 rounded-lg border border-[var(--border)] bg-[var(--card)] self-start">
          {(["1h", "24h", "7d", "30d"] as const).map((r) => (
            <button
              key={r}
              onClick={() => {
                lastRollupFetchRef.current = 0;
                setRange(r);
              }}
              className={`px-2.5 py-1 rounded text-xs font-mono cursor-pointer ${
                range === r
                  ? "bg-[var(--primary)] text-[var(--primary-fg)] font-medium"
                  : "text-[var(--muted)] hover:text-[var(--fg)]"
              }`}
            >
              {r}
            </button>
          ))}
        </div>
      </div>

      {metricsErr && (
        <div className="rounded-lg border border-[var(--error)]/40 bg-[var(--error)]/10 px-3.5 py-2 text-xs text-[var(--error)]">
          {metricsErr}
        </div>
      )}

      {/* 4 Charts */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <AreaChart
          title="CPU Utilization"
          unit="%"
          seriesA={cpuSeries}
          labelA="CPU"
          colorA="var(--chart-1)"
          timestamps={timestamps}
          fixedMax={100}
        />
        <AreaChart
          title="Memory Usage"
          unit="%"
          seriesA={ramSeries}
          labelA="RAM"
          colorA="var(--chart-2)"
          timestamps={timestamps}
          fixedMax={100}
        />
        <AreaChart
          title="Network Throughput"
          unit={netUnit}
          seriesA={netRxSeries}
          seriesB={netTxSeries}
          labelA="RX"
          labelB="TX"
          colorA="var(--chart-3)"
          colorB="var(--chart-5)"
          timestamps={timestamps}
        />
        <AreaChart
          title="Disk Utilization"
          unit="%"
          seriesA={diskSeries}
          labelA="Disk"
          colorA="var(--chart-6)"
          timestamps={timestamps}
          fixedMax={100}
        />
      </div>

      {/* Mounted Disks Strip */}
      {vps.disks.length > 0 && (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          {vps.disks.map((d) => {
            const pct = Math.max(0, Math.min(100, Number(d.used_percent) || 0));
            return (
              <div key={d.mount} className="rounded-lg border border-[var(--border)] bg-[var(--card)] p-3 text-xs space-y-1.5">
                <div className="flex items-center justify-between font-mono">
                  <span className="font-semibold text-[var(--fg)] truncate">{d.mount}</span>
                  <span className="text-[var(--muted)]">{d.fs}</span>
                </div>
                <div className="h-1.5 rounded-full bg-[var(--secondary)] overflow-hidden">
                  <div
                    className={`h-full rounded-full ${
                      pct >= vps.disk_warn ? "bg-[var(--error)]" : "bg-[var(--chart-6)]"
                    }`}
                    style={{ width: `${pct}%` }}
                  />
                </div>
                <div className="flex justify-between font-mono text-[var(--muted)]">
                  <span>
                    {formatBytes(d.used_bytes)} / {formatBytes(d.total_bytes)}
                  </span>
                  <span>{pct.toFixed(1)}%</span>
                </div>
              </div>
            );
          })}
        </div>
      )}

      {/* Detail Sub-tabs */}
      <div className="rounded-xl border border-[var(--border)] bg-[var(--card)] overflow-hidden">
        <div className="px-4 border-b border-[var(--border)] flex items-center gap-1 overflow-x-auto">
          {[
            ["probes", `External Probes (${vps.probes.length})`],
            ["docker", `Docker Containers (${vps.containers.length})`],
            ["systemd", `systemd Units (${vps.systemd_units.length})`],
            ["settings", "VPS Settings"],
          ].map(([id, label]) => (
            <button
              key={id}
              onClick={() => setTab(id as typeof tab)}
              className={`px-3 py-3 text-xs font-medium border-b-2 cursor-pointer whitespace-nowrap ${
                tab === id
                  ? "border-[var(--primary)] text-[var(--fg)]"
                  : "border-transparent text-[var(--muted)] hover:text-[var(--fg)]"
              }`}
            >
              {label}
            </button>
          ))}
        </div>

        <div className="p-4">
          {tab === "probes" && (
            <div className="space-y-3">
              <div className="flex items-center justify-between">
                <span className="text-xs text-[var(--muted)]">External health checks linked to {vps.name}</span>
                <button
                  onClick={() => onAddProbe(vps.id)}
                  className="inline-flex items-center gap-1 px-2.5 py-1 rounded text-xs font-medium bg-[var(--secondary)] border border-[var(--border)] cursor-pointer"
                >
                  <Plus className="w-3 h-3" /> Add Probe
                </button>
              </div>
              {vps.probes.length === 0 ? (
                <div className="text-xs text-[var(--muted)] py-2">No external probes linked to this VPS yet.</div>
              ) : (
                vps.probes.map((p) => (
                  <div
                    key={p.id}
                    className="rounded-lg border border-[var(--border)] bg-[var(--bg)] p-3.5 space-y-2.5"
                  >
                    <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className={`h-2.5 w-2.5 rounded-full ${probeStatusDot(p)}`} />
                        <span className="text-xs font-semibold">{p.name}</span>
                        <span className="px-1.5 py-0.5 rounded text-xs font-mono bg-[var(--secondary)] text-[var(--muted)]">
                          {p.type.toUpperCase()}
                        </span>
                        <span className="text-xs font-mono text-[var(--muted)]">{p.target}</span>
                      </div>
                      <div className="flex items-center gap-2.5 text-xs font-mono flex-wrap">
                        {p.tls_expiry_days !== null && (
                          <span
                            className={`px-2 py-0.5 rounded ${
                              p.tls_expiry_days < 14
                                ? "bg-[var(--warning)]/15 text-[var(--warning)]"
                                : "bg-[var(--success)]/15 text-[var(--success)]"
                            }`}
                          >
                            TLS {p.tls_expiry_days}d left
                          </span>
                        )}
                        <span>{p.status === "pending" ? "—" : `${p.latency_ms}ms`}</span>
                        <span className="text-[var(--success)]">24h {p.uptime_24h.toFixed(2)}%</span>
                        <span className="text-[var(--muted)]">30d {p.uptime_30d.toFixed(2)}%</span>
                        <button
                          onClick={() => handleCheckProbeNow(p.id)}
                          title="Run check now"
                          className="p-1 rounded border border-[var(--border)] hover:bg-[var(--secondary)] cursor-pointer"
                        >
                          <Play className="w-3 h-3" />
                        </button>
                        <button
                          onClick={() => handleDeleteProbe(p.id, p.name)}
                          title="Delete probe"
                          className="p-1 rounded border border-[var(--border)] text-[var(--error)] hover:bg-[var(--error)]/10 cursor-pointer"
                        >
                          <Trash2 className="w-3 h-3" />
                        </button>
                      </div>
                    </div>
                    <div className="flex items-center justify-between gap-3">
                      <HeartbeatBars ticks={p.ticks} count={36} />
                      <span className="text-xs text-[var(--muted)] shrink-0">
                        {p.last_error ? p.last_error : "Recent checks"}
                      </span>
                    </div>
                  </div>
                ))
              )}
            </div>
          )}

          {tab === "docker" && (
            <div className="space-y-3">
              <p className="text-xs text-[var(--muted)]">
                Tracked by container name across recreations. Clean Exited (0) containers do not alert unless pinned.
              </p>
              {vps.containers.length === 0 ? (
                <div className="text-xs text-[var(--muted)] py-2">
                  {vps.docker_available
                    ? "No Docker containers detected on this host."
                    : "Docker socket not detected on this VPS."}
                </div>
              ) : (
                <div className="overflow-x-auto">
                  <table className="w-full text-left border-collapse text-xs">
                    <thead>
                      <tr className="border-b border-[var(--border)] text-[var(--muted)]">
                        <th className="py-2 pr-3 font-medium">Container</th>
                        <th className="py-2 px-3 font-medium">State / Health</th>
                        <th className="py-2 px-3 font-medium">CPU / RAM</th>
                        <th className="py-2 px-3 font-medium">Restarts</th>
                        <th className="py-2 px-3 font-medium">Uptime</th>
                        <th className="py-2 pl-3 font-medium text-right">Policy</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-[var(--card-border)]">
                      {vps.containers.map((c) => {
                        const bad = isContainerUnhealthy(c);
                        const cleanExit = c.state === "exited" && c.exit_code === 0 && !c.pinned;
                        return (
                          <tr key={c.name} className={c.muted ? "opacity-55" : ""}>
                            <td className="py-2.5 pr-3">
                              <div className="font-medium">{c.name}</div>
                              <div className="font-mono text-[var(--muted)]">{c.image}</div>
                            </td>
                            <td className="py-2.5 px-3">
                              <span
                                className={`inline-flex px-2 py-0.5 rounded-full font-mono ${
                                  bad
                                    ? "bg-[var(--warning)]/15 text-[var(--warning)]"
                                    : cleanExit
                                      ? "bg-[var(--secondary)] text-[var(--muted)]"
                                      : "bg-[var(--success)]/15 text-[var(--success)]"
                                }`}
                              >
                                {c.state}
                                {c.state === "exited" ? ` (${c.exit_code})` : ""}
                                {c.health && c.health !== "none" ? ` · ${c.health}` : ""}
                              </span>
                            </td>
                            <td className="py-2.5 px-3 font-mono tabular-nums">
                              {c.cpu_percent.toFixed(1)}% · {formatBytes(c.mem_used_bytes)}
                            </td>
                            <td
                              className={`py-2.5 px-3 font-mono tabular-nums ${
                                c.restart_count > 0 ? "text-[var(--warning)] font-semibold" : ""
                              }`}
                            >
                              {c.restart_count}
                            </td>
                            <td className="py-2.5 px-3">
                              <div className="flex items-center gap-2">
                                <span className="font-mono tabular-nums">{c.uptime_24h.toFixed(1)}%</span>
                                <HeartbeatBars ticks={c.ticks} count={14} />
                              </div>
                            </td>
                            <td className="py-2.5 pl-3 text-right space-x-1.5 whitespace-nowrap">
                              <button
                                onClick={() => handleToggleContainer(c.name, { pinned: !c.pinned })}
                                className={`px-2 py-1 rounded border cursor-pointer ${
                                  c.pinned
                                    ? "bg-[var(--primary)]/15 text-[var(--primary)] border-[var(--primary)]/50"
                                    : "bg-[var(--bg)] border-[var(--border)] text-[var(--muted)]"
                                }`}
                              >
                                {c.pinned ? "Pinned" : "Pin"}
                              </button>
                              <button
                                onClick={() => handleToggleContainer(c.name, { muted: !c.muted })}
                                className={`px-2 py-1 rounded border cursor-pointer ${
                                  c.muted
                                    ? "bg-[var(--secondary)] text-[var(--fg)] border-[var(--border)]"
                                    : "bg-[var(--bg)] border-[var(--border)] text-[var(--muted)]"
                                }`}
                              >
                                {c.muted ? "Unmute" : "Mute"}
                              </button>
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          )}

          {tab === "systemd" && (
            <div className="space-y-4">
              {failedUnits.length > 0 && (
                <div className="rounded-lg border border-[var(--error)]/50 bg-[var(--error)]/10 px-3.5 py-2.5 text-xs">
                  <strong>Auto-surfaced failed unit(s):</strong>{" "}
                  <span className="font-mono">{failedUnits.map((u) => u.name).join(", ")}</span>
                </div>
              )}

              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2">
                <span className="text-xs text-[var(--muted)]">
                  Watched units sync to the agent inside its next heartbeat response.
                </span>
                <div className="flex items-center gap-2 flex-wrap">
                  {availableToAdd.length > 0 && (
                    <select
                      value={effectiveUnitChoice}
                      onChange={(e) => setUnitChoice(e.target.value)}
                      className="px-2.5 py-1.5 rounded text-xs font-mono bg-[var(--bg)] border border-[var(--border)]"
                    >
                      {availableToAdd.map((u) => (
                        <option key={u} value={u}>
                          {u}
                        </option>
                      ))}
                    </select>
                  )}
                  <input
                    value={customUnit}
                    onChange={(e) => setCustomUnit(e.target.value)}
                    placeholder="or custom unit.service"
                    className="px-2.5 py-1.5 rounded text-xs font-mono bg-[var(--bg)] border border-[var(--border)] w-44"
                  />
                  <button
                    onClick={handleAddWatchedUnit}
                    className="px-2.5 py-1.5 rounded text-xs font-medium bg-[var(--primary)] text-[var(--primary-fg)] cursor-pointer"
                  >
                    + Watch Unit
                  </button>
                </div>
              </div>

              {vps.systemd_units.length === 0 ? (
                <div className="text-xs text-[var(--muted)] py-2">
                  No watched or failed systemd units reported yet.
                </div>
              ) : (
                <div className="space-y-2">
                  {vps.systemd_units.map((u) => (
                    <div
                      key={u.name}
                      className="flex items-center justify-between gap-3 px-3.5 py-2.5 rounded-lg border border-[var(--border)] bg-[var(--bg)] text-xs"
                    >
                      <div className="flex items-center gap-2.5 min-w-0 flex-wrap">
                        <span
                          className={`h-2.5 w-2.5 rounded-full shrink-0 ${
                            u.active_state === "failed" || (u.watched && u.active_state !== "active")
                              ? "bg-[var(--error)]"
                              : "bg-[var(--success)]"
                          }`}
                        />
                        <span className="font-mono font-medium truncate">{u.name}</span>
                        <span
                          className={`px-1.5 py-0.5 rounded font-mono ${
                            u.active_state === "failed"
                              ? "bg-[var(--error)]/15 text-[var(--error)]"
                              : "bg-[var(--success)]/15 text-[var(--success)]"
                          }`}
                        >
                          {u.active_state} ({u.sub_state})
                        </span>
                        {u.auto_failed && (
                          <span className="px-1.5 py-0.5 rounded bg-[var(--warning)]/15 text-[var(--warning)]">
                            auto-detected
                          </span>
                        )}
                      </div>
                      <div className="flex items-center gap-3 shrink-0">
                        <span className="font-mono text-[var(--muted)]">
                          {u.mem_bytes > 0 ? formatBytes(u.mem_bytes) : "—"}
                        </span>
                        <HeartbeatBars ticks={u.ticks} count={16} />
                        {u.watched && (
                          <button
                            onClick={() => handleRemoveWatchedUnit(u.name)}
                            className="text-[var(--muted)] hover:text-[var(--error)] cursor-pointer"
                            title="Stop watching unit"
                          >
                            <Trash2 className="w-3.5 h-3.5" />
                          </button>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}

          {tab === "settings" && (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4 text-xs">
              <form
                onSubmit={handleSaveSettings}
                className="space-y-3 rounded-lg border border-[var(--border)] bg-[var(--bg)] p-4"
              >
                <h4 className="font-semibold">Metadata &amp; Alert Thresholds</h4>
                <div className="grid grid-cols-2 gap-2">
                  <div>
                    <label className="block text-[var(--muted)] mb-1">Name</label>
                    <input
                      required
                      value={name}
                      onChange={(e) => setName(e.target.value)}
                      className="w-full px-2.5 py-1.5 rounded bg-[var(--card)] border border-[var(--border)]"
                    />
                  </div>
                  <div>
                    <label className="block text-[var(--muted)] mb-1">Region / Tag</label>
                    <input
                      value={tag}
                      onChange={(e) => setTag(e.target.value)}
                      className="w-full px-2.5 py-1.5 rounded bg-[var(--card)] border border-[var(--border)]"
                    />
                  </div>
                </div>
                <div className="grid grid-cols-3 gap-2">
                  <div>
                    <label className="block text-[var(--muted)] mb-1">CPU % (3m)</label>
                    <input
                      required
                      type="number"
                      min={1}
                      max={100}
                      value={cpuWarn}
                      onChange={(e) => setCpuWarn(Number(e.target.value))}
                      className="w-full px-2.5 py-1.5 rounded bg-[var(--card)] border border-[var(--border)] font-mono"
                    />
                  </div>
                  <div>
                    <label className="block text-[var(--muted)] mb-1">RAM % (3m)</label>
                    <input
                      required
                      type="number"
                      min={1}
                      max={100}
                      value={ramWarn}
                      onChange={(e) => setRamWarn(Number(e.target.value))}
                      className="w-full px-2.5 py-1.5 rounded bg-[var(--card)] border border-[var(--border)] font-mono"
                    />
                  </div>
                  <div>
                    <label className="block text-[var(--muted)] mb-1">Disk %</label>
                    <input
                      required
                      type="number"
                      min={1}
                      max={100}
                      value={diskWarn}
                      onChange={(e) => setDiskWarn(Number(e.target.value))}
                      className="w-full px-2.5 py-1.5 rounded bg-[var(--card)] border border-[var(--border)] font-mono"
                    />
                  </div>
                </div>
                <button
                  disabled={busy}
                  type="submit"
                  className="px-3 py-1.5 rounded bg-[var(--primary)] text-[var(--primary-fg)] font-medium disabled:opacity-50 cursor-pointer"
                >
                  Save Settings
                </button>
                <p className="text-[var(--muted)]">
                  Alerts route to all enabled channels configured in Settings.
                </p>
              </form>

              <div className="space-y-3 rounded-lg border border-[var(--border)] bg-[var(--bg)] p-4 flex flex-col justify-between">
                <div className="space-y-2.5">
                  <h4 className="font-semibold">Agent Token &amp; Danger Zone</h4>
                  <p className="text-[var(--muted)]">
                    Rotating the token immediately revokes the previous token (401).
                  </p>
                  <button
                    disabled={busy}
                    type="button"
                    onClick={handleRotateToken}
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded bg-[var(--secondary)] border border-[var(--border)] disabled:opacity-50 cursor-pointer"
                  >
                    <RefreshCw className="w-3.5 h-3.5" /> Rotate Agent Token
                  </button>

                  {rotatedToken && (
                    <div className="p-2.5 rounded border border-[var(--border)] bg-[var(--card)] space-y-1.5">
                      <pre className="font-mono whitespace-pre-wrap break-all select-all">{installSnippet}</pre>
                      <button
                        type="button"
                        onClick={async () => {
                          const ok = await copyText(installSnippet);
                          if (ok) {
                            setCopied(true);
                            onToast("Install command copied");
                            if (copyTimerRef.current !== null) window.clearTimeout(copyTimerRef.current);
                            copyTimerRef.current = window.setTimeout(() => setCopied(false), 1800);
                          } else {
                            onToast("Copy failed — select and copy manually");
                          }
                        }}
                        className="inline-flex items-center gap-1 text-[var(--primary)] cursor-pointer"
                      >
                        {copied ? <Check className="w-3 h-3" /> : <Copy className="w-3 h-3" />}
                        {copied ? "Copied" : "Copy install command"}
                      </button>
                    </div>
                  )}
                </div>

                <div className="pt-3 border-t border-[var(--border)]">
                  <button
                    disabled={busy}
                    type="button"
                    onClick={handleDeleteVps}
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded bg-[var(--error)]/15 text-[var(--error)] hover:bg-[var(--error)]/25 disabled:opacity-50 cursor-pointer"
                  >
                    <Trash2 className="w-3.5 h-3.5" /> Delete VPS
                  </button>
                </div>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
