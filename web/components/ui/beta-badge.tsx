import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/primitives/tooltip";
import { icons } from "@/components/ui/icons";
import { cn } from "@/lib/utils";

// The private-beta tester's checkmark (decisions.md 049): kept for good, so the first users stay recognisable.
export function BetaBadge({ className }: { className?: string }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className={cn("inline-flex shrink-0 items-center text-beta", className)} aria-label="Beta tester">
          <icons.betaTester className="size-4" strokeWidth={2.25} />
        </span>
      </TooltipTrigger>
      <TooltipContent>Beta tester: one of Greyship&apos;s first users</TooltipContent>
    </Tooltip>
  );
}
