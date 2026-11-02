import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// 构建产物直接输出到 Go embed 目录
export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: "./",
  build: {
    outDir: "../cmd/cdtd/web/dist",
    emptyOutDir: true,
    sourcemap: false,
  },
});
