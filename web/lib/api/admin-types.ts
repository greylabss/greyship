// Shapes of the /v1/admin endpoints, which mirror the database rows (field names as in Go's store package).
export type AdminDeploymentRow = {
  ID: string;
  Status: string;
  Error: string | null;
  Source: string;
  BuildMode: string;
  Runner: string | null;
  CommitSha: string | null;
  Branch: string | null;
  RollbackOf: string | null;
  CreatedAt: string;
  FinishedAt: string | null;
  BuildSeconds: number | null;
  ProjectID: string;
  ProjectSlug: string;
  TeamSlug: string;
  CreatedByEmail: string;
};

export type AdminOverview = {
  Counts: { Users: number; Teams: number; Projects: number; Deploys24h: number; Failed24h: number; InProgress: number; InstancesLive: number };
  Nodes: { ID: string; Name: string; Region: string; State: string; TotalMb: number; LastHeartbeat: string; HeartbeatAge: number; UsedMb: number; Instances: number }[];
  Jobs: { Kind: string; Status: string; N: number }[];
  FailedJobs: { ID: number; Kind: string; Payload: string; Attempts: number; MaxAttempts: number; LastError: string | null; CreatedAt: string; FinishedAt: string | null }[];
  Failed: AdminDeploymentRow[];
};

export type AdminDetail = {
  Deployment: AdminDeploymentRow & {
    Env: string;
    SourceKey: string | null;
    SourceSize: number | null;
    SourceSha256: string | null;
    StartedAt: string | null;
    MachineSize: string | null;
    GithubReported: string | null;
    FilesKey: string | null;
    FilesSize: number | null;
    ProjectName: string;
    TeamID: string;
    Repo: string;
  };
  Artifacts: { ServiceName: string; ServicePath: string; ServiceType: string; ObjectKey: string; Sha256: string; SizeBytes: number; GoVersion: string; Goarch: string }[] | null;
  Uploaded: { ServiceName: string; Sha256: string; SizeBytes: number; GzBytes: number; ObjectKey: string }[] | null;
  Instances: { ID: string; State: string; Addr: string | null; Error: string | null; MemoryMb: number; StartedAt: string | null; StoppedAt: string | null; CreatedAt: string; ServiceName: string; ServiceType: string; NodeName: string }[] | null;
  Jobs: { ID: number; Kind: string; Status: string; Attempts: number; MaxAttempts: number; LastError: string | null; CreatedAt: string; FinishedAt: string | null }[] | null;
  BuildLog: { Seq: number; Stream: string; Line: string; Ts: string }[] | null;
  AppLogs: { ID: number; Ts: string; Stream: string; Line: string; InstanceID: string; ServiceName: string }[] | null;
};

export type AdminUser = { ID: string; Email: string; Name: string; IsOwner: boolean; MaxMemoryMb: number | null; MaxCpuMillis: number | null; MaxConcurrentBuilds: number | null; MemoryUsedMb: number; CpuUsedMillis: number; CreatedAt: string; Teams: string; Projects: number; LastDeploy: string | null };

export type AdminMail = { enabled: boolean; active: boolean; source: "dashboard" | "environment" | "none"; sending_domain: string; from_name: string; key_set: boolean };
