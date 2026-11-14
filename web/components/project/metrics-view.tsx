"use client";

import { useState } from "react";
import { ToggleGroup, ToggleGroupItem } from "@/components/primitives/toggle-group";
import { Chart, ChartLegend } from "@/components/ui/chart";
import type { Series } from "@/lib/api/types";
import { ServicePicker } from "./service-picker";

type Metrics = Record<"requests" | "latency" | "errors" | "instances" | "memory" | "cpu", Series>;

export function MetricsView({ services, metrics }: { services: string[]; metrics: Metrics }) {
  const [range, setRange] = useState("24h");
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <ServicePicker services={services} />
          <ToggleGroup type="single" value={range} onValueChange={(v) => v && setRange(v)} aria-label="Time range" className="rounded-input border border-border-strong p-0.5">
            {["1h", "24h", "7d", "30d"].map((r) => (
              <ToggleGroupItem key={r} value={r} className="h-8 px-3 font-mono text-12">
                {r}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </div>
        <ChartLegend />
      </div>

      <dl className="grid grid-cols-2 gap-px overflow-hidden rounded-panel border border-border bg-border lg:grid-cols-4">
        {[
          ["Requests", "57.9k", "+12% vs yesterday"],
          ["Latency p95", "41 ms", "p50 9 ms · p99 112 ms"],
          ["Cold starts", "1", "612 ms · 12 resumes avg 18 ms"],
          ["Running time", "14.2 h", "asleep 9.8 h"],
        ].map(([l, v, n]) => (
          <div key={l} className="flex flex-col gap-1 bg-bg px-4 py-4">
            <dt className="text-12 text-muted">{l}</dt>
            <dd className="text-20 font-semibold tabular">{v}</dd>
            <dd className="text-12 text-muted">{n}</dd>
          </div>
        ))}
      </dl>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {(Object.keys(metrics) as (keyof Metrics)[]).map((k) => (
          <section key={k} className="rounded-panel border border-border p-4">
            <Chart series={metrics[k]} height={140} />
          </section>
        ))}
      </div>
      <p className="text-12 text-muted">Gaps at zero are sleep: no instances running. Click a marker for details.</p>
    </div>
  );
}
