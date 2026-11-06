import Link from "next/link";
import type { ActivityItem, DeploymentStatus } from "@/lib/api/types";
import { icons } from "@/components/ui/icons";
import { Status, type Tone } from "@/components/ui/status";
import { cx } from "@/lib/format";

const status: Record<DeploymentStatus, { tone: Tone; label: string }> = {
  queued: { tone: "idle", label: "Queued" },
  building: { tone: "progress", label: "Building" },
  ready: { tone: "ok", label: "Ready" },
  failed: { tone: "error", label: "Failed" },
  superseded: { tone: "idle", label: "Superseded" },
};

export function ActivityList({ team, items, dense = false }: { team: string; items: ActivityItem[]; dense?: boolean }) {
  return (
    <ul className="divide-y divide-border rounded-panel border border-border">
      {items.map((d) => {
        const s = status[d.status];
        return (
          <li key={d.id}>
            <Link
              href={`/${team}/${d.project}/deployments/${d.id}`}
              className={cx(
                "flex flex-col gap-1.5 px-4 transition-colors duration-150 ease-out hover:bg-surface-2",
                dense ? "py-3" : "py-3.5 md:grid md:grid-cols-[160px_minmax(0,1fr)_200px_96px_88px] md:items-center md:gap-4",
              )}
            >
              {dense ? (
                <>
                  <div className="flex items-center justify-between gap-3">
                    <span className="truncate font-medium">{d.project}</span>
                    <Status tone={s.tone} label={s.label} className="shrink-0 text-13" />
                  </div>
                  <span className="truncate text-13 text-fg">{d.message}</span>
                  {d.failure && <span className="truncate font-mono text-12 text-error">{d.failure}</span>}
                  <span className="flex min-w-0 items-center gap-2 text-12 text-muted">
                    <icons.branch className="shrink-0 text-dim" />
                    <span className="truncate font-mono">{d.branch}</span>
                    <span className="font-mono">{d.sha}</span>
                    {d.env === "preview" && <span className="rounded-control border border-border-strong px-1">preview</span>}
                    <span className="ml-auto shrink-0">{d.ago}</span>
                  </span>
                </>
              ) : (
                <>
                  <span className="truncate font-medium">{d.project}</span>
                  <span className="flex min-w-0 flex-col">
                    <span className="truncate">{d.message}</span>
                    {d.failure && <span className="truncate font-mono text-12 text-error">{d.failure}</span>}
                  </span>
                  <span className="flex min-w-0 items-center gap-2 text-12 text-muted">
                    <icons.branch className="shrink-0 text-dim" />
                    <span className="truncate font-mono">{d.branch}</span>
                    <span className="font-mono">{d.sha}</span>
                    {d.env === "preview" && <span className="rounded-control border border-border-strong px-1">preview</span>}
                  </span>
                  <Status tone={s.tone} label={s.label} className="text-13" />
                  <span className="text-13 text-muted md:text-right">
                    {d.ago}
                    {d.duration && <span className="block font-mono text-12 text-dim">{d.duration}</span>}
                  </span>
                </>
              )}
            </Link>
          </li>
        );
      })}
    </ul>
  );
}
