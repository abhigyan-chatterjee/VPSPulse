interface SparklineProps {
  values: number[];
  color?: string;
}

export function Sparkline({ values, color = "var(--chart-1)" }: SparklineProps) {
  const w = 88;
  const h = 24;
  const clean = values.map((v) => (Number.isFinite(v) ? Math.max(0, v) : 0));

  if (clean.length === 0) {
    return (
      <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} className="shrink-0">
        <line x1={0} y1={h - 3} x2={w} y2={h - 3} stroke="var(--border)" strokeDasharray="2 3" />
      </svg>
    );
  }

  const max = Math.max(...clean, 100);
  if (clean.length === 1) {
    const y = h - (clean[0] / max) * (h - 4) - 2;
    return (
      <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} className="shrink-0">
        <line x1={0} y1={y} x2={w} y2={y} stroke={color} strokeWidth={1.5} strokeOpacity={0.6} />
        <circle cx={w} cy={y} r={2.2} fill={color} />
      </svg>
    );
  }

  const poly = clean
    .map((v, idx) => {
      const x = (idx / (clean.length - 1)) * w;
      const y = h - (v / max) * (h - 4) - 2;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");

  return (
    <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} className="shrink-0 overflow-visible">
      <polyline
        fill="none"
        stroke={color}
        strokeWidth={1.75}
        strokeLinecap="round"
        strokeLinejoin="round"
        points={poly}
      />
    </svg>
  );
}
