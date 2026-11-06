import { cx } from "@/lib/format";

// Stands in for a project's own icon until projects can upload one.
export function ProjectTile({ name, size = "sm" }: { name: string; size?: "xs" | "sm" | "md" }) {
  return (
    <span
      aria-hidden="true"
      className={cx(
        "inline-flex shrink-0 items-center justify-center rounded-input border border-border-strong bg-surface font-mono font-medium uppercase text-fg",
        size === "xs" ? "size-5 rounded-control text-12" : size === "sm" ? "size-8 text-13" : "size-10 text-16",
      )}
    >
      {name.slice(0, 2)}
    </span>
  );
}
