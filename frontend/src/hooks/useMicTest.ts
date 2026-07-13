"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { micConstraints, voiceErrorMessage } from "@/lib/voiceAudio";

interface MicTestOptions {
  noiseSuppression: boolean;
  echoCancellation: boolean;
  autoGainControl: boolean;
}

// Sink selection is not yet in the HTMLMediaElement lib type.
type SinkableAudio = HTMLAudioElement & {
  setSinkId?: (id: string) => Promise<void>;
};

export function useMicTest() {
  const [level, setLevel] = useState(0);
  const [running, setRunning] = useState(false);
  const [loopback, setLoopbackState] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const streamRef = useRef<MediaStream | null>(null);
  const ctxRef = useRef<AudioContext | null>(null);
  const rafRef = useRef(0);
  const audioRef = useRef<SinkableAudio | null>(null);

  const stop = useCallback(() => {
    cancelAnimationFrame(rafRef.current);
    streamRef.current?.getTracks().forEach((t) => t.stop());
    streamRef.current = null;
    if (audioRef.current) {
      audioRef.current.pause();
      audioRef.current.srcObject = null;
      audioRef.current = null;
    }
    void ctxRef.current?.close();
    ctxRef.current = null;
    setRunning(false);
    setLoopbackState(false);
    setLevel(0);
  }, []);

  useEffect(() => stop, [stop]); // teardown on unmount

  const start = useCallback(
    async (deviceId: string, opts: MicTestOptions) => {
      stop();
      setError(null);
      try {
        const stream = await navigator.mediaDevices.getUserMedia({
          audio: micConstraints({ deviceId, ...opts }),
        });
        streamRef.current = stream;
        const ctx = new AudioContext();
        ctxRef.current = ctx;
        await ctx.resume();
        const analyser = ctx.createAnalyser();
        analyser.fftSize = 512;
        ctx.createMediaStreamSource(stream).connect(analyser);
        const buf = new Uint8Array(analyser.fftSize);
        const tick = () => {
          analyser.getByteTimeDomainData(buf);
          let sum = 0;
          for (let i = 0; i < buf.length; i++) {
            const v = (buf[i] - 128) / 128;
            sum += v * v;
          }
          setLevel(Math.min(1, Math.sqrt(sum / buf.length) * 3));
          rafRef.current = requestAnimationFrame(tick);
        };
        rafRef.current = requestAnimationFrame(tick);
        setRunning(true);
      } catch (err) {
        setError(voiceErrorMessage(err));
      }
    },
    [stop],
  );

  const setLoopback = useCallback(
    async (on: boolean, outputDeviceId: string) => {
      if (!on) {
        if (audioRef.current) {
          audioRef.current.pause();
          audioRef.current.srcObject = null;
          audioRef.current = null;
        }
        setLoopbackState(false);
        return;
      }
      if (!streamRef.current) return;
      const audio = new Audio() as SinkableAudio;
      audio.srcObject = streamRef.current;
      if (outputDeviceId && audio.setSinkId) {
        await audio.setSinkId(outputDeviceId).catch(() => {});
      }
      await audio.play().catch(() => {});
      audioRef.current = audio;
      setLoopbackState(true);
    },
    [],
  );

  return { level, running, loopback, error, start, stop, setLoopback };
}
