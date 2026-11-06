import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { DeploymentView } from "@/components/project/deployment-view";
import { DataSection } from "@/components/ui/data-section";
import { Skeleton } from "@/components/ui/skeleton";
import { api, liveMode } from "@/lib/api";

export const metadata: Metadata = { title: "Deployment" };

export function generateStaticParams() {
  return [{ id: "7f3a9c2" }];
}

export default function DeploymentPage({ params }: PageProps<"/[team]/[project]/deployments/[id]">) {
  return (
    <DataSection what="deployment" fallback={<Skeleton className="h-[480px] w-full rounded-panel" />}>
      <Content params={params} />
    </DataSection>
  );
}

async function Content({ params }: { params: PageProps<"/[team]/[project]/deployments/[id]">["params"] }) {
  const { team, project: slug, id } = await params;
  const [project, deployment] = await Promise.all([api.getProject(team, slug), api.getDeployment(team, slug, id)]);
  if (!project || !deployment) notFound();
  const current = project.deployments.find((d) => d.env === "production" && d.status === "ready");
  return <DeploymentView key={id} team={team} deployment={deployment} live={id === "live"} currentSha={current?.sha} actions={!liveMode} real={liveMode} streamId={liveMode && (deployment.status === "queued" || deployment.status === "building") ? deployment.id : undefined} />;
}
