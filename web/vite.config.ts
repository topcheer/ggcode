import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";
import { fileURLToPath } from "node:url";

const dirname = path.dirname(fileURLToPath(import.meta.url));

// dev 模式:前端独立起服务,API/WS proxy 到本地 Go webui 服务
// build 模式:产物输出到 internal/webui/dist,由 go:embed 嵌入
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": path.resolve(dirname, "src") },
  },
  server: {
    port: 5199,
    proxy: {
      "/api": {
        target: process.env.GGCODE_WEBUI_TARGET || "http://127.0.0.1:8090",
        ws: true,
        changeOrigin: false,
      },
    },
  },
  build: {
    outDir: path.resolve(dirname, "../internal/webui/dist"),
    emptyOutDir: true,
    sourcemap: false,
  },
});
