// Package main implements the telos-core API gateway — the single entry point
// for all client traffic in a self-hosted Telos community server.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/argon2"
)

// ═══════════════════════════════════════════════════════════════════════════
// Global State
// ═══════════════════════════════════════════════════════════════════════════

var (
	// dbPool is the PostgreSQL connection pool, initialised at startup.
	dbPool *pgxpool.Pool

	// Canonical catalog identity and cross-surface continuity are mandatory
	// database-backed services. Startup initializes them immediately after the
	// database connection succeeds; handlers fail closed if tests omit them.
	catalogRepo    *CatalogRepository
	continuityRepo *ContinuityRepository

	// redisClient is the Redis connection used for pub/sub and caching.
	redisClient *redis.Client

	// upgrader negotiates WebSocket upgrades; the origin is validated against
	// the exact-origin security policy (same source of truth as HTTP).
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return originAllowed(securityConfig, r.Header.Get("Origin"))
		},
	}

	// upstreamTransport bounds connection establishment and response headers
	// without imposing a total duration on media streams. JSON/control requests
	// add their own context deadline with upstreamRequestContext.
	upstreamTransport = &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}
	upstreamHTTPClient = &http.Client{Transport: upstreamTransport}
)

var (
	observeCatalogIdentity = func(ctx context.Context, in CatalogObservation) (CatalogResolution, error) {
		if catalogRepo == nil {
			return CatalogResolution{}, errors.New("catalog repository is not initialized")
		}
		return catalogRepo.Observe(ctx, in)
	}
	resolveCatalogIdentity = func(ctx context.Context, rawID string, surface CatalogSurface) (CatalogResolution, error) {
		if catalogRepo == nil {
			return CatalogResolution{}, errors.New("catalog repository is not initialized")
		}
		return catalogRepo.ResolveFor(ctx, rawID, surface)
	}
	// resolveCatalogIdentitiesFor is the batched peer of resolveCatalogIdentity.
	// It drops items whose active source sits on another surface rather than
	// reporting errCatalogWrongSurface per item, because every caller already
	// folds that error into a denial.
	resolveCatalogIdentitiesFor = func(ctx context.Context, ids []string, surface CatalogSurface) (map[string]CatalogResolution, error) {
		if catalogRepo == nil {
			return nil, errors.New("catalog repository is not initialized")
		}
		if !validCatalogSurface(surface) {
			return nil, fmt.Errorf("%w: surface", errCatalogInvalid)
		}
		resolved, err := catalogRepo.ResolveManyCanonical(ctx, ids)
		if err != nil {
			return nil, err
		}
		for id, resolution := range resolved {
			if resolution.Surface != surface {
				delete(resolved, id)
			}
		}
		return resolved, nil
	}
	getMemberContinuity = func(ctx context.Context, userID, itemID string) (MemberProgress, error) {
		if continuityRepo == nil {
			return MemberProgress{}, errors.New("continuity repository is not initialized")
		}
		return continuityRepo.Get(ctx, userID, itemID)
	}
	putMemberContinuity = func(ctx context.Context, userID, itemID string, in ProgressInput) (MemberProgress, error) {
		if continuityRepo == nil {
			return MemberProgress{}, errors.New("continuity repository is not initialized")
		}
		return continuityRepo.Put(ctx, userID, itemID, in)
	}
)

const upstreamRequestTimeout = 15 * time.Second

var errUpstreamUnavailable = errors.New("upstream service unavailable")

func upstreamRequestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, upstreamRequestTimeout)
}

//go:embed db/migrations/*.sql
var migrationsFS embed.FS

// ═══════════════════════════════════════════════════════════════════════════
// Types
// ═══════════════════════════════════════════════════════════════════════════

type WSMessage struct {
	ID          string            `json:"id"`
	Sender      string            `json:"sender"`
	SenderID    string            `json:"senderId"`
	DisplayName string            `json:"displayName"`
	Avatar      string            `json:"avatar"`
	AvatarUrl   string            `json:"avatarUrl"`
	Role        string            `json:"role"`
	Content     string            `json:"content"`
	Timestamp   string            `json:"timestamp"`
	Reactions   []ReactionSummary `json:"reactions,omitempty"`
	EditedAt    string            `json:"editedAt,omitempty"` // formatted, empty if never edited
	Deleted     bool              `json:"deleted,omitempty"`  // soft-deleted tombstone
	Pinned      bool              `json:"pinned,omitempty"`
	Embed       *MessageEmbed     `json:"embed,omitempty"`
}

type ReactionSummary struct {
	Emoji string   `json:"emoji"`
	Count int      `json:"count"`
	Users []string `json:"users"` // userIds who reacted
}

type MessageEmbed struct {
	Kind     string          `json:"kind"` // library_book|stream_film|file
	Ref      string          `json:"ref"`
	Snapshot json.RawMessage `json:"snapshot"` // {title,subtitle,kicker,cover,duration}
}

type WSEvent struct {
	Type      string      `json:"type"`                // history|message|message.update|message.delete|reaction|pin|presence
	Messages  []WSMessage `json:"messages,omitempty"`  // history
	Message   *WSMessage  `json:"message,omitempty"`   // message | message.update
	MessageID string      `json:"messageId,omitempty"` // message.delete | reaction | pin
	ChannelID string      `json:"channelId,omitempty"`
	Reaction  *WSReaction `json:"reaction,omitempty"` // reaction
	Pin       *WSPin      `json:"pin,omitempty"`      // pin
	Presence  *WSPresence `json:"presence,omitempty"` // presence
}

type WSReaction struct {
	MessageID string `json:"messageId"`
	Emoji     string `json:"emoji"`
	UserID    string `json:"userId"`
	Op        string `json:"op"`    // "add" | "remove"
	Count     int    `json:"count"` // new total for this emoji on this message
	Mine      bool   `json:"-"`     // computed client-side, never serialized
}

type WSPin struct {
	MessageID string `json:"messageId"`
	Op        string `json:"op"` // "add" | "remove"
}

type WSPresence struct {
	ChannelID string         `json:"channelId,omitempty"`
	Online    []PresenceUser `json:"online"` // roster for this channel
	Count     int            `json:"count"`  // realm-wide online count
}

