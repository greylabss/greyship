"use client";

import { Toaster as Sonner } from "sonner";

export function Toaster() {
  return (
    <Sonner
      theme="dark"
      position="bottom-right"
      offset={20}
      toastOptions={{
        classNames: {
          toast: "!bg-surface-2 !border !border-border-strong !text-fg !rounded-panel !shadow-overlay !font-sans !text-14",
          description: "!text-muted !text-13",
          actionButton: "!bg-fg !text-bg !rounded-input !font-medium",
          cancelButton: "!bg-surface !text-muted !rounded-input",
          success: "[&_[data-icon]]:!text-ok",
          error: "[&_[data-icon]]:!text-error",
        },
      }}
    />
  );
}
