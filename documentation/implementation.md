Systems Architecture and Integration Blueprint for TelosExecutive Summary and Core Architectural ParadigmThe modern self-hosted application landscape is highly fragmented. Users are forced to maintain disparate platforms for real-time communication, high-fidelity media streaming, digital catalog management, and raw file hosting. This fragmentation introduces substantial system-resource overhead, inconsistent client-side experiences, and configuration fatigue.The Telos platform solves these structural inefficiencies through process-level consolidation. By executing Strategy A (the Suite/Orchestration Approach), the architecture avoids rewriting specialized components such as video transcoding engines, document parsers, and low-latency packet forwarders. Instead, Telos operates as a cohesive, single-port gateway and master user interface wrapper. This wrapper coordinates dedicated, headless open-source services running in isolated container environments. To the end user, the interface appears completely native, seamless, and original.The core backend gateway is written in Go or Rust to ensure optimal memory safety, high-throughput routing, and concurrent socket handling. Downstream, the platform integrates:A headless Jellyfin container for video transcoding and HTTP Live Streaming (HLS) generation;Grimmory—the active, community-maintained successor to the discontinued Booklore library manager—for rich publication cataloging;A LiveKit Selective Forwarding Unit (SFU) to process real-time WebRTC communications;PostgreSQL for structured metadata persistence and real-time chat history;Redis for global state synchronization, user presence tracking, and fast caching.                     +---------------------------------------+
                     |            Client Browser             |
                     |     (React / TypeScript / Zustand)    |
                     +-------------------+-------------------+
                                         |
                                         | HTTP/HTTPS, WebSockets, WebRTC
                                         v
                     +-------------------+-------------------+
                     |          Traefik API Gateway          |
                     |         (Port 80/443 Ingress)         |
                     +---+---------------+---------------+---+
                         |               |               |
       +-----------------+               |               +-----------------+
       | /api/v1/chat & auth             | /livekit      | /jellyfin       | /grimmory
       v                                 v               v                 v
+------+------------+              +-----+-----+   +-----+-----+     +-----+-----+
|  Telos Core Server|              |  LiveKit  |   | Jellyfin  |     | Grimmory  |
| (Go / Rust Engine)|              |    SFU    |   | Headless  |     | Headless  |
+---+---+---+---+---+              +-----+-----+   +-----+-----+     +-----+-----+
    |   |   |   |                        |               |                 |
    |   |   |   +-----------+------------+               |                 |
    |   |   |               |                            |                 |
    |   |   |               | Redis Pub/Sub              |                 |
    |   |   v               v                            v                 v
    |   | +-+---------------+--+                   +-----+-----+     +-----+-----+
    |   | |    Redis Cache     |                   |Shared Media|     |Shared Book|
    |   | | (Presence & State) |                   |  Directory |     |Drop Path  |
    |   | +--------------------+                   +-----+-----+     +-----+-----+
    v   v                                                ^                 ^
+---+---+---+                                            |                 |
|PostgreSQL |                                            +--------+--------+
| (Metadata)|                                                     |
+-----------+                                           +---------+---------+
                                                        |  Host Storage File|
                                                        |  System Directory |
                                                        +-------------------+
Traffic flows through a single entry point managed by the Traefik API Gateway. Path-based routing rules split standard REST operations and real-time client traffic. Real-time chat messaging and authentication handshakes traverse directly to the Telos Core Server, which persists structured chat histories into PostgreSQL and publishes user state updates via Redis.Voice and video channel signaling is forwarded through Traefik to the LiveKit SFU. The SFU dynamically establishes low-latency UDP streams back to the client. Downstream media streaming is securely isolated behind the /jellyfin prefix, while all publication catalog and metadata operations map directly to the /grimmory prefix.Phase 1: Environment and Container OrchestrationOperating multiple data platforms, caching services, and real-time communication nodes in a single self-hosted ecosystem requires a carefully structured orchestrator configuration. The system separates infrastructure databases, gateway ingress lines, and application processes into distinct networks to minimize potential exposure surfaces.The following configuration specifies the primary service layer dependencies and mounting volumes:YAMLversion: '3.8'

