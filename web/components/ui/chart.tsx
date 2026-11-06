"use client";

import { useState } from "react";
import type { ChartEvent, Series } from "@/lib/api/types";
import { cn } from "@/lib/utils";

const W = 600;
const H = 160;
const PAD = 6;

const eventColor: Record<ChartEvent["kind"], string> = {
  deploy: "var(--gs-text-muted)",
  wake: "var(--gs-text-dim)",
  restart: "var(--gs-warn)",
  crash: "var(--gs-error)",
};

function fmt(v: number, unit: string) {
  const n = Number.isInteger(v) ? v.toLocaleString("en-US") : v.toFixed(1);
  return unit === "%" ? `${n}%` : unit ? `${n} ${unit}` : n;
}

// Line chart with deploy/wake/restart markers and a hover readout (design.md §2, Render pattern).
export function Chart({ series, tone = "muted", height = 160, className }: { series: Series; tone?: "muted" | "accent"; height?: number; className?: string }) {
  const [hover, setHover] = useState<number | null>(null);
  const [eventOpen, setEventOpen] = useState<ChartEvent | null>(null);
  const { values, events, unit } = series;
  const max = Math.max(...values, 1) * 1.15;
  const step = W / (values.length - 1);
  const y = (v: number) => H - PAD - (v / max) * (H - PAD * 2);
  const points = values.map((v, i) => `${i * step},${y(v)}`).join(" ");
  const stroke = tone === "accent" ? "var(--gs-accent)" : "var(--gs-text)";
  const shown = hover ?? values.length - 1;

  return (
    <div className={cn("flex flex-col gap-2", className)}>
      <div className="flex items-baseline justify-between gap-3">
        <span className="text-13 text-muted">{series.label}</span>
        <span className="font-mono text-13 tabular">
          {fmt(values[shown], unit)}
          <span className="ml-2 text-12 text-dim">{hover === null ? "now" : `${String(hover).padStart(2, "0")}:00`}</span>
        </span>
      </div>
      <div className="relative">
        <svg
          viewBox={`0 0 ${W} ${H}`}
          preserveAspectRatio="none"
          className="block w-full"
          style={{ height }}
          role="img"
          aria-label={`${series.label} over the last 24 hours, now ${fmt(values.at(-1)!, unit)}`}
          onPointerLeave={() => setHover(null)}
          onPointerMove={(e) => {
            const r = e.currentTarget.getBoundingClientRect();
            setHover(Math.max(0, Math.min(values.length - 1, Math.round(((e.clientX - r.left) / r.width) * (values.length - 1)))));
          }}
        >
          {[0.25, 0.5, 0.75].map((f) => (
            <line key={f} x1="0" x2={W} y1={H * f} y2={H * f} stroke="var(--gs-border)" strokeDasharray="2 4" vectorEffect="non-scaling-stroke" />
          ))}
          <line x1="0" x2={W} y1={H - 0.5} y2={H - 0.5} stroke="var(--gs-border-strong)" vectorEffect="non-scaling-stroke" />
          {events.map((ev) => (
            <line key={ev.at} x1={ev.at * step} x2={ev.at * step} y1="0" y2={H} stroke={eventColor[ev.kind]} strokeDasharray="3 3" vectorEffect="non-scaling-stroke" opacity="0.7" />
          ))}
          <polygon points={`0,${H} ${points} ${W},${H}`} fill={stroke} fillOpacity="0.05" />
          <polyline points={points} fill="none" stroke={stroke} strokeWidth="1.5" vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
          {hover !== null && <line x1={hover * step} x2={hover * step} y1="0" y2={H} stroke="var(--gs-text-dim)" vectorEffect="non-scaling-stroke" />}
        </svg>
        {hover !== null && (
          <span
            aria-hidden="true"
            className="pointer-events-none absolute size-2 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-bg"
            style={{ left: `${(hover / (values.length - 1)) * 100}%`, top: `${(y(values[hover]) / H) * 100}%`, background: stroke }}
          />
        )}
        {events.map((ev) => (
          <button
            key={ev.at}
            type="button"
            aria-label={ev.label}
            onClick={() => setEventOpen(eventOpen === ev ? null : ev)}
            className="absolute top-0 size-3 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-bg transition-transform duration-150 ease-out hover:scale-125"
            style={{ left: `${(ev.at / (values.length - 1)) * 100}%`, background: eventColor[ev.kind] }}
          />
        ))}
        {eventOpen && (
          <div
            role="status"
            className="absolute top-3 z-10 max-w-64 -translate-x-1/2 rounded-input border border-border-strong bg-surface-2 px-2.5 py-1.5 text-12 shadow-overlay"
            style={{ left: `${Math.min(85, Math.max(15, (eventOpen.at / (values.length - 1)) * 100))}%` }}
          >
            <span className="capitalize text-muted">{eventOpen.kind}</span> · {eventOpen.label}
          </div>
        )}
      </div>
      <div className="flex justify-between font-mono text-12 text-dim">
        <span>24h ago</span>
        <span>12h ago</span>
        <span>now</span>
      </div>
    </div>
  );
}

export function ChartLegend() {
  return (
    <div className="flex flex-wrap gap-x-4 gap-y-1 text-12 text-muted">
      {(Object.keys(eventColor) as ChartEvent["kind"][]).map((k) => (
        <span key={k} className="inline-flex items-center gap-1.5 capitalize">
          <span className="size-2 rounded-full" style={{ background: eventColor[k] }} aria-hidden="true" />
          {k}
        </span>
      ))}
    </div>
  );
}
