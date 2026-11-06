import type { IconName } from "@/components/ui/icons";

export type NavItem = { label: string; href: string; icon: IconName; match: (path: string) => boolean };

const reserved = new Set(["deployments", "usage", "billing", "settings", "new"]);

export function projectFromPath(path: string): string | null {
  const [, , project] = path.split("/");
  return project && !reserved.has(project) ? project : null;
}

export function teamNav(team: string): NavItem[] {
  const base = `/${team}`;
  const at = (seg: string) => (p: string) => p.startsWith(`${base}/${seg}`);
  return [
    { label: "Projects", href: base, icon: "projects", match: (p) => p === base },
    { label: "Deployments", href: `${base}/deployments`, icon: "deployments", match: at("deployments") },
    { label: "Usage", href: `${base}/usage`, icon: "usage", match: at("usage") },
    { label: "Billing", href: `${base}/billing`, icon: "billing", match: at("billing") },
    { label: "Settings", href: `${base}/settings`, icon: "settings", match: at("settings") },
  ];
}

export function projectNav(team: string, project: string): NavItem[] {
  const base = `/${team}/${project}`;
  const at = (seg: string) => (p: string) => p === `${base}/${seg}` || p.startsWith(`${base}/${seg}/`);
  return [
    { label: "Overview", href: base, icon: "overview", match: (p) => p === base },
    { label: "Deployments", href: `${base}/deployments`, icon: "deployments", match: at("deployments") },
    { label: "Logs", href: `${base}/logs`, icon: "logs", match: at("logs") },
    { label: "Metrics", href: `${base}/metrics`, icon: "metrics", match: at("metrics") },
    { label: "Go Runtime", href: `${base}/runtime`, icon: "runtime", match: at("runtime") },
    { label: "Env", href: `${base}/env`, icon: "env", match: at("env") },
    { label: "Domains", href: `${base}/domains`, icon: "domains", match: at("domains") },
    { label: "Settings", href: `${base}/settings`, icon: "settings", match: at("settings") },
  ];
}

// The phone bottom bar fits five items (design.md §2).
export function mobileNav(items: NavItem[]): NavItem[] {
  return items.length <= 5 ? items : [items[0], items[1], items[2], items[5], items[7]];
}