networks:
  telos-ingress:
    name: telos-ingress
    driver: bridge
  telos-backend:
    name: telos-backend
    driver: bridge
    internal: true
  telos-db:
    name: telos-db
    driver: bridge
    internal: true

volumes:
  postgres_data:
  redis_data:
  jellyfin_config:
  grimmory_config:
  grimmory_db_data:

services:
  traefik:
    image: traefik:v2.10
    container_name: telos-traefik
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    networks:
      - telos-ingress
    ports:
      - "80:80"
      - "443:443"
      - "7881:7881" # WebRTC TCP Fallback
      - "3478:3478/udp" # STUN/TURN
      - "50000-50100:50000-50100/udp" # LiveKit Media RTP [cite: 14, 17]
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./config/traefik.yaml:/etc/traefik/traefik.yaml:ro
      - ./config/certs:/certs

  telos-core:
    build:
      context: ./backend
      dockerfile: Dockerfile
    container_name: telos-core
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    networks:
      - telos-ingress
      - telos-backend
    environment:
      - DATABASE_URL=postgres://telos_usr:SecurePass2026@postgres:5432/telos_db?sslmode=disable
      - REDIS_URL=redis://:SecureRedisPass2026@redis:6379/0
      - LIVEKIT_API_KEY=LK_API_KEY_TELOS
      - LIVEKIT_API_SECRET=LK_API_SECRET_LONG_VALUE_2026
      - JELLYFIN_ADMIN_TOKEN=JF_INTERNAL_ADMIN_TOKEN_SECURE
      - GRIMMORY_API_TOKEN=GRIM_INTERNAL_API_TOKEN_SECURE
    volumes:
      - /mnt/storage/shared:/data/shared

  postgres:
    image: postgres:16-alpine
    container_name: telos-postgres
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    networks:
      - telos-db
    environment:
      - POSTGRES_USER=telos_usr
      - POSTGRES_PASSWORD=SecurePass2026
      - POSTGRES_DB=telos_db
    volumes:
      - postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U telos_usr -d telos_db"]
      interval: 5s
      timeout: 5s
      retries: 5

  redis:
    image: redis:7-alpine
    container_name: telos-redis
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    networks:
      - telos-backend
      - telos-db
    command: redis-server --appendonly yes --requirepass SecureRedisPass2026
    volumes:
      - redis_data:/data
    healthcheck:
      test: ["CMD", "redis-cli", "-a", "SecureRedisPass2026", "ping"]
      interval: 5s
      timeout: 5s
      retries: 5

  jellyfin:
    image: jellyfin/jellyfin:latest
    container_name: telos-jellyfin-headless
    restart: unless-stopped
    user: "1000:1000" [cite: 18]
    networks:
      - telos-backend
    environment:
      - TZ=Etc/UTC
      - JELLYFIN_PublishedServerUrl=https://telos.local/jellyfin [cite: 19]
    volumes:
      - jellyfin_config:/config
      - /mnt/storage/shared/media:/data/media:ro
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.jellyfin.rule=PathPrefix(`/jellyfin`)"
      - "traefik.http.routers.jellyfin.entrypoints=websecure"
      - "traefik.http.services.jellyfin.loadbalancer.server.port=8096"

  grimmory-db:
    image: mariadb:10.11
    container_name: telos-grimmory-db
    restart: unless-stopped
    networks:
      - telos-db
    environment:
      - MYSQL_ROOT_PASSWORD=SecureMariaDBRootPass2026
      - MYSQL_DATABASE=grimmory_db
      - MYSQL_USER=grimmory_usr
      - MYSQL_PASSWORD=SecureMariaDBPass2026
    volumes:
      - grimmory_db_data:/var/lib/mysql
    healthcheck:
      test: ["CMD", "healthcheck.sh", "--connect", "--innodb_initialized"]
      interval: 10s
      timeout: 5s
      retries: 3

  grimmory:
    image: grimmory/grimmory:latest [cite: 13]
    container_name: telos-grimmory-headless
    restart: unless-stopped
    user: "1000:1000" [cite: 13]
    depends_on:
      grimmory-db:
        condition: service_healthy [cite: 13]
    networks:
      - telos-backend
      - telos-db
    environment:
      - USER_ID=1000 [cite: 3, 12]
      - GROUP_ID=1000 [cite: 3, 12]
      - DATABASE_URL=jdbc:mariadb://grimmory-db:3306/grimmory_db [cite: 3, 12]
      - DATABASE_USERNAME=grimmory_usr
      - DATABASE_PASSWORD=SecureMariaDBPass2026
      - API_DOCS_ENABLED=true [cite: 12, 20]
      - DISK_TYPE=LOCAL [cite: 3, 12]
    volumes:
      - grimmory_config:/app/data [cite: 12, 13]
      - /mnt/storage/shared/books:/books [cite: 12, 13]
      - /mnt/storage/shared/bookdrop:/bookdrop [cite: 12, 13]
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.grimmory.rule=PathPrefix(`/grimmory`)"
      - "traefik.http.routers.grimmory.entrypoints=websecure"
      - "traefik.http.services.grimmory.loadbalancer.server.port=6060"

  livekit:
    image: livekit/livekit-server:v1.10 [cite: 21]
    container_name: telos-livekit
    restart: unless-stopped
    command: --config /etc/livekit/config.yaml [cite: 14, 21]
    depends_on:
      redis:
        condition: service_healthy
    networks:
      - telos-ingress
      - telos-backend
    volumes:
      - ./config/livekit.yaml:/etc/livekit/config.yaml:ro [cite: 14, 21]
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.livekit.rule=PathPrefix(`/livekit`)"
      - "traefik.http.routers.livekit.entrypoints=websecure"
      - "traefik.http.services.livekit.loadbalancer.server.port=7880"
