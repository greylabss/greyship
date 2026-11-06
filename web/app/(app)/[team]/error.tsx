"use client";

import Link from "next/link";
import { Button, buttonClass } from "@/components/ui/button";

export default function TeamError({ error, retry }: { error: unknown; retry: () => void }) {
  const digest = error instanceof Error && "digest" in error ? String(error.digest) : null;
  return (
    <div className="flex flex-1 flex-col items-start justify-center gap-4 py-20 md:items-center md:text-center">
      <span className="font-mono text-13 text-dim">error{digest ? ` · ${digest}` : ""}</span>
      <h1 className="text-28 font-semibold tracking-tight">This page failed to load.</h1>
      <p className="max-w-md text-muted">Your apps are unaffected. Try again in a moment.</p>
      <div className="flex gap-2">
        <Button variant="primary" onClick={retry}>
          Try again
        </Button>
        <Link href="/continue" className={buttonClass("secondary")}>
          Back to projects
        </Link>
      </div>
    </div>
  );
}
