import type { Metadata } from "next";
import Link from "next/link";
import { notFound, redirect } from "next/navigation";
import { DeployButton, RollbackButton, RollbackTo } from "@/components/project/actions";
import { ServicesView } from "@/components/project/services-view";
import { Badge } from "@/components/ui/badge";
import { buttonClass } from "@/components/ui/button";
import { CommandHint } from "@/components/ui/command-hint";
import { CopyButton } from "@/components/ui/copy-button";
import { DataSection } from "@/components/ui/data-section";
import { icons } from "@/components/ui/icons";
import { Skeleton } from "@/components/ui/skeleton";
import { Status } from "@/components/ui/status";
import { DeployLatestButton } from "@/components/onboarding/github-import";
import { api, available, liveMode } from "@/lib/api";
import type { Deployment } from "@/lib/api/types";
import { compact } from "@/lib/format";

export const metadata: Metadata = { title: "Project" };

export default function ProjectPage({ params }: PageProps<"/[team]/[project]">) {
  return (
    <DataSection what="project" fallback={<OverviewSkeleton />}>
      <Overview params={params} />
    </DataSection>
  );
}

const deployTone = { ready: "ok", failed: "error", building: "progress", queued: "idle", superseded: "idle" } as const;

async function Overview({ params }: { params: PageProps<"/[team]/[project]">["params"] }) {
  const { team, project: slug } = await params;
  const project = await api.getProject(team, slug);
  if (!project) notFound();
  // A GitHub project that was never deployed starts at its review page.
  if (liveMode && project.github && project.deployments.length === 0) redirect(`/${team}/${project.slug}/setup`);

  const production = project.deployments.find((d) => d.env === "production" && d.status === "ready") ?? project.deployments[0];
  const previous = project.deployments.find((d) => d.env === "production" && d.status === "superseded");
  const previews = project.deployments.filter((d) => d.env === "preview");
  const latest = project.deployments.find((d) => d.env === "production");
  const failed = latest?.status === "failed" ? latest : undefined;
  const building = project.deployments.find((d) => d.status === "building");
  const crashing = project.services.find((s) => s.state.kind === "crashing");
  const branches = ["main", ...new Set(previews.map((d) => d.branch))];

  return (
    <div className="flex flex-col gap-8">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex min-w-0 flex-col gap-1.5">
          <h1 className="text-28 font-semibold tracking-tight">{project.slug}</h1>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-13 text-muted">
            {project.repo ? (
              <a href={`https://github.com/${project.repo}`} className="inline-flex items-center gap-1.5 hover:text-fg">
                <icons.github />
                {project.repo}
                {project.github && (
                  <span className="text-dim">
                    · {project.github.branch} · {project.github.public ? "public, deploys on demand" : "deploys on push"}
                  </span>
                )}
              </a>
            ) : (
              <span>Deployed with grey deploy</span>
            )}
            {project.region && (
              <>
                <span aria-hidden="true">·</span>
                <span>{project.region}</span>
              </>
            )}
            {project.goVersion && (
              <>
                <span aria-hidden="true">·</span>
                <span className="font-mono text-12">{project.goVersion}</span>
              </>
            )}
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {project.url && (
            <a href={`https://${project.url}`} className={buttonClass("secondary")}>
              Visit
              <icons.external />
            </a>
          )}
          {liveMode ? (
            project.github && project.id ? (
              <DeployLatestButton team={team} project={project.slug} projectId={project.id} />
            ) : (
              <CommandHint command="grey deploy" />
            )
          ) : (
            <DeployButton team={team} project={project.slug} branches={branches} />
          )}
        </div>
      </div>

      {crashing && crashing.state.kind === "crashing" && (
        <Alert
          title={`${crashing.name} is crashing`}
          body={<span className="font-mono text-12">{crashing.state.reason}</span>}
          note="Restarting with backoff. After 5 crashes in 5 minutes it stays stopped and we email you."
          href={`/${team}/${slug}/logs?service=${crashing.name}&level=ERROR`}
          action="View error logs"
        />
      )}
      {failed && !crashing && (
        <Alert
          title={`Build failed for ${failed.sha}`}
          body={<span className="font-mono text-12">{failed.failure}</span>}
          note={`${production.sha} keeps serving production.`}
          href={`/${team}/${slug}/deployments/${failed.id}`}
          action="Open build log"
        />
      )}

      <section className="grid grid-cols-1 overflow-hidden rounded-panel border border-border lg:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)]">
        <div className="flex flex-col gap-5 p-5">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <span className="flex items-center gap-2 text-13 text-muted">
              Production deployment
              {building && <Badge tone="progress">building {building.sha}</Badge>}
            </span>
            <Status tone={deployTone[production.status]} label={production.status === "ready" ? "Ready" : production.status} className="text-13" />
          </div>
          <Link href={`/${team}/${slug}/deployments/${production.id}`} className="flex flex-col gap-1 hover:underline">
            <span className="text-20 font-semibold">{production.message}</span>
          </Link>
          <dl className="grid grid-cols-2 gap-x-6 gap-y-3 text-13 sm:grid-cols-4">
            <Meta label="Commit" value={production.sha} mono />
            {production.branch && <Meta label="Branch" value={production.branch} mono />}
            <Meta label="Deployed" value={production.author ? `${production.ago} by ${production.author}` : production.ago} />
            <Meta label="Build" value={production.duration ?? "—"} mono />
          </dl>
          <div className="flex flex-wrap gap-2">
            {previous &&
              (liveMode ? (
                <RollbackTo team={team} project={slug} id={previous.id} sha={previous.sha} current={production.sha} />
              ) : (
                <RollbackButton sha={previous.sha} current={production.sha} />
              ))}
            <Link href={`/${team}/${slug}/deployments`} className={buttonClass("ghost")}>
              All deployments
            </Link>
          </div>
        </div>
        <div className="flex flex-col gap-3 border-t border-border bg-surface-2 p-5 lg:border-l lg:border-t-0">
          <span className="text-13 text-muted">Domains</span>
          <ul className="flex flex-col gap-2">
            {project.domains.map((d) => (
              <li key={d.hostname} className="flex min-w-0 items-center gap-2">
                <Status tone={d.status === "active" ? "ok" : "progress"} label="" className="gap-0" />
                <a href={`https://${d.hostname}`} className="min-w-0 truncate font-mono text-13 hover:underline">
                  {d.hostname}
                </a>
                <CopyButton value={`https://${d.hostname}`} label="Copy URL" />
                {d.status !== "active" && <Badge tone="progress">waiting for DNS</Badge>}
              </li>
            ))}
          </ul>
          <Link href={`/${team}/${slug}/domains`} className="mt-auto text-13 text-muted hover:text-fg">
            Manage domains
          </Link>
        </div>
      </section>

      <ServicesView team={team} project={slug} services={project.services} demo={!liveMode} />

      {available.metrics && (
      <div className="grid grid-cols-1 gap-8 lg:grid-cols-2">
        <section className="flex flex-col gap-3">
          <h2 className="flex h-9 items-center text-16 font-semibold">Preview deployments</h2>
          {previews.length === 0 ? (
            <div className="flex flex-col gap-2 rounded-panel border border-dashed border-border-strong px-4 py-6 text-13 text-muted">
              Push a branch or open a pull request to get a preview URL.
              <CommandHint command="grey deploy --branch my-feature" />
            </div>
          ) : (
            <ul className="divide-y divide-border rounded-panel border border-border">
              {previews.map((d) => (
                <PreviewRow key={d.id} team={team} project={slug} d={d} />
              ))}
            </ul>
          )}
        </section>
        <section className="flex flex-col gap-3">
          <h2 className="flex h-9 items-center text-16 font-semibold">Last 24 hours</h2>
          <dl className="grid grid-cols-2 gap-px overflow-hidden rounded-panel border border-border bg-border">
            <Tile label="Requests" value={compact(project.requests24h)} />
            <Tile label="Latency p95" value="41 ms" />
            <Tile label="Wake-ups" value="12" note="avg 18 ms" />
            <Tile label="Cold starts" value="1" note="612 ms" />
          </dl>
          <Link href={`/${team}/${slug}/metrics`} className="text-13 text-muted hover:text-fg">
            Open metrics
          </Link>
        </section>
      </div>
      )}
    </div>
  );
}

