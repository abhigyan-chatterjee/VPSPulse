interface AreaChartProps {
  title: string;
  unit: string;
  seriesA: number[];
  seriesB?: number[];
  labelA: string;
  labelB?: string;
  colorA: string;
  colorB?: string;
  timestamps?: number[];
  fixedMax?: number;
}

function sanitizeSeries(vals: number[]): number[] {
  return vals.map((v) => (Number.isFinite(v) ? Math.max(0, v) : 0));
}

function formatAxisTime(tsSec: number, multiDay: boolean): string {
  const d = new Date(tsSec * 1000);
  if (multiDay) {
    return d.toLocaleDateString([], { month: "short", day: "numeric" }) + " " + d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

export function AreaChart({
  title,
  unit,
  seriesA,
  seriesB,
  labelA,
  labelB,
  colorA,
  colorB = "var(--chart-4)",
  timestamps,
  fixedMax,
}: AreaChartProps) {
  const w = 440;
  const h = 115;
  const padL = 6;
  const padR = 6;
  const padT = 8;
  const padB = 8;

  const safeA = sanitizeSeries(seriesA);
  const safeB = seriesB && seriesB.length > 0 ? sanitizeSeries(seriesB) : undefined;
  const hasData = safeA.length > 0;

  const allVals = hasData ? [...safeA, ...(safeB || [0])] : [0];
  const maxVal = fixedMax ?? Math.max(...allVals, 0.1) * 1.15;

  const buildPts = (vals: number[]) =>
    vals.map((v, i) => {
      const x = vals.length === 1 ? w - padR : padL + (i / (vals.length - 1)) * (w - padL - padR);
      const y = h - padB - (Math.min(v, maxVal) / Math.max(maxVal, 0.0001)) * (h - padT - padB);
      return [x, y] as const;
    });

  const latestA = hasData ? safeA[safeA.length - 1] : null;
  const latestB = safeB && safeB.length > 0 ? safeB[safeB.length - 1] : null;
  const fmt = (n: number) => (n >= 100 ? n.toFixed(0) : n.toFixed(1));

  const multiDay =
    timestamps && timestamps.length > 1 ? timestamps[timestamps.length - 1] - timestamps[0] > 86400 : false;
  const startLabel = timestamps && timestamps.length > 1 ? formatAxisTime(timestamps[0], multiDay) : "";
  const endLabel =
    timestamps && timestamps.length > 1 ? formatAxisTime(timestamps[timestamps.length - 1], multiDay) : "";

  const ptsA = hasData ? buildPts(safeA) : [];
  const lineA = ptsA.map((p) => `${p[0].toFixed(1)},${p[1].toFixed(1)}`).join(" ");
  const areaA = `${padL},${h - padB} ${lineA} ${w - padR},${h - padB}`;
  const lineB =
    safeB && safeB.length > 1
      ? buildPts(safeB)
          .map((p) => `${p[0].toFixed(1)},${p[1].toFixed(1)}`)
          .join(" ")
      : null;

  return (
    <div className="rounded-xl border border-[var(--border)] bg-[var(--card)] p-4">
      <div className="flex items-center justify-between gap-2 mb-2">
        <span className="text-xs font-medium text-[var(--fg)]">{title}</span>
        <div className="flex items-center gap-3 text-xs font-mono tabular-nums">
          <span className="flex items-center gap-1.5 text-[var(--fg)]">
            <span className="h-2 w-2 rounded-full" style={{ background: colorA }} />
            {labelA}: {latestA !== null ? `${fmt(latestA)}${unit}` : "—"}
          </span>
          {safeB && latestB !== null && (
            <span className="flex items-center gap-1.5 text-[var(--muted)]">
              <span className="h-2 w-2 rounded-full" style={{ background: colorB }} />
              {labelB}: {fmt(latestB)}
              {unit}
            </span>
          )}
        </div>
      </div>

      {!hasData ? (
        <div className="h-28 flex items-center justify-center text-xs text-[var(--muted)] border border-dashed border-[var(--border)] rounded-lg">
          No metrics collected for this range yet
        </div>
      ) : (
        <svg viewBox={`0 0 ${w} ${h}`} className="w-full h-28 overflow-hidden">
          <line x1={padL} y1={h * 0.25} x2={w - padR} y2={h * 0.25} stroke="var(--border)" strokeDasharray="2 3" />
          <line x1={padL} y1={h * 0.5} x2={w - padR} y2={h * 0.5} stroke="var(--border)" strokeDasharray="2 3" />
          <line x1={padL} y1={h * 0.75} x2={w - padR} y2={h * 0.75} stroke="var(--border)" strokeDasharray="2 3" />
          {ptsA.length === 1 ? (
            <>
              <line x1={padL} y1={ptsA[0][1]} x2={w - padR} y2={ptsA[0][1]} stroke={colorA} strokeWidth={1.5} strokeOpacity={0.5} />
              <circle cx={ptsA[0][0]} cy={ptsA[0][1]} r={3} fill={colorA} />
            </>
          ) : (
            <>
              <polygon fill={colorA} fillOpacity={0.14} points={areaA} />
              <polyline
                fill="none"
                stroke={colorA}
                strokeWidth={2}
                strokeLinecap="round"
                strokeLinejoin="round"
                points={lineA}
              />
              {lineB && (
                <polyline fill="none" stroke={colorB} strokeWidth={1.75} strokeDasharray="4 3" points={lineB} />
              )}
            </>
          )}
        </svg>
      )}

      {(startLabel || endLabel) && (
        <div className="flex items-center justify-between text-xs font-mono text-[var(--muted)] mt-1">
          <span>{startLabel}</span>
          <span>{endLabel}</span>
        </div>
      )}
    </div>
  );
}