type PresenceUser struct {
	UserID      string `json:"userId"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
	Avatar      string `json:"avatar"`
	AvatarUrl   string `json:"avatarUrl"`
}

type WSNotification = WSEvent

type UserContext struct {
	ID          string
	Username    string
	Roles       []string
	Permissions []string
	DisplayName string
	HasAvatar   bool
	DeviceID    string
	SessionHash string
}

type contextKey string

const userContextKey contextKey = "user"

// ═══════════════════════════════════════════════════════════════════════════
// Entrypoint
// ═══════════════════════════════════════════════════════════════════════════

func main() {
	// Subcommands: "migrate" applies migrations with the schema-owner URL and
	// exits; the default ("serve") verifies migrations and runs the gateway.
	mode := "serve"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	if mode == "migrate" {
		runMigrateCommand()
		return
	}
	if mode == "audiobook-migrate" {
		runAudiobookMigrateCommand(os.Args[2:])
		return
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	databaseURL := os.Getenv("DATABASE_URL")
	redisURL := os.Getenv("REDIS_URL")

	// Validate production secrets fast
	if os.Getenv("TELOS_ENV") != "development" {
		validateSecrets()
	}

	var cfgErr error
	securityConfig, cfgErr = loadSecurityConfig()
	if cfgErr != nil {
		log.Fatalf("Critical Configuration Error: %v", cfgErr)
	}

	// Cursor-signing keyring for stable pagination.
	ring, ringErr := loadCursorKeyring(os.Getenv("TELOS_CURSOR_KEYS_FILE"), securityConfig.Environment)
	if ringErr != nil {
		log.Fatalf("Critical Configuration Error: %v", ringErr)
	}
	cursorCodec = &hmacCursorCodec{ring: ring}

	// Dedicated HLS locator signing key (never shared with session/cursor keys).
	hlsKey, hlsErr := loadHLSSigningKey(os.Getenv("TELOS_HLS_SIGNING_KEY_FILE"), securityConfig.Environment)
	if hlsErr != nil {
		log.Fatalf("Critical Configuration Error: %v", hlsErr)
	}
	hlsSigningKey = hlsKey

	// Long-stream admission control (per-node global and per-user bounds).
	streamCapacity = newStreamCapacity(
		envInt("TELOS_STREAM_MAX_CONCURRENT", 100),
		envInt("TELOS_STREAM_MAX_PER_USER", 6),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Initialize the Postgres pool with bounded connections and per-connection
	// statement/lock/idle-in-transaction timeouts.
	getenv = os.Getenv
	dbCfg := defaultDatabaseConfig(databaseURL)
	dbCfg.StatementTimeout = envDuration("TELOS_DB_STATEMENT_TIMEOUT_MS", dbCfg.StatementTimeout)
	dbCfg.LockTimeout = envDuration("TELOS_DB_LOCK_TIMEOUT_MS", dbCfg.LockTimeout)
	var err error
	dbPool, err = NewDatabasePool(ctx, dbCfg)
	if err != nil {
		log.Fatalf("Critical: Failed to connect to database: %v", err)
	}
	defer dbPool.Close()
	catalogRepo = NewCatalogRepository(dbPool)
	continuityRepo = NewContinuityRepository(dbPool)

	// Serve path only verifies migration state; the one-shot telos-migrate
	// service (schema owner) is responsible for applying. In a single-role
	// development setup (TELOS_MIGRATE_ON_SERVE=1), apply here for convenience.
	migrateCtx, migrateCancel := context.WithTimeout(context.Background(), 60*time.Second)
	if os.Getenv("TELOS_MIGRATE_ON_SERVE") == "1" {
		if _, err := RunMigrations(migrateCtx, dbPool, migrationsFS); err != nil {
			migrateCancel()
			log.Fatalf("Critical: Database migration failed: %v", err)
		}
	} else if _, err := VerifyMigrations(migrateCtx, dbPool, migrationsFS); err != nil {
		migrateCancel()
		log.Fatalf("Critical: Database migration state is not current: %v", err)
	}
	migrateCancel()
	// Migrations are confirmed current; readiness may now report the schema as OK.
	migrationsVerified = true

	// Initialize Redis Client
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("Critical: Failed to parse Redis URL: %v", err)
	}
	redisClient = redis.NewClient(opt)
	defer redisClient.Close()

	// Atomic login limiter (Redis Lua reservations with a degraded in-process
	// fallback) replaces the old check-then-act throttle.
	loginLimiter = newLoginLimiter(redisClient)

	// Jellyfin item authorizer: every media item must belong to a configured
	// library. Disabled (allow-all) in development when JELLYFIN_LIBRARY_IDS is
	// unset; required in production.
	if libIDs := splitCSV(os.Getenv("JELLYFIN_LIBRARY_IDS")); len(libIDs) > 0 {
		if ja, jerr := NewJellyfinAuthorizer(jellyfinAPIResolver{}, libIDs); jerr == nil {
			jellyfinAuthorizer = ja
		} else {
			log.Fatalf("Critical Configuration Error: %v", jerr)
		}
	} else if securityConfig.Environment != "development" {
		log.Fatal("Critical Configuration Error: JELLYFIN_LIBRARY_IDS is required in production")
	}

	// Grimmory book authorizer (library membership + view/manage permission).
	if libIDs := splitCSV(os.Getenv("GRIMMORY_LIBRARY_IDS")); len(libIDs) > 0 {
		if ga, gerr := NewGrimmoryAuthorizer(grimmoryAPIResolver{}, libIDs); gerr == nil {
			grimmoryAuthorizer = ga
		} else {
			log.Fatalf("Critical Configuration Error: %v", gerr)
		}
	} else if securityConfig.Environment != "development" {
		log.Fatal("Critical Configuration Error: GRIMMORY_LIBRARY_IDS is required in production")
	}

	// Bounded, coalescing session-touch worker (replaces per-request
	// goroutines). Drained on shutdown.
	touchCtx, touchCancel := context.WithCancel(context.Background())
	sessionTouches = newSessionTouchWorker(dbPool)
	sessionTouches.Run(touchCtx)
	defer func() { touchCancel(); sessionTouches.Wait() }()

	// Transactional outbox: durable real-time delivery for chat and security
	// mutations. The dispatcher replaces the Phase 2 no-op OutboxDrainer, and
	// the sink replaces the no-op SecurityEventSink.
	securityEvents = OutboxSecurityEventSink{}
	outboxDispatcher = newOutboxDispatcher(dbPool, redisClient)
	outboxDrainer = outboxDispatcher
	outboxCtx, outboxCancel := context.WithCancel(context.Background())
	outboxDispatcher.Run(outboxCtx)
	defer outboxCancel()

	// Logical file/folder mutation state machine and the filesystem/database
	// reconciler. Physical storage and the book catalog are wired in later; the
	// reconciler is a bounded no-op until then and never marks a row deleted or
	// missing without a way to prove physical state.
	fileService = newFileService(dbPool)
	fileReconciler = newFileReconciler(dbPool, nil, nil)
	reconcileCtx, reconcileCancel := context.WithCancel(context.Background())
	StartFileReconciler(reconcileCtx, fileReconciler)
	defer reconcileCancel()

	// Readiness: cheap, coalesced, sanitized dependency probes. Upstream media
	// services are required in production and advisory (warn) in development.
	requiredUpstreams := securityConfig.Environment != "development"
	healthService = newHealthService(
		postgresChecker(),
		redisChecker(),
		outboxChecker(),
		mountChecker("storage", "/data/shared"),
		upstreamChecker("jellyfin", jellyfinBaseURL+"/System/Info/Public", requiredUpstreams),
		upstreamChecker("grimmory", grimmoryBaseURL+"/api/v1/healthcheck", requiredUpstreams),
	)

	// Ensure local directories exist
	if err := os.MkdirAll("/data/shared/staging", 0755); err != nil {
		log.Printf("Warning: Failed to create staging dir: %v", err)
	}
	if err := os.MkdirAll("/data/shared/staging/library", 0755); err != nil {
		log.Printf("Warning: Failed to create staging library dir: %v", err)
	}
	if err := os.MkdirAll("/data/shared/media", 0755); err != nil {
		log.Printf("Warning: Failed to create media dir: %v", err)
	}
	if err := os.MkdirAll("/data/shared/bookdrop", 0755); err != nil {
		log.Printf("Warning: Failed to create bookdrop dir: %v", err)
	}

	// Create ServeMux
	mux := http.NewServeMux()

	// Register frontend static file handler (no-op in headless mode)
	registerFrontend(mux)

	// Public Routes
	mux.HandleFunc("GET /api/v1/health/live", handleLiveness)
	mux.HandleFunc("GET /api/v1/health/ready", handleReadiness)
	mux.HandleFunc("GET /api/v1/health", handleReadiness)
	mux.HandleFunc("POST /api/v1/auth/bootstrap", handleBootstrap)
	mux.HandleFunc("POST /api/v1/auth/login", handleLogin)
	mux.HandleFunc("POST /api/v1/auth/invites/accept", handleAcceptInvite)
	// Only the refresh exchange is public: it authenticates by presenting a
	// valid refresh token in the body. Everything else here needs an existing
	// credential, so it goes through withAuth like every other guarded route —
	// the route table is how this codebase's auth posture is audited, and a bare
	// HandleFunc on a device route reads as public at a glance.
	mux.HandleFunc("POST /api/v1/auth/devices/refresh", handleRefreshDevice)

	// Authenticated Routes (Requires Session check)
	mux.Handle("POST /api/v1/auth/logout", withAuth(http.HandlerFunc(handleLogout), ""))
	mux.Handle("GET /api/v1/auth/me", withAuth(http.HandlerFunc(handleMe), ""))
	mux.Handle("POST /api/v1/auth/invites", withAuth(http.HandlerFunc(handleCreateInvite), "manage_community"))

	// Settings — profile & preferences
	mux.Handle("PATCH /api/v1/users/me", withAuth(http.HandlerFunc(handleUpdateProfile), ""))
	mux.Handle("GET /api/v1/users/me/preferences", withAuth(http.HandlerFunc(handleGetPreferences), ""))
	mux.Handle("PUT /api/v1/users/me/preferences", withAuth(http.HandlerFunc(handlePutPreferences), ""))

	// Settings — password & sessions & devices
	mux.Handle("POST /api/v1/users/me/password", withAuth(http.HandlerFunc(handleChangePassword), ""))
	mux.Handle("GET /api/v1/users/me/sessions", withAuth(http.HandlerFunc(handleListMySessions), ""))
	mux.Handle("DELETE /api/v1/users/me/sessions/{id}", withAuth(http.HandlerFunc(handleRevokeSession), ""))
	mux.Handle("POST /api/v1/users/me/sessions/revoke-others", withAuth(http.HandlerFunc(handleRevokeOtherSessions), ""))
	mux.Handle("POST /api/v1/auth/devices/register", withAuth(http.HandlerFunc(handleRegisterDevice), ""))
	mux.Handle("POST /api/v1/auth/ws-ticket", withAuth(http.HandlerFunc(handleWSTicket), ""))
	mux.Handle("GET /api/v1/users/me/devices", withAuth(http.HandlerFunc(handleListDevices), ""))
	mux.Handle("DELETE /api/v1/users/me/devices/{id}", withAuth(http.HandlerFunc(handleRevokeDevice), ""))

	// Settings — avatars
	mux.Handle("POST /api/v1/users/me/avatar", withAuth(http.HandlerFunc(handleUploadAvatar), ""))
	mux.Handle("DELETE /api/v1/users/me/avatar", withAuth(http.HandlerFunc(handleDeleteAvatar), ""))
	mux.Handle("GET /api/v1/users/{id}/avatar", withAuth(http.HandlerFunc(handleGetAvatar), ""))

	// Settings — admin: members
	mux.Handle("GET /api/v1/admin/users", withAuth(http.HandlerFunc(handleAdminListUsers), "manage_members"))
	mux.Handle("PUT /api/v1/admin/users/{id}/roles", withAuth(http.HandlerFunc(handleAdminSetUserRoles), "manage_roles"))
	mux.Handle("POST /api/v1/admin/users/{id}/active", withAuth(http.HandlerFunc(handleAdminSetUserActive), "manage_members"))
	mux.Handle("DELETE /api/v1/admin/users/{id}", withAuth(http.HandlerFunc(handleAdminDeleteUser), "manage_members"))

	// Settings — admin: invites
	mux.Handle("GET /api/v1/admin/invites", withAuth(http.HandlerFunc(handleAdminListInvites), "manage_community"))
	mux.Handle("DELETE /api/v1/admin/invites/{id}", withAuth(http.HandlerFunc(handleAdminRevokeInvite), "manage_community"))

	// Settings — admin: roles & permissions
	mux.Handle("POST /api/v1/admin/channels", withAuth(http.HandlerFunc(handleAdminCreateChannel), "manage_channels"))
	mux.Handle("PATCH /api/v1/admin/channels/{id}", withAuth(http.HandlerFunc(handleAdminUpdateChannel), "manage_channels"))
	mux.Handle("DELETE /api/v1/admin/channels/{id}", withAuth(http.HandlerFunc(handleAdminDeleteChannel), "manage_channels"))
	mux.Handle("GET /api/v1/admin/channels/{id}/overrides", withAuth(http.HandlerFunc(handleGetChannelOverrides), "manage_channels"))
	mux.Handle("PUT /api/v1/admin/channels/{id}/overrides", withAuth(http.HandlerFunc(handlePutChannelOverride), "manage_channels"))

	mux.Handle("GET /api/v1/admin/roles", withAuth(http.HandlerFunc(handleAdminListRoles), "manage_roles"))
	mux.Handle("GET /api/v1/admin/permissions", withAuth(http.HandlerFunc(handleAdminListPermissions), "manage_roles"))
	mux.Handle("POST /api/v1/admin/roles", withAuth(http.HandlerFunc(handleAdminCreateRole), "manage_roles"))
	mux.Handle("PUT /api/v1/admin/roles/{id}/permissions", withAuth(http.HandlerFunc(handleAdminSetRolePermissions), "manage_roles"))
	mux.Handle("DELETE /api/v1/admin/roles/{id}", withAuth(http.HandlerFunc(handleAdminDeleteRole), "manage_roles"))

	// Chat WebSocket
	mux.Handle("GET /api/v1/chat/ws", withAuth(http.HandlerFunc(handleWebSocket), "view_channel"))
	mux.Handle("POST /api/v1/channels/{id}/messages", withAuth(http.HandlerFunc(handleSendMessage), "send_messages"))
	mux.Handle("GET /api/v1/channels/{id}/roots", withAuth(http.HandlerFunc(handleListChannelRoots), "view_channel"))
	mux.Handle("POST /api/v1/channels/{id}/messages/{rootID}/replies", withAuth(http.HandlerFunc(handleSendReply), "send_messages"))
	mux.Handle("GET /api/v1/channels/{id}/messages/{rootID}/replies", withAuth(http.HandlerFunc(handleListThreadReplies), "view_channel"))
	mux.Handle("GET /api/v1/channels/{id}/changes", withAuth(http.HandlerFunc(handleChannelChanges), "view_channel"))
	mux.Handle("PUT /api/v1/channels/{id}/read", withAuth(http.HandlerFunc(handleMarkChannelRead), "view_channel"))
	mux.Handle("PATCH /api/v1/channels/{id}/messages/{mid}", withAuth(http.HandlerFunc(handleEditMessage), "send_messages"))
	mux.Handle("DELETE /api/v1/channels/{id}/messages/{mid}", withAuth(http.HandlerFunc(handleDeleteMessage), "view_channel"))
	mux.Handle("POST /api/v1/channels/{id}/messages/{mid}/reactions", withAuth(http.HandlerFunc(handleAddReaction), "send_messages"))
	mux.Handle("DELETE /api/v1/channels/{id}/messages/{mid}/reactions/{emoji}", withAuth(http.HandlerFunc(handleRemoveReaction), "send_messages"))

	// Channel list
	mux.Handle("GET /api/v1/channels", withAuth(http.HandlerFunc(handleListChannels), "view_channel"))
	mux.Handle("GET /api/v1/channels/{id}/members", withAuth(http.HandlerFunc(handleChannelMembers), "view_channel"))
	mux.Handle("GET /api/v1/channels/{id}/pins", withAuth(http.HandlerFunc(handleListPins), "view_channel"))
	mux.Handle("POST /api/v1/channels/{id}/pins", withAuth(http.HandlerFunc(handlePinMessage), "manage_messages"))
	mux.Handle("DELETE /api/v1/channels/{id}/pins/{mid}", withAuth(http.HandlerFunc(handleUnpinMessage), "manage_messages"))
	mux.Handle("GET /api/v1/users/search", withAuth(http.HandlerFunc(handleUserSearch), "view_channel"))
	mux.Handle("GET /api/v1/search", withAuth(http.HandlerFunc(handleSearch), ""))

	// Jellyfin Proxy routes (Require view_media)
	mux.Handle("GET /api/v1/media", withAuth(http.HandlerFunc(handleMedia), "view_media"))
	mux.Handle("POST /api/v1/media/refresh", withAuth(http.HandlerFunc(handleMediaRefresh), "manage_files"))
	mux.Handle("GET /api/v1/media/refresh/status", withAuth(http.HandlerFunc(handleMediaRefreshStatus), "manage_files"))
	mux.Handle("GET /api/v1/media/items", withAuth(http.HandlerFunc(handleMediaItems), "view_media"))
	mux.Handle("GET /api/v1/media/items/{id}", withAuth(http.HandlerFunc(handleMediaItemByID), "view_media"))
	mux.Handle("GET /api/v1/media/items/{id}/cover", withAuth(http.HandlerFunc(handleMediaItemCover), "view_media"))
	mux.Handle("GET /api/v1/media/continue", withAuth(http.HandlerFunc(handleMediaContinue), "view_media"))
	mux.Handle("GET /api/v1/media/recent", withAuth(http.HandlerFunc(handleMediaRecent), "view_media"))
	mux.Handle("GET /api/v1/media/items/{id}/related", withAuth(http.HandlerFunc(handleMediaRelated), "view_media"))
	mux.Handle("GET /api/v1/media/items/{id}/playback-info", withAuth(http.HandlerFunc(handleMediaPlaybackInfo), "view_media"))
	mux.Handle("GET /api/v1/stream/audio/{id}", withAuth(http.HandlerFunc(handleStreamAudio), "view_media"))
	mux.Handle("GET /api/v1/stream/video/{id}", withAuth(http.HandlerFunc(handleStreamVideo), "view_media"))
	mux.Handle("GET /api/v1/stream/video/{id}/{path...}", withAuth(http.HandlerFunc(handleStreamVideoSubpath), "view_media"))
	mux.Handle("GET /api/v1/hls/{locator}", withAuth(http.HandlerFunc(handleHLSResource), "view_media"))
	// Library module routes (Grimmory-backed catalog)
	mux.Handle("GET /api/v1/library/books", withAuth(http.HandlerFunc(handleLibraryBooks), "view_library"))
	mux.Handle("GET /api/v1/library/books/{id}", withAuth(http.HandlerFunc(handleLibraryBookByID), "view_library"))
	mux.Handle("GET /api/v1/library/continue", withAuth(http.HandlerFunc(handleLibraryContinue), "view_library"))
	mux.Handle("GET /api/v1/library/recent", withAuth(http.HandlerFunc(handleLibraryRecent), "view_library"))
	mux.Handle("GET /api/v1/library/authors", withAuth(http.HandlerFunc(handleLibraryAuthors), "view_library"))
	mux.Handle("GET /api/v1/library/authors/{id}/books", withAuth(http.HandlerFunc(handleLibraryAuthorBooks), "view_library"))
	mux.Handle("GET /api/v1/library/series", withAuth(http.HandlerFunc(handleLibrarySeries), "view_library"))
	mux.Handle("GET /api/v1/library/series/{name}/books", withAuth(http.HandlerFunc(handleLibrarySeriesBooks), "view_library"))
	mux.Handle("GET /api/v1/library/facets", withAuth(http.HandlerFunc(handleLibraryFacets), "view_library"))
	mux.Handle("GET /api/v1/library/books/{id}/cover", withAuth(http.HandlerFunc(handleLibraryBookCover), "view_library"))
	mux.Handle("GET /api/v1/library/books/{id}/content", withAuth(http.HandlerFunc(handleLibraryBookContent), "view_library"))
	mux.Handle("GET /api/v1/library/audiobooks/{id}/info", withAuth(http.HandlerFunc(handleAudiobookInfo), "view_library"))
	mux.Handle("GET /api/v1/library/audiobooks/{id}/stream", withAuth(http.HandlerFunc(handleAudiobookStream), "view_library"))
	mux.Handle("GET /api/v1/library/audiobooks/{id}/tracks/{index}/stream", withAuth(http.HandlerFunc(handleAudiobookTrackStream), "view_library"))
	mux.Handle("GET /api/v1/library/books/{id}/progress", withAuth(http.HandlerFunc(handleGetBookProgress), "view_library"))
	mux.Handle("PUT /api/v1/library/books/{id}/progress", withAuth(http.HandlerFunc(handlePutBookProgress), "view_library"))
	mux.Handle("PUT /api/v1/library/books/{id}/metadata", withAuth(http.HandlerFunc(handleUpdateLibraryBookMetadata), "manage_library"))
	mux.Handle("POST /api/v1/library/books/{id}/metadata/fetch", withAuth(http.HandlerFunc(handleFetchLibraryBookMetadata), "manage_library"))
	mux.Handle("PUT /api/v1/library/books/{id}/cover", withAuth(http.HandlerFunc(handleUpdateLibraryBookCover), "manage_library"))
	mux.Handle("DELETE /api/v1/library/books/{id}", withAuth(http.HandlerFunc(handleDeleteLibraryBook), "manage_library"))

	// File Library (Require view_files, upload_files, upload_books, manage_files)
	mux.Handle("GET /api/v1/files", withAuth(http.HandlerFunc(handleListFiles), "view_files"))
	mux.Handle("POST /api/v1/files", withAuth(http.HandlerFunc(handleUploadFile), "upload_files"))
	mux.Handle("POST /api/v1/files/books", withAuth(http.HandlerFunc(handleUploadBook), "upload_books"))
	mux.Handle("GET /api/v1/files/{id}/download", withAuth(http.HandlerFunc(handleDownloadFile), "view_files"))
	mux.Handle("DELETE /api/v1/files/{id}", withAuth(http.HandlerFunc(handleDeleteFile), "manage_files"))
	mux.Handle("POST /api/v1/folders", withAuth(http.HandlerFunc(handleCreateFolder), "upload_files"))
	mux.Handle("DELETE /api/v1/folders/{id}", withAuth(http.HandlerFunc(handleDeleteFolder), "manage_files"))
	mux.Handle("GET /api/v1/files/audit", withAuth(http.HandlerFunc(handleListFileAudit), "manage_files"))

	// Recipient-scoped user-event stream (WebSocket catch-up + live delivery).
	mux.Handle("GET /api/v1/events", withAuth(http.HandlerFunc(handleEventsCatchUp), ""))
	mux.Handle("GET /api/v1/events/ws", withAuth(http.HandlerFunc(handleEventsWS), ""))

	// Book annotations (private/community highlights) — book-scoped, so gated on
	// view_library with the locator validated against the book format.
	mux.Handle("GET /api/v1/library/books/{id}/annotations", withAuth(http.HandlerFunc(handleListAnnotations), "view_library"))
	mux.Handle("POST /api/v1/library/books/{id}/annotations", withAuth(http.HandlerFunc(handleCreateAnnotation), "view_library"))

	// Commentary on streamed media and files — gated in-handler on view_media /
	// view_files. Comments carry no locator.
	mux.Handle("GET /api/v1/media/items/{id}/comments", withAuth(commentListHandler("media"), "view_media"))
	mux.Handle("POST /api/v1/media/items/{id}/comments", withAuth(commentCreateHandler("media"), "view_media"))
	mux.Handle("GET /api/v1/files/{id}/comments", withAuth(commentListHandler("file"), "view_files"))
	mux.Handle("POST /api/v1/files/{id}/comments", withAuth(commentCreateHandler("file"), "view_files"))

	// Annotation-scoped edit/delete/reply routes. The target type is read from
	// the row and authorized in-handler, so no fixed capability is required here.
	mux.Handle("PATCH /api/v1/library/annotations/{aid}", withAuth(http.HandlerFunc(handlePatchAnnotation), ""))
	mux.Handle("DELETE /api/v1/library/annotations/{aid}", withAuth(http.HandlerFunc(handleDeleteAnnotation), ""))
	mux.Handle("GET /api/v1/library/annotations/{aid}/replies", withAuth(http.HandlerFunc(handleListAnnotationReplies), ""))
	mux.Handle("POST /api/v1/library/annotations/{aid}/replies", withAuth(http.HandlerFunc(handleCreateAnnotationReply), ""))
	mux.Handle("DELETE /api/v1/library/annotation-replies/{rid}", withAuth(http.HandlerFunc(handleDeleteAnnotationReply), ""))

	// Internet-facing boundary chain (outermost first): correlation ID,
	// response security headers, bounded admission before any auth/DB/Redis
	// work, development CORS, then the exact-origin policy for state-changing
	// requests. requireTrustedOrigin replaces the old csrf/cors middlewares.
	handler := requestIDMiddleware(
		securityHeaders(
			shuttingDownMiddleware(
				admissionMiddleware(
					devCORS(
						requireTrustedOrigin(mux, securityConfig),
						securityConfig),
					securityConfig.MaxInFlightHTTP)),
			securityConfig))
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    securityConfig.MaxHeaderBytes,
		IdleTimeout:       120 * time.Second,
	}

	runServer(server)
}

func validateSecrets() {
	vars := []string{
		"DATABASE_URL", "REDIS_URL", "TELOS_DOMAIN", "TELOS_PUBLIC_ORIGIN", "TELOS_BOOTSTRAP_TOKEN",
		"JELLYFIN_ADMIN_TOKEN", "GRIMMORY_ADMIN_USER", "GRIMMORY_ADMIN_PASSWORD",
	}
	placeholders := []string{"your-secret-here", "change-me", "temp-token", "placeholder", "generate-me"}
	for _, v := range vars {
		val := os.Getenv(v)
		if val == "" {
			log.Fatalf("Critical Configuration Error: Environment variable %s is not set.", v)
		}
		for _, ph := range placeholders {
			if strings.Contains(strings.ToLower(val), ph) {
				log.Fatalf("Critical Configuration Error: Environment variable %s contains an insecure placeholder value.", v)
			}
		}
	}
	if len(os.Getenv("TELOS_BOOTSTRAP_TOKEN")) < 32 {
		log.Fatal("Critical Configuration Error: TELOS_BOOTSTRAP_TOKEN must be at least 32 characters.")
	}
	domain := strings.ToLower(strings.TrimSpace(os.Getenv("TELOS_DOMAIN")))
	if strings.HasSuffix(domain, ".local") || strings.HasSuffix(domain, ".example") || strings.HasSuffix(domain, ".example.com") {
		log.Fatal("Critical Configuration Error: TELOS_DOMAIN must be a real production domain.")
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Authentication, Session and Cryptography Helpers
// ═══════════════════════════════════════════════════════════════════════════

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, 2, 19456, 1, 32)
	saltBase64 := base64.RawStdEncoding.EncodeToString(salt)
	hashBase64 := base64.RawStdEncoding.EncodeToString(hash)
	return fmt.Sprintf("$argon2id$v=19$m=19456,t=2,p=1$%s$%s", saltBase64, hashBase64), nil
}

func verifyPassword(password, encodedHash string) (bool, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 {
		return false, errors.New("invalid hash format")
	}
	if parts[1] != "argon2id" {
		return false, errors.New("incompatible variant")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, err
	}
	if version != 19 {
		return false, errors.New("incompatible version")
	}
	var memory uint32
	var time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, err
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	decodedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	hash := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(decodedHash)))
	return subtle.ConstantTimeCompare(hash, decodedHash) == 1, nil
}

// generateToken returns (token, sha256hex(token)); a random-source failure is
// surfaced so no zero/partial-entropy token is ever issued.
func generateToken() (string, string, error) {
	token, err := secureToken(32)
	if err != nil {
		return "", "", err
	}
	h := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(h[:]), nil
}

// dummyPasswordHash is a valid argon2id hash used to equalize the timing of a
// login for an unknown or malformed username. Computed once at startup.
var dummyPasswordHash = func() string {
	h, err := hashPassword("telos-dummy-verification-password")
	if err != nil {
		// hashPassword only fails on a random-source error; fall back to a
		// well-formed constant so verifyPassword still does argon2 work.
		return "$argon2id$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	}
	return h
}()

// ═══════════════════════════════════════════════════════════════════════════
// Middlewares & Origin Policy
// ═══════════════════════════════════════════════════════════════════════════

// Origin policy, CORS, request admission, and body limits now live in
// security.go (loadSecurityConfig, originAllowed, requireTrustedOrigin,
// devCORS, admissionMiddleware, securityHeaders, decodeJSON, clientIP).

func getAuthenticatedUser(r *http.Request) (*UserContext, error) {
	if dbPool == nil {
		return nil, errors.New("db uninitialized")
	}

	// Two credential classes, deliberately rescoped from the original
	// cookie-only invariant. Browser sessions still arrive only in the HttpOnly
	// SameSite=Strict cookie, and sessionTokenFromRequest stays cookie-only so a
	// session token can never be relayed to an upstream proxy. Native clients
	// present a bearer token instead, read here in a separate branch that applies
	// the same opacity check — a token carrying URL or header structure is
	// rejected rather than looked up. WebSocket single-use tickets arrive in
	// Sec-WebSocket-Protocol header (format: telos-ticket.<ticket>).
	token, err := sessionTokenFromRequest(r)
	if err != nil {
		if raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			raw = strings.TrimSpace(raw)
			if raw != "" && !strings.ContainsAny(raw, " \t\r\n/?#&=") {
				token = raw
			}
		}
	}

	if token == "" {
		if isWebSocketUpgrade(r) {
			if ticket := parseWSTicketHeader(r); ticket != "" {
				userID, deviceID, err := consumeWSTicket(r.Context(), ticket)
				if err != nil {
					return nil, fmt.Errorf("invalid websocket ticket: %w", err)
				}
				if err := assertTicketSubjectUsable(r.Context(), userID, deviceID); err != nil {
					return nil, err
				}
				uc, err := loadUserContext(r.Context(), userID)
				if err != nil {
					return nil, err
				}
				uc.SessionHash = sha256Hex(ticket)
				uc.DeviceID = deviceID
				return uc, nil
			}
		}
		return nil, errors.New("missing authentication token")
	}

	h := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(h[:])

	// Single aggregate query for identity + roles + sorted permissions.
	uc, err := LoadAuthenticatedUser(r.Context(), dbPool, tokenHash)
	if err != nil {
		return nil, err
	}
	uc.SessionHash = tokenHash

	var devID *string
	_ = dbPool.QueryRow(r.Context(), `SELECT device_id::text FROM sessions WHERE token_hash = $1 AND revoked_at IS NULL`, tokenHash).Scan(&devID)
	if devID != nil {
		uc.DeviceID = *devID
	}

	// Coalesced, bounded last-seen stamp — never blocks or spawns a goroutine.
	if sessionTouches != nil {
		sessionTouches.Touch(tokenHash)
	}
	return uc, nil
}

// loadUserContext loads a user's identity, roles, and sorted effective
// permissions by user ID (used by socket revalidation, which has no request).
func loadUserContext(ctx context.Context, userID string) (*UserContext, error) {
	uc := &UserContext{ID: userID}
	var displayName *string
	var avatarFileID *string
	if err := dbPool.QueryRow(ctx, `
		SELECT username, display_name, avatar_file_id FROM users WHERE id = $1
	`, userID).Scan(&uc.Username, &displayName, &avatarFileID); err != nil {
		return nil, err
	}
	if displayName != nil {
		uc.DisplayName = *displayName
	}
	uc.HasAvatar = avatarFileID != nil

	rows, err := dbPool.Query(ctx, `SELECT role_id FROM user_roles WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var rID string
		if err := rows.Scan(&rID); err == nil {
			uc.Roles = append(uc.Roles, rID)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	permRows, err := dbPool.Query(ctx, `
		SELECT DISTINCT rp.permission_id
		FROM role_permissions rp JOIN user_roles ur ON ur.role_id = rp.role_id
		WHERE ur.user_id = $1 ORDER BY rp.permission_id
	`, userID)
	if err != nil {
		return nil, err
	}
	defer permRows.Close()
	for permRows.Next() {
		var p string
		if err := permRows.Scan(&p); err != nil {
			return nil, err
		}
		uc.Permissions = append(uc.Permissions, p)
	}
	return uc, permRows.Err()
}

func hasPermission(ctx context.Context, user *UserContext, perm string, channelID *string) (bool, error) {
	for _, r := range user.Roles {
		if r == "Owner" {
			return true, nil
		}
	}

	var hasGlobal bool
	err := dbPool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM role_permissions
			WHERE role_id = ANY($1) AND permission_id = $2
		)
	`, user.Roles, perm).Scan(&hasGlobal)
	if err != nil {
		return false, err
	}

	if channelID != nil {
		// Explicit per-(channel, role, permission) overrides apply to every
		// non-Owner role — including Administrator and custom roles. Deny takes
		// precedence over allow; a missing row means "inherit" (fall through to
		// the global grant). Owner already returned true above.
		var denied, allowed int
		err = dbPool.QueryRow(ctx, `
			SELECT
				COUNT(*) FILTER (WHERE decision = 'deny'),
				COUNT(*) FILTER (WHERE decision = 'allow')
			FROM channel_permission_overrides
			WHERE channel_id = $1 AND permission_id = $2 AND role_id = ANY($3)
		`, *channelID, perm, user.Roles).Scan(&denied, &allowed)
		if err != nil {
			return false, err
		}
		if denied > 0 {
			return false, nil
		}
		if allowed > 0 {
			return true, nil
		}
	}

	return hasGlobal, nil
}

func withAuth(next http.Handler, requiredPerm string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := getAuthenticatedUser(r)
		if err != nil {
			http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
			return
		}

		// Inject user into context
		r = r.WithContext(context.WithValue(r.Context(), userContextKey, user))

		if requiredPerm != "" {
			var channelID *string
			cParam := r.URL.Query().Get("channel")
			if cParam == "" {
				cParam = r.PathValue("id")
			}
			if cParam != "" {
				// verify if it is valid UUID
				if len(cParam) == 36 {
					channelID = &cParam
				}
			}

			allowed, err := hasPermission(r.Context(), user, requiredPerm, channelID)
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			if !allowed {
				http.Error(w, "Forbidden: Missing permission "+requiredPerm, http.StatusForbidden)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// ═══════════════════════════════════════════════════════════════════════════
// Authentication Handlers
// ═══════════════════════════════════════════════════════════════════════════

func handleBootstrap(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Token    string `json:"token"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.AuthJSONBytes); err != nil {
		return
	}

	expectedToken := os.Getenv("TELOS_BOOTSTRAP_TOKEN")
	if expectedToken == "" || body.Token != expectedToken {
		http.Error(w, "Forbidden: Invalid bootstrap token", http.StatusForbidden)
		return
	}

	username, err := canonicalUsername(body.Username)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_username", "Username must be 3-32 chars of a-z 0-9 . _ - and start alphanumeric.")
		return
	}
	if len(body.Password) < 15 || len(body.Password) > 128 {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_password", "Password must be 15-128 characters.")
		return
	}

	ctx := r.Context()
	hash, err := hashPassword(body.Password)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	tx, err := dbPool.Begin(ctx)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	defer tx.Rollback(ctx)

	// Serialize concurrent bootstraps: only the first past the advisory lock
	// observes zero Owners, so 20 simultaneous requests create exactly one.
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", bootstrapAdvisoryLock); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	var existing int
	if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM user_roles WHERE role_id = 'Owner'").Scan(&existing); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	if existing > 0 {
		writeAPIError(w, r, http.StatusForbidden, "already_bootstrapped", "An Owner already exists.")
		return
	}

	var userID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (username, password_hash) VALUES ($1, $2) RETURNING id
	`, username, hash).Scan(&userID); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1, 'Owner')`, userID); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	if err := securityEvents.Record(ctx, tx, SecurityEventIntent{Kind: "owner_bootstrapped", ActorID: userID, SubjectID: userID}); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	log.Printf("Owner bootstrapped successfully request_id=%s. Bootstrap token consumed.", requestIDFrom(ctx))
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "success", "userId": userID})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.AuthJSONBytes); err != nil {
		return
	}

	clientAddr, ipErr := clientIP(r, securityConfig.TrustedProxyRanges)
	if ipErr != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad_client_address", "The client address could not be determined.")
		return
	}
	ctx := r.Context()

	// A malformed username can never match; still reserve so probing a bad
	// username costs the same and is rate-limited identically.
	canonUser, unameErr := canonicalUsername(body.Username)
	limiterUser := canonUser
	if unameErr != nil {
		limiterUser = strings.ToLower(strings.TrimSpace(body.Username))
	}

	// Reserve an atomic login slot before any password work.
	attempt, err := loginLimiter.Reserve(ctx, limiterUser, clientAddr)
	if err != nil {
		switch {
		case errors.Is(err, errLoginThrottled):
			writeAPIError(w, r, http.StatusTooManyRequests, "too_many_attempts", "Too many login attempts; try again later.")
		case errors.Is(err, errAuthThrottleUnavailable):
			writeAPIError(w, r, http.StatusServiceUnavailable, "auth_throttle_unavailable", "Login is temporarily unavailable; try again shortly.")
		default:
			writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		}
		return
	}

	failLogin := func() {
		_ = attempt.Complete(ctx, LoginFailure)
		writeAPIError(w, r, http.StatusUnauthorized, "invalid_credentials", "Invalid username or password.")
	}

	var userID string
	var hash string
	var active bool

	if unameErr != nil {
		// Unknown-shaped username: run one dummy verification to equalize
		// timing, then fail.
		_, _ = verifyPassword(body.Password, dummyPasswordHash)
		failLogin()
		return
	}

	err = dbPool.QueryRow(ctx, `
		SELECT id, password_hash, active FROM users WHERE username = $1
	`, canonUser).Scan(&userID, &hash, &active)
	if err != nil {
		_, _ = verifyPassword(body.Password, dummyPasswordHash)
		failLogin()
		return
	}

	ok, err := verifyPassword(body.Password, hash)
	if err != nil || !ok {
		failLogin()
		return
	}

	if !active {
		// Correct password but disabled: do not count as a failed attempt.
		_ = attempt.Complete(ctx, LoginSuccess)
		writeAPIError(w, r, http.StatusUnauthorized, "account_disabled", "This account is disabled.")
		return
	}

	// Successful login: release the reservation and reset prior failures.
	_ = attempt.Complete(ctx, LoginSuccess)
	clientIP := clientAddr.String()

	token, tokenHash, err := generateToken()
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	expiry := time.Now().Add(12 * time.Hour)

	ua := r.Header.Get("User-Agent")
	if len(ua) > 255 {
		ua = ua[:255]
	}
	_, err = dbPool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at, user_agent, client_ip)
		VALUES ($1, $2, $3, $4, $5)
	`, tokenHash, userID, expiry, ua, clientIP)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Set session cookie. Secure is relaxed in development so plain-http
	// access (LAN/Tailscale IPs) doesn't silently drop the cookie.
	http.SetCookie(w, &http.Cookie{
		Name:     "telos_session",
		Value:    token,
		Expires:  expiry,
		HttpOnly: true,
		Secure:   os.Getenv("TELOS_ENV") != "development",
		SameSite: http.SameSiteStrictMode,
		Path:     "/",
	})

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("telos_session")
	if err != nil {
		http.Error(w, "No active session", http.StatusBadRequest)
		return
	}

	h := sha256.Sum256([]byte(cookie.Value))
	tokenHash := hex.EncodeToString(h[:])

	_, err = dbPool.Exec(r.Context(), `
		UPDATE sessions SET revoked_at = NOW() WHERE token_hash = $1
	`, tokenHash)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	// Immediately close any live socket for this session.
	revokeSessionHash(tokenHash)

	// Delete cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "telos_session",
		Value:    "",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   os.Getenv("TELOS_ENV") != "development",
		SameSite: http.SameSiteStrictMode,
		Path:     "/",
	})

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleMe(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

func handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	roleID := "Member"
	var body struct {
		RoleID string `json:"roleId"`
	}
	// The body is optional — older clients send none — but when present it is
	// size-bounded and must be valid JSON.
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &body, securityConfig.AuthJSONBytes); err != nil {
			return
		}
		if body.RoleID != "" {
			roleID = body.RoleID
		}
	}
	if roleID == "Owner" && !containsRole(user.Roles, "Owner") {
		http.Error(w, "Forbidden: only an Owner can create Owner invites", http.StatusForbidden)
		return
	}
	var exists bool
	if err := dbPool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM roles WHERE id = $1)`, roleID).Scan(&exists); err != nil || !exists {
		http.Error(w, "Bad Request: unknown role", http.StatusBadRequest)
		return
	}
	if err := ensureCanAssignRoles(r.Context(), user, []string{roleID}); err != nil {
		status := http.StatusForbidden
		if !errors.Is(err, errPermissionAmplification) {
			status = http.StatusBadRequest
		}
		http.Error(w, http.StatusText(status)+": "+err.Error(), status)
		return
	}
	rawToken, tokenHash, err := generateToken()
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	expiry := time.Now().Add(7 * 24 * time.Hour) // Invite valid for 7 days

	_, err = dbPool.Exec(r.Context(), `
		INSERT INTO invites (token_hash, creator_id, expires_at, role_id) VALUES ($1, $2, $3, $4)
	`, tokenHash, user.ID, expiry, roleID)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"invite_token": rawToken,
		"expires_at":   expiry.Format(time.RFC3339),
		"role_id":      roleID,
	})
}

func handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.AuthJSONBytes); err != nil {
		return
	}

	username, err := canonicalUsername(body.Username)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_username", "Username must be 3-32 chars of a-z 0-9 . _ - and start alphanumeric.")
		return
	}
	if len(body.Password) < 15 || len(body.Password) > 128 {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_password", "Password must be 15-128 characters.")
		return
	}

	ctx := r.Context()
	h := sha256.Sum256([]byte(body.Token))
	tokenHash := hex.EncodeToString(h[:])

	hash, err := hashPassword(body.Password)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	tx, err := dbPool.Begin(ctx)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	defer tx.Rollback(ctx)

	// Atomically consume the invite: only one concurrent accept wins the
	// conditional UPDATE, so 20 simultaneous accepts create exactly one user.
	var inviteRole *string
	err = tx.QueryRow(ctx, `
		UPDATE invites SET used_at = NOW()
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > NOW()
		RETURNING role_id
	`, tokenHash).Scan(&inviteRole)
	if errors.Is(err, pgx.ErrNoRows) {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_invite", "The invite token is invalid, already used, or expired.")
		return
	}
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	var userID string
	err = tx.QueryRow(ctx, `
		INSERT INTO users (username, password_hash) VALUES ($1, $2) RETURNING id
	`, username, hash).Scan(&userID)
	if err != nil {
		if isUniqueViolation(err) {
			writeAPIError(w, r, http.StatusConflict, "username_taken", "That username is already taken.")
			return
		}
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	assignRole := "Member"
	if inviteRole != nil && *inviteRole != "" {
		assignRole = *inviteRole
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)`, userID, assignRole); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE invites SET used_by = $1 WHERE token_hash = $2`, userID, tokenHash); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	if err := securityEvents.Record(ctx, tx, SecurityEventIntent{Kind: "invite_accepted", ActorID: userID, SubjectID: userID}); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "success", "userId": userID})
}

// The Health handlers (liveness/readiness) live in health.go.

// ═══════════════════════════════════════════════════════════════════════════
// Handler — Chat / WebSocket
// ═══════════════════════════════════════════════════════════════════════════

