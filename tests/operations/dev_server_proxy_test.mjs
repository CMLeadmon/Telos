import assert from "node:assert/strict";
import { cpSync, existsSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const cleanCopy = mkdtempSync(join(tmpdir(), "telos-dev-proxy-"));
const copiedModule = join(cleanCopy, "dev-server.mjs");
cpSync(new URL("../../frontend/dev-server.mjs", import.meta.url), copiedModule);
assert.equal(existsSync(join(cleanCopy, "node_modules")), false);

let createDevServer;
try {
  ({ createDevServer } = await import(pathToFileURL(copiedModule)));
} finally {
  rmSync(cleanCopy, { recursive: true, force: true });
}

let requestListener;
const upgradeListeners = new Map();
const calls = [];
const proxy = {
  web(request, response, options) {
    calls.push({ kind: "http", request, response, target: options.target });
  },
  ws(request, socket, head, options) {
    calls.push({ kind: "ws", request, socket, head, target: options.target });
  },
};
const server = {
  on(event, listener) {
    upgradeListeners.set(event, listener);
    return this;
  },
};

const created = createDevServer({
  proxy,
  createServerImpl(listener) {
    requestListener = listener;
    return server;
  },
  gatewayTarget: "http://gateway.test:8080",
  nextTarget: "http://next.test:3001",
});

assert.equal(created, server);

function dispatchHttp(url, target) {
  const request = { url };
  const response = {};
  requestListener(request, response);
  const call = calls.at(-1);
  assert.deepEqual(call, { kind: "http", request, response, target });
  assert.equal(request.url, url);
}

function dispatchWebSocket(url, target) {
  const request = { url };
  const socket = {};
  const head = Buffer.from("head");
  upgradeListeners.get("upgrade")(request, socket, head);
  const call = calls.at(-1);
  assert.deepEqual(call, { kind: "ws", request, socket, head, target });
  assert.equal(request.url, url);
}

dispatchHttp("/api/v1/health?probe=1", "http://gateway.test:8080");
dispatchWebSocket("/api/v1/chat/ws?channel=general", "http://gateway.test:8080");
dispatchHttp("/stream/?page=2", "http://next.test:3001");
dispatchHttp("/livekit?legacy=1", "http://next.test:3001");
dispatchWebSocket("/stream/socket?ordinary=1", "http://next.test:3001");
dispatchWebSocket("/livekit?legacy=1", "http://next.test:3001");
