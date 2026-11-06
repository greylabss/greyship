import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { MetricsView } from "@/components/project/metrics-view";
import { DataSection } from "@/components/ui/data-section";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { Arrives } from "@/components/ui/arrives";
import { api, available } from "@/lib/api";

export const metadata: Metadata = { title: "Metrics" };

export default function Page({ params }: PageProps<"/[team]/[project]/metrics">) {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Metrics" description="Requests, latency and resources per service. Markers show deploys, wake-ups and restarts." />
      <DataSection what="metrics" fallback={<Skeleton className="h-[560px] w-full rounded-panel" />}>
        <Content params={params} />
      </DataSection>
    </div>
  );
}

async function Content({ params }: { params: PageProps<"/[team]/[project]/metrics">["params"] }) {
  const { team, project: slug } = await params;
  if (!available.metrics) return <Arrives title="Metrics" when="a later release (requests, latency and memory per service)" command="grey logs -f" />;
  const [project, data] = await Promise.all([api.getProject(team, slug), api.getMetrics(team, slug, "")]);
  if (!project) notFound();
  return <MetricsView services={project.services.filter((s) => s.type !== "cron").map((s) => s.name)} metrics={data} />;
}
