"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/field";
import { ApiError, call } from "@/lib/greyship-api";

// 20 random characters from an alphabet without look-alikes; the browser's CSPRNG, never Math.random.
export function generatePassword() {
  const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  const bytes = crypto.getRandomValues(new Uint8Array(20));
  return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join("");
}

export function CreateUser() {
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [admin, setAdmin] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit() {
    setError("");
    setBusy(true);
    try {
      await call("POST", "/v1/admin/users", { email: email.trim(), name: name.trim(), password, admin });
      toast.success(`Created ${email.trim()}`);
      setEmail("");
      setName("");
      setPassword("");
      setAdmin(false);
      setOpen(false);
      router.refresh();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Couldn't create the user. Try again.");
    } finally {
      setBusy(false);
    }
  }

  if (!open) {
    return (
      <div>
        <Button onClick={() => setOpen(true)}>Add user</Button>
      </div>
    );
  }
  return (
    <form
      className="flex max-w-xl flex-col gap-4 rounded-panel border border-border p-5"
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <Field label="Email">
          <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="off" placeholder="beta@example.com" required />
        </Field>
        <Field label="Name" hint="Optional">
          <Input value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
        </Field>
      </div>
      <Field label="Password" hint="10 to 128 characters. Give it to them yourself; it isn't shown again.">
        <div className="flex gap-2">
          <Input value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="off" mono required />
          <Button type="button" variant="secondary" onClick={() => setPassword(generatePassword())}>
            Generate
          </Button>
        </div>
      </Field>
      <label className="flex items-start gap-2.5 text-13 text-muted">
        <input type="checkbox" checked={admin} onChange={(e) => setAdmin(e.target.checked)} className="mt-0.5 size-4 accent-[var(--gs-text)]" />
        <span>
          <span className="text-fg">Admin</span>: sees every user and deployment on this instance and manages users.
        </span>
      </label>
      {error && <span className="text-13 text-error">{error}</span>}
      <div className="flex gap-2">
        <Button type="submit" disabled={busy}>
          {busy ? "Creating…" : "Create user"}
        </Button>
        <Button type="button" variant="ghost" onClick={() => setOpen(false)}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
