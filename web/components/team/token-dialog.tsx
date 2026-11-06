"use client";

import { useState } from "react";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/primitives/dialog";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { Field, Input } from "@/components/ui/field";

export function TokenDialog() {
  const [token, setToken] = useState<string | null>(null);
  return (
    <Dialog onOpenChange={(o) => !o && setToken(null)}>
      <DialogTrigger asChild>
        <Button className="self-start">Create token</Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{token ? "Copy your token now" : "Create an API token"}</DialogTitle>
          <DialogDescription>{token ? "You won't be able to see it again." : "Use it as GREYSHIP_TOKEN in CI."}</DialogDescription>
        </DialogHeader>
        {token ? (
          <div className="flex items-center gap-1 rounded-input border border-border-strong bg-surface-2 px-3 py-2">
            <code className="flex-1 truncate font-mono text-13">{token}</code>
            <CopyButton value={token} label="Copy token" />
          </div>
        ) : (
          <form
            id="token-form"
            onSubmit={(e) => {
              e.preventDefault();
              setToken("gs_live_7Kq2vX9mR4tW8nB3cL6pZ1aY5hJ0dF");
            }}
          >
            <Field label="Name" hint="Where it's used, e.g. github-actions">
              <Input name="name" defaultValue="github-actions" mono />
            </Field>
          </form>
        )}
        <DialogFooter>{!token && <Button type="submit" form="token-form" variant="primary">Create token</Button>}</DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
