"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { Button, buttonClass } from "@/components/ui/button";
import { icons } from "@/components/ui/icons";
import { ApiError, call } from "@/lib/greyship-api";
import type { GitHubRepo } from "@/lib/api/live";
import { cn } from "@/lib/utils";

// Real repositories from the team's GitHub installations. Importing creates the project, connects the default
// branch and opens the first deploy's live log; every later push to that branch deploys.
export function GitHubImport({ team, repos, installURL }: { team: string; repos: GitHubRepo[]; installURL: string }) {
  const router = useRouter();
  const [q, setQ] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const shown = repos.filter((r) => r.full_name.toLowerCase().includes(q.trim().toLowerCase()));

  async function importRepo(repo: GitHubRepo) {
    setBusy(repo.full_name);
    setError(null);
    try {
      const res = await call<{ project: { slug: string } }>("POST", `/v1/teams/${encodeURIComponent(team)}/github/import`, { repo: repo.full_name, deploy: false });
      router.push(`/${team}/${res.project.slug}/setup`);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Import failed. Try again.");
      setBusy(null);
    }
  }

  return (
    <section aria-label="Repositories" className="flex flex-col overflow-hidden rounded-panel border border-border">
      <div className="flex flex-wrap items-center gap-2 border-b border-border p-3">
        <label className="relative min-w-48 flex-1">
          <span className="sr-only">Search repositories</span>
          <icons.search className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-dim" />
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Search repositories…"
            className="h-10 w-full rounded-input border border-border-strong bg-surface pl-9 pr-3 text-14 outline-none placeholder:text-dim focus:border-muted"
          />
        </label>
        <a href={installURL} className={cn(buttonClass("secondary"), "h-10")}>
          <icons.github />
          Add repositories
        </a>
      </div>
      {error && <p className="border-b border-border px-4 py-2 text-13 text-error">{error}</p>}
      {shown.length === 0 ? (
        <p className="px-4 py-8 text-center text-muted">{repos.length === 0 ? "The app can't read any repositories yet. Add some on GitHub." : "No repository matches."}</p>
      ) : (
        <ul className="divide-y divide-border">
          {shown.map((r) => (
            <li key={r.full_name} className="flex items-center gap-3 px-4 py-3">
              <icons.github className="shrink-0 text-dim" />
              <div className="flex min-w-0 flex-1 flex-col">
                <span className="truncate font-medium">{r.full_name}</span>
                <span className="truncate font-mono text-12 text-dim">
                  {r.default_branch} · {r.private ? "private" : "public"}
                </span>
              </div>
              <Button size="sm" variant="primary" disabled={busy !== null} onClick={() => importRepo(r)}>
                {busy === r.full_name ? "Importing…" : "Import"}
              </Button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

// Shown until the team installs the Greyship GitHub app.
export function InstallGitHub({ installURL }: { installURL: string }) {
  return (
    <div className="flex flex-col items-start gap-4 rounded-panel border border-dashed border-border-strong px-6 py-10 md:items-center md:text-center">
      <div className="flex max-w-md flex-col gap-1">
        <h2 className="text-16 font-semibold">Connect GitHub</h2>
        <p className="text-muted">Install the Greyship app on your GitHub account and choose the repositories it may read. Every push to a connected branch then deploys.</p>
      </div>
      <a href={installURL} className={buttonClass("primary")}>
        <icons.github />
        Install the GitHub app
      </a>
    </div>
  );
}

// Paste a public repository's link and deploy it, without installing the app. Later commits deploy only when someone
// clicks Deploy latest (decisions.md 043).
export function PublicRepoForm({ team }: { team: string }) {
  const router = useRouter();
  const [link, setLink] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await call<{ project: { slug: string } }>("POST", `/v1/teams/${encodeURIComponent(team)}/github/import-url`, { url: link.trim(), deploy: false });
      router.push(`/${team}/${res.project.slug}/setup`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Couldn't deploy that repository. Try again.");
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-3 rounded-panel border border-border p-4">
      <div className="flex flex-col gap-1">
        <h2 className="font-semibold">Import a public repository</h2>
        <p className="text-13 text-muted">Paste its GitHub link. New commits deploy when you click Deploy latest.</p>
      </div>
      <div className="flex flex-col gap-2 sm:flex-row">
        <input
          value={link}
          onChange={(e) => setLink(e.target.value)}
          placeholder="https://github.com/owner/repo"
          aria-label="Public GitHub repository link"
          className="h-10 min-w-0 flex-1 rounded-input border border-border-strong bg-surface px-3 font-mono text-13 outline-none placeholder:text-dim focus:border-muted"
          required
        />
        <Button type="submit" variant="primary" disabled={busy || link.trim() === ""}>
          {busy ? "Importing…" : "Import"}
        </Button>
      </div>
      {error && <p className="text-13 text-error">{error}</p>}
    </form>
  );
}

// Deploys the head of the project's connected branch and opens its log.
export function DeployLatestButton({ team, project, projectId, label = "Deploy latest" }: { team: string; project: string; projectId: string; label?: string }) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  return (
    <span className="flex flex-col items-end gap-1">
      <Button
        variant="primary"
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          setNote(null);
          try {
            const res = await call<{ deployment_id: string }>("POST", `/v1/projects/${projectId}/github/deploy`);
            router.push(`/${team}/${project}/deployments/${res.deployment_id}`);
          } catch (err) {
            setNote(err instanceof ApiError ? err.message : "Couldn't start a deploy. Try again.");
            setBusy(false);
          }
        }}
      >
        <icons.deployments />
        {busy ? "Starting…" : label}
      </Button>
      {note && <span className="max-w-64 text-right text-12 text-muted">{note}</span>}
    </span>
  );
}
