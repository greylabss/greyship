import Link from "next/link";
import type { ProjectHealth, ProjectSummary } from "@/lib/api/types";
import { compact } from "@/lib/format";
import { icons } from "@/components/ui/icons";
import { ProjectTile } from "@/components/ui/project-tile";
import { Sparkline } from "@/components/ui/sparkline";
import { Status, type Tone } from "@/components/ui/status";

export const health: Record<ProjectHealth, { tone: Tone; label: string }> = {
  ready: { tone: "ok", label: "Ready" },
  building: { tone: "progress", label: "Building" },
  failed: { tone: "error", label: "Failed" },
  crashing: { tone: "error", label: "Crashing" },
  sleeping: { tone: "idle", label: "Sleeping" },
};

export function ProjectCard({ team, project, metrics = true }: { team: string; project: ProjectSummary; metrics?: boolean }) {
  const { tone, label } = health[project.health];
  const { lastDeploy } = project;

  return (
    <Link
      href={`/${team}/${project.slug}`}
      className="group flex flex-col gap-4 rounded-panel border border-border bg-surface-2 p-4 transition-colors duration-150 ease-out hover:border-border-strong"
    >
      <div className="flex items-start gap-3">
        <ProjectTile name={project.slug} size="md" />
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-16 font-semibold">{project.slug}</span>
          <span className="truncate text-13 text-muted">{project.url ?? "No public URL"}</span>
        </div>
        <Status tone={tone} label={label} className="shrink-0 text-13" />
      </div>

      <div className="flex flex-col gap-1.5 text-13">
        {lastDeploy.failure ? (
          <span className="line-clamp-2 font-mono text-12 text-error">{lastDeploy.failure}</span>
        ) : (
          <span className="flex min-w-0 items-center gap-2">
            <icons.branch className="shrink-0 text-dim" />
            <span className="truncate text-fg">{lastDeploy.message}</span>
          </span>
        )}
        <span className="flex min-w-0 items-center gap-2 text-muted">
          {project.repo ? <icons.github className="shrink-0 text-dim" /> : <icons.terminal className="shrink-0 text-dim" />}
          <span className="truncate">
            {project.repo || "grey deploy"}
            {lastDeploy.branch && (
              <>
                {" "}
                · <span className="font-mono text-12">{lastDeploy.branch}</span>
              </>
            )}{" "}
            · {lastDeploy.ago}
          </span>
        </span>
      </div>

      {metrics && (
      <div className="flex items-end justify-between gap-4 border-t border-border pt-3">
        <div className="flex flex-col">
          <span className="text-12 text-muted">Requests · 24h</span>
          <span className="font-medium tabular">{compact(project.requests24h)}</span>
        </div>
        <Sparkline values={project.requestsSeries} label={`Requests over 24 hours for ${project.slug}`} className="h-8 w-32" />
      </div>
      )}
    </Link>
  );
}
