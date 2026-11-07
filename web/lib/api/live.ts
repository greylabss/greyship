import "server-only";
import { cookies, headers } from "next/headers";
import { toLogLine, type ApiLogLine } from "./loglines";
import type {
  ActivityItem,
  BuildLine,
  Deployment,
  DeploymentDetail,
  DeploymentStatus,
  LogLine,
  ProjectDetail,
  ProjectHealth,
  ProjectSummary,
  ResourceUsage,
  Service,
  Team,
  User,
} from "./types";

// Phase 2: the screens' data comes from the real /v1 API, called from the server with the visitor's session cookie.
// Shapes the API doesn't have yet (usage, metrics, runtime, repos) are reported as unavailable, never faked.

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

const base = (process.env.GREYSHIP_API_URL ?? "").replace(/\/$/, "");

async function get(path: string): Promise<Response> {
  const jar = await cookies();
  const hdrs = await headers();
  const cookie = jar
    .getAll()
    .filter((c) => c.name === "__Host-gs_session" || c.name === "gs_session")
    .map((c) => `${c.name}=${c.value}`)
    .join("; ");
  const res = await fetch(base + path, {
    headers: {
      cookie,
      "x-greyship-client-ip": hdrs.get("x-forwarded-for")?.split(",")[0]?.trim() ?? "",
      "x-greyship-proxy-secret": process.env.GREYSHIP_PROXY_SECRET ?? "",
    },
    cache: "no-store",
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new ApiError(body?.error?.message ?? `The API answered ${res.status}`, res.status);
  }
  return res;
}

async function json<T>(path: string): Promise<T> {
  return (await get(path)).json() as Promise<T>;
}

// adminJson reads the operator-only /v1/admin endpoints (decisions.md 048); anyone else gets 404 from the API.
export async function adminJson<T>(path: string): Promise<T> {
  return json<T>(path);
}

async function ndjson<T>(path: string): Promise<T[]> {
  const text = await (await get(path)).text();
  return text
    .split("\n")
    .filter(Boolean)
    .map((l) => JSON.parse(l) as T);
}

type ApiUser = { id: string; email: string; name?: string; teams: { id: string; slug: string; name: string; role: string }[] };
type ApiProject = { id: string; team: string; name: string; slug: string; created_at: string };
type ApiDeployment = {
  id: string;
  project: string;
  source: Deployment["source"];
  status: string;
  error: string | null;
  env: string;
  commit_sha: string | null;
  build_mode: string;
  rollback_of: string | null;
  created_at: string;
  finished_at: string | null;
};
type ApiService = {
  name: string;
  type: Service["type"];
  path: string;
  url: string | null;
  state: "running" | "starting" | "sleeping";
  instances: number;
  released_at: string | null;
  memory_mb: number;
  max_instances: number;
  concurrency: number;
  grace_seconds: number;
};

export function ago(iso: string | null): string {
  if (!iso) return "never";
  const s = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000));
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

function duration(from: string, to: string | null): string | null {
  if (!to) return null;
  const s = Math.round((new Date(to).getTime() - new Date(from).getTime()) / 1000);
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
}

const statuses: Record<string, DeploymentStatus> = {
  uploading: "queued",
  queued: "queued",
  building: "building",
  built: "building",
  releasing: "building",
  ready: "ready",
  failed: "failed",
  superseded: "superseded",
};

