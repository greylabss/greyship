import type { Metadata } from "next";
import { cx } from "@/lib/format";
import Link from "next/link";
import { TeamLinkButton } from "@/components/shell/team-link";
import { ActivityList } from "@/components/team/activity-list";
import { ProjectCard } from "@/components/team/project-card";
import { ProjectsLayout, ProjectsToolbar, ProjectsView } from "@/components/team/projects-view";
import { LinkButton } from "@/components/ui/button";
import { DataSection } from "@/components/ui/data-section";
import { EmptyState } from "@/components/ui/empty-state";
import { icons } from "@/components/ui/icons";
import { Skeleton, SkeletonRows } from "@/components/ui/skeleton";
import { api, available } from "@/lib/api";
import { compact } from "@/lib/format";

export const metadata: Metadata = { title: "Overview" };

export default function OverviewPage({ params }: PageProps<"/[team]">) {
  const team = params.then((p) => p.team);
  return (
    <ProjectsView>
      <div className="flex flex-col gap-6">
        <ProjectsToolbar
          action={
            <TeamLinkButton to="/new" variant="primary">
              <icons.plus />
              New project
            </TeamLinkButton>
          }
        />

        <DataSection what="overview" fallback={<Skeleton className="h-[86px] w-full rounded-panel" />}>
          <Stats team={team} />
        </DataSection>

        <div className="grid grid-cols-[minmax(0,1fr)] items-start gap-8 lg:grid-cols-[minmax(0,1fr)_340px]">
          <DataSection what="projects" fallback={<ProjectsSkeleton />}>
            <Projects team={team} />
          </DataSection>

          <div className="flex flex-col gap-8">
            <DataSection what="recent deployments" fallback={<SkeletonRows rows={4} />}>
              <RecentDeployments team={team} />
            </DataSection>
          </div>
        </div>
      </div>
    </ProjectsView>
  );
}

async function Stats({ team: teamSlug }: { team: Promise<string> }) {
  const team = await teamSlug;
  const [projects, activity] = await Promise.all([api.listProjects(team), api.listActivity(team)]);
  const requests = projects.reduce((n, p) => n + p.requests24h, 0);
  const sleeping = projects.filter((p) => p.health === "sleeping").length;

  const stats = [
    { label: "Projects", value: String(projects.length), detail: "" },
    ...(available.metrics ? [{ label: "Requests · 24h", value: compact(requests), detail: "" }] : []),
    { label: "Sleeping now", value: String(sleeping), detail: "holding no CPU" },
    { label: "Deploys · 7d", value: String(activity.length), detail: "" },
  ];

  return (
    <dl
      className={cx(
        "grid grid-cols-2 gap-px overflow-hidden rounded-panel border border-border bg-border",
        stats.length === 4 ? "sm:grid-cols-4" : "sm:grid-cols-3",
      )}
    >
      {stats.map((s) => (
        <div key={s.label} className="flex flex-col gap-1 bg-bg px-4 py-4">
          <dt className="text-12 text-muted">{s.label}</dt>
          <dd className="flex items-baseline gap-1.5">
            <span className="text-20 font-semibold tabular">{s.value}</span>
            {s.detail && <span className="text-13 text-muted">{s.detail}</span>}
          </dd>
        </div>
      ))}
    </dl>
  );
}

async function Projects({ team: teamSlug }: { team: Promise<string> }) {
  const team = await teamSlug;
  const projects = await api.listProjects(team);

  if (projects.length === 0) {
    return (
      <EmptyState
        title="No projects yet"
        body="Import a Go repository from GitHub, or run grey deploy in one."
        action={
          <LinkButton href={`/${team}/new`} variant="primary">
            <icons.github />
            Import from GitHub
          </LinkButton>
        }
        command="grey deploy"
      />
    );
  }

  return (
    <ProjectsLayout
      entries={projects.map((p, i) => ({
        key: p.slug,
        name: `${p.slug} ${p.repo} ${p.url ?? ""}`,
        recentRank: i,
        requests: p.requests24h,
      }))}
      empty={
        <p className="rounded-panel border border-dashed border-border-strong px-4 py-8 text-center text-muted">
          No projects match your search.
        </p>
      }
    >
      {projects.map((p) => (
        <ProjectCard key={p.slug} team={team} project={p} metrics={available.metrics} />
      ))}
    </ProjectsLayout>
  );
}

async function RecentDeployments({ team: teamSlug }: { team: Promise<string> }) {
  const team = await teamSlug;
  const items = (await api.listActivity(team)).slice(0, 4);

  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <h2 className="text-16 font-semibold">Recent deployments</h2>
        {items.length > 0 && (
          <Link href={`/${team}/deployments`} className="text-13 text-muted hover:text-fg">
            View all
          </Link>
        )}
      </div>
      {items.length === 0 ? (
        <div className="flex flex-col gap-2 rounded-panel border border-dashed border-border-strong px-4 py-6 text-13 text-muted">
          No deployments yet.
        </div>
      ) : (
        <ActivityList team={team} items={items} dense />
      )}
    </section>
  );
}

function ProjectsSkeleton() {
  return (
    <div className="grid grid-cols-1 gap-3 xl:grid-cols-2" aria-busy="true" aria-label="Loading projects">
      {Array.from({ length: 4 }, (_, i) => (
        <div key={i} className="flex flex-col gap-4 rounded-panel border border-border p-4">
          <div className="flex gap-3">
            <Skeleton className="size-10" />
            <div className="flex flex-col gap-2">
              <Skeleton className="h-4 w-32" />
              <Skeleton className="h-3 w-44" />
            </div>
          </div>
          <Skeleton className="h-3 w-56" />
          <Skeleton className="h-3 w-48" />
          <Skeleton className="h-8 w-full" />
        </div>
      ))}
    </div>
  );
}
