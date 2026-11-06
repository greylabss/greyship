"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { ApiError, call } from "@/lib/greyship-api";
import { generatePassword } from "./create-user";

export function UserActions({ id, email, isAdmin }: { id: string; email: string; isAdmin: boolean }) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);

  async function patch(body: object, done: string) {
    setBusy(true);
    try {
      await call("PATCH", `/v1/admin/users/${encodeURIComponent(id)}`, body);
      toast.success(done);
      router.refresh();
    } catch (e) {
      toast.error("Couldn't change the user", { description: e instanceof ApiError ? e.message : "Try again." });
    } finally {
      setBusy(false);
    }
  }

  async function resetPassword() {
    const password = generatePassword();
    await patch({ password }, "Password reset");
    // Shown once, in the browser only: the server keeps just a hash.
    window.prompt(`New password for ${email}. Copy it now; it isn't shown again.`, password);
  }

  return (
    <div className="flex justify-end gap-1">
      <Button size="sm" variant="ghost" disabled={busy} onClick={() => void resetPassword()}>
        Reset password
      </Button>
      <Button size="sm" variant="ghost" disabled={busy} onClick={() => void patch({ admin: !isAdmin }, isAdmin ? "Removed admin" : "Made admin")}>
        {isAdmin ? "Remove admin" : "Make admin"}
      </Button>
    </div>
  );
}
