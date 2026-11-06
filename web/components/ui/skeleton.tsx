import { cx } from "@/lib/format";

export function Skeleton({ className }: { className?: string }) {
  return <span aria-hidden="true" className={cx("skeleton block", className)} />;
}

export function SkeletonRows({ rows = 4, className }: { rows?: number; className?: string }) {
  return (
    <div className={cx("divide-y divide-border rounded-panel border border-border", className)} aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="flex items-center gap-4 px-4 py-4">
          <Skeleton className="size-2 rounded-full" />
          <Skeleton className="h-3.5 w-40" />
          <Skeleton className="ml-auto h-3.5 w-20" />
        </div>
      ))}
    </div>
  );
}
