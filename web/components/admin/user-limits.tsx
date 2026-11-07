"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/field";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/primitives/dialog";
import { ApiError, call } from "@/lib/greyship-api";

type Limits = { memoryMb: number | null; cpuMillis: number | null; builds: number | null };

// A user's limits: what their running apps may use together. Empty means no limit.
export function UserLimits({ id, email, limits }: { id: string; email: string; limits: Limits }) {
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [memory, setMemory] = useState(limits.memoryMb?.toString() ?? "");
  const [cpu, setCpu] = useState(limits.cpuMillis ? String(limits.cpuMillis / 1000) : "");
  const [builds, setBuilds] = useState(limits.builds?.toString() ?? "");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const number = (v: string) => (v.trim() === "" ? null : Number(v));

  async function save() {
    const m = number(memory);
    const c = number(cpu);
    const b = number(builds);
    if ([m, c, b].some((v) => v !== null && (!Number.isFinite(v) || v <= 0))) return setError("Use positive numbers, or leave a field empty for no limit.");
    if ((m !== null && !Number.isInteger(m)) || (b !== null && !Number.isInteger(b))) return setError("Memory and builds are whole numbers.");
    setError("");
    setBusy(true);
    try {
      await call("PATCH", `/v1/admin/users/${encodeURIComponent(id)}`, { limits: { memory_mb: m, cpu_cores: c, concurrent_builds: b } });
      toast.success(`Limits saved for ${email}`);
      setOpen(false);
      router.refresh();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Couldn't save. Try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm" variant="ghost">
          Limits
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Limits for {email}</DialogTitle>
          <DialogDescription>What all of this user&apos;s running apps may use together. Leave a field empty for no limit.</DialogDescription>
        </DialogHeader>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <Field label="Memory (MB)" hint="For example 1024. A deploy briefly runs the old and the new version together, so leave some room.">
            <Input inputMode="numeric" value={memory} onChange={(e) => setMemory(e.target.value)} placeholder="No limit" mono />
          </Field>
          <Field label="CPU (cores)" hint="For example 2, or 0.5.">
            <Input inputMode="decimal" value={cpu} onChange={(e) => setCpu(e.target.value)} placeholder="No limit" mono />
          </Field>
          <Field label="Builds at once">
            <Input inputMode="numeric" value={builds} onChange={(e) => setBuilds(e.target.value)} placeholder="No limit" mono />
          </Field>
          {error && <span className="text-13 text-error">{error}</span>}
          <DialogFooter>
            <Button type="submit" disabled={busy}>
              {busy ? "Saving…" : "Save limits"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
