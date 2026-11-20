import "./env.mjs";
import { spawn } from "node:child_process";
const args = [
  "run",
  ...(process.platform === "darwin" ? ["-ldflags=-linkmode=external"] : []),
  "./cmd/host-agent",
];
const child = spawn("go", args, {
  stdio: "inherit",
  env: process.env,
  detached: process.platform !== "win32",
});
function signal(sig) {
  try {
    process.kill(-child.pid, sig);
  } catch {
    child.kill(sig);
  }
}
process.on("SIGINT", () => signal("SIGINT"));
process.on("SIGTERM", () => signal("SIGTERM"));
child.on("error", (e) => {
  console.error(e.message);
  process.exitCode = 1;
});
child.on("exit", (code, sig) => process.exit(code ?? (sig ? 1 : 0)));
