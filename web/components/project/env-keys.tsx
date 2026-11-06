"use client";

import { useRouter } from "next/navigation";
import { useMemo, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Table, Td, Th } from "@/components/ui/table";
import type { EnvVar } from "@/lib/api/types";
import { parseDotenv } from "@/lib/dotenv";
import { ApiError, call } from "@/lib/greyship-api";

const field = "w-full rounded-input border border-border-strong bg-surface px-3 py-2 font-mono text-13 outline-none placeholder:text-dim focus:border-muted";

type Change = { added: string[]; updated: string[]; removed: string[] };

// The project's env vars as stored: keys only, values never sent back to the browser. Saving restarts the services
// that are running, with no rebuild (F-40).
// suggested are names the repo's .env.example lists; clicking one starts adding it.
export function EnvKeys({ projectId, vars, suggested = [] }: { projectId: string; vars: EnvVar[]; suggested?: string[] }) {
  const router = useRouter();
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [pasted, setPasted] = useState("");
  const [busy, setBusy] = useState(false);
  const parsed = useMemo(() => parseDotenv(pasted), [pasted]);

  async function apply(set: Record<string, string>, unset: string[]) {
    setBusy(true);
    try {
      const c = await call<Change>("PATCH", `/v1/projects/${projectId}/env`, { set, unset });
      const parts = [c.added.length && `${c.added.length} added`, c.updated.length && `${c.updated.length} updated`, c.removed.length && `${c.removed.length} removed`].filter(Boolean);
      toast.success(parts.length ? parts.join(", ") : "Nothing changed", { description: "Running services restart with the new values; no rebuild" });
      router.refresh();
      return true;
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Saving failed. Try again.");
      return false;
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-8">
      <section className="flex flex-col gap-3">
        {vars.length === 0 ? (
          <p className="rounded-panel border border-dashed border-border-strong px-4 py-8 text-center text-muted">No variables yet. Add one below or paste your .env.</p>
        ) : (
          <Table minWidth={0}>
            <thead>
              <tr>
                <Th>Key</Th>
                <Th className="max-sm:hidden">Updated</Th>
                <Th>
                  <span className="sr-only">Actions</span>
                </Th>
              </tr>
            </thead>
            <tbody>
              {vars.map((v) => (
                <tr key={v.key}>
                  <Td mono>{v.key}</Td>
                  <Td className="text-muted max-sm:hidden">{v.updated}</Td>
                  <Td align="right">
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={busy}
                      onClick={() => {
                        if (window.confirm(`Remove ${v.key}? Running services restart without it.`)) void apply({}, [v.key]);
                      }}
                    >
                      Remove
                    </Button>
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
        <p className="text-13 text-muted">Values are stored encrypted and never shown again.</p>
      </section>

      <form
        className="flex flex-col gap-3 rounded-panel border border-border p-4"
        onSubmit={async (e) => {
          e.preventDefault();
          if (await apply({ [key.trim()]: value }, [])) {
            setKey("");
            setValue("");
          }
        }}
      >
        <h2 className="font-semibold">Add or change a variable</h2>
        {suggested.filter((k) => !vars.some((v) => v.key === k)).length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5 text-13">
            <span className="text-muted">From .env.example:</span>
            {suggested
              .filter((k) => !vars.some((v) => v.key === k))
              .map((k) => (
                <button key={k} type="button" onClick={() => setKey(k)} className="rounded-control border border-border-strong px-2 py-0.5 font-mono text-12 hover:border-muted">
                  {k}
                </button>
              ))}
          </div>
        )}
        <div className="grid gap-3 md:grid-cols-[minmax(0,240px)_minmax(0,1fr)]">
          <input value={key} onChange={(e) => setKey(e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, "_"))} placeholder="DATABASE_URL" className={field} aria-label="Key" required />
          <textarea value={value} onChange={(e) => setValue(e.target.value)} placeholder="value" rows={1} className={field} aria-label="Value" />
        </div>
        <div>
          <Button type="submit" variant="primary" disabled={busy || !/^[A-Za-z_][A-Za-z0-9_]*$/.test(key.trim())}>
            Save
          </Button>
        </div>
      </form>

      <section className="flex flex-col gap-3 rounded-panel border border-border p-4">
        <h2 className="font-semibold">Paste a .env file</h2>
        <textarea
          value={pasted}
          onChange={(e) => setPasted(e.target.value)}
          placeholder={"DATABASE_URL=postgres://…\nSTRIPE_KEY=sk_live_…\n# comments and export are fine"}
          rows={6}
          className={field}
          aria-label="Paste .env contents"
        />
        {parsed.errors.length > 0 && (
          <ul className="flex flex-col gap-0.5 text-13 text-error">
            {parsed.errors.map((e) => (
              <li key={e}>{e}</li>
            ))}
          </ul>
        )}
        {parsed.pairs.length > 0 && (
          <p className="font-mono text-12 text-muted">
            {parsed.pairs.length} found: {parsed.pairs.map(([k]) => k).join(", ")}
          </p>
        )}
        <div>
          <Button
            variant="primary"
            disabled={busy || parsed.pairs.length === 0 || parsed.errors.length > 0}
            onClick={async () => {
              if (await apply(Object.fromEntries(parsed.pairs), [])) setPasted("");
            }}
          >
            Save {parsed.pairs.length || ""} variables
          </Button>
        </div>
      </section>

    </div>
  );
}
