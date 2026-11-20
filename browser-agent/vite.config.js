import { defineConfig } from "vite";
export default defineConfig({
  build: {
    lib: {
      entry: "src/index.js",
      name: "TraceLite",
      formats: ["iife", "es"],
      fileName: (f) =>
        f === "iife" ? "tracelite.browser.min.js" : "tracelite.browser.js",
    },
    minify: "esbuild",
    sourcemap: true,
  },
});
