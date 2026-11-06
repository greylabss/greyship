"use client";

import { useSearchParams } from "next/navigation";

// Why a Google or GitHub sign-in came back refused (the API redirects with ?error=<reason>).
const messages: Record<string, string> = {
  not_invited: "This email isn't invited to this Greyship yet. Ask its owner to add it.",
  invite_check: "We couldn't check the invite list just now. Try again in a minute.",
  unverified_email: "That account's email isn't verified with the provider. Verify it there, or sign in with an email code.",
  state: "That sign-in took too long or came from another tab. Try again.",
  denied: "Signing in was cancelled.",
  exchange: "Google or GitHub didn't finish signing you in. Try again.",
  profile: "We couldn't read your email from Google or GitHub. Try again, or use an email code.",
};

export function SignInError() {
  const reason = useSearchParams().get("error");
  if (!reason) return null;
  return (
    <p role="alert" className="rounded-input border border-error/50 bg-surface-2 px-3 py-2 text-center text-13 text-error">
      {messages[reason] ?? "Signing in didn't work. Try again, or use an email code."}
    </p>
  );
}
