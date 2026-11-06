import { Suspense, type ReactNode } from "react";
import { ErrorBoundary } from "./error-panel";

// Every data-backed region gets its own skeleton and its own error state.
export function DataSection({ what, fallback, children }: { what: string; fallback: ReactNode; children: ReactNode }) {
  return (
    <ErrorBoundary what={what}>
      <Suspense fallback={fallback}>{children}</Suspense>
    </ErrorBoundary>
  );
}
