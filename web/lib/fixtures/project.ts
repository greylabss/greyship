import type {
  BuildLine,
  Deployment,
  DeploymentDetail,
  Domain,
  EnvVar,
  LogLine,
  ProjectDetail,
  RuntimeInfo,
  Scaling,
  Series,
  Service,
} from "@/lib/api/types";
import type { Scenario } from "./scenarios";
import { projects } from "./team";

const freeScaling: Scaling = { minInstances: 0, maxInstances: 2, concurrency: 100, graceSeconds: 30, timeoutSeconds: 30 };

function service(s: Partial<Service> & Pick<Service, "name" | "type" | "path" | "state">): Service {
  return {
    visibility: "public",
    url: null,
    alwaysOn: false,
    lastDeploy: "2h ago",
    requests24h: null,
    memoryMb: 128,
    calls: [],
    scaling: freeScaling,
    binarySize: "9.1 MB",
    ...s,
  };
}

function deployment(d: Partial<Deployment> & Pick<Deployment, "sha" | "message" | "ago">): Deployment {
  return {
    id: d.sha,
    branch: "main",
    author: "haki",
    env: "production",
    status: "ready",
    source: "github",
    duration: "8.3s",
    createdAt: "Oct 24, 10:42",
    ...d,
  };
}

function acme(scenario: Scenario): Omit<ProjectDetail, keyof ReturnType<typeof projects>[number]> {
  const crashing = scenario === "crashing";
  const sleeping = scenario === "sleeping";
  const services: Service[] = [
    service({
      name: "api",
      type: "web",
      path: "./cmd/api",
      url: "api-acme-api.greyship.app",
      state: sleeping ? { kind: "sleeping" } : { kind: "running", instances: 2 },
      requests24h: 46_903,
      calls: ["orders", "worker"],
      binarySize: "11.2 MB",
    }),
    service({
      name: "orders",
      type: "web",
      visibility: "private",
      path: "./cmd/orders",
      url: "orders.internal",
      state: { kind: "sleeping" },
      requests24h: 1_284,
      binarySize: "8.4 MB",
    }),
    service({
      name: "worker",
      type: "worker",
      path: "./cmd/worker",
      state: crashing
        ? { kind: "crashing", reason: "panic: assignment to entry in nil map · internal/jobs/email.go:41" }
        : sleeping
          ? { kind: "sleeping", queued: 0 }
          : { kind: "running", instances: 1 },
      queue: { name: "emails", ready: crashing ? 37 : sleeping ? 0 : 4, inFlight: crashing ? 0 : 1, dead: crashing ? 3 : 0 },
      binarySize: "9.8 MB",
    }),
    service({
      name: "cron-cleanup",
      type: "cron",
      path: "./cmd/cron-cleanup",
      schedule: "0 3 * * *",
      state: { kind: "scheduled", nextRun: "03:00", lastRun: { at: "today 03:00", exitCode: 0, duration: "2.1s" } },
      binarySize: "7.1 MB",
    }),
  ];

  const deployments: Deployment[] = [
    deployment({ sha: "7f3a9c2", message: "Add rate limiting to /v1/orders", ago: "2h ago", source: "cli" }),
    deployment({ sha: "a24d7f0", message: "New checkout flow", ago: "5h ago", branch: "checkout-v2", env: "preview", duration: "7.4s", previewUrl: "api-checkout-v2-acme-api.greyship.app" }),
    deployment({ sha: "c91e04b", message: "Bump pgx to v5.7", ago: "6h ago", status: "failed", duration: "4.1s", failure: "undefined: pgxpool.ParseConfig in internal/db/pool.go:17" }),
    deployment({ sha: "3b8e611", message: "Cache product lookups", ago: "yesterday", status: "superseded", duration: "7.9s", createdAt: "Oct 23, 16:10" }),
    deployment({ sha: "5d21a9e", message: "Move email sending to worker queue", ago: "2 days ago", status: "superseded", duration: "8.8s", createdAt: "Oct 22, 11:02" }),
    deployment({ sha: "0e7c4b3", message: "Add /healthz", ago: "4 days ago", status: "superseded", duration: "6.6s", createdAt: "Oct 20, 09:15", source: "dashboard" }),
  ];
  if (scenario === "building") {
    deployments.unshift(deployment({ sha: "d02f5aa", message: "New checkout flow: tax lines", ago: "now", branch: "checkout-v2", env: "preview", status: "building", duration: null }));
  }

  const domains: Domain[] = [
    { hostname: "api-acme-api.greyship.app", kind: "default", status: "active", target: "api" },
    { hostname: "api.acme.com", kind: "custom", status: "active", target: "api" },
    {
      hostname: "shop.acme.com",
      kind: "custom",
      status: scenario === "dns" ? "dns" : "active",
      target: "api",
      record: { type: "CNAME", name: "shop", value: "edge.greyship.app" },
    },
  ];

  const env: EnvVar[] = [
    { key: "DATABASE_URL", service: null, value: "postgres://acme:••••@db.acme.internal:5432/acme", updated: "3 days ago" },
    { key: "REDIS_URL", service: null, value: "redis://cache.acme.internal:6379", updated: "3 days ago" },
    { key: "LOG_LEVEL", service: null, value: "info", updated: "last week" },
    { key: "PAYSTACK_SECRET_KEY", service: "api", value: "sk_live_4f9c2e71b0a3", updated: "yesterday" },
    { key: "JWT_SIGNING_KEY", service: "api", value: "c2VjcmV0LXNpZ25pbmcta2V5", updated: "2 weeks ago" },
    { key: "LOG_LEVEL", service: "worker", value: "debug", updated: "2h ago" },
    { key: "QUEUE_CONCURRENCY", service: "worker", value: "8", updated: "2h ago" },
    { key: "SMTP_PASSWORD", service: "worker", value: "smtp-9a8b7c", updated: "last week" },
  ];

  return {
    goVersion: "go1.27",
    region: "Europe · Germany",
    services,
    deployments,
    domains,
    env,
    excludes: [{ service: "cron-cleanup", key: "REDIS_URL" }],
  };
}

