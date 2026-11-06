import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { DomainsView } from "@/components/project/domains-view";
import { DataSection } from "@/components/ui/data-section";
import { PageHeader } from "@/components/ui/page-header";
import { SkeletonRows } from "@/components/ui/skeleton";
import { Arrives } from "@/components/ui/arrives";
import { api, liveMode } from "@/lib/api";

export const metadata: Metadata = { title: "Domains" };

export default function DomainsPage({ params }: PageProps<"/[team]/[project]/domains">) {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Domains" description="HTTPS certificates are issued and renewed automatically." />
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
  if (liveMode) {
    return (
      <div className="flex flex-col gap-6">
        <ul className="flex flex-col divide-y divide-border rounded-panel border border-border">
          {project.domains.map((d) => (
            <li key={d.hostname} className="flex items-center justify-between gap-3 px-4 py-3">
              <a href={`https://${d.hostname}`} className="truncate font-mono text-13 hover:underline">
                {d.hostname}
              </a>
              <span className="shrink-0 text-12 text-muted">{d.target} · HTTPS · default</span>
            </li>
          ))}
        </ul>
        <Arrives title="Custom domains" when="a later release" />
      </div>
    );
  }
  return <DomainsView initial={project.domains} services={project.services.filter((s) => s.type === "web" && s.visibility === "public").map((s) => s.name)} />;
}
