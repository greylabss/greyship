"use client";

import { useRouter, useSearchParams } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/field";
import { apiEnabled, ApiError, call } from "@/lib/greyship-api";

// Only same-site paths are allowed as a post-login destination, so ?next= can't send people elsewhere.
function safeNext(raw: string | null, fallback: string) {
  return raw && raw.startsWith("/") && !raw.startsWith("//") ? raw : fallback;
}

// Passwordless sign-in (decisions.md 023): email → 6-digit code. Mock mode accepts any 6 digits.
export function EmailCodeForm({ fallbackNext, withTerms = false }: { fallbackNext: string; withTerms?: boolean }) {
  const router = useRouter();
  const next = safeNext(useSearchParams().get("next"), fallbackNext);
  const [step, setStep] = useState<"email" | "code">("email");
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [agreed, setAgreed] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [cooldown, setCooldown] = useState(0);
  const codeRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (cooldown <= 0) return;
    const id = setTimeout(() => setCooldown((c) => c - 1), 1000);
    return () => clearTimeout(id);
  }, [cooldown]);

  async function sendCode() {
    if (!/^\S+@\S+\.\S+$/.test(email.trim())) return setError("Enter a valid email address");
    if (withTerms && !agreed) return setError("Accept the Terms and Privacy Policy to continue");
    setError("");
    setBusy(true);
    try {
      if (apiEnabled) await call("POST", "/v1/auth/email/start", { email: email.trim() });
      setStep("code");
      setCode("");
      setCooldown(60);
      setTimeout(() => codeRef.current?.focus(), 0);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Couldn't send the code. Try again.");
    } finally {
      setBusy(false);
    }
  }

  async function verify(value: string) {
    if (!/^\d{6}$/.test(value)) return setError("Enter the 6-digit code from the email");
    setError("");
    setBusy(true);
    try {
      if (apiEnabled) await call("POST", "/v1/auth/email/verify", { email: email.trim(), code: value });
      router.push(next);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Couldn't check the code. Try again.");
      setBusy(false);
    }
  }

  if (step === "code") {
    return (
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          void verify(code);
        }}
      >
        <div className="flex flex-col gap-1">
          <span className="font-medium">Check your email</span>
          <span className="text-13 text-muted">
            We sent a 6-digit code to <span className="text-fg">{email.trim()}</span>. It expires in 10 minutes.
          </span>
        </div>
        <Field label="Code" error={error || undefined}>
          <Input
            ref={codeRef}
            value={code}
            onChange={(e) => {
              const v = e.target.value.replace(/\D/g, "").slice(0, 6);
              setCode(v);
              setError("");
              if (v.length === 6) void verify(v);
            }}
            inputMode="numeric"
            autoComplete="one-time-code"
            placeholder="000000"
            aria-invalid={Boolean(error)}
            className="h-12 text-center font-mono text-20 tracking-[0.4em] md:text-20"
          />
        </Field>
        <Button type="submit" variant="primary" className="h-11" disabled={busy}>
          {busy ? "Checking…" : "Sign in"}
        </Button>
        <div className="flex flex-wrap justify-between gap-2 text-13">
          <button type="button" onClick={() => setStep("email")} className="text-muted underline-offset-4 hover:text-fg hover:underline">
            Use a different email
          </button>
          <button
            type="button"
            disabled={cooldown > 0 || busy}
            onClick={() => void sendCode()}
            className="text-muted underline-offset-4 hover:text-fg hover:underline disabled:no-underline disabled:opacity-60"
          >
            {cooldown > 0 ? `Resend in ${cooldown}s` : "Resend code"}
          </button>
        </div>
        {!apiEnabled && <p className="text-12 text-dim">Mock mode: any 6 digits work.</p>}
      </form>
    );
  }

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        void sendCode();
      }}
    >
      <Field label="Email" error={error && !error.startsWith("Accept") ? error : undefined}>
        <Input name="email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@example.com" autoComplete="email" className="h-11" />
      </Field>
      {withTerms && (
        <>
          <label className="flex items-start gap-2.5 text-13 text-muted">
            <input type="checkbox" checked={agreed} onChange={(e) => setAgreed(e.target.checked)} className="mt-0.5 size-4 accent-[var(--gs-text)]" />
            <span>
              I agree to the{" "}
              <a href="#" className="text-fg underline-offset-4 hover:underline">
                Terms
              </a>{" "}
              and{" "}
              <a href="#" className="text-fg underline-offset-4 hover:underline">
                Privacy Policy
              </a>
            </span>
          </label>
          {error.startsWith("Accept") && <span className="text-12 text-error">{error}</span>}
        </>
      )}
      <Button type="submit" className="h-11" disabled={busy}>
        {busy ? "Sending code…" : "Email me a sign-in code"}
      </Button>
    </form>
  );
}
