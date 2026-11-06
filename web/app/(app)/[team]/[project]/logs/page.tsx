import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { LogsView } from "@/components/project/logs-view";
import { DataSection } from "@/components/ui/data-section";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "@/lib/api";

export const metadata: Metadata = { title: "Logs" };

export default function LogsPage({ params }: PageProps<"/[team]/[project]/logs">) {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Logs" description="stdout and stderr from every instance. JSON lines are parsed; click a line for its fields." />
      <DataSection what="logs" fallback={<Skeleton className="h-[620px] w-full rounded-panel" />}>
        <Content params={params} />
      </DataSection>
    </div>
  );
}

async function Content({ params }: { params: PageProps<"/[team]/[project]/logs">["params"] }) {
  const { team, project: slug } = await params;
  const [project, lines] = await Promise.all([api.getProject(team, slug), api.listLogs(team, slug)]);
  if (!project) notFound();
  return <LogsView initial={lines} services={project.services.map((s) => s.name)} projectId={project.id} />;
}
