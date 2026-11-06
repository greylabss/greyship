import Link from "next/link";
import { connection } from "next/server";
import { Suspense } from "react";
import { adminJson } from "@/lib/api/live";
import type { AdminOverview } from "@/lib/api/admin-types";
import { DeploymentLink, Section, State, Table, Td, took, when } from "./ui";

export default function AdminOverviewPage() {
  return (
    <Suspense fallback={<p className="text-muted">Loading…</p>}>
      <Overview />
    </Suspense>
  );
}

async function Overview() {
  await connection();
  const o = await adminJson<AdminOverview>("/v1/admin/overview");
  const c = o.Counts;
  const stats: [string, number, string?][] = [
    ["Users", c.Users],
    ["Teams", c.Teams],
    ["Projects", c.Projects],
    ["Deploys · 24h", c.Deploys24h],
    ["Failed · 24h", c.Failed24h, c.Failed24h > 0 ? "text-error" : undefined],
    ["In progress", c.InProgress],
    ["Live instances", c.InstancesLive],
  ];
  return (
    <>
      <h1 className="text-24 font-semibold">Overview</h1>
      <dl className="grid grid-cols-2 gap-px overflow-hidden rounded-panel border border-border bg-border sm:grid-cols-4 lg:grid-cols-7">
        {stats.map(([label, n, tone]) => (
          <div key={label} className="flex flex-col gap-1 bg-bg px-4 py-3">
            <dt className="text-12 text-muted">{label}</dt>
            <dd className={`text-20 font-semibold tabular ${tone ?? ""}`}>{n}</dd>
          </div>
        ))}
      </dl>

      <Section title="Latest failed deployments" aside={<Link href="/admin/deployments?status=failed">all failed</Link>}>
        <Table head={["Deployment", "Project", "By", "Source", "Error", "When"]}>
          {(o.Failed ?? []).map((d) => (
            <tr key={d.ID}>
              <Td>
                <DeploymentLink id={d.ID} />
              </Td>
              <Td mono>
                {d.TeamSlug}/{d.ProjectSlug}
              </Td>
              <Td className="text-muted">{d.CreatedByEmail || "—"}</Td>
              <Td mono>
                {d.Source} · {d.BuildMode}
              </Td>
              <Td className="max-w-[420px] text-error">{d.Error}</Td>
              <Td className="whitespace-nowrap text-muted">{when(d.CreatedAt)}</Td>
            </tr>
          ))}
        </Table>
        {(o.Failed ?? []).length === 0 && <p className="text-13 text-muted">No failed deployments.</p>}
      </Section>

      <div className="grid grid-cols-1 gap-8 lg:grid-cols-2">
        <Section title="Nodes">
          <Table head={["Node", "State", "Memory", "Instances", "Heartbeat"]}>
            {(o.Nodes ?? []).map((n) => {
              const age = n.HeartbeatAge;
              return (
                <tr key={n.ID}>
                  <Td mono>
                    {n.Name} ({n.Region})
                  </Td>
                  <Td>
                    <State value={age > 30 ? "stale" : n.State} />
                  </Td>
                  <Td mono>
                    {n.UsedMb} / {n.TotalMb} MB
                  </Td>
                  <Td mono>{n.Instances}</Td>
                  <Td className={age > 30 ? "text-error" : "text-muted"}>{age}s ago</Td>
                </tr>
              );
            })}
          </Table>
        </Section>
        <Section title="Job queue" aside="open, plus the last 24h">
          <Table head={["Kind", "Status", "Count"]}>
            {(o.Jobs ?? []).map((j) => (
              <tr key={j.Kind + j.Status}>
                <Td mono>{j.Kind}</Td>
                <Td>
                  <State value={j.Status} />
                </Td>
                <Td mono>{j.N}</Td>
              </tr>
            ))}
          </Table>
        </Section>
      </div>

      <Section title="Failed jobs" aside="gave up after their last attempt">
        <Table head={["Job", "Kind", "Payload", "Attempts", "Last error", "Finished"]}>
          {(o.FailedJobs ?? []).map((j) => (
            <tr key={j.ID}>
              <Td mono>#{j.ID}</Td>
              <Td mono>{j.Kind}</Td>
              <Td mono className="max-w-[260px] break-all">
                {j.Payload}
              </Td>
              <Td mono>
                {j.Attempts}/{j.MaxAttempts}
              </Td>
              <Td className="max-w-[420px] break-all text-error">{j.LastError}</Td>
              <Td className="whitespace-nowrap text-muted">
                {when(j.FinishedAt)} · {took(j.CreatedAt, j.FinishedAt)}
              </Td>
            </tr>
          ))}
        </Table>
        {(o.FailedJobs ?? []).length === 0 && <p className="text-13 text-muted">None.</p>}
      </Section>
    </>
  );
}
