/**
 * 假后端的可执行入口（给 Playwright 的 webServer 用）。
 *
 *   bun run test/serve.ts [port]
 *
 * ★ 为什么单独一个文件而不是直接 import fake-server：
 *   Playwright 的 webServer 需要一条「起服务」的命令，而 fake-server.ts
 *   是个库。拆开之后这个库也能被 `bun test` 直接调用，两条路径共用同一份
 *   场景定义 —— 不会出现「Playwright 跑的场景和单测跑的不是同一个」。
 */
import { startFakeServer } from "./fake-server"

const port = Number(process.argv[2] ?? 4180)
const server = startFakeServer(port)

// 收到终止信号时干净退出，否则 Playwright 关 webServer 会留下悬挂进程。
const bye = () => {
  server.stop()
  process.exit(0)
}
process.on("SIGINT", bye)
process.on("SIGTERM", bye)

console.log(`[fake-server] listening on ${server.url}`)