function simple(slug: string, scenario: Scenario): Omit<ProjectDetail, keyof ReturnType<typeof projects>[number]> {
  const failed = scenario === "failed" && slug === "payments-service";
  const fresh = !projects("default").some((p) => p.slug === slug);
  const services = fresh
    ? detectServices(slug).map((d) =>
        service({ name: d.name, type: d.type, path: d.path, url: d.type === "web" ? `${d.name}-${slug}.greyship.app` : null, state: d.type === "cron" ? { kind: "scheduled", nextRun: "03:00" } : { kind: "sleeping" }, schedule: d.type === "cron" ? "0 3 * * *" : undefined }),
      )
    : slug === "payments-service"
      ? [
          service({ name: "gateway", type: "web", path: "./cmd/gateway", url: "gateway-payments-service.greyship.app", state: { kind: "running", instances: 1 }, requests24h: 9_400 }),
          service({ name: "webhooks", type: "web", visibility: "private", path: "./cmd/webhooks", url: "webhooks.internal", state: { kind: "sleeping" }, requests24h: 412 }),
        ]
      : [service({ name: slug === "telegram-bot" ? "bot" : "api", type: "web", path: ".", url: `${slug === "telegram-bot" ? "bot" : "api"}-${slug}.greyship.app`, state: { kind: "sleeping" }, requests24h: 312 })];
  const deployments = [
    failed
      ? deployment({ sha: "c91e04b", message: "Bump pgx to v5.7", ago: "3h ago", status: "failed", duration: "4.1s", failure: "undefined: pgxpool.ParseConfig in internal/db/pool.go:17" })
      : deployment({ sha: "91bd0e7", message: "Retry Paystack webhooks with backoff", ago: "3h ago", duration: "6.1s" }),
    deployment({ sha: "e4c19b0", message: "Initial deploy", ago: "last week", status: failed ? "ready" : "superseded", duration: "5.2s" }),
  ];
  return {
    goVersion: "go1.27",
    region: "Europe · Germany",
    services,
    deployments,
    domains: services.filter((s) => s.visibility === "public").map((s) => ({ hostname: s.url!, kind: "default" as const, status: "active" as const, target: s.name })),
    env: [{ key: "DATABASE_URL", service: null, value: "postgres://pay:••••@db.internal:5432/pay", updated: "last week" }],
    excludes: [],
  };
}

