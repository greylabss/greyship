import { connection } from "next/server";
import { Suspense } from "react";
import { adminJson } from "@/lib/api/live";
import type { AdminUser } from "@/lib/api/admin-types";
import { CreateUser } from "@/components/admin/create-user";
import { UserActions } from "@/components/admin/user-actions";
import { Table, Td, when } from "../ui";

export default function AdminUsersPage() {
  return (
    <Suspense fallback={<p className="text-muted">Loading…</p>}>
      <Users />
    </Suspense>
  );
}

async function Users() {
  await connection();
  const { users } = await adminJson<{ users: AdminUser[] | null }>("/v1/admin/users");
  const list = users ?? [];
  return (
    <>
      <h1 className="text-24 font-semibold">Users · {list.length}</h1>
      <CreateUser />
      <Table head={["Email", "Role", "Memory", "CPU", "Projects", "Last deploy", "Joined", ""]}>
        {list.map((u) => (
          <tr key={u.ID}>
            <Td mono>{u.Email}</Td>
            <Td>{u.IsOwner ? "Admin" : "User"}</Td>
            <Td mono>{used(u.MemoryUsedMb, u.MaxMemoryMb, "MB")}</Td>
            <Td mono>{used(u.CpuUsedMillis / 1000, u.MaxCpuMillis === null ? null : u.MaxCpuMillis / 1000, "cores")}</Td>
            <Td mono>{u.Projects}</Td>
            <Td className="text-muted">{when(u.LastDeploy)}</Td>
            <Td className="text-muted">{when(u.CreatedAt)}</Td>
            <Td>
              <UserActions id={u.ID} email={u.Email} isAdmin={u.IsOwner} limits={{ memoryMb: u.MaxMemoryMb, cpuMillis: u.MaxCpuMillis, builds: u.MaxConcurrentBuilds }} />
            </Td>
          </tr>
        ))}
      </Table>
    </>
  );
}

// "256 / 1024 MB", or just what is used when there is no limit.
function used(now: number, max: number | null, unit: string) {
  const n = Number.isInteger(now) ? String(now) : now.toFixed(2);
  return max === null ? `${n} ${unit}` : `${n} / ${max} ${unit}`;
}
