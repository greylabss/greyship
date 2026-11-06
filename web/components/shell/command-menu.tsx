"use client";

import { useParams, useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
  CommandShortcut,
} from "@/components/primitives/command";
import { icons } from "@/components/ui/icons";
import { ProjectTile } from "@/components/ui/project-tile";
import { teamNav } from "./nav";

const openEvent = "gs:command-menu";

export function openCommandMenu() {
  window.dispatchEvent(new Event(openEvent));
}

export function CommandMenu({ projects }: { projects: { slug: string; url: string | null }[] }) {
  const [open, setOpen] = useState(false);
  const router = useRouter();
  const { team } = useParams<{ team: string }>();

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        setOpen((o) => !o);
      }
    }
    const onOpen = () => setOpen(true);
    window.addEventListener("keydown", onKey);
    window.addEventListener(openEvent, onOpen);
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener(openEvent, onOpen);
    };
  }, []);

  function go(href: string) {
    setOpen(false);
    router.push(href);
  }

  function copy(text: string) {
    setOpen(false);
    navigator.clipboard.writeText(text).catch(() => undefined);
  }

  return (
    <CommandDialog open={open} onOpenChange={setOpen} title="Search Greyship" description="Jump to a project or page, or run an action" showCloseButton={false} className="top-[20%] translate-y-0 sm:max-w-xl">
      <CommandInput placeholder="Search projects, pages and actions…" />
      <CommandList>
        <CommandEmpty>No results.</CommandEmpty>
        {projects.length > 0 && (
          <CommandGroup heading="Projects">
            {projects.map((p) => (
              <CommandItem key={p.slug} value={`project ${p.slug} ${p.url ?? ""}`} onSelect={() => go(`/${team}/${p.slug}`)}>
                <ProjectTile name={p.slug} size="xs" />
                <span>{p.slug}</span>
                {p.url && <span className="ml-auto truncate text-12 text-dim">{p.url}</span>}
              </CommandItem>
            ))}
          </CommandGroup>
        )}
        <CommandSeparator />
        <CommandGroup heading="Go to">
          {teamNav(team).map((item) => {
            const Glyph = icons[item.icon];
            return (
              <CommandItem key={item.href} value={`go ${item.label}`} onSelect={() => go(item.href)}>
                <Glyph />
                {item.label}
              </CommandItem>
            );
          })}
        </CommandGroup>
        <CommandSeparator />
        <CommandGroup heading="Actions">
          <CommandItem value="new project import repository" onSelect={() => go(`/${team}/new`)}>
            <icons.plus />
            New project
          </CommandItem>
          <CommandItem value="copy grey deploy command cli" onSelect={() => copy("grey deploy")}>
            <icons.copy />
            Copy <code className="font-mono text-13">grey deploy</code>
          </CommandItem>
          <CommandItem value="copy install cli curl" onSelect={() => copy("curl -fsSL greyship.tech/install | sh")}>
            <icons.copy />
            Copy CLI install command
          </CommandItem>
          <CommandItem value="docs documentation" onSelect={() => window.open("/blog", "_blank")}>
            <icons.docs />
            Open docs
            <CommandShortcut>↗</CommandShortcut>
          </CommandItem>
        </CommandGroup>
      </CommandList>
      <div className="flex items-center gap-4 border-t border-border px-4 py-2 text-12 text-dim">
        <span>
          <kbd className="font-mono">↑↓</kbd> navigate
        </span>
        <span>
          <kbd className="font-mono">↵</kbd> open
        </span>
        <span>
          <kbd className="font-mono">esc</kbd> close
        </span>
      </div>
    </CommandDialog>
  );
}
