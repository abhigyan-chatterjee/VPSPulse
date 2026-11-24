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

  const safeA = seriesA.length > 0 ? seriesA : [0];
  const safeB = seriesB && seriesB.length > 0 ? seriesB : undefined;
  const allVals = [...safeA, ...(safeB || [0])];
  const maxVal = fixedMax ?? Math.max(...allVals, 10) * 1.15;

  const buildPts = (vals: number[]) =>
    vals.map((v, i) => {
      const x = padL + (i / Math.max(vals.length - 1, 1)) * (w - padL - padR);
      const y = h - padB - (Math.min(v, maxVal) / Math.max(maxVal, 0.001)) * (h - padT - padB);
      return [x, y] as const;
    });

  const ptsA = buildPts(safeA);
  const lineA = ptsA.map((p) => `${p[0].toFixed(1)},${p[1].toFixed(1)}`).join(" ");
  const areaA = `${padL},${h - padB} ${lineA} ${w - padR},${h - padB}`;

  const lineB = safeB
    ? buildPts(safeB)
        .map((p) => `${p[0].toFixed(1)},${p[1].toFixed(1)}`)
        .join(" ")
    : null;

  const latestA = safeA[safeA.length - 1];
  const latestB = safeB ? safeB[safeB.length - 1] : null;
  const fmt = (n: number) => (n >= 100 ? n.toFixed(0) : n.toFixed(1));

  const startLabel =
    timestamps && timestamps.length > 1
      ? new Date(timestamps[0] * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
      : "";
  const endLabel =
    timestamps && timestamps.length > 1
      ? new Date(timestamps[timestamps.length - 1] * 1000).toLocaleTimeString([], {
          hour: "2-digit",
          minute: "2-digit",
        })
      : "";

  return (
    <div className="rounded-xl border border-[var(--border)] bg-[var(--card)] p-4">
      <div className="flex items-center justify-between gap-2 mb-2">
        <span className="text-xs font-medium text-[var(--fg)]">{title}</span>
        <div className="flex items-center gap-3 text-xs font-mono tabular-nums">
          <span className="flex items-center gap-1.5 text-[var(--fg)]">
            <span className="h-2 w-2 rounded-full" style={{ background: colorA }} />
            {labelA}: {fmt(latestA)}
            {unit}
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
      <svg viewBox={`0 0 ${w} ${h}`} className="w-full h-28 overflow-visible">
        <line x1={padL} y1={h * 0.25} x2={w - padR} y2={h * 0.25} stroke="var(--border)" strokeDasharray="2 3" />
        <line x1={padL} y1={h * 0.5} x2={w - padR} y2={h * 0.5} stroke="var(--border)" strokeDasharray="2 3" />
        <line x1={padL} y1={h * 0.75} x2={w - padR} y2={h * 0.75} stroke="var(--border)" strokeDasharray="2 3" />
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
      </svg>
      {(startLabel || endLabel) && (
        <div className="flex items-center justify-between text-xs font-mono text-[var(--muted)] mt-1">
          <span>{startLabel}</span>
          <span>{endLabel}</span>
        </div>
      )}
    </div>
  );
}
