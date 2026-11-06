import { cx } from "@/lib/format";

export type Tone = "ok" | "error" | "progress" | "idle" | "warn";

const dot: Record<Tone, string> = {
  ok: "bg-ok",
  error: "bg-error",
  progress: "bg-accent",
  idle: "bg-dim",
  warn: "bg-warn",
};

// Colour is never the only signal: a dot always comes with its label (design.md §7.9).
export function Status({ tone, label, detail, className }: { tone: Tone; label: string; detail?: string; className?: string }) {
  return (
    <span className={cx("inline-flex items-center gap-2", className)}>
      <span className={cx("size-2 shrink-0 rounded-full", dot[tone], tone === "progress" && "animate-pulse")} aria-hidden="true" />
      <span className="text-fg">{label}</span>
      {detail && <span className="text-muted">{detail}</span>}
    </span>
  );
}

export function AlwaysOnBadge() {
  return (
    <span className="inline-flex h-5 items-center rounded-control border border-border-strong px-1.5 text-12 text-muted">
      always on · billed 24/7
    </span>
  );
}
