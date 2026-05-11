import { defineConfig } from "vite";
import { resolve } from "path";

export default defineConfig({
  build: {
    lib: {
      entry: resolve(__dirname, "src/index.ts"),
      name: "DuxClient",
      fileName: "dux-client",
      formats: ["es"],
    },
    rollupOptions: {
      external: ["highlight.js", /^highlight\.js\//],
    },
    outDir: "dist",
  },
});
