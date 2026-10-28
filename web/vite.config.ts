import fs from "node:fs"
import path from "node:path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig, loadEnv } from "vite"

export default defineConfig(({ mode }) => {
  const isTest = mode === "test"

  const repoRoot = path.resolve(import.meta.dirname, "..")
  const env = {
    ...loadEnv(mode, repoRoot, "OPENHERON_"),
    ...loadEnv(mode, process.cwd(), "OPENHERON_"),
  }

  const apiTarget =
    env.OPENHERON_API_TARGET ?? (isTest ? "http://127.0.0.1:18099" : "http://127.0.0.1:8080")
  const port = Number(env.OPENHERON_TEST_FRONTEND_PORT ?? (isTest ? 15173 : 5173))

  if (!fs.existsSync(path.join(repoRoot, ".env.test"))) {
    console.warn(`[vite] ${repoRoot}/.env.test not found; using built-in test defaults`)
  }

  return {
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: {
        "@": path.resolve(import.meta.dirname, "./src"),
      },
    },
    server: {
      port,
      strictPort: true,
      proxy: {
        "/v2": { target: apiTarget, changeOrigin: true },
      },
    },
    build: {
      outDir: "dist",
      emptyOutDir: true,
      chunkSizeWarningLimit: 700,
      rolldownOptions: {
        output: {
          codeSplitting: {
            groups: [
              { name: "vendor", test: /node_modules[\\/](react|react-dom|scheduler)[\\/]/ },
              { name: "markdown", test: /node_modules[\\/](react-markdown|remark-|micromark|mdast|hast|unist|property-information|space-separated|comma-separated|decode-named|character-entities|html-url-attributes|zwitch|longest-streak|ccount|escape-string-regexp|markdown-table|bail|is-plain-obj|trough|devlop)[\\/]/ },
            ],
          },
        },
      },
    },
  }
})