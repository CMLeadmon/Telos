# Voice Secure-Context Guard Design

Date: 2026-07-17
Status: Approved for implementation

## Problem

Telos voice works from `http://localhost:3000` but fails when the same
development server is reached through a router or another non-loopback HTTP
origin. Browsers treat localhost as a trustworthy exception. Ordinary HTTP
origins are not secure contexts and do not expose
`navigator.mediaDevices.getUserMedia`.

The current join sequence requests a voice token and connects a LiveKit room
before it creates the microphone pipeline. On insecure HTTP, signaling and ICE
can therefore succeed before microphone capture throws. The catch handler then
disconnects the room and displays the JavaScript exception instead of an
actionable explanation.

Production Telos already specifies a trusted HTTPS origin through Traefik. The
application must enforce that boundary clearly; it must not attempt to bypass
browser secure-context requirements.

## Goals

- Reject voice joins before token acquisition when microphone capture is
  unavailable because the page is not a secure context.
- Show stable guidance that voice requires HTTPS instead of exposing a raw
  `TypeError`.
- Preserve existing permission-denied, device-not-found, mute, deafen, device
  switching, and LiveKit connection behavior.
- Correct development documentation so port 3000 is not described as
  voice-capable over remote plain HTTP.
- Preserve the production Traefik, LiveKit, and embedded TURN architecture.

## Non-Goals

- Bypassing browser microphone security.
- Automatically changing `.env`, DNS, certificates, router forwarding, or
  firewall rules.
- Adding a new reverse proxy or certificate-management system.
- Claiming off-network audio success without testing the operator's real HTTPS
  deployment.
- Refactoring unrelated voice or settings code.

## Approaches Considered

### 1. Secure-context preflight plus the existing HTTPS deployment

Add a small capability function that checks the browser's secure-context state
and the presence of `getUserMedia`. Call it before requesting a voice token.
Keep Traefik HTTPS as the way remote users obtain microphone access.

This is the selected approach. It prevents the misleading partial join,
produces deterministic behavior, and aligns code with the normative deployment
architecture.

### 2. Serve the frontend development process with ad hoc HTTPS

This could make port 3000 trustworthy only when every client trusts the
certificate. It introduces certificate distribution and renewal into a
development process, conflicts with the production ingress boundary, and does
not provide a safe public deployment. It is rejected.

### 3. Let LiveKit connect and improve only the catch message

This would retain unnecessary token requests, transient room membership, and
misleading LiveKit log noise. It treats the symptom after crossing boundaries
that the client already knows are unavailable. It is rejected.

## Application Design

`frontend/src/lib/voiceAudio.ts` will export a pure capability evaluator with
explicit inputs:

```typescript
export interface VoiceCaptureEnvironment {
  isSecureContext: boolean;
  hasGetUserMedia: boolean;
}

export function voiceCaptureEnvironmentError(
  environment: VoiceCaptureEnvironment,
): string | null;
```

It returns `null` when the environment can attempt microphone capture. It
returns a stable HTTPS-required message when the context is insecure or
`getUserMedia` is absent. Explicit inputs keep the policy independently
testable without constructing browser globals in a Node test.

A second browser adapter reads `globalThis.isSecureContext` and
`navigator.mediaDevices?.getUserMedia`:

```typescript
export function currentVoiceCaptureEnvironmentError(): string | null;
```

At the start of `useVoiceSessionStore.join`, after leaving any previous room
but before changing to `connecting` or requesting a token, the store calls the
adapter. When it returns an error, the store enters its existing `error` state
with the requested channel ID retained for Retry. No LiveKit room is
constructed, and no token request is sent.

The existing `createMicPipeline` call remains defensive. If browser capability
changes after preflight or permission is denied, its error continues through
`voiceErrorMessage`.

## Error Copy

The stable message is:

> Voice requires a secure HTTPS connection. Reopen Telos using its HTTPS
> address and try again.

The message explains the remedy without suggesting browser-security bypasses
or exposing implementation details.

## Testing Design

Use Node's built-in test runner with TypeScript type stripping, which is
available in the repository's Node 24+ development environment. Add a focused
test file for the pure evaluator and a package script that runs it.

The red-green cycle covers:

- secure context plus `getUserMedia` returns no error;
- insecure context returns the HTTPS-required error;
- missing `getUserMedia` returns the same error even if the context reports
  secure.

Existing Playwright voice coverage remains responsible for the connected UI
flow. A browser test discovers a non-loopback IPv4 interface at runtime (with
an environment-variable override) so it can exercise an actual insecure HTTP
origin without committing machine-specific IP addresses to the test suite. A
capability probe compares that origin with loopback during verification.

Verification includes:

- focused unit tests;
- frontend lint;
- frontend production build;
- relevant Playwright voice tests when credentials and a voice channel are
  available;
- Compose configuration parsing if infrastructure files change;
- Playwright confirmation that localhost exposes microphone APIs while remote
  plain HTTP is rejected;
- the repository verification commands for affected areas.

## Documentation Design

Update `frontend/AGENTS.md` and
`documentation/architecture/02-deployment.md` to distinguish page reachability
from microphone capability:

- port 3000 is a development entry point;
- non-loopback plain HTTP can render the application but cannot use
  microphone-dependent voice;
- trusted HTTPS through the production domain is required for remote voice;
- a temporary remote development setup needs a trusted HTTPS proxy or tunnel;
- DNS, certificates, and router forwarding remain operator responsibilities.

The existing TURN runbooks remain authoritative for off-network media
forwarding. They require no change unless implementation inspection finds a
direct contradiction.

## Definition of Done

- An insecure or microphone-incapable browser receives the stable HTTPS error
  before any voice token request or LiveKit connection.
- Secure clients retain the current join behavior.
- Focused tests demonstrate the capability policy.
- Lint and production build pass.
- Documentation no longer implies remote plain-HTTP port 3000 supports voice.
- No unrelated worktree changes, secrets, external DNS, certificates, or router
  state are modified.
