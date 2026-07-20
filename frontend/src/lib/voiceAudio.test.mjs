import assert from "node:assert/strict";
import test from "node:test";

import {
  VOICE_HTTPS_REQUIRED_MESSAGE,
  voiceCaptureEnvironmentError,
} from "./voiceAudio.ts";

test("allows microphone capture from a secure capable environment", () => {
  assert.equal(
    voiceCaptureEnvironmentError({
      isSecureContext: true,
      hasGetUserMedia: true,
    }),
    null,
  );
});

test("requires HTTPS in an insecure context", () => {
  assert.equal(
    voiceCaptureEnvironmentError({
      isSecureContext: false,
      hasGetUserMedia: true,
    }),
    VOICE_HTTPS_REQUIRED_MESSAGE,
  );
});

test("requires HTTPS when getUserMedia is unavailable", () => {
  assert.equal(
    voiceCaptureEnvironmentError({
      isSecureContext: true,
      hasGetUserMedia: false,
    }),
    VOICE_HTTPS_REQUIRED_MESSAGE,
  );
});
