import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

// Badges are for status and classification only (design.md §6.1).
export function Badge({ children, tone = "neutral", mono, className }: { children: ReactNode; tone?: "neutral" | "ok" | "error" | "progress"; mono?: boolean; className?: string }) {
  return (
    <span
      className={cn(
        "inline-flex h-5 shrink-0 items-center gap-1 rounded-control border px-1.5 text-12 leading-none",
        tone === "neutral" && "border-border-strong text-muted",
        tone === "ok" && "border-ok/40 text-ok",
        tone === "error" && "border-error/40 text-error",
        tone === "progress" && "border-accent/40 text-accent",
        mono && "font-mono",
        className,
      )}
    >
      {children}
    </span>
  );
}
