import { cx } from "@/lib/format";
import { CopyButton } from "./copy-button";

// The matching `grey` command next to a dashboard action (design.md §1.5). On real data the dashboard shows only
// what it needs to, so the hints are kept for the sample-data design mode.
export function CommandHint({ command, className }: { command: string; className?: string }) {
  if (process.env.NEXT_PUBLIC_API_ENABLED === "1") return null;
  return (
    <span className={cx("inline-flex min-w-0 items-center gap-1 text-dim", className)}>
      <code className="truncate font-mono text-12 text-muted">{command}</code>
      <CopyButton value={command} label="Copy command" />
    </span>
  );
}
