import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { RuntimeView } from "@/components/project/runtime-view";
import { DataSection } from "@/components/ui/data-section";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { Arrives } from "@/components/ui/arrives";
import { api, available } from "@/lib/api";

export const metadata: Metadata = { title: "Go Runtime" };

export default function Page({ params }: PageProps<"/[team]/[project]/runtime">) {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Go Runtime" description="Goroutines, heap and GC for every instance, read straight from the Go runtime." />
      <DataSection what="runtime" fallback={<Skeleton className="h-[560px] w-full rounded-panel" />}>
        <Content params={params} />
      </DataSection>
    </div>
  );
}

async function Content({ params }: { params: PageProps<"/[team]/[project]/runtime">["params"] }) {
  const { team, project: slug } = await params;
  if (!available.runtime) return <Arrives title="Go runtime" when="the public beta (goroutines, heap, GC and pprof)" />;
  const [project, data] = await Promise.all([api.getProject(team, slug), api.getRuntime(team, slug, "")]);
  if (!project) notFound();
  return <RuntimeView services={project.services.filter((s) => s.type !== "cron").map((s) => s.name)} info={data} />;
}
