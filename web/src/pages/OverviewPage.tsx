import { useEffect, useMemo, useState } from "react";
import { Play, Plus, Search, Trash2 } from "lucide-react";
import { api } from "../api/client";
import type { Incident, Probe, VPS } from "../types";
import { HeartbeatBars } from "../components/HeartbeatBars";
import { VpsCard, isContainerUnhealthy, probeStatusDot, vpsHasIssue } from "../components/VpsCard";

interface OverviewPageProps {
  vpsList: VPS[];
  standaloneProbes: Probe[];
  incidents: Incident[];
  onSelectVps: (id: string) => void;
  onAddStandaloneProbe: () => void;
  onAddVps: () => void;
  onProbeUpdated: (probe: Probe) => void;
  onProbeDeleted: (id: string) => void;
  onToast: (msg: string) => void;
}

export function OverviewPage({
  vpsList,
  standaloneProbes,
  incidents,
  onSelectVps,
  onAddStandaloneProbe,
  onAddVps,
  onProbeUpdated,
  onProbeDeleted,
  onToast,
}: OverviewPageProps) {
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState("all");
  const [busyProbeId, setBusyProbeId] = useState<string | null>(null);

  const tags = useMemo(
    () => Array.from(new Set(vpsList.map((v) => v.tag).filter(Boolean))),
    [vpsList],
  );

  useEffect(() => {
    if (filter !== "all" && filter !== "issues" && !tags.includes(filter)) {
      setFilter("all");
    }
  }, [filter, tags]);

  const onlineCount = vpsList.filter((v) => v.status === "online").length;
  const issueVpsCount = vpsList.filter(vpsHasIssue).length;
  const openIncidents = incidents.filter((i) => i.status === "open").length;

  const { totalServices, upServices, avgUptime } = useMemo(() => {
    let total = standaloneProbes.length;
    let down = standaloneProbes.filter((p) => p.status === "down").length;
    const uptimes: number[] = standaloneProbes.map((p) => p.uptime_30d);

    for (const v of vpsList) {
      for (const p of v.probes) {
        total++;
        if (p.status === "down") down++;
        uptimes.push(p.uptime_30d);
      }
      for (const c of v.containers) {
        if (c.muted) continue;
        total++;
        if (isContainerUnhealthy(c)) down++;
        uptimes.push(c.uptime_24h);
      }
      for (const u of v.systemd_units) {
        total++;
        if (u.active_state === "failed" || (u.watched && u.active_state !== "active")) down++;
        uptimes.push(u.uptime_24h);
      }
    }
    const avg = uptimes.length > 0 ? uptimes.reduce((a, b) => a + b, 0) / uptimes.length : null;
    return { totalServices: total, upServices: total - down, avgUptime: avg };
  }, [vpsList, standaloneProbes]);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return vpsList.filter((v) => {
      if (q) {
        const hitHost =
          v.name.toLowerCase().includes(q) ||
          v.tag.toLowerCase().includes(q) ||
          v.ip.toLowerCase().includes(q);
        const hitSvc =
          v.probes.some((p) => p.name.toLowerCase().includes(q)) ||
          v.containers.some((c) => c.name.toLowerCase().includes(q)) ||
          v.systemd_units.some((u) => u.name.toLowerCase().includes(q));
        if (!hitHost && !hitSvc) return false;
      }
      if (filter === "issues") return vpsHasIssue(v);
      if (filter !== "all") return v.tag === filter;
      return true;
    });
  }, [vpsList, search, filter]);

  const handleCheckStandalone = async (probe: Probe) => {
    setBusyProbeId(probe.id);
    try {
      const res = await api.checkProbeNow(probe.id);
      if (res.probe) onProbeUpdated(res.probe);
      onToast(`Checked ${probe.name}`);
    } catch (err) {
      onToast(err instanceof Error ? err.message : "Probe check failed");
    } finally {
      setBusyProbeId(null);
    }
  };

  const handleDeleteStandalone = async (probe: Probe) => {
    if (!confirm(`Delete external probe "${probe.name}"?`)) return;
    setBusyProbeId(probe.id);
    try {
      await api.deleteProbe(probe.id);
      onProbeDeleted(probe.id);
      onToast(`Deleted probe ${probe.name}`);
    } catch (err) {
      onToast(err instanceof Error ? err.message : "Failed to delete probe");
    } finally {
      setBusyProbeId(null);
    }
  };

  return (
    <div className="space-y-5">
      {/* KPI Strip */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <div className="rounded-lg border border-[var(--border)] bg-[var(--card)] p-4">
          <div className="flex items-center justify-between">
            <span className="text-xs text-[var(--muted)]">VPSes Online</span>
            <span
              className={`h-2 w-2 rounded-full ${
                vpsList.length === 0
                  ? "bg-[var(--muted)]"
                  : onlineCount === vpsList.length
                    ? "bg-[var(--success)]"
                    : "bg-[var(--warning)]"
              }`}
            />
          </div>
          <div className="mt-2 flex items-baseline gap-2">
            <span className="text-xl font-semibold tabular-nums">
              {onlineCount} / {vpsList.length}
            </span>
            <span className="text-xs text-[var(--muted)]">
              {vpsList.length === 0 ? "none added" : onlineCount === vpsList.length ? "all reachable" : "attention"}
            </span>
          </div>
        </div>

        <div className="rounded-lg border border-[var(--border)] bg-[var(--card)] p-4">
          <div className="flex items-center justify-between">
            <span className="text-xs text-[var(--muted)]">Services Healthy</span>
            <span
              className={`h-2 w-2 rounded-full ${
                totalServices === 0
                  ? "bg-[var(--muted)]"
                  : upServices === totalServices
                    ? "bg-[var(--success)]"
                    : "bg-[var(--warning)]"
              }`}
            />
          </div>
          <div className="mt-2 flex items-baseline gap-2">
            <span className="text-xl font-semibold tabular-nums">
              {upServices} / {totalServices}
            </span>
            <span className="text-xs text-[var(--muted)]">probes · docker · systemd</span>
          </div>
        </div>

        <div className="rounded-lg border border-[var(--border)] bg-[var(--card)] p-4">
          <div className="flex items-center justify-between">
            <span className="text-xs text-[var(--muted)]">Open Incidents</span>
            <span
              className={`h-2 w-2 rounded-full ${
                openIncidents > 0 ? "bg-[var(--warning)]" : "bg-[var(--success)]"
              }`}
            />
          </div>
          <div className="mt-2 flex items-baseline gap-2">
            <span className="text-xl font-semibold tabular-nums">{openIncidents}</span>
            <span className={`text-xs ${openIncidents > 0 ? "text-[var(--warning)]" : "text-[var(--muted)]"}`}>
              {openIncidents > 0 ? `${issueVpsCount} host(s) affected` : "All clear"}
            </span>
          </div>
        </div>

        <div className="rounded-lg border border-[var(--border)] bg-[var(--card)] p-4">
          <div className="flex items-center justify-between">
            <span className="text-xs text-[var(--muted)]">Avg Uptime</span>
            <span className="text-xs font-mono text-[var(--muted)]">24h / 30d</span>
          </div>
          <div className="mt-2 flex items-baseline gap-2">
            <span className="text-xl font-semibold tabular-nums">
              {avgUptime !== null ? `${avgUptime.toFixed(2)}%` : "—"}
            </span>
            <span className="text-xs text-[var(--muted)]">across all checks</span>
          </div>
        </div>
      </div>

      {/* Filter & Search Bar */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
        <div className="flex items-center flex-wrap gap-1.5">
          <button
            onClick={() => setFilter("all")}
            className={`px-3 py-1.5 rounded-md text-xs font-medium cursor-pointer border ${
              filter === "all"
                ? "bg-[var(--primary)] text-[var(--primary-fg)] border-transparent"
                : "bg-[var(--card)] text-[var(--muted)] border-[var(--border)] hover:text-[var(--fg)]"
            }`}
          >
            All ({vpsList.length})
          </button>
          <button
            onClick={() => setFilter("issues")}
            className={`px-3 py-1.5 rounded-md text-xs font-medium cursor-pointer border ${
              filter === "issues"
                ? "bg-[var(--warning)] text-[var(--primary-fg)] border-transparent"
                : "bg-[var(--card)] text-[var(--muted)] border-[var(--border)] hover:text-[var(--fg)]"
            }`}
          >
            Issues ({issueVpsCount})
          </button>
          {tags.map((t) => (
            <button
              key={t}
              onClick={() => setFilter(t)}
              className={`px-3 py-1.5 rounded-md text-xs font-medium cursor-pointer border ${
                filter === t
                  ? "bg-[var(--secondary)] text-[var(--fg)] border-[var(--primary)]"
                  : "bg-[var(--card)] text-[var(--muted)] border-[var(--border)] hover:text-[var(--fg)]"
              }`}
            >
              {t}
            </button>
          ))}
        </div>

        <div className="relative w-full sm:w-72">
          <Search className="w-3.5 h-3.5 absolute left-3 top-1/2 -translate-y-1/2 text-[var(--muted)]" />
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search VPS, IP, container, or unit..."
            className="w-full pl-8 pr-3 py-1.5 rounded-md text-xs bg-[var(--card)] border border-[var(--border)] focus:outline-none focus:border-[var(--primary)]"
          />
        </div>
      </div>

      {/* VPS Grid */}
      {vpsList.length === 0 ? (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--card)] p-10 text-center space-y-3">
          <p className="text-sm font-medium">No VPSes connected yet</p>
          <p className="text-xs text-[var(--muted)]">
            Add your first VPS to get a one-line install command and live telemetry.
          </p>
          <button
            onClick={onAddVps}
            className="inline-flex items-center gap-1.5 px-3.5 py-2 rounded-md text-xs font-medium bg-[var(--primary)] text-[var(--primary-fg)] cursor-pointer"
          >
            <Plus className="w-3.5 h-3.5" /> Add VPS
          </button>
        </div>
      ) : filtered.length === 0 ? (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--card)] p-8 text-center text-xs text-[var(--muted)]">
          No VPSes matched your current search or filter.
        </div>
      ) : (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {filtered.map((v) => (
            <VpsCard key={v.id} vps={v} onSelect={onSelectVps} />
          ))}
        </div>
      )}

      {/* Standalone External Services */}
      <div className="rounded-xl border border-[var(--border)] bg-[var(--card)] p-4">
        <div className="flex items-center justify-between mb-3">
          <span className="text-xs font-semibold uppercase tracking-wider text-[var(--muted)]">
            External Services (Standalone Probes)
          </span>
          <button
            onClick={onAddStandaloneProbe}
            className="inline-flex items-center gap-1 px-2.5 py-1 rounded text-xs font-medium bg-[var(--secondary)] border border-[var(--border)] hover:opacity-90 cursor-pointer"
          >
            <Plus className="w-3 h-3" /> Add Probe
          </button>
        </div>
        {standaloneProbes.length === 0 ? (
          <div className="text-xs text-[var(--muted)]">No standalone external probes configured.</div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-2.5">
            {standaloneProbes.map((p) => (
              <div
                key={p.id}
                title={p.last_error || p.target}
                className="flex items-center justify-between gap-2 px-3 py-2 rounded-lg bg-[var(--bg)] border border-[var(--border)]"
              >
                <div className="flex items-center gap-2 min-w-0">
                  <span className={`h-2 w-2 rounded-full shrink-0 ${probeStatusDot(p)}`} />
                  <span className="text-xs font-medium truncate">{p.name}</span>
                  <span className="px-1.5 py-0.5 rounded text-xs font-mono bg-[var(--secondary)] text-[var(--muted)] shrink-0">
                    {p.type.toUpperCase()}
                  </span>
                  {p.tls_expiry_days !== null && p.tls_expiry_days < 14 && (
                    <span className="px-1.5 py-0.5 rounded text-xs font-mono bg-[var(--warning)]/15 text-[var(--warning)] shrink-0">
                      TLS {p.tls_expiry_days}d
                    </span>
                  )}
                </div>
                <div className="flex items-center gap-2 shrink-0">
                  <span className="text-xs font-mono tabular-nums text-[var(--muted)]">
                    {p.status === "pending" ? "—" : `${p.latency_ms}ms`}
                  </span>
                  <span className="text-xs font-mono tabular-nums text-[var(--success)]">
                    {p.uptime_30d.toFixed(2)}%
                  </span>
                  <div className="hidden sm:block">
                    <HeartbeatBars ticks={p.ticks} count={14} />
                  </div>
                  <button
                    disabled={busyProbeId === p.id}
                    onClick={() => handleCheckStandalone(p)}
                    title="Run check now"
                    className="p-1 rounded border border-[var(--border)] hover:bg-[var(--secondary)] disabled:opacity-50 cursor-pointer"
                  >
                    <Play className="w-3 h-3" />
                  </button>
                  <button
                    disabled={busyProbeId === p.id}
                    onClick={() => handleDeleteStandalone(p)}
                    title="Delete probe"
                    className="p-1 rounded border border-[var(--border)] text-[var(--error)] hover:bg-[var(--error)]/10 disabled:opacity-50 cursor-pointer"
                  >
                    <Trash2 className="w-3 h-3" />
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