Network Separation ArchitectureThe execution of isolated application clusters prevents downstream network horizontal traversal in the event of an individual container vulnerability.The configuration segments traffic across three distinct network environments:Network NameAccessibilityPermitted MembersOperational Intenttelos-ingressExternal Ingress / Exposedtraefik, telos-core, livekitHandles client connection negotiations, SSL/TLS handshakes, real-time WebRTC signals, and API gateways.telos-backendIsolated / Internal Bridgetelos-core, redis, jellyfin, grimmory, livekitAllows fast, low-latency API communications and state synchronization while blocking direct access from the host.telos-dbStrictly Isolated / Database Onlypostgres, redis, grimmory-db, grimmoryProtects database connections and files from client exposure and external scanning.Persistent Volume Allocation and Ingestion StrategiesThe file management subsystem uses a standardized directory structure on the host system mounted directly to the container platforms. The storage root is allocated at /mnt/storage/shared. It features separate, dedicated paths for various ingest pipelines:/mnt/storage/shared/media: Holds the structured media libraries utilized by Jellyfin./mnt/storage/shared/books: Acts as the physical storage pool containing the indexed, validated, and categorized publication structures./mnt/storage/shared/bookdrop: Operates as an asynchronous watch folder continuously monitored by the Grimmory import pipeline.When users upload book files through the native Telos file manager, the backend writes those payloads directly into the /mnt/storage/shared/bookdrop path. Grimmory's virtual filesystem listener registers these updates, extracts metadata from Google Books or Open Library, and prepares the documents for the user review queue.To prevent locks or database state corruption on shared storage configurations, the environment variable DISK_TYPE is set to LOCAL. This directs the ingestion engine to use safe, transactional rename operations rather than unsafe concurrent operations.Phase 2: Gateway Configuration, API Mapping, and Headless Control MatrixTo maintain the illusion of a single native platform, the Telos Core Server maps standard browser operations to the underlying REST interfaces of Jellyfin and Grimmory.Headless API Integration MatrixThe following routing table details how the Go core backend aggregates the child APIs and translates endpoints to deliver a unified interface:Platform ComponentMaster API EndpointInternal Headless RequestMapping and Aggregation Processing LogicMedia StreamGET /api/v1/mediaGET /Users/{userId}/Views[cite: 25]Resolves active media library roots and converts Jellyfin views into standardized media collections.Media CatalogGET /api/v1/media/itemsGET /Users/{userId}/Items[cite: 25]Passes parent view parameters to retrieve libraries, then converts the payloads to matching standard layouts.Audio StreamGET /api/v1/stream/audio/{id}GET /Audio/{itemId}/stream[cite: 15]Proxies raw, high-throughput audio streams back to the client while enforcing credential validations.Video PlaybackGET /api/v1/stream/video/{id}GET /Videos/{itemId}/hls/{playlistId}/stream.m3u8[cite: 15]Initiates transcoding profiles and maps adaptive segment outputs using dynamic playlist files.Digital LibraryGET /api/v1/library/booksGET /api/v1/books[cite: 3, 22]Calls Grimmory's REST API and extracts details such as titles, authors, and formats.Library FacetsGET /api/v1/library/facetsGET /api/v1/books/facets[cite: 26, 27]Queries live count facets to populate navigation sidebars and categorization tags.Read ProgressPOST /api/v1/library/progressPOST /api/v1/books/progressUpdates the page reading coordinates for specific user profiles and syncs them across devices.Unified OIDC Identity Configuration FlowTo achieve true single sign-on (SSO), Telos integrates an identity flow based on the OpenID Connect (OIDC) specification. When users authenticate against the main gateway, a proxy interceptor automatically provisions matching profiles in the underlying micro-services using internal administrative credentials.To configure OIDC capabilities within Jellyfin, developers utilize the jellyfin-plugin-sso extension. This plugin can be configured programmatically during system deployment by posting a configuration payload to the plugin's REST endpoint:Bashcurl -X POST "https://telos.local/jellyfin/sso/OID/Add/telos-idp?api_key=JF_INTERNAL_ADMIN_TOKEN_SECURE" \
     -H "Content-Type: application/json" \
     -d '{
       "oidEndpoint": "https://telos.local/api/v1/auth/oidc",
       "oidClientId": "telos-jellyfin-client",
       "oidSecret": "SuperSecretJellyfinOidcPass2026",
       "enabled": true,
       "enableAuthorization": true,
       "enableAllFolders": true,
       "defaultUsernameClaim": "preferred_username"
     }' [cite: 33]
