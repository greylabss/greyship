"use client";

import { useState } from "react";
import { toast } from "sonner";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/primitives/sheet";
import { Switch } from "@/components/primitives/switch";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/primitives/tooltip";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Chart } from "@/components/ui/chart";
import { CommandHint } from "@/components/ui/command-hint";
import { icons } from "@/components/ui/icons";
import type { RuntimeInfo } from "@/lib/api/types";
import { cn } from "@/lib/utils";
import { ServicePicker, useService } from "./service-picker";

const dump = `goroutine 4129 [chan receive, 38 minutes]:
github.com/haki/acme-api/handlers.(*Orders).awaitPayment(0xc0001a2000, {0x9a1f20, 0xc000488000})
	/src/handlers/orders.go:88 +0x1c5
created by github.com/haki/acme-api/handlers.(*Orders).Create in goroutine 4101
	/src/handlers/orders.go:61 +0x2f4

63 goroutines with this stack · blocked on a channel nobody sends to after the
request context is cancelled. Select on ctx.Done() next to the receive.`;

export function RuntimeView({ services, info }: { services: string[]; info: RuntimeInfo }) {
  const service = useService(services);
  const [leakOpen, setLeakOpen] = useState(Boolean(info.leak));
  const [dumpOpen, setDumpOpen] = useState(false);
  const [capturing, setCapturing] = useState<number | null>(null);
  const [captured, setCaptured] = useState("14:52");
  const [tuning, setTuning] = useState({ maxprocs: true, memlimit: true, pgo: info.tuning.pgo });

  function capture() {
    let n = 0;
    setCapturing(0);
    const id = setInterval(() => {
      n += 1;
      if (n < 30) return setCapturing(n);
      clearInterval(id);
      setCapturing(null);
      setCaptured("just now");
      toast.success("CPU profile captured", { description: `30s from ${service}-2 · used for PGO on next deploy` });
    }, 80);
  }

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <ServicePicker services={services} />
        <span className="text-13 text-muted">No code changes needed. Profiles need <code className="font-mono text-12">import _ &quot;greyship.tech/agent&quot;</code></span>
      </div>

      {info.leak && leakOpen && (
        <div role="status" className="flex flex-wrap items-start gap-3 rounded-panel border border-accent/50 bg-surface-2 px-4 py-4">
          <icons.alert className="mt-0.5 text-accent" />
          <div className="flex min-w-0 flex-1 flex-col gap-1">
            <span className="font-medium">Possible goroutine leak</span>
            <span className="text-13 text-muted">
              Goroutines rose from {info.leak.from} to {info.leak.to} since {info.leak.since} while traffic stayed flat. {info.leak.blocked} are blocked on a channel
              receive in <span className="font-mono text-fg">{info.leak.site}</span>.
            </span>
          </div>
          <div className="flex gap-2">
            <Button size="sm" onClick={() => setDumpOpen(true)}>
              View goroutine dump
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setLeakOpen(false)}>
              Dismiss
            </Button>
          </div>
        </div>
      )}

      <dl className="grid grid-cols-2 gap-px overflow-hidden rounded-panel border border-border bg-border lg:grid-cols-4">
        {info.stats.map((s) => (
          <div key={s.label} className="flex flex-col gap-1 bg-bg px-4 py-4">
            <dt className="text-12 text-muted">{s.label}</dt>
            <dd className="text-20 font-semibold tabular">{s.value}</dd>
            <dd className="text-12 text-muted">{s.note}</dd>
          </div>
        ))}
      </dl>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <section className="rounded-panel border border-border p-4">
          <Chart series={info.goroutines} tone={info.leak ? "accent" : "muted"} height={150} />
        </section>
        <section className="rounded-panel border border-border p-4">
          <Chart series={info.heap} height={150} />
        </section>
      </div>

      <section className="flex flex-col gap-4 rounded-panel border border-border p-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="flex flex-col gap-1">
            <h2 className="flex items-center gap-2 font-semibold">
              CPU profile · 30s <Badge>Pro</Badge>
            </h2>
            <span className="text-13 text-muted">Captured {captured} from {service}-2 · also used for automatic PGO on the next deploy</span>
          </div>
          <div className="flex gap-2">
            <Button size="sm" variant="ghost" onClick={() => toast("Downloading profile", { description: `${service}-cpu.pprof · 184 KB` })}>
              Download .pprof
            </Button>
            <Button size="sm" disabled={capturing !== null} onClick={capture}>
              {capturing === null ? "Capture new profile" : `Capturing… ${capturing}s`}
            </Button>
          </div>
        </div>
        {capturing !== null && (
          <div className="h-1 overflow-hidden rounded-full bg-surface" role="progressbar" aria-valuemin={0} aria-valuemax={30} aria-valuenow={capturing}>
            <div className="h-full bg-fg transition-[width] duration-100 ease-out" style={{ width: `${(capturing / 30) * 100}%` }} />
          </div>
        )}
        <Flame frames={info.flame} />
        <CommandHint command={`grey pprof cpu ${service}`} />
      </section>

      <section className="flex flex-col divide-y divide-border rounded-panel border border-border">
        <div className="px-4 py-3">
          <h2 className="font-semibold">Runtime tuning</h2>
          <p className="text-13 text-muted">Greyship sets these for you. Override only if you know you need to.</p>
        </div>
        <Toggle title="Auto GOMAXPROCS" body="Match Go's scheduler to the CPU you actually get." value={`= ${info.tuning.gomaxprocs}`} on={tuning.maxprocs} set={(v) => setTuning((t) => ({ ...t, maxprocs: v }))} />
        <Toggle title="Auto GOMEMLIMIT" body="Let the GC work before hitting the memory limit." value={`= ${info.tuning.gomemlimit}`} on={tuning.memlimit} set={(v) => setTuning((t) => ({ ...t, memlimit: v }))} />
        <Toggle title="Profile-guided optimisation" body="Build with real production profiles for faster code." value="pgo = auto" on={tuning.pgo} set={(v) => setTuning((t) => ({ ...t, pgo: v }))} />
      </section>

      <Sheet open={dumpOpen} onOpenChange={setDumpOpen}>
        <SheetContent className="w-full sm:max-w-xl">
          <SheetHeader>
            <SheetTitle>Goroutine dump</SheetTitle>
            <SheetDescription>{service}-2 · grouped by stack · 184 goroutines</SheetDescription>
          </SheetHeader>
          <pre className="mx-4 overflow-auto rounded-panel border border-border bg-surface-2 p-4 font-mono text-12 leading-5 text-fg">{dump}</pre>
        </SheetContent>
      </Sheet>
    </div>
  );
}