function toDeployment(d: ApiDeployment): Deployment {
  return {
    id: d.id,
    sha: (d.commit_sha ?? d.id).slice(0, 7),
    message: d.rollback_of
      ? `Redeploy of ${(d.commit_sha ?? d.rollback_of).slice(0, 7)}`
      : d.build_mode === "local"
        ? "Built on your machine with grey deploy"
        : "Built on Greyship",
    branch: "",
    author: "",
    env: "production",
    status: statuses[d.status] ?? "queued",
    source: d.source,
    duration: duration(d.created_at, d.finished_at),
    createdAt: new Date(d.created_at).toLocaleString("en-GB", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", timeZone: "UTC" }) + " UTC",
    ago: ago(d.created_at),
    rollbackOf: d.rollback_of ?? undefined,
    failure: d.error ?? undefined,
  };
}

function toService(s: ApiService): Service {
  const state: Service["state"] = s.state === "running" ? { kind: "running", instances: s.instances } : { kind: "sleeping" };
  return {
    name: s.name,
    type: s.type,
    visibility: s.url ? "public" : "private",
    path: s.path,
    url: s.url ? s.url.replace(/^https?:\/\//, "") : null,
    state,
    alwaysOn: false,
    lastDeploy: ago(s.released_at),
    requests24h: null,
    memoryMb: s.memory_mb,
    calls: [],
    scaling: { minInstances: 0, maxInstances: s.max_instances, concurrency: s.concurrency, graceSeconds: s.grace_seconds, timeoutSeconds: 30 },
    binarySize: "",
  };
}

function health(services: Service[], latest?: Deployment): ProjectHealth {
  if (latest?.status === "failed") return "failed";
  if (latest && (latest.status === "queued" || latest.status === "building")) return "building";
  return services.some((s) => s.state.kind === "running") ? "ready" : "sleeping";
}

async function projectId(team: string, slug: string): Promise<string | null> {
  try {
    return (await json<{ project: ApiProject }>(`/v1/teams/${encodeURIComponent(team)}/projects/${encodeURIComponent(slug)}`)).project.id;
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) return null;
    throw e;
  }
}

async function summary(p: ApiProject): Promise<ProjectSummary & { services: Service[]; deployments: Deployment[] }> {
  const [svc, deps] = await Promise.all([
    json<{ services: ApiService[] }>(`/v1/projects/${p.id}/services`),
    json<{ deployments: ApiDeployment[] }>(`/v1/projects/${p.id}/deployments?limit=20`),
  ]);
  const services = svc.services.map(toService);
  const deployments = deps.deployments.map(toDeployment);
  const latest = deployments[0];
  return {
    slug: p.slug,
    repo: "",
    url: services.find((s) => s.url)?.url ?? null,
    health: health(services, latest),
    lastDeploy: latest
      ? { message: latest.message, ago: latest.ago, branch: latest.branch, sha: latest.sha, failure: latest.failure }
      : { message: "Not deployed yet", ago: ago(p.created_at), branch: "", sha: "" },
    serviceCount: services.length,
    requests24h: 0,
    requestsSeries: [],
    services,
    deployments,
  };
}

export type TokenRow = { id?: string; name: string; created: string; lastUsed: string };

export type GitHubRepo = { full_name: string; default_branch: string; private: boolean };

// The team's GitHub state for the import screen: its repos, or that the app isn't installed (with the link to do
// so), or that GitHub deploys aren't set up on this Greyship at all.
export type GitHubState = { kind: "repos"; repos: GitHubRepo[]; installURL: string } | { kind: "install"; installURL: string } | { kind: "off" };

export type GitHubPreview = {
  branch: string;
  commit: string;
  go_version?: string;
  services?: { name: string; type: string; path: string; reason: string }[];
  release?: { path: string; reason: string } | null;
  skipped?: { path: string; reason: string }[] | null;
  env_example?: string[];
  problem?: { message: string; fix: string };
};

// githubPreview is what deploying the project's connected branch would run, before its first deploy.
export async function githubPreview(projectId: string): Promise<GitHubPreview> {
  return json<GitHubPreview>(`/v1/projects/${projectId}/github/preview`);
}

export async function githubState(team: string): Promise<GitHubState> {
  const t = encodeURIComponent(team);
  let installURL: string;
  try {
    installURL = (await json<{ url: string }>(`/v1/teams/${t}/github/install`)).url;
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) return { kind: "off" };
    throw e;
  }
  try {
    const { repos } = await json<{ repos: GitHubRepo[] }>(`/v1/teams/${t}/github/repos`);
    return { kind: "repos", repos, installURL };
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) return { kind: "install", installURL };
    throw e;
  }
}

