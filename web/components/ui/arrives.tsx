import { EmptyState } from "./empty-state";

// A screen whose data the API doesn't have yet. It says when it arrives instead of showing invented numbers.
export function Arrives({ title, when, command }: { title: string; when: string; command?: string }) {
  return <EmptyState title={title} body={`This arrives with ${when}.`} command={command} />;
}
