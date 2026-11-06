import type { Service } from "@/lib/api/types";
import { AlwaysOnBadge, Status, type Tone } from "@/components/ui/status";

// Sleeping is calm grey, never red (design.md §1.3).
export function serviceState(s: Service): { tone: Tone; label: string; detail?: string } {
  const st = s.state;
  switch (st.kind) {
    case "running":
      return { tone: "ok", label: "Running", detail: `${st.instances} ${st.instances === 1 ? "instance" : "instances"}` };
    case "sleeping":
      return s.type === "worker"
        ? { tone: "idle", label: "Sleeping", detail: `${st.queued ?? 0} queued` }
        : { tone: "idle", label: "Sleeping", detail: "wakes on request" };
    case "building":
      return { tone: "progress", label: "Building" };
    case "crashing":
      return { tone: "error", label: "Crashing", detail: st.reason };
    case "failed":
      return { tone: "error", label: "Failed", detail: st.reason };
    case "scheduled":
      return { tone: "idle", label: `Next run ${st.nextRun}`, detail: st.lastRun ? `last exit ${st.lastRun.exitCode} · ${st.lastRun.duration}` : undefined };
  }
}

export function ServiceStatus({ service, withDetail = true }: { service: Service; withDetail?: boolean }) {
  const s = serviceState(service);
  return (
    <span className="inline-flex min-w-0 flex-wrap items-center gap-2">
      <Status tone={s.tone} label={s.label} detail={withDetail ? s.detail : undefined} className="min-w-0 [&>span:last-child]:truncate" />
      {service.alwaysOn && <AlwaysOnBadge />}
    </span>
  );
}
