import type { Metadata } from "next";
import { DataSection } from "@/components/ui/data-section";
import { Meter } from "@/components/ui/meter";
import { PageHeader, SectionHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, Td, Th } from "@/components/ui/table";
import { Arrives } from "@/components/ui/arrives";
import { api, available } from "@/lib/api";
import type { UsageMeter } from "@/lib/api/types";
import { compact, dollars } from "@/lib/format";

export const metadata: Metadata = { title: "Usage" };

export default function UsagePage({ params }: PageProps<"/[team]/usage">) {
  const team = params.then((p) => p.team);
  return (
    <div className="flex flex-col gap-10">
      <PageHeader title="Usage" description="Billing follows running time. Sleeping apps cost nothing." />
      <DataSection what="usage" fallback={<Skeleton className="h-56 w-full rounded-panel" />}>
        <UsageMeters team={team} />
      </DataSection>
    </div>
  );
}

async function UsageMeters({ team: teamSlug }: { team: Promise<string> }) {
  const team = await teamSlug;
  if (!available.usage) {
    return (
      <div className="flex flex-col gap-8">
        <BuildMinutes team={team} />
        <Arrives title="Running time" when="billing in the public beta. Until then running apps is free" />
      </div>
    );
  }
  const usage = await api.getUsage(team);
  return (
    <section className="flex flex-col gap-4">
      <SectionHeader title={`${usage.period} usage`} aside={`Resets ${usage.resetsOn} · sleeping apps don't count`} />
      <div className="grid grid-cols-1 gap-x-10 gap-y-6 rounded-panel border border-border p-5 md:grid-cols-2">
        {usage.meters.map((m) => (
          <MeterRow key={m.label} meter={m} />
        ))}
      </div>
      <div className="flex flex-wrap gap-x-8 gap-y-2 text-13 text-muted">
        <span>
          Spent so far <span className="text-fg tabular">{dollars(usage.spendCents)}</span>
        </span>
        <span>
          Projected for {usage.period} <span className="text-fg tabular">{dollars(usage.projectedCents)}</span>
        </span>
      </div>

      {usage.byProject.length > 0 && (
        <div className="flex flex-col gap-3 pt-4">
          <SectionHeader title="By project" />
          <Table minWidth={480}>
            <thead>
              <tr>
                <Th>Project</Th>
                <Th className="w-1/2">Share</Th>
                <Th align="right">This month</Th>
              </tr>
            </thead>
            <tbody>
              {usage.byProject.map((p) => (
                <tr key={p.slug}>
                  <Td className="font-medium">{p.slug}</Td>
                  <Td>
                    <Meter value={p.spendCents} max={Math.max(usage.spendCents, 1)} label={`${p.slug} share of spend`} />
                  </Td>
                  <Td align="right" mono>
                    {dollars(p.spendCents)}
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        </div>
      )}
    </section>
  );
}

async function BuildMinutes({ team }: { team: string }) {
  const b = await api.buildUsage(team);
  if (!b) return <p className="text-13 text-muted">This Greyship has no build-minute limit. Builds on your own machine and on the server are never counted.</p>;
  return (
    <section className="flex flex-col gap-4">
      <SectionHeader title="Build minutes" aside={`Resets ${b.resetsOn}`} />
      <div className="flex flex-col gap-3 rounded-panel border border-border p-5">
        <MeterRow meter={{ label: "Builds on Greyship this month", used: b.usedMinutes, included: b.includedMinutes, unit: "min" }} />
        <span className="text-13 text-muted">Building on your own machine with grey deploy is free and doesn&apos;t count.</span>
      </div>
    </section>
  );
}

function MeterRow({ meter }: { meter: UsageMeter }) {
  const fmt = (n: number) => (meter.unit === "requests" ? compact(n) : String(n));
  const unit = meter.unit === "requests" ? "" : ` ${meter.unit}`;
  return (
    <div className="flex flex-col gap-2">
      <div className="flex justify-between gap-4">
        <span>{meter.label}</span>
        <span className="font-mono text-13 text-muted tabular">
          {fmt(meter.used)} / {fmt(meter.included)}
          {unit}
        </span>
      </div>
      <Meter value={meter.used} max={meter.included} label={`${meter.label} used`} />
    </div>
  );
}
