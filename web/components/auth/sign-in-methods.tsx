"use client";

import { useRouter, useSearchParams } from "next/navigation";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/field";
import { apiEnabled, ApiError, call } from "@/lib/greyship-api";
import { EmailCodeForm } from "./email-code-form";

function safeNext(raw: string | null, fallback: string) {
  return raw && raw.startsWith("/") && !raw.startsWith("//") ? raw : fallback;
}

// Email code by default; accounts an admin created sign in with a password (decisions.md 051).
export function SignInMethods({ fallbackNext }: { fallbackNext: string }) {
  const [method, setMethod] = useState<"code" | "password">("code");
  return (
    <div className="flex flex-col gap-4">
      {method === "code" ? <EmailCodeForm fallbackNext={fallbackNext} /> : <PasswordForm fallbackNext={fallbackNext} />}
      {apiEnabled && (
        <button type="button" onClick={() => setMethod(method === "code" ? "password" : "code")} className="text-13 text-muted underline-offset-4 hover:text-fg hover:underline">
          {method === "code" ? "Use a password instead" : "Use an email code instead"}
        </button>
      )}
    </div>
  );
}

function PasswordForm({ fallbackNext }: { fallbackNext: string }) {
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
      await call("POST", "/v1/auth/password/login", { email: email.trim(), password });
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
    </form>
  );
}
