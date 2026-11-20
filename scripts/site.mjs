import { createServer } from "node:http";
import { readFile, appendFile, mkdir } from "node:fs/promises";
await mkdir("var", { recursive: true });
const types = { "/site.js": "text/javascript", "/style.css": "text/css" };
const server = createServer(async (req, res) => {
  const pathname = new URL(req.url, "http://localhost").pathname;
  const file =
    pathname === "/site.js" || pathname === "/style.css"
      ? pathname
      : "/index.html";
  try {
    const data = await readFile(
      new URL("../examples/site" + file, import.meta.url),
    );
    res.writeHead(200, {
      "Content-Type": types[file] || "text/html; charset=utf-8",
      "X-Content-Type-Options": "nosniff",
    });
    res.end(data);
    await appendFile(
      "var/site-access.jsonl",
      JSON.stringify({
        timestamp: Date.now(),
        path: pathname,
        method: req.method,
        status: 200,
        bytes: data.length,
        ua: req.headers["user-agent"] || "",
      }) + "\n",
    ).catch((error) =>
      console.error("Access log write failed:", error.message),
    );
  } catch {
    res.writeHead(500);
    res.end("Cannot read test site files");
  }
});
const port = Number(process.env.SAILFIN_SITE_PORT || 5174);
server.listen(port, "127.0.0.1", () =>
  console.log(
    `Local verification site: http://localhost:${port} (no data is sent until you opt in)`,
  ),
);
