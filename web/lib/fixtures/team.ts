import type { ActivityItem, ProjectSummary, ResourceUsage, Team, User } from "@/lib/api/types";
import type { Scenario } from "./scenarios";

export const me: User = {
  id: "usr_01",
  name: "Haki",
  email: "haki@acme.dev",
  initials: "H",
};

export function team(): Team {
  return { slug: "haki", name: "haki" };
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
        { ...acme, health: "sleeping", requests24h: 0, requestsSeries: acmeSeries.map(() => 0) },
        bot,
        { ...payments, health: "sleeping", requests24h: 0, requestsSeries: paySeries.map(() => 0) },
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

export function resourceUsage(scenario: Scenario): ResourceUsage {
  if (scenario === "empty") return { memoryUsedMb: 0, memoryMaxMb: 1024, cpuUsedCores: 0, cpuMaxCores: 2, concurrentBuildsMax: 1 };
  return { memoryUsedMb: 384, memoryMaxMb: 1024, cpuUsedCores: 0.75, cpuMaxCores: 2, concurrentBuildsMax: 1 };
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