export function projectDetail(slug: string, scenario: Scenario): ProjectDetail | null {
  const summary = projects(scenario === "empty" ? "default" : scenario).find((p) => p.slug === slug) ?? newProject(slug);
  if (!summary) return null;
  const extra = slug === "acme-api" ? acme(scenario) : simple(slug, scenario);
  return { ...summary, ...extra };
}

// Any project created in the onboarding demo (including a renamed one) gets one fresh deploy.
function newProject(slug: string) {
  if (!/^[a-z][a-z0-9-]{1,38}$/.test(slug)) return null;
  return {
    slug,
    repo: `haki/${slug}`,
    url: `api-${slug}.greyship.app`,
    health: "ready" as const,
    lastDeploy: { message: "Initial deploy", ago: "just now", branch: "main", sha: "f1e2d3c" },
    serviceCount: 1,
    requests24h: 0,
    requestsSeries: Array(24).fill(0),
    spendCents: 0,
  };
}

export const importable = [
  { name: "acme-api", updated: "Updated 2h ago", mains: 4, imported: true },
  { name: "url-shortener", updated: "Updated last week", mains: 1, imported: false },
  { name: "chat-server", updated: "Updated 2 weeks ago", mains: 2, imported: false },
  { name: "telegram-bot", updated: "Updated yesterday", mains: 1, imported: true },
  { name: "payments-service", updated: "Updated 3 days ago", mains: 2, imported: true },
  { name: "portfolio", updated: "Updated last month", mains: 0, imported: false, language: "TypeScript" },
];

export const templates = [
  { slug: "chi-rest-api", name: "Chi REST API", desc: "net/http + chi, health check, JSON handlers" },
  { slug: "gin-postgres-api", name: "Gin API + Postgres", desc: "pgx pool, migrations, env-based config" },
  { slug: "api-worker-cron", name: "API + worker + cron", desc: "Three services from one repo" },
  { slug: "telegram-bot-starter", name: "Telegram bot", desc: "Webhook receiver that sleeps when idle" },
];

export function detectServices(repo: string) {
  if (repo === "api-worker-cron") {
    return [
      { name: "api", path: "./cmd/api", type: "web" as const },
      { name: "worker", path: "./cmd/worker", type: "worker" as const },
      { name: "cron-cleanup", path: "./cmd/cron-cleanup", type: "cron" as const },
    ];
  }
  if (repo === "chat-server") {
    return [
      { name: "server", path: "./cmd/server", type: "web" as const },
      { name: "presence-worker", path: "./cmd/presence-worker", type: "worker" as const },
    ];
  }
  return [{ name: "api", path: ".", type: "web" as const }];
}

const okLog = (svc: { name: string; size: string }[], sha: string): BuildLine[] => [
  { t: "00:00.0", text: "Received source from GitHub (412 KB)" },
  { t: "00:00.6", text: `Detected ${svc.length} ${svc.length === 1 ? "service" : "services"}: ${svc.map((s) => s.name).join(", ")}` },
  { t: "00:00.9", text: "go1.27 · module cache hit (142 modules)", tone: "dim" },
  { t: "00:01.3", text: "govulncheck: no vulnerable functions called", tone: "ok" },
  ...svc.map((s, i) => ({ t: `00:0${4 + i}.${(i * 3) % 10}`, text: `Built ${s.name.padEnd(14)} ${s.size}` })),
  { t: "00:06.9", text: "Runtime tuned: GOMAXPROCS=1 GOMEMLIMIT=115MiB", tone: "dim" },
  { t: "00:07.8", text: "Health check api: listening on $PORT after 212 ms" },
  { t: "00:08.3", text: `Traffic switched to ${sha} · previous instances draining` },
  { t: "00:08.3", text: "Deployed in 8.3s", tone: "ok" },
];