// wsClient (bounded single-writer socket, RevocableConnection) lives in
// realtime.go.

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	userID := user.ID

	// A channel is mandatory and must be a real channel the user may view.
	// No default-channel bypass: an absent/invalid/hidden channel is refused
	// before any socket, subscription, or presence state is allocated.
	channelIDStr := r.URL.Query().Get("channel")
	if err := AuthorizeChannel(r.Context(), user, channelIDStr, ChannelView); err != nil {
		if errors.Is(err, errChannelForbidden) {
			writeAPIError(w, r, http.StatusForbidden, "forbidden", "You cannot access this channel.")
		} else {
			writeAPIError(w, r, http.StatusNotFound, "channel_not_found", "Channel not found.")
		}
		return
	}

	// Per-channel subscriber cap.
	if n, err := redisClient.HLen(r.Context(), "telos:presence:chan:"+channelIDStr).Result(); err == nil && n >= maxSubscribersPerChan {
		writeAPIError(w, r, http.StatusServiceUnavailable, "channel_full", "This channel is at capacity.")
		return
	}

	// Session hash keys the socket for targeted revocation.
	sessionHash := user.SessionHash
	deviceID := user.DeviceID
	if sessionHash == "" {
		rawToken, tokErr := sessionTokenFromRequest(r)
		if tokErr != nil {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required.")
			return
		}
		sessionHash = sha256Hex(rawToken)
	}

	var responseHeader http.Header
	if ticket := parseWSTicketHeader(r); ticket != "" {
		responseHeader = http.Header{"Sec-WebSocket-Protocol": []string{"telos-ticket." + ticket}}
	}

	conn, err := upgrader.Upgrade(w, r, responseHeader)
	if err != nil {
		log.Printf("WebSocket upgrade failed request_id=%s: %v", requestIDFrom(r.Context()), err)
		return
	}
	client := newWSClient(conn)
	defer client.close()

	release, ok := sessionRegistryInstance.RegisterDevice(sessionHash, userID, deviceID, client)
	if !ok {
		// Per-user socket cap reached.
		client.CloseWithCode(websocket.ClosePolicyViolation, "too many connections")
		return
	}
	defer release()

	conn.SetReadLimit(wsInboundLimitBytes)
	const pongWait = 60 * time.Second
	if err := conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		return
	}
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Join the fan-out goroutines before returning so no background goroutine
	// touches shared state after the handler exits.
	var wg sync.WaitGroup

	if msgs, err := getMessagesForChannel(ctx, channelIDStr); err == nil {
		client.sendJSON(WSNotification{Type: "history", Messages: msgs})
	}

	redisChanName := fmt.Sprintf("telos:chat:%s", channelIDStr)
	pubsub := redisClient.Subscribe(ctx, redisChanName)
	defer pubsub.Close()

	wg.Add(1)
	go func() {
		defer wg.Done()
		ch := pubsub.Channel()
		for {
			select {
			case redisMsg, ok := <-ch:
				if !ok {
					return
				}
				if !client.sendText([]byte(redisMsg.Payload)) {
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	presenceJoin(ctx, channelIDStr, userID)
	defer presenceLeave(ctx, channelIDStr, userID)

	// Heartbeat plus a 30-second authorization-revalidation fallback: if the
	// session expired, the account was disabled, or channel view was revoked,
	// close the socket even if the direct revocation signal was missed.
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	revalidate := time.NewTicker(30 * time.Second)
	defer revalidate.Stop()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ticker.C:
				if err := client.ping(); err != nil {
					cancel()
					client.close()
					return
				}
				redisClient.Set(ctx, "telos:presence:hb:"+userID, "1", 60*time.Second)
			case <-revalidate.C:
				fresh, err := revalidateSocket(ctx, sessionHash, channelIDStr)
				if err != nil || !fresh {
					client.CloseWithCode(websocket.ClosePolicyViolation, "session no longer valid")
					cancel()
					return
				}
			case <-ctx.Done():
				return
			case <-client.done:
				cancel()
				return
			}
		}
	}()

	log.Printf("client connected request_id=%s channel=%s", requestIDFrom(r.Context()), channelIDStr)

	// Inbound frames are not accepted; reading only detects disconnects and
	// enforces the read deadline/limit.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	// Stop the fan-out goroutines and wait for them before the deferred
	// presenceLeave/release run.
	cancel()
	wg.Wait()
}

// revalidateSocket re-checks that the session is still live (not
// revoked/expired), the account is active, and the user still holds view on
// the channel. It is the 30-second fallback behind direct revocation.
func revalidateSocket(ctx context.Context, sessionHash, channelID string) (bool, error) {
	var userID string
	var active bool
	var expiresAt time.Time
	err := dbPool.QueryRow(ctx, `
		SELECT s.user_id, u.active, s.expires_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.revoked_at IS NULL
	`, sessionHash).Scan(&userID, &active, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !active || time.Now().After(expiresAt) {
		return false, nil
	}
	user, err := loadUserContext(ctx, userID)
	if err != nil {
		return false, err
	}
	if err := AuthorizeChannel(ctx, user, channelID, ChannelView); err != nil {
		return false, nil
	}
	return true, nil
}

func presenceJoin(ctx context.Context, cid, uid string) {
	redisClient.HIncrBy(ctx, "telos:presence:chan:"+cid, uid, 1)
	redisClient.HIncrBy(ctx, "telos:presence:refs", uid, 1)
	redisClient.SAdd(ctx, "telos:presence:online", uid)
	redisClient.Set(ctx, "telos:presence:hb:"+uid, "1", 60*time.Second)

	broadcastPresence(ctx, cid)
}

func presenceLeave(ctx context.Context, cid, uid string) {
	n, _ := redisClient.HIncrBy(ctx, "telos:presence:chan:"+cid, uid, -1).Result()
	if n <= 0 {
		redisClient.HDel(ctx, "telos:presence:chan:"+cid, uid)
	}

	g, _ := redisClient.HIncrBy(ctx, "telos:presence:refs", uid, -1).Result()
	if g <= 0 {
		redisClient.HDel(ctx, "telos:presence:refs", uid)
		redisClient.SRem(ctx, "telos:presence:online", uid)
	}

	broadcastPresence(ctx, cid)
}

func broadcastPresence(ctx context.Context, cid string) {
	roster, count, err := channelRoster(ctx, cid)
	if err != nil {
		log.Printf("broadcastPresence error: %v", err)
		return
	}
	publishChatEvent(ctx, cid, WSEvent{
		Type: "presence",
		Presence: &WSPresence{
			ChannelID: cid,
			Online:    roster,
			Count:     count,
		},
	})
}

func channelRoster(ctx context.Context, cid string) ([]PresenceUser, int, error) {
	fields, err := redisClient.HGetAll(ctx, "telos:presence:chan:"+cid).Result()
	if err != nil {
		return nil, 0, err
	}

	var activeUserIDs []string
	for uid, countStr := range fields {
		count, _ := strconv.Atoi(countStr)
		if count > 0 {
			exists, _ := redisClient.Exists(ctx, "telos:presence:hb:"+uid).Result()
			if exists > 0 {
				activeUserIDs = append(activeUserIDs, uid)
			} else {
				redisClient.HDel(ctx, "telos:presence:chan:"+cid, uid)
				g, _ := redisClient.HIncrBy(ctx, "telos:presence:refs", uid, -1).Result()
				if g <= 0 {
					redisClient.HDel(ctx, "telos:presence:refs", uid)
					redisClient.SRem(ctx, "telos:presence:online", uid)
				}
			}
		}
	}

	roster := []PresenceUser{}
	if len(activeUserIDs) > 0 {
		rows, err := dbPool.Query(ctx, `
			SELECT id::text, username, COALESCE(display_name, ''), avatar_file_id IS NOT NULL
			FROM users
			WHERE id = ANY($1)
		`, activeUserIDs)
		if err != nil {
			return nil, 0, err
		}
		defer rows.Close()

		for rows.Next() {
			var u PresenceUser
			var hasAvatar bool
			if err := rows.Scan(&u.UserID, &u.Username, &u.DisplayName, &hasAvatar); err == nil {
				var role string
				dbPool.QueryRow(ctx, `
					SELECT role_id FROM user_roles WHERE user_id = $1 LIMIT 1
				`, u.UserID).Scan(&role)
				if role == "" {
					role = "Member"
				}
				u.Role = role

				u.Avatar = "MB"
				if len(u.Username) >= 2 {
					u.Avatar = strings.ToUpper(u.Username[:2])
				}
				if hasAvatar {
					u.AvatarUrl = "/api/v1/users/" + u.UserID + "/avatar"
				}
				roster = append(roster, u)
			}
		}
	}

	globalCount, _ := redisClient.SCard(ctx, "telos:presence:online").Result()
	return roster, int(globalCount), nil
}

func handleChannelMembers(w http.ResponseWriter, r *http.Request) {
	channelID, ok := authorizeChannelHTTP(w, r, ChannelView)
	if !ok {
		return
	}
	roster, count, err := channelRoster(r.Context(), channelID)
	if err != nil {
		http.Error(w, "failed to get roster", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"online": roster,
		"count":  count,
	})
}

func handlePinMessage(w http.ResponseWriter, r *http.Request) {
	// withAuth already gated this route on the moderation permission; here we
	// additionally require the channel to exist and be viewable.
	channelID, ok := authorizeChannelHTTP(w, r, ChannelView)
	if !ok {
		return
	}
	user := r.Context().Value(userContextKey).(*UserContext)
	var body struct {
		MessageID string `json:"messageId"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}

	var exists bool
	err := dbPool.QueryRow(r.Context(), `
		SELECT EXISTS(SELECT 1 FROM messages WHERE id=$1 AND channel_id=$2 AND deleted_at IS NULL)
	`, body.MessageID, channelID).Scan(&exists)
	if err != nil || !exists {
		http.Error(w, "message not found in channel", 404)
		return
	}

	_, err = dbPool.Exec(r.Context(), `
		INSERT INTO channel_pins (channel_id, message_id, pinned_by) VALUES ($1, $2, $3)
		ON CONFLICT (channel_id, message_id) DO NOTHING
	`, channelID, body.MessageID, user.ID)
	if err != nil {
		http.Error(w, "pin failed", 500)
		return
	}

	publishChatEvent(r.Context(), channelID, WSEvent{
		Type: "pin",
		Pin: &WSPin{
			MessageID: body.MessageID,
			Op:        "add",
		},
	})

	w.WriteHeader(201)
}

func handleUnpinMessage(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("id")
	mid := r.PathValue("mid")

	tag, err := dbPool.Exec(r.Context(), `
		DELETE FROM channel_pins WHERE channel_id=$1 AND message_id=$2
	`, channelID, mid)
	if err != nil {
		http.Error(w, "unpin failed", 500)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "pin not found", 404)
		return
	}

	publishChatEvent(r.Context(), channelID, WSEvent{
		Type: "pin",
		Pin: &WSPin{
			MessageID: mid,
			Op:        "remove",
		},
	})

	w.WriteHeader(204)
}

func handleListPins(w http.ResponseWriter, r *http.Request) {
	channelID, ok := authorizeChannelHTTP(w, r, ChannelView)
	if !ok {
		return
	}

	rows, err := dbPool.Query(r.Context(), `
		SELECT m.id::text, u.id::text, u.username, COALESCE(u.display_name, ''),
			u.avatar_file_id IS NOT NULL, m.content, m.created_at, m.edited_at,
			(cp.message_id IS NOT NULL) AS pinned,
			m.embed_kind, m.embed_ref, m.embed_snapshot
		FROM channel_pins cp
		JOIN messages m ON cp.message_id = m.id
		JOIN users u ON m.user_id = u.id
		WHERE cp.channel_id = $1 AND m.deleted_at IS NULL
		ORDER BY cp.pinned_at DESC
	`, channelID)
	if err != nil {
		http.Error(w, "failed to query pins", 500)
		return
	}
	defer rows.Close()

	pins := []WSMessage{}
	var ids []string
	for rows.Next() {
		var msg WSMessage
		var t time.Time
		var hasAvatar bool
		var editedAt *time.Time
		var embedKind, embedRef *string
		var embedSnapshot []byte
		err := rows.Scan(
			&msg.ID, &msg.SenderID, &msg.Sender, &msg.DisplayName, &hasAvatar,
			&msg.Content, &t, &editedAt, &msg.Pinned,
			&embedKind, &embedRef, &embedSnapshot,
		)
		if err != nil {
			http.Error(w, "failed to scan pin", 500)
			return
		}
		msg.Timestamp = t.Format("03:04 pm")
		if editedAt != nil {
			msg.EditedAt = editedAt.Format("03:04 pm")
		}
		if len(msg.Sender) >= 2 {
			msg.Avatar = strings.ToUpper(msg.Sender[:2])
		} else {
			msg.Avatar = "MB"
		}
		if hasAvatar {
			msg.AvatarUrl = "/api/v1/users/" + msg.SenderID + "/avatar"
		}
		if embedKind != nil && embedRef != nil {
			msg.Embed = &MessageEmbed{
				Kind:     *embedKind,
				Ref:      *embedRef,
				Snapshot: embedSnapshot,
			}
		}

		var role string
		dbPool.QueryRow(r.Context(), `
			SELECT role_id FROM user_roles WHERE user_id = $1 LIMIT 1
		`, msg.SenderID).Scan(&role)
		if role == "" {
			role = "Member"
		}
		msg.Role = role

		pins = append(pins, msg)
		ids = append(ids, msg.ID)
	}

	if len(ids) > 0 {
		rrows, err := dbPool.Query(r.Context(), `
			SELECT message_id::text, emoji, COUNT(*)::int, array_agg(user_id::text)
			FROM message_reactions
			WHERE message_id = ANY($1)
			GROUP BY message_id, emoji
		`, ids)
		if err == nil {
			defer rrows.Close()
			reactionsMap := make(map[string][]ReactionSummary)
			for rrows.Next() {
				var mid, emoji string
				var count int
				var users []string
				if err := rrows.Scan(&mid, &emoji, &count, &users); err == nil {
					reactionsMap[mid] = append(reactionsMap[mid], ReactionSummary{
						Emoji: emoji,
						Count: count,
						Users: users,
					})
				}
			}
			for i := range pins {
				if r, exists := reactionsMap[pins[i].ID]; exists {
					pins[i].Reactions = r
				} else {
					pins[i].Reactions = []ReactionSummary{}
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string][]WSMessage{"pins": pins})
}

type SearchUserResponse struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	AvatarUrl   string `json:"avatarUrl,omitempty"`
}

func handleUserSearch(w http.ResponseWriter, r *http.Request) {
	pattern, ok := normalizedSearchTerm(r.URL.Query().Get("q"))
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]SearchUserResponse{})
		return
	}

	// lower(col) LIKE matches the pg_trgm GIN index expressions; capped at 15.
	rows, err := dbPool.Query(r.Context(), `
		SELECT id::text, username, COALESCE(display_name, ''), avatar_file_id IS NOT NULL
		FROM users
		WHERE active = TRUE
		  AND (lower(username) LIKE $1 OR lower(coalesce(display_name,'')) LIKE $1)
		LIMIT $2
	`, pattern, searchScopeLimit)
	if err != nil {
		http.Error(w, "query failed", 500)
		return
	}
	defer rows.Close()

	results := []SearchUserResponse{}
	for rows.Next() {
		var u SearchUserResponse
		var hasAvatar bool
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &hasAvatar); err == nil {
			if hasAvatar {
				u.AvatarUrl = "/api/v1/users/" + u.ID + "/avatar"
			}
			results = append(results, u)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func buildWSMessage(ctx context.Context, msgID, userID, content string, ts time.Time) WSMessage {
	var username, displayName string
	var hasAvatar bool
	var role string
	var roles []string

	err := dbPool.QueryRow(ctx, `
		SELECT username, COALESCE(display_name, ''), avatar_file_id IS NOT NULL
		FROM users WHERE id = $1
	`, userID).Scan(&username, &displayName, &hasAvatar)
	if err != nil {
		log.Printf("buildWSMessage: failed to get user details: %v", err)
	}

	roleRows, err := dbPool.Query(ctx, `
		SELECT role_id FROM user_roles WHERE user_id = $1
	`, userID)
	if err == nil {
		for roleRows.Next() {
			var rID string
			if err := roleRows.Scan(&rID); err == nil {
				roles = append(roles, rID)
			}
		}
		roleRows.Close()
	}
	if len(roles) > 0 {
		role = roles[0]
	} else {
		role = "Member"
	}

	avatar := "MB"
	if len(username) >= 2 {
		avatar = strings.ToUpper(username[:2])
	}

	avatarURL := ""
	if hasAvatar {
		avatarURL = "/api/v1/users/" + userID + "/avatar"
	}

	var embedKind, embedRef *string
	var embedSnapshot []byte
	var pinned bool
	_ = dbPool.QueryRow(ctx, `
		SELECT embed_kind, embed_ref, embed_snapshot,
			EXISTS(SELECT 1 FROM channel_pins cp WHERE cp.message_id = messages.id) AS pinned
		FROM messages WHERE id = $1
	`, msgID).Scan(&embedKind, &embedRef, &embedSnapshot, &pinned)

	var embed *MessageEmbed
	if embedKind != nil && embedRef != nil {
		embed = &MessageEmbed{
			Kind:     *embedKind,
			Ref:      *embedRef,
			Snapshot: embedSnapshot,
		}
	}

	return WSMessage{
		ID:          msgID,
		Sender:      username,
		SenderID:    userID,
		DisplayName: displayName,
		Avatar:      avatar,
		AvatarUrl:   avatarURL,
		Role:        role,
		Content:     content,
		Timestamp:   ts.Format("03:04 pm"),
		Pinned:      pinned,
		Embed:       embed,
	}
}

func publishChatEvent(ctx context.Context, channelID string, ev WSEvent) {
	ev.ChannelID = channelID
	payload, err := json.Marshal(ev)
	if err != nil {
		log.Printf("marshal chat event: %v", err)
		return
	}
	if err := redisClient.Publish(ctx, "telos:chat:"+channelID, payload).Err(); err != nil {
		log.Printf("publish chat event: %v", err)
	}
}

func buildEmbedSnapshot(ctx context.Context, kind, ref string) (string, []byte, error) {
	var snapshot struct {
		Title    string `json:"title"`
		Subtitle string `json:"subtitle"`
		Kicker   string `json:"kicker"`
		Cover    string `json:"cover"`
		Duration string `json:"duration"`
	}

	switch kind {
	case "library_book":
		resolution, err := resolveAuthorizedCatalogTarget(ctx, "book", ref)
		if err != nil {
			return "", nil, errors.New("book not found")
		}
		requestCtx, cancel := upstreamRequestContext(ctx)
		defer cancel()
		book, err := fetchGrimmoryBook(requestCtx, resolution.UpstreamID)
		if err != nil {
			if errors.Is(err, errGrimmoryBookNotFound) {
				return "", nil, errors.New("book not found")
			}
			return "", nil, fmt.Errorf("%w: grimmory", errUpstreamUnavailable)
		}
		if strconv.FormatInt(book.UpstreamID, 10) != resolution.UpstreamID {
			return "", nil, errors.New("book not found")
		}
		snapshot.Title = book.Title
		snapshot.Subtitle = strings.Join(book.Authors, ", ")
		snapshot.Kicker = "Library Book"
		snapshot.Cover = "/api/v1/library/books/" + resolution.ID + "/cover"
		ref = resolution.ID

	case "stream_film":
		resolution, err := resolveAuthorizedCatalogTarget(ctx, "media", ref)
		if err != nil || !validJellyfinID(resolution.UpstreamID) {
			return "", nil, errors.New("invalid media id")
		}
		userID, err := getJellyfinUserID(ctx)
		if err != nil {
			return "", nil, fmt.Errorf("%w: jellyfin: %v", errUpstreamUnavailable, err)
		}

		token := getJellyfinAdminToken()
		reqURL := fmt.Sprintf("%s/Users/%s/Items/%s", jellyfinBaseURL, userID, resolution.UpstreamID)
		requestCtx, cancel := upstreamRequestContext(ctx)
		defer cancel()
		req, err := http.NewRequestWithContext(requestCtx, "GET", reqURL, nil)
		if err != nil {
			return "", nil, err
		}
		req.Header.Set("X-Emby-Token", token)
		req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))

		resp, err := upstreamHTTPClient.Do(req)
		if err != nil {
			return "", nil, fmt.Errorf("%w: jellyfin: %v", errUpstreamUnavailable, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", nil, fmt.Errorf("%w: jellyfin returned %s", errUpstreamUnavailable, resp.Status)
		}

		var rawItem struct {
			ID             string `json:"Id"`
			Name           string `json:"Name"`
			Type           string `json:"Type"`
			ProductionYear int    `json:"ProductionYear"`
			RunTimeTicks   int64  `json:"RunTimeTicks"`
			Studios        []struct {
				Name string `json:"Name"`
			} `json:"Studios"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&rawItem); err != nil {
			return "", nil, err
		}
		if rawItem.ID != resolution.UpstreamID {
			return "", nil, errors.New("media not found")
		}

		durationSec := rawItem.RunTimeTicks / 10000000
		h := durationSec / 3600
		m := (durationSec % 3600) / 60
		durationStr := ""
		if h > 0 {
			durationStr = fmt.Sprintf("%dh %dm", h, m)
		} else {
			durationStr = fmt.Sprintf("%dm", m)
		}

		snapshot.Title = rawItem.Name
		snapshot.Kicker = "Stream Video"
		if rawItem.Type == "Audio" || rawItem.Type == "Audiobook" {
			snapshot.Kicker = "Stream Audio"
		}
		snapshot.Cover = "/api/v1/media/items/" + resolution.ID + "/cover"
		snapshot.Duration = durationStr
		ref = resolution.ID

		sub := ""
		if len(rawItem.Studios) > 0 {
			sub = rawItem.Studios[0].Name
		}
		if rawItem.ProductionYear > 0 {
			if sub != "" {
				sub += fmt.Sprintf(" (%d)", rawItem.ProductionYear)
			} else {
				sub = fmt.Sprintf("%d", rawItem.ProductionYear)
			}
		}
		snapshot.Subtitle = sub

	case "file":
		// Files are not catalog items — the identity layer covers only the
		// 'library' and 'stream' surfaces — so a file ref stays as passed.
		filename, size, mime, err := resolveSharedFileMeta(ctx, ref)
		if err != nil {
			return "", nil, err
		}

		snapshot.Title = filename
		snapshot.Kicker = "Shared File"

		var sizeStr string
		if size >= 1024*1024 {
			sizeStr = fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
		} else if size >= 1024 {
			sizeStr = fmt.Sprintf("%.1f KB", float64(size)/1024)
		} else {
			sizeStr = fmt.Sprintf("%d B", size)
		}
		snapshot.Subtitle = fmt.Sprintf("%s · %s", sizeStr, mime)

	default:
		return "", nil, errors.New("unsupported embed kind")
	}

	raw, err := json.Marshal(snapshot)
	return ref, raw, err
}

