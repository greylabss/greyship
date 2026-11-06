"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/primitives/select";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/field";
import { icons } from "@/components/ui/icons";

type Detected = { name: string; path: string; type: "web" | "worker" | "cron" };

const typeNote = { web: "URL + scale to zero", worker: "Sleeping · wakes on queue", cron: "Runs on schedule" };

export function ReviewForm({ team, repo, detected }: { team: string; repo: string; detected: Detected[] }) {
  const router = useRouter();
  const [name, setName] = useState(repo);
  const [services, setServices] = useState(detected);
  const [env, setEnv] = useState<{ k: string; v: string }[]>([{ k: "DATABASE_URL", v: "" }]);
  const [deploying, setDeploying] = useState(false);
  const nameError = /^[a-z][a-z0-9-]{1,38}$/.test(name) ? "" : "Lowercase letters, digits and dashes, 2–39 characters";

  function deploy() {
    if (nameError) return;
    setDeploying(true);
    toast.success(`Created ${name}`, { description: "First build queued" });
    setTimeout(() => router.push(`/${team}/${name}/deployments/live`), 600);
  }

  return (
    <div className="grid grid-cols-1 items-start gap-6 lg:grid-cols-[minmax(0,1fr)_320px]">
      <div className="flex flex-col gap-6">
        <section className="grid grid-cols-1 gap-4 rounded-panel border border-border p-5 sm:grid-cols-2">
          <Field label="Project name" error={nameError} hint={`URLs look like api-${name}.greyship.app`}>
            <Input value={name} onChange={(e) => setName(e.target.value.toLowerCase())} mono aria-invalid={Boolean(nameError)} />
          </Field>
          <Field label="Region">
            <Select defaultValue="eu">
              <SelectTrigger className="h-10 w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="eu">Europe · Germany</SelectItem>
                <SelectItem value="lagos" disabled>
                  Lagos · coming soon
                </SelectItem>
              </SelectContent>
            </Select>
          </Field>
        </section>

        <section className="flex flex-col overflow-hidden rounded-panel border border-border">
          <div className="flex flex-wrap items-baseline justify-between gap-2 border-b border-border px-5 py-3">
            <h2 className="font-semibold">Services</h2>
            <span className="text-12 text-muted">Detected from main packages · override in greyship.toml</span>
          </div>
          <ul className="divide-y divide-border">
            {services.map((s, i) => (
              <li key={s.path} className="flex flex-wrap items-center gap-3 px-5 py-3">
                <span className="w-36 truncate font-medium">{s.name}</span>
                <span className="flex-1 truncate font-mono text-13 text-muted">{s.path}</span>
                <span className="hidden text-13 text-muted md:inline">{typeNote[s.type]}</span>
                <Select value={s.type} onValueChange={(v) => setServices((ss) => ss.map((x, j) => (j === i ? { ...x, type: v as Detected["type"] } : x)))}>
                  <SelectTrigger aria-label={`Type of ${s.name}`} className="h-8 w-28 font-mono text-12">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {(["web", "worker", "cron"] as const).map((t) => (
                      <SelectItem key={t} value={t} className="font-mono text-13">
                        {t}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </li>
            ))}
          </ul>
        </section>

        <section className="flex flex-col gap-3 rounded-panel border border-border p-5">
          <div className="flex flex-col gap-1">
            <h2 className="font-semibold">Environment variables</h2>
            <p className="text-13 text-muted">Shared by every service. Add per-service values after the first deploy.</p>
          </div>
          {env.map((row, i) => (
            <div key={i} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_auto] gap-2">
              <Input aria-label="Key" mono placeholder="KEY" value={row.k} onChange={(e) => setEnv((es) => es.map((x, j) => (j === i ? { ...x, k: e.target.value.toUpperCase() } : x)))} />
              <Input aria-label="Value" mono type="password" placeholder="value" value={row.v} onChange={(e) => setEnv((es) => es.map((x, j) => (j === i ? { ...x, v: e.target.value } : x)))} />
              <Button variant="ghost" aria-label={`Remove ${row.k || "row"}`} onClick={() => setEnv((es) => es.filter((_, j) => j !== i))}>
                <icons.close />
              </Button>
            </div>
          ))}
          <Button size="sm" className="self-start" onClick={() => setEnv((es) => [...es, { k: "", v: "" }])}>
            <icons.plus />
            Add variable
          </Button>
        </section>
      </div>

      <aside className="flex flex-col gap-4 rounded-panel border border-border p-5 lg:sticky lg:top-20">
        <h2 className="font-semibold">Summary</h2>
        <dl className="flex flex-col gap-2.5 text-13">
          {[
            ["Plan", "Free"],
            ["Services", String(services.length)],
            ["Memory per instance", "128 MB"],
            ["Scale to zero", "On"],
            ["Variables", String(env.filter((e) => e.k).length)],
            ["Spend cap", "$0 (free tier)"],
          ].map(([k, v]) => (
            <div key={k} className="flex justify-between gap-3">
              <dt className="text-muted">{k}</dt>
              <dd>{v}</dd>
            </div>
          ))}
        </dl>
        <div className="flex flex-wrap gap-1.5">
          {services.map((s) => (
            <Badge key={s.name} mono>
              {s.name}:{s.type}
            </Badge>
          ))}
        </div>
        <Button variant="primary" disabled={Boolean(nameError) || deploying} onClick={deploy}>
          {deploying ? "Creating project…" : `Deploy ${name}`}
        </Button>
        <span className="text-12 text-muted">Every push to main redeploys automatically. Pull requests get preview URLs on Pro.</span>
      </aside>
    </div>
  );
}
