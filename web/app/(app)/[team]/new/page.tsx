import type { Metadata } from "next";
import Link from "next/link";
import { Suspense } from "react";
import { RepoPicker } from "@/components/onboarding/repo-picker";
import { Steps } from "@/components/onboarding/steps";
import { CommandHint } from "@/components/ui/command-hint";
import { DataSection } from "@/components/ui/data-section";
import { SkeletonRows } from "@/components/ui/skeleton";
import { Arrives } from "@/components/ui/arrives";
import { GitHubImport, InstallGitHub, PublicRepoForm } from "@/components/onboarding/github-import";
import { api, liveMode } from "@/lib/api";
import { githubState } from "@/lib/api/live";
import { templates } from "@/lib/fixtures/project";

export const metadata: Metadata = { title: "New project" };

export default function NewProjectPage({ params }: PageProps<"/[team]/new">) {
  return (
    <Suspense>
      <Body params={params} />
    </Suspense>
  );
}

async function Body({ params }: { params: PageProps<"/[team]/new">["params"] }) {
  const { team } = await params;
  return (
    <div className="mx-auto flex w-full max-w-5xl flex-col gap-8">
      <Steps current={0} />
      <div className="flex flex-col gap-1">
        <h1 className="text-28 font-semibold tracking-tight">Deploy a Go backend</h1>
        <p className="text-muted">Pick a repository. You&apos;ll see its services and set env vars before deploying.</p>
      </div>
      <div className="grid grid-cols-1 items-start gap-6 lg:grid-cols-[minmax(0,1fr)_320px]">
        <DataSection what="repositories" fallback={<SkeletonRows rows={6} />}>
          <Repos team={team} />
        </DataSection>
        <aside className="flex flex-col gap-6">
          {!liveMode && (
            <section className="flex flex-col gap-3 rounded-panel border border-border p-4">
              <h2 className="font-semibold">Prefer the terminal?</h2>
              <div className="flex flex-col gap-1">
                <CommandHint command="curl -fsSL greyship.tech/install | sh" />
                <CommandHint command="grey login" />
                <CommandHint command="grey deploy" />
              </div>
            </section>
          )}
          {!liveMode && (
          <section className="flex flex-col gap-2">
            <h2 className="font-semibold">Start from a template</h2>
            <ul className="divide-y divide-border rounded-panel border border-border">
              {templates.map((t) => (
                <li key={t.slug}>
                  <Link href={`/${team}/new/${t.slug}`} className="flex flex-col gap-0.5 px-4 py-3 hover:bg-surface-2">
                    <span className="font-medium">{t.name}</span>
                    <span className="text-13 text-muted">{t.desc}</span>
                  </Link>
                </li>
              ))}
            </ul>
          </section>
          )}
        </aside>
      </div>
    </div>
  );
}

async function Repos({ team }: { team: string }) {
  if (!liveMode) return <RepoPicker team={team} repos={await api.listRepos(team)} />;
  const gh = await githubState(team);
  if (gh.kind === "off") return <Arrives title="Import from GitHub" when="GitHub deploys, once they're switched on for this Greyship" command="grey deploy" />;
  return (
    <div className="flex flex-col gap-6">
      <PublicRepoForm team={team} />
      {gh.kind === "install" ? <InstallGitHub installURL={gh.installURL} /> : <GitHubImport team={team} repos={gh.repos} installURL={gh.installURL} />}
    </div>
  );
}
