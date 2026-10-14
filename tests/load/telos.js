// k6 load test for the Telos capacity drill (scripts/run-capacity-drill.sh runs it; see
// documentation/operations/capacity.md). It is not a unit test: it drives a REAL node over HTTPS.
//
// Shape: exactly 25 simultaneously authenticated member sessions (one per VU) for TELOS_LOAD_DURATION
// (default 30m). The test is split into four equal phases; at each phase boundary every VU signs its
// account out and signs another in, so all 100 fixture accounts take a turn and there are never more
// than 25 live sessions. Every iteration of every VU does, over its own session:
//   - two reads, rotating through Chat, Stream (incl. a bounded range request), Library, Files and
//     commentary, each validated for status AND shape -- not just "200";
//   - one durable write, alternating a Chat message and a commentary note, paced so the 25 sessions
//     together produce 10 writes per second.
// Environment: TELOS_ORIGIN (https origin), TELOS_LOAD_DATA (path to the fixture file with
// {fixture: {chatChannel, video: {id}, audio: {id}, ebook: {id}}, accounts: [{username, password} x 100]}).
import http from "k6/http";
import exec from "k6/execution";
import { check, sleep } from "k6";
import { Counter } from "k6/metrics";

const ORIGIN = __ENV.TELOS_ORIGIN;
const DATA = JSON.parse(open(__ENV.TELOS_LOAD_DATA));
const DURATION = __ENV.TELOS_LOAD_DURATION || "30m";
const SESSIONS = 25;
const PHASES = 4;
const WRITES_PER_SECOND = 10;
const ITERATION_SECONDS = SESSIONS / WRITES_PER_SECOND; // 2.5 s per write per VU

function seconds(text) {
  const match = /^(\d+)(s|m|h)$/.exec(text);
  if (!match) throw new Error(`TELOS_LOAD_DURATION must look like 30m, got ${text}`);
  return Number(match[1]) * { s: 1, m: 60, h: 3600 }[match[2]];
}
const PHASE_SECONDS = seconds(DURATION) / PHASES;

export const options = {
  scenarios: {
    member_journeys: { executor: "constant-vus", vus: SESSIONS, duration: DURATION, exec: "memberJourney" },
  },
  thresholds: {
    http_req_failed: ["rate<0.01"],
    http_req_duration: ["p(95)<750"],
    "http_req_duration{kind:mutation}": ["p(95)<1000"],
    checks: ["rate>0.99"],
    semantic_failures: ["count==0"],
  },
  summaryTrendStats: ["avg", "min", "med", "p(90)", "p(95)", "max"],
};

const semanticFailures = new Counter("semantic_failures");
const state = { phase: -1, username: null, channelId: null }; // per VU: k6 gives each VU its own module scope

function headers(extra) {
  return Object.assign({ "Content-Type": "application/json", Origin: ORIGIN }, extra || {});
}

function ok(response, name, predicate) {
  const passed = check(response, { [name]: (r) => predicate(r) });
  if (!passed) semanticFailures.add(1);
  return passed;
}

function json(response) {
  try { return response.json(); } catch (_) { return null; }
}

function rotateSession() {
  const elapsed = (Date.now() - exec.scenario.startTime) / 1000;
  const phase = Math.min(PHASES - 1, Math.floor(elapsed / PHASE_SECONDS));
  if (phase === state.phase) return;
  if (state.username !== null) {
    const out = http.post(`${ORIGIN}/api/v1/auth/logout`, null, { headers: headers(), tags: { kind: "mutation", step: "logout" } });
    ok(out, "logout", (r) => r.status === 200 || r.status === 204);
  }
  const account = DATA.accounts[(exec.vu.idInTest - 1) % SESSIONS + SESSIONS * phase];
  const login = http.post(`${ORIGIN}/api/v1/auth/login`, JSON.stringify({ username: account.username, password: account.password }),
    { headers: headers(), tags: { kind: "mutation", step: "login" } });
  if (!ok(login, "login", (r) => r.status === 200)) { sleep(1); return; }
  state.phase = phase;
  state.username = account.username;
}

