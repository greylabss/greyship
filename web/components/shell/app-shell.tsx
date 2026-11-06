import Link from "next/link";
import { cookies } from "next/headers";
import { Suspense, type ReactNode } from "react";
import { Meter } from "@/components/ui/meter";
import { Skeleton } from "@/components/ui/skeleton";
import { api, available } from "@/lib/api";
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

async function load(teamSlug: Promise<string>) {
  const team = await teamSlug;
  return Promise.all([api.me(), api.getTeam(team), available.usage ? api.getUsage(team) : null, available.usage ? null : api.buildUsage(team)]).catch(() => null);
}

async function Account({ team }: { team: Promise<string> }) {
  const data = await load(team);
  if (!data) return null;
  const [me, info] = data;
  return <AccountMenu team={info.slug} name={info.name} plan={info.plan} email={me.email} betaTester={me.betaTester} />;
}

async function Palette({ team: teamSlug }: { team: Promise<string> }) {
  const team = await teamSlug;
  const projects = await api.listProjects(team).catch(() => []);
  return <CommandMenu projects={projects.map((p) => ({ slug: p.slug, url: p.url }))} />;
}

async function SidebarFooter({ team }: { team: Promise<string> }) {
  const data = await load(team);
  if (!data) return null;
  const [me, info, usage, builds] = data;
  if (!usage) {
    return (
      <div className="flex flex-col gap-3 px-3 pb-3">
        {builds && info.plan === "free" && (
          <Link href={`/${info.slug}/usage`} className="flex flex-col gap-2 rounded-panel border border-border p-3 hover:border-border-strong">
            <div className="flex items-baseline justify-between gap-2">
              <span className="text-13 font-medium">Free plan</span>
              <span className="text-12 text-muted tabular">
                {builds.usedMinutes} / {builds.includedMinutes} build min
              </span>
            </div>
            <Meter value={builds.usedMinutes} max={builds.includedMinutes} label="Build minutes used this month" />
            <span className="text-12 text-muted">Resets {builds.resetsOn}.</span>
          </Link>
        )}
        <UserMenu team={info.slug} email={me.email} initials={me.initials} />
      </div>
    );
  }
  const peak = usage.meters.reduce((a, b) => (b.used / b.included > a.used / a.included ? b : a));
  const pct = Math.round((peak.used / peak.included) * 100);

  return (
    <div className="flex flex-col gap-3 px-3 pb-3">
      {info.plan === "free" && (
        <Link href={`/${info.slug}/usage`} className="flex flex-col gap-2 rounded-panel border border-border p-3 hover:border-border-strong">
          <div className="flex items-baseline justify-between gap-2">
            <span className="text-13 font-medium">Free plan</span>
            <span className="text-12 text-muted tabular">{pct}% used</span>
          </div>
          <Meter value={peak.used} max={peak.included} label={`${peak.label} used this month`} />
          <span className="text-12 text-muted">Idle apps don&apos;t count. Resets {usage.resetsOn}.</span>
        </Link>
      )}
      <UserMenu team={info.slug} email={me.email} initials={me.initials} />
    </div>
  );
}

async function DevScenario() {
  const store = await cookies();
  return <ScenarioSwitcher current={parseScenario(store.get(scenarioCookie)?.value)} />;
}
