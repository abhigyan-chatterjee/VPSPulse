import { useState } from "react";
import { api, formatRelativeTime } from "../api/client";
import type { Incident, VPS } from "../types";

interface IncidentsPageProps {
  incidents: Incident[];
  vpsList: VPS[];
  onRefresh: () => void;
  onToast: (msg: string) => void;
}

export function IncidentsPage({ incidents, vpsList, onRefresh, onToast }: IncidentsPageProps) {
  const [filter, setFilter] = useState<"all" | "open" | "acknowledged" | "resolved">("all");
  const vpsNameMap = new Map(vpsList.map((v) => [v.id, v.name]));

  const filtered = incidents.filter((i) => (filter === "all" ? true : i.status === filter));

  const ack = async (id: string) => {
    try {
      await api.ackIncident(id);
      onRefresh();
      onToast("Incident acknowledged");
    } catch (err) {
      onToast(err instanceof Error ? err.message : "Failed to acknowledge");
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
        <div>
          <h1 className="text-base font-semibold">Incidents &amp; Alert Log</h1>
          <p className="text-xs text-[var(--muted)]">
            State-transition events across VPSes, containers, systemd units, and probes (retained 90 days).
          </p>
        </div>

        <div className="flex items-center gap-1.5">
          {(["all", "open", "acknowledged", "resolved"] as const).map((st) => (
            <button
              key={st}
              onClick={() => setFilter(st)}
              className={`px-3 py-1.5 rounded-md text-xs font-medium capitalize cursor-pointer border ${
                filter === st
                  ? "bg-[var(--primary)] text-[var(--primary-fg)] border-transparent"
                  : "bg-[var(--card)] text-[var(--muted)] border-[var(--border)]"
              }`}
            >
              {st}
            </button>
          ))}
        </div>
      </div>

      {filtered.length === 0 ? (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--card)] p-8 text-center text-xs text-[var(--muted)]">
          No incidents in this category.
        </div>
      ) : (
        <div className="space-y-2.5">
          {filtered.map((inc) => {
            const pill =
              inc.status === "open"
                ? "bg-[var(--warning)]/15 text-[var(--warning)]"
                : inc.status === "acknowledged"
                  ? "bg-[var(--secondary)] text-[var(--fg)]"
                  : "bg-[var(--success)]/15 text-[var(--success)]";
            const host = inc.vps_id ? vpsNameMap.get(inc.vps_id) || inc.vps_id : "External";
            return (
              <div
                key={inc.id}
                className="rounded-xl border border-[var(--border)] bg-[var(--card)] p-4 flex flex-col sm:flex-row sm:items-center justify-between gap-3"
              >
                <div className="space-y-1">
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className={`px-2 py-0.5 rounded-full text-xs font-medium ${pill}`}>{inc.status}</span>
                    <span className="text-xs font-semibold">{inc.title}</span>
                    <span className="text-xs font-mono text-[var(--muted)]">{host}</span>
                  </div>
                  <p className="text-xs text-[var(--muted)]">{inc.detail}</p>
                </div>
                <div className="flex items-center gap-2 shrink-0">
                  <span className="text-xs font-mono text-[var(--muted)]">
                    {formatRelativeTime(inc.updated_at || inc.created_at)}
                  </span>
                  {inc.status === "open" && (
                    <button
                      onClick={() => ack(inc.id)}
                      className="px-2.5 py-1 rounded text-xs bg-[var(--secondary)] border border-[var(--border)] cursor-pointer"
                    >
                      Acknowledge
                    </button>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
