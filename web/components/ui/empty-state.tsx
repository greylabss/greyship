import type { ReactNode } from "react";
import { CommandHint } from "./command-hint";

// What this is, the one action to take, and the matching CLI command (design.md §7.6).
export function EmptyState({
  title,
  body,
  action,
  command,
}: {
  title: string;
  body: string;
  action?: ReactNode;
  command?: string;
}) {
  return (
    <div className="flex flex-col items-start gap-4 rounded-panel border border-dashed border-border-strong px-6 py-10 md:items-center md:text-center">
      <div className="flex max-w-md flex-col gap-1">
        <h2 className="text-16 font-semibold">{title}</h2>
        <p className="text-muted">{body}</p>
      </div>
      {action}
      {command && <CommandHint command={command} />}
    </div>
  );
}
