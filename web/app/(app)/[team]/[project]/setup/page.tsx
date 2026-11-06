import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { DeployLatestButton } from "@/components/onboarding/github-import";
import { EnvKeys } from "@/components/project/env-keys";
import { DataSection } from "@/components/ui/data-section";
import { icons } from "@/components/ui/icons";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { api, liveMode } from "@/lib/api";
import { githubPreview } from "@/lib/api/live";

export const metadata: Metadata = { title: "Review and deploy" };

// After importing a repo: what it will run, the env vars it needs, then Deploy.
export default function SetupPage({ params }: PageProps<"/[team]/[project]/setup">) {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Review and deploy" description="Check what Greyship found, add the env vars your app needs, then deploy." />
      <DataSection what="the repository" fallback={<Skeleton className="h-[560px] w-full rounded-panel" />}>
        <Content params={params} />
      </DataSection>
    </div>
  );
}

const kinds: Record<string, string> = { web: "Web service · gets a URL", worker: "Worker · runs in the background", cron: "Cron · runs on a schedule" };

async function Content({ params }: { params: PageProps<"/[team]/[project]/setup">["params"] }) {
  const { team, project: slug } = await params;
  const project = await api.getProject(team, slug);
  if (!project || !liveMode || !project.id || !project.github) notFound();
  const pv = await githubPreview(project.id);

  return (
    <div className="flex flex-col gap-8">
      <div className="flex flex-wrap items-center justify-between gap-4 rounded-panel border border-border px-4 py-3">
        <span className="flex min-w-0 items-center gap-2 text-13">
          <icons.github className="shrink-0 text-dim" />
          <span className="truncate font-mono">{project.github.fullName}</span>
          <span className="text-dim">
            · {pv.branch} · {pv.commit.slice(0, 7)}
            {pv.go_version ? ` · Go ${pv.go_version}` : ""}
          </span>
        </span>
        {!pv.problem && <DeployLatestButton team={team} project={slug} projectId={project.id} label="Deploy" />}
      </div>

      {pv.problem ? (
        <section className="flex flex-col gap-1 rounded-panel border border-error/40 px-4 py-4">
          <h2 className="font-semibold text-error">This repository can&apos;t be deployed yet</h2>
          <p>{pv.problem.message}</p>
          {pv.problem.fix && <p className="text-muted">Fix: {pv.problem.fix}</p>}
        </section>
      ) : (
        <section className="flex flex-col gap-3">
          <h2 className="text-16 font-semibold">Architecture</h2>
          <ul className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {pv.services?.map((s) => (
              <li key={s.name} className="flex flex-col gap-1.5 rounded-panel border border-border p-4">
                <span className="flex items-center justify-between gap-2">
                  <span className="font-medium">{s.name}</span>
                  <span className="font-mono text-12 text-dim">{s.path}</span>
                </span>
                <span className="text-13">{kinds[s.type] ?? s.type}</span>
                <span className="text-12 text-muted">{s.reason}</span>
              </li>
            ))}
            {pv.release && (
              <li className="flex flex-col gap-1.5 rounded-panel border border-dashed border-border-strong p-4">
                <span className="flex items-center justify-between gap-2">
                  <span className="font-medium">release</span>
                  <span className="font-mono text-12 text-dim">{pv.release.path}</span>
                </span>
                <span className="text-13">Runs once before each deploy goes live (migrations)</span>
                <span className="text-12 text-muted">{pv.release.reason}</span>
              </li>
            )}
          </ul>
          {(pv.skipped?.length ?? 0) > 0 && (
            <ul className="flex flex-col gap-0.5 text-12 text-dim">
              {pv.skipped!.map((s) => (
                <li key={s.path}>
                  Skipped <span className="font-mono">{s.path}</span>: {s.reason}
                </li>
              ))}
            </ul>
          )}
        </section>
      )}

      <section className="flex flex-col gap-3">
        <h2 className="text-16 font-semibold">Environment variables</h2>
        <EnvKeys projectId={project.id} vars={project.env} suggested={pv.env_example ?? []} />
      </section>
    </div>
  );
}
