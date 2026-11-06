"use client";

import { catchError, type ErrorInfo } from "next/error";
import { Button } from "./button";
import { icons } from "./icons";

// What happened and how to fix it, scoped to the part of the page that failed (design.md §7.6).
export function ErrorMessage({ what, message, onRetry }: { what: string; message: string; onRetry?: () => void }) {
  return (
    <div role="alert" className="flex flex-wrap items-start gap-3 rounded-panel border border-border-strong px-4 py-4">
      <icons.alert className="mt-0.5 text-error" />
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <span className="font-medium">Couldn&apos;t load {what}</span>
        <span className="font-mono text-13 text-muted">{message}</span>
        <span className="text-13 text-muted">Running apps aren&apos;t affected. Try again in a moment.</span>
      </div>
      {onRetry && (
        <Button size="sm" onClick={onRetry}>
          Try again
        </Button>
      )}
    </div>
  );
}

function Fallback({ what }: { what: string }, { error, retry }: ErrorInfo) {
  const message = error instanceof Error ? error.message : "Unknown error";
  return <ErrorMessage what={what} message={message} onRetry={retry} />;
}

export const ErrorBoundary = catchError(Fallback);
