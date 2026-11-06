import type { LogLevel, LogLine } from "./types";

// One app log line as the API sends it (NDJSON from /v1/projects/{id}/logs).
export type ApiLogLine = { id: number; ts: string; service: string; instance: string; stream: string; line: string };

// toLogLine reads structured (JSON) app logs the way grey logs does; anything else is plain text, and stderr counts
// as an error.
export function toLogLine(r: ApiLogLine): LogLine {
  return { id: String(r.id), t: new Date(r.ts).toISOString().slice(11, 23), service: r.service, instance: r.instance, ...parse(r.stream, r.line) };
}

// parse finds the level and message the way Go apps write them: JSON (slog's JSONHandler, zap, zerolog), logfmt
// (slog's TextHandler: time=… level=INFO msg="…" k=v), or a level word near the start ("2026/11/04 20:39:48 INFO
// server started", log.Printf with a prefix). Go's log package writes to stderr, so stderr alone says nothing about
// the level; a line with no level is INFO.
function parse(_stream: string, line: string): { level: LogLevel; msg: string; fields: Record<string, string | number> } {
  try {
    const parsed = JSON.parse(line) as Record<string, unknown>;
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      const { level: lv, lvl, severity, msg, message, ...rest } = parsed;
      const fields: Record<string, string | number> = {};
      for (const [k, v] of Object.entries(rest)) if (k !== "time" && k !== "ts") fields[k] = typeof v === "number" ? v : String(v);
      return { level: toLevel(String(lv ?? lvl ?? severity ?? "")), msg: String(msg ?? message ?? line), fields };
    }
  } catch {}
  if (/(^|\s)level=/.test(line)) {
    const pairs = logfmt(line);
    const fields: Record<string, string | number> = {};
    for (const [k, v] of Object.entries(pairs)) if (!["time", "level", "msg"].includes(k)) fields[k] = /^-?\d+(\.\d+)?$/.test(v) ? Number(v) : v;
    return { level: toLevel(pairs.level ?? ""), msg: pairs.msg ?? line, fields };
  }
  const word = /^(?:\S+\s+){0,3}?\[?(DEBUG|INFO|WARN|WARNING|ERROR|ERR|FATAL|PANIC)\]?:?\s+(.*)$/i.exec(line);
  if (word) return { level: toLevel(word[1]), msg: word[2], fields: {} };
  return { level: "INFO", msg: line, fields: {} };
}

function toLevel(s: string): LogLevel {
  const l = s.toUpperCase();
  if (l === "ERROR" || l === "ERR" || l === "FATAL" || l === "PANIC" || l === "CRITICAL") return "ERROR";
  if (l === "WARN" || l === "WARNING") return "WARN";
  return "INFO";
}

// logfmt reads key=value and key="quoted value" pairs.
function logfmt(line: string): Record<string, string> {
  const out: Record<string, string> = {};
  const re = /([A-Za-z0-9_.-]+)=("((?:\\.|[^"\\])*)"|\S*)/g;
  for (let m = re.exec(line); m; m = re.exec(line)) out[m[1]] = m[3] !== undefined ? m[3].replace(/\\(.)/g, "$1") : m[2];
  return out;
}
