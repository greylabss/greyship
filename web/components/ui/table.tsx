import type { ComponentProps } from "react";
import { cx } from "@/lib/format";

// Tables scroll inside their box on narrow screens, never the page (design.md §7.10).
export function Table({ className, minWidth = 720, ...props }: ComponentProps<"table"> & { minWidth?: number }) {
  return (
    <div className="relative overflow-x-auto rounded-panel border border-border">
      <table className={cx("w-full border-collapse text-14", className)} style={{ minWidth }} {...props} />
    </div>
  );
}

export function Th({ className, align, ...props }: ComponentProps<"th"> & { align?: "right" }) {
  return (
    <th
      scope="col"
      className={cx("bg-surface px-4 py-2.5 text-left text-12 font-medium text-muted", align === "right" && "text-right", className)}
      {...props}
    />
  );
}

export function Td({ className, align, mono, ...props }: ComponentProps<"td"> & { align?: "right"; mono?: boolean }) {
  return (
    <td
      className={cx(
        "border-t border-border px-4 py-3.5 align-middle",
        align === "right" && "text-right tabular",
        mono && "font-mono text-13",
        className,
      )}
      {...props}
    />
  );
}
