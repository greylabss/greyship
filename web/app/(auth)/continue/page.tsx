import { connection } from "next/server";
import { redirect } from "next/navigation";
import { Suspense } from "react";
import { api, ApiError } from "@/lib/api";

// Where sign-in lands: the person's first team. Signed out goes back to sign in.
export default function ContinuePage() {
  return (
    <Suspense>
      <Continue />
    </Suspense>
  );
}

async function Continue(): Promise<null> {
  await connection();
  let teams: string[];
  try {
    teams = await api.teams();
  } catch (e) {
    if (e instanceof Error && "status" in e && (e as ApiError).status === 401) redirect("/signin");
    throw e;
  }
  redirect(teams.length > 0 ? `/${teams[0]}` : "/signin");
  return null;
}
