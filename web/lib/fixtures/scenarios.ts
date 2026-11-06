export const scenarios = [
  { id: "default", label: "Default" },
  { id: "empty", label: "Empty team" },
  { id: "building", label: "Deploy building" },
  { id: "failed", label: "Build failed" },
  { id: "sleeping", label: "All sleeping" },
  { id: "crashing", label: "Service crashing" },
  { id: "dns", label: "Waiting for DNS" },
  { id: "over-cap", label: "Over spend cap" },
  { id: "pro", label: "Pro plan" },
  { id: "error", label: "API error" },
  { id: "slow", label: "Slow API (loading)" },
] as const;

export type Scenario = (typeof scenarios)[number]["id"];

export const scenarioCookie = "gs_scenario";

export function parseScenario(value: string | undefined): Scenario {
  return scenarios.some((s) => s.id === value) ? (value as Scenario) : "default";
}
