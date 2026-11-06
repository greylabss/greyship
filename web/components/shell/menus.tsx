"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from "@/components/primitives/dropdown-menu";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/primitives/popover";
import { BetaBadge } from "@/components/ui/beta-badge";
import { icons } from "@/components/ui/icons";
import { apiEnabled, call } from "@/lib/greyship-api";

export function AccountMenu({ team, name, plan, email, betaTester = false }: { team: string; name: string; plan: string; email: string; betaTester?: boolean }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger className="flex h-9 min-w-0 flex-1 items-center gap-2 rounded-input px-2 outline-none hover:bg-surface focus-visible:ring-2 focus-visible:ring-fg/50 data-[state=open]:bg-surface">
        <span className="truncate font-medium">{name}</span>
        {betaTester && <BetaBadge />}
        {plan !== "unlimited" && <span className="rounded-control border border-border-strong px-1.5 text-12 capitalize text-muted">{plan}</span>}
        <icons.updown className="ml-auto text-dim" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-60">
        <DropdownMenuLabel className="flex flex-col font-normal">
          <span className="flex items-center gap-1.5 font-medium text-fg">
            {name}
            {betaTester && <span className="text-12 font-normal text-beta">beta tester</span>}
          </span>
          <span className="text-12 text-muted">{email}</span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link href={`/${team}/settings`}>
            <icons.settings />
            Settings
          </Link>
        </DropdownMenuItem>
        {plan !== "unlimited" && (
          <DropdownMenuItem asChild>
            <Link href={`/${team}/billing`}>
              <icons.billing />
              Billing
              <DropdownMenuShortcut className="capitalize">{plan}</DropdownMenuShortcut>
            </Link>
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function UserMenu({ team, email, initials }: { team: string; email: string; initials: string }) {
  const router = useRouter();
  async function signOut() {
    await call("POST", "/v1/auth/logout").catch(() => undefined);
    router.replace("/continue");
    router.refresh();
  }
  return (
    <DropdownMenu>
      <DropdownMenuTrigger className="flex min-w-0 flex-1 items-center gap-2 rounded-input p-1 text-left outline-none hover:bg-surface focus-visible:ring-2 focus-visible:ring-fg/50 data-[state=open]:bg-surface">
        <span aria-hidden="true" className="inline-flex size-7 shrink-0 items-center justify-center rounded-full bg-border text-12 font-semibold">
          {initials}
        </span>
        <span className="min-w-0 flex-1 truncate text-13 text-muted">{email}</span>
        <icons.more className="text-dim" />
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="start" className="w-(--radix-dropdown-menu-trigger-width) min-w-56">
        <DropdownMenuItem asChild>
          <Link href={`/${team}/settings`}>
            <icons.settings />
            Account settings
          </Link>
        </DropdownMenuItem>
        <DropdownMenuItem asChild>
          <Link href="/blog">
            <icons.docs />
            How it works
          </Link>
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" onSelect={signOut}>
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

type Note = { id: string; kind: string; title: string; body: string; link: string; read: boolean; created_at: string };

function ago(iso: string) {
  const s = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000));
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

// The bell: deploy results and new projects (decisions.md 046). With real data it polls once a minute for the
// unread dot and marks everything read when opened.
export function NotificationsButton() {
  const [notes, setNotes] = useState<Note[] | null>(null);
  const [unread, setUnread] = useState(0);

  useEffect(() => {
    if (!apiEnabled) return;
    let stop = false;
    const load = async () => {
      const res = await call<{ notifications: Note[]; unread: number }>("GET", "/v1/notifications?limit=20").catch(() => null);
      if (res && !stop) {
        setNotes(res.notifications);
        setUnread(res.unread);
      }
    };
    void load();
    const id = setInterval(load, 60_000);
    return () => {
      stop = true;
      clearInterval(id);
    };
  }, []);

  function opened(open: boolean) {
    if (!open || unread === 0) return;
    setUnread(0);
    void call("POST", "/v1/notifications/read").catch(() => undefined);
  }

  return (
    <Popover onOpenChange={opened}>
      <PopoverTrigger
        aria-label={unread > 0 ? `Notifications, ${unread} unread` : "Notifications"}
        className="relative inline-flex size-9 items-center justify-center rounded-input text-muted outline-none hover:bg-surface hover:text-fg focus-visible:ring-2 focus-visible:ring-fg/50 data-[state=open]:bg-surface data-[state=open]:text-fg max-md:size-11"
      >
        <icons.bell />
        {unread > 0 && <span aria-hidden="true" className="absolute right-2 top-2 size-2 rounded-full bg-accent" />}
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80 p-0">
        <div className="border-b border-border px-4 py-3 font-medium">Notifications</div>
        {notes && notes.length > 0 ? (
          <ul className="max-h-96 divide-y divide-border overflow-y-auto">
            {notes.map((n) => (
              <li key={n.id}>
                <Link href={n.link} className="flex flex-col gap-0.5 px-4 py-3 hover:bg-surface-2">
                  <span className="flex items-center gap-2 font-medium">
                    {!n.read && <span aria-hidden="true" className="size-1.5 shrink-0 rounded-full bg-accent" />}
                    <span className={n.kind === "deploy.failed" ? "text-error" : undefined}>{n.title}</span>
                  </span>
                  <span className="line-clamp-2 text-13 text-muted">{n.body}</span>
                  <span className="text-12 text-dim">{ago(n.created_at)}</span>
                </Link>
              </li>
            ))}
          </ul>
        ) : (
          <div className="flex flex-col gap-1 px-4 py-6 text-13 text-muted">
            <span className="text-fg">You&apos;re all caught up.</span>
            Deploy results and new projects show up here.
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}
