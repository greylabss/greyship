export function Sparkline({ values, label, className }: { values: number[]; label: string; className?: string }) {
  const width = 120;
  const height = 28;
  const max = Math.max(...values, 1);
  const step = width / Math.max(values.length - 1, 1);
  const points = values.map((v, i) => `${(i * step).toFixed(1)},${(height - 2 - (v / max) * (height - 4)).toFixed(1)}`).join(" ");

  return (
    <svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" role="img" aria-label={label} className={className}>
      <polyline points={points} fill="none" stroke="var(--gs-text-muted)" strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
    </svg>
  );
}
