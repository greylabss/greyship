import type { Metadata } from "next";
import Link from "next/link";
import { AuthShell, Divider } from "@/components/auth/auth-shell";
import { SignInError } from "@/components/auth/signin-error";
import { SocialButtons } from "@/components/auth/social-buttons";
import { Suspense } from "react";
import { EmailCodeForm } from "@/components/auth/email-code-form";
import { CommandHint } from "@/components/ui/command-hint";

export const metadata: Metadata = { title: "Sign in" };

export default function SignInPage() {
  return (
    <AuthShell
      aside={
        <span className="text-13 text-muted">
          New here?{" "}
          <Link href="/signup" className="text-fg hover:underline">
            Create an account
          </Link>
        </span>
      }
    >
      <div className="flex w-full max-w-sm flex-col gap-6">
        <div className="flex flex-col gap-1.5 text-center">
          <h1 className="text-28 font-semibold tracking-tight">Sign in to Greyship</h1>
          <p className="text-muted">Deploy and manage your Go apps.</p>
        </div>
        <Suspense>
          <SignInError />
        </Suspense>
        <SocialButtons to="/continue" verb="Continue" />
        <Divider />
        <Suspense>
          <EmailCodeForm fallbackNext="/continue" />
        </Suspense>
        <div className="flex flex-col items-center gap-1 text-13 text-muted">
          Signing in from the terminal?
          <CommandHint command="grey login" />
        </div>
      </div>
    </AuthShell>
  );
}
