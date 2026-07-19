import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Every duxd API route the app talks to, proxied during `bun run dev`.
// /api covers the dashboards module (/api/dash/...).
const API_ROUTES = [
  "/query", "/schema", "/values", "/measures", "/relationships",
  "/datetable", "/hidden", "/refresh", "/import", "/export", "/version",
  "/api",
];

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    target: "esnext",
    rollupOptions: {
      output: {
        // The chart engine is the dash chunk's bulk and changes only on
        // dependency bumps — its own chunk caches across app releases.
        // React is pinned to its own chunk: without that, Rollup colocates
        // it inside the recharts chunk, which makes the entry statically
        // import that chunk and load charts on every tab.
        manualChunks(id: string) {
          if (/node_modules[\\/](react|react-dom|scheduler)[\\/]/.test(id)) return "react";
          if (/node_modules[\\/](recharts|victory-vendor|d3-[^\\/]+)[\\/]/.test(id)) return "recharts";
        },
      },
    },
  },
  server: {
    port: 5173,
    proxy: Object.fromEntries(API_ROUTES.map((r) => [r, "http://localhost:8080"])),
  },
});
