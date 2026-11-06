import Link from "next/link";
import { redirect } from "next/navigation";
import { connection } from "next/server";
import { Suspense } from "react";
import { adminJson } from "@/lib/api/live";
import type { AdminDeploymentRow } from "@/lib/api/admin-types";
import { DeploymentLink, State, Table, Td, took, when } from "../ui";

const statuses = ["", "failed", "ready", "superseded", "building", "releasing", "queued", "uploading"];

export default function AdminDeploymentsPage({ searchParams }: { searchParams: Promise<{ q?: string; status?: string }> }) {
  return (
    <Suspense fallback={<p className="text-muted">Loading…</p>}>
      <List searchParams={searchParams} />
    </Suspense>
  );
}

async function List({ searchParams }: { searchParams: Promise<{ q?: string; status?: string }> }) {
  await connection();
  const { q = "", status = "" } = await searchParams;
  // A full deployment id goes straight to its page.
  if (/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(q.trim())) redirect(`/admin/deployments/${q.trim().toLowerCase()}`);
  const params = new URLSearchParams({ limit: "200" });
  if (q) params.set("q", q);
  if (status) params.set("status", status);
  const { deployments: found } = await adminJson<{ deployments: AdminDeploymentRow[] | null }>(`/v1/admin/deployments?${params}`);
  const deployments = found ?? [];
  return (
    <>
      <div className="flex flex-wrap items-baseline justify-between gap-3">
        <h1 className="text-24 font-semibold">Deployments</h1>
        <div className="flex flex-wrap gap-2 text-13">
          {statuses.map((s) => (
            <Link
              key={s || "all"}
              href={`/admin/deployments?${new URLSearchParams({ ...(q ? { q } : {}), ...(s ? { status: s } : {}) })}`}
              className={`rounded-control border px-2 py-0.5 ${s === status ? "border-fg text-fg" : "border-border text-muted hover:text-fg"}`}
            >
              {s || "all"}
            </Link>
          ))}
        </div>
      </div>
      {q && (
        <p className="text-13 text-muted">
          Matching <span className="font-mono text-fg">{q}</span> (an id prefix, a project slug or a team slug).{" "}
          <Link href="/admin/deployments" className="underline">
            Clear
          </Link>
        </p>
      )}
      <Table head={["Deployment", "Status", "Project", "By", "Source", "Commit", "Took", "Created"]}>
        {deployments.map((d) => (
          <tr key={d.ID}>
            <Td>
              <DeploymentLink id={d.ID} />
            </Td>
            <Td>
              <State value={d.Status} />
              {d.Error && <div className="mt-1 max-w-[360px] text-12 text-error">{d.Error}</div>}
            </Td>
            <Td mono>
              {d.TeamSlug}/{d.ProjectSlug}
            </Td>
            <Td className="text-muted">{d.CreatedByEmail || "—"}</Td>
            <Td mono>
              {d.Source} · {d.BuildMode}
              {d.Runner ? ` · ${d.Runner}` : ""}
              {d.RollbackOf ? ` · rollback of ${d.RollbackOf.slice(0, 8)}` : ""}
            </Td>
            <Td mono>{d.CommitSha?.slice(0, 7) ?? "—"}</Td>
            <Td mono>{took(d.CreatedAt, d.FinishedAt)}</Td>
            <Td className="whitespace-nowrap text-muted">{when(d.CreatedAt)}</Td>
          </tr>
        ))}
      </Table>
      {deployments.length === 0 && <p className="text-13 text-muted">No deployments match.</p>}
    </>
  );
}
