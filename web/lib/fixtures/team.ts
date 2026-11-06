import type { ActivityItem, Invoice, ProjectSummary, Team, Usage, User } from "@/lib/api/types";
import type { Scenario } from "./scenarios";

export const me: User = {
  id: "usr_01",
  name: "Haki",
  email: "haki@acme.dev",
  initials: "H",
  betaTester: true,
};

export function team(scenario: Scenario): Team {
  const paid = scenario === "pro";
  return {
    slug: "haki",
    name: "haki",
    plan: paid ? "pro" : "free",
    spendCapCents: paid ? 2500 : 1000,
  };
}

const acmeSeries = [1.2, 1.4, 1.1, 0.9, 0.7, 0.6, 0.8, 1.5, 2.4, 3.1, 3.4, 3.2, 2.9, 3.3, 3.6, 3.1, 2.8, 2.6, 2.4, 2.2, 1.9, 1.7, 1.5, 1.3];
const botSeries = [0, 0, 0, 0.1, 0, 0, 0.2, 0.4, 0.1, 0, 0, 0.3, 0.5, 0.2, 0, 0, 0.1, 0.6, 0.3, 0, 0, 0.1, 0, 0];
const paySeries = [0.3, 0.3, 0.2, 0.2, 0.1, 0.1, 0.2, 0.4, 0.6, 0.7, 0.8, 0.7, 0.6, 0.7, 0.8, 0.7, 0.5, 0.5, 0.4, 0.4, 0.3, 0.3, 0.3, 0.2];

export function projects(scenario: Scenario): ProjectSummary[] {
  if (scenario === "empty") return [];

  const acme: ProjectSummary = {
    slug: "acme-api",
    repo: "haki/acme-api",
    url: "api.acme.com",
    health: "ready",
    lastDeploy: { message: "Add rate limiting to /v1/orders", ago: "2h ago", branch: "main", sha: "7f3a9c2" },
    serviceCount: 4,
    requests24h: 48_200,
    requestsSeries: acmeSeries,
    spendCents: 31,
  };
  const bot: ProjectSummary = {
    slug: "telegram-bot",
    repo: "haki/telegram-bot",
    url: "bot-telegram-bot.greyship.app",
    health: "sleeping",
    lastDeploy: { message: "Handle /start in groups", ago: "yesterday", branch: "main", sha: "e4c19b0" },
    serviceCount: 1,
    requests24h: 312,
    requestsSeries: botSeries,
    spendCents: 0,
  };
  const payments: ProjectSummary = {
    slug: "payments-service",
    repo: "haki/payments-service",
    url: "gateway-payments-service.greyship.app",
    health: "ready",
    lastDeploy: { message: "Retry Paystack webhooks with backoff", ago: "3h ago", branch: "main", sha: "91bd0e7" },
    serviceCount: 2,
    requests24h: 9_400,
    requestsSeries: paySeries,
    spendCents: 12,
  };

  switch (scenario) {
    case "building":
      return [{ ...acme, health: "building", lastDeploy: { message: "New checkout flow", ago: "now", branch: "checkout-v2", sha: "a24d7f0" } }, bot, payments];
    case "failed":
      return [
        acme,
        bot,
        {
          ...payments,
          health: "failed",
          lastDeploy: {
            message: "Bump pgx to v5.7",
            ago: "3h ago",
            branch: "main",
            sha: "c91e04b",
            failure: "Build failed: undefined: pgxpool.ParseConfig in internal/db/pool.go:17",
          },
        },
      ];
    case "sleeping":
      return [
        { ...acme, health: "sleeping", requests24h: 0, requestsSeries: acmeSeries.map(() => 0), spendCents: 0 },
        bot,
        { ...payments, health: "sleeping", requests24h: 0, requestsSeries: paySeries.map(() => 0), spendCents: 0 },
      ];
    case "crashing":
      return [
        {
          ...acme,
          health: "crashing",
          lastDeploy: {
            message: "Add rate limiting to /v1/orders",
            ago: "2h ago",
            branch: "main",
            sha: "7f3a9c2",
            failure: "worker crashed 5 times in 5 min: panic: assignment to entry in nil map",
          },
        },
        bot,
        payments,
      ];
    default:
      return [acme, bot, payments];
  }
}

