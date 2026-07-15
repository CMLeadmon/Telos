"use client";

import { useEffect } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import {
  Bell,
  BookOpen,
  Folder,
  Hash,
  LogOut,
  MessageSquare,
  Moon,
  Search,
  Settings,
  Sparkles,
  Sun,
  Tv,
  Volume2,
} from "lucide-react";
import { useAuthStore } from "@/stores/useAuthStore";
import { useChatSessionStore } from "@/stores/useChatSessionStore";
import { useVoiceSessionStore } from "@/stores/useVoiceSessionStore";
import { useThemeStore } from "@/stores/useThemeStore";
import { usePreferencesStore } from "@/stores/usePreferencesStore";
import { avatarUrl } from "@/lib/api";
import { BrandLogo } from "@/components/BrandLogo";
import { VoiceDock } from "@/components/VoiceDock";

const MODULES = [
  { href: "/chat", label: "Chat", icon: MessageSquare },
  { href: "/stream", label: "Stream", icon: Tv },
  { href: "/library", label: "Library", icon: BookOpen },
  { href: "/files", label: "Files", icon: Folder },
];

export function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const { user, status, fetchMe, logout } = useAuthStore();
  const theme = useThemeStore((s) => s.theme);
  // Theme toggling routes through usePreferencesStore so it persists server-side.
  const prefsLoaded = usePreferencesStore((s) => s.loaded);
  const loadPrefs = usePreferencesStore((s) => s.load);
  const { channels, activeChannelId, fetchChannels, connect } =
    useChatSessionStore();
  const voice = useVoiceSessionStore();

  useEffect(() => {
    if (status === "unknown") void fetchMe();
  }, [status, fetchMe]);

  useEffect(() => {
    if (status === "authenticated" && !prefsLoaded) void loadPrefs();
  }, [status, prefsLoaded, loadPrefs]);

  useEffect(() => {
    if (status === "anonymous") router.replace("/login/");
  }, [status, router]);

  useEffect(() => {
    if (status === "authenticated" && channels.length === 0) {
      fetchChannels().catch(() => {});
    }
  }, [status, channels.length, fetchChannels]);

  if (status !== "authenticated") {
    return (
      <div className="authwrap">
        <span className="kicker">{"// connecting to node…"}</span>
      </div>
    );
  }

  const textChannels = channels.filter((c) => c.type === "text");
  const voiceChannels = channels.filter((c) => c.type === "voice");
  const onChat = pathname.startsWith("/chat");

  return (
    <div className="app app--viewport" data-testid="app-shell">
      <div className="topbar">
        <div className="brand">
          <BrandLogo size={62} />
          <span className="word">TELOS</span>
        </div>
        <div className="searchbar">
          <Search size={16} style={{ position: "absolute", left: 14 }} />
          <input placeholder="Title, Author, Series, Genre, or Tags…" />
        </div>
        <div className="topacts">
          <span className="chip">
            {user?.HasAvatar ? (
              // eslint-disable-next-line @next/next/no-img-element
              <img className="chipav" src={avatarUrl(user.ID)} alt="" />
            ) : (
              <span className="dot" />
            )}{" "}
            {user?.DisplayName || user?.Username}
          </span>
          <button className="iconbtn" aria-label="oracle">
            <Sparkles size={18} />
          </button>
          <button className="iconbtn" aria-label="notifications">
            <Bell size={18} />
          </button>
          <Link
            href="/settings"
            className={`iconbtn${pathname.startsWith("/settings") ? " on" : ""}`}
            aria-label="settings"
          >
            <Settings size={18} />
          </Link>
          <button
            className="iconbtn"
            aria-label="toggle theme"
            onClick={() =>
              void usePreferencesStore
                .getState()
                .save({ theme: theme === "synthwave" ? "ink" : "synthwave" })
            }
          >
            {theme === "synthwave" ? <Sun size={18} /> : <Moon size={18} />}
          </button>
          <button
            className="iconbtn"
            aria-label="log out"
            onClick={() => void logout()}
          >
            <LogOut size={18} />
          </button>
        </div>
      </div>

      <div className="shellbody">
        <aside className="rail">
          <div className="scroll">
            <nav className="modnav">
              {MODULES.map(({ href, label, icon: Icon }) => (
                <Link
                  key={href}
                  href={href}
                  className={`navbtn${pathname.startsWith(href) ? " on" : ""}`}
                >
                  <Icon size={18} />
                  {label}
                </Link>
              ))}
            </nav>

            {textChannels.length > 0 && (
              <div>
                <h3 className="grouplabel">Text channels</h3>
                <div className="chanlist">
                  {textChannels.map((c) => (
                    <button
                      key={c.id}
                      className={`chan${
                        onChat && c.id === activeChannelId ? " on" : ""
                      }`}
                      onClick={() => {
                        if (!onChat) router.push("/chat/");
                        connect(c.id);
                      }}
                    >
                      <span className="l">
                        <Hash size={15} />
                        {c.name}
                      </span>
                    </button>
                  ))}
                </div>
              </div>
            )}

            {voiceChannels.length > 0 && (
              <div>
                <h3 className="grouplabel">Voice channels</h3>
                <div className="chanlist">
                  {voiceChannels.map((c) => {
                    const joined =
                      voice.status === "connected" && voice.channelId === c.id;
                    return (
                      <button
                        key={c.id}
                        className={`chan${joined ? " on" : ""}`}
                        onClick={() =>
                          joined ? void voice.leave() : void voice.join(c.id)
                        }
                      >
                        <span className="l">
                          <Volume2 size={15} />
                          {c.name}
                        </span>
                        <span className="joinlbl">
                          {joined
                            ? "Leave"
                            : voice.status === "connecting" &&
                                voice.channelId === c.id
                              ? "…"
                              : "Join"}
                        </span>
                      </button>
                    );
                  })}
                </div>
              </div>
            )}
          </div>
          <VoiceDock />
          <div className="railfoot">
            <div className="row">
              <span className="dot" style={{ width: 5, height: 5 }} /> tunnel:
              encrypted
            </div>
            <div className="row">node: telos-node-1</div>
          </div>
        </aside>

        <main className="arena">{children}</main>
      </div>

      <nav className="tabbar">
        {MODULES.map(({ href, label, icon: Icon }) => (
          <Link
            key={href}
            href={href}
            className={`tab${pathname.startsWith(href) ? " on" : ""}`}
          >
            <Icon size={20} />
            {label}
          </Link>
        ))}
      </nav>
    </div>
  );
}