This JSON configuration maps user profiles on-the-fly. The parameters ensure that when users log in through the unified dashboard, their profiles are dynamically linked using their standard system usernames.Similarly, the Grimmory container includes built-in OIDC support managed through environment variables. When the system starts, standard claim templates automatically synchronize user groups, roles, and administrative flags directly within the active database.Real-Time Ingress Routing StrategyManaging WebRTC signaling, live chat sessions over WebSockets, and heavy adaptive video streams requires distinct reverse-proxy configurations. Traefik processes this traffic using specific transport rules to optimize performance:YAML# /etc/traefik/traefik.yaml
entryPoints:
  web:
    address: ":80"
    http:
      redirections:
        entryPoint:
          to: websecure
          scheme: https
  websecure:
    address: ":443"
  livekit-webrtc:
    address: ":7881" # Real-time TCP Fallback

http:
  routers:
    # Telos main web application router
    core-router:
      rule: "Host(`telos.local`)"
      service: core-service
      entryPoints:
        - websecure
    
    # Heads-up proxy rules mapping video transcoder streams [cite: 35, 36]
    jellyfin-router:
      rule: "Host(`telos.local`) && PathPrefix(`/jellyfin`)"
      service: jellyfin-service
      entryPoints:
        - websecure
      middlewares:
        - streaming-headers
        - buffer-limits

    # Isolated routing rules for document and book indexing services
    grimmory-router:
      rule: "Host(`telos.local`) && PathPrefix(`/grimmory`)"
      service: grimmory-service
      entryPoints:
        - websecure

  services:
    core-service:
      loadBalancer:
        servers:
          - url: "http://telos-core:8080"
    jellyfin-service:
      loadBalancer:
        servers:
          - url: "http://jellyfin:8096"
    grimmory-service:
      loadBalancer:
        servers:
          - url: "http://grimmory:6060"

  middlewares:
    # Restores real client IPs and preserves Range headers for heavy streaming [cite: 26, 35]
    streaming-headers:
      headers:
        customRequestHeaders:
          X-Forwarded-For: "${remote-ip}"
        customResponseHeaders:
          Access-Control-Allow-Origin: "*"
          Accept-Ranges: "bytes"
    buffer-limits:
      buffering:
        maxRequestBodyBytes: 104857600 # Prevents buffer overflow issues on large file uploads
