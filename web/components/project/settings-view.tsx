"use client";

import { useRouter } from "next/navigation";
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
  AlertDialogTrigger,
} from "@/components/primitives/alert-dialog";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/primitives/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/primitives/select";
import { AlwaysOnBadge } from "@/components/ui/status";
import { Button } from "@/components/ui/button";
import { CommandHint } from "@/components/ui/command-hint";
import { Field, Input } from "@/components/ui/field";
import type { Plan, Service } from "@/lib/api/types";
import { ServicePicker, useService } from "./service-picker";

type Form = { memory: string; min: number; max: number; concurrency: number; grace: number; timeout: number; visibility: string };

const limits: Record<Plan, { memory: number; max: number; minAllowed: boolean; timeout: number }> = {
  free: { memory: 128, max: 2, minAllowed: false, timeout: 30 },
  pro: { memory: 1024, max: 10, minAllowed: true, timeout: 300 },
  team: { memory: 2048, max: 50, minAllowed: true, timeout: 300 },
  unlimited: { memory: 2048, max: 50, minAllowed: true, timeout: 300 },
};

const labels: Record<keyof Form, string> = {
  memory: "Memory per instance",
  min: "Min instances",
  max: "Max instances",
  concurrency: "Concurrency per instance",
  grace: "Grace window",
  timeout: "Request timeout",
  visibility: "Visibility",
};

export function SettingsView({ team, project, services, plan }: { team: string; project: string; services: Service[]; plan: Plan }) {
  const name = useService(services.map((s) => s.name));
  const service = services.find((s) => s.name === name)!;
  return <ServiceSettings key={name} team={team} project={project} service={service} services={services.map((s) => s.name)} plan={plan} />;
}

