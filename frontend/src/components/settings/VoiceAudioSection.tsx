"use client";

import { useState } from "react";
import { Room } from "livekit-client";
import { usePreferencesStore } from "@/stores/usePreferencesStore";
import { useVoiceSessionStore } from "@/stores/useVoiceSessionStore";
import { useMicTest } from "@/hooks/useMicTest";

export function VoiceAudioSection() {
  const prefs = usePreferencesStore((s) => s.prefs);
  const save = usePreferencesStore((s) => s.save);
  const setInputDevice = useVoiceSessionStore((s) => s.setInputDevice);
  const setOutputDevice = useVoiceSessionStore((s) => s.setOutputDevice);
  const mic = useMicTest();

  const [inputs, setInputs] = useState<MediaDeviceInfo[]>([]);
  const [outputs, setOutputs] = useState<MediaDeviceInfo[]>([]);
  const [devicesLoaded, setDevicesLoaded] = useState(false);
  const [deviceError, setDeviceError] = useState<string | null>(null);

  // Device labels are only exposed after a granted getUserMedia, so listing
  // is behind an explicit button instead of running on mount.
  const loadDevices = () => {
    Promise.all([
      Room.getLocalDevices("audioinput", true),
      Room.getLocalDevices("audiooutput", false),
    ])
      .then(([ins, outs]) => {
        setInputs(ins);
        setOutputs(outs);
        setDevicesLoaded(true);
        setDeviceError(null);
      })
      .catch(() =>
        setDeviceError(
          "Microphone access denied — check browser permissions.",
        ),
      );
  };

  const testOpts = {
    noiseSuppression: prefs.voiceNoiseSuppression,
    echoCancellation: prefs.voiceEchoCancellation,
    autoGainControl: prefs.voiceAutoGainControl,
  };

  return (
    <>
      <h2>Voice &amp; Audio</h2>
      <div className="setcard">
        {!devicesLoaded ? (
          <div className="setrow">
            <div className="lbl">
              <b>Audio devices</b>
              <span>
                Grant microphone access to list your input and output devices.
              </span>
            </div>
            <button
              className="btn cyan btn-sm"
              data-testid="voice-devices-enable"
              onClick={loadDevices}
            >
              Enable microphone access
            </button>
          </div>
        ) : (
          <>
            <div className="setrow">
              <div className="lbl">
                <b>Input device</b>
                <span>Which microphone Telos captures.</span>
              </div>
              <select
                data-testid="voice-input-select"
                aria-label="input device"
                value={prefs.voiceInputDeviceId}
                onChange={(e) => void setInputDevice(e.target.value)}
              >
                <option value="">Default</option>
                {inputs.map((d) => (
                  <option key={d.deviceId} value={d.deviceId}>
                    {d.label || d.deviceId.slice(0, 12)}
                  </option>
                ))}
              </select>
            </div>
            <hr className="hr" />
            <div className="setrow">
              <div className="lbl">
                <b>Output device</b>
                <span>Where voice audio plays.</span>
              </div>
              <select
                data-testid="voice-output-select"
                aria-label="output device"
                value={prefs.voiceOutputDeviceId}
                onChange={(e) => void setOutputDevice(e.target.value)}
              >
                <option value="">Default</option>
                {outputs.map((d) => (
                  <option key={d.deviceId} value={d.deviceId}>
                    {d.label || d.deviceId.slice(0, 12)}
                  </option>
                ))}
              </select>
            </div>
          </>
        )}
        {deviceError && <p className="setmsg err">{deviceError}</p>}
        <hr className="hr" />
        <div className="setrow">
          <div className="lbl">
            <b>Input gain</b>
            <span>Microphone boost. Applies live while in a call.</span>
          </div>
          <input
            type="range"
            aria-label="input gain"
            min={0}
            max={2}
            step={0.05}
            value={prefs.voiceInputGain}
            onChange={(e) =>
              void save({ voiceInputGain: Number(e.target.value) })
            }
          />
        </div>
        <hr className="hr" />
        <div className="setrow">
          <div className="lbl">
            <b>Output volume</b>
            <span>Volume of everyone else in the call.</span>
          </div>
          <input
            type="range"
            aria-label="output volume"
            min={0}
            max={1}
            step={0.05}
            value={prefs.voiceOutputVolume}
            onChange={(e) =>
              void save({ voiceOutputVolume: Number(e.target.value) })
            }
          />
        </div>
        <hr className="hr" />
        <div className="setrow">
          <div className="lbl">
            <b>Noise suppression</b>
            <span>Filter background noise from your microphone.</span>
          </div>
          <input
            type="checkbox"
            className="setswitch"
            aria-label="noise suppression"
            checked={prefs.voiceNoiseSuppression}
            onChange={(e) =>
              void save({ voiceNoiseSuppression: e.target.checked })
            }
          />
        </div>
        <hr className="hr" />
        <div className="setrow">
          <div className="lbl">
            <b>Echo cancellation</b>
            <span>Prevent your speakers from feeding back into the mic.</span>
          </div>
          <input
            type="checkbox"
            className="setswitch"
            aria-label="echo cancellation"
            checked={prefs.voiceEchoCancellation}
            onChange={(e) =>
              void save({ voiceEchoCancellation: e.target.checked })
            }
          />
        </div>
        <hr className="hr" />
        <div className="setrow">
          <div className="lbl">
            <b>Auto gain control</b>
            <span>Let the browser normalize your mic level.</span>
          </div>
          <input
            type="checkbox"
            className="setswitch"
            aria-label="auto gain control"
            checked={prefs.voiceAutoGainControl}
            onChange={(e) =>
              void save({ voiceAutoGainControl: e.target.checked })
            }
          />
        </div>
      </div>

      <h2>Mic test</h2>
      <div className="setcard">
        <div className="setrow">
          <div className="lbl">
            <b>Level</b>
            <span>Speak — the bar should move.</span>
          </div>
          <button
            className="btn cyan btn-sm"
            aria-label="mic test"
            onClick={() =>
              mic.running
                ? mic.stop()
                : void mic.start(prefs.voiceInputDeviceId, testOpts)
            }
          >
            {mic.running ? "Stop test" : "Start test"}
          </button>
        </div>
        <div
          className="micmeter"
          data-testid="voice-mic-meter"
          data-level={mic.level.toFixed(2)}
        >
          <div
            className="micmeter-fill"
            style={{ width: `${Math.round(mic.level * 100)}%` }}
          />
        </div>
        {mic.running && (
          <div className="setrow">
            <div className="lbl">
              <b>Loopback</b>
              <span>Hear your own mic through the selected output.</span>
            </div>
            <input
              type="checkbox"
              className="setswitch"
              aria-label="loopback test"
              checked={mic.loopback}
              onChange={(e) =>
                void mic.setLoopback(
                  e.target.checked,
                  prefs.voiceOutputDeviceId,
                )
              }
            />
          </div>
        )}
        {mic.error && <p className="setmsg err">{mic.error}</p>}
      </div>
    </>
  );
}
