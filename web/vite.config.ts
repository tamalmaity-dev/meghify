import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { TanStackRouterVite } from "@tanstack/router-plugin/vite";
import { fileURLToPath } from "node:url";

// The build output lands in ../internal/webui/dist so `go build -tags web_assets`
// can embed the compiled SPA into the panel binary.
export default defineConfig({
  plugins: [
    TanStackRouterVite({ target: "react", autoCodeSplitting: true, routesDirectory: "./src/routes" }),
    react(),
    tailwindcss(),
  ],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      // The Go panel owns /api and the SSE stream.
      "/api": "http://localhost:8080",
    },
  },
});
