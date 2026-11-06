"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/primitives/select";
import { scenarioCookie, scenarios, type Scenario } from "@/lib/fixtures/scenarios";

// Dev-only: switches which fixture set lib/api returns (design.md §5).
export function ScenarioSwitcher({ current }: { current: Scenario }) {
  const router = useRouter();
  const [value, setValue] = useState(current);

  function change(next: Scenario) {
    setValue(next);
    document.cookie = `${scenarioCookie}=${next}; path=/; max-age=31536000; samesite=lax`;
    router.refresh();
  }

  return (
    <div className="fixed bottom-20 right-3 z-40 flex items-center gap-2 rounded-input border border-dashed border-border-strong bg-surface-2 py-1 pl-2.5 pr-1 text-12 text-dim md:bottom-4 md:right-4">
      Fixture
      <Select value={value} onValueChange={(v) => change(v as Scenario)}>
        <SelectTrigger size="sm" aria-label="Fixture scenario" className="h-7 min-w-36 text-12">
          <SelectValue />
        </SelectTrigger>
        <SelectContent align="end">
          {scenarios.map((s) => (
            <SelectItem key={s.id} value={s.id} className="text-13">
              {s.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}
