import type { Metadata } from "next";
import { notFound, redirect } from "next/navigation";
import { connection } from "next/server";
import { Suspense } from "react";
import { AppShell } from "@/components/shell/app-shell";
import { api, liveMode } from "@/lib/api";

export const metadata: Metadata = { robots: { index: false, follow: false } };

export function generateStaticParams() {
  return [{ team: "haki" }];
}

export default function TeamLayout({ children, params }: LayoutProps<"/[team]">) {
  const team = params.then((p) => p.team);
  return (
    <>
      {liveMode && (
        <Suspense>
          <SignedIn team={team} />
        </Suspense>
      )}
      <AppShell team={team}>{children}</AppShell>
    </>
  );
}

// Signed out goes to sign in and comes back; a team the person isn't in looks like it doesn't exist.
async function SignedIn({ team: teamSlug }: { team: Promise<string> }): Promise<null> {
  await connection();
  const team = await teamSlug;
  let teams: string[];
  try {
    teams = await api.teams();
  } catch (e) {
    if (e instanceof Error && "status" in e && (e as { status: number }).status === 401) redirect(`/signin?next=/${encodeURIComponent(team)}`);
    throw e;
  }
  if (!teams.includes(team)) notFound();
  return null;
}
