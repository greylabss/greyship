import type { Metadata } from "next";
import { SystemPage } from "@/components/ui/system-page";
import { buttonClass } from "@/components/ui/button";

export const metadata: Metadata = { title: "Maintenance", robots: { index: false } };

export default function MaintenancePage() {
  return (
    <SystemPage
      code="maintenance · until 14:30 UTC"
      title="The dashboard is down for planned maintenance."
      body="Your apps keep serving traffic and waking on requests. Deploys and settings changes resume when we're back."
      actions={
        <a href="https://x.com/greylabss" className={buttonClass("secondary")}>
          Updates on X
        </a>
      }
    />
  );
}
