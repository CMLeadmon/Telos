"use client";

// The sanctioned decoration: retro sun + perspective grid + glowline + palms.
// Renders flat (rose sun, faint navy grid) automatically under data-theme="ink".
// Users can turn it off entirely via Settings → Appearance.

import { usePreferencesStore } from "@/stores/usePreferencesStore";

const PALM_PATHS = (
  <>
    <path d="M118 300 L110 190 C108 160 112 140 118 128 L126 128 C124 150 122 170 126 190 L134 300 Z" />
    <path d="M120 130 C90 110 60 108 30 122 C62 96 100 98 122 118 Z" />
    <path d="M122 128 C100 96 70 82 36 84 C74 64 112 84 126 116 Z" />
    <path d="M124 126 C124 90 140 60 172 46 C148 78 140 104 138 126 Z" />
    <path d="M126 128 C150 104 184 96 214 106 C182 112 154 122 136 134 Z" />
    <path d="M124 124 C132 98 156 78 190 74 C160 92 142 110 134 128 Z" />
  </>
);

export function VaporwaveScene() {
  const sceneEnabled = usePreferencesStore((s) => s.prefs.sceneEnabled);
  if (!sceneEnabled) return null;
  return (
    <div className="scene" aria-hidden="true">
      <div className="sun" />
      <div className="glowline" />
      <div className="grid" />
      <svg className="palm l" viewBox="0 0 240 300" fill="currentColor">
        {PALM_PATHS}
      </svg>
      <svg className="palm r" viewBox="0 0 240 300" fill="currentColor">
        {PALM_PATHS}
      </svg>
    </div>
  );
}
