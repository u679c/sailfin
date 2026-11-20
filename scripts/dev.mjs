import { spawn } from "node:child_process";
import { watch, mkdirSync, readdirSync } from "node:fs";
import { resolve } from "node:path";
import "./env.mjs";
const children = new Set();
let closing = false,
  restarting = false,
  again = false;
mkdirSync("var", { recursive: true });
const webPort = process.env.TRACELITE_WEB_PORT || "5173";
const env = {
  ...process.env,
  TRACELITE_PUBLIC_URL:
    process.env.TRACELITE_PUBLIC_URL || `http://localhost:${webPort}`,
};
const goArgs =
  process.platform === "darwin" ? ["-ldflags=-linkmode=external"] : [];
function start(cmd, args, name) {
  const p = spawn(cmd, args, {
    stdio: "inherit",
    env,
    detached: process.platform !== "win32",
  });
  children.add(p);
  p.on("error", (e) => {
    console.error(`${name}: ${e.message}`);
    shutdown(1);
  });
  p.on("exit", (code) => {
    children.delete(p);
    if (!closing && !p.intentional && code)
      console.error(
        `${name} exited (${code}); resolve its error and restart make dev.`,
      );
  });
  return p;
}
function kill(p, signal = "SIGTERM") {
  if (!p || p.exitCode !== null) return;
  p.intentional = true;
  try {
    process.kill(-p.pid, signal);
  } catch {
    p.kill(signal);
  }
}
function stop(p) {
  if (!p || p.exitCode !== null) return Promise.resolve();
  return new Promise((done) => {
    p.once("exit", done);
    kill(p);
    const timer = setTimeout(() => kill(p, "SIGKILL"), 8000);
    p.once("exit", () => clearTimeout(timer));
  });
}
const coreArgs = [
  "run",
  ...goArgs,
  "./cmd/core",
  "serve",
  ...process.argv.slice(2),
];
let core = start("go", coreArgs, "Core");
start("npm", ["run", "dev", "-w", "web"], "Web");
let timer;
async function restart() {
  if (restarting) {
    again = true;
    return;
  }
  restarting = true;
  await stop(core);
  if (!closing) {
    core = start("go", coreArgs, "Core");
  }
  restarting = false;
  if (again) {
    again = false;
    void restart();
  }
}
const watchers = [],
  watched = new Set();
function watchDir(dir) {
  const abs = resolve(dir);
  if (watched.has(abs)) return;
  watched.add(abs);
  watchers.push(
    watch(abs, (_, file) => {
      if (file && /\.(go|sql|js)$/.test(file)) {
        clearTimeout(timer);
        timer = setTimeout(() => void restart(), 300);
      }
      try {
        scan(abs);
      } catch {}
    }),
  );
  scan(abs);
}
function scan(dir) {
  for (const e of readdirSync(dir, { withFileTypes: true }))
    if (e.isDirectory()) watchDir(resolve(dir, e.name));
}
watchDir("cmd");
watchDir("internal");
watchDir("browser-agent/src");
async function shutdown(code = 0) {
  if (closing) return;
  closing = true;
  clearTimeout(timer);
  watchers.forEach((w) => w.close());
  await Promise.all([...children].map(stop));
  process.exit(code);
}
process.on("SIGINT", () => void shutdown());
process.on("SIGTERM", () => void shutdown());
console.log(
  `TraceLite Web: ${env.TRACELITE_PUBLIC_URL} • Core: ${env.TRACELITE_CORE_URL || "http://127.0.0.1:8080"} • Ctrl+C stops these development processes.`,
);
