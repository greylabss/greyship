import type { Metadata } from "next";
import { DataSection } from "@/components/ui/data-section";
import { Meter } from "@/components/ui/meter";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "@/lib/api";

export const metadata: Metadata = { title: "Usage" };

export default function UsagePage() {
  return (
    <div className="flex max-w-2xl flex-col gap-10">
      <PageHeader title="Usage" description="What your running apps use now. Sleeping apps hold no CPU." />
      <DataSection what="usage" fallback={<Skeleton className="h-40 w-full rounded-panel" />}>
        <Resources />
      </DataSection>
    </div>
  );
}

async function Resources() {
  const u = await api.myUsage();
  const unlimited = u.memoryMaxMb === null && u.cpuMaxCores === null && u.concurrentBuildsMax === null;
  return (
    <section className="flex flex-col gap-4">
      <div className="flex flex-col gap-6 rounded-panel border border-border p-5">
        <Row label="Memory" used={u.memoryUsedMb} max={u.memoryMaxMb} unit="MB" />
        <Row label="CPU" used={u.cpuUsedCores} max={u.cpuMaxCores} unit="cores" />
        <div className="flex justify-between gap-4">
          <span>Builds at once</span>
          <span className="font-mono text-13 text-muted tabular">{u.concurrentBuildsMax ?? "no limit"}</span>
        </div>
      </div>
      <p className="text-13 text-muted">
        {unlimited ? "Your instance admin hasn't set limits for you." : "These limits are set by your instance admin. Ask them if you need more."}
      </p>
    </section>
  );
}

function Row({ label, used, max, unit }: { label: string; used: number; max: number | null; unit: string }) {
  const shown = Number.isInteger(used) ? String(used) : used.toFixed(2);
  return (
    <div className="flex flex-col gap-2">
      <div className="flex justify-between gap-4">
        <span>{label}</span>
        <span className="font-mono text-13 text-muted tabular">
          {shown} {max === null ? `${unit} · no limit` : `/ ${max} ${unit}`}
        </span>
      </div>
      {max !== null && <Meter value={used} max={max} label={`${label} used`} />}
    </div>
  );
}
