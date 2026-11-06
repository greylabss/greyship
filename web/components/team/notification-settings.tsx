"use client";

import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { ApiError, call } from "@/lib/greyship-api";

type Setting = { kind: string; email: boolean; push: boolean };

const labels: Record<string, string> = {
  "deploy.succeeded": "Deploy succeeded",
  "deploy.failed": "Deploy failed",
  "project.created": "New project",
};

// Notification settings and this browser's push switch (decisions.md 046). Everything goes through the API.
export function NotificationSettings() {
  const [settings, setSettings] = useState<Setting[] | null>(null);
  const [pushAvailable, setPushAvailable] = useState(false);
  const [browser, setBrowser] = useState<"unsupported" | "off" | "on" | "blocked">("off");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    call<{ settings: Setting[]; push_available: boolean }>("GET", "/v1/me/notifications/settings")
      .then((r) => {
        setSettings(r.settings);
        setPushAvailable(r.push_available);
      })
      .catch(() => setSettings([]));
    // The browser's push support and permission only exist after hydration.
    const support = !("serviceWorker" in navigator) || !("PushManager" in window) || !("Notification" in window) ? "unsupported" : Notification.permission === "denied" ? "blocked" : null;
    if (support) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- browser APIs only exist after hydration
      setBrowser(support);
      return;
    }
    navigator.serviceWorker
      .getRegistration("/")
      .then((reg) => reg?.pushManager.getSubscription())
      .then((sub) => setBrowser(sub ? "on" : "off"))
      .catch(() => undefined);
  }, []);

  async function save(next: Setting[]) {
    setSettings(next);
    try {
      await call("PATCH", "/v1/me/notifications/settings", { settings: next });
    } catch (err) {
      toast.error("Couldn't save", { description: err instanceof ApiError ? err.message : "Try again." });
    }
  }

  async function turnOn() {
    setBusy(true);
    try {
      if ((await Notification.requestPermission()) !== "granted") {
        setBrowser("blocked");
        return;
      }
      const { public_key } = await call<{ public_key: string }>("GET", "/v1/push/key");
      const reg = await navigator.serviceWorker.register("/sw.js", { scope: "/" });
      await navigator.serviceWorker.ready;
      const sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: urlBase64ToBytes(public_key) });
      await call("POST", "/v1/push/subscriptions", sub.toJSON());
      setBrowser("on");
      toast.success("Notifications are on in this browser");
    } catch (err) {
      toast.error("Couldn't turn them on", { description: err instanceof ApiError ? err.message : "Your browser refused. Try again." });
    } finally {
      setBusy(false);
    }
  }

  async function turnOff() {
    setBusy(true);
    try {
      const reg = await navigator.serviceWorker.getRegistration("/");
      const sub = await reg?.pushManager.getSubscription();
      await sub?.unsubscribe();
      setBrowser("off");
    } finally {
      setBusy(false);
    }
  }

  if (settings === null) return <div className="h-32 animate-pulse rounded-panel bg-surface" />;
  return (
    <div className="flex flex-col gap-4">
      <div className="overflow-hidden rounded-panel border border-border">
        <div className="grid grid-cols-[minmax(0,1fr)_64px_64px] items-center bg-surface px-4 py-2.5 text-12 font-medium text-muted">
          <span>Event</span>
          <span className="text-center">Email</span>
          <span className="text-center">Push</span>
        </div>
        {settings.map((s, i) => (
          <div key={s.kind} className="grid grid-cols-[minmax(0,1fr)_64px_64px] items-center border-t border-border px-4 py-3">
            <span>{labels[s.kind] ?? s.kind}</span>
            {(["email", "push"] as const).map((ch) => (
              <label key={ch} className="flex justify-center">
                <span className="sr-only">
                  {labels[s.kind]} by {ch}
                </span>
                <input
                  type="checkbox"
                  checked={s[ch]}
                  disabled={ch === "push" && !pushAvailable}
                  onChange={(e) => save(settings.map((x, j) => (j === i ? { ...x, [ch]: e.target.checked } : x)))}
                  className="size-4 accent-[var(--gs-text)]"
                />
              </label>
            ))}
          </div>
        ))}
      </div>
      {pushAvailable && (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-panel border border-border px-4 py-3">
          <div className="flex min-w-0 flex-col">
            <span className="font-medium">Browser notifications</span>
            <span className="text-13 text-muted">
              {browser === "on"
                ? "On in this browser, even with the dashboard closed."
                : browser === "blocked"
                  ? "Blocked in this browser. Allow notifications for this site in the browser's settings."
                  : browser === "unsupported"
                    ? "This browser can't show them. On iPhone, add Greyship to the home screen first."
                    : "Get deploy results on this computer or phone, even with the dashboard closed."}
            </span>
          </div>
          {browser === "off" && (
            <Button variant="primary" disabled={busy} onClick={turnOn}>
              {busy ? "Turning on…" : "Turn on"}
            </Button>
          )}
          {browser === "on" && (
            <Button disabled={busy} onClick={turnOff}>
              Turn off here
            </Button>
          )}
        </div>
      )}
    </div>
  );
}

function urlBase64ToBytes(s: string): Uint8Array<ArrayBuffer> {
  const padded = (s + "=".repeat((4 - (s.length % 4)) % 4)).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(padded);
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}
