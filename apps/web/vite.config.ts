import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, ".", "VISORAFT_");
  const runtimeEnv = (
    globalThis as typeof globalThis & {
      process?: { env?: Record<string, string | undefined> };
    }
  ).process?.env;
  const apiTarget =
    runtimeEnv?.VISORAFT_API_PROXY ||
    env.VISORAFT_API_PROXY ||
    "http://127.0.0.1:8080";
  return {
    plugins: [react()],
    server: {
      proxy: {
        "/api": apiTarget,
        "/health": apiTarget
      }
    },
    preview: {
      host: "0.0.0.0",
      port: 4173
    }
  };
});
