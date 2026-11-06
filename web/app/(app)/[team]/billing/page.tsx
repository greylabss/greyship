import type { Metadata } from "next";
import { UpgradeDialog } from "@/components/team/upgrade-dialog";
import { ActionButton } from "@/components/ui/action-button";
import { DataSection } from "@/components/ui/data-section";
import { icons } from "@/components/ui/icons";
import { PageHeader, SectionHeader } from "@/components/ui/page-header";
import { Skeleton, SkeletonRows } from "@/components/ui/skeleton";
import { Table, Td, Th } from "@/components/ui/table";
import { Arrives } from "@/components/ui/arrives";
import { api, available } from "@/lib/api";
import type { Plan } from "@/lib/api/types";
import { dollars } from "@/lib/format";

export const metadata: Metadata = { title: "Billing" };

const planInfo: Record<Plan, { name: string; price: string; pitch: string }> = {
  free: {
    name: "Free",
    price: "$0 / month",
    pitch: "Upgrade to Pro for pprof, alerts, preview deploys and bigger instances. $8 includes $8 of usage.",
  },
  pro: { name: "Pro", price: "$8 / month", pitch: "$8 of usage included, then pay as you go." },
  team: { name: "Team", price: "$25 / user / month", pitch: "$20 of usage per seat, team features." },
  unlimited: { name: "Unlimited", price: "—", pitch: "This Greyship has no plans or limits." },
};

export default function BillingPage({ params }: PageProps<"/[team]/billing">) {
  const team = params.then((p) => p.team);
  return (
    <div className="flex flex-col gap-10">
      <PageHeader title="Billing" />
      <DataSection what="billing" fallback={<Skeleton className="h-44 w-full rounded-panel" />}>
        <PlanAndCap team={team} />
      </DataSection>
      <div className="grid grid-cols-1 gap-10 lg:grid-cols-2">
        {available.usage && <Payment />}
        <DataSection what="invoices" fallback={<SkeletonRows rows={2} />}>
          <Invoices team={team} />
        </DataSection>
      </div>
    </div>
  );
}

async function PlanAndCap({ team: teamSlug }: { team: Promise<string> }) {
  const team = await teamSlug;
  if (!available.usage) {
    const info = await api.getTeam(team);
    if (info.plan === "unlimited") return <p className="text-13 text-muted">This Greyship has no plans, billing or limits.</p>;
    return <Arrives title="Billing" when="the public beta. Until then Greyship is free" />;
  }
  const [info, usage] = await Promise.all([api.getTeam(team), api.getUsage(team)]);
  const plan = planInfo[info.plan];

  return (
    <div className="flex flex-col gap-4">
      {usage.overCap && (
        <div role="alert" className="flex flex-wrap items-start gap-3 rounded-panel border border-accent px-4 py-4">
          <icons.alert className="mt-0.5 text-accent" />
          <div className="flex min-w-0 flex-1 flex-col gap-1">
            <span className="font-medium">Spend cap reached · services paused</span>
            <span className="text-13 text-muted">
              You reached your {dollars(info.spendCapCents)} cap on Oct 21. [COPY: over-cap — raise the cap or wait for the reset on{" "}
              {usage.resetsOn}; requests get 503 until then]
            </span>
          </div>
        </div>
      )}

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <section className="flex flex-col gap-4 rounded-panel border border-border p-5">
          <span className="text-13 text-muted">Current plan</span>
          <div className="flex items-baseline gap-3">
            <span className="text-28 font-semibold">{plan.name}</span>
            <span className="text-muted">{plan.price}</span>
          </div>
          <p className="text-13 text-muted">{plan.pitch}</p>
          {info.plan === "free" && <UpgradeDialog />}
        </section>

        <section className="flex flex-col gap-4 rounded-panel border border-border p-5">
          <span className="text-13 text-muted">Spend cap</span>
          <div className="flex items-baseline gap-3">
            <span className="text-28 font-semibold tabular">{dollars(info.spendCapCents).replace(".00", "")}</span>
            <span className="text-muted">per month</span>
          </div>
          <p className="text-13 text-muted">If you reach it, services pause and we email you. No surprise bills.</p>
          <form className="flex flex-wrap items-end gap-2">
            <label className="flex flex-col gap-1.5">
              <span className="text-12 text-muted">Spend cap in dollars</span>
              <input
                inputMode="decimal"
                defaultValue={info.spendCapCents / 100}
                className="h-10 w-32 rounded-input border border-border-strong bg-surface px-3 font-mono text-14 tabular"
              />
            </label>
            <ActionButton message="Spend cap updated" description="Services pause and we email you if you reach it">Update cap</ActionButton>
          </form>
        </section>
      </div>
    </div>
  );
}

function Payment() {
  const methods = [
    { name: "Pay in naira", detail: "Card, bank transfer or USSD via Paystack", action: "Set up Paystack" },
    { name: "Pay in dollars", detail: "International cards via Stripe", action: "Set up Stripe" },
    { name: "Prepaid credits", detail: "Top up once, no recurring charge · balance $0.00", action: "Add credits" },
  ];
  return (
    <section className="flex flex-col gap-3">
      <SectionHeader title="Payment" />
      <div className="divide-y divide-border rounded-panel border border-border">
        {methods.map((m) => (
          <div key={m.name} className="flex flex-wrap items-center justify-between gap-3 px-4 py-3.5">
            <div className="flex min-w-0 flex-col">
              <span className="font-medium">{m.name}</span>
              <span className="text-13 text-muted">{m.detail}</span>
            </div>
            <ActionButton size="sm" message={`${m.action}: redirecting`} description="Opens the provider to save a payment method">
              {m.action}
            </ActionButton>
          </div>
        ))}
      </div>
    </section>
  );
}

async function Invoices({ team: teamSlug }: { team: Promise<string> }) {
  const team = await teamSlug;
  if (!available.usage) return null;
  const invoices = await api.listInvoices(team);
  return (
    <section className="flex flex-col gap-3">
      <SectionHeader title="Invoices" />
      {invoices.length === 0 ? (
        <p className="rounded-panel border border-dashed border-border-strong px-4 py-6 text-13 text-muted">
          Invoices appear here once you&apos;re on a paid plan.
        </p>
      ) : (
        <Table minWidth={420}>
          <thead>
            <tr>
              <Th>Period</Th>
              <Th>Status</Th>
              <Th align="right">Amount</Th>
              <Th>
                <span className="sr-only">Download</span>
              </Th>
            </tr>
          </thead>
          <tbody>
            {invoices.map((inv) => (
              <tr key={inv.id}>
                <Td>{inv.period}</Td>
                <Td className="text-muted">{inv.status === "paid" ? `Paid · ${inv.provider === "paystack" ? "Paystack" : "Stripe"}` : "Open"}</Td>
                <Td align="right" mono>
                  {dollars(inv.amountCents)}
                </Td>
                <Td align="right">
                  <ActionButton size="sm" variant="ghost" message="Downloading invoice" description={`${inv.id}.pdf`}>
                    Download PDF
                  </ActionButton>
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </section>
  );
}
