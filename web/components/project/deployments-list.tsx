"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";
import { DropdownMenuItem, DropdownMenuSeparator } from "@/components/primitives/dropdown-menu";
import { Tabs, TabsList, TabsTrigger } from "@/components/primitives/tabs";
import { Badge } from "@/components/ui/badge";
import { icons } from "@/components/ui/icons";
import { Status, type Tone } from "@/components/ui/status";
import type { Deployment, DeploymentStatus } from "@/lib/api/types";
import { RowMenu, useRedeploy } from "./actions";

const status: Record<DeploymentStatus, { tone: Tone; label: string }> = {
  queued: { tone: "idle", label: "Queued" },
  building: { tone: "progress", label: "Building" },
  ready: { tone: "ok", label: "Ready" },
  failed: { tone: "error", label: "Failed" },
  superseded: { tone: "idle", label: "Superseded" },
};

const sourceLabel = { cli: "grey deploy", dashboard: "Dashboard", github: "GitHub push" };

// real: Redeploy and Roll back call the API (F-27); otherwise they're the demo's toasts.
export function DeploymentsList({ team, project, deployments, real = false }: { team: string; project: string; deployments: Deployment[]; real?: boolean }) {
  const [env, setEnv] = useState("all");
  const router = useRouter();
  const redeploy = useRedeploy(team, project);
  const current = deployments.find((d) => d.env === "production" && d.status === "ready");
  const shown = deployments.filter((d) => env === "all" || d.env === env);

  return (
    <div className="flex flex-col gap-4">
      <Tabs value={env} onValueChange={setEnv}>
        <TabsList>
          <TabsTrigger value="all">All</TabsTrigger>
          <TabsTrigger value="production">Production</TabsTrigger>
          <TabsTrigger value="preview">Preview</TabsTrigger>
        </TabsList>
      </Tabs>
      <ul className="divide-y divide-border rounded-panel border border-border">
        {shown.map((d) => {
          const s = status[d.status];
          const live = d.id === current?.id;
          return (
            <li
              key={d.id}
              onClick={() => router.push(`/${team}/${project}/deployments/${d.id}`)}
              className="grid cursor-pointer grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1.5 px-4 py-3.5 transition-colors duration-150 ease-out hover:bg-surface-2 md:grid-cols-[110px_minmax(0,1fr)_200px_120px_40px]"
            >
              <span className="flex items-center gap-2 max-md:order-2">
                <Status tone={s.tone} label={s.label} className="text-13" />
              </span>
              <span className="flex min-w-0 flex-col gap-0.5 max-md:order-1 max-md:col-span-2">
                <Link href={`/${team}/${project}/deployments/${d.id}`} className="truncate font-medium hover:underline" onClick={(e) => e.stopPropagation()}>
                  {d.message}
                </Link>
                {d.failure ? (
                  <span className="truncate font-mono text-12 text-error">{d.failure}</span>
                ) : (
                  <span className="flex items-center gap-1.5 text-12 text-muted">
                    {live && <Badge tone="ok">Current</Badge>}
                    <Badge>{d.env === "production" ? "Production" : "Preview"}</Badge>
                    via {sourceLabel[d.source]}
                  </span>
                )}
              </span>
              <span className="flex min-w-0 items-center gap-2 text-12 text-muted max-md:order-3">
                {d.branch && (
                  <>
                    <icons.branch className="text-dim" />
                    <span className="truncate font-mono">{d.branch}</span>
                  </>
                )}
                <span className="font-mono">{d.sha}</span>
              </span>
              <span className="text-13 text-muted max-md:order-4 md:text-right">
                {d.ago} · <span className="font-mono text-12">{d.duration ?? "…"}</span>
              </span>
              <span className="max-md:order-5 max-md:hidden" onClick={(e) => e.stopPropagation()}>
                <RowMenu label={`Actions for ${d.sha}`}>
                  {d.previewUrl && (
                    <DropdownMenuItem asChild>
                      <a href={`https://${d.previewUrl}`}>
                        <icons.external />
                        Visit preview
                      </a>
                    </DropdownMenuItem>
                  )}
                  {(real ? live : true) && (
                    <DropdownMenuItem
                      disabled={redeploy.busy}
                      onSelect={() => {
                        if (real) return void redeploy.run(d.id);
                        toast.success(`Redeploying ${d.sha}`);
                        router.push(`/${team}/${project}/deployments/live`);
                      }}
                    >
                      <icons.deployments />
                      Redeploy
                    </DropdownMenuItem>
                  )}
                  {d.status === "superseded" && current && (
                    <DropdownMenuItem
                      disabled={redeploy.busy}
                      onSelect={() => {
                        if (real) return void redeploy.run(d.id);
                        toast.success(`Production now serves ${d.sha}`, { description: `Rolled back from ${current.sha}` });
                      }}
                    >
                      <icons.overview />
                      Roll back to this
                    </DropdownMenuItem>
                  )}
                  <DropdownMenuSeparator />
                  <DropdownMenuItem
                    onSelect={() => {
                      navigator.clipboard.writeText(d.sha).catch(() => undefined);
                      toast("Commit SHA copied", { description: d.sha });
                    }}
                  >
                    <icons.copy />
                    Copy commit SHA
                  </DropdownMenuItem>
                </RowMenu>
              </span>
            </li>
          );
        })}
      </ul>
      <p className="text-12 text-muted">Any deployment that went live can be rolled back to. Nothing is rebuilt.</p>
    </div>
  );
}
