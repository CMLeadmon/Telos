"use client";

import React from "react";
import { openExternal } from "@/lib/platform";

/**
 * A link that leaves Telos.
 *
 * In a browser this behaves as an ordinary anchor. In the native client a plain
 * anchor navigates the one app window away from Telos, and a webview has no
 * back button — the member ends up on someone else's page with no way home and
 * has to force-quit. The href is kept so that right-click, copy-link, middle-
 * click and the web build all still work; only the plain click is redirected to
 * the operating system's browser.
 */
export function ExternalLink({
  href,
  children,
  ...rest
}: React.AnchorHTMLAttributes<HTMLAnchorElement> & { href: string }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer noopener"
      {...rest}
      onClick={(e) => {
        // Leave modified clicks alone: they are the member deliberately asking
        // for a new tab or a saved link, and the platform handles them.
        if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) {
          return;
        }
        e.preventDefault();
        void openExternal(href);
      }}
    >
      {children}
    </a>
  );
}
