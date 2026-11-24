interface SparklineProps {
  values: number[];
  color?: string;
}

export function Sparkline({ values, color = "var(--chart-1)" }: SparklineProps) {
  const w = 88;
  const h = 24;
  const pts = values.length > 0 ? values : [0, 0];
  const min = 0;
  const max = Math.max(...pts, 100);
  const poly = pts
    .map((v, idx) => {
      const x = (idx / Math.max(pts.length - 1, 1)) * w;
      const y = h - ((v - min) / Math.max(max - min, 1)) * (h - 4) - 2;
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
