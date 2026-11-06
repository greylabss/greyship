"use client";

import Link from "next/link";
import { useState } from "react";
import { ToggleGroup, ToggleGroupItem } from "@/components/primitives/toggle-group";
import { Badge } from "@/components/ui/badge";
import { CopyButton } from "@/components/ui/copy-button";
import { icons } from "@/components/ui/icons";
import { Status } from "@/components/ui/status";
import type { Service } from "@/lib/api/types";
import { compact } from "@/lib/format";
import { cn } from "@/lib/utils";
import { ServiceMenu } from "./actions";
import { serviceState, ServiceStatus } from "./service-state";

export function ServicesView({ team, project, services, demo = true }: { team: string; project: string; services: Service[]; demo?: boolean }) {
  const [view, setView] = useState<"list" | "map">("list");
  return (
    <section className="flex flex-col gap-3">
      <div className="flex h-9 items-center justify-between gap-3">
        <h2 className="text-16 font-semibold">
          Services <span className="font-normal text-muted tabular">{services.length}</span>
        </h2>
        <ToggleGroup type="single" value={view} onValueChange={(v) => v && setView(v as "list" | "map")} aria-label="Services view" className="rounded-input border border-border-strong p-0.5">
          <ToggleGroupItem value="list" className="h-8 gap-1.5 px-2.5 text-13">
            <icons.list />
            List
          </ToggleGroupItem>
          <ToggleGroupItem value="map" className="h-8 gap-1.5 px-2.5 text-13">
            <icons.domains />
            Map
          </ToggleGroupItem>
        </ToggleGroup>
      </div>
      {view === "list" ? <ServiceList team={team} project={project} services={services} demo={demo} /> : <ServiceMap team={team} project={project} services={services} />}
    </section>
  );
}

function ServiceList({ team, project, services, demo }: { team: string; project: string; services: Service[]; demo: boolean }) {
  return (
    <div className="relative overflow-x-auto rounded-panel border border-border">
      <div className="min-w-[760px] divide-y divide-border">
        <div className="grid grid-cols-[180px_minmax(0,1fr)_minmax(0,1.1fr)_96px_40px] gap-4 bg-surface px-4 py-2.5 text-12 font-medium text-muted">
          <span>Service</span>
          <span>Status</span>
          <span>Address</span>
          <span className="text-right">Req · 24h</span>
          <span className="sr-only">Actions</span>
        </div>
        {services.map((s) => (
          <div key={s.name} className="grid grid-cols-[180px_minmax(0,1fr)_minmax(0,1.1fr)_96px_40px] items-center gap-4 px-4 py-3 transition-colors duration-150 ease-out hover:bg-surface-2">
            <div className="flex min-w-0 flex-col gap-1">
              <Link href={`/${team}/${project}/logs?service=${s.name}`} className="truncate font-medium hover:underline">
                {s.name}
              </Link>
              <span className="flex gap-1.5">
                <Badge mono>{s.type}</Badge>
                {s.visibility === "private" && <Badge>private</Badge>}
              </span>
            </div>
            <ServiceStatus service={s} />
            <span className="flex min-w-0 items-center gap-1 text-muted">
              {s.url ? (
                <>
                  <span className="truncate font-mono text-13">{s.url}</span>
                  <CopyButton value={s.url} label="Copy address" />
                </>
              ) : s.queue ? (
                <span className="truncate font-mono text-13">
                  queue {s.queue.name} · {s.queue.ready} ready
                </span>
              ) : (
                <span className="truncate font-mono text-13">{s.schedule}</span>
              )}
            </span>
            <span className="text-right font-mono text-13 tabular">{s.requests24h === null ? "—" : compact(s.requests24h)}</span>
            <ServiceMenu team={team} project={project} service={s.name} url={s.url} restart={demo} />
          </div>
        ))}
      </div>
    </div>
  );
}

// Railway-style map: internet → public services → private services, background work on the side.
function ServiceMap({ team, project, services }: { team: string; project: string; services: Service[] }) {
  const pub = services.filter((s) => s.type === "web" && s.visibility === "public");
  const priv = services.filter((s) => s.type === "web" && s.visibility === "private");
  const bg = services.filter((s) => s.type !== "web");

  return (
    <div className="relative overflow-x-auto rounded-panel border border-border bg-surface-2 p-6 [background-image:radial-gradient(var(--gs-border)_1px,transparent_1px)] [background-size:20px_20px]">
      <div className="grid min-w-[760px] grid-cols-[120px_1fr_1fr_1fr] items-center gap-8">
        <div className="flex flex-col items-center gap-2 text-muted">
          <span className="inline-flex size-10 items-center justify-center rounded-full border border-border-strong bg-bg">
            <icons.domains />
          </span>
          <span className="text-12">Internet</span>
        </div>
        <MapColumn title="Public" team={team} project={project} services={pub} connector />
        <MapColumn title="Private · .internal" team={team} project={project} services={priv} connector={priv.length > 0} />
        <MapColumn title="Background" team={team} project={project} services={bg} />
      </div>
      <p className="mt-6 text-12 text-muted">Lines show calls seen in the last 24h through the internal router. Services never talk to each other directly.</p>
    </div>
  );
}

function MapColumn({ title, team, project, services, connector }: { title: string; team: string; project: string; services: Service[]; connector?: boolean }) {
  return (
    <div className={cn("relative flex flex-col gap-3", connector && "before:absolute before:-left-8 before:top-1/2 before:h-px before:w-8 before:bg-border-strong")}>
      <span className="text-12 text-dim">{title}</span>
      {services.length === 0 && <span className="rounded-panel border border-dashed border-border-strong px-3 py-4 text-12 text-dim">None</span>}
      {services.map((s) => {
        const st = serviceState(s);
        return (
          <Link
            key={s.name}
            href={`/${team}/${project}/logs?service=${s.name}`}
            className={cn(
              "flex flex-col gap-1.5 rounded-panel border bg-bg p-3 transition-colors duration-150 ease-out hover:border-dim",
              st.tone === "error" ? "border-error/60" : "border-border-strong",
            )}
          >
            <span className="flex items-center justify-between gap-2">
              <span className="font-medium">{s.name}</span>
              <Badge mono>{s.type}</Badge>
            </span>
            <Status tone={st.tone} label={st.label} className="text-12" />
            {st.tone === "error" && st.detail && <span className="line-clamp-2 font-mono text-12 text-error">{st.detail}</span>}
            {s.calls.length > 0 && <span className="text-12 text-muted">calls {s.calls.join(", ")}</span>}
            {s.state.kind === "scheduled" && s.state.lastRun && (
              <span className="text-12 text-muted">
                last run {s.state.lastRun.at} · exit {s.state.lastRun.exitCode}
              </span>
            )}
          </Link>
        );
      })}
    </div>
  );
}
