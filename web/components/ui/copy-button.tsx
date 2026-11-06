"use client";

import { useState } from "react";
import { cx } from "@/lib/format";
import { icons } from "./icons";

export function CopyButton({ value, label = "Copy", className }: { value: string; label?: string; className?: string }) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  }

  const Glyph = copied ? icons.check : icons.copy;
  return (
    <button
      type="button"
      onClick={copy}
      aria-label={copied ? "Copied" : `${label}: ${value}`}
      className={cx(
        "inline-flex size-7 shrink-0 items-center justify-center rounded-control text-dim transition-colors duration-150 ease-out hover:bg-surface hover:text-fg max-md:size-11",
        copied && "text-ok",
        className,
      )}
    >
      <Glyph />
    </button>
  );
}

export function Copyable({ value, className }: { value: string; className?: string }) {
  return (
    <span className={cx("inline-flex min-w-0 items-center gap-1", className)}>
      <span className="truncate font-mono text-13">{value}</span>
      <CopyButton value={value} />
    </span>
  );
}