function Toggle({ title, body, value, on, set }: { title: string; body: string; value: string; on: boolean; set: (v: boolean) => void }) {
  return (
    <label className="flex items-center gap-4 px-4 py-3">
      <Switch
        checked={on}
        onCheckedChange={(v) => {
          set(v);
          toast(v ? `${title} on` : `${title} off`, { description: "Applies on the next instance start" });
        }}
      />
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="font-medium">{title}</span>
        <span className="text-13 text-muted">{body}</span>
      </span>
      <span className={cn("font-mono text-13", on ? "text-fg" : "text-dim line-through")}>{value}</span>
    </label>
  );
}

function Flame({ frames }: { frames: RuntimeInfo["flame"] }) {
  const depth = Math.max(...frames.map((f) => f.depth)) + 1;
  return (
    <div className="flex flex-col gap-0.5 overflow-x-auto" role="img" aria-label="CPU flame graph; handlers.CreateOrder uses 55% of CPU">
      {Array.from({ length: depth }, (_, d) => (
        <div key={d} className="relative h-7 min-w-[640px]">
          {frames
            .filter((f) => f.depth === d)
            .map((f) => (
              <Tooltip key={f.name}>
                <TooltipTrigger asChild>
                  <button
                    type="button"
                    className={cn(
                      "absolute inset-y-0 truncate rounded-control border px-2 text-left font-mono text-12 transition-colors duration-150 ease-out hover:border-dim",
                      f.hot ? "border-fg bg-surface text-fg" : "border-border bg-surface text-muted",
                    )}
                    style={{ left: `${f.offset}%`, width: `calc(${f.pct}% - 2px)` }}
                  >
                    {f.name}
                  </button>
                </TooltipTrigger>
                <TooltipContent>
                  {f.name} · {f.pct}% CPU
                </TooltipContent>
              </Tooltip>
            ))}
        </div>
      ))}
    </div>
  );
}
