"use client";

import React, { useState } from "react";
import { Search } from "lucide-react";

const EMOJI_CATEGORIES = [
  {
    name: "Recent & Popular",
    emojis: ["🔥", "📖", "🌊", "✅", "👍", "😂", "🎉", "❤️", "⭐", "🚀", "💻", "💡"],
  },
  {
    name: "Smileys",
    emojis: ["😀", "😃", "😄", "😁", "😆", "😅", "😂", "🤣", "😊", "😇", "🙂", "🙃", "😉", "😌", "😍", "🥰", "😘", "😗", "😙", "😚", "😋", "😛", "😝", "😜", "🤪", "🤨", "🧐", "🤓", "😎", "🥸", "🤩", "🥳", "😏", "😒", "😞", "😔", "😟", "😕", "🙁", "☹️", "😣", "😖", "😫", "😩", "🥺", "😢", "😭", "😤", "😠", "😡", "🤬", "🤯", "😳", "🥵", "🥶", "😱", "😨", "😰", "😥", "😓", "🤫", "🫠"],
  },
  {
    name: "Gestures",
    emojis: ["👋", "🤚", "🖐️", "✋", "🖖", "👌", "🤌", "🤏", "✌️", "🤞", "🫰", "🤟", "🤘", "🤙", "👈", "👉", "👆", "🖕", "👇", "☝️", "👍", "👎", "✊", "👊", "🤛", "🤜", "👏", "🙌", "👐", "🤲", "🤝", "🙏", "✍️", "💅", "🤳", "💪", "🦾"],
  },
  {
    name: "Symbols",
    emojis: ["❤️", "🧡", "💛", "💚", "💙", "💜", "🖤", "🤍", "🤎", "💔", "❤️‍🔥", "❤️‍🩹", "❣️", "💕", "💞", "💓", "💗", "💖", "💘", "💝", "💟", "🔯", "⭐", "🌟", "✨", "⚡", "💥", "🔥", "🌈", "☀️", "🌙", "🪐", "🌍", "💡", "🛡️", "🔮", "🛎️", "⚙️", "🔧", "🔑", "✉️", "📫"],
  }
];

const EMOJI_NAMES: Record<string, string> = {
  "🔥": "fire hot lit burn", "📖": "book read library grimmory", "🌊": "wave water ocean stream",
  "✅": "check correct yes pass ok done", "👍": "thumbs up like good yes ok", "😂": "laugh cry lol haha",
  "🎉": "celebrate party congrats woo", "❤️": "heart love red", "⭐": "star gold favorite",
  "🚀": "rocket space launch fast fly", "💻": "computer laptop dev code tech", "💡": "bulb light idea smart",
  "😀": "smile happy joy face", "😃": "smile happy joy face", "😄": "smile happy joy face",
  "😁": "smile happy joy face grin", "😆": "smile happy joy face laugh", "😅": "smile sweat nervous face",
  "🤣": "laugh rotfl roll face", "😊": "smile happy content face blush", "😇": "angel halo innocent face",
  "🙂": "slight smile face", "🙃": "upside down face", "😉": "wink face", "😌": "relieved calm face",
  "😍": "heart eyes love face", "🥰": "hearts blushing love face", "😘": "kiss face blow",
  "😜": "wink tongue crazy face", "😎": "cool sunglasses face", "🥸": "glasses face mustache disguise",
  "🤩": "star eyes face", "🥳": "party celebrate face", "😏": "smirk face", "😒": "unamused face",
  "😞": "disappointed face", "😔": "pensive sad face", "😟": "worried face", "😕": "confused face",
  "🙁": "frown face", "☹️": "frown face", "😣": "persevere face", "😖": "confounded face",
  "😫": "tired face", "😩": "weary face", "🥺": "pleading beg eyes face", "😢": "cry sad tear face",
  "😭": "cry sob sad face", "😤": "triumph nose steam face", "😠": "angry mad face",
  "😡": "angry mad face red", "🤬": "symbols mouth curse swear face", "🤯": "explode head mind blown face",
  "😳": "flushed embarrassed face", "🥵": "hot red face sweat", "🥶": "cold blue face ice",
  "😱": "scream shock fear face", "👋": "wave hello goodbye hand",
  "👎": "thumbs down dislike bad no", "👏": "clap hand", "🙏": "please pray thanks hand",
  "🤝": "shake hands support deal",
};

