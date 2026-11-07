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
import { Field, Input } from "@/components/ui/field";
import type { GitHubAppStatus } from "@/lib/api/live";
import { ApiError, call } from "@/lib/greyship-api";

// A team's own GitHub App (decisions.md 055): create it with one click and a confirmation on GitHub, or see and
// remove it. Everyone's app is their own; nothing here is shared across the instance.
export function GitHubApp({ team, app }: { team: string; app: GitHubAppStatus }) {
  const router = useRouter();
  const [org, setOrg] = useState("");
  const [name, setName] = useState(app.default_name);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const base = `/v1/teams/${encodeURIComponent(team)}/github/app`;

  // GitHub takes the manifest as a form post from the person's own browser, then sends them back here.
  async function create() {
    setError("");
    setBusy(true);
    try {
      const { action, manifest } = await call<{ action: string; manifest: unknown }>("POST", `${base}/manifest`, { org: org.trim(), name: name.trim() });
      const form = document.createElement("form");
      form.method = "POST";
      form.action = action;
      const field = document.createElement("input");
      field.type = "hidden";
      field.name = "manifest";
      field.value = JSON.stringify(manifest);
      form.appendChild(field);
      document.body.appendChild(form);
      form.submit();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Couldn't start. Try again.");
      setBusy(false);
    }
  }

  async function remove() {
    try {
      await call("DELETE", base);
      toast.success("GitHub App removed");
      router.refresh();
    } catch (e) {
      toast.error("Couldn't remove it", { description: e instanceof ApiError ? e.message : "Try again." });
    }
  }

  if (app.configured) {
    return (
      <div className="flex max-w-xl flex-col gap-4 rounded-panel border border-border p-5">
        <div className="flex flex-col gap-1">
          <span className="font-medium">{app.name}</span>
          <a href={app.html_url} className="text-13 text-muted underline-offset-4 hover:text-fg hover:underline">
            Open the app on GitHub
          </a>
        </div>
        <p className="text-13 text-muted">
          Install it on the repositories you want to deploy from <span className="text-fg">New project → Import from GitHub</span>. Pushes reach Greyship at{" "}
          <span className="break-all font-mono text-12 text-fg">{app.webhook_url}</span>.
        </p>
        {app.warnings.map((w) => (
          <p key={w} className="text-13 text-error">
            {w}
          </p>
        ))}
        <AlertDialog>
          <AlertDialogTrigger asChild>
            <Button variant="ghost" size="sm" className="self-start">
              Remove the app
            </Button>
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Remove this GitHub App from Greyship?</AlertDialogTitle>
              <AlertDialogDescription>
                Projects connected through it stop deploying on push, and you will need to create the app again and reinstall it. Delete the app on GitHub too if you
                want it gone there.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction onClick={remove}>Remove</AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </div>
    );
  }

  return (
    <form
      className="flex max-w-xl flex-col gap-4 rounded-panel border border-border p-5"
      onSubmit={(e) => {
        e.preventDefault();
        void create();
      }}
    >
      <div className="flex flex-col gap-1">
        <h2 className="font-semibold">Connect your GitHub</h2>
        <p className="text-13 text-muted">
          Create your own GitHub App with one click. You confirm it on GitHub and come straight back, then pick the repositories it may read. It only reads
          what you choose and writes commit statuses.
        </p>
      </div>
      <Field label="App name" hint="Shown on GitHub. It must be unique across GitHub, 34 characters at most.">
        <Input value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
      </Field>
      <Field label="GitHub organisation" hint="Optional. Leave empty to create the app under your own GitHub account.">
        <Input value={org} onChange={(e) => setOrg(e.target.value)} autoComplete="off" placeholder="my-company" mono />
      </Field>
      {app.warnings.map((w) => (
        <p key={w} className="text-13 text-error">
          {w}
        </p>
      ))}
      {error && <span className="text-13 text-error">{error}</span>}
      <div>
        <Button type="submit" disabled={busy}>
          {busy ? "Opening GitHub…" : "Create GitHub App"}
        </Button>
      </div>
    </form>
  );
}
