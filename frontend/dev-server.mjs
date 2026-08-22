import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { resolve } from "node:path";
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

export function routeRequest(req, {
  gatewayTarget: requestedGateway = gatewayTarget,
  nextTarget: requestedNext = nextTarget,
} = {}) {
  const pathname = new URL(req.url ?? "/", "http://telos.dev").pathname;
  if (pathname.startsWith("/api/")) return requestedGateway;
  return requestedNext;
}

export function createDevServer({
  proxy,
  createServerImpl = createServer,
  gatewayTarget: requestedGateway = gatewayTarget,
  nextTarget: requestedNext = nextTarget,
}) {
  const server = createServerImpl((req, res) => {
    proxy.web(req, res, {
      target: routeRequest(req, {
        gatewayTarget: requestedGateway,
        nextTarget: requestedNext,
      }),
    });
  });

  server.on("upgrade", (req, socket, head) => {
    proxy.ws(req, socket, head, {
      target: routeRequest(req, {
        gatewayTarget: requestedGateway,
        nextTarget: requestedNext,
      }),
    });
  });

  return server;
}

function startDevServer() {
  const proxy = httpProxy.createProxyServer({
    changeOrigin: false,
    proxyTimeout: 30_000,
    timeout: 30_000,
    ws: true,
    xfwd: true,
  });

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

  const server = createDevServer({ proxy });

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
  });
}

const invokedDirectly =
  process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url);
if (invokedDirectly) startDevServer();