export function usage(scenario: Scenario): Usage {
  if (scenario === "empty") {
    return {
      period: "October",
      resetsOn: "Nov 1",
      meters: [
        { label: "Compute", unit: "vCPU-h", used: 0, included: 50 },
        { label: "Memory", unit: "GB-h", used: 0, included: 100 },
        { label: "Requests", unit: "requests", used: 0, included: 100_000 },
        { label: "Egress", unit: "GB", used: 0, included: 5 },
      ],
      spendCents: 0,
      projectedCents: 0,
      byProject: [],
      overCap: false,
      creditsCents: 0,
    };
  }
  const overCap = scenario === "over-cap";
  return {
    period: "October",
    resetsOn: "Nov 1",
    meters: [
      { label: "Compute", unit: "vCPU-h", used: overCap ? 50 : 6.1, included: 50 },
      { label: "Memory", unit: "GB-h", used: overCap ? 84.2 : 9.4, included: 100 },
      { label: "Requests", unit: "requests", used: overCap ? 100_000 : 58_000, included: 100_000 },
      { label: "Egress", unit: "GB", used: overCap ? 3.9 : 0.8, included: 5 },
    ],
    spendCents: overCap ? 1000 : 43,
    projectedCents: overCap ? 1000 : 61,
    byProject: [
      { slug: "acme-api", spendCents: overCap ? 720 : 31 },
      { slug: "payments-service", spendCents: overCap ? 280 : 12 },
      { slug: "telegram-bot", spendCents: 0 },
    ],
    overCap,
    creditsCents: 0,
  };
}

export function invoices(scenario: Scenario): Invoice[] {
  if (scenario !== "pro") return [];
  return [
    { id: "inv_2026_09", period: "September 2026", amountCents: 800, status: "paid", provider: "paystack" },
    { id: "inv_2026_08", period: "August 2026", amountCents: 912, status: "paid", provider: "paystack" },
  ];
}

export function activity(scenario: Scenario): ActivityItem[] {
  if (scenario === "empty") return [];
  const base = (d: Partial<ActivityItem> & Pick<ActivityItem, "project" | "sha" | "message" | "ago">): ActivityItem => ({
    id: `dpl_${d.sha}`,
    branch: "main",
    author: "haki",
    env: "production",
    status: "ready",
    source: "github",
    duration: "8.3s",
    createdAt: "",
    ...d,
  });
  const items: ActivityItem[] = [
    base({ project: "acme-api", sha: "7f3a9c2", message: "Add rate limiting to /v1/orders", ago: "2h ago", source: "cli" }),
    base({ project: "payments-service", sha: "91bd0e7", message: "Retry Paystack webhooks with backoff", ago: "3h ago", duration: "6.1s" }),
    base({ project: "acme-api", sha: "a24d7f0", message: "New checkout flow", ago: "5h ago", branch: "checkout-v2", env: "preview", duration: "7.4s" }),
    base({ project: "telegram-bot", sha: "e4c19b0", message: "Handle /start in groups", ago: "yesterday", duration: "4.9s" }),
    base({ project: "acme-api", sha: "3b8e611", message: "Cache product lookups", ago: "yesterday", duration: "7.9s" }),
  ];
  if (scenario === "building") items.unshift(base({ project: "acme-api", sha: "d02f5aa", message: "New checkout flow: tax lines", ago: "now", branch: "checkout-v2", env: "preview", status: "building", duration: null }));
  if (scenario === "failed") items.unshift(base({ project: "payments-service", sha: "c91e04b", message: "Bump pgx to v5.7", ago: "3h ago", status: "failed", duration: "4.1s", failure: "undefined: pgxpool.ParseConfig in internal/db/pool.go:17" }));
  return items;
}
