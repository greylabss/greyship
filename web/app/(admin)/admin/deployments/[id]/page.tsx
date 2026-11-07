import Link from "next/link";
import { notFound } from "next/navigation";
import { connection } from "next/server";
import { Suspense } from "react";
import { ApiError, adminJson } from "@/lib/api/live";
import type { AdminDetail } from "@/lib/api/admin-types";
import { CopyButton } from "@/components/ui/copy-button";
import { Grid, Log, Section, State, Table, Td, took, when } from "../../ui";

export default function AdminDeploymentPage({ params }: { params: Promise<{ id: string }> }) {
  return (
    <Suspense fallback={<p className="text-muted">Loading…</p>}>
      <Detail params={params} />
    </Suspense>
  );
}

const mb = (n: number | null | undefined) =>
  n == null ? "—" : n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MB` : n >= 1024 ? `${Math.round(n / 1024)} KB` : `${n} B`;
const clock = (iso: string) => new Date(iso).toISOString().slice(11, 19);

async function Detail({ params }: { params: Promise<{ id: string }> }) {
  await connection();
  const { id } = await params;
  let x: AdminDetail;
  try {
    x = await adminJson<AdminDetail>(`/v1/admin/deployments/${encodeURIComponent(id)}`);
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) {
      return <p className="text-muted">Several deployments start with {id}. Give more of the id.</p>;
    }
    if (e instanceof ApiError && e.status === 404) notFound();
    throw e;
  }
  const d = x.Deployment;
  const dash = `/${d.TeamSlug}/${d.ProjectSlug}/deployments/${d.ID}`;
  return (
    <>
      <div className="flex flex-col gap-2">
        <span className="flex flex-wrap items-center gap-3 text-13">
          <State value={d.Status} />
          <span className="text-muted">
            {d.TeamSlug}/{d.ProjectSlug} · {d.Env}
          </span>
          <Link href={dash} className="text-muted underline">
            open in the dashboard
          </Link>
        </span>
        <h1 className="flex items-center gap-2 break-all font-mono text-20 font-semibold">
          {d.ID}
          <CopyButton value={d.ID} label="Copy deployment id" />
        </h1>
        {d.Error && <p className="rounded-panel border border-error/50 bg-surface-2 px-4 py-3 text-error">{d.Error}</p>}
      </div>

      <div className="grid grid-cols-1 gap-8 xl:grid-cols-2">
        <Section title="Deployment">
          <Grid
            rows={[
              ["Team", `${d.TeamSlug} · ${d.TeamID}`],
              ["Project", `${d.ProjectName} · ${d.ProjectID}`],
              ["Started by", d.CreatedByEmail || "— (GitHub push)"],
              ["Source", d.Source],
              ["Build", `${d.BuildMode}${d.Runner ? ` on ${d.Runner}` : ""}${d.MachineSize ? ` (${d.MachineSize})` : ""}${d.BuildSeconds != null ? ` · ${d.BuildSeconds}s` : ""}`],
              ["Repo", d.Repo ? `${d.Repo}${d.Branch ? ` @ ${d.Branch}` : ""}` : "—"],
              ["Commit", d.CommitSha ?? "—"],
              ["GitHub status", d.GithubReported ?? "—"],
              ["Rollback of", d.RollbackOf ? <Link key="r" href={`/admin/deployments/${d.RollbackOf}`} className="underline">{d.RollbackOf}</Link> : "—"],
              ["Created", when(d.CreatedAt)],
              ["Build started", when(d.StartedAt)],
              ["Finished", `${when(d.FinishedAt)} · took ${took(d.CreatedAt, d.FinishedAt)}`],
              ["Source object", d.SourceKey ? `${d.SourceKey} · ${mb(d.SourceSize)}` : "— (local build)"],
              ["Files bundle", d.FilesKey ? `${d.FilesKey} · ${mb(d.FilesSize)}` : "—"],
            ]}
          />
        </Section>
        <Section title="Binaries">
          <Table head={["Service", "Type", "Path", "Size", "Go", "sha256"]}>
            {(x.Artifacts ?? []).map((a) => (
              <tr key={a.ServiceName}>
                <Td mono>{a.ServiceName}</Td>
                <Td mono>{a.ServiceType}</Td>
                <Td mono>{a.ServicePath}</Td>
                <Td mono>{mb(a.SizeBytes)}</Td>
                <Td mono>
                  {a.GoVersion} {a.Goarch}
                </Td>
                <Td mono className="max-w-[160px] truncate" >
                  {a.Sha256.slice(0, 16)}…
                </Td>
              </tr>
            ))}
          </Table>
          {(x.Artifacts ?? []).length === 0 && (
            <p className="text-13 text-muted">
              No binaries recorded{(x.Uploaded ?? []).length > 0 ? `; ${(x.Uploaded ?? []).length} were declared by the CLI (${(x.Uploaded ?? []).map((u) => u.ServiceName).join(", ")})` : ""}.
            </p>
          )}
        </Section>
      </div>

      <Section title="Instances" aside="this deployment's own instances">
        <Table head={["Instance", "Service", "Node", "State", "Error", "Started", "Stopped"]}>
          {(x.Instances ?? []).map((i) => (
            <tr key={i.ID}>
              <Td mono>{i.ID.slice(0, 8)}</Td>
              <Td mono>
                {i.ServiceName} ({i.ServiceType})
              </Td>
              <Td mono>{i.NodeName}</Td>
              <Td>
                <State value={i.State} />
              </Td>
              <Td className="max-w-[420px] whitespace-pre-wrap text-12 text-error">{i.Error}</Td>
              <Td className="whitespace-nowrap text-muted">{when(i.StartedAt)}</Td>
              <Td className="whitespace-nowrap text-muted">{when(i.StoppedAt)}</Td>
            </tr>
          ))}
        </Table>
        {(x.Instances ?? []).length === 0 && <p className="text-13 text-muted">None started (it failed before the release, or is still building).</p>}
      </Section>

      <Section title="Jobs" aside="build, verify, fetch, release and notify jobs for this deployment">
        <Table head={["Job", "Kind", "Status", "Attempts", "Last error", "Created", "Finished"]}>
          {(x.Jobs ?? []).map((j) => (
            <tr key={j.ID}>
              <Td mono>#{j.ID}</Td>
              <Td mono>{j.Kind}</Td>
              <Td>
                <State value={j.Status} />
              </Td>
              <Td mono>
                {j.Attempts}/{j.MaxAttempts}
              </Td>
              <Td className="max-w-[420px] break-all text-12 text-error">{j.LastError}</Td>
              <Td className="whitespace-nowrap text-muted">{when(j.CreatedAt)}</Td>
              <Td className="whitespace-nowrap text-muted">{when(j.FinishedAt)}</Td>
            </tr>
          ))}
        </Table>
      </Section>

      <Section title="Build and release log" aside={`${(x.BuildLog ?? []).length} lines`}>
        <Log
          empty="No log lines."
          lines={(x.BuildLog ?? []).map((l) => ({ key: l.Seq, t: clock(l.Ts), text: l.Line, dim: l.Stream === "system" }))}
        />
      </Section>

      <Section title="App output" aside="what this deployment's instances printed, last 300 lines">
        <Log
          empty="Nothing printed (or older than the log retention)."
          lines={(x.AppLogs ?? []).map((l) => ({ key: l.ID, t: clock(l.Ts), text: l.Line, dim: l.Stream === "system", tag: `${l.ServiceName}·${l.InstanceID.slice(0, 4)}` }))}
        />
      </Section>
    </>
  );
}
