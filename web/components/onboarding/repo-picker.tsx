"use client";

import Link from "next/link";
import { useState } from "react";
import { toast } from "sonner";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/primitives/select";
import { Badge } from "@/components/ui/badge";
import { buttonClass } from "@/components/ui/button";
import { inputClass } from "@/components/ui/field";
import { icons } from "@/components/ui/icons";
import { cn } from "@/lib/utils";

type Repo = { name: string; updated: string; mains: number; imported: boolean; language?: string };

export function RepoPicker({ team, repos }: { team: string; repos: Repo[] }) {
  const [q, setQ] = useState("");
  const shown = repos.filter((r) => r.name.includes(q.trim().toLowerCase()));

  return (
    <section aria-label="Repositories" className="flex flex-col overflow-hidden rounded-panel border border-border">
      <div className="flex flex-wrap gap-2 border-b border-border p-3">
        <Select defaultValue="haki">
          <SelectTrigger aria-label="GitHub account" className="h-10 min-w-40 gap-2">
            <icons.github />
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="haki">haki</SelectItem>
            <SelectItem value="acme-labs">acme-labs</SelectItem>
          </SelectContent>
        </Select>
        <label className="relative min-w-48 flex-1">
          <span className="sr-only">Search repositories</span>
          <icons.search className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-dim" />
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search repositories…" className={cn(inputClass, "pl-9")} autoFocus />
        </label>
      </div>
      <ul className="divide-y divide-border">
        {shown.map((r) => {
          const go = r.mains > 0;
          return (
            <li key={r.name} className="flex items-center gap-3 px-4 py-3">
              <icons.github className="text-dim" />
              <div className="flex min-w-0 flex-1 flex-col">
                <span className="truncate font-medium">haki/{r.name}</span>
                <span className="truncate text-12 text-muted">
                  {r.updated}
                  {r.mains > 1 && ` · ${r.mains} mains in cmd/`}
                  {!go && " · no Go main found"}
                </span>
              </div>
              {r.language && <Badge>{r.language}</Badge>}
              {r.imported ? (
                <Link href={`/${team}/${r.name}`} className={buttonClass("ghost", "sm")}>
                  Imported
                </Link>
              ) : go ? (
                <Link href={`/${team}/new/${r.name}`} className={buttonClass("secondary", "sm")}>
                  Import
                </Link>
              ) : (
                <button type="button" className={buttonClass("ghost", "sm")} onClick={() => toast("Greyship runs Go only", { description: `haki/${r.name} has no package main` })}>
                  Not Go
                </button>
              )}
            </li>
          );
        })}
        {shown.length === 0 && <li className="px-4 py-8 text-center text-muted">No repositories match &ldquo;{q}&rdquo;.</li>}
      </ul>
      <div className="border-t border-border px-4 py-3 text-13 text-muted">
        Missing a repo?{" "}
        <a href="https://github.com/settings/installations" className="text-fg underline-offset-4 hover:underline">
          Adjust GitHub app permissions
        </a>
      </div>
    </section>
  );
}