interface EmojiPickerProps {
  onPick: (emoji: string) => void;
  onClose: () => void;
}

export function EmojiPicker({ onPick, onClose }: EmojiPickerProps) {
  const [search, setSearch] = useState("");

  const filteredCategories = EMOJI_CATEGORIES.map((cat) => {
    if (!search) return cat;
    const lower = search.toLowerCase();
    const matches = cat.emojis.filter((emoji) => {
      const name = EMOJI_NAMES[emoji] || "";
      return name.includes(lower) || cat.name.toLowerCase().includes(lower);
    });
    return { name: cat.name, emojis: matches };
  }).filter((cat) => cat.emojis.length > 0);

  return (
    <>
      <div
        style={{
          position: "fixed",
          inset: 0,
          zIndex: 990,
          background: "transparent",
        }}
        onClick={onClose}
      />
      <div
        className="emoji-picker"
        style={{
          position: "absolute",
          bottom: "100%",
          right: 0,
          marginBottom: "12px",
          width: "280px",
          maxHeight: "320px",
          background: "var(--surface-2)",
          border: "1px solid var(--line)",
          borderRadius: "var(--r)",
          display: "flex",
          flexDirection: "column",
          zIndex: 995,
          overflow: "hidden",
          backdropFilter: "blur(12px)",
        }}
      >
        <div
          style={{
            padding: "10px",
            borderBottom: "1px solid var(--line-2)",
            display: "flex",
            alignItems: "center",
            gap: "8px",
            background: "var(--surface)",
          }}
        >
          <Search size={14} style={{ color: "var(--faint)", flex: "none" }} />
          <input
            type="text"
            placeholder="Search emoji..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            style={{
              flex: 1,
              background: "transparent",
              border: "none",
              outline: "none",
              color: "var(--ink)",
              fontSize: "12.5px",
            }}
            autoFocus
          />
        </div>
        <div
          style={{
            flex: 1,
            overflowY: "auto",
            padding: "8px 12px",
            display: "flex",
            flexDirection: "column",
            gap: "10px",
          }}
        >
          {filteredCategories.length === 0 ? (
            <div
              style={{
                padding: "20px",
                textAlign: "center",
                color: "var(--faint)",
                fontSize: "12px",
              }}
            >
              No emojis found
            </div>
          ) : (
            filteredCategories.map((cat) => (
              <div key={cat.name}>
                <div
                  style={{
                    fontSize: "10px",
                    fontWeight: 600,
                    textTransform: "uppercase",
                    letterSpacing: "0.06em",
                    color: "var(--faint)",
                    marginBottom: "6px",
                  }}
                >
                  {cat.name}
                </div>
                <div
                  style={{
                    display: "grid",
                    gridTemplateColumns: "repeat(8, 1fr)",
                    gap: "4px",
                  }}
                >
                  {cat.emojis.map((emoji) => (
                    <button
                      key={emoji}
                      onClick={() => {
                        onPick(emoji);
                        onClose();
                      }}
                      style={{
                        background: "transparent",
                        border: "none",
                        fontSize: "20px",
                        padding: "4px",
                        cursor: "pointer",
                        borderRadius: "var(--r-xs)",
                        display: "flex",
                        alignItems: "center",
                        justifyContent: "center",
                        transition: "background 0.1s",
                      }}
                      className="emoji-btn"
                    >
                      {emoji}
                    </button>
                  ))}
                </div>
              </div>
            ))
          )}
        </div>
      </div>
    </>
  );
}
