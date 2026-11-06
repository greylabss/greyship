import { cx } from "@/lib/format";

export function Meter({ value, max, label, className }: { value: number; max: number; label: string; className?: string }) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0;
  const full = pct >= 100;
  return (
    <div
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={max}
      aria-valuenow={value}
      className={cx("h-1.5 w-full overflow-hidden rounded-full bg-surface", className)}
    >
      <div className={cx("h-full rounded-full", full ? "bg-fg" : "bg-muted")} style={{ width: `${pct}%` }} />
    </div>
  );
}
