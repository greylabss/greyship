// Response shapes for the /v1 API. They follow docs/architecture.md §3; when the
// OpenAPI spec exists these become generated types and screens stay unchanged.

export type Team = {
  slug: string;
  name: string;
};

export type User = {
  id: string;
  name: string;
  email: string;
  initials: string;
};

export type ServiceType = "web" | "worker" | "cron";
export type Visibility = "public" | "private";

export type ServiceState =
  | { kind: "running"; instances: number }
  | { kind: "sleeping"; queued?: number }
  | { kind: "building" }
  | { kind: "crashing"; reason: string }
  | { kind: "failed"; reason: string }
  | { kind: "scheduled"; nextRun: string; lastRun?: CronRun };

export type CronRun = { at: string; exitCode: number; duration: string };

export type Service = {
  name: string;
  type: ServiceType;
  visibility: Visibility;
  path: string;
  url: string | null;
  state: ServiceState;
  alwaysOn: boolean;
  lastDeploy: string;
  requests24h: number | null;
  memoryMb: number;
  calls: string[];
  scaling: Scaling;
  schedule?: string;
  queue?: { name: string; ready: number; inFlight: number; dead: number };
  binarySize: string;
};

export type Scaling = {
  minInstances: number;
  maxInstances: number;
  concurrency: number;
  graceSeconds: number;
  timeoutSeconds: number;
};

export type DeploymentStatus = "queued" | "building" | "ready" | "failed" | "superseded";
export type Environment = "production" | "preview";

export type Deployment = {
  id: string;
  sha: string;
  message: string;
  branch: string;
  author: string;
  env: Environment;
  status: DeploymentStatus;
  source: "cli" | "dashboard" | "github";
  duration: string | null;
  createdAt: string;
  ago: string;
  failure?: string;
  previewUrl?: string;
  rollbackOf?: string;
};

export type ProjectHealth = "ready" | "building" | "failed" | "sleeping" | "crashing";

export type ProjectSummary = {
  slug: string;
  repo: string;
  url: string | null;
  health: ProjectHealth;
  lastDeploy: { message: string; ago: string; branch: string; sha: string; failure?: string };
  serviceCount: number;
  requests24h: number;
  requestsSeries: number[];
};

// What a user's running apps hold next to the limits their instance admin set. A null max means no limit.
export type ResourceUsage = {
  memoryUsedMb: number;
  memoryMaxMb: number | null;
  cpuUsedCores: number;
  cpuMaxCores: number | null;
  concurrentBuildsMax: number | null;
};

export type ActivityItem = Deployment & { project: string };

export type DomainStatus = "active" | "dns" | "verifying";

export type Domain = {
  hostname: string;
  kind: "default" | "custom";
  status: DomainStatus;
  target: string;
  record?: { type: "CNAME" | "A"; name: string; value: string };
};

export type EnvVar = { key: string; service: string | null; value: string; updated: string };

export type ProjectDetail = ProjectSummary & {
  id?: string; // the API's project id, when the data is real
  github?: { fullName: string; branch: string; public: boolean }; // the connected repository, if any
  goVersion: string;
  region: string;
  services: Service[];
  deployments: Deployment[];
  domains: Domain[];
  env: EnvVar[];
  excludes: { service: string; key: string }[];
};

export type BuildLine = { t: string; text: string; tone?: "dim" | "ok" | "error" | "warn" };

export type DeploymentDetail = Deployment & { url?: string; project: string; goVersion: string; built: { name: string; size: string }[]; log: BuildLine[] };

export type LogLevel = "INFO" | "WARN" | "ERROR";
export type LogLine = { id: string; t: string; service: string; instance: string; level: LogLevel; msg: string; fields: Record<string, string | number> };

export type ChartEvent = { at: number; kind: "deploy" | "wake" | "restart" | "crash"; label: string };
export type Series = { label: string; unit: string; values: number[]; events: ChartEvent[] };

export type RuntimeInfo = {
  service: string;
  stats: { label: string; value: string; note: string }[];
  goroutines: Series;
  heap: Series;
  leak?: { from: number; to: number; since: string; blocked: number; site: string };
  flame: { name: string; pct: number; depth: number; offset: number; hot?: boolean }[];
  tuning: { gomaxprocs: string; gomemlimit: string; pgo: boolean };
};
