import Link from "next/link";
import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export function when(iso: string | null | undefined) {
  if (!iso) return "—";
  return new Date(iso).toLocaleString("en-GB", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", second: "2-digit", timeZone: "UTC" }) + " UTC";
}

export function took(from: string, to: string | null) {
  if (!to) return "…";
  const s = Math.round((new Date(to).getTime() - new Date(from).getTime()) / 1000);
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
}

const tones: Record<string, string> = {
  ready: "text-ok",
  running: "text-ok",
  done: "text-ok",
  failed: "text-error",
  exited: "text-error",
  superseded: "text-muted",
  stopped: "text-muted",
};

export function State({ value }: { value: string }) {
  return <span className={cn("font-mono text-12", tones[value] ?? "text-warn")}>{value}</span>;
}

export function Section({ title, aside, children }: { title: string; aside?: ReactNode; children: ReactNode }) {
  return (
    <section className="flex min-w-0 flex-col gap-2">
      <div className="flex items-baseline justify-between gap-3">
        <h2 className="font-semibold">{title}</h2>
        {aside && <span className="text-12 text-muted">{aside}</span>}
      </div>
      {children}
    </section>
  );
}

export function Grid({ rows }: { rows: [string, ReactNode][] }) {
  return (
    <dl className="grid grid-cols-1 gap-x-6 gap-y-2 rounded-panel border border-border p-4 text-13 sm:grid-cols-[160px_minmax(0,1fr)]">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-muted">{k}</dt>
          <dd className="min-w-0 break-all font-mono">{v ?? "—"}</dd>
        </div>
      ))}
    </dl>
  );
}

export function Table({ head, children }: { head: string[]; children: ReactNode }) {
  return (
    <div className="overflow-x-auto rounded-panel border border-border">
      <table className="w-full min-w-[640px] border-collapse text-13">
        <thead>
          <tr>
            {head.map((h) => (
              <th key={h} className="bg-surface px-3 py-2 text-left text-12 font-medium text-muted">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  );
}

export function Td({ children, mono, className }: { children: ReactNode; mono?: boolean; className?: string }) {
  return <td className={cn("border-t border-border px-3 py-2 align-top", mono && "font-mono text-12", className)}>{children}</td>;
}

export function DeploymentLink({ id }: { id: string }) {
  return (
    <Link href={`/admin/deployments/${id}`} className="font-mono text-12 underline-offset-2 hover:underline">
      {id.slice(0, 8)}
    </Link>
  );
}

export function Log({ lines, empty }: { lines: { key: string | number; t: string; text: string; dim?: boolean; tag?: string }[]; empty: string }) {
  if (lines.length === 0) return <p className="rounded-panel border border-dashed border-border-strong px-4 py-6 text-13 text-muted">{empty}</p>;
  return (
    <div className="max-h-[560px] overflow-auto rounded-panel border border-border bg-surface-2 py-2 font-mono text-12 leading-5">
      {lines.map((l) => (
        <div key={l.key} className="flex gap-3 px-3 hover:bg-surface">
          <span className="w-16 shrink-0 select-none text-dim">{l.t}</span>
          {l.tag && <span className="w-20 shrink-0 truncate text-muted">{l.tag}</span>}
          <span className={cn("whitespace-pre-wrap break-all", l.dim ? "text-muted" : "text-fg")}>{l.text}</span>
        </div>
      ))}
    </div>
  );
}
