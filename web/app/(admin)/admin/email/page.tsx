import { connection } from "next/server";
import { Suspense } from "react";
import { MailSettings } from "@/components/admin/mail-settings";
import type { AdminMail } from "@/lib/api/admin-types";
import { adminJson } from "@/lib/api/live";

export default function AdminEmailPage() {
  return (
    <Suspense fallback={<p className="text-muted">Loading…</p>}>
      <Email />
    </Suspense>
  );
}

async function Email() {
  await connection();
  const status = await adminJson<AdminMail>("/v1/admin/mail");
  return (
    <>
      <h1 className="text-24 font-semibold">Email</h1>
      <p className="max-w-2xl text-13 text-muted">
        Greyship sends deploy notifications by email through <a href="https://resend.com" className="text-fg underline-offset-4 hover:underline">Resend</a>. Add your sending domain in Resend and verify it, create an API key, then paste both here. Signing in never needs email. Without it, notification emails are not sent; the bell and browser notifications still work.
      </p>
      <MailSettings initial={status} />
    </>
  );
}
