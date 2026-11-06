export function dollars(cents: number): string {
  return `$${(cents / 100).toFixed(2)}`;
}

export function compact(n: number): string {
  if (n >= 1_000_000) return `${trim(n / 1_000_000)}M`;
  if (n >= 1_000) return `${trim(n / 1_000)}k`;
  return String(n);
}

export function grouped(n: number): string {
  return n.toLocaleString("en-US");
}

function trim(n: number): string {
  return n >= 100 ? n.toFixed(0) : n.toFixed(1).replace(/\.0$/, "");
}

export function cx(...classes: (string | false | null | undefined)[]): string {
  return classes.filter(Boolean).join(" ");
}
