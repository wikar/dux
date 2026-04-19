import { defineConfig } from "vite";
import solidPlugin from "vite-plugin-solid";

export default defineConfig({
  plugins: [solidPlugin()],
  build: {
    outDir: "dist",
    target: "esnext",
  },
  server: {
    port: 5173,
    proxy: {
      "/query":  "http://localhost:80",
      "/schema": "http://localhost:80",
    },
  },
});
