import type { Metadata } from "next";
import { connection } from "next/server";
import { Suspense } from "react";
import { AuthShell } from "@/components/auth/auth-shell";
import { SetupForm, SignInForm } from "@/components/auth/sign-in-form";
import { CommandHint } from "@/components/ui/command-hint";
import { api } from "@/lib/api";

export const metadata: Metadata = { title: "Sign in" };

export default function SignInPage() {
  return (
    <AuthShell aside={null}>
      <Suspense>
        <Card />
      </Suspense>
    </AuthShell>
  );
}

// A new instance has no accounts: its first visitor creates the owner. After that this is a plain sign-in.
async function Card() {
  await connection();
  const setup = await api.setupNeeded().catch(() => false);
  return (
    <div className="flex w-full max-w-sm flex-col gap-6">
      <div className="flex flex-col gap-1.5 text-center">
        <h1 className="text-28 font-semibold tracking-tight">{setup ? "Create the owner account" : "Sign in to Greyship"}</h1>
        <p className="text-muted">{setup ? "This is a new Greyship. The first account becomes its owner and adds everyone else." : "Deploy and manage your Go apps."}</p>
      </div>
      <Suspense>{setup ? <SetupForm /> : <SignInForm fallbackNext="/continue" />}</Suspense>
      {!setup && (
        <div className="flex flex-col items-center gap-1 text-13 text-muted">
          Signing in from the terminal?
          <CommandHint command="grey login" />
        </div>
      )}
    </div>
  );
}
