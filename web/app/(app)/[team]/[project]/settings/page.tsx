import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { SettingsView } from "@/components/project/settings-view";
import { DataSection } from "@/components/ui/data-section";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { Arrives } from "@/components/ui/arrives";
import { api, liveMode } from "@/lib/api";

export const metadata: Metadata = { title: "Settings" };

export default function SettingsPage({ params }: PageProps<"/[team]/[project]/settings">) {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Service settings" />
      <DataSection what="settings" fallback={<Skeleton className="h-[480px] w-full rounded-panel" />}>
        <Content params={params} />
      </DataSection>
    </div>
  );
}

async function Content({ params }: { params: PageProps<"/[team]/[project]/settings">["params"] }) {
  const { team, project: slug } = await params;
  const project = await api.getProject(team, slug);
  if (!project) notFound();
  if (liveMode) return <Arrives title="Project settings" when="a later release. Scaling and memory come from greyship.toml until then" />;
  return <SettingsView team={team} project={slug} services={project.services} />;
}
