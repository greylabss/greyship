"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useEffect, useState } from "react";
import { Button, buttonClass } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/field";
import { icons } from "@/components/ui/icons";
import { apiEnabled, ApiError, call } from "@/lib/greyship-api";

type Device = { user_code: string; client_name: string; ip: string; status: string };
type State = "enter" | "loading" | "ask" | "busy" | "done" | "cancelled" | "error";

const MOCK: Device = { user_code: "KQWD-7HXP", client_name: "grey on macbook (darwin)", ip: "", status: "pending" };

// Approves `grey login` (F-2). The code comes from ?code= (the CLI opens that URL) or is typed in.
export function DeviceConfirm() {
  const router = useRouter();
  const params = useSearchParams();
  const initial = params.get("code") ?? "";
  const [code, setCode] = useState(initial);
  const [device, setDevice] = useState<Device | null>(null);
  const [state, setState] = useState<State>(initial ? "loading" : "enter");
  const [error, setError] = useState("");

  async function load(userCode: string) {
    if (!apiEnabled) {
      setDevice({ ...MOCK, user_code: userCode.toUpperCase() || MOCK.user_code });
      setState("ask");
      return;
    }
    setState("loading");
    try {
      setDevice(await call<Device>("GET", `/v1/auth/device?user_code=${encodeURIComponent(userCode)}`));
      setState("ask");
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        router.replace(`/signin?next=${encodeURIComponent(`/device?code=${userCode}`)}`);
        return;
      }
      setError(e instanceof ApiError ? e.message : "Couldn't load this login request.");
      setState("error");
    }
  }

  useEffect(() => {
    // Runs once for a code that arrived in the URL.
    // eslint-disable-next-line react-hooks/set-state-in-effect -- loading the request the URL points at
    if (initial) void load(initial);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function decide(approve: boolean) {
    if (!device) return;
    setState("busy");
    try {
      if (apiEnabled) await call("POST", approve ? "/v1/auth/device/approve" : "/v1/auth/device/deny", { user_code: device.user_code });
      setState(approve ? "done" : "cancelled");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Couldn't save your answer.");
      setState("error");
    }
  }

  if (state === "done" || state === "cancelled") {
    const ok = state === "done";
    return (
      <div className="flex w-full max-w-md flex-col items-center gap-4 rounded-panel border border-border bg-surface-2 p-8 text-center">
        <span className="inline-flex size-10 items-center justify-center rounded-full border border-border-strong">
          {ok ? <icons.check className="text-ok" /> : <icons.close className="text-muted" />}
        </span>
        <h1 className="text-20 font-semibold">{ok ? "CLI connected" : "Request denied"}</h1>
        <p className="text-muted">{ok ? "Go back to your terminal; grey is now signed in." : "Nothing was connected. You can close this tab."}</p>
        <Link href="/continue" className={buttonClass("secondary")}>
          Open dashboard
        </Link>
      </div>
    );
  }

  if (state === "enter" || state === "error") {
    return (
      <form
        className="flex w-full max-w-md flex-col gap-5 rounded-panel border border-border bg-surface-2 p-7"
        onSubmit={(e) => {
          e.preventDefault();
          setError("");
          void load(code);
        }}
      >
        <div className="flex flex-col gap-1">
          <h1 className="text-20 font-semibold">Connect the Greyship CLI</h1>
          <p className="text-13 text-muted">
            Enter the code shown by <span className="font-mono">grey login</span>.
          </p>
        </div>
        <Field label="Code" error={error || undefined}>
          <Input value={code} onChange={(e) => setCode(e.target.value.toUpperCase())} placeholder="XXXX-XXXX" autoComplete="off" className="h-12 text-center font-mono text-20 tracking-[0.3em] md:text-20" />
        </Field>
        <Button type="submit" variant="primary" className="h-11">
          Continue
        </Button>
      </form>
    );
  }

  if (state === "loading" || !device) {
    return <div className="h-80 w-full max-w-md animate-pulse rounded-panel border border-border bg-surface-2" aria-busy="true" />;
  }

  return (
    <div className="flex w-full max-w-md flex-col gap-6 rounded-panel border border-border bg-surface-2 p-7">
      <div className="flex items-center gap-3">
        <span className="inline-flex size-11 items-center justify-center rounded-input border border-border-strong font-mono text-accent">&gt;_</span>
        <div className="flex min-w-0 flex-col">
          <h1 className="text-20 font-semibold">Authorize the Greyship CLI</h1>
          <span className="truncate text-13 text-muted">
            Requested from <span className="font-mono">{device.client_name}</span>
            {device.ip && ` · ${device.ip}`}
          </span>
        </div>
      </div>
      <div className="flex flex-col gap-2">
        <span className="text-13 text-muted">Check that this code matches your terminal</span>
        <div className="rounded-panel border border-dashed border-border-strong py-4 text-center font-mono text-28 tracking-[0.3em]">{device.user_code}</div>
      </div>
      <div className="flex flex-col gap-1.5 text-14">
        <span className="text-13 text-muted">The CLI will be able to</span>
        {["Deploy and roll back your projects", "Read logs and metrics", "Manage environment variables and domains"].map((t) => (
          <span key={t} className="flex items-center gap-2">
            <icons.check className="text-muted" />
            {t}
          </span>
        ))}
      </div>
      <div className="grid grid-cols-2 gap-2">
        <Button className="h-11" disabled={state === "busy"} onClick={() => void decide(false)}>
          Deny
        </Button>
        <Button variant="primary" className="h-11" disabled={state === "busy"} onClick={() => void decide(true)}>
          {state === "busy" ? "Saving…" : "Authorize grey"}
        </Button>
      </div>
      <p className="text-12 text-dim">Didn&apos;t run grey login? Deny and nothing will be connected.</p>
    </div>
  );
}
