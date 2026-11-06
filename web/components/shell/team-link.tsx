"use client";

import { useParams } from "next/navigation";
import type { ComponentProps } from "react";
import { LinkButton } from "@/components/ui/button";

// Links relative to the current team, for page headers that render outside data boundaries.
export function TeamLinkButton({ to, ...props }: { to: string } & Omit<ComponentProps<typeof LinkButton>, "href">) {
  const { team } = useParams<{ team: string }>();
  return <LinkButton href={`/${team}${to}`} {...props} />;
}
