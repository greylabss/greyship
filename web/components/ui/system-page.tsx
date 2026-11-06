import type { ReactNode } from "react";
import { Logo } from "@/components/brand/logo";

// Calm, one sentence, a way back (design.md §4, System).
export function SystemPage({ code, title, body, actions }: { code: string; title: string; body?: string; actions: ReactNode }) {
  return (
    <div className="flex min-h-dvh flex-col px-5 py-5 md:px-8">
      <Logo href="/" height={24} />
      <main className="flex flex-1 flex-col items-start justify-center gap-4 md:items-center md:text-center">
        <span className="font-mono text-13 text-dim">{code}</span>
        <h1 className="text-28 font-semibold tracking-tight">{title}</h1>
        {body && <p className="max-w-md text-muted">{body}</p>}
        <div className="flex flex-wrap gap-2">{actions}</div>
      </main>
    </div>
  );
}