export function deploymentDetail(project: ProjectDetail, id: string): DeploymentDetail | null {
  const live = id === "live" ? deployment({ sha: "e81c3f4", message: "Deploy main from dashboard", ago: "now", source: "dashboard", createdAt: "Oct 24, 14:56" }) : undefined;
  const d = live ?? project.deployments.find((x) => x.id === id);
  if (!d) return null;
  const built = project.services.map((s) => ({ name: s.name, size: s.binarySize }));
  let log = okLog(built, d.sha);
  if (d.status === "failed") {
    log = [
      ...log.slice(0, 4),
      { t: "00:03.2", text: "# github.com/haki/acme-api/internal/db", tone: "error" },
      { t: "00:03.2", text: "internal/db/pool.go:17:18: undefined: pgxpool.ParseConfig", tone: "error" },
      { t: "00:03.3", text: "Build failed. Previous deployment 7f3a9c2 keeps serving.", tone: "error" },
    ];
  }
  if (d.status === "building") log = log.slice(0, 5);
  return { ...d, project: project.slug, goVersion: project.goVersion, built, log };
}

const levels = ["INFO", "INFO", "INFO", "INFO", "WARN", "INFO", "INFO", "ERROR"] as const;
const messages: [string, Record<string, string | number>][] = [
  ["GET /v1/products 200", { path: "/v1/products", status: 200, duration_ms: 12 }],
  ["POST /v1/orders 201", { path: "/v1/orders", status: 201, duration_ms: 48, order_id: "ord_8F1Z" }],
  ["GET /healthz 200", { path: "/healthz", status: 200, duration_ms: 0 }],
  ["cache miss key=products:list:page=2", { key: "products:list:page=2" }],
  ["rate limit hit path=/v1/orders", { ip: "102.89.x.x", path: "/v1/orders", limit: 60 }],
  ["GET /v1/orders/ord_8F1Z 200", { path: "/v1/orders/ord_8F1Z", status: 200, duration_ms: 6 }],
  ["enqueued job queue=emails", { queue: "emails", job_id: "job_31aa" }],
  ["payment provider timeout path=/v1/orders", { path: "/v1/orders", order_id: "ord_8F2K", duration_ms: 30002, trace_id: "4b1e9c7a2f" }],
];

export function logLine(n: number, service = "api"): LogLine {
  const i = n % messages.length;
  const [msg, fields] = messages[i];
  const sec = 1 + (n % 59);
  const ms = (n * 137) % 1000;
  return {
    id: `log_${n}`,
    t: `14:52:${String(sec).padStart(2, "0")}.${String(ms).padStart(3, "0")}`,
    service,
    instance: `${service}-${(n % 2) + 1}`,
    level: levels[i],
    msg,
    fields: { level: levels[i].toLowerCase(), msg, ...fields },
  };
}

export function logs(scenario: Scenario): LogLine[] {
  const lines = Array.from({ length: 40 }, (_, i) => logLine(i, i % 5 === 4 ? "worker" : "api"));
  if (scenario === "crashing") {
    lines.push({ id: "log_crash", t: "14:53:02.118", service: "worker", instance: "worker-1", level: "ERROR", msg: "panic: assignment to entry in nil map", fields: { level: "error", msg: "panic: assignment to entry in nil map", file: "internal/jobs/email.go:41", goroutine: 18 } });
  }
  return lines;
}

function series(label: string, unit: string, values: number[]): Series {
  return {
    label,
    unit,
    values,
    events: [
      { at: 6, kind: "wake", label: "Woke from pause in 18 ms" },
      { at: 14, kind: "deploy", label: "Deployed 7f3a9c2" },
      { at: 19, kind: "restart", label: "Instance api-2 restarted (OOM avoided by GOMEMLIMIT)" },
    ],
  };
}

