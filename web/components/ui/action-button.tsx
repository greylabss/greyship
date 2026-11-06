"use client";

import type { ComponentProps } from "react";
import { toast } from "sonner";
import { Button } from "./button";

// Mock-phase stand-in for a button whose real effect is an API call: it shows the outcome as a toast.
export function ActionButton({ message, description, ...props }: ComponentProps<typeof Button> & { message: string; description?: string }) {
  return <Button {...props} onClick={() => toast.success(message, { description })} />;
}
