import { cookies } from "next/headers";
import { live } from "./live";
import { parseScenario, scenarioCookie, type Scenario } from "@/lib/fixtures/scenarios";
import * as projectFixtures from "@/lib/fixtures/project";
import * as teamFixtures from "@/lib/fixtures/team";

// With GREYSHIP_API_URL set, screens read the real /v1 API (lib/api/live.ts). Without it they read fixtures for the
// scenario chosen in the dev toggle, so the dashboard can still be designed without a running API.

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

async function scenario(): Promise<Scenario> {
  const store = await cookies();
  return parseScenario(store.get(scenarioCookie)?.value);
}

async function respond<T>(build: (s: Scenario) => T): Promise<T> {
  const s = await scenario();
  if (s === "error") throw new ApiError("api.greyship.app returned 503: control plane unavailable", 503);
  await new Promise((resolve) => setTimeout(resolve, s === "slow" ? 2500 : 60));
  return build(s);
}

// Fixtures hold one team ("haki"); slug arguments mirror the real /v1 paths.
const fixtureTeam = "haki";
const fixtures = {
  me: () => respond(() => teamFixtures.me),
  getTeam: (slug: string) => respond((s) => ({ ...teamFixtures.team(s), slug })),
  listProjects: (_team: string) => respond(teamFixtures.projects),
  getUsage: (_team: string) => respond(teamFixtures.usage),
  listInvoices: (_team: string) => respond(teamFixtures.invoices),
  listActivity: (_team: string) => respond(teamFixtures.activity),
  getProject: (_team: string, project: string) => respond((s) => projectFixtures.projectDetail(project, s)),
  getDeployment: (_team: string, project: string, id: string) =>
    respond((s) => {
      const p = projectFixtures.projectDetail(project, s);
      return p ? projectFixtures.deploymentDetail(p, id) : null;
    }),
  listLogs: (_team: string, _project: string) => respond(projectFixtures.logs),
  getMetrics: (_team: string, _project: string, _service: string) => respond(() => projectFixtures.metrics()),
  getRuntime: (_team: string, _project: string, _service: string) => respond(projectFixtures.runtime),
  listRepos: (_team: string) => respond(() => projectFixtures.importable),
  detectServices: (_team: string, repo: string) => respond(() => projectFixtures.detectServices(repo)),
};

export const liveMode = (process.env.GREYSHIP_API_URL ?? "") !== "";

// What the real API can answer yet; screens show "arrives with …" for the rest instead of invented numbers.
export const available = {
  usage: !liveMode,
  metrics: !liveMode,
  runtime: !liveMode,
  repos: !liveMode,
};

const fixtureTokens = [
  { name: "github-actions", created: "Oct 12", lastUsed: "2h ago" },
  { name: "laptop · grey login", created: "Oct 3", lastUsed: "now" },
];

export const api: typeof fixtures & { teams: () => Promise<string[]>; listTokens: typeof live.listTokens; buildUsage: typeof live.buildUsage } = liveMode
  ? { ...fixtures, ...live }
  : {
      ...fixtures,
      teams: async () => [fixtureTeam],
      listTokens: () => respond(() => fixtureTokens),
      buildUsage: () => respond(() => ({ usedMinutes: 12, includedMinutes: 100, resetsOn: "1 December" })),
    };
