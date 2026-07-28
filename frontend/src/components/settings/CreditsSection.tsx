"use client";

import { ExternalLink, Scale } from "lucide-react";

export type CreditEntry = {
  name: string;
  role: string;
  sourceUrl: string;
  licenseName: string;
  licenseUrl: string;
};

// Reviewed static projection of the canonical matrix in CREDITS.md — keep the
// two in agreement (scripts/check-release-truth.sh verifies).
const ISOLATED_SERVICES: CreditEntry[] = [
  {
    name: "Jellyfin",
    role: "Media transcoding, HLS generation, and streaming (isolated service)",
    sourceUrl: "https://github.com/jellyfin/jellyfin",
    licenseName: "GPL-2.0",
    licenseUrl: "https://github.com/jellyfin/jellyfin/blob/master/LICENSE",
  },
  {
    name: "Grimmory",
    role: "E-book catalog, metadata, and library management (isolated service)",
    sourceUrl: "https://github.com/grimmory-tools/grimmory",
    licenseName: "AGPL-3.0",
    licenseUrl: "https://www.gnu.org/licenses/agpl-3.0.html",
  },
  {
    name: "ClamAV",
    role: "Malware scanning for uploads (isolated service)",
    sourceUrl: "https://github.com/Cisco-Talos/clamav",
    licenseName: "GPL-2.0",
    licenseUrl: "https://github.com/Cisco-Talos/clamav/blob/main/COPYING.txt",
  },
];

const INFRASTRUCTURE: CreditEntry[] = [
  {
    name: "Traefik",
    role: "Edge router, TLS termination, single-origin ingress",
    sourceUrl: "https://github.com/traefik/traefik",
    licenseName: "MIT",
    licenseUrl: "https://github.com/traefik/traefik/blob/master/LICENSE.md",
  },
  {
    name: "PostgreSQL",
    role: "Authoritative durable data store",
    sourceUrl: "https://github.com/postgres/postgres",
    licenseName: "PostgreSQL License",
    licenseUrl: "https://www.postgresql.org/about/licence/",
  },
  {
    name: "Redis",
    role: "Rebuildable cache, presence, pub/sub, rate limiting",
    sourceUrl: "https://github.com/redis/redis",
    licenseName: "RSALv2/SSPLv1",
    licenseUrl: "https://github.com/redis/redis/blob/unstable/LICENSE.txt",
  },
  {
    name: "MariaDB",
    role: "Grimmory's database (isolated with Grimmory)",
    sourceUrl: "https://github.com/MariaDB/server",
    licenseName: "GPL-2.0",
    licenseUrl: "https://github.com/MariaDB/server/blob/main/COPYING",
  },
];

function CreditRow({ entry }: { entry: CreditEntry }) {
  return (
    <li
      className="creditrow"
      data-testid={`credit-${entry.name.toLowerCase()}`}
    >
      <div className="creditmain">
        <span className="creditname">{entry.name}</span>
        <span className="creditrole">{entry.role}</span>
      </div>
      <div className="creditlinks">
        <a href={entry.sourceUrl} target="_blank" rel="noreferrer">
          <ExternalLink size={13} aria-hidden /> Source
        </a>
        <a href={entry.licenseUrl} target="_blank" rel="noreferrer">
          <Scale size={13} aria-hidden /> {entry.licenseName} license
        </a>
      </div>
    </li>
  );
}

export function CreditsSection() {
  return (
    <section className="creditsec" data-testid="credits-section">
      <h2>Credits</h2>
      <p className="creditintro">
        The Telos gateway and client are licensed under the{" "}
        <a
          href="https://www.apache.org/licenses/LICENSE-2.0"
          target="_blank"
          rel="noreferrer"
        >
          Apache License 2.0
        </a>
        . Telos exists because of the independent projects below; each isolated
        service runs as a separate process behind a container boundary under
        its own license.
      </p>
      <h3 className="grouplabel">Isolated services</h3>
      <ul className="creditlist">
        {ISOLATED_SERVICES.map((entry) => (
          <CreditRow key={entry.name} entry={entry} />
        ))}
      </ul>
      <h3 className="grouplabel">Infrastructure</h3>
      <ul className="creditlist">
        {INFRASTRUCTURE.map((entry) => (
          <CreditRow key={entry.name} entry={entry} />
        ))}
      </ul>
    </section>
  );
}
