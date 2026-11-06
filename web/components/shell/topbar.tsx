"use client";

import Link from "next/link";
import { useParams, usePathname } from "next/navigation";
import { Logo } from "@/components/brand/logo";
import { icons } from "@/components/ui/icons";
import { openCommandMenu } from "./command-menu";
import { NotificationsButton } from "./menus";
import { projectFromPath, teamNav } from "./nav";

export function Topbar() {
  const { team } = useParams<{ team: string }>();
  const path = usePathname();
  const project = projectFromPath(path);
  const page = teamNav(team).find((item) => item.match(path))?.label ?? "Projects";

  return (
    <header className="sticky top-0 z-10 flex h-14 items-center gap-2 border-b border-border bg-bg px-4 md:px-8">
      <div className="mr-1 md:hidden">
        <Logo href={`/${team}`} wordmark={false} />
      </div>
      <nav aria-label="Breadcrumb" className="flex min-w-0 items-center gap-2 text-14">
        <Link href={`/${team}`} className={project ? "text-muted hover:text-fg" : "font-medium text-fg"}>
          {project ? "Projects" : page}
        </Link>
        {project && (
          <>
            <span className="text-dim" aria-hidden="true">
              /
            </span>
            <Link href={`/${team}/${project}`} className="truncate font-medium text-fg hover:underline">
              {project}
            </Link>
          </>
        )}
      </nav>
      <div className="flex-1" />
      <Link href="/blog" className="hidden h-9 items-center gap-2 rounded-input px-2.5 text-muted hover:bg-surface hover:text-fg sm:inline-flex">
        <icons.docs />
        How it works
      </Link>
      <NotificationsButton />
      <button type="button" onClick={openCommandMenu} aria-label="Search" className="inline-flex size-11 items-center justify-center rounded-input text-muted hover:bg-surface md:hidden">
        <icons.search />
      </button>
    </header>
  );
}
