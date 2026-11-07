"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/field";
import { ApiError, call } from "@/lib/greyship-api";

export type DomainRow = { hostname: string; service: string; custom: boolean; dns?: string };

const dnsLabel: Record<string, { text: string; tone: string }> = {
  pointing: { text: "DNS points here", tone: "text-muted" },
  not_pointing: { text: "DNS points somewhere else", tone: "text-error" },
  unresolved: { text: "No DNS record yet", tone: "text-error" },
  unknown: { text: "DNS not checked", tone: "text-muted" },
};

// A project's addresses (decisions.md 057): the default one for each web service, and the custom domains people point
// at them. HTTPS for a custom domain is issued the first time it is visited, once its DNS record answers.
export function CustomDomains({ id, rows }: { id: string; rows: DomainRow[] }) {
  const router = useRouter();
  const services = [...new Set(rows.map((r) => r.service))];
  const [hostname, setHostname] = useState("");
  const [service, setService] = useState(services[0] ?? "");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const base = `/v1/projects/${encodeURIComponent(id)}/domains`;
  const defaultAddr = rows.find((r) => !r.custom && r.service === service)?.hostname;

  async function add() {
    setError("");
    setBusy(true);
    try {
      await call("POST", base, { hostname: hostname.trim(), service });
      toast.success(`Added ${hostname.trim().toLowerCase()}`);
      setHostname("");
      router.refresh();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Couldn't add it. Try again.");
    } finally {
      setBusy(false);
    }
  }

  async function remove(host: string) {
    try {
      await call("DELETE", `${base}/${encodeURIComponent(host)}`);
      toast.success(`Removed ${host}`);
      router.refresh();
    } catch (e) {
      toast.error("Couldn't remove it", { description: e instanceof ApiError ? e.message : "Try again." });
    }
  }

  return (
    <div className="flex flex-col gap-8">
      <ul className="flex flex-col divide-y divide-border rounded-panel border border-border">
        {rows.length === 0 && <li className="px-4 py-3 text-13 text-muted">No addresses yet. Deploy a web service first.</li>}
        {rows.map((d) => {
          const state = d.custom ? dnsLabel[d.dns ?? "unknown"] : undefined;
          return (
            <li key={d.hostname} className="flex flex-wrap items-center justify-between gap-3 px-4 py-3">
              <div className="flex min-w-0 flex-col gap-0.5">
                <span className="truncate font-mono text-13">{d.hostname}</span>
                <span className="text-12 text-muted">
                  {d.service} · {d.custom ? "custom domain" : "default address"}
                </span>
              </div>
              <div className="flex items-center gap-3">
                {state && <span className={`text-12 ${state.tone}`}>{state.text}</span>}
                {d.custom && (
                  <Button size="sm" variant="ghost" onClick={() => void remove(d.hostname)}>
                    Remove
                  </Button>
                )}
              </div>
            </li>
          );
        })}
      </ul>

      {services.length > 0 && (
        <form
          className="flex max-w-xl flex-col gap-4 rounded-panel border border-border p-5"
          onSubmit={(e) => {
            e.preventDefault();
            void add();
          }}
        >
          <h2 className="font-semibold">Add a domain</h2>
          <Field label="Domain" hint="For example api.example.com">
            <Input value={hostname} onChange={(e) => setHostname(e.target.value)} autoComplete="off" placeholder="api.example.com" mono />
          </Field>
          {services.length > 1 && (
            <Field label="Service">
              <select value={service} onChange={(e) => setService(e.target.value)} className="h-10 rounded-input border border-border-strong bg-surface px-3 text-14">
                {services.map((s) => (
                  <option key={s}>{s}</option>
                ))}
              </select>
            </Field>
          )}
          {error && <span className="text-13 text-error">{error}</span>}
          <div>
            <Button type="submit" disabled={busy || !hostname.trim()}>
              {busy ? "Adding…" : "Add domain"}
            </Button>
          </div>
          <p className="text-12 text-dim">
            Then, at your DNS provider, add a <span className="font-mono">CNAME</span> record from your domain to{" "}
            <span className="break-all font-mono text-fg">{defaultAddr ?? "the service's default address"}</span>. For a name with no subdomain (example.com) use an{" "}
            <span className="font-mono">A</span> record to this server&apos;s IP address instead. HTTPS turns on by itself the first time the domain is visited.
          </p>
        </form>
      )}
    </div>
  );
}