func handleSendMessage(w http.ResponseWriter, r *http.Request) {
	channelID, ok := authorizeChannelHTTP(w, r, ChannelSend)
	if !ok {
		return
	}
	user := r.Context().Value(userContextKey).(*UserContext)
	var body struct {
		Content          string `json:"content"`
		ClientMutationID string `json:"clientMutationId"`
		Embed            *struct {
			Kind string `json:"kind"`
			Ref  string `json:"ref"`
		} `json:"embed"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}
	body.Content = strings.TrimSpace(body.Content)
	if body.Content == "" && body.Embed == nil {
		http.Error(w, "empty message", 400)
		return
	}
	if len(body.Content) > 4000 {
		http.Error(w, "too long", 400)
		return
	}

	var embed messageEmbedInput
	if body.Embed != nil {
		k, ref := body.Embed.Kind, body.Embed.Ref
		canonicalRef, snap, err := buildEmbedSnapshot(r.Context(), k, ref)
		if err != nil {
			if errors.Is(err, errUpstreamUnavailable) {
				http.Error(w, "embed source unavailable", http.StatusServiceUnavailable)
				return
			}
			http.Error(w, "invalid embed: "+err.Error(), http.StatusBadRequest)
			return
		}
		embed = messageEmbedInput{Kind: &k, Ref: &canonicalRef, Snapshot: snap}
	}

	msgID, ts, ok := persistChatMessage(w, r, channelID, user.ID, body.Content, body.ClientMutationID, nil, embed)
	if !ok {
		return
	}

	msg := buildWSMessage(r.Context(), msgID, user.ID, body.Content, ts)
	publishChatEvent(r.Context(), channelID, WSEvent{Type: "message", Message: &msg})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	json.NewEncoder(w).Encode(map[string]*WSMessage{"message": &msg})
}

// persistChatMessage runs the transactional create (idempotency + change log +
// notifications) and maps thread errors to HTTP responses. It returns ok=false
// after writing an error response.
func persistChatMessage(w http.ResponseWriter, r *http.Request, channelID, authorID, content, clientMutationID string, threadRootID *string, embed messageEmbedInput) (string, time.Time, bool) {
	tx, err := dbPool.Begin(r.Context())
	if err != nil {
		http.Error(w, "persist failed", 500)
		return "", time.Time{}, false
	}
	defer tx.Rollback(r.Context())
	msgID, ts, _, err := createChatMessageTx(r.Context(), tx, channelID, authorID, content, clientMutationID, threadRootID, embed)
	if err != nil {
		switch {
		case errors.Is(err, errReplyToReply), errors.Is(err, errThreadRootMissing):
			http.Error(w, "invalid thread root", http.StatusBadRequest)
		default:
			http.Error(w, "persist failed", 500)
		}
		return "", time.Time{}, false
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, "persist failed", 500)
		return "", time.Time{}, false
	}
	return msgID, ts, true
}

// handleSendReply posts a one-level thread reply under a root message.
func handleSendReply(w http.ResponseWriter, r *http.Request) {
	channelID, ok := authorizeChannelHTTP(w, r, ChannelSend)
	if !ok {
		return
	}
	rootID := r.PathValue("rootID")
	if !looksLikeUUID(rootID) {
		http.Error(w, "invalid thread root", http.StatusBadRequest)
		return
	}
	user := r.Context().Value(userContextKey).(*UserContext)
	var body struct {
		Content          string `json:"content"`
		ClientMutationID string `json:"clientMutationId"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}
	body.Content = strings.TrimSpace(body.Content)
	if body.Content == "" || len(body.Content) > 4000 {
		http.Error(w, "invalid content", 400)
		return
	}
	msgID, ts, ok := persistChatMessage(w, r, channelID, user.ID, body.Content, body.ClientMutationID, &rootID, messageEmbedInput{})
	if !ok {
		return
	}
	msg := buildWSMessage(r.Context(), msgID, user.ID, body.Content, ts)
	publishChatEvent(r.Context(), channelID, WSEvent{Type: "message", Message: &msg})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	json.NewEncoder(w).Encode(map[string]*WSMessage{"message": &msg})
}

func handleEditMessage(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("id")
	mid := r.PathValue("mid")
	user := r.Context().Value(userContextKey).(*UserContext)

	var body struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}
	body.Content = strings.TrimSpace(body.Content)
	if body.Content == "" || len(body.Content) > 4000 {
		http.Error(w, "invalid content", 400)
		return
	}

	var ts time.Time
	err := dbPool.QueryRow(r.Context(), `
		UPDATE messages SET content=$1, edited_at=now()
		WHERE id=$2 AND channel_id=$3 AND user_id=$4 AND deleted_at IS NULL
		RETURNING created_at`, body.Content, mid, channelID, user.ID).Scan(&ts)
	if err != nil {
		http.Error(w, "not found or not yours", 404)
		return
	}

	msg := buildWSMessage(r.Context(), mid, user.ID, body.Content, ts)
	msg.EditedAt = time.Now().Format("03:04 pm")
	publishChatEvent(r.Context(), channelID, WSEvent{Type: "message.update", Message: &msg})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]*WSMessage{"message": &msg})
}

func handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("id")
	mid := r.PathValue("mid")
	user := r.Context().Value(userContextKey).(*UserContext)

	isMod, err := hasPermission(r.Context(), user, "manage_messages", nil)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	tag, err := dbPool.Exec(r.Context(), `
		UPDATE messages SET deleted_at=now(), content='', embed_kind=NULL, embed_ref=NULL, embed_snapshot=NULL
		WHERE id=$1 AND channel_id=$2 AND deleted_at IS NULL AND ($3 OR user_id=$4)`,
		mid, channelID, isMod, user.ID)
	if err != nil {
		http.Error(w, "delete failed", 500)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "not found or forbidden", 404)
		return
	}

	_, _ = dbPool.Exec(r.Context(), `DELETE FROM channel_pins WHERE message_id=$1`, mid)

	publishChatEvent(r.Context(), channelID, WSEvent{Type: "message.delete", MessageID: mid})
	w.WriteHeader(204)
}

