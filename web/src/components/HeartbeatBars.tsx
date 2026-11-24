import type { ServiceTick } from "../types";

interface HeartbeatBarsProps {
  ticks: ServiceTick[];
  count?: number;
}

export function HeartbeatBars({ ticks, count = 20 }: HeartbeatBarsProps) {
  const slice = ticks.slice(-count);
  const pad = Math.max(0, count - slice.length);

  return (
    <div className="flex items-center gap-0.5" aria-label="Heartbeat history">
      {Array.from({ length: pad }).map((_, idx) => (
        <span
          key={`empty-${idx}`}
          className="w-1 h-3.5 rounded-[1px] bg-[var(--secondary)] opacity-50 inline-block"
          title="No data yet"
        />
      ))}
      {slice.map((t, idx) => {
        const cls =
          t.status === "up"
            ? "bg-[var(--success)] opacity-85"
            : t.status === "warn" || t.status === "degraded"
              ? "bg-[var(--warning)] opacity-95"
              : "bg-[var(--error)] opacity-95";
        const timeStr = new Date(t.ts * 1000).toLocaleTimeString([], {
          hour: "2-digit",
          minute: "2-digit",
          second: "2-digit",
        });
        const label =
          t.status === "down"
            ? `${timeStr} · DOWN`
            : `${timeStr} · ${t.status.toUpperCase()}${t.latency_ms ? ` (${t.latency_ms}ms)` : ""}`;
        return (
          <span
            key={`${t.ts}-${idx}`}
            title={label}
            className={`tick-bar w-1 h-3.5 rounded-[1px] ${cls} inline-block`}
          />
        );
      })}
    </div>
  );
}
