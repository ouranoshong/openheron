import { startFakeServer } from "./fake-server"

const port = Number(process.argv[2] ?? 4180)
const server = startFakeServer(port)

const bye = () => {
  server.stop()
  process.exit(0)
}
process.on("SIGINT", bye)
process.on("SIGTERM", bye)

console.log(`[fake-server] listening on ${server.url}`)
