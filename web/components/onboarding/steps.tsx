import { icons } from "@/components/ui/icons";
import { cn } from "@/lib/utils";

const steps = ["Import", "Review", "Deploy"];

// A real sequence, so numbered markers are allowed here (design.md §6.1).
export function Steps({ current }: { current: number }) {
  return (
    <ol className="flex items-center gap-3 text-13" aria-label="Progress">
      {steps.map((s, i) => (
        <li key={s} className="flex items-center gap-3" aria-current={i === current ? "step" : undefined}>
          <span className={cn("flex items-center gap-2", i === current ? "text-fg" : "text-muted")}>
            <span
              className={cn(
                "inline-flex size-6 items-center justify-center rounded-full border font-mono text-12",
                i < current ? "border-ok/50 text-ok" : i === current ? "border-fg text-fg" : "border-border-strong text-dim",
              )}
            >
              {i < current ? <icons.check className="size-3.5" /> : i + 1}
            </span>
            {s}
          </span>
          {i < steps.length - 1 && <span className="h-px w-8 bg-border-strong" aria-hidden="true" />}
        </li>
      ))}
    </ol>
  );
}
