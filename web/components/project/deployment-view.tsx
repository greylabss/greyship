"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { Button, buttonClass } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { icons } from "@/components/ui/icons";
import { Status } from "@/components/ui/status";
import type { BuildLine, DeploymentDetail } from "@/lib/api/types";
import { cn } from "@/lib/utils";
import { RollbackButton, useRedeploy } from "./actions";

const lineTone: Record<NonNullable<BuildLine["tone"]>, string> = {
  dim: "text-dim",
  ok: "text-ok",
  error: "text-error",
  warn: "text-warn",
};

// "live" deployments stream their build log in, then flip to Ready — the full deploy moment in the demo.
// With streamId (real data, deploy not finished yet) the log keeps coming from the API until the deploy ends.
export function DeploymentView({
  team,
  deployment,
  live,
  currentSha,
  actions = true,
  real = false,
  streamId,
}: {
  team: string;
  deployment: DeploymentDetail;
  live: boolean;
  currentSha?: string;
  actions?: boolean;
  real?: boolean;
  streamId?: string;
}) {
  const router = useRouter();
  const redeploy = useRedeploy(team, deployment.project);
  const [count, setCount] = useState(live ? 0 : deployment.log.length);
  const [follow, setFollow] = useState(true);
  const [more, setMore] = useState<BuildLine[]>([]);
  const [ended, setEnded] = useState<string | null>(null);
  const logRef = useRef<HTMLDivElement>(null);
  // Only the demo's "live" deployment plays its log line by line; real logs are complete or streaming.
  const done = !live || count >= deployment.log.length;
  const streaming = Boolean(streamId) && ended === null;
  const status = live && !done ? "building" : streaming ? "building" : ((ended as typeof deployment.status | null) ?? deployment.status);
  const shown = [...deployment.log.slice(0, count), ...more];

  useEffect(() => {
    if (!streamId) return;
    let after = deployment.log.length;
    let stop = false;
    const tick = async () => {
      const res = await fetch(`/v1/deployments/${streamId}/logs?after=${after}`, { credentials: "include" }).catch(() => null);
      if (res?.ok) {
        const entries = (await res.text())
          .split("\n")
          .filter(Boolean)
          .map((l) => JSON.parse(l) as { line?: { seq: number; stream: string; line: string; ts: string }; end?: { status: string } });
        const lines: BuildLine[] = [];
        for (const e of entries) {
          if (e.line) {
            after = Math.max(after, e.line.seq);
            lines.push({ t: new Date(e.line.ts).toISOString().slice(11, 19), text: e.line.line, tone: e.line.stream === "system" ? "dim" : undefined });
          }
          if (e.end) {
            stop = true;
            setEnded(e.end.status === "ready" ? "ready" : e.end.status === "superseded" ? "superseded" : "failed");
            router.refresh();
          }
        }
        if (lines.length) setMore((m) => [...m, ...lines]);
      }
      if (!stop) timer = setTimeout(tick, 1000);
    };
    let timer = setTimeout(tick, 1000);
    return () => {
      stop = true;
      clearTimeout(timer);
    };
  }, [streamId, deployment.log.length, router]);

  useEffect(() => {
    if (!live || done) return;
    const id = setTimeout(() => setCount((c) => c + 1), count === 0 ? 400 : 380);
    return () => clearTimeout(id);
  }, [live, done, count]);

  useEffect(() => {
    if (live && done) toast.success(`${deployment.project} is live`, { description: `Deployed ${deployment.sha} in 8.3s` });
  }, [live, done, deployment.project, deployment.sha]);

  useEffect(() => {
    if (follow && logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight;
  }, [count, follow, more.length]);

  const text = shown.map((l) => `${l.t}  ${l.text}`).join("\n");
  const tone = status === "ready" ? "ok" : status === "failed" ? "error" : status === "building" ? "progress" : "idle";
  const base = `/${team}/${deployment.project}`;

  return (
    <div className="flex flex-col gap-6">
      <nav aria-label="Breadcrumb" className="flex items-center gap-2 text-13 text-muted">
        <Link href={`${base}/deployments`} className="hover:text-fg">
          Deployments
        </Link>
        <span aria-hidden="true">/</span>
        <span className="font-mono text-fg">{deployment.sha}</span>
      </nav>

      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex min-w-0 flex-col gap-2">
          <span className="flex items-center gap-2 text-13">
            <Status tone={tone} label={status === "ready" ? "Ready" : status === "building" ? "Building" : status === "failed" ? "Failed" : "Superseded"} />
            <span className="text-muted">· {deployment.env === "production" ? "Production" : "Preview"}</span>
          </span>
          <h1 className="text-28 font-semibold tracking-tight">{deployment.message}</h1>
        </div>
        <div className="flex flex-wrap gap-2">
          {status === "ready" && (deployment.url ?? deployment.previewUrl) && (
            <a href={`https://${deployment.url ?? deployment.previewUrl}`} className={buttonClass("secondary")}>
              Visit
              <icons.external />
            </a>
          )}
          {real && status === "superseded" && currentSha && (
            <RollbackButton sha={deployment.sha} current={currentSha} busy={redeploy.busy} onConfirm={() => redeploy.run(deployment.id)} />
          )}
          {real && status === "ready" && (
            <Button disabled={redeploy.busy} onClick={() => redeploy.run(deployment.id)}>
              {redeploy.busy ? "Starting…" : "Redeploy"}
            </Button>
          )}
          {actions && deployment.status === "superseded" && currentSha && <RollbackButton sha={deployment.sha} current={currentSha} />}
          {actions && status !== "building" && (
            <Button onClick={() => toast.success(`Redeploying ${deployment.sha}`, { description: "Uses cached modules · usually under 10s" })}>Redeploy</Button>
          )}
          {actions && status === "building" && (
            <Button variant="destructive" onClick={() => toast("Build cancelled", { description: `${currentSha ?? "Previous deployment"} keeps serving` })}>
              Cancel build
            </Button>
          )}
        </div>
      </div>

      {live && done && (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-panel border border-ok/40 bg-surface-2 px-4 py-3">
          <span className="flex items-center gap-2">
            <icons.check className="text-ok" />
            Live at <span className="font-mono text-13">{deployment.url ?? deployment.previewUrl ?? deployment.project}</span>
          </span>
          <Link href={base} className={buttonClass("primary", "sm")}>
            Go to project
          </Link>
        </div>
      )}

      <div className="grid grid-cols-1 items-start gap-6 lg:grid-cols-[280px_minmax(0,1fr)]">
        <aside className="flex flex-col gap-5 rounded-panel border border-border p-4">
          <dl className="flex flex-col gap-3 text-13">
            <Row label="Commit">
              <span className="flex items-center gap-1 font-mono">
                {deployment.sha}
                <CopyButton value={deployment.sha} label="Copy SHA" />
              </span>
            </Row>
            {deployment.branch && (
              <Row label="Branch">
                <span className="font-mono">{deployment.branch}</span>
              </Row>
            )}
            {deployment.author && <Row label="Author">{deployment.author}</Row>}
            <Row label="Source">{deployment.source === "cli" ? "grey deploy (CLI)" : deployment.source === "github" ? "GitHub push" : "Dashboard"}</Row>
            {deployment.goVersion && (
              <Row label="Go version">
                <span className="font-mono">{deployment.goVersion}</span>
              </Row>
            )}
            {deployment.rollbackOf && (
              <Row label="Binaries from">
                <Link href={`${base}/deployments/${deployment.rollbackOf}`} className="font-mono hover:underline">
                  {deployment.rollbackOf.slice(0, 8)}
                </Link>
              </Row>
            )}
            <Row label={deployment.rollbackOf ? "Took" : "Build time"}>
              <span className="font-mono">{done ? (deployment.duration ?? "8.3s") : "…"}</span>
            </Row>
            <Row label="Created">{deployment.createdAt}</Row>
          </dl>
          <div className="flex flex-col gap-2 border-t border-border pt-4">
            <span className="text-12 text-muted">Binaries</span>
            {deployment.built.map((b, i) => (
              <div key={b.name} className="flex justify-between font-mono text-13">
                <span className={cn(!done && i >= Math.max(0, count - 4) && "text-dim")}>{b.name}</span>
                <span className="text-muted">{b.size}</span>
              </div>
            ))}
          </div>
        </aside>

        <section aria-label="Build log" className="flex min-w-0 flex-col overflow-hidden rounded-panel border border-border">
          <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border bg-surface px-4 py-2">
            <span className="flex items-center gap-2 text-13 font-medium">
              Build log
              {status === "building" && <span className="size-1.5 animate-pulse rounded-full bg-accent" aria-hidden="true" />}
            </span>
            <div className="flex items-center gap-1">
              <label className="mr-2 flex items-center gap-1.5 text-12 text-muted">
                <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} className="accent-[var(--gs-text)]" />
                Follow
              </label>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => {
                  navigator.clipboard.writeText(text).catch(() => undefined);
                  toast("Build log copied", { description: `${shown.length} lines` });
                }}
              >
                <icons.copy />
                Copy
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => {
                  const a = document.createElement("a");
                  a.href = URL.createObjectURL(new Blob([text + "\n"], { type: "text/plain" }));
                  a.download = `${deployment.project}-${deployment.sha}-build.log`;
                  a.click();
                  URL.revokeObjectURL(a.href);
                }}
              >
                Download
              </Button>
            </div>
          </div>
          <div ref={logRef} className="max-h-[520px] min-h-[320px] overflow-auto bg-surface-2 py-3 font-mono text-13 leading-6">
            {shown.map((l, i) => (
              <div key={i} className="flex gap-4 px-4 hover:bg-surface">
                <span className="w-16 shrink-0 select-none text-dim tabular">{l.t}</span>
                <span className={cn("whitespace-pre", l.tone ? lineTone[l.tone] : "text-fg")}>{l.text}</span>
              </div>
            ))}
            {!done && (
              <div className="flex gap-4 px-4">
                <span className="w-16 shrink-0 text-dim">…</span>
                <span className="inline-block h-4 w-2 animate-pulse bg-muted" aria-hidden="true" />
              </div>
            )}
          </div>
        </section>
      </div>
    </div>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <dt className="text-muted">{label}</dt>
      <dd className="min-w-0 truncate text-right">{children}</dd>
    </div>
  );
}
