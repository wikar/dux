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
  },
  server: {
    port: 5173,
    proxy: Object.fromEntries(API_ROUTES.map((r) => [r, "http://localhost:8080"])),
  },
});
