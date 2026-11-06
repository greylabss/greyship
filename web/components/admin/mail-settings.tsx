"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/field";
import type { AdminMail } from "@/lib/api/admin-types";
import { ApiError, call } from "@/lib/greyship-api";

export function MailSettings({ initial }: { initial: AdminMail }) {
  const router = useRouter();
  const [enabled, setEnabled] = useState(initial.enabled);
  const [domain, setDomain] = useState(initial.sending_domain);
  const [name, setName] = useState(initial.from_name);
  const [apiKey, setApiKey] = useState("");
  const [keySet, setKeySet] = useState(initial.key_set);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState<"" | "save" | "test">("");

  async function save() {
    setError("");
    setBusy("save");
    try {
      const saved = await call<AdminMail>("PUT", "/v1/admin/mail", { enabled, sending_domain: domain.trim(), from_name: name.trim(), api_key: apiKey.trim() });
      setKeySet(saved.key_set);
      setApiKey("");
      toast.success("Email settings saved");
      router.refresh();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Couldn't save. Try again.");
    } finally {
      setBusy("");
    }
  }

  async function sendTest() {
    setBusy("test");
    try {
      await call("POST", "/v1/admin/mail/test");
      toast.success("Test email sent to you");
    } catch (e) {
      toast.error("The test email failed", { description: e instanceof ApiError ? e.message : "Try again." });
    } finally {
      setBusy("");
    }
  }

  const status =
    initial.source === "dashboard" ? "On, using the settings below." : initial.source === "environment" ? "On, using GREYSHIP_RESEND_API_KEY from the server. Saving here replaces it." : "Off.";

  return (
    <form
      className="flex max-w-xl flex-col gap-4 rounded-panel border border-border p-5"
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      <p className="text-13 text-muted">{status}</p>
      <label className="flex items-start gap-2.5 text-13 text-muted">
        <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} className="mt-0.5 size-4 accent-[var(--gs-text)]" />
        <span>
          <span className="text-fg">Send email</span>: login codes and deploy notifications.
        </span>
      </label>
      <Field label="Resend API key" hint={keySet ? "A key is saved. Leave empty to keep it, or paste a new one." : "Starts with re_. Stored encrypted; never shown again."}>
        <Input type="password" value={apiKey} onChange={(e) => setApiKey(e.target.value)} autoComplete="off" placeholder={keySet ? "Saved" : "re_…"} mono />
      </Field>
      <Field label="Sending domain" hint="Verified in Resend. Mail is sent from login@ and notifications@ this domain.">
        <Input value={domain} onChange={(e) => setDomain(e.target.value)} autoComplete="off" placeholder="mail.example.com" mono />
      </Field>
      <Field label="Sender name">
        <Input value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" placeholder="Greyship" />
      </Field>
      {error && <span className="text-13 text-error">{error}</span>}
      <div className="flex flex-wrap gap-2">
        <Button type="submit" disabled={busy !== ""}>
          {busy === "save" ? "Saving…" : "Save"}
        </Button>
        <Button type="button" variant="secondary" disabled={busy !== "" || !initial.active} onClick={() => void sendTest()}>
          {busy === "test" ? "Sending…" : "Send test email"}
        </Button>
      </div>
    </form>
  );
}
