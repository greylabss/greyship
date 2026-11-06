"use client";

import Link from "next/link";
import { useParams, usePathname } from "next/navigation";
import { useEffect, useRef, useState, type PointerEvent, type ReactNode } from "react";
import { Logo } from "@/components/brand/logo";
import { icons } from "@/components/ui/icons";
import { ProjectTile } from "@/components/ui/project-tile";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/primitives/tooltip";
import { openCommandMenu } from "./command-menu";
import { cx } from "@/lib/format";
import { mobileNav, projectFromPath, projectNav, teamNav, type NavItem } from "./nav";

const minWidth = 200;
const maxWidth = 320;
const storageKey = "gs_sidebar";

type Prefs = { width: number; collapsed: boolean };

function loadPrefs(): Prefs | null {
  try {
    const raw = localStorage.getItem(storageKey);
    return raw ? (JSON.parse(raw) as Prefs) : null;
  } catch {
    return null;
  }
}

function savePrefs(prefs: Prefs) {
  try {
    localStorage.setItem(storageKey, JSON.stringify(prefs));
  } catch {
    // Storage blocked (private window): the sidebar still works, it just forgets.
  }
}

function useNav() {
  const { team } = useParams<{ team: string }>();
  const path = usePathname();
  const project = projectFromPath(path);
  const items = project ? projectNav(team, project) : teamNav(team);
  return { team, path, project, items };
}

export function Sidebar({ account, footer }: { account: ReactNode; footer: ReactNode }) {
  const { team, path, project, items } = useNav();
  const [prefs, setPrefs] = useState<Prefs>({ width: 248, collapsed: false });
  const dragging = useRef(false);

  useEffect(() => {
    const stored = loadPrefs();
    // eslint-disable-next-line react-hooks/set-state-in-effect -- localStorage only exists after hydration
    if (stored) setPrefs(stored);
  }, []);

  function update(next: Prefs) {
    setPrefs(next);
    savePrefs(next);
  }

  function onPointerDown(e: PointerEvent<HTMLDivElement>) {
    dragging.current = true;
    e.currentTarget.setPointerCapture(e.pointerId);
  }

  function onPointerMove(e: PointerEvent<HTMLDivElement>) {
    if (!dragging.current) return;
    const width = Math.min(maxWidth, Math.max(minWidth, e.clientX));
    setPrefs((p) => ({ ...p, width, collapsed: false }));
  }

  function onPointerUp() {
    if (!dragging.current) return;
    dragging.current = false;
    savePrefs(prefs);
  }

  const collapsed = prefs.collapsed;

  return (
    <aside
      className="sticky top-0 hidden h-dvh shrink-0 flex-col border-r border-border bg-bg md:flex"
      style={{ width: collapsed ? 60 : prefs.width }}
      aria-label="Sidebar"
    >
      <div className={cx("flex h-14 items-center gap-2", collapsed ? "justify-center" : "px-3")}>
        <Logo href={`/${team}`} wordmark={false} height={24} />
        {!collapsed && account}
      </div>

      {!collapsed && (
        <div className="px-3 pb-2">
          <button
            type="button"
            onClick={openCommandMenu}
            className="flex h-9 w-full items-center gap-2 rounded-input border border-border bg-surface px-2.5 text-muted hover:border-border-strong hover:text-fg"
          >
            <icons.search />
            <span className="flex-1 text-left">Find…</span>
            <kbd className="rounded-control border border-border-strong px-1.5 font-mono text-12 text-dim">⌘K</kbd>
          </button>
        </div>
      )}

      {project && !collapsed && (
        <div className="mx-3 mb-1 flex items-center gap-2.5 rounded-input border border-border px-2.5 py-2">
          <ProjectTile name={project} />
          <div className="flex min-w-0 flex-col">
            <span className="truncate font-medium">{project}</span>
            <Link href={`/${team}`} className="text-12 text-muted hover:text-fg">
              All projects
            </Link>
          </div>
        </div>
      )}

      <nav aria-label={project ? "Project" : "Team"} className="flex flex-1 flex-col gap-0.5 overflow-y-auto px-2 py-2">
        {items.map((item) => (
          <SidebarLink key={item.href} item={item} active={item.match(path)} collapsed={collapsed} />
        ))}
      </nav>

      {!collapsed && footer}

      <div className={cx("flex border-t border-border p-2", collapsed ? "justify-center" : "justify-end")}>
        <button
          type="button"
          onClick={() => update({ ...prefs, collapsed: !collapsed })}
          aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          aria-expanded={!collapsed}
          className="inline-flex size-8 items-center justify-center rounded-control text-dim hover:bg-surface hover:text-fg"
        >
          <icons.sidebar />
        </button>
      </div>

      {!collapsed && (
        <div
          role="separator"
          aria-orientation="vertical"
          aria-label="Resize sidebar"
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onDoubleClick={() => update({ ...prefs, width: 248 })}
          className="absolute inset-y-0 -right-1 w-2 cursor-col-resize hover:bg-border"
        />
      )}
    </aside>
  );
}

function SidebarLink({ item, active, collapsed }: { item: NavItem; active: boolean; collapsed: boolean }) {
  const Glyph = icons[item.icon];
  const link = (
    <Link
      href={item.href}
      aria-current={active ? "page" : undefined}
      className={cx(
        "relative flex h-9 items-center gap-3 rounded-input px-2.5 transition-colors duration-150 ease-out",
        collapsed && "justify-center px-0",
        active ? "bg-surface text-fg" : "text-muted hover:bg-surface hover:text-fg",
      )}
    >
      <Glyph />
      {collapsed ? <span className="sr-only">{item.label}</span> : <span className="truncate">{item.label}</span>}
    </Link>
  );
  if (!collapsed) return link;
  return (
    <Tooltip>
      <TooltipTrigger asChild>{link}</TooltipTrigger>
      <TooltipContent side="right">{item.label}</TooltipContent>
    </Tooltip>
  );
}

export function BottomBar() {
  const { path, items } = useNav();
  return (
    <nav
      aria-label="Primary"
      className="fixed inset-x-3 bottom-3 z-20 flex justify-around rounded-panel border border-border-strong bg-surface-2 p-1 md:hidden"
    >
      {mobileNav(items).map((item) => {
        const Glyph = icons[item.icon];
        const active = item.match(path);
        return (
          <Link
            key={item.href}
            href={item.href}
            aria-current={active ? "page" : undefined}
            className={cx(
              "flex min-h-11 min-w-11 flex-1 flex-col items-center justify-center gap-0.5 rounded-input text-12",
              active ? "bg-surface text-fg" : "text-muted",
            )}
          >
            <Glyph />
            <span className="truncate">{item.label}</span>
          </Link>
        );
      })}
    </nav>
  );
}
