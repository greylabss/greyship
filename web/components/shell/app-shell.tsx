import Link from "next/link";
import { cookies } from "next/headers";
import { Suspense, type ReactNode } from "react";
import { Meter } from "@/components/ui/meter";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "@/lib/api";
import { parseScenario, scenarioCookie } from "@/lib/fixtures/scenarios";
import { ScenarioSwitcher } from "./scenario-switcher";
import { CommandMenu } from "./command-menu";
import { AccountMenu, UserMenu } from "./menus";
import { BottomBar, Sidebar } from "./sidebar";
import { Topbar } from "./topbar";

export function AppShell({ team, children }: { team: Promise<string>; children: ReactNode }) {
  return (
    <div className="flex min-h-dvh">
      <Sidebar
        account={
          <Suspense fallback={<Skeleton className="h-8 flex-1" />}>
            <Account team={team} />
          </Suspense>
        }
        footer={
          <Suspense fallback={<Skeleton className="mx-3 mb-3 h-24" />}>
            <SidebarFooter team={team} />
          </Suspense>
        }
      />
      <div className="flex min-w-0 flex-1 flex-col">
        <Topbar />
        <main className="mx-auto flex w-full max-w-[1280px] flex-1 flex-col px-4 pb-28 pt-6 md:px-8 md:pb-12">{children}</main>
      </div>
      <BottomBar />
      <Suspense>
        <Palette team={team} />
      </Suspense>
      {process.env.NODE_ENV === "development" && (
        <Suspense>
          <DevScenario />
        </Suspense>
      )}
    </div>
  );
}

async function Account({ team: teamSlug }: { team: Promise<string> }) {
  const team = await teamSlug;
  const [me, info] = await Promise.all([api.me(), api.getTeam(team)]).catch(() => [null, null] as const);
  if (!me || !info) return null;
  return <AccountMenu team={info.slug} name={info.name} email={me.email} />;
}

async function Palette({ team: teamSlug }: { team: Promise<string> }) {
  const team = await teamSlug;
  const projects = await api.listProjects(team).catch(() => []);
  return <CommandMenu projects={projects.map((p) => ({ slug: p.slug, url: p.url }))} />;
}

async function SidebarFooter({ team: teamSlug }: { team: Promise<string> }) {
  const team = await teamSlug;
  const [me, usage] = await Promise.all([api.me(), api.myUsage()]).catch(() => [null, null] as const);
  if (!me) return null;
  const limited = usage && (usage.memoryMaxMb !== null || usage.cpuMaxCores !== null);
  return (
    <div className="flex flex-col gap-3 px-3 pb-3">
      {limited && usage.memoryMaxMb !== null && (
        <Link href={`/${team}/usage`} className="flex flex-col gap-2 rounded-panel border border-border p-3 hover:border-border-strong">
          <div className="flex items-baseline justify-between gap-2">
            <span className="text-13 font-medium">Memory</span>
            <span className="text-12 text-muted tabular">
              {usage.memoryUsedMb} / {usage.memoryMaxMb} MB
            </span>
          </div>
          <Meter value={usage.memoryUsedMb} max={usage.memoryMaxMb} label="Memory used" />
        </Link>
      )}
      <UserMenu team={team} email={me.email} initials={me.initials} />
    </div>
  );
}

async function DevScenario() {
  const store = await cookies();
  return <ScenarioSwitcher current={parseScenario(store.get(scenarioCookie)?.value)} />;
}
