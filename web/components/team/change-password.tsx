"use client";

import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/field";
import { ApiError, call } from "@/lib/greyship-api";

export function ChangePassword({ hasPassword }: { hasPassword: boolean }) {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit() {
    setError("");
    setBusy(true);
    try {
      await call("POST", "/v1/me/password", { current, new: next });
      toast.success("Password saved");
      setCurrent("");
      setNext("");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Couldn't save it. Try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form
      className="flex max-w-sm flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      {hasPassword && (
        <Field label="Current password">
          <Input type="password" value={current} onChange={(e) => setCurrent(e.target.value)} autoComplete="current-password" />
        </Field>
      )}
      <Field label="New password" hint="10 to 128 characters." error={error || undefined}>
        <Input type="password" value={next} onChange={(e) => setNext(e.target.value)} autoComplete="new-password" />
      </Field>
      <div>
        <Button type="submit" disabled={busy || !next}>
          {busy ? "Saving…" : hasPassword ? "Change password" : "Set password"}
        </Button>
      </div>
    </form>
  );
}