function Alert({ title, body, note, href, action }: { title: string; body: React.ReactNode; note: string; href: string; action: string }) {
  return (
    <div role="alert" className="flex flex-wrap items-start gap-3 rounded-panel border border-error/50 bg-surface-2 px-4 py-4">
      <icons.alert className="mt-0.5 text-error" />
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <span className="font-medium">{title}</span>
        <span className="text-error">{body}</span>
        <span className="text-13 text-muted">{note}</span>
      </div>
      <Link href={href} className={buttonClass("secondary", "sm")}>
        {action}
      </Link>
    </div>
  );
}

function Meta({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <dt className="text-12 text-muted">{label}</dt>
      <dd className={mono ? "truncate font-mono text-13" : "truncate"}>{value}</dd>
    </div>
  );
}

function Tile({ label, value, note }: { label: string; value: string; note?: string }) {
  return (
    <div className="flex flex-col gap-1 bg-bg px-4 py-4">
      <dt className="text-12 text-muted">{label}</dt>
      <dd className="flex items-baseline gap-1.5">
        <span className="text-20 font-semibold tabular">{value}</span>
        {note && <span className="text-13 text-muted">{note}</span>}
      </dd>
    </div>
  );
}

function PreviewRow({ team, project, d }: { team: string; project: string; d: Deployment }) {
  return (
    <li className="flex flex-col gap-1.5 px-4 py-3">
      <div className="flex items-center justify-between gap-3">
        <Link href={`/${team}/${project}/deployments/${d.id}`} className="truncate font-medium hover:underline">
          {d.message}
        </Link>
        <Status tone={deployTone[d.status]} label={d.status === "ready" ? "Ready" : "Building"} className="shrink-0 text-13" />
      </div>
      <div className="flex min-w-0 items-center gap-2 text-12 text-muted">
        <icons.branch className="text-dim" />
        <span className="font-mono">{d.branch}</span>
        {d.previewUrl && (
          <>
            <span aria-hidden="true">·</span>
            <a href={`https://${d.previewUrl}`} className="truncate font-mono hover:text-fg">
              {d.previewUrl}
            </a>
          </>
        )}
        <span className="ml-auto shrink-0">{d.ago}</span>
      </div>
    </li>
  );
}

function OverviewSkeleton() {
  return (
    <div className="flex flex-col gap-8" aria-busy="true" aria-label="Loading project">
      <div className="flex flex-col gap-2">
        <Skeleton className="h-7 w-48" />
        <Skeleton className="h-4 w-72" />
      </div>
      <Skeleton className="h-52 w-full rounded-panel" />
      <Skeleton className="h-64 w-full rounded-panel" />
    </div>
  );
}
