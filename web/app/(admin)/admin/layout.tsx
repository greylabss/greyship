import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { connection } from "next/server";
import { Suspense, type ReactNode } from "react";
import { liveMode } from "@/lib/api";
import { ApiError, adminJson } from "@/lib/api/live";

export const metadata: Metadata = { title: "Admin", robots: { index: false, follow: false } };

// The operator's view (decisions.md 048). The API decides who's allowed; anyone else gets a plain 404.
export default function AdminLayout({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-dvh flex-col bg-bg">
      <header className="flex flex-wrap items-center gap-x-6 gap-y-2 border-b border-border px-4 py-3 md:px-8">
        <Link href="/admin" className="font-semibold">
          Greyship admin
        </Link>
        <nav className="flex gap-4 text-13 text-muted">
          <Link href="/admin" className="hover:text-fg">
            Overview
          </Link>
          <Link href="/admin/deployments" className="hover:text-fg">
            Deployments
          </Link>
          <Link href="/admin/users" className="hover:text-fg">
            Users
          </Link>
          <Link href="/admin/email" className="hover:text-fg">
            Email
          </Link>
        </nav>
        <form action="/admin/deployments" className="ml-auto flex w-full gap-2 sm:w-auto">
          <input
            name="q"
            placeholder="Deployment id, project or team"
            className="h-9 w-full min-w-0 rounded-input border border-border-strong bg-surface px-3 font-mono text-13 sm:w-72"
            aria-label="Find a deployment"
          />
        </form>
      </header>
      <main className="mx-auto flex w-full max-w-[1400px] flex-1 flex-col gap-8 px-4 py-6 md:px-8">
        <Suspense>
          <Gate>{children}</Gate>
        </Suspense>
      </main>
    </div>
  );
}

async function Gate({ children }: { children: ReactNode }) {
  await connection();
  if (!liveMode) notFound();
  try {
    await adminJson("/v1/admin");
  } catch (e) {
    // 401 (signed out) and 404 (not the operator) both look like a page that doesn't exist.
    if (e instanceof ApiError && (e.status === 401 || e.status === 404)) notFound();
    throw e;
  }
  return <>{children}</>;
}
