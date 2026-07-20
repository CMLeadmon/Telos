import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { fileURLToPath } from "node:url";
import httpProxy from "http-proxy";

const publicHost = process.env.TELOS_DEV_HOST ?? "0.0.0.0";
const publicPort = Number.parseInt(process.env.PORT ?? "3000", 10);
const nextPort = Number.parseInt(
  process.env.TELOS_NEXT_DEV_PORT ?? String(publicPort + 1),
  10,
);
const nextTarget = `http://127.0.0.1:${nextPort}`;
const gatewayTarget =
  process.env.TELOS_DEV_GATEWAY_URL ?? "http://127.0.0.1:8080";
const livekitTarget =
  process.env.TELOS_DEV_LIVEKIT_URL ?? "http://127.0.0.1:7880";

const proxy = httpProxy.createProxyServer({
  changeOrigin: false,
  proxyTimeout: 30_000,
  timeout: 30_000,
  ws: true,
  xfwd: true,
});

function routeRequest(req) {
  const pathname = new URL(req.url ?? "/", "http://telos.dev").pathname;
  if (pathname.startsWith("/api/")) return gatewayTarget;
  if (pathname === "/livekit" || pathname.startsWith("/livekit/")) {
    const parsed = new URL(req.url ?? "/livekit", "http://telos.dev");
    parsed.pathname = parsed.pathname.slice("/livekit".length) || "/";
    req.url = `${parsed.pathname}${parsed.search}`;
    return livekitTarget;
  }
  return nextTarget;
}

proxy.on("error", (error, req, response) => {
  const target = req.url?.startsWith("/api/") ? "gateway" : "upstream";
  console.error(`[dev proxy] ${target} request failed: ${error.message}`);

  if (response && "writeHead" in response && !response.headersSent) {
    response.writeHead(502, { "Content-Type": "text/plain; charset=utf-8" });
    response.end(`Development ${target} unavailable`);
  } else if (response && "destroy" in response) {
    response.destroy();
  }
});

const server = createServer((req, res) => {
  proxy.web(req, res, { target: routeRequest(req) });
});

server.on("upgrade", (req, socket, head) => {
  proxy.ws(req, socket, head, { target: routeRequest(req) });
});

const nextBin = fileURLToPath(
  new URL("./node_modules/next/dist/bin/next", import.meta.url),
);
const nextProcess = spawn(
  process.execPath,
  [nextBin, "dev", "--hostname", "127.0.0.1", "--port", String(nextPort)],
  {
    cwd: fileURLToPath(new URL(".", import.meta.url)),
    env: process.env,
    stdio: "inherit",
  },
);

let stopping = false;

function stop(signal) {
  if (stopping) return;
  stopping = true;
  server.close();
  nextProcess.kill(signal);
}

for (const signal of ["SIGINT", "SIGTERM"]) {
  process.once(signal, () => stop(signal));
}

nextProcess.once("exit", (code, signal) => {
  server.close();
  if (!stopping) {
    console.error(`[dev proxy] Next.js exited (${signal ?? code ?? "unknown"})`);
    process.exitCode = code ?? 1;
  }
});

server.listen(publicPort, publicHost, () => {
  console.log(`> Telos dev server: http://${publicHost}:${publicPort}`);
  console.log(`> API and WebSocket gateway: ${gatewayTarget}`);
  console.log(`> LiveKit signaling: ${livekitTarget}`);
});