func isEmoji(s string) bool {
	runes := []rune(s)
	if len(runes) == 0 || len(runes) > 8 {
		return false
	}
	for _, r := range runes {
		if r < 127 && ((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r < 32) {
			return false
		}
	}
	return true
}

func handleAddReaction(w http.ResponseWriter, r *http.Request) {
	channelID, ok := authorizeChannelHTTP(w, r, ChannelSend)
	if !ok {
		return
	}
	mid := r.PathValue("mid")
	user := r.Context().Value(userContextKey).(*UserContext)

	var body struct {
		Emoji string `json:"emoji"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}
	if !isEmoji(body.Emoji) {
		http.Error(w, "bad emoji", 400)
		return
	}

	var exists bool
	err := dbPool.QueryRow(r.Context(), `
		SELECT EXISTS(SELECT 1 FROM messages WHERE id=$1 AND channel_id=$2 AND deleted_at IS NULL)
	`, mid, channelID).Scan(&exists)
	if err != nil || !exists {
		http.Error(w, "message not found", 404)
		return
	}

	_, err = dbPool.Exec(r.Context(), `
		INSERT INTO message_reactions (message_id, user_id, emoji) VALUES ($1, $2, $3)
		ON CONFLICT (message_id, user_id, emoji) DO NOTHING
	`, mid, user.ID, body.Emoji)
	if err != nil {
		http.Error(w, "failed to add reaction", 500)
		return
	}

	var count int
	err = dbPool.QueryRow(r.Context(), `
		SELECT COUNT(*) FROM message_reactions WHERE message_id=$1 AND emoji=$2
	`, mid, body.Emoji).Scan(&count)
	if err != nil {
		http.Error(w, "failed to count reactions", 500)
		return
	}

	publishChatEvent(r.Context(), channelID, WSEvent{
		Type: "reaction",
		Reaction: &WSReaction{
			MessageID: mid,
			Emoji:     body.Emoji,
			UserID:    user.ID,
			Op:        "add",
			Count:     count,
		},
	})
	w.WriteHeader(200)
}

func handleRemoveReaction(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("id")
	mid := r.PathValue("mid")
	emoji := r.PathValue("emoji")
	if unescaped, err := url.PathUnescape(emoji); err == nil {
		emoji = unescaped
	}
	if !isEmoji(emoji) {
		http.Error(w, "bad emoji", 400)
		return
	}
	user := r.Context().Value(userContextKey).(*UserContext)

	tag, err := dbPool.Exec(r.Context(), `
		DELETE FROM message_reactions WHERE message_id=$1 AND user_id=$2 AND emoji=$3
	`, mid, user.ID, emoji)
	if err != nil {
		http.Error(w, "failed to remove reaction", 500)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "reaction not found", 404)
		return
	}

	var count int
	err = dbPool.QueryRow(r.Context(), `
		SELECT COUNT(*) FROM message_reactions WHERE message_id=$1 AND emoji=$2
	`, mid, emoji).Scan(&count)
	if err != nil {
		http.Error(w, "failed to count reactions", 500)
		return
	}

	publishChatEvent(r.Context(), channelID, WSEvent{
		Type: "reaction",
		Reaction: &WSReaction{
			MessageID: mid,
			Emoji:     emoji,
			UserID:    user.ID,
			Op:        "remove",
			Count:     count,
		},
	})
	w.WriteHeader(204)
}

type ChannelResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func handleListChannels(w http.ResponseWriter, r *http.Request) {
	rows, err := dbPool.Query(r.Context(), `
		SELECT id::text, name FROM channels ORDER BY name
	`)
	if err != nil {
		http.Error(w, "Failed to list channels", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	channels := []ChannelResponse{}
	for rows.Next() {
		var c ChannelResponse
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			http.Error(w, "Failed to scan channel entry", http.StatusInternalServerError)
			return
		}
		channels = append(channels, c)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(channels)
}

func getMessagesForChannel(ctx context.Context, channelID string) ([]WSMessage, error) {
	rows, err := dbPool.Query(ctx, `
		SELECT m.id::text, u.id::text, u.username, COALESCE(u.display_name, ''),
			u.avatar_file_id IS NOT NULL,
			CASE WHEN m.deleted_at IS NULL THEN m.content ELSE '' END,
			m.created_at, m.edited_at, m.deleted_at IS NOT NULL,
			(cp.message_id IS NOT NULL) AS pinned,
			m.embed_kind, m.embed_ref, m.embed_snapshot
		FROM messages m
		JOIN users u ON m.user_id = u.id
		LEFT JOIN channel_pins cp ON cp.message_id = m.id AND cp.channel_id = m.channel_id
		WHERE m.channel_id = $1
		ORDER BY m.created_at DESC
		LIMIT 50
	`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	msgs := []WSMessage{}
	var ids []string
	for rows.Next() {
		var msg WSMessage
		var t time.Time
		var hasAvatar bool
		var editedAt *time.Time
		var embedKind, embedRef *string
		var embedSnapshot []byte
		err := rows.Scan(
			&msg.ID, &msg.SenderID, &msg.Sender, &msg.DisplayName, &hasAvatar,
			&msg.Content, &t, &editedAt, &msg.Deleted, &msg.Pinned,
			&embedKind, &embedRef, &embedSnapshot,
		)
		if err != nil {
			return nil, err
		}
		msg.Timestamp = t.Format("03:04 pm")
		if editedAt != nil {
			msg.EditedAt = editedAt.Format("03:04 pm")
		}
		if len(msg.Sender) >= 2 {
			msg.Avatar = strings.ToUpper(msg.Sender[:2])
		} else {
			msg.Avatar = "MB"
		}
		if hasAvatar {
			msg.AvatarUrl = "/api/v1/users/" + msg.SenderID + "/avatar"
		}
		if embedKind != nil && embedRef != nil {
			msg.Embed = &MessageEmbed{
				Kind:     *embedKind,
				Ref:      *embedRef,
				Snapshot: embedSnapshot,
			}
		}

		// Resolve role
		var role string
		dbPool.QueryRow(ctx, `
			SELECT role_id FROM user_roles WHERE user_id = (SELECT id FROM users WHERE username = $1) LIMIT 1
		`, msg.Sender).Scan(&role)
		if role == "" {
			role = "Member"
		}
		msg.Role = role

		msgs = append(msgs, msg)
		ids = append(ids, msg.ID)
	}

	if len(ids) > 0 {
		rrows, err := dbPool.Query(ctx, `
			SELECT message_id::text, emoji, COUNT(*)::int, array_agg(user_id::text)
			FROM message_reactions
			WHERE message_id = ANY($1)
			GROUP BY message_id, emoji
			ORDER BY MIN(created_at)
		`, ids)
		if err == nil {
			defer rrows.Close()
			reactionsMap := make(map[string][]ReactionSummary)
			for rrows.Next() {
				var mid, emoji string
				var count int
				var users []string
				if err := rrows.Scan(&mid, &emoji, &count, &users); err == nil {
					reactionsMap[mid] = append(reactionsMap[mid], ReactionSummary{
						Emoji: emoji,
						Count: count,
						Users: users,
					})
				}
			}
			for i := range msgs {
				if r, exists := reactionsMap[msgs[i].ID]; exists {
					msgs[i].Reactions = r
				} else {
					msgs[i].Reactions = []ReactionSummary{}
				}
			}
		}
	}

	// Reverse to chronological ascending
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// ═══════════════════════════════════════════════════════════════════════════
// Jellyfin Media Proxy
// ═══════════════════════════════════════════════════════════════════════════

var (
	jellyfinBaseURL = "http://jellyfin:8096/jellyfin"
)

func validJellyfinID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') &&
			(r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func validUpstreamSubpath(subpath string) bool {
	if subpath == "" || len(subpath) > 1024 || strings.ContainsAny(subpath, "\\\x00") {
		return false
	}
	for _, segment := range strings.Split(subpath, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func getJellyfinAdminToken() string {
	return os.Getenv("JELLYFIN_ADMIN_TOKEN")
}

func getJellyfinUserID(ctx context.Context) (string, error) {
	if redisClient != nil {
		val, err := redisClient.Get(ctx, "telos:jellyfin:userId").Result()
		if err == nil && val != "" {
			return val, nil
		}
	}

	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()
	token := getJellyfinAdminToken()
	req, err := http.NewRequestWithContext(requestCtx, "GET", jellyfinBaseURL+"/Users", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Emby-Token", token)
	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))

	resp, err := upstreamHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("jellyfin users api returned status: %s", resp.Status)
	}

	var users []struct {
		ID   string `json:"Id"`
		Name string `json:"Name"`
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(bodyBytes, &users); err != nil {
		return "", err
	}

	if len(users) == 0 {
		return "", fmt.Errorf("no users found on jellyfin server")
	}

	userID := ""
	if configuredUser := os.Getenv("JELLYFIN_USER_NAME"); configuredUser != "" {
		for _, u := range users {
			if strings.EqualFold(u.Name, configuredUser) {
				userID = u.ID
				break
			}
		}
	}
	if userID == "" {
		userID = users[0].ID
	}

	if redisClient != nil {
		_ = redisClient.Set(ctx, "telos:jellyfin:userId", userID, 1*time.Hour).Err()
	}

	return userID, nil
}

type mediaLibrary struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`           // "video" or "audio"
	CollectionType string `json:"collectionType"` // Jellyfin's raw, lowercased collection type
}

func handleMedia(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if redisClient != nil {
		val, err := redisClient.Get(ctx, "telos:jellyfin:libraries:canonical-v1").Result()
		if err == nil && val != "" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(val))
			return
		}
	}

	userID, err := getJellyfinUserID(ctx)
	if err != nil {
		http.Error(w, "Jellyfin unreachable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	token := getJellyfinAdminToken()
	reqURL := fmt.Sprintf("%s/Users/%s/Views", jellyfinBaseURL, userID)
	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, "GET", reqURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("X-Emby-Token", token)
	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))

	resp, err := upstreamHTTPClient.Do(req)
	if err != nil {
		http.Error(w, "Jellyfin Views request failed: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "Jellyfin Views returned error: "+resp.Status, http.StatusServiceUnavailable)
		return
	}

	var jResp struct {
		Items []struct {
			ID             string `json:"Id"`
			Name           string `json:"Name"`
			CollectionType string `json:"CollectionType"`
		} `json:"Items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jResp); err != nil {
		http.Error(w, "Failed to decode Jellyfin Views response", http.StatusInternalServerError)
		return
	}

	var libs []mediaLibrary
	for _, item := range jResp.Items {
		if !jellyfinLibraryAllowed(item.ID) {
			continue
		}
		mediaType := "video"
		cType := strings.ToLower(item.CollectionType)
		// Jellyfin 10.9+ has no distinct "audiobooks" collection type; audiobook
		// libraries are created as CollectionType "books". Telos gets text
		// e-books from Grimmory, so any Jellyfin "books" library is audio here.
		if cType == "music" || cType == "audiobooks" || cType == "audio" || cType == "podcasts" || cType == "books" {
			mediaType = "audio"
		}
		resolution, err := observeJellyfinCatalogItem(ctx, item.ID, item.ID, "", true)
		if err != nil {
			http.Error(w, "Failed to reconcile Jellyfin Views response", http.StatusServiceUnavailable)
			return
		}
		libs = append(libs, mediaLibrary{
			ID:             resolution.ID,
			Name:           item.Name,
			Type:           mediaType,
			CollectionType: cType,
		})
	}

	respJSON, err := json.Marshal(libs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if redisClient != nil {
		_ = redisClient.Set(ctx, "telos:jellyfin:libraries:canonical-v1", string(respJSON), 5*time.Minute).Err()
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(respJSON)
}

type jellyfinTaskResult struct {
	Status       string `json:"Status"`
	EndTimeUTC   string `json:"EndTimeUtc"`
	ErrorMessage string `json:"ErrorMessage"`
}

type jellyfinTaskInfo struct {
	ID                        string              `json:"Id"`
	Key                       string              `json:"Key"`
	State                     string              `json:"State"`
	CurrentProgressPercentage *float64            `json:"CurrentProgressPercentage"`
	LastExecutionResult       *jellyfinTaskResult `json:"LastExecutionResult"`
}

type mediaRefreshResponse struct {
	Status    string   `json:"status"`
	Message   string   `json:"message"`
	Progress  *float64 `json:"progress,omitempty"`
	UpdatedAt string   `json:"updatedAt"`
}

var (
	mediaRefreshPollInterval = time.Second
	mediaRefreshMaxDuration  = 5 * time.Minute
	mediaRefreshState        = struct {
		sync.Mutex
		response mediaRefreshResponse
	}{response: mediaRefreshResponse{
		Status:    "idle",
		Message:   "No Jellyfin scan is running.",
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}}
)

func mediaRefreshIsActive(status string) bool {
	return status == "starting" || status == "scanning" || status == "refreshing"
}

func getMediaRefreshResponse() mediaRefreshResponse {
	mediaRefreshState.Lock()
	defer mediaRefreshState.Unlock()
	return mediaRefreshState.response
}

func setMediaRefreshResponse(status, message string, progress *float64) mediaRefreshResponse {
	mediaRefreshState.Lock()
	defer mediaRefreshState.Unlock()
	mediaRefreshState.response = mediaRefreshResponse{
		Status:    status,
		Message:   message,
		Progress:  progress,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	return mediaRefreshState.response
}

func writeMediaRefreshResponse(w http.ResponseWriter, statusCode int, response mediaRefreshResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("WARN: failed to encode media refresh response: %v", err)
	}
}

func jellyfinControlRequest(ctx context.Context, method, path string) (*http.Response, context.CancelFunc, error) {
	requestCtx, cancel := upstreamRequestContext(ctx)
	req, err := http.NewRequestWithContext(requestCtx, method, jellyfinBaseURL+path, nil)
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	token := getJellyfinAdminToken()
	req.Header.Set("X-Emby-Token", token)
	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))
	resp, err := upstreamHTTPClient.Do(req)
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	return resp, cancel, nil
}

func getJellyfinRefreshTask(ctx context.Context) (jellyfinTaskInfo, error) {
	resp, cancel, err := jellyfinControlRequest(ctx, http.MethodGet, "/ScheduledTasks")
	if err != nil {
		return jellyfinTaskInfo{}, err
	}
	defer cancel()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return jellyfinTaskInfo{}, fmt.Errorf("scheduled tasks returned %s", resp.Status)
	}

	var tasks []jellyfinTaskInfo
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		return jellyfinTaskInfo{}, fmt.Errorf("decode scheduled tasks: %w", err)
	}
	for _, task := range tasks {
		if task.Key == "RefreshLibrary" {
			return task, nil
		}
	}
	return jellyfinTaskInfo{}, errors.New("Jellyfin library scan task not found")
}

func getJellyfinTask(ctx context.Context, id string) (jellyfinTaskInfo, error) {
	resp, cancel, err := jellyfinControlRequest(ctx, http.MethodGet, "/ScheduledTasks/"+url.PathEscape(id))
	if err != nil {
		return jellyfinTaskInfo{}, err
	}
	defer cancel()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return jellyfinTaskInfo{}, fmt.Errorf("scheduled task returned %s", resp.Status)
	}
	var task jellyfinTaskInfo
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return jellyfinTaskInfo{}, fmt.Errorf("decode scheduled task: %w", err)
	}
	return task, nil
}

func startJellyfinLibraryRefresh(ctx context.Context) error {
	resp, cancel, err := jellyfinControlRequest(ctx, http.MethodPost, "/Library/Refresh")
	if err != nil {
		return err
	}
	defer cancel()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("library refresh returned %s", resp.Status)
	}
	return nil
}

func invalidateJellyfinCache(ctx context.Context) {
	if redisClient == nil {
		return
	}
	iter := redisClient.Scan(ctx, 0, "telos:jellyfin:*", 100).Iterator()
	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		log.Printf("WARN: failed to scan jellyfin cache keys: %v", err)
	}
	if len(keys) > 0 {
		if err := redisClient.Del(ctx, keys...).Err(); err != nil {
			log.Printf("WARN: failed to drop jellyfin cache keys: %v", err)
		}
	}
}

// monitorJellyfinRefresh owns the asynchronous portion of a manual scan. It
// waits for Jellyfin's RefreshLibrary scheduled task to finish before clearing
// gateway caches; this ensures removed files cannot be repopulated from data
// fetched while Jellyfin was still scanning.
func monitorJellyfinRefresh(taskID, previousEndTime string, observedRunning bool) {
	ctx, cancel := context.WithTimeout(context.Background(), mediaRefreshMaxDuration)
	defer cancel()
	ticker := time.NewTicker(mediaRefreshPollInterval)
	defer ticker.Stop()
	lastErr := error(nil)

	for {
		select {
		case <-ctx.Done():
			message := "Jellyfin scan timed out after five minutes; the scan may still be running."
			if lastErr != nil {
				message = "Jellyfin scan status became unavailable and timed out."
			}
			setMediaRefreshResponse("timeout", message, nil)
			return
		case <-ticker.C:
			task, err := getJellyfinTask(ctx, taskID)
			if err != nil {
				lastErr = err
				continue
			}
			lastErr = nil
			if strings.EqualFold(task.State, "Running") {
				observedRunning = true
				setMediaRefreshResponse("scanning", "Jellyfin is scanning the shared media libraries.", task.CurrentProgressPercentage)
				continue
			}

			resultChanged := task.LastExecutionResult != nil &&
				task.LastExecutionResult.EndTimeUTC != "" &&
				task.LastExecutionResult.EndTimeUTC != previousEndTime
			firstResultFinished := previousEndTime == "" && observedRunning &&
				task.LastExecutionResult != nil && task.LastExecutionResult.EndTimeUTC != ""
			if !resultChanged && !firstResultFinished {
				continue
			}
			if task.LastExecutionResult == nil {
				continue
			}

			if strings.EqualFold(task.LastExecutionResult.Status, "Completed") {
				setMediaRefreshResponse("refreshing", "Refreshing the Telos media catalog.", nil)
				report, err := reconcileJellyfinCatalog(ctx)
				if err != nil {
					log.Printf("WARN: Jellyfin catalog reconciliation failed after observed=%d scans=%d backfill_updated=%d: %v", report.Observed, len(report.Scans), report.Backfill.Updated, err)
					setMediaRefreshResponse("failed", "Jellyfin scan completed, but Telos catalog reconciliation failed.", nil)
					return
				}
				cacheCtx, cacheCancel := context.WithTimeout(context.Background(), upstreamRequestTimeout)
				invalidateJellyfinCache(cacheCtx)
				cacheCancel()
				setMediaRefreshResponse("complete", "Jellyfin scan complete.", nil)
				return
			}

			message := "Jellyfin could not complete the library scan."
			if task.LastExecutionResult.ErrorMessage != "" {
				message = "Jellyfin scan failed: " + task.LastExecutionResult.ErrorMessage
			}
			setMediaRefreshResponse("failed", message, nil)
			return
		}
	}
}

// handleMediaRefresh starts one manual Jellyfin scan. Concurrent requests join
// the in-flight scan instead of queueing duplicate work.
func handleMediaRefresh(w http.ResponseWriter, r *http.Request) {
	mediaRefreshState.Lock()
	if mediaRefreshIsActive(mediaRefreshState.response.Status) {
		response := mediaRefreshState.response
		mediaRefreshState.Unlock()
		writeMediaRefreshResponse(w, http.StatusAccepted, response)
		return
	}
	mediaRefreshState.response = mediaRefreshResponse{
		Status:    "starting",
		Message:   "Starting Jellyfin library scan.",
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	mediaRefreshState.Unlock()

	task, err := getJellyfinRefreshTask(r.Context())
	if err != nil {
		setMediaRefreshResponse("failed", "Jellyfin scan could not be started.", nil)
		http.Error(w, "Jellyfin unreachable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	previousEndTime := ""
	if task.LastExecutionResult != nil {
		previousEndTime = task.LastExecutionResult.EndTimeUTC
	}
	alreadyRunning := strings.EqualFold(task.State, "Running")
	if !alreadyRunning {
		if err := startJellyfinLibraryRefresh(r.Context()); err != nil {
			setMediaRefreshResponse("failed", "Jellyfin scan could not be started.", nil)
			http.Error(w, "Jellyfin unreachable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
	}

	response := setMediaRefreshResponse("scanning", "Jellyfin is scanning the shared media libraries.", task.CurrentProgressPercentage)
	go monitorJellyfinRefresh(task.ID, previousEndTime, alreadyRunning)
	writeMediaRefreshResponse(w, http.StatusAccepted, response)
}

func handleMediaRefreshStatus(w http.ResponseWriter, _ *http.Request) {
	writeMediaRefreshResponse(w, http.StatusOK, getMediaRefreshResponse())
}

type MediaPlayableItem struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Duration   string `json:"duration"`
	Type       string `json:"type"`
	IsFolder   bool   `json:"isFolder"`
	ChildCount int    `json:"childCount,omitempty"`
}

type jellyfinChildItem struct {
	ID           string `json:"Id"`
	Name         string `json:"Name"`
	RunTimeTicks int64  `json:"RunTimeTicks"`
	Type         string `json:"Type"`
	IsFolder     bool   `json:"IsFolder"`
	ChildCount   int    `json:"ChildCount"`
}

// mapJellyfinChildren converts Jellyfin's raw child items (the direct
// children of a ParentId — a library's series, a series' seasons, a
// season's episodes, an audiobook's chapters, etc.) into the gateway's
// wire format.
func mapJellyfinChildren(items []jellyfinChildItem) []MediaPlayableItem {
	result := make([]MediaPlayableItem, 0, len(items))
	for _, item := range items {
		durationStr := ""
		if item.RunTimeTicks > 0 {
			seconds := item.RunTimeTicks / 10000000
			h := seconds / 3600
			m := (seconds % 3600) / 60
			if h > 0 {
				durationStr = fmt.Sprintf("%dh %dm", h, m)
			} else {
				durationStr = fmt.Sprintf("%dm", m)
			}
		} else {
			durationStr = "0m"
		}

		result = append(result, MediaPlayableItem{
			ID:         item.ID,
			Title:      item.Name,
			Duration:   durationStr,
			Type:       item.Type,
			IsFolder:   item.IsFolder,
			ChildCount: item.ChildCount,
		})
	}
	return result
}

func handleMediaItems(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rawParentID := r.URL.Query().Get("parentId")
	if rawParentID == "" {
		rawParentID = r.URL.Query().Get("libraryId")
	}
	if !validJellyfinID(rawParentID) {
		http.Error(w, "Missing parentId or libraryId parameter", http.StatusBadRequest)
		return
	}
	parent, ok := resolveJellyfinItemHTTP(w, r, rawParentID)
	if !ok {
		return
	}
	parentID := parent.UpstreamID

	cacheKey := fmt.Sprintf("telos:jellyfin:library-items:canonical-v1:%s", parentID)
	if redisClient != nil {
		val, err := redisClient.Get(ctx, cacheKey).Result()
		if err == nil && val != "" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(val))
			return
		}
	}

	userID, err := getJellyfinUserID(ctx)
	if err != nil {
		http.Error(w, "Jellyfin unreachable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	token := getJellyfinAdminToken()
	reqURL := fmt.Sprintf("%s/Users/%s/Items?ParentId=%s&SortBy=IndexNumber,SortName&SortOrder=Ascending&Fields=ChildCount", jellyfinBaseURL, userID, parentID)
	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, "GET", reqURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("X-Emby-Token", token)
	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))

	resp, err := upstreamHTTPClient.Do(req)
	if err != nil {
		http.Error(w, "Jellyfin Items request failed: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "Jellyfin Items returned error: "+resp.Status, http.StatusServiceUnavailable)
		return
	}

	var jResp struct {
		Items []jellyfinChildItem `json:"Items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jResp); err != nil {
		http.Error(w, "Failed to decode items response", http.StatusInternalServerError)
		return
	}

	authorized := make(map[string]AuthorizedMediaItem, len(jResp.Items))
	if jellyfinAuthorizer == nil {
		for _, item := range jResp.Items {
			authorized[item.ID] = AuthorizedMediaItem{ID: item.ID, LibraryID: parent.LibraryID, MediaType: item.Type, IsFolder: item.IsFolder}
		}
	} else {
		ids := make([]string, 0, len(jResp.Items))
		for _, item := range jResp.Items {
			ids = append(ids, item.ID)
		}
		var err error
		authorized, err = jellyfinAuthorizer.AuthorizeItems(ctx, ids)
		if err != nil {
			http.Error(w, "Jellyfin authorization failed", http.StatusServiceUnavailable)
			return
		}
	}
	items := make([]MediaPlayableItem, 0, len(jResp.Items))
	for _, rawItem := range jResp.Items {
		authorizedItem, ok := authorized[rawItem.ID]
		if !ok || !jellyfinLibraryAllowed(authorizedItem.LibraryID) {
			continue
		}
		resolution, err := observeJellyfinCatalogItem(ctx, rawItem.ID, authorizedItem.LibraryID, rawItem.Type, rawItem.IsFolder)
		if err != nil {
			continue
		}
		mapped := mapJellyfinChildren([]jellyfinChildItem{rawItem})[0]
		mapped.ID = resolution.ID
		items = append(items, mapped)
	}

	respJSON, err := json.Marshal(items)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if redisClient != nil {
		_ = redisClient.Set(ctx, cacheKey, string(respJSON), 5*time.Minute).Err()
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(respJSON)
}

func handleMediaItemByID(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rawID := r.PathValue("id")
	if !validJellyfinID(rawID) {
		http.Error(w, "missing item id", 400)
		return
	}
	resolution, ok := resolveJellyfinItemHTTP(w, r, rawID)
	if !ok {
		return
	}
	id := resolution.UpstreamID

	cacheKey := fmt.Sprintf("telos:jellyfin:item:canonical-v1:%s", id)
	if redisClient != nil {
		val, err := redisClient.Get(ctx, cacheKey).Result()
		if err == nil && val != "" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(val))
			return
		}
	}

	userID, err := getJellyfinUserID(ctx)
	if err != nil {
		http.Error(w, "Jellyfin unreachable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	token := getJellyfinAdminToken()
	reqURL := fmt.Sprintf("%s/Users/%s/Items/%s", jellyfinBaseURL, userID, id)
	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, "GET", reqURL, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	req.Header.Set("X-Emby-Token", token)
	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))

	resp, err := upstreamHTTPClient.Do(req)
	if err != nil {
		http.Error(w, "Jellyfin request failed: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		status := http.StatusBadGateway
		if resp.StatusCode == http.StatusNotFound {
			status = http.StatusNotFound
		}
		http.Error(w, "Jellyfin returned error: "+resp.Status, status)
		return
	}

	var rawItem struct {
		ID             string `json:"Id"`
		Name           string `json:"Name"`
		Type           string `json:"Type"`
		ProductionYear int    `json:"ProductionYear"`
		RunTimeTicks   int64  `json:"RunTimeTicks"`
		Studios        []struct {
			Name string `json:"Name"`
		} `json:"Studios"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&rawItem); err != nil {
		http.Error(w, "failed to decode jellyfin item response", 500)
		return
	}

	durationSec := rawItem.RunTimeTicks / 10000000
	result := map[string]interface{}{
		"id":          resolution.ID,
		"title":       rawItem.Name,
		"year":        rawItem.ProductionYear,
		"director":    "",
		"durationSec": durationSec,
		"kind":        strings.ToLower(rawItem.Type),
		"coverUrl":    "/api/v1/media/items/" + resolution.ID + "/cover",
	}
	if len(rawItem.Studios) > 0 {
		result["director"] = rawItem.Studios[0].Name
	}

	respJSON, _ := json.Marshal(result)
	if redisClient != nil {
		_ = redisClient.Set(ctx, cacheKey, string(respJSON), 5*time.Minute).Err()
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(respJSON)
}

func handleMediaItemCover(w http.ResponseWriter, r *http.Request) {
	rawID := r.PathValue("id")
	if !validJellyfinID(rawID) {
		http.Error(w, "missing item id", 400)
		return
	}
	resolution, ok := resolveJellyfinItemHTTP(w, r, rawID)
	if !ok {
		return
	}
	token := getJellyfinAdminToken()
	targetURL := fmt.Sprintf("%s/Items/%s/Images/Primary", jellyfinBaseURL, resolution.UpstreamID)
	proxyRequest(w, r, targetURL, token, nil)
}

func handleStreamAudio(w http.ResponseWriter, r *http.Request) {
	rawID := r.PathValue("id")
	if !validJellyfinID(rawID) {
		http.Error(w, "Missing item ID", http.StatusBadRequest)
		return
	}
	resolution, ok := resolveJellyfinItemHTTP(w, r, rawID)
	if !ok {
		return
	}
	release, ok := acquireStreamSlot(w, r)
	if !ok {
		return
	}
	defer release()
	token := getJellyfinAdminToken()
	targetURL := fmt.Sprintf("%s/Audio/%s/stream?static=true", jellyfinBaseURL, resolution.UpstreamID)
	proxyRequest(w, r, targetURL, token, nil)
}

func handleStreamVideo(w http.ResponseWriter, r *http.Request) {
	rawID := r.PathValue("id")
	if !validJellyfinID(rawID) {
		http.Error(w, "Missing item ID", http.StatusBadRequest)
		return
	}
	resolution, ok := resolveJellyfinItemHTTP(w, r, rawID)
	if !ok {
		return
	}

	ctx := r.Context()
	userID, err := getJellyfinUserID(ctx)
	if err != nil {
		http.Error(w, "Failed to resolve Jellyfin user ID: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	token := getJellyfinAdminToken()
	playbackInfoURL := fmt.Sprintf("%s/Items/%s/PlaybackInfo?UserId=%s", jellyfinBaseURL, resolution.UpstreamID, userID)

	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, "POST", playbackInfoURL, strings.NewReader("{}"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Emby-Token", token)
	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))

	resp, err := upstreamHTTPClient.Do(req)
	if err != nil {
		http.Error(w, "PlaybackInfo request failed: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "Jellyfin PlaybackInfo error: "+resp.Status, http.StatusServiceUnavailable)
		return
	}

	var jResp struct {
		PlaySessionId string `json:"PlaySessionId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jResp); err != nil {
		http.Error(w, "Failed to decode playback info", http.StatusInternalServerError)
		return
	}

	if jResp.PlaySessionId == "" {
		jResp.PlaySessionId = fmt.Sprintf("telos-session-%d", time.Now().UnixNano())
	}

	redirectURL := fmt.Sprintf("/api/v1/stream/video/%s/main.m3u8?PlaySessionId=%s", resolution.ID, jResp.PlaySessionId)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func handleStreamVideoSubpath(w http.ResponseWriter, r *http.Request) {
	rawID := r.PathValue("id")
	subpath := r.PathValue("path")
	if !validJellyfinID(rawID) || !validUpstreamSubpath(subpath) {
		http.Error(w, "Missing item ID or subpath", http.StatusBadRequest)
		return
	}
	resolution, ok := resolveJellyfinItemHTTP(w, r, rawID)
	if !ok {
		return
	}
	// A manifest is rewritten server-side so every segment/key/variant URI
	// becomes an opaque, token-free Telos locator; only non-manifest binaries
	// reached directly here are proxied (segments now arrive via /api/v1/hls).
	if strings.HasSuffix(subpath, ".m3u8") {
		serveRewrittenManifest(w, r, resolution.UpstreamID, subpath, r.URL.RawQuery)
		return
	}
	release, ok := acquireStreamSlot(w, r)
	if !ok {
		return
	}
	defer release()
	token := getJellyfinAdminToken()
	targetURL := fmt.Sprintf("%s/Videos/%s/%s", jellyfinBaseURL, resolution.UpstreamID, subpath)
	proxyRequest(w, r, targetURL, token, jellyfinStreamQueryKeys)
}

// ═══════════════════════════════════════════════════════════════════════════
// Shared File Library & ClamAV Integration
// ═══════════════════════════════════════════════════════════════════════════

// scanFileWithClamAV streams r to the deadline-bounded scanner (backend/clamav.go).
func scanFileWithClamAV(r io.Reader) (bool, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return clamScanner.Scan(ctx, r)
}

// mediaRoot is the shared file library's on-disk root. It is a package var so
// tests can point it at a temp directory.
var mediaRoot = "/data/shared/media"

// resolveMediaPath joins a caller-supplied relative path to mediaRoot, cleans
// it, and confirms the result stays within mediaRoot. Returns the absolute
// on-disk path or an error for traversal/escape attempts. rel == "" yields
// mediaRoot itself.
func resolveMediaPath(rel string) (string, error) {
	if strings.ContainsRune(rel, 0) {
		return "", errors.New("invalid path")
	}
	clean := filepath.Clean(filepath.Join(mediaRoot, rel))
	if clean != mediaRoot && !strings.HasPrefix(clean, mediaRoot+string(os.PathSeparator)) {
		return "", errors.New("path escapes media root")
	}
	return clean, nil
}

// mediaUploadTarget resolves where a media-library upload should land given a
// caller-supplied relative directory, returning the absolute destination dir,
// a collision-resolved basename, and the base64url file ID (relative to root).
func mediaUploadTarget(relDir, filename string) (destDir, destKey, fileID string, err error) {
	destDir, err = resolveMediaPath(relDir)
	if err != nil {
		return "", "", "", err
	}
	destKey = getUniqueFilename(destDir, filename)
	rel, err := filepath.Rel(mediaRoot, filepath.Join(destDir, destKey))
	if err != nil {
		return "", "", "", err
	}
	fileID = base64.RawURLEncoding.EncodeToString([]byte(rel))
	return destDir, destKey, fileID, nil
}

func getUniqueFilename(dir, filename string) string {
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)

	destPath := filepath.Join(dir, filename)
	if _, err := os.Stat(destPath); os.IsNotExist(err) {
		return filename
	}

	for i := 1; ; i++ {
		newName := fmt.Sprintf("%s_%d%s", base, i, ext)
		destPath = filepath.Join(dir, newName)
		if _, err := os.Stat(destPath); os.IsNotExist(err) {
			return newName
		}
	}
}

// mimeForName maps a filename's extension to a MIME type for library listings.
func mimeForName(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp4":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".mp3":
		return "audio/mpeg"
	case ".m4a":
		return "audio/mp4"
	case ".wav":
		return "audio/wav"
	case ".ogg":
		return "audio/ogg"
	case ".pdf":
		return "application/pdf"
	case ".epub":
		return "application/epub+zip"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

func handleListFiles(w http.ResponseWriter, r *http.Request) {
	relPath := r.URL.Query().Get("path")
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	const limit = 20

	dir, err := resolveMediaPath(relPath)
	if err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "Folder not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to read directory", http.StatusInternalServerError)
		return
	}

	type folderEntry struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Path string `json:"path"`
	}
	type fileEntry struct {
		ID         string    `json:"id"`
		Filename   string    `json:"filename"`
		SHA256     string    `json:"sha256"`
		UploaderID string    `json:"uploader_id"`
		ScanStatus string    `json:"scan_status"`
		SizeBytes  int64     `json:"size_bytes"`
		MimeType   string    `json:"mime_type"`
		CreatedAt  time.Time `json:"created_at"`
	}

	folders := []folderEntry{}
	files := []fileEntry{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		rel := filepath.Join(relPath, name)
		id := base64.RawURLEncoding.EncodeToString([]byte(rel))
		if e.IsDir() {
			folders = append(folders, folderEntry{ID: id, Name: name, Path: rel})
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileEntry{
			ID:         id,
			Filename:   name,
			SHA256:     "",
			UploaderID: "",
			ScanStatus: "clean",
			SizeBytes:  info.Size(),
			MimeType:   mimeForName(name),
			CreatedAt:  info.ModTime(),
		})
	}

	sort.Slice(folders, func(i, j int) bool { return folders[i].Name < folders[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].CreatedAt.After(files[j].CreatedAt) })

	// Paginate over the combined (folders-first) sequence.
	total := len(folders) + len(files)
	offset := (page - 1) * limit
	end := offset + limit
	if offset > total {
		offset = total
	}
	if end > total {
		end = total
	}
	pageFolders := []folderEntry{}
	pageFiles := []fileEntry{}
	for i := offset; i < end; i++ {
		if i < len(folders) {
			pageFolders = append(pageFolders, folders[i])
		} else {
			pageFiles = append(pageFiles, files[i-len(folders)])
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"path":    relPath,
		"folders": pageFolders,
		"files":   pageFiles,
		"page":    page,
		"hasNext": end < total,
	})
}

