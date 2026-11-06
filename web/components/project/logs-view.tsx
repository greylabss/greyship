"use client";

import { useSearchParams } from "next/navigation";
import { useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/primitives/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/primitives/sheet";
import { Switch } from "@/components/primitives/switch";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CommandHint } from "@/components/ui/command-hint";
import { CopyButton } from "@/components/ui/copy-button";
import { inputClass } from "@/components/ui/field";
import { icons } from "@/components/ui/icons";
import type { LogLevel, LogLine } from "@/lib/api/types";
import { toLogLine, type ApiLogLine } from "@/lib/api/loglines";
import { logLine } from "@/lib/fixtures/project";
import { cn } from "@/lib/utils";

const levelClass: Record<LogLevel, string> = { INFO: "text-muted", WARN: "text-warn", ERROR: "text-error" };

// With projectId (real data) live tail polls the API; without it (fixtures) it plays sample lines.
export function LogsView({ initial, services, projectId }: { initial: LogLine[]; services: string[]; projectId?: string }) {
  const params = useSearchParams();
  const [lines, setLines] = useState(initial);
  const [service, setService] = useState(params.get("service") ?? "all");
  const [level, setLevel] = useState(params.get("level") ?? "all");
  const [query, setQuery] = useState("");
  const [range, setRange] = useState("15m");
  const [live, setLive] = useState(true);
  const [open, setOpen] = useState<LogLine | null>(null);
  const counter = useRef(initial.length);
  const box = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!live || !projectId) return;
    const id = setInterval(async () => {
      const res = await fetch(`/v1/projects/${projectId}/logs?tail=300`, { credentials: "include" }).catch(() => null);
      if (!res?.ok) return;
      const rows = (await res.text())
        .split("\n")
        .filter(Boolean)
        .map((l) => JSON.parse(l) as ApiLogLine)
        .filter((r) => r.id);
      setLines(rows.map(toLogLine));
    }, 3000);
    return () => clearInterval(id);
  }, [live, projectId]);

  useEffect(() => {
    if (!live || projectId) return;
    const id = setInterval(() => {
      counter.current += 1;
      const n = counter.current;
      setLines((l) => [...l.slice(-300), logLine(n * 7, n % 4 === 0 ? "worker" : "api")]);
    }, 1400);
    return () => clearInterval(id);
  }, [live, projectId]);

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    return lines.filter(
      (l) => (service === "all" || l.service === service) && (level === "all" || l.level === level) && (!q || `${l.msg} ${JSON.stringify(l.fields)}`.toLowerCase().includes(q)),
    );
  }, [lines, service, level, query]);

  useEffect(() => {
    if (live && box.current) box.current.scrollTop = box.current.scrollHeight;
  }, [shown.length, live]);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <label className="relative min-w-64 flex-1">
          <span className="sr-only">Search logs</span>
          <icons.search className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-dim" />
          <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Search logs…  e.g. ord_8F2K or /v1/orders" className={cn(inputClass, "pl-9")} />
        </label>
        <Filter label="Service" value={service} onChange={setService} options={[["all", "All services"], ...services.map((s) => [s, s] as [string, string])]} />
        <Filter label="Level" value={level} onChange={setLevel} options={[["all", "All levels"], ["ERROR", "Error"], ["WARN", "Warn"], ["INFO", "Info"]]} />
        <Filter label="Time range" value={range} onChange={setRange} options={[["15m", "Last 15 minutes"], ["1h", "Last hour"], ["24h", "Last 24 hours"], ["7d", "Last 7 days"]]} />
        <label className="flex h-10 items-center gap-2 rounded-input border border-border-strong px-3 text-13">
          <Switch checked={live} onCheckedChange={setLive} aria-label="Live tail" />
          <span className="flex items-center gap-1.5">
            {live && <span className="size-1.5 animate-pulse rounded-full bg-ok" aria-hidden="true" />}
            Live
          </span>
        </label>
      </div>

      <div className="overflow-hidden rounded-panel border border-border">
        <div className="flex items-center justify-between border-b border-border bg-surface px-4 py-2 text-12 text-muted">
          <span className="tabular">
            {shown.length} lines{live ? " · streaming" : " · paused"}
          </span>
          <CommandHint command={`grey logs -f${service !== "all" ? ` --service ${service}` : ""}`} />
        </div>
        <div ref={box} className="h-[560px] overflow-auto bg-surface-2 py-2 font-mono text-13 leading-6" role="log" aria-live="off">
          {shown.length === 0 && <p className="px-4 py-10 text-center font-sans text-muted">No lines match these filters.</p>}
          {shown.map((l) => (
            <button
              key={l.id}
              type="button"
              onClick={() => setOpen(l)}
              className={cn(
                "grid w-full grid-cols-[100px_76px_52px_minmax(0,1fr)] gap-3 px-4 text-left hover:bg-surface max-md:grid-cols-[84px_44px_minmax(0,1fr)]",
                l.level === "ERROR" && "bg-error/5",
                open?.id === l.id && "bg-surface",
              )}
            >
              <span className="text-dim tabular">{l.t.slice(0, 12)}</span>
              <span className="truncate text-muted max-md:hidden">{l.instance}</span>
              <span className={levelClass[l.level]}>{l.level}</span>
              <span className={cn("truncate", l.level === "ERROR" ? "text-error" : "text-fg")}>{l.msg}</span>
            </button>
          ))}
        </div>
      </div>

      <Sheet open={open !== null} onOpenChange={(o) => !o && setOpen(null)}>
        <SheetContent className="w-full gap-0 sm:max-w-md">
          {open && (
            <>
              <SheetHeader className="border-b border-border">
                <SheetTitle className="flex items-center gap-2">
                  <Badge tone={open.level === "ERROR" ? "error" : "neutral"}>{open.level}</Badge>
                  <span className="truncate font-mono text-14">{open.instance}</span>
                </SheetTitle>
                <SheetDescription className="font-mono text-12">Oct 24 · {open.t}</SheetDescription>
              </SheetHeader>
              <div className="flex flex-col gap-4 overflow-auto p-4">
                <p className={cn("font-mono text-13", open.level === "ERROR" ? "text-error" : "text-fg")}>{open.msg}</p>
                <dl className="divide-y divide-border rounded-panel border border-border">
                  {Object.entries(open.fields).map(([k, v]) => (
                    <div key={k} className="flex items-center justify-between gap-3 px-3 py-2">
                      <dt className="font-mono text-12 text-muted">{k}</dt>
                      <dd className="flex min-w-0 items-center gap-1 font-mono text-13">
                        <span className="truncate">{String(v)}</span>
                        <CopyButton value={String(v)} label={`Copy ${k}`} />
                      </dd>
                    </div>
                  ))}
                </dl>
                <div className="flex flex-wrap gap-2">
                  <Button
                    size="sm"
                    onClick={() => {
                      navigator.clipboard.writeText(JSON.stringify(open.fields, null, 2)).catch(() => undefined);
                      toast("JSON copied");
                    }}
                  >
                    <icons.copy />
                    Copy JSON
                  </Button>
                  {"trace_id" in open.fields && (
                    <Button size="sm" variant="ghost" onClick={() => setQuery(String(open.fields.trace_id))}>
                      Show lines with this trace
                    </Button>
                  )}
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => {
                      setService(open.service);
                      setOpen(null);
                    }}
                  >
                    Only {open.service}
                  </Button>
                </div>
              </div>
            </>
          )}
        </SheetContent>
      </Sheet>
    </div>
  );
}

function Filter({ label, value, onChange, options }: { label: string; value: string; onChange: (v: string) => void; options: [string, string][] }) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger aria-label={label} className="h-10 min-w-36 text-13">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {options.map(([v, l]) => (
          <SelectItem key={v} value={v}>
            {l}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
