"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/primitives/alert-dialog";
import { Button } from "@/components/ui/button";
import { ApiError, call } from "@/lib/greyship-api";

export function RevokeToken({ id, name }: { id: string; name: string }) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  async function revoke() {
    setBusy(true);
    try {
      await call("DELETE", `/v1/tokens/${encodeURIComponent(id)}`);
      toast.success(`Revoked ${name}`);
      router.refresh();
    } catch (err) {
      toast.error("Couldn't revoke it", { description: err instanceof ApiError ? err.message : "Try again." });
    } finally {
      setBusy(false);
    }
  }
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>
        <Button size="sm" variant="ghost" disabled={busy}>
          {busy ? "Revoking…" : "Revoke"}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Revoke {name}?</AlertDialogTitle>
          <AlertDialogDescription>Requests using it will be refused. A computer using it needs grey login again.</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction onClick={revoke}>Revoke</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
