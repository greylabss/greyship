import type { Metadata } from "next";
import { Suspense } from "react";
import { AuthShell } from "@/components/auth/auth-shell";
import { DeviceConfirm } from "@/components/auth/device-confirm";

export const metadata: Metadata = { title: "Authorize CLI" };

export default function DevicePage() {
  return (
    <AuthShell aside={null}>
      <Suspense>
        <DeviceConfirm />
      </Suspense>
    </AuthShell>
  );
}
