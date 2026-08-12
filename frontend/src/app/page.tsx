"use client";

import Link from "next/link";
import {
  BookOpen,
  Github,
  MessageSquare,
  Rocket,
  Terminal,
  Tv,
} from "lucide-react";
import { BrandLogo } from "@/components/BrandLogo";
import { ExternalLink } from "@/components/ExternalLink";
import { useThemeStore } from "@/stores/useThemeStore";
import { useConnectionStore } from "@/stores/useConnectionStore";
import { requiresServerSelection } from "@/lib/serverConfig";
import { VaporwaveScene } from "@/components/VaporwaveScene";

export default function LandingPage() {
  // The ink mockups set the wordmark in title case, the synthwave ones in caps.
  const wordmark = useThemeStore((s) => (s.theme === "ink" ? "Telos" : "TELOS"));
  // A native shell has to be told which node to talk to before it can show a
  // login form. A web build is already being served by its node, so sending it
  // to /connect would ask which server it is currently talking to.
  const connectionState = useConnectionStore((s) => s.state);
  const launchHref =
    connectionState === "configured" || !requiresServerSelection()
      ? "/login/"
      : "/connect/";
  return (
    <div className="page">
      <nav className="lnav">
        <div className="brand">
          <BrandLogo size={67} />
          <span className="word">{wordmark}</span>
        </div>
        <div className="navlinks">
          <a href="#product">Product</a>
          <a href="#sovereign">Self-host</a>
          <ExternalLink href="https://github.com/CMLeadmon/Telos">
            Docs
          </ExternalLink>
        </div>
        <div className="right">
          <Link className="btn rose btn-sm" href={launchHref}>
            Launch app
          </Link>
        </div>
      </nav>

      <header className="hero">
        <VaporwaveScene />
        <div className="scrim" aria-hidden="true" />
        <div className="inner">
          <span className="kicker">Open-source · Self-hosted · Sovereign</span>
          <h1>
            Read, watch and <span className="glow">discuss</span>
            {" — on a server that's yours."}
          </h1>
          <p className="sub">
            Telos is one place for your community&apos;s books, films and
            conversations. Bootstrapped from Jellyfin and friends, owned by no
            one but you.
          </p>
          <div className="cta">
            <Link className="btn rose btn-lg" href={launchHref}>
              <Rocket size={17} /> Launch your node
            </Link>
            <ExternalLink
              className="btn-ghost btn-lg"
              href="https://github.com/CMLeadmon/Telos"
            >
              <Github size={17} /> Star on GitHub
            </ExternalLink>
          </div>
          <div className="slog">be on the net, but not of the net</div>
        </div>
      </header>

      <section className="wrap" id="product">
        <div className="triad">
          <div className="feat">
            <span className="num">01</span>
            <div
              className="ic"
              style={{ background: "rgba(255,8,128,.14)", color: "var(--rose)" }}
            >
              <MessageSquare size={26} />
            </div>
            <h3>Chat</h3>
            <p>
              Group messaging built for talking about what you&apos;re reading
              and watching. Threaded conversation, reactions, and pins keep the
              discussion in one place.
            </p>
          </div>
          <div className="feat">
            <span className="num">02</span>
            <div
              className="ic"
              style={{
                background: "rgba(192,60,254,.14)",
                color: "var(--violet)",
              }}
            >
              <Tv size={26} />
            </div>
            <h3>Stream</h3>
            <p>
              Your media server, reimagined. Stream films and shows from
              Jellyfin with a front end that doesn&apos;t feel like a database.
            </p>
          </div>
          <div className="feat">
            <span className="num">03</span>
            <div
              className="ic"
              style={{ background: "rgba(2,181,255,.14)", color: "var(--cyan)" }}
            >
              <BookOpen size={26} />
            </div>
            <h3>Library</h3>
            <p>
              EPUBs, PDFs and audiobooks in one shelf. Read in-app, keep
              private notes, and share annotations with your community when
              you choose.
            </p>
          </div>
        </div>
      </section>

      <section className="band" id="sovereign">
        <div className="wrap">
          <div>
            <span className="kicker">Why self-host</span>
            <h2>
              No landlord. No feed.
              <br />
              No one selling your shelf.
            </h2>
            <p>
              Telos runs on a box you control — a homelab, a VPS, an old
              laptop. Your friends join over direct HTTPS, or a private
              network you operate. External calls — metadata lookups and
              encrypted off-node backups — happen only when you enable them.
            </p>
            <div className="cta">
              <ExternalLink
                className="btn cyan"
                href="https://github.com/CMLeadmon/Telos/blob/main/documentation/architecture/02-deployment.md"
              >
                <Terminal size={16} /> Read the deploy guide
              </ExternalLink>
            </div>
          </div>
          <div className="specgrid">
            <div className="cell">
              <div className="k">Bootstraps from</div>
              <div className="v rose">Jellyfin</div>
            </div>
            <div className="cell">
              <div className="k">Transport</div>
              <div className="v cyan">Encrypted</div>
            </div>
            <div className="cell">
              <div className="k">Formats</div>
              <div className="v violet">EPUB · PDF</div>
            </div>
            <div className="cell">
              <div className="k">License</div>
              <div className="v indigo">Apache-2.0</div>
            </div>
          </div>
        </div>
      </section>

      <section className="close">
        <div className="wrap">
          <span className="kicker">One command to begin</span>
          <h2>Bring your people home.</h2>
          <p>
            Spin up a Telos node in minutes and invite the group that actually
            matters.
          </p>
          <div className="cta">
            <Link className="btn rose btn-lg" href={launchHref}>
              <Rocket size={17} /> Launch your node
            </Link>
          </div>
        </div>
      </section>

      <footer>
        <div className="wrap">
          <div className="lockup">
            <BrandLogo size={29} />
            {wordmark}
          </div>
          <span className="mono" style={{ color: "var(--faint)", fontSize: 12 }}>
            node: telos-node-1
          </span>
        </div>
      </footer>
    </div>
  );
}