function ServiceSettings({ team, project, service, services, plan }: { team: string; project: string; service: Service; services: string[]; plan: Plan }) {
  const router = useRouter();
  const initial: Form = {
    memory: `${service.memoryMb}`,
    min: service.scaling.minInstances,
    max: service.scaling.maxInstances,
    concurrency: service.scaling.concurrency,
    grace: service.scaling.graceSeconds,
    timeout: service.scaling.timeoutSeconds,
    visibility: service.visibility,
  };
  const [form, setForm] = useState(initial);
  const [saved, setSaved] = useState(initial);
  const [review, setReview] = useState(false);
  const [confirmName, setConfirmName] = useState("");
  const limit = limits[plan];

  const errors: Partial<Record<keyof Form, string>> = {};
  if (Number(form.memory) > limit.memory) errors.memory = `${plan === "free" ? "Free" : "Your"} plan max is ${limit.memory} MB`;
  if (form.min > 0 && !limit.minAllowed) errors.min = "Keeping instances warm needs a paid plan";
  if (form.max > limit.max) errors.max = `Plan max is ${limit.max}`;
  if (form.min > form.max) errors.min = "Can't be more than max instances";
  if (form.timeout > limit.timeout) errors.timeout = `Up to ${limit.timeout}s on this plan`;

  const changes = (Object.keys(form) as (keyof Form)[]).filter((k) => form[k] !== saved[k]);
  const valid = Object.keys(errors).length === 0;
  const alwaysOnCost = form.min > 0 ? `≈ $${(form.min * Number(form.memory) * 0.0045).toFixed(2)}/mo while idle` : null;

  function set<K extends keyof Form>(k: K, v: Form[K]) {
    setForm((f) => ({ ...f, [k]: v }));
  }

  const num = (k: "min" | "max" | "concurrency" | "grace" | "timeout") => (e: React.ChangeEvent<HTMLInputElement>) => set(k, Math.max(0, Number(e.target.value) || 0));

  return (
    <div className="flex max-w-4xl flex-col gap-8">
      <div className="flex flex-wrap items-center gap-3">
        <ServicePicker services={services} />
        <span className="font-mono text-13 text-muted">
          {service.type} · {service.path}
        </span>
      </div>

      <section className="flex flex-col gap-5 rounded-panel border border-border p-5">
        <div className="flex flex-col gap-1">
          <h2 className="font-semibold">Scaling</h2>
          <p className="text-13 text-muted">How {service.name} wakes, sleeps and grows with traffic.</p>
        </div>
        <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 lg:grid-cols-3">
          <Field label={labels.memory} hint={`Up to ${limit.memory} MB on this plan`} error={errors.memory}>
            <Select value={form.memory} onValueChange={(v) => set("memory", v)}>
              <SelectTrigger className="h-10 w-full font-mono text-13">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {["128", "256", "512", "1024", "2048"].map((m) => (
                  <SelectItem key={m} value={m} className="font-mono text-13">
                    {m} MB
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field label={labels.min} hint="0 = scale to zero" error={errors.min}>
            <Input type="number" min={0} mono value={form.min} onChange={num("min")} aria-invalid={Boolean(errors.min)} />
          </Field>
          <Field label={labels.max} hint={`Plan max ${limit.max}`} error={errors.max}>
            <Input type="number" min={1} mono value={form.max} onChange={num("max")} aria-invalid={Boolean(errors.max)} />
          </Field>
          <Field label={labels.concurrency} hint="Requests before adding an instance">
            <Input type="number" min={1} mono value={form.concurrency} onChange={num("concurrency")} />
          </Field>
          <Field label={`${labels.grace} (s)`} hint="Stay awake after the last request so goroutines finish">
            <Input type="number" min={0} mono value={form.grace} onChange={num("grace")} />
          </Field>
          <Field label={`${labels.timeout} (s)`} hint="Up to 300s on paid plans" error={errors.timeout}>
            <Input type="number" min={1} mono value={form.timeout} onChange={num("timeout")} aria-invalid={Boolean(errors.timeout)} />
          </Field>
        </div>
        {form.min > 0 && (
          <div className="flex flex-wrap items-center gap-2 text-13 text-muted">
            <AlwaysOnBadge /> {service.name} will never sleep. {alwaysOnCost}
          </div>
        )}
        <div className="flex flex-wrap items-center justify-between gap-3 border-t border-border pt-4">
          <span className="text-12 text-muted">Also settable in greyship.toml under [services.{service.name}]</span>
          <div className="flex gap-2">
            {changes.length > 0 && <Button onClick={() => setForm(saved)}>Discard</Button>}
            <Button variant="primary" disabled={changes.length === 0 || !valid} onClick={() => setReview(true)}>
              Review {changes.length || ""} {changes.length === 1 ? "change" : "changes"}
            </Button>
          </div>
        </div>
      </section>

      {service.type === "web" && (
        <section className="flex flex-col gap-4 rounded-panel border border-border p-5">
          <div className="flex flex-col gap-1">
            <h2 className="font-semibold">Networking</h2>
            <p className="text-13 text-muted">Private services have no public URL. Other services in this project reach them at {service.name}.internal.</p>
          </div>
          <Field label="Visibility" className="max-w-xs">
            <Select value={form.visibility} onValueChange={(v) => set("visibility", v)}>
              <SelectTrigger className="h-10 w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="public">Public · {service.name}-{project}.greyship.app</SelectItem>
                <SelectItem value="private">Private · {service.name}.internal</SelectItem>
              </SelectContent>
            </Select>
          </Field>
        </section>
      )}

      <section className="flex flex-col gap-3 rounded-panel border border-border p-5">
        <h2 className="font-semibold">Build</h2>
        <dl className="flex flex-col divide-y divide-border text-13">
          {[
            ["Main package", service.path],
            ["Go version", "go1.27 (from go.mod)"],
            ["Build command", "CGO_ENABLED=0 go build -trimpath -ldflags='-s -w'"],
            ["Binary size", service.binarySize],
          ].map(([k, v]) => (
            <div key={k} className="flex flex-wrap justify-between gap-2 py-2">
              <dt className="text-muted">{k}</dt>
              <dd className="font-mono">{v}</dd>
            </div>
          ))}
        </dl>
      </section>

      <section className="flex flex-wrap items-center justify-between gap-4 rounded-panel border border-error/40 p-5">
        <div className="flex flex-col gap-1">
          <h2 className="font-semibold">Delete service</h2>
          <p className="text-13 text-muted">Removes {service.name}, its deployments, logs and domains. This can&apos;t be undone.</p>
        </div>
        <AlertDialog onOpenChange={() => setConfirmName("")}>
          <AlertDialogTrigger asChild>
            <Button variant="destructive">Delete {service.name}</Button>
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Delete {service.name}?</AlertDialogTitle>
              <AlertDialogDescription>
                Type <span className="font-mono text-fg">{service.name}</span> to confirm. If {service.path} still exists in the repo, the next deploy creates it again.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <Input value={confirmName} onChange={(e) => setConfirmName(e.target.value)} mono autoFocus aria-label={`Type ${service.name} to confirm`} />
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction
                variant="destructive"
                disabled={confirmName !== service.name}
                onClick={() => {
                  toast(`Deleted ${service.name}`);
                  router.push(`/${team}/${project}`);
                }}
              >
                Delete {service.name}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </section>

      <Dialog open={review} onOpenChange={setReview}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>Review changes to {service.name}</DialogTitle>
            <DialogDescription>Applied with a rolling restart. No rebuild.</DialogDescription>
          </DialogHeader>
          <ul className="divide-y divide-border rounded-panel border border-border text-13">
            {changes.map((k) => (
              <li key={k} className="flex items-center justify-between gap-3 px-3 py-2.5">
                <span>{labels[k]}</span>
                <span className="font-mono">
                  <span className="text-dim line-through">{String(saved[k])}</span> → <span className="text-fg">{String(form[k])}</span>
                </span>
              </li>
            ))}
          </ul>
          <p className="text-13 text-muted">{alwaysOnCost ? `Estimated cost: ${alwaysOnCost}.` : "Estimated cost unchanged: still $0 while idle."}</p>
          <CommandHint command={`grey deploy  # or edit [services.${service.name}] in greyship.toml`} />
          <DialogFooter>
            <Button onClick={() => setReview(false)}>Back</Button>
            <Button
              variant="primary"
              onClick={() => {
                setSaved(form);
                setReview(false);
                toast.success(`Updated ${service.name}`, { description: `${changes.length} ${changes.length === 1 ? "setting" : "settings"} applied · rolling restart started` });
              }}
            >
              Apply {changes.length} {changes.length === 1 ? "change" : "changes"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
