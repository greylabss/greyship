"use client";

import { Children, createContext, useContext, useMemo, useState, type ReactNode } from "react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/primitives/dropdown-menu";
import { ToggleGroup, ToggleGroupItem } from "@/components/primitives/toggle-group";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/primitives/tooltip";
import { icons } from "@/components/ui/icons";
import { cn } from "@/lib/utils";

type Sort = "recent" | "name" | "requests";
type Layout = "grid" | "list";
type ViewState = { query: string; sort: Sort; layout: Layout };
type View = ViewState & { set: (v: Partial<ViewState>) => void };

const ViewContext = createContext<View | null>(null);

function useView() {
  const view = useContext(ViewContext);
  if (!view) throw new Error("ProjectsView components must sit inside ProjectsView");
  return view;
}

// Search, sort and layout run client-side over the project list the server already loaded.
export function ProjectsView({ children }: { children: ReactNode }) {
  const [state, setState] = useState<ViewState>({ query: "", sort: "recent", layout: "grid" });
  const value = useMemo(() => ({ ...state, set: (v: Partial<ViewState>) => setState((s) => ({ ...s, ...v })) }), [state]);
  return <ViewContext.Provider value={value}>{children}</ViewContext.Provider>;
}

const sorts: { value: Sort; label: string }[] = [
  { value: "recent", label: "Last deployed" },
  { value: "name", label: "Name" },
  { value: "requests", label: "Requests · 24h" },
];

export function ProjectsToolbar({ action }: { action: ReactNode }) {
  const view = useView();
  return (
    <div className="flex flex-wrap items-center gap-2">
      <label className="relative min-w-60 flex-1">
        <span className="sr-only">Search projects</span>
        <icons.search className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-dim" />
        <input
          type="search"
          value={view.query}
          onChange={(e) => view.set({ query: e.target.value })}
          placeholder="Search projects…"
          className="h-10 w-full rounded-input border border-border-strong bg-surface pl-9 pr-3 text-14 text-fg placeholder:text-dim"
        />
      </label>
      <DropdownMenu>
        <Tooltip>
          <TooltipTrigger asChild>
            <DropdownMenuTrigger
              aria-label="Sort projects"
              className="inline-flex size-10 items-center justify-center rounded-input border border-border-strong text-muted outline-none hover:bg-surface hover:text-fg focus-visible:ring-2 focus-visible:ring-fg/50 data-[state=open]:bg-surface"
            >
              <icons.sort />
            </DropdownMenuTrigger>
          </TooltipTrigger>
          <TooltipContent>Sort</TooltipContent>
        </Tooltip>
        <DropdownMenuContent align="end" className="w-48">
          <DropdownMenuLabel className="text-12 font-normal text-muted">Sort by</DropdownMenuLabel>
          <DropdownMenuRadioGroup value={view.sort} onValueChange={(v) => view.set({ sort: v as Sort })}>
            {sorts.map((s) => (
              <DropdownMenuRadioItem key={s.value} value={s.value}>
                {s.label}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      {action}
    </div>
  );
}

export type ProjectEntry = { key: string; name: string; recentRank: number; requests: number };

export function ProjectsLayout({ entries, children, empty }: { entries: ProjectEntry[]; children: ReactNode; empty: ReactNode }) {
  const view = useView();
  const nodes = Children.toArray(children);
  const q = view.query.trim().toLowerCase();

  const shown = entries
    .map((e, i) => ({ e, node: nodes[i] }))
    .filter(({ e }) => !q || e.name.toLowerCase().includes(q))
    .sort((a, b) =>
      view.sort === "name" ? a.e.name.localeCompare(b.e.name) : view.sort === "requests" ? b.e.requests - a.e.requests : a.e.recentRank - b.e.recentRank,
    );

  return (
    <div className="flex flex-col gap-3">
      <div className="flex h-9 items-center justify-between">
        <h2 className="text-16 font-semibold">
          Projects <span className="font-normal text-muted tabular">{shown.length}</span>
        </h2>
        <ToggleGroup
          type="single"
          value={view.layout}
          onValueChange={(v) => v && view.set({ layout: v as Layout })}
          aria-label="Layout"
          className="rounded-input border border-border-strong p-0.5"
        >
          <ToggleGroupItem value="grid" aria-label="Grid layout" className="size-8">
            <icons.grid />
          </ToggleGroupItem>
          <ToggleGroupItem value="list" aria-label="List layout" className="size-8">
            <icons.list />
          </ToggleGroupItem>
        </ToggleGroup>
      </div>
      {shown.length === 0 ? (
        empty
      ) : (
        <div className={cn("grid grid-cols-[minmax(0,1fr)] gap-3", view.layout === "grid" && "xl:grid-cols-2")}>
          {shown.map(({ e, node }) => (
            <div key={e.key} className="grid min-w-0 grid-cols-[minmax(0,1fr)]">
              {node}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
