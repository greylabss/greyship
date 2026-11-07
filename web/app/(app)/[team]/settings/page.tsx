import type { Metadata } from "next";
import { ChangePassword } from "@/components/team/change-password";
import { GitHubApp } from "@/components/team/github-app";
import { NotificationSettings } from "@/components/team/notification-settings";
import { RevokeToken } from "@/components/team/revoke-token";
import { TokenDialog } from "@/components/team/token-dialog";
import { ActionButton } from "@/components/ui/action-button";
import { CommandHint } from "@/components/ui/command-hint";
import { DataSection } from "@/components/ui/data-section";
import { PageHeader, SectionHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, Td, Th } from "@/components/ui/table";
import { api, liveMode } from "@/lib/api";
import { githubApp } from "@/lib/api/live";

export const metadata: Metadata = { title: "Settings" };

export default function SettingsPage({ params }: PageProps<"/[team]/settings">) {
  const team = params.then((p) => p.team);
  return (
    <div className="flex max-w-3xl flex-col gap-10">
      <PageHeader title="Settings" />
      <DataSection what="account" fallback={<Skeleton className="h-40 w-full rounded-panel" />}>
        <Account team={team} />
      </DataSection>

      {liveMode && (
        <section className="flex flex-col gap-3">
          <SectionHeader title="Notifications" />
          <p className="text-13 text-muted">For deploys you start and projects you create.</p>
          <NotificationSettings />
        </section>
      )}

      {liveMode && (
        <section className="flex flex-col gap-3">
          <SectionHeader title="GitHub" />
          <p className="text-13 text-muted">Your own GitHub App: it lets Greyship read the repositories you pick and deploy them on every push.</p>
          <DataSection what="github" fallback={<Skeleton className="h-40 w-full rounded-panel" />}>
            <GitHub team={team} />
          </DataSection>
        </section>
      )}

      {liveMode && (
        <section className="flex flex-col gap-3">
          <SectionHeader title="Password" />
          <ChangePassword />
        </section>
      )}

      <section className="flex flex-col gap-3">
        <SectionHeader title="API tokens" aside={<CommandHint command="export GREYSHIP_TOKEN=…" />} />
        <p className="text-13 text-muted">Use a token to deploy from CI. Tokens can do everything you can; revoke any you don&apos;t recognise.</p>
        <DataSection what="tokens" fallback={<Skeleton className="h-24 w-full rounded-panel" />}>
          <Tokens />
        </DataSection>
        {liveMode ? <p className="text-13 text-muted">Run grey login on a computer to create a token for it.</p> : <TokenDialog />}
      </section>

      {!liveMode && (
      <section className="flex flex-wrap items-center justify-between gap-4 rounded-panel border border-border px-5 py-4">
        <div className="flex flex-col gap-1">
          <h2 className="font-semibold">Delete account</h2>
          <span className="text-13 text-muted">Deletes every project, deployment, log and domain. This can&apos;t be undone.</span>
        </div>
        <ActionButton variant="destructive" message="Confirmation email sent" description="Follow the link in it to delete your account">
          Delete account
        </ActionButton>
      </section>
      )}
    </div>
  );
}

async function Account({ team: teamSlug }: { team: Promise<string> }) {
  const team = await teamSlug;
  const [me, info] = await Promise.all([api.me(), api.getTeam(team)]);
  const field = "h-10 rounded-input border border-border-strong bg-surface px-3 text-14";
  return (
    <section className="flex flex-col gap-4 rounded-panel border border-border p-5">
      <SectionHeader title="Account" />
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <label className="flex flex-col gap-1.5">
          <span className="text-12 text-muted">Name</span>
          <input defaultValue={me.name} readOnly={liveMode} className={field} />
        </label>
        <label className="flex flex-col gap-1.5">
          <span className="text-12 text-muted">Email</span>
          <input type="email" defaultValue={me.email} readOnly={liveMode} className={field} />
        </label>
        <label className="flex flex-col gap-1.5 sm:col-span-2">
          <span className="text-12 text-muted">URL slug · used in default domains</span>
          <input defaultValue={info.slug} readOnly={liveMode} className={`${field} font-mono`} />
        </label>
      </div>
      {!liveMode && (
        <ActionButton variant="primary" className="self-start" message="Account saved">
          Save
        </ActionButton>
      )}
    </section>
  );
}

async function Tokens() {
  const tokens = await api.listTokens();
  return (
          <Table minWidth={0}>
            <thead>
              <tr>
                <Th>Name</Th>
                <Th className="max-sm:hidden">Created</Th>
                <Th>Last used</Th>
                <Th>
                  <span className="sr-only">Actions</span>
                </Th>
              </tr>
            </thead>
            <tbody>
              {tokens.map((t) => (
                <tr key={t.id ?? t.name}>
                  <Td mono>{t.name}</Td>
                  <Td className="text-muted max-sm:hidden">{t.created}</Td>
                  <Td className="text-muted">{t.lastUsed}</Td>
                  <Td align="right">
                    {liveMode && t.id && <RevokeToken id={t.id} name={t.name} />}
                    {!liveMode && (
                      <ActionButton size="sm" variant="ghost" message={`Revoked ${t.name}`} description="Requests using it now get 401">
                        Revoke
                      </ActionButton>
                    )}
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
  );
}

async function GitHub({ team: teamSlug }: { team: Promise<string> }) {
  const team = await teamSlug;
  return <GitHubApp team={team} app={await githubApp(team)} />;
}
