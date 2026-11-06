import { clsx, type ClassValue } from "clsx";
import { extendTailwindMerge } from "tailwind-merge";

// tailwind-merge must know the design.md scales, or it treats text-14 as a colour.
const merge = extendTailwindMerge({
  extend: {
    theme: {
      text: ["12", "13", "14", "16", "20", "28", "44", "64"],
      radius: ["control", "input", "panel"],
      shadow: ["overlay"],
      color: ["bg", "surface", "surface-2", "border", "border-strong", "fg", "muted", "dim", "accent", "ok", "error", "warn"],
    },
  },
});

export function cn(...inputs: ClassValue[]) {
  return merge(clsx(inputs));
}
