import type { Metadata } from "next";
import { ActivityList } from "@/components/team/activity-list";
import { CommandHint } from "@/components/ui/command-hint";
import { DataSection } from "@/components/ui/data-section";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { SkeletonRows } from "@/components/ui/skeleton";
import { api } from "@/lib/api";

export const metadata: Metadata = { title: "Deployments" };

export default function DeploymentsPage({ params }: PageProps<"/[team]/deployments">) {
  const team = params.then((p) => p.team);
  return (
    <div className="flex flex-col gap-8">
      <PageHeader title="Deployments" description={<CommandHint command="grey deploy" />} />
      <DataSection what="deployments" fallback={<SkeletonRows rows={6} />}>
        <All team={team} />
      </DataSection>
    </div>
  );
}

async function All({ team: teamSlug }: { team: Promise<string> }) {
  const team = await teamSlug;
  const items = await api.listActivity(team);
  if (items.length === 0) {
    return <EmptyState title="No deployments yet" body="Deployments show up here once you deploy a project." command="grey deploy" />;
  }
  return <ActivityList team={team} items={items} />;
}