This configuration ensures that video transfers do not experience socket starvation. Traefik disables local request buffering for the /jellyfin prefix to stream HLS data segments to clients without memory limits.Phase 3: Frontend Client Architecture and Aggregated State ManagementDeveloping a unified visual experience requires decoupled, reactive state managers that operate independently of the React rendering lifecycles.If WebRTC connections are managed within typical React component states, standard navigation actions will destroy the underlying peer context, instantly dropping calls.Persistent Global WebRTC State in ZustandTelos resolves this issue by establishing a global WebRTC and voice communication context inside a specialized Zustand store. This store resides entirely outside the React component tree to ensure persistent, uninterrupted sessions.TypeScriptimport { create } from 'zustand';
import { Room, RoomEvent, Track, Participant, ConnectionState } from 'livekit-client';

interface VoiceSessionState {
  room: Room | null;
  activeChannelId: string | null;
  connectionStatus: ConnectionState;
  activeParticipants: Participant[];
  audioOutputDevice: string;
  isMuted: boolean;
  
  joinVoiceRoom: (gatewayUrl: string, userJwt: string, targetChannel: string) => Promise<void>;
  terminateVoiceSession: () => Promise<void>;
  toggleMicrophoneStream: () => Promise<void>;
}

export const useVoiceSessionStore = create<VoiceSessionState>((set, get) => ({
  room: null,
  activeChannelId: null,
  connectionStatus: ConnectionState.Disconnected,
  activeParticipants: [],
  audioOutputDevice: 'default',
  isMuted: false,

  joinVoiceRoom: async (gatewayUrl, userJwt, targetChannel) => {
    // Teardown pre-existing calls safely [cite: 39]
    const existingRoom = get().room;
    if (existingRoom) {
      await existingRoom.disconnect();
    }

    const livekitRoom = new Room({
      adaptiveStream: true,
      dynacast: true,
      audioDefaults: {
        echoCancellation: true,
        noiseSuppression: true,
        autoGainControl: true, [cite: 14]
      }
    });

    // Wire global connection and participant change event hooks [cite: 40]
    livekitRoom.on(RoomEvent.ConnectionStateChanged, (status: ConnectionState) => {
      set({ connectionStatus: status });
    });

    livekitRoom.on(RoomEvent.ParticipantConnected, () => {
      set({ activeParticipants: Array.from(livekitRoom.participants.values()) });
    });

    livekitRoom.on(RoomEvent.ParticipantDisconnected, () => {
      set({ activeParticipants: Array.from(livekitRoom.participants.values()) });
    });

    try {
      set({ connectionStatus: ConnectionState.Connecting });
      await livekitRoom.connect(gatewayUrl, userJwt); [cite: 39]
      await livekitRoom.localParticipant.setMicrophoneEnabled(true);
      
      set({
        room: livekitRoom,
        activeChannelId: targetChannel,
        connectionStatus: ConnectionState.Connected,
        activeParticipants: Array.from(livekitRoom.participants.values()),
        isMuted: false
      });
    } catch (connectionError) {
      set({ connectionStatus: ConnectionState.Disconnected });
      throw connectionError;
    }
  },

  terminateVoiceSession: async () => {
    const currentRoom = get().room;
    if (currentRoom) {
      await currentRoom.disconnect();
    }
    set({
      room: null,
      activeChannelId: null,
      connectionStatus: ConnectionState.Disconnected,
      activeParticipants: []
    });
  },

  toggleMicrophoneStream: async () => {
    const activeRoom = get().room;
    if (activeRoom) {
      const currentState = get().isMuted;
      await activeRoom.localParticipant.setMicrophoneEnabled(currentState);
      set({ isMuted: !currentState });
    }
  }
}));
Decoupled Core Layout ComponentThe outer shell layout utilizes a decoupled design pattern to isolate application navigation pages while ensuring that active voice layers remain mounted.TypeScriptimport React, { useEffect } from 'react';
import { useVoiceSessionStore } from '../stores/useVoiceSessionStore';
import { SidebarNavigation } from './SidebarNavigation';
import { SubModuleRenderer } from './SubModuleRenderer';

