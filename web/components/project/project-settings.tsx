"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/field";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/primitives/dialog";
import { ApiError, call } from "@/lib/greyship-api";

// A project's settings: its name, and deleting it (decisions.md 056). Deleting asks for the slug, then the project
// is gone from the dashboard at once while its apps stop and its files are removed in the background.
export function ProjectSettings({ team, id, slug, repo }: { team: string; id: string; slug: string; repo?: { fullName: string; branch: string } }) {
  const router = useRouter();
  const [name, setName] = useState(slug);
  const [renaming, setRenaming] = useState(false);
  const [confirm, setConfirm] = useState("");
  const [deleting, setDeleting] = useState(false);
  const [open, setOpen] = useState(false);

  async function rename() {
    setRenaming(true);
    try {
      const { project } = await call<{ project: { slug: string } }>("PATCH", `/v1/projects/${encodeURIComponent(id)}`, { name: name.trim() });
      toast.success("Renamed");
      router.replace(`/${team}/${project.slug}/settings`);
      router.refresh();
    } catch (e) {
      toast.error("Couldn't rename it", { description: e instanceof ApiError ? e.message : "Try again." });
    } finally {
      setRenaming(false);
    }
  }

  async function remove() {
    setDeleting(true);
    try {
      await call("DELETE", `/v1/projects/${encodeURIComponent(id)}`, { confirm: confirm.trim() });
      toast.success(`Deleting ${slug}`);
      router.replace(`/${team}`);
      router.refresh();
    } catch (e) {
      toast.error("Couldn't delete it", { description: e instanceof ApiError ? e.message : "Try again." });
      setDeleting(false);
    }
  }

  return (
    <div className="flex max-w-2xl flex-col gap-10">
      <form
        className="flex flex-col gap-4 rounded-panel border border-border p-5"
        onSubmit={(e) => {
          e.preventDefault();
          void rename();
        }}
      >
        <h2 className="font-semibold">Name</h2>
        <Field label="Project name" hint="The name sets the address of its services (<service>-<name>). Renaming keeps its history and its folder links.">
          <Input value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
        </Field>
        <div>
          <Button type="submit" disabled={renaming || !name.trim() || name.trim() === slug}>
            {renaming ? "Saving…" : "Save name"}
          </Button>
        </div>
        {repo && (
          <p className="text-13 text-muted">
            Connected to <span className="font-mono text-fg">{repo.fullName}</span> on <span className="font-mono text-fg">{repo.branch}</span>. Every push to that branch deploys.
          </p>
        )}
      </form>

      <section className="flex flex-col gap-3 rounded-panel border border-error/50 p-5">
        <h2 className="font-semibold">Delete project</h2>
        <p className="text-13 text-muted">
          Stops this project&apos;s apps and deletes it with its deployments, env vars, logs, domains and stored files. This can&apos;t be undone.
        </p>
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>
            <Button variant="destructive" className="self-start">
              Delete this project
            </Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Delete {slug}?</DialogTitle>
              <DialogDescription>Its apps stop and everything it stored is removed for good. Type the project&apos;s name to confirm.</DialogDescription>
            </DialogHeader>
            <form
              className="flex flex-col gap-4"
              onSubmit={(e) => {
                e.preventDefault();
                void remove();
              }}
            >
              <Field label={`Type ${slug}`}>
                <Input value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="off" mono />
              </Field>
              <DialogFooter>
                <Button type="submit" variant="destructive" disabled={deleting || confirm.trim() !== slug}>
                  {deleting ? "Deleting…" : "Delete for good"}
                </Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </Dialog>
      </section>
    </div>
  );
}
