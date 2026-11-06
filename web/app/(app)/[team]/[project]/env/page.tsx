import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { EnvView } from "@/components/project/env-view";
import { DataSection } from "@/components/ui/data-section";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { EnvKeys } from "@/components/project/env-keys";
import { api, liveMode } from "@/lib/api";

export const metadata: Metadata = { title: "Environment variables" };

export default function EnvPage({ params }: PageProps<"/[team]/[project]/env">) {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Environment variables" description="Encrypted at rest. Changing one restarts only the services that use it, with no rebuild." />
      <DataSection what="environment variables" fallback={<Skeleton className="h-[480px] w-full rounded-panel" />}>
        <Content params={params} />
      </DataSection>
    </div>
  );
}

async function Content({ params }: { params: PageProps<"/[team]/[project]/env">["params"] }) {
  const { team, project: slug } = await params;
  const project = await api.getProject(team, slug);
  if (!project) notFound();
  if (liveMode && project.id) return <EnvKeys projectId={project.id} vars={project.env} />;
  return <EnvView services={project.services.map((s) => s.name)} initial={project.env} initialExcludes={project.excludes} />;
}
