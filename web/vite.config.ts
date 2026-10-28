import fs from "node:fs"
import path from "node:path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig, loadEnv } from "vite"

// openheron 前端开发配置
//
// 开发时前端由本进程提供，/v2 反代到 Go。
// 因为走代理，前端请求是同源的 —— openheron 不需要 CORS 中间件。
// 生产时前端由 Go 的 go:embed 提供（见 internal/webui），此处 proxy 失效。
//
// ── 两套配置，两套端口 ────────────────────────────────────────────────
//
//   mode = development → :5173，代理到 :8080（默认）
//   mode = test        → :15173，代理到 :18099
//
//   ★ 测试**不复用开发端口**。端口复用不只是「抢占」：8080 上跑着开发实例
//     时，E2E 会「全绿」但打的是真后端 —— 假后端根本没被用到，而这正是
//     playwright.config.ts 里 webServer 端口用变量的原因。
//   ★ 端口与代理目标全部取自**仓库根的 .env.test**，不在这里复述一份；
//     改端口只改那一个文件。
export default defineConfig(({ mode }) => {
  const isTest = mode === "test"

  // ★ loadEnv 的第二个参数是目录，这里给**仓库根**而非 web/：
  //   .env.test 放在仓库根，与后端脚本（scripts/test-backend.sh）共读同一份，
  //   两边端口才不会各说各话。
  const repoRoot = path.resolve(import.meta.dirname, "..")
  const env = {
    ...loadEnv(mode, repoRoot, "OPENHERON_"),
    ...loadEnv(mode, process.cwd(), "OPENHERON_"),
  }

  // E2E 时把 /v2 指向 test/fake-server.ts（Playwright 起在另一个测试端口）。
  // 做成可覆盖而不是写死端口，是因为产品代码不该知道测试的存在。
  const apiTarget =
    env.OPENHERON_API_TARGET ?? (isTest ? "http://127.0.0.1:18099" : "http://127.0.0.1:8080")
  const port = Number(env.OPENHERON_TEST_FRONTEND_PORT ?? (isTest ? 15173 : 5173))

  // 本地存在 .env.test 时不必存在；缺失则回落到上面的字面量。
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
      // ★ 端口被占时**报错退出**，而不是静默换到下一个端口。
      //   静默换端口会让测试连到别人的服务上，然后「莫名其妙」地全绿。
      strictPort: true,
      proxy: {
        "/v2": { target: apiTarget, changeOrigin: true },
      },
    },
    build: {
      outDir: "dist",
      emptyOutDir: true,
      // ★ 首屏的每个块都远低于 500KB（app 190 / vendor 218 / markdown 119）。
      //   唯一超过 500KB 的是**设置对话框**那一块（CodeMirror，懒加载）——
      //   它只在用户打开设置时下载，不在首屏路径上，所以按默认阈值它会一直
      //   报警。阈值放到 700 是为了让这个告警**继续有意义**（首屏路径上
      //   任何块重新涨过 500KB 仍会报），而不是为了消掉一条已知且已解释的告警。
      //   ★ 要根治它得换掉 @uiw/react-codemirror（它拖进整套 CM6），
      //     见 docs/frontend-design.md §1.5。
      chunkSizeWarningLimit: 700,
      rolldownOptions: {
        output: {
          // ★ 分成三块是为了让「首屏装了什么」可读，不是为了减少总字节 ——
          //   三块加起来和原来那个大包一样大。分开之后块体积才重新有意义：
          //   应用代码 / 框架 / markdown 渲染器各归各，哪一块涨了看得出来。
          //
          //   ★ 这是 **rolldown** 的选项（vite 8 内置 rolldown），
          //     不是 rollup 的 `manualChunks`。
          //
          //   正则里的 `[\\/]` 同时匹配 POSIX 与 Windows 的路径分隔符。
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