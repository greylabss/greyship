"use client";

import { useState } from "react";
import { toast } from "sonner";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/primitives/alert-dialog";
import { DropdownMenuItem, DropdownMenuLabel, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuSeparator } from "@/components/primitives/dropdown-menu";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CommandHint } from "@/components/ui/command-hint";
import { CopyButton } from "@/components/ui/copy-button";
import { Input } from "@/components/ui/field";
import { Status } from "@/components/ui/status";
import type { Domain } from "@/lib/api/types";
import { RowMenu } from "./actions";

const label = { active: "HTTPS active", dns: "Waiting for DNS", verifying: "Verifying · issuing certificate" };
const tone = { active: "ok", dns: "progress", verifying: "progress" } as const;

export function DomainsView({ initial, services }: { initial: Domain[]; services: string[] }) {
  const [domains, setDomains] = useState(initial);
  const [error, setError] = useState("");
  const [remove, setRemove] = useState<Domain | null>(null);

  function update(host: string, patch: Partial<Domain>) {
    setDomains((ds) => ds.map((d) => (d.hostname === host ? { ...d, ...patch } : d)));
  }

  function check(d: Domain) {
    update(d.hostname, { status: "verifying" });
    setTimeout(() => {
      update(d.hostname, { status: "active" });
      toast.success(`${d.hostname} is live`, { description: "Certificate issued · renews automatically" });
    }, 2200);
  }

  return (
    <div className="flex flex-col gap-6">
      <form
        className="flex flex-wrap items-start gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          const form = e.currentTarget;
          const host = String(new FormData(form).get("host") ?? "").trim().toLowerCase();
          if (!/^([a-z0-9-]+\.)+[a-z]{2,}$/.test(host)) return setError("Enter a domain like api.example.com");
          if (domains.some((d) => d.hostname === host)) return setError(`${host} is already added`);
          const apex = host.split(".").length === 2;
          setError("");
          setDomains((ds) => [
            ...ds,
            {
              hostname: host,
              kind: "custom",
              status: "dns",
              target: services[0],
              record: apex ? { type: "A", name: "@", value: "203.0.113.24" } : { type: "CNAME", name: host.split(".")[0], value: "edge.greyship.app" },
            },
          ]);
          toast(`Added ${host}`, { description: "Add the DNS record below to finish" });
          form.reset();
        }}
      >
        <label className="flex min-w-64 flex-1 flex-col gap-1.5">
          <span className="sr-only">Domain</span>
          <Input name="host" placeholder="api.example.com" mono aria-invalid={Boolean(error)} />
          {error && <span className="text-12 text-error">{error}</span>}
        </label>
        <Button type="submit" variant="primary">
          Add domain
        </Button>
      </form>

      <ul className="divide-y divide-border rounded-panel border border-border">
        {domains.map((d) => (
          <li key={d.hostname} className="flex flex-col gap-4 px-4 py-4">
            <div className="flex flex-wrap items-center gap-3">
              <span className="flex min-w-0 flex-1 items-center gap-1">
                <a href={`https://${d.hostname}`} className="truncate font-mono text-14 hover:underline">
                  {d.hostname}
                </a>
                <CopyButton value={`https://${d.hostname}`} label="Copy URL" />
              </span>
              <Badge>{d.kind === "default" ? "Default" : "Custom"}</Badge>
              <span className="text-13 text-muted">
                → <span className="font-mono">{d.target}</span>
              </span>
              <Status tone={tone[d.status]} label={label[d.status]} className="text-13" />
              {d.kind === "custom" && (
                <RowMenu label={`Actions for ${d.hostname}`}>
                  <DropdownMenuLabel className="text-12 font-normal text-muted">Route to service</DropdownMenuLabel>
                  <DropdownMenuRadioGroup
                    value={d.target}
                    onValueChange={(v) => {
                      update(d.hostname, { target: v });
                      toast(`${d.hostname} now routes to ${v}`);
                    }}
                  >
                    {services.map((s) => (
                      <DropdownMenuRadioItem key={s} value={s} className="font-mono text-13">
                        {s}
                      </DropdownMenuRadioItem>
                    ))}
                  </DropdownMenuRadioGroup>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem variant="destructive" onSelect={() => setRemove(d)}>
                    Remove domain
                  </DropdownMenuItem>
                </RowMenu>
              )}
            </div>
            {d.status !== "active" && d.record && (
              <div className="flex flex-col gap-3 rounded-panel border border-border bg-surface-2 p-4">
                <div className="flex flex-col gap-1">
                  <span className="font-medium">Finish setting up {d.hostname}</span>
                  <span className="text-13 text-muted">Add this record at your DNS provider. We check every minute and issue the certificate as soon as it&apos;s live.</span>
                </div>
                <div className="overflow-x-auto rounded-input border border-border">
                  <table className="w-full min-w-[480px] text-13">
                    <thead>
                      <tr className="bg-surface text-left text-12 text-muted">
                        <th className="px-3 py-2 font-medium">Type</th>
                        <th className="px-3 py-2 font-medium">Name</th>
                        <th className="px-3 py-2 font-medium">Value</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr className="border-t border-border font-mono">
                        <td className="px-3 py-2">{d.record.type}</td>
                        <td className="px-3 py-2">
                          <span className="inline-flex items-center gap-1">
                            {d.record.name}
                            <CopyButton value={d.record.name} label="Copy name" />
                          </span>
                        </td>
                        <td className="px-3 py-2">
                          <span className="inline-flex items-center gap-1">
                            {d.record.value}
                            <CopyButton value={d.record.value} label="Copy value" />
                          </span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>
                <div className="flex flex-wrap items-center gap-3">
                  <Button size="sm" disabled={d.status === "verifying"} onClick={() => check(d)}>
                    {d.status === "verifying" ? "Checking DNS…" : "Check DNS now"}
                  </Button>
                  <span className="text-12 text-muted">Last checked 40s ago</span>
                </div>
              </div>
            )}
          </li>
        ))}
      </ul>
      <CommandHint command="grey domains add api.example.com" />

      <AlertDialog open={remove !== null} onOpenChange={(o) => !o && setRemove(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove {remove?.hostname}?</AlertDialogTitle>
            <AlertDialogDescription>Requests to this domain stop reaching your app right away. The certificate is revoked.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                if (!remove) return;
                setDomains((ds) => ds.filter((d) => d.hostname !== remove.hostname));
                toast(`Removed ${remove.hostname}`);
              }}
            >
              Remove domain
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