export const CoreAppShell: React.FC = () => {
  const { connectionStatus, activeChannelId, isMuted } = useVoiceSessionStore();

  useEffect(() => {
    // Retain socket connection states across rapid URL routes
    return () => {
      console.log('Main component unmounted. Persisting WebRTC loop.');
    };
  }, []);

  return (
    <div className="telos-frame flex h-screen w-screen overflow-hidden bg-slate-950 font-sans antialiased text-slate-100">
      <SidebarNavigation />
      
      <main className="flex-1 relative flex flex-col min-w-0 bg-slate-900">
        <SubModuleRenderer />
      </main>

      {/* Floating Call Bar for Persistent Voice Calls */}
      {connectionStatus === ConnectionState.Connected && activeChannelId && (
        <div className="absolute bottom-6 right-6 z-50 flex items-center gap-4 p-4 rounded-xl border border-emerald-500/20 bg-slate-950/90 backdrop-blur-md shadow-2xl">
          <div className="flex flex-col gap-0.5">
            <span className="text-[10px] font-bold uppercase tracking-wider text-emerald-400">Voice Active</span>
            <span className="text-xs text-slate-400 font-medium">Channel ID: {activeChannelId}</span>
          </div>
          <div className="flex items-center gap-2">
            <button
              onClick={() => useVoiceSessionStore.getState().toggleMicrophoneStream()}
              className={`p-2 rounded-lg text-xs transition-colors ${
                isMuted ? 'bg-amber-500/20 text-amber-400' : 'bg-slate-800 text-slate-200'
              }`}
            >
              {isMuted ? 'Unmute' : 'Mute'}
            </button>
            <button
              onClick={() => useVoiceSessionStore.getState().terminateVoiceSession()}
              className="px-3 py-2 bg-rose-600 hover:bg-rose-700 text-white rounded-lg text-xs font-semibold transition"
            >
              Disconnect
            </button>
          </div>
        </div>
      )}
    </div>
  );
};
This layout mounts navigation modules in SubModuleRenderer while keeping the global core layout active. Navigating from books to videos destroys the page DOM but leaves the root level of the app shell intact, preventing WebRTC call disruptions.Phase 4: Phased Roadmap and Legal ComplianceStrategic Implementation PlanThe development schedule uses a logical path to minimize integration blockers. It begins with the database layer, configures identity routing, and proceeds to the real-time processing pipelines:Development StageFunctional ScopePrimary TasksBlockers and MitigationsPhase 1: Foundation(Weeks 1 - 6)Chat Engine and Persistence Mesh* Establish PostgreSQL schemas and Redis cache clusters. * Develop WebSocket core connection systems.* Deploy master API Gateway proxy configuration.State Corruption: Resolve potential Redis database collisions by implementing isolated database prefixes.Phase 2: Storage(Weeks 7 - 12)File System and Media Pipelines* Mount storage volume paths to target systems. * Configure Jellyfin headless streaming paths.  * Build custom HLS.js client players.Disk IO Bottlenecks: Avoid system freezes on heavy file operations by implementing non-blocking Go channels.Phase 3: Catalog(Weeks 13 - 18)Digital Library Integrations* Deploy Grimmory library catalog modules.  * Implement file watch-folders. * Develop unified EPUB/PDF reading controls.Metadata Sync Desyncs: Use database triggers to prevent mismatched catalog schemas.Phase 4: Real-Time(Weeks 19 - 24)WebRTC Communications* Set up the LiveKit communication server.  * Integrate global Zustand voice store handlers. * Complete end-to-end performance and latency tuning.Packet Jitter: Use STUN/TURN configurations to bypass restrictive client firewalls.Legal Analysis of Copyleft BoundariesThe integration of open-source services under the Affero General Public License (AGPL) and General Public License (GPL) requires careful boundary separation to prevent copyleft contamination. If a closed-source or permissively licensed application statically or dynamically links against copyleft libraries, it can be legally classified as a "derivative work." This classification would force the entire codebase to inherit the GPL/AGPL license terms.Technical Boundaries Preventing ContaminationThe architecture maintains strict compliance with both GPL (Jellyfin) and AGPL (Grimmory) licenses through a process-level separation strategy. This approach relies on four primary design patterns:Process Separation: The Go core gateway, Jellyfin, and Grimmory run as isolated operating system processes in separate container layers.Standardized API Communication: Inter-container data exchange occurs exclusively over standard network boundaries via HTTP REST queries, generic JSON payloads, and network sockets.No Build-Time Linking: The platform does not link, compile, or bundle any libraries or source code derived from copyleft components at build time.Independent Operability: If any individual background engine is stopped, the parent gateway continues to operate and serve its other modules, satisfying the "mere aggregation" definition.These boundaries ensure that the custom Telos application code remains a separate work. As a result, developers can distribute the platform under a permissive license (such as MIT or Apache 2.0) while still using these powerful headless backends.Open-Source Compliance & Credits README TemplateThis template must be integrated into the main README.md and accessible via the user interface under a dedicated "Credits" module to ensure proper open-source credit is maintained.Open-Source Compliance & AttributionsTelos is built on a foundation of collaborative open-source technology.
While Telos provides a custom unified interface, it relies on independent,
headless companion processes running in isolated environments to deliver
its core capabilities.The system architecture maintains process-level separation, which satisfies
the "mere aggregation" compliance guidelines under all applicable GPL and
AGPL licenses.Upstream Project AttributionsProjectRepository LinkLicenseArchitectural UseJellyfingithub.com/jellyfinGNU GPL v2.0 / v3.0Video parsing, hardware-accelerated transcoding, and adaptive HLS streams.Grimmorygithub.com/grimmory-toolsGNU AGPL v3.0E-book and comic catalog parsing, metadata extraction, and reader backend APIs.LiveKitgithub.com/livekitApache License 2.0Real-time WebRTC communications, audio/video channels, and room signaling.Downstream Licensing and Compliance Guidelines1. Process Separation and API IntegrityThe Telos Go/Rust backend operates purely as a proxy, gateway, and aggregator. It communicates with upstream services exclusively over standard network protocols. The custom components of the Telos client and backend contain no GPL or AGPL code, allowing them to be distributed under the permissive MIT License.2. Modifying Upstream ComponentsJellyfin: Any changes to the underlying Jellyfin codebase must be released under the GPL v2.0 or later.Grimmory: Any modifications to the Grimmory service—even if accessed solely over a network—trigger the source disclosure obligations of AGPL Section 13. You must make the source code of those modifications available to users interacting with the service over the network.3. Verification of Compliance BoundariesTo ensure clean division, compile verification audits must confirm that no upstream binaries are statically compiled into the Telos gateway executable. All database connections and API endpoints must use standard network interfaces.