export function metrics(): { requests: Series; latency: Series; errors: Series; instances: Series; memory: Series; cpu: Series } {
  return {
    requests: series("Requests / min", "req", [120, 140, 110, 90, 70, 60, 80, 150, 240, 310, 340, 320, 290, 330, 360, 310, 280, 260, 240, 220, 190, 170, 150, 130]),
    latency: series("Latency p95", "ms", [38, 40, 39, 41, 44, 40, 39, 42, 47, 51, 49, 46, 44, 41, 40, 43, 45, 41, 39, 40, 41, 39, 38, 41]),
    errors: series("5xx errors", "%", [0.1, 0, 0, 0.1, 0, 0, 0, 0.2, 0.4, 0.3, 0.2, 0.1, 0.1, 0, 0.1, 0, 0, 0.9, 0.2, 0.1, 0, 0, 0.1, 0]),
    instances: series("Instances", "", [1, 1, 1, 0, 0, 0, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1]),
    memory: series("Memory", "MB", [22, 24, 23, 0, 0, 0, 21, 26, 31, 38, 41, 39, 36, 40, 42, 39, 35, 33, 30, 29, 27, 25, 24, 23]),
    cpu: series("CPU", "%", [8, 9, 7, 0, 0, 0, 6, 14, 22, 31, 34, 30, 28, 33, 36, 30, 26, 24, 21, 19, 16, 13, 11, 9]),
  };
}

export function runtime(scenario: Scenario): RuntimeInfo {
  const leak = scenario !== "sleeping";
  const goroutines = leak
    ? [118, 121, 119, 123, 120, 122, 121, 124, 122, 120, 123, 121, 128, 134, 139, 145, 151, 157, 162, 168, 172, 177, 181, 184]
    : [118, 121, 119, 123, 120, 122, 121, 124, 122, 120, 123, 121, 119, 122, 120, 121, 123, 120, 119, 122, 121, 120, 119, 121];
  return {
    service: "api",
    stats: [
      { label: "Goroutines", value: String(goroutines.at(-1)), note: leak ? "+52% in 24h" : "steady" },
      { label: "Heap in use", value: "23.4 MB", note: "GOMEMLIMIT 115 MiB" },
      { label: "GC pause p99", value: "0.41 ms", note: "38 cycles / min" },
      { label: "Wake-up p50", value: "18 ms", note: "from paused · 12 today" },
    ],
    goroutines: series("Goroutines", "", goroutines),
    heap: series("Heap in use", "MB", [14, 18, 22, 16, 19, 23, 17, 20, 24, 18, 21, 25, 19, 22, 24, 18, 21, 23, 20, 22, 24, 21, 23, 23.4]),
    leak: leak ? { from: 121, to: 184, since: "14:20", blocked: 63, site: "handlers/orders.go:88" } : undefined,
    flame: [
      { name: "main", pct: 100, depth: 0, offset: 0 },
      { name: "net/http.(*conn).serve", pct: 91, depth: 1, offset: 0 },
      { name: "runtime.gcBgMarkWorker", pct: 9, depth: 1, offset: 91 },
      { name: "chi.(*Mux).ServeHTTP", pct: 88, depth: 2, offset: 0 },
      { name: "handlers.CreateOrder", pct: 55, depth: 3, offset: 0, hot: true },
      { name: "handlers.ListProducts", pct: 32, depth: 3, offset: 55 },
      { name: "pgx.(*Conn).Query", pct: 31, depth: 4, offset: 0 },
      { name: "encoding/json.Marshal", pct: 23, depth: 4, offset: 31 },
      { name: "cache.(*LRU).Get", pct: 19, depth: 4, offset: 55 },
      { name: "json.(*encodeState).string", pct: 9, depth: 5, offset: 31 },
    ],
    tuning: { gomaxprocs: "1", gomemlimit: "115MiB", pgo: true },
  };
}
