import { readFileSync } from "node:fs";
// Only simple KEY=value assignments; never evaluate shell, expansions or commands.
try {
  for (const line of readFileSync(".env", "utf8").split(/\r?\n/)) {
    const m = line.match(
      /^\s*(?:export\s+)?([A-Z_][A-Z_0-9]*)\s*=\s*(.*?)\s*$/,
    );
    if (!m || process.env[m[1]] !== undefined) continue;
    let v = m[2];
    if (
      (v.startsWith('"') && v.endsWith('"')) ||
      (v.startsWith("'") && v.endsWith("'"))
    )
      v = v.slice(1, -1);
    process.env[m[1]] = v;
  }
} catch (e) {
  if (e.code !== "ENOENT") throw e;
}

// Existing installations may still use the previous environment prefix.
for (const [key, value] of Object.entries(process.env)) {
  if (!key.startsWith("TRACELITE_")) continue;
  const next = "SAILFIN_" + key.slice("TRACELITE_".length);
  if (process.env[next] === undefined) process.env[next] = value;
}