func handleUploadFile(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	processUpload(w, r, user.ID, false)
}

var defaultUploadExts = map[string]bool{
	".pdf": true, ".epub": true, ".jpg": true, ".jpeg": true,
	".png": true, ".webp": true, ".mp3": true, ".m4a": true,
	".ogg": true, ".wav": true, ".mp4": true, ".webm": true,
	".m4b": true, ".opus": true,
}

var bookdropExts = map[string]bool{
	".pdf": true, ".epub": true,
	".m4b": true, ".m4a": true, ".mp3": true, ".opus": true,
}

func handleUploadBook(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	processUploadWithLimits(w, r, user.ID, true, 104857600, bookdropExts, true)
}

func processUpload(w http.ResponseWriter, r *http.Request, uploaderID string, isBook bool) {
	exts := defaultUploadExts
	if isBook {
		exts = bookdropExts
	}
	processUploadWithLimits(w, r, uploaderID, isBook, 104857600, exts, true)
}

// processUploadWithLimits stages, scans, and stores a multipart upload. On
// failure it writes the error response and returns ok=false. On success it
// returns the new files row ID; when writeResponse is true it also writes the
// original 201 upload JSON.
func processUploadWithLimits(w http.ResponseWriter, r *http.Request, uploaderID string, isBook bool, maxBytes int64, extWhitelist map[string]bool, writeResponse bool) (string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	err := r.ParseMultipartForm(maxBytes)
	if err != nil {
		http.Error(w, fmt.Sprintf("File size exceeds %d MiB limit", maxBytes/(1024*1024)), http.StatusBadRequest)
		return "", false
	}
	relDir := r.FormValue("path")

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Missing file in multipart form (key 'file')", http.StatusBadRequest)
		return "", false
	}
	defer file.Close()

	if len(header.Filename) > 255 {
		http.Error(w, "Filename too long", http.StatusBadRequest)
		return "", false
	}

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !extWhitelist[ext] {
		http.Error(w, "File extension not allowed", http.StatusBadRequest)
		return "", false
	}

	if isBook && !bookdropExts[ext] {
		http.Error(w, "Only PDF, EPUB, M4B, M4A, MP3, and OPUS are allowed for book uploads", http.StatusBadRequest)
		return "", false
	}

	// Staging path
	tempID := fmt.Sprintf("temp-%d-%s", time.Now().UnixNano(), header.Filename)
	tempPath := filepath.Join("/data/shared/staging", tempID)
	outFS, err := os.Create(tempPath)
	if err != nil {
		http.Error(w, "Failed to stage upload file", http.StatusInternalServerError)
		return "", false
	}
	defer outFS.Close()

	// Compute SHA-256 hash and detect MIME type on the fly
	hasher := sha256.New()
	teaser := make([]byte, 512)
	n, _ := file.Read(teaser)
	detectedMime := http.DetectContentType(teaser[:n])

	// Validate magic bytes/extension matches
	allowedMimes := map[string]bool{
		"application/pdf": true, "application/epub+zip": true, "image/jpeg": true,
		"image/png": true, "image/webp": true, "audio/mpeg": true, "audio/mp4": true,
		"audio/ogg": true, "audio/wav": true, "video/mp4": true, "video/webm": true,
		"application/octet-stream": true, // fallback for some epubs
	}
	if !allowedMimes[detectedMime] && !strings.HasPrefix(detectedMime, "audio/") && !strings.HasPrefix(detectedMime, "video/") {
		os.Remove(tempPath)
		http.Error(w, "MIME type verification failed", http.StatusBadRequest)
		return "", false
	}

	// Write teaser to hash/file
	_, _ = hasher.Write(teaser[:n])
	_, _ = outFS.Write(teaser[:n])

	// Stream rest
	written, err := io.Copy(io.MultiWriter(outFS, hasher), file)
	if err != nil {
		os.Remove(tempPath)
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return "", false
	}
	totalSize := int64(n) + written

	fileHash := hex.EncodeToString(hasher.Sum(nil))

	// Close temporary file so it can be read for scanning
	outFS.Close()

	// Scan with ClamAV
	scanFile, err := os.Open(tempPath)
	if err != nil {
		os.Remove(tempPath)
		http.Error(w, "Failed to scan file: local staging open error", http.StatusInternalServerError)
		return "", false
	}
	clean, scanStatus, scanErr := scanFileWithClamAV(scanFile)
	scanFile.Close()

	if scanErr != nil {
		os.Remove(tempPath)
		http.Error(w, "Security Scan failed: "+scanErr.Error(), http.StatusServiceUnavailable)
		return "", false
	}

	if !clean {
		os.Remove(tempPath)
		// Record infected state in database for audit
		dbPool.Exec(r.Context(), `
			INSERT INTO files (filename, sha256, uploader_id, scan_status, storage_key, size_bytes, mime_type)
			VALUES ($1, $2, $3, $4, '', $5, $6)
		`, header.Filename, fileHash, uploaderID, scanStatus, totalSize, detectedMime)

		http.Error(w, "Upload Rejected: Security Scan detected malicious code.", http.StatusUnprocessableEntity)
		return "", false
	}

	// File is clean, promote atomically
	var destDir string
	var destKey string
	var mediaFileID string
	if isBook {
		destDir = "/data/shared/bookdrop"
		destKey = fileHash + ext
	} else if !writeResponse {
		destDir = "/data/shared/staging/library"
		destKey = fileHash + ext
	} else {
		var terr error
		destDir, destKey, mediaFileID, terr = mediaUploadTarget(relDir, header.Filename)
		if terr != nil {
			os.Remove(tempPath)
			http.Error(w, "Invalid upload path", http.StatusBadRequest)
			return "", false
		}
	}

	destPath := filepath.Join(destDir, destKey)
	if err := os.Rename(tempPath, destPath); err != nil {
		// Try copy if rename fails (across devices)
		input, err := os.Open(tempPath)
		if err != nil {
			os.Remove(tempPath)
			http.Error(w, "Atomic move failed", http.StatusInternalServerError)
			return "", false
		}
		defer input.Close()
		output, err := os.Create(destPath)
		if err != nil {
			os.Remove(tempPath)
			http.Error(w, "Atomic move failed", http.StatusInternalServerError)
			return "", false
		}
		defer output.Close()
		_, _ = io.Copy(output, input)
		os.Remove(tempPath)
	}

	// Insert into DB (skip for standard media library files)
	var fileID string
	if !isBook && writeResponse {
		fileID = mediaFileID
	} else {
		err = dbPool.QueryRow(r.Context(), `
			INSERT INTO files (filename, sha256, uploader_id, scan_status, storage_key, size_bytes, mime_type)
			VALUES ($1, $2, $3, 'clean', $4, $5, $6)
			RETURNING id
		`, header.Filename, fileHash, uploaderID, destKey, totalSize, detectedMime).Scan(&fileID)
		if err != nil {
			http.Error(w, "Database record failed", http.StatusInternalServerError)
			return "", false
		}
	}

	if writeResponse {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{
			"id":          fileID,
			"filename":    header.Filename,
			"sha256":      fileHash,
			"scan_status": "clean",
		})
	}
	return fileID, true
}