function chatChannelId() {
  if (state.channelId) return state.channelId;
  const response = http.get(`${ORIGIN}/api/v1/channels`, { tags: { kind: "read", module: "chat" } });
  const channels = json(response);
  ok(response, "channels", (r) => r.status === 200 && Array.isArray(channels));
  const channel = (channels || []).find((c) => (c.name || c.Name) === DATA.fixture.chatChannel);
  if (channel) state.channelId = channel.id || channel.ID;
  return state.channelId;
}

const READS = {
  chat() {
    const id = chatChannelId();
    if (!id) return;
    const response = http.get(`${ORIGIN}/api/v1/channels/${id}/roots`, { tags: { kind: "read", module: "chat" } });
    ok(response, "chat roots", (r) => r.status === 200 && json(r) !== null);
  },
  stream() {
    const list = http.get(`${ORIGIN}/api/v1/media/items`, { tags: { kind: "read", module: "stream" } });
    ok(list, "media items", (r) => r.status === 200 && json(r) !== null);
    const info = http.get(`${ORIGIN}/api/v1/media/items/${DATA.fixture.video.id}/playback-info`, { tags: { kind: "read", module: "stream" } });
    ok(info, "playback info", (r) => r.status === 200 && json(r) !== null);
    const range = http.get(`${ORIGIN}/api/v1/stream/audio/${DATA.fixture.audio.id}`, { headers: { Range: "bytes=0-65535" }, tags: { kind: "read", module: "stream", step: "range" } });
    ok(range, "audio range", (r) => r.status === 206 && String(r.headers["Content-Range"] || "").startsWith("bytes 0-") && r.body.length <= 65536);
  },
  library() {
    const list = http.get(`${ORIGIN}/api/v1/library/books`, { tags: { kind: "read", module: "library" } });
    ok(list, "books", (r) => r.status === 200 && Array.isArray(json(r)) && json(r).length > 0);
    const progress = http.get(`${ORIGIN}/api/v1/library/books/${DATA.fixture.ebook.id}/progress`, { tags: { kind: "read", module: "library" } });
    ok(progress, "book progress", (r) => r.status === 200 || r.status === 404);
  },
  files() {
    const response = http.get(`${ORIGIN}/api/v1/files`, { tags: { kind: "read", module: "files" } });
    ok(response, "files", (r) => r.status === 200 && json(r) !== null);
  },
  commentary() {
    const response = http.get(`${ORIGIN}/api/v1/media/items/${DATA.fixture.video.id}/comments`, { tags: { kind: "read", module: "commentary" } });
    ok(response, "comments", (r) => r.status === 200 && json(r) !== null);
  },
};
const READ_ORDER = ["chat", "stream", "library", "files", "commentary"];

export function eventWrite(index) {
  if (index % 2 === 0) {
    const id = chatChannelId();
    if (!id) return;
    const marker = `cap-${exec.vu.idInTest}-${index}`;
    const response = http.post(`${ORIGIN}/api/v1/channels/${id}/messages`, JSON.stringify({ content: marker }), { headers: headers(), tags: { kind: "mutation", module: "chat" } });
    ok(response, "message posted", (r) => (r.status === 200 || r.status === 201) && json(r) !== null);
  } else {
    const response = http.post(`${ORIGIN}/api/v1/media/items/${DATA.fixture.video.id}/comments`, JSON.stringify({ note: `cap-${exec.vu.idInTest}-${index}`, visibility: "private" }),
      { headers: headers(), tags: { kind: "mutation", module: "commentary" } });
    ok(response, "comment posted", (r) => (r.status === 200 || r.status === 201) && json(r) !== null);
  }
}

export function memberJourney() {
  const started = Date.now();
  rotateSession();
  if (state.username !== null) {
    const index = exec.scenario.iterationInTest;
    READS[READ_ORDER[index % READ_ORDER.length]]();
    READS[READ_ORDER[(index + 2) % READ_ORDER.length]]();
    eventWrite(index);
  }
  const rest = ITERATION_SECONDS - (Date.now() - started) / 1000;
  if (rest > 0) sleep(rest);
}
