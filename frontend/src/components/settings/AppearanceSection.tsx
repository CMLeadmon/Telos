"use client";

import { usePreferencesStore } from "@/stores/usePreferencesStore";
import type { Theme } from "@/stores/useThemeStore";

export function AppearanceSection() {
  const prefs = usePreferencesStore((s) => s.prefs);
  const save = usePreferencesStore((s) => s.save);

  return (
    <>
      <h2>Appearance</h2>
      <div className="setcard">
        <div className="setrow">
          <div className="lbl">
            <b>Theme</b>
            <span>Synced to your account across devices.</span>
          </div>
          <select
            value={prefs.theme}
            onChange={(e) => void save({ theme: e.target.value as Theme })}
            aria-label="theme"
          >
            <option value="synthwave">Synthwave</option>
            <option value="ink">Ink</option>
          </select>
        </div>
        <hr className="hr" />
        <div className="setrow">
          <div className="lbl">
            <b>Background scene</b>
            <span>The animated vaporwave backdrop on module pages.</span>
          </div>
          <input
            type="checkbox"
            className="setswitch"
            checked={prefs.sceneEnabled}
            onChange={(e) => void save({ sceneEnabled: e.target.checked })}
            aria-label="background scene"
          />
        </div>
        <hr className="hr" />
        <div className="setrow">
          <div className="lbl">
            <b>Reduce motion</b>
            <span>Minimize animations and transitions across the app.</span>
          </div>
          <input
            type="checkbox"
            className="setswitch"
            checked={prefs.reducedMotion}
            onChange={(e) => void save({ reducedMotion: e.target.checked })}
            aria-label="reduce motion"
          />
        </div>
      </div>
    </>
  );
}
