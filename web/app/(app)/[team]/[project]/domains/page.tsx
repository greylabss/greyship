import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { CustomDomains } from "@/components/project/custom-domains";
import { DomainsView } from "@/components/project/domains-view";
import { DataSection } from "@/components/ui/data-section";
import { PageHeader } from "@/components/ui/page-header";
import { SkeletonRows } from "@/components/ui/skeleton";
import { api, liveMode } from "@/lib/api";
import { projectDomains } from "@/lib/api/live";

export const metadata: Metadata = { title: "Domains" };

export default function DomainsPage({ params }: PageProps<"/[team]/[project]/domains">) {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Domains" description="Each web service has a default address. Point your own domain at it; HTTPS is issued and renewed automatically." />
      <DataSection what="domains" fallback={<SkeletonRows rows={3} />}>
        <Content params={params} />
      </DataSection>
    </div>
  );
}

async function Content({ params }: { params: PageProps<"/[team]/[project]/domains">["params"] }) {
  const { team, project: slug } = await params;
  const project = await api.getProject(team, slug);
  if (!project) notFound();
  if (liveMode && project.id) {
    const rows = await projectDomains(project.id);
    return <CustomDomains id={project.id} rows={rows} />;
  }
  return <DomainsView initial={project.domains} services={project.services.filter((s) => s.type === "web" && s.visibility === "public").map((s) => s.name)} />;
}
