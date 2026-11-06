import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { DeploymentsList } from "@/components/project/deployments-list";
import { DeployButton } from "@/components/project/actions";
import { CommandHint } from "@/components/ui/command-hint";
import { DataSection } from "@/components/ui/data-section";
import { PageHeader } from "@/components/ui/page-header";
import { SkeletonRows } from "@/components/ui/skeleton";
import { api, liveMode } from "@/lib/api";

export const metadata: Metadata = { title: "Deployments" };

export default function DeploymentsPage({ params }: PageProps<"/[team]/[project]/deployments">) {
  return (
    <DataSection what="deployments" fallback={<SkeletonRows rows={6} />}>
      <Content params={params} />
    </DataSection>
  );
}

async function Content({ params }: { params: PageProps<"/[team]/[project]/deployments">["params"] }) {
  const { team, project: slug } = await params;
  const project = await api.getProject(team, slug);
  if (!project) notFound();
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Deployments" description={<CommandHint command="grey deploy" />} actions={liveMode ? undefined : <DeployButton team={team} project={slug} branches={["main", "checkout-v2"]} />} />
      <DeploymentsList team={team} project={slug} deployments={project.deployments} real={liveMode} />
    </div>
  );
}
