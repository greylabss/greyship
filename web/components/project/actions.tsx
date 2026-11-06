"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState, type ReactNode } from "react";
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
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/primitives/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/primitives/dropdown-menu";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/primitives/select";
import { Button } from "@/components/ui/button";
import { CommandHint } from "@/components/ui/command-hint";
import { icons } from "@/components/ui/icons";
import { ApiError, call } from "@/lib/greyship-api";

// useRedeploy starts a new deployment from an existing one's binaries (Redeploy or Roll back, F-27) and opens its log.
export function useRedeploy(team: string, project: string) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  async function run(id: string) {
    setBusy(true);
    try {
      const res = await call<{ deployment: { id: string } }>("POST", `/v1/deployments/${encodeURIComponent(id)}/redeploy`);
      router.push(`/${team}/${project}/deployments/${res.deployment.id}`);
    } catch (err) {
      toast.error("Couldn't start it", { description: err instanceof ApiError ? err.message : "Try again." });
      setBusy(false);
    }
  }
  return { busy, run };
}

export function DeployButton({ team, project, branches = ["main"], children = "Deploy" }: { team: string; project: string; branches?: string[]; children?: ReactNode }) {
  const router = useRouter();
  const [branch, setBranch] = useState(branches[0]);
  const [open, setOpen] = useState(false);

  function deploy() {
    setOpen(false);
    toast.success(`Deploying ${branch}`, { description: `${project} · build queued` });
    router.push(`/${team}/${project}/deployments/live`);
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="primary">
          <icons.deployments />
          {children}
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Deploy {project}</DialogTitle>
          <DialogDescription>Builds the latest commit on the branch and switches traffic once health checks pass.</DialogDescription>
        </DialogHeader>
        <label className="flex flex-col gap-1.5">
          <span className="text-13 font-medium">Branch</span>
          <Select value={branch} onValueChange={setBranch}>
            <SelectTrigger className="w-full font-mono text-13">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {branches.map((b) => (
                <SelectItem key={b} value={b} className="font-mono text-13">
                  {b}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <span className="text-12 text-muted">{branch === "main" ? "Production deployment" : "Preview deployment with its own URL"}</span>
        </label>
        <CommandHint command={branch === "main" ? "grey deploy" : `grey deploy --branch ${branch}`} />
        <DialogFooter>
          <Button onClick={() => setOpen(false)}>Cancel</Button>
          <Button variant="primary" onClick={deploy}>
            Deploy {branch}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// With onConfirm (real data) the rollback goes through the API; without it, the demo shows a toast.
export function RollbackButton({
  sha,
  current,
  variant = "secondary",
  size = "md",
  onConfirm,
  busy = false,
}: {
  sha: string;
  current: string;
  variant?: "secondary" | "ghost";
  size?: "sm" | "md";
  onConfirm?: () => void;
  busy?: boolean;
}) {
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>
        <Button variant={variant} size={size} disabled={busy}>
          {busy ? "Starting…" : `Roll back to ${sha}`}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Roll back production to {sha}?</AlertDialogTitle>
          <AlertDialogDescription>
            {onConfirm ? (
              <>
                {sha}&apos;s binaries start on fresh instances with the current env vars. Traffic moves from <span className="font-mono text-fg">{current}</span> once
                they pass their health check. No rebuild, and the release command doesn&apos;t run again.
              </>
            ) : (
              <>
                Traffic moves from <span className="font-mono text-fg">{current}</span> to <span className="font-mono text-fg">{sha}</span> right away. No rebuild; the
                binaries are already built. You can roll forward again at any time.
              </>
            )}
          </AlertDialogDescription>
        </AlertDialogHeader>
        {!onConfirm && <CommandHint command={`grey rollback ${sha}`} />}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction onClick={onConfirm ?? (() => toast.success(`Production now serves ${sha}`, { description: "Rolled back in 0.4s · previous instances draining" }))}>
            Roll back to {sha}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

export function RollbackTo({ team, project, id, sha, current }: { team: string; project: string; id: string; sha: string; current: string }) {
  const redeploy = useRedeploy(team, project);
  return <RollbackButton sha={sha} current={current} busy={redeploy.busy} onConfirm={() => redeploy.run(id)} />;
}

export function RowMenu({ label, children }: { label: string; children: ReactNode }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label={label}
        className="inline-flex size-8 items-center justify-center rounded-input text-dim outline-none hover:bg-surface hover:text-fg focus-visible:ring-2 focus-visible:ring-fg/50 data-[state=open]:bg-surface data-[state=open]:text-fg max-md:size-11"
        onClick={(e) => e.stopPropagation()}
      >
        <icons.more />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
        {children}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// restart is the demo's toast only; restarting a service isn't in the API yet.
export function ServiceMenu({ team, project, service, url, restart = true }: { team: string; project: string; service: string; url: string | null; restart?: boolean }) {
  const base = `/${team}/${project}`;
  return (
    <RowMenu label={`Actions for ${service}`}>
      <DropdownMenuItem asChild>
        <Link href={`${base}/logs?service=${service}`}>
          <icons.logs />
          View logs
        </Link>
      </DropdownMenuItem>
      <DropdownMenuItem asChild>
        <Link href={`${base}/metrics?service=${service}`}>
          <icons.metrics />
          Metrics
        </Link>
      </DropdownMenuItem>
      <DropdownMenuItem asChild>
        <Link href={`${base}/settings?service=${service}`}>
          <icons.settings />
          Settings
        </Link>
      </DropdownMenuItem>
      {(url || restart) && <DropdownMenuSeparator />}
      {url && (
        <DropdownMenuItem
          onSelect={() => {
            navigator.clipboard.writeText(url).catch(() => undefined);
            toast("URL copied", { description: url });
          }}
        >
          <icons.copy />
          Copy URL
        </DropdownMenuItem>
      )}
      {restart && (
        <DropdownMenuItem onSelect={() => toast.success(`Restarting ${service}`, { description: "New instances start before old ones drain" })}>
          <icons.deployments />
          Restart
        </DropdownMenuItem>
      )}
    </RowMenu>
  );
}
