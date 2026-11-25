import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import { ServerResponse } from "node:http";
export default defineConfig(({ mode }) => {
  const e = loadEnv(mode, process.cwd(), "");
  for (const [key, value] of Object.entries(e)) {
    if (key.startsWith("TRACELITE_"))
      e["SAILFIN_" + key.slice("TRACELITE_".length)] ??= value;
  }
  return {
    plugins: [react()],
    server: {
      host: e.SAILFIN_WEB_HOST || "127.0.0.1",
      port: Number(e.SAILFIN_WEB_PORT || 5173),
      strictPort: true,
      proxy: {
        "/api": {
          target: e.SAILFIN_CORE_URL || "http://127.0.0.1:8080",
          changeOrigin: true,
          configure(proxy) {
            proxy.on("error", (_error, _request, response) => {
              if (
                !(response instanceof ServerResponse) ||
                response.headersSent ||
                response.writableEnded
              )
                return;
              response.writeHead(503, {
                "Content-Type": "application/json; charset=utf-8",
                "Cache-Control": "no-store",
              });
              response.end(
                JSON.stringify({
                  error:
                    "暂时无法连接 Core。请稍后重试，或查看 make dev 终端。",
                }),
              );
            });
          },
        },
        "/sdk": e.SAILFIN_CORE_URL || "http://127.0.0.1:8080",
      },
    },
    build: { outDir: "dist" },
  };
});
