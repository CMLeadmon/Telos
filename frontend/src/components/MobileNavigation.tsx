"use client";


import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import {
  BookOpen,
  Folder,
  Hash,
  MessageSquare,
  Tv,
  X,
} from "lucide-react";
import { useAuthStore } from "@/stores/useAuthStore";
import { useChatSessionStore } from "@/stores/useChatSessionStore";
import { useMobileNavStore } from "@/stores/useMobileNavStore";
import { hasCapability } from "@/lib/capabilities";

// Four surfaces, which is also the DS's specified mobile tabbar size.
const MODULES = [
  { href: "/chat", label: "Chat", icon: MessageSquare, capability: "view_channel" },
  { href: "/stream", label: "Stream", icon: Tv, capability: "view_media" },
  { href: "/library", label: "Library", icon: BookOpen, capability: "view_library" },
  { href: "/files", label: "Files", icon: Folder, capability: "view_files" },
];

export function MobileNavigation() {
  const pathname = usePathname();
  const router = useRouter();
  const user = useAuthStore((s) => s.user);
  const { channels, activeChannelId, connect } = useChatSessionStore();

  // Opened from the chat header's back affordance, so the state is shared.
  const drawerOpen = useMobileNavStore((s) => s.channelDrawerOpen);
  const closeDrawer = useMobileNavStore((s) => s.closeChannelDrawer);

  const onChat = pathname.startsWith("/chat");

  const visibleModules = MODULES.filter((m) => hasCapability(user, m.capability));

  return (
    <>
      {/* Mobile Bottom Navigation Bar */}
      <nav
        className="mobile-bottom-nav"
        data-testid="mobile-bottom-nav"
        aria-label="Mobile Navigation"
      >
        {visibleModules.map(({ href, label, icon: Icon }) => (
          <Link
            key={href}
            href={href}
            className={`mobile-nav-btn${pathname.startsWith(href) ? " on" : ""}`}
            aria-label={label}
          >
            <Icon size={20} />
            <span className="mobile-nav-label">{label}</span>
          </Link>
        ))}
      </nav>

      {/* Mobile Channels Drawer Overlay */}
      {drawerOpen && (
        <div
          className="mobile-drawer-backdrop"
          onClick={() => closeDrawer()}
          data-testid="mobile-drawer-backdrop"
        >
          <div
            className="mobile-drawer"
            onClick={(e) => e.stopPropagation()}
            data-testid="mobile-channels-drawer"
            role="dialog"
            aria-label="Channels Drawer"
          >
            <div className="mobile-drawer-header">
              <h3>Channels</h3>
              <button
                className="iconbtn"
                onClick={() => closeDrawer()}
                aria-label="Close channels drawer"
              >
                <X size={18} />
              </button>
            </div>
            <div className="mobile-drawer-content">
              {channels.length > 0 && (
                <div className="mobile-drawer-section">
                  <h4 className="grouplabel">Channels</h4>
                  <div className="chanlist">
                    {channels.map((c) => (
                      <button
                        key={c.id}
                        className={`chan${onChat && c.id === activeChannelId ? " on" : ""}`}
                        onClick={() => {
                          if (!onChat) router.push("/chat/");
                          connect(c.id);
                          closeDrawer();
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
            </div>
          </div>
        </div>
      )}
    </>
  );
}
