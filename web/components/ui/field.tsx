import type { ComponentProps, ReactNode } from "react";
import { cn } from "@/lib/utils";

export const inputClass =
  "h-10 w-full rounded-input border border-border-strong bg-surface px-3 text-14 text-fg placeholder:text-dim transition-colors duration-150 ease-out hover:border-dim focus-visible:border-fg focus-visible:outline-none aria-invalid:border-error disabled:opacity-50";

export function Input({ className, mono, ...props }: ComponentProps<"input"> & { mono?: boolean }) {
  return <input className={cn(inputClass, mono && "font-mono text-13", className)} {...props} />;
}

export function Field({ label, hint, error, children, className }: { label: string; hint?: ReactNode; error?: string; children: ReactNode; className?: string }) {
  return (
    <label className={cn("flex flex-col gap-1.5", className)}>
      <span className="text-13 font-medium text-fg">{label}</span>
      {children}
      {error ? <span className="text-12 text-error">{error}</span> : hint && <span className="text-12 text-muted">{hint}</span>}
    </label>
  );
}
