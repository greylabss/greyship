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
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/primitives/dialog";
import { DropdownMenuItem, DropdownMenuSeparator } from "@/components/primitives/dropdown-menu";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/primitives/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/primitives/tabs";
import { ToggleGroup, ToggleGroupItem } from "@/components/primitives/toggle-group";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CommandHint } from "@/components/ui/command-hint";
import { Input, inputClass } from "@/components/ui/field";
import { icons } from "@/components/ui/icons";
import type { EnvVar } from "@/lib/api/types";
import { cn } from "@/lib/utils";
import { RowMenu } from "./actions";

type Exclude = { service: string; key: string };
const reserved = /^(PORT|GREYSHIP_.*)$/;

export function EnvView({ services, initial, initialExcludes }: { services: string[]; initial: EnvVar[]; initialExcludes: Exclude[] }) {
  const [vars, setVars] = useState(initial);
  const [excludes, setExcludes] = useState(initialExcludes);
  const [env, setEnv] = useState("production");
  const [revealed, setRevealed] = useState(false);
  const [askReveal, setAskReveal] = useState(false);
  const [edit, setEdit] = useState<{ key: string; service: string | null; value: string; mode: "edit" | "override" } | null>(null);
  const [paste, setPaste] = useState(false);
  const [remove, setRemove] = useState<EnvVar | null>(null);

  const shared = vars.filter((v) => v.service === null);

  function affected(key: string, service: string | null) {
    if (service) return [service];
    return services.filter((s) => !vars.some((v) => v.service === s && v.key === key) && !excludes.some((e) => e.service === s && e.key === key));
  }

  function saved(key: string, service: string | null, verb = "Saved") {
    const list = affected(key, service);
    toast.success(`${verb} ${key}`, { description: list.length ? `Restarting ${list.join(", ")} · no rebuild` : "No running service uses it yet" });
  }

  function upsert(key: string, service: string | null, value: string) {
    setVars((vs) => {
      const i = vs.findIndex((v) => v.key === key && v.service === service);
      const next = { key, service, value, updated: "just now" };
      return i === -1 ? [...vs, next] : vs.map((v, j) => (j === i ? next : v));
    });
  }

  function reveal() {
    if (revealed) return setRevealed(false);
    setAskReveal(true);
  }

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <ToggleGroup type="single" value={env} onValueChange={(v) => v && setEnv(v)} aria-label="Environment" className="rounded-input border border-border-strong p-0.5">
          <ToggleGroupItem value="production" className="h-8 px-3 text-13">
            Production
          </ToggleGroupItem>
          <ToggleGroupItem value="preview" className="h-8 px-3 text-13">
            Preview
          </ToggleGroupItem>
        </ToggleGroup>
        <div className="flex gap-2">
          <Button size="sm" variant="ghost" onClick={reveal}>
            {revealed ? "Hide values" : "Reveal values"}
          </Button>
          <Button size="sm" onClick={() => setPaste(true)}>
            Paste a .env file
          </Button>
        </div>
      </div>

      <AddForm services={services} onAdd={(k, s, v) => (upsert(k, s, v), saved(k, s))} />

      <Tabs defaultValue="shared" className="gap-4">
        <TabsList variant="line" className="w-full justify-start overflow-x-auto border-b border-border">
          <TabsTrigger value="shared">Shared</TabsTrigger>
          {services.map((s) => (
            <TabsTrigger key={s} value={s} className="font-mono text-13">
              {s}
            </TabsTrigger>
          ))}
        </TabsList>

        <TabsContent value="shared" className="flex flex-col gap-3">
          <p className="text-13 text-muted">Every service gets these, unless it overrides or excludes a key.</p>
          <EnvTable>
            {shared.map((v) => {
              const overriddenBy = services.filter((s) => vars.some((x) => x.service === s && x.key === v.key));
              const excludedBy = excludes.filter((e) => e.key === v.key).map((e) => e.service);
              return (
                <EnvRow key={v.key} name={v.key} value={v.value} revealed={revealed} updated={v.updated}>
                  <span className="flex flex-wrap gap-1">
                    <Badge>all services</Badge>
                    {overriddenBy.map((s) => (
                      <Badge key={s} mono>
                        {s} overrides
                      </Badge>
                    ))}
                    {excludedBy.map((s) => (
                      <Badge key={s} mono>
                        {s} excluded
                      </Badge>
                    ))}
                  </span>
                  <RowMenu label={`Actions for ${v.key}`}>
                    <DropdownMenuItem onSelect={() => setEdit({ key: v.key, service: null, value: v.value, mode: "edit" })}>Edit value</DropdownMenuItem>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem variant="destructive" onSelect={() => setRemove(v)}>
                      Delete
                    </DropdownMenuItem>
                  </RowMenu>
                </EnvRow>
              );
            })}
          </EnvTable>
          <CommandHint command="grey env set KEY=value" />
        </TabsContent>

        {services.map((s) => {
          const own = vars.filter((v) => v.service === s);
          const ownKeys = new Set(own.map((v) => v.key));
          const platform: [string, string][] = [
            ["PORT", "8080"],
            ["GREYSHIP_SERVICE", s],
            ["GOMAXPROCS", "1"],
            ["GOMEMLIMIT", "115MiB"],
          ];
          return (
            <TabsContent key={s} value={s} className="flex flex-col gap-3">
              <p className="text-13 text-muted">
                What <span className="font-mono text-fg">{s}</span> receives at start. It never sees another service&apos;s own variables.
              </p>
              <EnvTable>
                {platform.map(([k, val]) => (
                  <EnvRow key={k} name={k} value={val} revealed updated="platform" muted>
                    <Badge>platform</Badge>
                    <span className="inline-flex size-8 items-center justify-center text-dim" title="Set by Greyship">
                      <icons.env />
                    </span>
                  </EnvRow>
                ))}
                {shared.map((v) => {
                  const excluded = excludes.some((e) => e.service === s && e.key === v.key);
                  if (ownKeys.has(v.key)) return null;
                  return (
                    <EnvRow key={`shared-${v.key}`} name={v.key} value={v.value} revealed={revealed} updated={v.updated} struck={excluded}>
                      <Badge>{excluded ? "excluded" : "shared"}</Badge>
                      <RowMenu label={`Actions for ${v.key}`}>
                        {excluded ? (
                          <DropdownMenuItem
                            onSelect={() => {
                              setExcludes((es) => es.filter((e) => !(e.service === s && e.key === v.key)));
                              saved(v.key, s, "Included");
                            }}
                          >
                            Include again
                          </DropdownMenuItem>
                        ) : (
                          <>
                            <DropdownMenuItem onSelect={() => setEdit({ key: v.key, service: s, value: "", mode: "override" })}>Override for this service</DropdownMenuItem>
                            <DropdownMenuItem
                              onSelect={() => {
                                setExcludes((es) => [...es, { service: s, key: v.key }]);
                                saved(v.key, s, "Excluded");
                              }}
                            >
                              Exclude from this service
                            </DropdownMenuItem>
                          </>
                        )}
                      </RowMenu>
                    </EnvRow>
                  );
                })}
                {own.map((v) => (
                  <EnvRow key={`own-${v.key}`} name={v.key} value={v.value} revealed={revealed} updated={v.updated}>
                    <Badge tone={shared.some((x) => x.key === v.key) ? "progress" : "neutral"}>{shared.some((x) => x.key === v.key) ? "override" : "service"}</Badge>
                    <RowMenu label={`Actions for ${v.key}`}>
                      <DropdownMenuItem onSelect={() => setEdit({ key: v.key, service: s, value: v.value, mode: "edit" })}>Edit value</DropdownMenuItem>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem variant="destructive" onSelect={() => setRemove(v)}>
                        {shared.some((x) => x.key === v.key) ? "Remove override" : "Delete"}
                      </DropdownMenuItem>
                    </RowMenu>
                  </EnvRow>
                ))}
              </EnvTable>
              <CommandHint command={`grey env ls --service ${s}`} />
            </TabsContent>
          );
        })}
      </Tabs>

      {env === "preview" && <p className="text-13 text-muted">Preview deployments use these values unless a preview-only value is set.</p>}

      <AlertDialog open={askReveal} onOpenChange={setAskReveal}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Confirm it&apos;s you</AlertDialogTitle>
            <AlertDialogDescription>Secret values are encrypted at rest. Re-authenticate with GitHub to show them for 10 minutes.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                setRevealed(true);
                toast("Values visible for 10 minutes");
              }}
            >
              <icons.github />
              Continue with GitHub
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <Dialog open={edit !== null} onOpenChange={(o) => !o && setEdit(null)}>
        <DialogContent className="sm:max-w-md">
          {edit && (
            <form
              className="flex flex-col gap-4"
              onSubmit={(e) => {
                e.preventDefault();
                const value = String(new FormData(e.currentTarget).get("value") ?? "");
                if (!value) return;
                upsert(edit.key, edit.service, value);
                saved(edit.key, edit.service, edit.mode === "override" ? "Overrode" : "Saved");
                setEdit(null);
              }}
            >
              <DialogHeader>
                <DialogTitle>
                  {edit.mode === "override" ? "Override" : "Edit"} <span className="font-mono">{edit.key}</span>
                </DialogTitle>
                <DialogDescription>
                  {edit.mode === "override" ? `Only ${edit.service} gets this value. Other services keep the shared one.` : edit.service ? `Used only by ${edit.service}.` : "Used by every service that doesn't override or exclude it."}
                </DialogDescription>
              </DialogHeader>
              <Input name="value" defaultValue={edit.value} mono autoFocus placeholder="New value" />
              <p className="text-12 text-muted">Saving restarts {affected(edit.key, edit.service).join(", ") || "nothing"}. No rebuild.</p>
              <DialogFooter>
                <Button onClick={() => setEdit(null)}>Cancel</Button>
                <Button type="submit" variant="primary">
                  Save {edit.key}
                </Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>

      <PasteDialog
        open={paste}
        setOpen={setPaste}
        services={services}
        onImport={(pairs, service) => {
          pairs.forEach(([k, v]) => upsert(k, service, v));
          toast.success(`Imported ${pairs.length} variables`, { description: service ? `Into ${service}` : "As shared variables" });
        }}
      />

      <AlertDialog open={remove !== null} onOpenChange={(o) => !o && setRemove(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete {remove?.key}?</AlertDialogTitle>
            <AlertDialogDescription>
              {remove && `Restarts ${affected(remove.key, remove.service).join(", ") || "nothing"}. Services that read it will see it unset.`}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                if (!remove) return;
                setVars((vs) => vs.filter((v) => !(v.key === remove.key && v.service === remove.service)));
                saved(remove.key, remove.service, "Deleted");
              }}
            >
              Delete {remove?.key}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function AddForm({ services, onAdd }: { services: string[]; onAdd: (key: string, service: string | null, value: string) => void }) {
  const [scope, setScope] = useState("shared");
  const [error, setError] = useState("");
  return (
    <form
      className="grid grid-cols-1 gap-3 rounded-panel border border-border p-4 md:grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)_180px_auto] md:items-end"
      onSubmit={(e) => {
        e.preventDefault();
        const form = e.currentTarget;
        const data = new FormData(form);
        const key = String(data.get("key") ?? "").trim().toUpperCase();
        const value = String(data.get("value") ?? "");
        if (!/^[A-Z_][A-Z0-9_]*$/.test(key)) return setError("Use letters, digits and underscores, e.g. STRIPE_SECRET_KEY");
        if (reserved.test(key)) return setError(`${key} is set by Greyship and can't be changed`);
        if (!value) return setError("Add a value");
        setError("");
        onAdd(key, scope === "shared" ? null : scope, value);
        form.reset();
      }}
    >
      <label className="flex flex-col gap-1.5">
        <span className="text-13 font-medium">Key</span>
        <Input name="key" mono placeholder="STRIPE_SECRET_KEY" aria-invalid={Boolean(error)} />
      </label>
      <label className="flex flex-col gap-1.5">
        <span className="text-13 font-medium">Value</span>
        <Input name="value" type="password" mono placeholder="sk_live_…" />
      </label>
      <label className="flex flex-col gap-1.5">
        <span className="text-13 font-medium">Applies to</span>
        <Select value={scope} onValueChange={setScope}>
          <SelectTrigger className="h-10 w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="shared">All services (shared)</SelectItem>
            {services.map((s) => (
              <SelectItem key={s} value={s} className="font-mono text-13">
                {s} only
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </label>
      <Button type="submit" variant="primary">
        <icons.plus />
        Add variable
      </Button>
      {error && <p className="text-12 text-error md:col-span-4">{error}</p>}
    </form>
  );
}

function PasteDialog({ open, setOpen, services, onImport }: { open: boolean; setOpen: (o: boolean) => void; services: string[]; onImport: (pairs: [string, string][], service: string | null) => void }) {
  const [text, setText] = useState("");
  const [scope, setScope] = useState("shared");
  const pairs = text
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l && !l.startsWith("#") && l.includes("="))
    .map((l) => {
      const i = l.indexOf("=");
      return [l.slice(0, i).trim(), l.slice(i + 1).trim().replace(/^["']|["']$/g, "")] as [string, string];
    })
    .filter(([k]) => /^[A-Z_][A-Z0-9_]*$/i.test(k) && !reserved.test(k));

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Paste a .env file</DialogTitle>
          <DialogDescription>One KEY=value per line. Comments and PORT / GREYSHIP_* are skipped.</DialogDescription>
        </DialogHeader>
        <textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          rows={8}
          placeholder={"DATABASE_URL=postgres://…\nSTRIPE_SECRET_KEY=sk_live_…"}
          className={cn(inputClass, "h-auto py-2 font-mono text-13")}
        />
        <div className="flex flex-wrap items-center justify-between gap-3">
          <Select value={scope} onValueChange={setScope}>
            <SelectTrigger className="h-9 min-w-48 text-13">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="shared">All services (shared)</SelectItem>
              {services.map((s) => (
                <SelectItem key={s} value={s}>
                  {s} only
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <span className="text-13 text-muted tabular">{pairs.length} variables found</span>
        </div>
        <DialogFooter>
          <Button onClick={() => setOpen(false)}>Cancel</Button>
          <Button
            variant="primary"
            disabled={pairs.length === 0}
            onClick={() => {
              onImport(pairs, scope === "shared" ? null : scope);
              setText("");
              setOpen(false);
            }}
          >
            Import {pairs.length || ""} variables
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function EnvTable({ children }: { children: React.ReactNode }) {
  return (
    <div className="relative overflow-x-auto rounded-panel border border-border">
      <div className="min-w-[680px] divide-y divide-border">
        <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)_minmax(0,1fr)_96px_40px] gap-4 bg-surface px-4 py-2.5 text-12 font-medium text-muted">
          <span>Key</span>
          <span>Value</span>
          <span>Source</span>
          <span>Updated</span>
          <span className="sr-only">Actions</span>
        </div>
        {children}
      </div>
    </div>
  );
}

function EnvRow({
  name,
  value,
  revealed,
  updated,
  muted,
  struck,
  children,
}: {
  name: string;
  value: string;
  revealed: boolean;
  updated: string;
  muted?: boolean;
  struck?: boolean;
  children: [React.ReactNode, React.ReactNode];
}) {
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)_minmax(0,1fr)_96px_40px] items-center gap-4 px-4 py-2.5">
      <span className={cn("truncate font-mono text-13", muted && "text-muted", struck && "text-dim line-through")}>{name}</span>
      <span className={cn("truncate font-mono text-13", revealed ? "text-fg" : "text-dim", struck && "line-through")}>{revealed ? value : "••••••••••••"}</span>
      <span className="min-w-0">{children[0]}</span>
      <span className="text-13 text-muted">{updated}</span>
      {children[1]}
    </div>
  );
}
