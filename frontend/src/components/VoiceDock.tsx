"use client";

import { useState } from "react";
import {
  Headphones,
  HeadphoneOff,
  Mic,
  MicOff,
  PhoneOff,
  Settings2,
} from "lucide-react";
import { Room } from "livekit-client";
import { useVoiceSessionStore } from "@/stores/useVoiceSessionStore";
import { useChatSessionStore } from "@/stores/useChatSessionStore";
import { usePreferencesStore } from "@/stores/usePreferencesStore";

export function VoiceDock() {
  const voice = useVoiceSessionStore();
  const channels = useChatSessionStore((s) => s.channels);
  const inputDeviceId = usePreferencesStore((s) => s.prefs.voiceInputDeviceId);
  const [devicesOpen, setDevicesOpen] = useState(false);
  const [devices, setDevices] = useState<MediaDeviceInfo[]>([]);

  if (voice.status === "idle") return null;

  const channelName =
    channels.find((c) => c.id === voice.channelId)?.name ?? voice.channelId;

  if (voice.status === "error") {
    return (
      <div className="voicedock voicedock--error" data-testid="voice-dock">
        <div className="vderr" data-testid="voice-dock-error">
          {voice.error}
        </div>
        <div className="vdrow">
          {voice.channelId && (
            <button
              className="vdbtn wide"
              onClick={() => void voice.join(voice.channelId!)}
            >
              Retry
            </button>
          )}
          <button className="vdbtn wide" onClick={voice.dismissError}>
            Dismiss
          </button>
        </div>
      </div>
    );
  }

  const openDevices = () => {
    const next = !devicesOpen;
    setDevicesOpen(next);
    if (next) {
      Room.getLocalDevices("audioinput", true)
        .then(setDevices)
        .catch(() => setDevices([]));
    }
  };

  return (
    <div className="voicedock" data-testid="voice-dock">
      <div className="vdhead">
        <span className={`vddot${voice.status === "connected" ? " ok" : ""}`} />
        <span className="vdchan">{channelName}</span>
        <span className="vdstatus">
          {voice.status === "connected" ? "voice connected" : "connecting…"}
        </span>
      </div>
      {voice.participants.length > 0 && (
        <ul className="vdppl">
          {voice.participants.map((p) => (
            <li key={p.identity} className={p.speaking ? "speaking" : ""}>
              {p.identity}
            </li>
          ))}
        </ul>
      )}
      {devicesOpen && (
        <select
          className="vddevices"
          aria-label="voice input device"
          value={inputDeviceId}
          onChange={(e) => {
            void voice.setInputDevice(e.target.value);
            setDevicesOpen(false);
          }}
        >
          <option value="">Default microphone</option>
          {devices.map((d) => (
            <option key={d.deviceId} value={d.deviceId}>
              {d.label || d.deviceId.slice(0, 12)}
            </option>
          ))}
        </select>
      )}
      <div className="vdrow">
        <button
          className={`vdbtn${voice.micMuted ? " off" : ""}`}
          aria-label={voice.micMuted ? "unmute microphone" : "mute microphone"}
          onClick={() => void voice.toggleMute()}
        >
          {voice.micMuted ? <MicOff size={16} /> : <Mic size={16} />}
        </button>
        <button
          className={`vdbtn${voice.deafened ? " off" : ""}`}
          aria-label={voice.deafened ? "undeafen" : "deafen"}
          onClick={() => void voice.toggleDeafen()}
        >
          {voice.deafened ? (
            <HeadphoneOff size={16} />
          ) : (
            <Headphones size={16} />
          )}
        </button>
        <button
          className="vdbtn"
          aria-label="voice devices"
          onClick={openDevices}
        >
          <Settings2 size={16} />
        </button>
        <button
          className="vdbtn danger"
          aria-label="leave voice"
          onClick={() => void voice.leave()}
        >
          <PhoneOff size={16} />
        </button>
      </div>
    </div>
  );
}
