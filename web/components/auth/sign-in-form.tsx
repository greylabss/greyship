"use client";

import { useRouter, useSearchParams } from "next/navigation";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/field";
import { apiEnabled, ApiError, call } from "@/lib/greyship-api";

// Only same-site paths are allowed as a post-login destination, so ?next= can't send people elsewhere.
function safeNext(raw: string | null, fallback: string) {
  return raw && raw.startsWith("/") && !raw.startsWith("//") ? raw : fallback;
}

// Email and password (decisions.md 053). Mock mode accepts anything.
export function SignInForm({ fallbackNext }: { fallbackNext: string }) {
  const router = useRouter();
  const next = safeNext(useSearchParams().get("next"), fallbackNext);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit() {
    if (!email.trim() || !password) return setError("Enter your email and password");
    setError("");
    setBusy(true);
    try {
      if (apiEnabled) await call("POST", "/v1/auth/password/login", { email: email.trim(), password });
      router.push(next);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Couldn't sign in. Try again.");
      setBusy(false);
    }
  }

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      <Field label="Email">
        <Input name="email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@example.com" autoComplete="username" className="h-11" />
      </Field>
      <Field label="Password" error={error || undefined}>
        <Input name="password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" aria-invalid={Boolean(error)} className="h-11" />
      </Field>
      <Button type="submit" className="h-11" disabled={busy}>
        {busy ? "Signing in…" : "Sign in"}
      </Button>
      <p className="text-center text-12 text-dim">Forgot it? Ask your instance admin, or run greyship-admin password on the server.</p>
    </form>
  );
}

// The first visit to a new instance: whoever opens it creates the owner account, once.
export function SetupForm() {
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit() {
    if (!/^\S+@\S+\.\S+$/.test(email.trim())) return setError("Enter a valid email address");
    setError("");
    setBusy(true);
    try {
      await call("POST", "/v1/auth/setup", { email: email.trim(), name: name.trim(), password });
      router.push("/continue");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Couldn't create the account. Try again.");
      setBusy(false);
    }
  }

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      <Field label="Email">
        <Input name="email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@example.com" autoComplete="username" className="h-11" />
      </Field>
      <Field label="Name" hint="Optional">
        <Input name="name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="name" className="h-11" />
      </Field>
      <Field label="Password" hint="10 to 128 characters." error={error || undefined}>
        <Input name="password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" aria-invalid={Boolean(error)} className="h-11" />
      </Field>
      <Button type="submit" className="h-11" disabled={busy}>
        {busy ? "Creating…" : "Create owner account"}
      </Button>
    </form>
  );
}
