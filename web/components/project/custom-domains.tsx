"use client";

import { useRouter } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { Button, buttonClass } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { Field, Input } from "@/components/ui/field";
import { icons } from "@/components/ui/icons";
import { ApiError, call } from "@/lib/greyship-api";
import { cx } from "@/lib/format";

export type DomainRecord = { type: string; name: string; value: string };
export type DomainRow = { hostname: string; service: string; custom: boolean; dns?: string; records?: DomainRecord[] };

type Badge = { text: string; tone: string; icon: "ok" | "warn" | "idle" };

const badges: Record<string, Badge> = {
  pointing: { text: "Valid configuration", tone: "text-ok", icon: "ok" },
  not_pointing: { text: "Points somewhere else", tone: "text-error", icon: "warn" },
  unresolved: { text: "Verification required", tone: "text-error", icon: "warn" },
  unknown: { text: "Can't verify yet", tone: "text-muted", icon: "idle" },
};

const firstCheckMs = 5_000;
const slowestCheckMs = 15_000;
const giveUpAfterMs = 10 * 60_000;

// A project's addresses (decisions.md 057, 059): the default one for each web service, and the custom domains people
// point at them. A custom domain shows the DNS records to create and is checked again until they answer.
export function CustomDomains({ id, rows: initial }: { id: string; rows: DomainRow[] }) {
  const router = useRouter();
  // The server's list, with the newest answer from checking each domain laid over it. A fresh list from the server
  // (after adding or removing) replaces the answers.
  const [checked, setChecked] = useState<{ source: DomainRow[]; byHost: Record<string, DomainRow> }>({ source: initial, byHost: {} });
  const answers = checked.source === initial ? checked.byHost : {};
  const rows = initial.map((r) => ({ ...r, ...answers[r.hostname] }));

  const services = [...new Set(rows.map((r) => r.service))];
  const [hostname, setHostname] = useState("");
  const [service, setService] = useState(services[0] ?? "");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [checking, setChecking] = useState<string[]>([]);
  const [open, setOpen] = useState<Record<string, boolean>>({});
  const [round, setRound] = useState(0);
  const base = `/v1/projects/${encodeURIComponent(id)}/domains`;
  const latest = useRef<{ dns: Record<string, string | undefined>; verify: (host: string, quiet: boolean) => Promise<void> }>({ dns: {}, verify: async () => {} });

  const verify = useCallback(
    async (host: string, quiet: boolean) => {
      setChecking((c) => [...c, host]);
      try {
        const { domain } = await call<{ domain: DomainRow }>("POST", `${base}/${encodeURIComponent(host)}/verify`);
        if (latest.current.dns[host] !== "pointing" && domain.dns === "pointing") toast.success(`${host} is configured`);
        setChecked((prev) => ({ source: initial, byHost: { ...(prev.source === initial ? prev.byHost : {}), [host]: domain } }));
      } catch (e) {
        if (!quiet) toast.error("Couldn't check it", { description: e instanceof ApiError ? e.message : "Try again." });
      } finally {
        setChecking((c) => c.filter((h) => h !== host));
      }
    },
    [base, initial],
  );

  // Keep checking while a domain isn't pointing here yet: every few seconds, slower as time goes on, paused while the
  // tab is hidden, and given up after ten minutes (Refresh starts it again).
  const waiting = rows.filter((r) => r.custom && r.dns !== "pointing").map((r) => r.hostname);
  const waitingKey = waiting.join(",");
  const dnsNow = Object.fromEntries(rows.map((r) => [r.hostname, r.dns]));
  useEffect(() => {
    latest.current = { dns: dnsNow, verify };
  });
  useEffect(() => {
    if (!waitingKey) return;
    const hosts = waitingKey.split(",");
    let elapsed = 0;
    let delay = firstCheckMs;
    let timer: ReturnType<typeof setTimeout>;
    let stopped = false;
    const tick = async () => {
      if (document.visibilityState === "visible") await Promise.all(hosts.map((h) => latest.current.verify(h, true)));
      elapsed += delay;
      if (stopped || elapsed > giveUpAfterMs) return;
      delay = Math.min(delay * 1.25, slowestCheckMs);
      timer = setTimeout(() => void tick(), delay);
    };
    timer = setTimeout(() => void tick(), delay);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [waitingKey, round]);

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

  function refresh(host: string) {
    setRound((n) => n + 1);
    void verify(host, false);
  }

  function toggle(host: string, isOpen: boolean) {
    setOpen((o) => ({ ...o, [host]: !isOpen }));
  }

  return (
    <div className="flex flex-col gap-8">
      <ul className="flex flex-col divide-y divide-border rounded-panel border border-border">
        {rows.length === 0 && <li className="px-4 py-3 text-13 text-muted">No addresses yet. Deploy a web service first.</li>}
        {rows.map((d) => {
          if (!d.custom) return <DefaultRow key={d.hostname} row={d} />;
          const badge = badges[d.dns ?? "unknown"] ?? badges.unknown;
          const isOpen = open[d.hostname] ?? d.dns !== "pointing";
          const isChecking = checking.includes(d.hostname);
          return (
            <li key={d.hostname} className="flex flex-col gap-4 px-4 py-4">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="flex min-w-0 items-start gap-3">
                  <span className={cx("mt-0.5 shrink-0", badge.tone)} aria-hidden="true">
                    {badge.icon === "ok" ? <icons.check className="size-4" /> : <icons.alert className="size-4" />}
                  </span>
                  <div className="flex min-w-0 flex-col gap-1.5">
                    <span className="break-all font-mono text-14">{d.hostname}</span>
                    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-12">
                      <span className={cx("font-medium", badge.tone)}>{badge.text}</span>
                      <span className="text-muted">{d.service} · custom domain</span>
                      <button type="button" aria-expanded={isOpen} onClick={() => toggle(d.hostname, isOpen)} className="inline-flex items-center gap-1 text-muted hover:text-fg">
                        {isOpen ? "Hide DNS configuration" : "View DNS configuration"}
                        <icons.chevronDown className={cx("size-3.5 transition-transform duration-150", isOpen && "rotate-180")} />
                      </button>
                    </div>
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  <Button size="sm" variant="secondary" disabled={isChecking} onClick={() => refresh(d.hostname)}>
                    {isChecking ? "Checking…" : "Refresh"}
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => void remove(d.hostname)}>
                    Remove
                  </Button>
                </div>
              </div>
              {isOpen && <DnsPanel row={d} connectPath={`${base}/${encodeURIComponent(d.hostname)}/connect`} />}
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
        </form>
      )}
    </div>
  );
}

function DefaultRow({ row }: { row: DomainRow }) {
  return (
    <li className="flex items-start gap-3 px-4 py-4">
      <span className="mt-0.5 shrink-0 text-ok" aria-hidden="true">
        <icons.check className="size-4" />
      </span>
      <div className="flex min-w-0 flex-col gap-1">
        <span className="break-all font-mono text-14">{row.hostname}</span>
        <span className="text-12 text-muted">{row.service} · default address</span>
      </div>
    </li>
  );
}

// The records to create, as Vercel shows them, plus one-click setup when the DNS provider supports it.
function DnsPanel({ row, connectPath }: { row: DomainRow; connectPath: string }) {
  const records = row.records ?? [];
  const pointing = row.dns === "pointing";
  return (
    <div className="flex flex-col gap-4 rounded-input border border-border bg-surface-2 p-4">
      {pointing ? (
        <p className="text-13 text-muted">The DNS record is in place. HTTPS is issued and renewed automatically.</p>
      ) : (
        <p className="text-13 text-muted">
          Create {records.length === 1 ? "this record" : "these records"} at your DNS provider. This page checks again every few seconds and turns green when it sees
          {records.length === 1 ? " it" : " them"}.
        </p>
      )}
      {records.length > 0 && (
        <div className="flex flex-col font-mono text-13">
          <div className="hidden grid-cols-[5rem_9rem_minmax(0,1fr)] gap-x-4 pb-2 font-sans text-12 text-dim md:grid">
            <span>Type</span>
            <span>Name</span>
            <span>Value</span>
          </div>
          {records.map((r) => (
            <div key={`${r.type}${r.name}`} className="grid gap-x-4 gap-y-2 border-t border-border py-3 md:grid-cols-[5rem_9rem_minmax(0,1fr)]">
              <Cell label="Type">{r.type}</Cell>
              <Cell label="Name">
                <span className="inline-flex items-center gap-1">
                  {r.name}
                  <CopyButton value={r.name} label="Copy name" />
                </span>
              </Cell>
              <Cell label="Value">
                {r.value ? (
                  <span className="inline-flex min-w-0 items-center gap-1">
                    <span className="break-all">{r.value}</span>
                    <CopyButton value={r.value} label="Copy value" />
                  </span>
                ) : (
                  <span className="font-sans text-muted">this server&apos;s IP address</span>
                )}
              </Cell>
            </div>
          ))}
        </div>
      )}
      {!pointing && (
        <div className="flex flex-wrap items-center gap-3">
          <CloudflareButton path={connectPath} />
          <span className="text-12 text-dim">On Cloudflare, set the record to &ldquo;DNS only&rdquo; (grey cloud) so HTTPS can be issued.</span>
        </div>
      )}
    </div>
  );
}

// Asks whether the DNS provider can add the records itself (Cloudflare). "Not available" is a normal answer and shows
// nothing; the records above always work.
function CloudflareButton({ path }: { path: string }) {
  const [url, setUrl] = useState<string | null>(null);
  useEffect(() => {
    let live = true;
    call<{ url: string }>("GET", path)
      .then((out) => live && setUrl(out.url))
      .catch(() => live && setUrl(null));
    return () => {
      live = false;
    };
  }, [path]);
  if (!url) return null;
  return (
    <a href={url} target="_blank" rel="noopener noreferrer" className={buttonClass("secondary", "sm")}>
      Configure Cloudflare
      <icons.external className="size-3.5" />
    </a>
  );
}

// One field of a record: labelled on phones, where the three fields stack; the table header labels them from md up.
function Cell({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <span className="font-sans text-12 text-dim md:hidden">{label}</span>
      {children}
    </div>
  );
}
