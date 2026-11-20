import { defineConfig } from "vite";
export default defineConfig({
  build: {
    lib: {
      entry: "src/index.js",
      name: "Sailfin",
      formats: ["iife", "es"],
      fileName: (f) =>
        f === "iife" ? "sailfin.browser.min.js" : "sailfin.browser.js",
    },
    minify: "esbuild",
    sourcemap: true,
  },
});
