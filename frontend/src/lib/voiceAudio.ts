// Mic capture pipeline. LiveKit has no native input gain, so the published
// microphone track is the output of getUserMedia -> GainNode -> destination.

export interface MicPipelineOptions {
  deviceId: string;
  gain: number;
  noiseSuppression: boolean;
  echoCancellation: boolean;
  autoGainControl: boolean;
}

export interface MicPipeline {
  track: MediaStreamTrack;
  setGain: (v: number) => void;
  stop: () => void;
}

export function micConstraints(opts: {
  deviceId: string;
  noiseSuppression: boolean;
  echoCancellation: boolean;
  autoGainControl: boolean;
}): MediaTrackConstraints {
  return {
    // "ideal" so a stale per-machine device ID falls back to the default mic.
    ...(opts.deviceId ? { deviceId: { ideal: opts.deviceId } } : {}),
    noiseSuppression: opts.noiseSuppression,
    echoCancellation: opts.echoCancellation,
    autoGainControl: opts.autoGainControl,
  };
}

export async function createMicPipeline(
  opts: MicPipelineOptions,
): Promise<MicPipeline> {
  const stream = await navigator.mediaDevices.getUserMedia({
    audio: micConstraints(opts),
  });
  const ctx = new AudioContext();
  await ctx.resume();
  const source = ctx.createMediaStreamSource(stream);
  const gain = ctx.createGain();
  gain.gain.value = opts.gain;
  const dest = ctx.createMediaStreamDestination();
  source.connect(gain);
  gain.connect(dest);
  return {
    track: dest.stream.getAudioTracks()[0],
    setGain: (v) => {
      gain.gain.value = v;
    },
    stop: () => {
      stream.getTracks().forEach((t) => t.stop());
      dest.stream.getTracks().forEach((t) => t.stop());
      void ctx.close();
    },
  };
}

export function voiceErrorMessage(err: unknown): string {
  if (err instanceof DOMException) {
    if (err.name === "NotAllowedError" || err.name === "SecurityError") {
      return "Microphone access denied — check browser permissions.";
    }
    if (err.name === "NotFoundError" || err.name === "OverconstrainedError") {
      return "No microphone found.";
    }
  }
  return err instanceof Error ? err.message : "voice connection failed";
}