export const live = {
  async setupNeeded(): Promise<boolean> {
    return (await json<{ setup_needed: boolean }>("/v1/auth/setup")).setup_needed;
  },

  async myUsage(): Promise<ResourceUsage> {
    const u = await json<{ memory_used_mb: number; memory_max_mb: number | null; cpu_used_cores: number; cpu_max_cores: number | null; concurrent_builds_max: number | null }>("/v1/me/usage");
    return { memoryUsedMb: u.memory_used_mb, memoryMaxMb: u.memory_max_mb, cpuUsedCores: u.cpu_used_cores, cpuMaxCores: u.cpu_max_cores, concurrentBuildsMax: u.concurrent_builds_max };
  },

  async listTokens(): Promise<TokenRow[]> {
    type ApiToken = { id: string; name: string; created_at: string; last_used_at: string | null };
    const { tokens } = await json<{ tokens: ApiToken[] }>("/v1/tokens");
    return tokens.map((t) => ({ id: t.id, name: t.name, created: ago(t.created_at), lastUsed: t.last_used_at ? ago(t.last_used_at) : "never" }));
  },

  async me(): Promise<User> {
    const { user } = await json<{ user: ApiUser }>("/v1/me");
    const name = user.name || user.email.split("@")[0];
    return { id: user.id, name, email: user.email, initials: name.slice(0, 1).toUpperCase() };
  },

  async teams(): Promise<string[]> {
    const { user } = await json<{ user: ApiUser }>("/v1/me");
    return user.teams.map((t) => t.slug);
  },

  async getTeam(slug: string): Promise<Team> {
    const { user } = await json<{ user: ApiUser }>("/v1/me");
    const t = user.teams.find((x) => x.slug === slug);
    if (!t) throw new ApiError("No such team.", 404);
    return { slug: t.slug, name: t.name };
  },

  async listProjects(team: string): Promise<ProjectSummary[]> {
    const { projects } = await json<{ projects: ApiProject[] }>(`/v1/teams/${encodeURIComponent(team)}/projects`);
    return Promise.all(projects.map(summary));
  },

  async listActivity(team: string): Promise<ActivityItem[]> {
    const { projects } = await json<{ projects: ApiProject[] }>(`/v1/teams/${encodeURIComponent(team)}/projects`);
    const all = await Promise.all(
      projects.map(async (p) => {
        const { deployments } = await json<{ deployments: ApiDeployment[] }>(`/v1/projects/${p.id}/deployments?limit=10`);
        return deployments.map((d) => ({ ...toDeployment(d), project: p.slug }));
      }),
    );
    return all.flat().sort((a, b) => b.createdAt.localeCompare(a.createdAt));
  },

  async getProject(team: string, slug: string): Promise<ProjectDetail | null> {
    const id = await projectId(team, slug);
    if (!id) return null;
    const { project } = await json<{ project: ApiProject }>(`/v1/projects/${id}`);
    const [s, env, gh] = await Promise.all([
      summary(project),
      json<{ vars: { key: string; updated_at: string }[] }>(`/v1/projects/${id}/env`),
      json<{ repo: { full_name: string; branch: string; public: boolean } | null }>(`/v1/projects/${id}/github`).catch(() => ({ repo: null })),
    ]);
    if (gh.repo) s.repo = gh.repo.full_name;
    const github = gh.repo ? { fullName: gh.repo.full_name, branch: gh.repo.branch, public: gh.repo.public } : undefined;
    return {
      ...s,
      id,
      github,
      goVersion: "",
      region: "",
      domains: s.services
        .filter((x) => x.url)
        .map((x) => ({ hostname: x.url!, kind: "default" as const, status: "active" as const, target: x.name })),
      env: env.vars.map((v) => ({ key: v.key, service: null, value: "", updated: ago(v.updated_at) })),
      excludes: [],
    };
  },

  async getDeployment(team: string, slug: string, id: string): Promise<DeploymentDetail | null> {
    const pid = await projectId(team, slug);
    if (!pid) return null;
    let d: ApiDeployment;
    let urls: { service: string; url: string }[] = [];
    try {
      const got = await json<{ deployment: ApiDeployment; urls: { service: string; url: string }[] | null }>(`/v1/deployments/${encodeURIComponent(id)}`);
      d = got.deployment;
      urls = got.urls ?? [];
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) return null;
      throw e;
    }
    if (d.project !== slug) return null;
    type Entry = { line?: { stream: string; line: string; ts: string }; end?: { artifacts: { service: string; size_bytes: number }[] } };
    const entries = await ndjson<Entry>(`/v1/deployments/${encodeURIComponent(id)}/logs`);
    const log: BuildLine[] = entries
      .filter((e) => e.line)
      .map((e) => ({ t: new Date(e.line!.ts).toISOString().slice(11, 19), text: e.line!.line, tone: e.line!.stream === "system" ? "dim" : undefined }));
    const built = (entries.find((e) => e.end)?.end?.artifacts ?? []).map((a) => ({ name: a.service, size: `${(a.size_bytes / (1 << 20)).toFixed(1)} MB` }));
    const url = urls[0]?.url.replace(/^https?:\/\//, "");
    return { ...toDeployment(d), url, project: slug, goVersion: "", built, log };
  },

  async listLogs(team: string, slug: string): Promise<LogLine[]> {
    const id = await projectId(team, slug);
    if (!id) return [];
    const rows = await ndjson<ApiLogLine>(`/v1/projects/${id}/logs?tail=200`);
    return rows.filter((r) => r.id).map(toLogLine);
  },
};