// The Files surface has two ID namespaces. handleListFiles walks the shared
// filesystem and IDs entries as base64url(relative path); uploads recorded in
// the files table are keyed by UUID. Anything that accepts a file ID has to
// accept both, and in this order — a UUID string decodes as valid base64url,
// so the disk probe has to fail before the DB lookup is tried.
//
// sharedFileMetaFromPath describes a file in the filesystem namespace. The ID
// is attacker-controlled and is decoded into a path, so containment lives here:
// resolveMediaPath rejects anything escaping the root, the media root itself is
// not a file, and directories are not shareable. ok is false for all of those,
// leaving the caller to try the UUID namespace.
func sharedFileMetaFromPath(id string) (filename string, size int64, mime string, ok bool) {
	relPath, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return "", 0, "", false
	}
	cleanPath, err := resolveMediaPath(string(relPath))
	if err != nil || cleanPath == mediaRoot {
		return "", 0, "", false
	}
	info, err := os.Stat(cleanPath)
	if err != nil || info.IsDir() {
		return "", 0, "", false
	}
	name := filepath.Base(cleanPath)
	return name, info.Size(), mimeForName(name), true
}

// resolveSharedFileMeta returns the display metadata for either namespace.
// Callers that need bytes (handleDownloadFile) resolve locations themselves;
// this is only for describing a file.
func resolveSharedFileMeta(ctx context.Context, id string) (filename string, size int64, mime string, err error) {
	if name, sz, mt, ok := sharedFileMetaFromPath(id); ok {
		return name, sz, mt, nil
	}

	// Infected uploads are excluded, matching handleDownloadFile — a share card
	// whose Download action cannot resolve is worse than a refused share.
	err = dbPool.QueryRow(ctx, `
		SELECT filename, size_bytes, mime_type FROM files
		WHERE id = $1 AND scan_status = 'clean'
	`, id).Scan(&filename, &size, &mime)
	if err != nil {
		return "", 0, "", errors.New("file not found")
	}
	return filename, size, mime, nil
}

func handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Missing file ID", http.StatusBadRequest)
		return
	}

	// Try base64 decoding first (filesystem paths)
	if relPathBytes, err := base64.RawURLEncoding.DecodeString(id); err == nil {
		if cleanPath, err := resolveMediaPath(string(relPathBytes)); err == nil && cleanPath != mediaRoot {
			if info, err := os.Stat(cleanPath); err == nil && !info.IsDir() {
				w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(cleanPath)))
				w.Header().Set("X-Content-Type-Options", "nosniff")
				http.ServeFile(w, r, cleanPath)
				return
			}
		}
	}

	// Fallback to database lookup
	var storageKey string
	var filename string
	var mimeType string
	err := dbPool.QueryRow(r.Context(), `
		SELECT storage_key, filename, mime_type FROM files WHERE id = $1 AND scan_status = 'clean'
	`, id).Scan(&storageKey, &filename, &mimeType)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	filePath := filepath.Join("/data/shared/staging/library", storageKey)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.Error(w, "File asset missing from disk", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, filePath)
}

func handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Missing file ID", http.StatusBadRequest)
		return
	}

	// Try base64 decoding first
	if relPathBytes, err := base64.RawURLEncoding.DecodeString(id); err == nil {
		if cleanPath, err := resolveMediaPath(string(relPathBytes)); err == nil && cleanPath != mediaRoot {
			if info, err := os.Stat(cleanPath); err == nil && !info.IsDir() {
				if err := os.Remove(cleanPath); err != nil {
					http.Error(w, "Failed to delete file from disk", http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{"status": "success"})
				return
			}
		}
	}

	// Fallback to database deletion
	var storageKey string
	err := dbPool.QueryRow(r.Context(), `
		SELECT storage_key FROM files WHERE id = $1
	`, id).Scan(&storageKey)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	filePath := filepath.Join("/data/shared/staging/library", storageKey)
	_ = os.Remove(filePath) // delete if exists

	_, err = dbPool.Exec(r.Context(), "DELETE FROM files WHERE id = $1", id)
	if err != nil {
		http.Error(w, "Database delete failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > 255 || name == "." || name == ".." ||
		strings.ContainsAny(name, "/\x00") {
		http.Error(w, "Invalid folder name", http.StatusBadRequest)
		return
	}

	parent, err := resolveMediaPath(body.Path)
	if err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		http.Error(w, "Parent folder not found", http.StatusNotFound)
		return
	}

	target := filepath.Join(parent, name)
	if err := os.Mkdir(target, 0755); err != nil {
		if os.IsExist(err) {
			http.Error(w, "A folder with that name already exists", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to create folder", http.StatusInternalServerError)
		return
	}

	rel, err := filepath.Rel(mediaRoot, target)
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	id := base64.RawURLEncoding.EncodeToString([]byte(rel))
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id, "name": name, "path": rel})
}

func handleDeleteFolder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	relBytes, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		http.Error(w, "Invalid folder ID", http.StatusBadRequest)
		return
	}
	target, err := resolveMediaPath(string(relBytes))
	if err != nil || target == mediaRoot {
		http.Error(w, "Invalid folder", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		http.Error(w, "Folder not found", http.StatusNotFound)
		return
	}

	// Refuse non-empty folders (rmdir semantics) — check before removing.
	children, err := os.ReadDir(target)
	if err != nil {
		http.Error(w, "Failed to read folder", http.StatusInternalServerError)
		return
	}
	if len(children) > 0 {
		http.Error(w, "Folder isn't empty", http.StatusConflict)
		return
	}
	if err := os.Remove(target); err != nil {
		http.Error(w, "Failed to delete folder", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

type SearchFileItem struct {
	ID        string    `json:"id"`
	Filename  string    `json:"filename"`
	SizeBytes int64     `json:"sizeBytes"`
	MimeType  string    `json:"mimeType"`
	CreatedAt time.Time `json:"createdAt"`
}

type SearchResults struct {
	Channels []ChannelResponse    `json:"channels"`
	Users    []SearchUserResponse `json:"users"`
	Books    []LibraryBook        `json:"books"`
	Media    []MediaPlayableItem  `json:"media"`
	Files    []SearchFileItem     `json:"files"`
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := ctx.Value(userContextKey).(*UserContext)
	q := r.URL.Query().Get("q")
	pattern, ok := normalizedSearchTerm(q)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(SearchResults{
			Channels: []ChannelResponse{},
			Users:    []SearchUserResponse{},
			Books:    []LibraryBook{},
			Media:    []MediaPlayableItem{},
			Files:    []SearchFileItem{},
		})
		return
	}

	var results SearchResults
	results.Channels = []ChannelResponse{}
	results.Users = []SearchUserResponse{}
	results.Books = []LibraryBook{}
	results.Media = []MediaPlayableItem{}
	results.Files = []SearchFileItem{}

	var wg sync.WaitGroup
	var mu sync.Mutex

	// 1. Channels & Users (gated by view_channel)
	canViewChannel, err := hasPermission(ctx, user, "view_channel", nil)
	if err == nil && canViewChannel {
		// Search Users
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := dbPool.Query(ctx, `
				SELECT id::text, username, COALESCE(display_name, ''), avatar_file_id IS NOT NULL
				FROM users
				WHERE active = TRUE
				  AND (lower(username) LIKE $1 OR lower(coalesce(display_name,'')) LIKE $1)
				LIMIT 15
			`, pattern)
			if err != nil {
				log.Printf("WARN: search users query failed: %v", err)
				return
			}
			defer rows.Close()

			var users []SearchUserResponse
			for rows.Next() {
				var u SearchUserResponse
				var hasAvatar bool
				if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &hasAvatar); err == nil {
					if hasAvatar {
						u.AvatarUrl = "/api/v1/users/" + u.ID + "/avatar"
					}
					users = append(users, u)
				}
			}
			mu.Lock()
			if len(users) > 0 {
				results.Users = users
			}
			mu.Unlock()
		}()

		// Search Channels
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := dbPool.Query(ctx, `
				SELECT id::text, name FROM channels ORDER BY name
			`)
			if err != nil {
				log.Printf("WARN: search channels query failed: %v", err)
				return
			}
			defer rows.Close()

			var chans []ChannelResponse
			for rows.Next() {
				var c ChannelResponse
				if err := rows.Scan(&c.ID, &c.Name); err == nil {
					allowed, err := hasPermission(ctx, user, "view_channel", &c.ID)
					if err == nil && allowed {
						if strings.Contains(strings.ToLower(c.Name), strings.ToLower(q)) {
							chans = append(chans, c)
						}
					}
				}
			}
			mu.Lock()
			if len(chans) > 0 {
				results.Channels = chans
			}
			mu.Unlock()
		}()
	}

	// 2. Library Books (gated by view_library)
	canViewLibrary, err := hasPermission(ctx, user, "view_library", nil)
	if err == nil && canViewLibrary {
		wg.Add(1)
		go func() {
			defer wg.Done()
			books, err := getLibraryBooks(ctx)
			if err != nil {
				log.Printf("WARN: search library books failed: %v", err)
				return
			}
			var matched []LibraryBook
			lowerQ := strings.ToLower(q)
			for _, b := range books {
				match := strings.Contains(strings.ToLower(b.Title), lowerQ)
				if !match {
					for _, auth := range b.Authors {
						if strings.Contains(strings.ToLower(auth), lowerQ) {
							match = true
							break
						}
					}
				}
				if !match {
					for _, cat := range b.Categories {
						if strings.Contains(strings.ToLower(cat), lowerQ) {
							match = true
							break
						}
					}
				}
				if match {
					matched = append(matched, b)
					if len(matched) >= 15 {
						break
					}
				}
			}
			mu.Lock()
			if len(matched) > 0 {
				results.Books = matched
			}
			mu.Unlock()
		}()
	}

	// 3. Stream Media (gated by view_media)
	canViewMedia, err := hasPermission(ctx, user, "view_media", nil)
	if err == nil && canViewMedia {
		wg.Add(1)
		go func() {
			defer wg.Done()
			userID, err := getJellyfinUserID(ctx)
			if err != nil {
				log.Printf("WARN: search jellyfin failed to get user ID: %v", err)
				return
			}
			token := getJellyfinAdminToken()
			reqURL := fmt.Sprintf("%s/Users/%s/Items?searchTerm=%s&Recursive=true&Fields=ChildCount,RunTimeTicks&Limit=15", jellyfinBaseURL, userID, url.QueryEscape(q))
			requestCtx, cancel := upstreamRequestContext(ctx)
			defer cancel()
			req, err := http.NewRequestWithContext(requestCtx, "GET", reqURL, nil)
			if err != nil {
				return
			}
			req.Header.Set("X-Emby-Token", token)
			req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))

			resp, err := upstreamHTTPClient.Do(req)
			if err != nil {
				log.Printf("WARN: search jellyfin items failed: %v", err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				var jResp struct {
					Items []jellyfinChildItem `json:"Items"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&jResp); err == nil {
					items := mapJellyfinChildren(jResp.Items)
					mu.Lock()
					if len(items) > 0 {
						results.Media = items
					}
					mu.Unlock()
				}
			}
		}()
	}

	// 4. Shared Files (gated by view_files)
	canViewFiles, err := hasPermission(ctx, user, "view_files", nil)
	if err == nil && canViewFiles {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := dbPool.Query(ctx, `
				SELECT id::text, filename, size_bytes, mime_type, created_at
				FROM files
				WHERE lower(filename) LIKE $1
				  AND purpose = 'shared' AND state = 'available'
				  AND scan_status = 'clean' AND visibility = 'community'
				LIMIT 15
			`, pattern)
			if err != nil {
				log.Printf("WARN: search files failed: %v", err)
				return
			}
			defer rows.Close()

			var files []SearchFileItem
			seen := map[string]bool{}
			for rows.Next() {
				var f SearchFileItem
				if err := rows.Scan(&f.ID, &f.Filename, &f.SizeBytes, &f.MimeType, &f.CreatedAt); err == nil {
					files = append(files, f)
					seen[f.Filename] = true
				}
			}

			// The table only records uploads made through the app. Everything
			// else on the shared volume is listed by the Files browser and is
			// equally shareable, so search has to reach it too.
			if len(files) < searchScopeLimit {
				for _, f := range searchSharedFilesystem(q, searchScopeLimit-len(files)) {
					if !seen[f.Filename] {
						files = append(files, f)
					}
				}
			}

			mu.Lock()
			if len(files) > 0 {
				results.Files = files
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}
