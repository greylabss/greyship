// Reads pasted .env text exactly the way grey deploy reads .env.prod (internal/dotenv/dotenv.go): KEY=VALUE lines,
// # comments, an optional "export ", single quotes taken literally, double quotes with \n \t \r \\ \" escapes that
// may span lines, and a " #" comment after an unquoted value. Later lines win for repeated keys.

export type ParsedEnv = { pairs: [string, string][]; errors: string[] };

const keyPattern = /^[A-Za-z_][A-Za-z0-9_]*$/;

export function parseDotenv(text: string): ParsedEnv {
  const out = new Map<string, string>();
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  for (let i = 0; i < lines.length; i++) {
    let line = lines[i].trim();
    if (!line || line.startsWith("#")) continue;
    if (line.startsWith("export ")) line = line.slice(7);
    const eq = line.indexOf("=");
    const key = (eq < 0 ? line : line.slice(0, eq)).trim();
    if (eq < 0 || !keyPattern.test(key)) return { pairs: [], errors: [`line ${i + 1}: expected KEY=VALUE`] };
    const raw = line.slice(eq + 1).replace(/^[ \t]+/, "");
    if (raw.startsWith('"')) {
      const read = doubleQuoted(raw.slice(1), lines.slice(i + 1));
      if (read === null) return { pairs: [], errors: [`line ${i + 1}: unclosed double quote`] };
      out.set(key, read.value);
      i += read.used;
    } else if (raw.startsWith("'")) {
      const end = raw.indexOf("'", 1);
      if (end < 0) return { pairs: [], errors: [`line ${i + 1}: unclosed single quote`] };
      out.set(key, raw.slice(1, end));
    } else {
      const j = raw.indexOf(" #");
      out.set(key, (j >= 0 ? raw.slice(0, j) : raw).trim());
    }
  }
  return { pairs: [...out.entries()], errors: [] };
}

// doubleQuoted reads up to the closing quote, continuing onto following lines; used is how many extra lines it took.
function doubleQuoted(first: string, rest: string[]): { value: string; used: number } | null {
  const escapes: Record<string, string> = { n: "\n", t: "\t", r: "\r" };
  let value = "";
  let text = first;
  for (let used = 0; ; used++) {
    for (let j = 0; j < text.length; j++) {
      const c = text[j];
      if (c === "\\" && j + 1 < text.length) {
        j++;
        value += escapes[text[j]] ?? text[j];
      } else if (c === '"') {
        return { value, used };
      } else {
        value += c;
      }
    }
    if (used >= rest.length) return null;
    value += "\n";
    text = rest[used];
  }
}
