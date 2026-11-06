import type { Metadata } from "next";
import Link from "next/link";
import { Suspense } from "react";
import { AuthShell, Divider } from "@/components/auth/auth-shell";
import { EmailCodeForm } from "@/components/auth/email-code-form";
import { SignInError } from "@/components/auth/signin-error";
import { SocialButtons } from "@/components/auth/social-buttons";

export const metadata: Metadata = { title: "Create your account" };

export default function SignUpPage() {
  return (
    <AuthShell
      aside={
        <span className="text-13 text-muted">
          Have an account?{" "}
          <Link href="/signin" className="text-fg hover:underline">
            Sign in
          </Link>
        </span>
      }
    >
      <div className="flex w-full max-w-sm flex-col gap-6">
        <div className="flex flex-col gap-1.5 text-center">
          <h1 className="text-28 font-semibold tracking-tight">Create your account</h1>
          <p className="text-muted">The first account on a new Greyship becomes its owner.</p>
        </div>
        <Suspense>
          <SignInError />
        </Suspense>
        <SocialButtons to="/continue" verb="Sign up" />
        <Divider />
        <Suspense>
          <EmailCodeForm fallbackNext="/continue" withTerms />
        </Suspense>
      </div>
    </AuthShell>
  );
}
