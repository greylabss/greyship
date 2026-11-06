"use client";

import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/primitives/select";

export function useService(services: string[]) {
  const params = useSearchParams();
  const wanted = params.get("service");
  return wanted && services.includes(wanted) ? wanted : services[0];
}

export function ServicePicker({ services }: { services: string[] }) {
  const router = useRouter();
  const path = usePathname();
  const current = useService(services);
  return (
    <Select value={current} onValueChange={(v) => router.replace(`${path}?service=${v}`, { scroll: false })}>
      <SelectTrigger aria-label="Service" className="h-10 min-w-40 font-mono text-13">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {services.map((s) => (
          <SelectItem key={s} value={s} className="font-mono text-13">
            {s}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
