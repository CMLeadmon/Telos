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
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/livekit/protocol/auth"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/argon2"
)

// ═══════════════════════════════════════════════════════════════════════════
// Global State
// ═══════════════════════════════════════════════════════════════════════════

var (
	// dbPool is the PostgreSQL connection pool, initialised at startup.
	dbPool *pgxpool.Pool

	// redisClient is the Redis connection used for pub/sub and caching.
	redisClient *redis.Client

	// upgrader negotiates WebSocket upgrades; origin is validated by
	// isAllowedWSOrigin against the TELOS_DOMAIN env var.
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return isAllowedWSOrigin(r.Header.Get("Origin"), r.Host, os.Getenv("TELOS_DOMAIN"))
		},
	}
)

//go:embed db/migrations/*.sql
var migrationsFS embed.FS

//go:embed all:out
var frontendFS embed.FS

// ═══════════════════════════════════════════════════════════════════════════
// Types
// ═══════════════════════════════════════════════════════════════════════════

type HealthResponse struct {
	Status   string            `json:"status"`
	Services map[string]string `json:"services"`
}

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
	Kind     string          `json:"kind"`     // library_book|stream_film|file
	Ref      string          `json:"ref"`
	Snapshot json.RawMessage `json:"snapshot"` // {title,subtitle,kicker,cover,duration}
}

type WSEvent struct {
	Type      string          `json:"type"` // history|message|message.update|message.delete|reaction|pin|presence
	Messages  []WSMessage     `json:"messages,omitempty"`  // history
	Message   *WSMessage      `json:"message,omitempty"`   // message | message.update
	MessageID string          `json:"messageId,omitempty"` // message.delete | reaction | pin
	ChannelID string          `json:"channelId,omitempty"`
	Reaction  *WSReaction     `json:"reaction,omitempty"`  // reaction
	Pin       *WSPin          `json:"pin,omitempty"`       // pin
	Presence  *WSPresence     `json:"presence,omitempty"`  // presence
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
	DisplayName string
	HasAvatar   bool
}

type contextKey string

const userContextKey contextKey = "user"

// ═══════════════════════════════════════════════════════════════════════════
// Entrypoint
// ═══════════════════════════════════════════════════════════════════════════

func main() {
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Initialize Postgres Pool
	var err error
	dbPool, err = pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatalf("Critical: Failed to connect to database: %v", err)
	}
	defer dbPool.Close()

	// Initialize tables via migrations
	if err := initDatabase(ctx); err != nil {
		log.Fatalf("Critical: Database migration failed: %v", err)
	}

	// Initialize Redis Client
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("Critical: Failed to parse Redis URL: %v", err)
	}
	redisClient = redis.NewClient(opt)
	defer redisClient.Close()

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

	// Get sub-filesystem for the frontend static files
	subFS, err := fs.Sub(frontendFS, "out")
	if err != nil {
		log.Fatalf("Failed to locate frontend out/ directory: %v", err)
	}
	fileServer := http.FileServer(http.FS(subFS))

	// Create ServeMux
	mux := http.NewServeMux()

	// Public Routes
	mux.HandleFunc("GET /api/v1/health", handleHealth)
	mux.HandleFunc("POST /api/v1/auth/bootstrap", handleBootstrap)
	mux.HandleFunc("POST /api/v1/auth/login", handleLogin)
	mux.HandleFunc("POST /api/v1/auth/invites/accept", handleAcceptInvite)

	// Authenticated Routes (Requires Session check)
	mux.Handle("POST /api/v1/auth/logout", withAuth(http.HandlerFunc(handleLogout), ""))
	mux.Handle("GET /api/v1/auth/me", withAuth(http.HandlerFunc(handleMe), ""))
	mux.Handle("POST /api/v1/auth/invites", withAuth(http.HandlerFunc(handleCreateInvite), "manage_community"))

	// Settings — profile & preferences
	mux.Handle("PATCH /api/v1/users/me", withAuth(http.HandlerFunc(handleUpdateProfile), ""))
	mux.Handle("GET /api/v1/users/me/preferences", withAuth(http.HandlerFunc(handleGetPreferences), ""))
	mux.Handle("PUT /api/v1/users/me/preferences", withAuth(http.HandlerFunc(handlePutPreferences), ""))

	// Settings — password & sessions
	mux.Handle("POST /api/v1/users/me/password", withAuth(http.HandlerFunc(handleChangePassword), ""))
	mux.Handle("GET /api/v1/users/me/sessions", withAuth(http.HandlerFunc(handleListMySessions), ""))
	mux.Handle("DELETE /api/v1/users/me/sessions/{id}", withAuth(http.HandlerFunc(handleRevokeSession), ""))
	mux.Handle("POST /api/v1/users/me/sessions/revoke-others", withAuth(http.HandlerFunc(handleRevokeOtherSessions), ""))

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
	mux.Handle("GET /api/v1/admin/roles", withAuth(http.HandlerFunc(handleAdminListRoles), "manage_roles"))
	mux.Handle("GET /api/v1/admin/permissions", withAuth(http.HandlerFunc(handleAdminListPermissions), "manage_roles"))
	mux.Handle("POST /api/v1/admin/roles", withAuth(http.HandlerFunc(handleAdminCreateRole), "manage_roles"))
	mux.Handle("PUT /api/v1/admin/roles/{id}/permissions", withAuth(http.HandlerFunc(handleAdminSetRolePermissions), "manage_roles"))
	mux.Handle("DELETE /api/v1/admin/roles/{id}", withAuth(http.HandlerFunc(handleAdminDeleteRole), "manage_roles"))

	// Chat WebSocket
	mux.Handle("GET /api/v1/chat/ws", withAuth(http.HandlerFunc(handleWebSocket), "view_channel"))
	mux.Handle("POST /api/v1/channels/{id}/messages", withAuth(http.HandlerFunc(handleSendMessage), "send_messages"))
	mux.Handle("PATCH /api/v1/channels/{id}/messages/{mid}", withAuth(http.HandlerFunc(handleEditMessage), "send_messages"))
	mux.Handle("DELETE /api/v1/channels/{id}/messages/{mid}", withAuth(http.HandlerFunc(handleDeleteMessage), "view_channel"))
	mux.Handle("POST /api/v1/channels/{id}/messages/{mid}/reactions", withAuth(http.HandlerFunc(handleAddReaction), "send_messages"))
	mux.Handle("DELETE /api/v1/channels/{id}/messages/{mid}/reactions/{emoji}", withAuth(http.HandlerFunc(handleRemoveReaction), "send_messages"))

	// Channel list
	mux.Handle("GET /api/v1/channels", withAuth(http.HandlerFunc(handleListChannels), "view_channel"))

	// Jellyfin Proxy routes (Require view_media)
	mux.Handle("GET /api/v1/media", withAuth(http.HandlerFunc(handleMedia), "view_media"))
	mux.Handle("GET /api/v1/media/items", withAuth(http.HandlerFunc(handleMediaItems), "view_media"))
	mux.Handle("GET /api/v1/stream/audio/{id}", withAuth(http.HandlerFunc(handleStreamAudio), "view_media"))
	mux.Handle("GET /api/v1/stream/video/{id}", withAuth(http.HandlerFunc(handleStreamVideo), "view_media"))
	mux.Handle("GET /api/v1/stream/video/{id}/{path...}", withAuth(http.HandlerFunc(handleStreamVideoSubpath), "view_media"))
	mux.Handle("/jellyfin/", withAuth(http.HandlerFunc(handleJellyfinDirectProxy), "view_media"))

	// Grimmory Proxy route (Requires view_library)
	mux.Handle("/grimmory/", withAuth(http.HandlerFunc(handleGrimmoryProxy), "view_library"))

	// Library module routes (Grimmory-backed catalog)
	mux.Handle("GET /api/v1/library/books", withAuth(http.HandlerFunc(handleLibraryBooks), "view_library"))
	mux.Handle("GET /api/v1/library/facets", withAuth(http.HandlerFunc(handleLibraryFacets), "view_library"))
	mux.Handle("GET /api/v1/library/books/{id}/cover", withAuth(http.HandlerFunc(handleLibraryBookCover), "view_library"))
	mux.Handle("GET /api/v1/library/books/{id}/content", withAuth(http.HandlerFunc(handleLibraryBookContent), "view_library"))
	mux.Handle("GET /api/v1/library/books/{id}/progress", withAuth(http.HandlerFunc(handleGetBookProgress), "view_library"))
	mux.Handle("PUT /api/v1/library/books/{id}/progress", withAuth(http.HandlerFunc(handlePutBookProgress), "view_library"))

	// Voice Token (Requires join_voice)
	mux.Handle("POST /api/v1/voice/channels/{id}/token", withAuth(http.HandlerFunc(handleVoiceToken), "join_voice"))

	// File Library (Require view_files, upload_files, upload_books, manage_files)
	mux.Handle("GET /api/v1/files", withAuth(http.HandlerFunc(handleListFiles), "view_files"))
	mux.Handle("POST /api/v1/files", withAuth(http.HandlerFunc(handleUploadFile), "upload_files"))
	mux.Handle("POST /api/v1/files/books", withAuth(http.HandlerFunc(handleUploadBook), "upload_books"))
	mux.Handle("GET /api/v1/files/{id}/download", withAuth(http.HandlerFunc(handleDownloadFile), "view_files"))
	mux.Handle("DELETE /api/v1/files/{id}", withAuth(http.HandlerFunc(handleDeleteFile), "manage_files"))
	mux.Handle("POST /api/v1/folders", withAuth(http.HandlerFunc(handleCreateFolder), "upload_files"))
	mux.Handle("DELETE /api/v1/folders/{id}", withAuth(http.HandlerFunc(handleDeleteFolder), "manage_files"))

	// Frontend static assets handler
	mux.Handle("/", fileServer)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      csrfMiddleware(corsMiddleware(mux)),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	log.Printf("Server listening on port %s", port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed: %v", err)
	}
}

func validateSecrets() {
	vars := []string{
		"DATABASE_URL", "REDIS_URL", "TELOS_DOMAIN", "TELOS_BOOTSTRAP_TOKEN",
		"LIVEKIT_API_KEY", "LIVEKIT_API_SECRET", "JELLYFIN_ADMIN_TOKEN", "GRIMMORY_ADMIN_USER", "GRIMMORY_ADMIN_PASSWORD",
	}
	placeholders := []string{"your-secret-here", "change-me", "temp-token", "placeholder"}
	for _, v := range vars {
		val := os.Getenv(v)
		if val == "" {
			log.Fatalf("Critical Configuration Error: Environment variable %s is not set.", v)
		}
		for _, ph := range placeholders {
			if strings.Contains(strings.ToLower(val), ph) {
				log.Fatalf("Critical Configuration Error: Environment variable %s contains insecure placeholder value %q.", v, val)
			}
		}
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Database Initialization & Migrations
// ═══════════════════════════════════════════════════════════════════════════

func initDatabase(ctx context.Context) error {
	log.Println("Checking database schema migrations...")

	_, err := dbPool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INT PRIMARY KEY,
			applied_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);
	`)
	if err != nil {
		return fmt.Errorf("failed to ensure schema_migrations table: %v", err)
	}

	entries, err := fs.ReadDir(migrationsFS, "db/migrations")
	if err != nil {
		return fmt.Errorf("failed to read migrations: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		parts := strings.SplitN(entry.Name(), "_", 2)
		if len(parts) < 2 {
			continue
		}
		version, err := strconv.Atoi(parts[0])
		if err != nil {
			return fmt.Errorf("invalid migration name %q: %v", entry.Name(), err)
		}

		var exists bool
		err = dbPool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)", version).Scan(&exists)
		if err != nil {
			return fmt.Errorf("failed to check migration state: %v", err)
		}

		if exists {
			continue
		}

		log.Printf("Applying database migration version %d (%s)...", version, entry.Name())
		sqlBytes, err := fs.ReadFile(migrationsFS, "db/migrations/"+entry.Name())
		if err != nil {
			return fmt.Errorf("failed to read migration file: %v", err)
		}

		tx, err := dbPool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("failed to start migration transaction: %v", err)
		}

		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("migration run failed: %v", err)
		}

		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("failed to insert migration version: %v", err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("failed to commit migration transaction: %v", err)
		}
		log.Printf("Migration version %d applied successfully", version)
	}

	return nil
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

func generateToken() (string, string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	h := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(h[:])
}

// ═══════════════════════════════════════════════════════════════════════════
// Rate Limiting
// ═══════════════════════════════════════════════════════════════════════════

func isThrottled(ctx context.Context, username, clientIP string) (bool, error) {
	if redisClient == nil {
		return false, nil
	}
	userKey := fmt.Sprintf("telos:ratelimit:login:user:%s", strings.ToLower(username))
	ipKey := fmt.Sprintf("telos:ratelimit:login:ip:%s", clientIP)

	uVal, err := redisClient.Get(ctx, userKey).Int()
	if err == nil && uVal >= 5 {
		return true, nil
	}
	iVal, err := redisClient.Get(ctx, ipKey).Int()
	if err == nil && iVal >= 10 {
		return true, nil
	}
	return false, nil
}

func recordFailedLogin(ctx context.Context, username, clientIP string) {
	if redisClient == nil {
		return
	}
	userKey := fmt.Sprintf("telos:ratelimit:login:user:%s", strings.ToLower(username))
	ipKey := fmt.Sprintf("telos:ratelimit:login:ip:%s", clientIP)

	pipe := redisClient.Pipeline()
	pipe.Incr(ctx, userKey)
	pipe.Expire(ctx, userKey, 15*time.Minute)
	pipe.Incr(ctx, ipKey)
	pipe.Expire(ctx, ipKey, 15*time.Minute)
	_, _ = pipe.Exec(ctx)
}

func resetFailedLogins(ctx context.Context, username, clientIP string) {
	if redisClient == nil {
		return
	}
	userKey := fmt.Sprintf("telos:ratelimit:login:user:%s", strings.ToLower(username))
	ipKey := fmt.Sprintf("telos:ratelimit:login:ip:%s", clientIP)
	redisClient.Del(ctx, userKey, ipKey)
}

// ═══════════════════════════════════════════════════════════════════════════
// Middlewares & Origin Policy
// ═══════════════════════════════════════════════════════════════════════════

func isAllowedWSOrigin(origin, requestHost, configuredDomain string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Host == requestHost {
		return true
	}
	hostname := u.Hostname()
	if configuredDomain != "" && hostname == configuredDomain {
		return true
	}
	return hostname == "localhost" || hostname == "127.0.0.1"
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS, HEAD")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isAllowedCSRFOrigin reports whether a request's Origin/Referer URL is
// trusted. Same-origin requests (URL host == request Host) are always
// allowed, so the check works regardless of how the node is reached
// (LAN IP, Tailscale MagicDNS, reverse proxy) without enumerating hosts.
// In development mode a hostname-only match is also accepted, covering the
// dev split where the frontend on :3000 calls the gateway on :8080.
func isAllowedCSRFOrigin(rawURL, requestHost, configuredDomain, envMode string) bool {
	if rawURL == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, requestHost) {
		return true
	}
	hostname := u.Hostname()
	if configuredDomain != "" && strings.EqualFold(hostname, configuredDomain) {
		return true
	}
	if envMode == "development" {
		if strings.EqualFold(hostname, "localhost") || hostname == "127.0.0.1" {
			return true
		}
		if reqHostname, _, err := net.SplitHostPort(requestHost); err == nil && strings.EqualFold(hostname, reqHostname) {
			return true
		}
	}
	return false
}

func csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" {
			next.ServeHTTP(w, r)
			return
		}

		origin := r.Header.Get("Origin")
		referer := r.Header.Get("Referer")
		configuredDomain := os.Getenv("TELOS_DOMAIN")
		envMode := os.Getenv("TELOS_ENV")

		isValid := isAllowedCSRFOrigin(origin, r.Host, configuredDomain, envMode) ||
			isAllowedCSRFOrigin(referer, r.Host, configuredDomain, envMode)

		if !isValid {
			http.Error(w, "Forbidden: CSRF Validation Failed", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func getAuthenticatedUser(r *http.Request) (*UserContext, error) {
	if dbPool == nil {
		return nil, errors.New("db uninitialized")
	}

	var token string
	// Check cookie
	cookie, err := r.Cookie("telos_session")
	if err == nil {
		token = cookie.Value
	} else {
		// Fallback for WS connection upgrades that might supply ticket in query string
		token = r.URL.Query().Get("token")
	}

	if token == "" {
		return nil, errors.New("missing session token")
	}

	h := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(h[:])

	var userID string
	var username string
	var active bool
	var expiresAt time.Time
	var displayName *string
	var avatarFileID *string

	err = dbPool.QueryRow(r.Context(), `
		SELECT s.user_id, u.username, u.active, s.expires_at, u.display_name, u.avatar_file_id
		FROM sessions s
		JOIN users u ON s.user_id = u.id
		WHERE s.token_hash = $1 AND s.revoked_at IS NULL
	`, tokenHash).Scan(&userID, &username, &active, &expiresAt, &displayName, &avatarFileID)
	if err != nil {
		return nil, err
	}

	if !active {
		return nil, errors.New("account disabled")
	}

	if time.Now().After(expiresAt) {
		return nil, errors.New("session expired")
	}

	rows, err := dbPool.Query(r.Context(), `
		SELECT role_id FROM user_roles WHERE user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []string
	for rows.Next() {
		var rID string
		if err := rows.Scan(&rID); err == nil {
			roles = append(roles, rID)
		}
	}

	// Best-effort, throttled last-seen stamp; never blocks the request.
	go func(hash string) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		dbPool.Exec(ctx, `
			UPDATE sessions SET last_seen_at = NOW()
			WHERE token_hash = $1 AND (last_seen_at IS NULL OR last_seen_at < NOW() - INTERVAL '60 seconds')
		`, hash)
	}(tokenHash)

	uc := &UserContext{
		ID:       userID,
		Username: username,
		Roles:    roles,
	}
	if displayName != nil {
		uc.DisplayName = *displayName
	}
	uc.HasAvatar = avatarFileID != nil
	return uc, nil
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
		isAdmin := false
		for _, r := range user.Roles {
			if r == "Administrator" {
				isAdmin = true
				break
			}
		}

		if !isAdmin {
			var flag int
			switch perm {
			case "view_channel":
				flag = 1
			case "send_messages":
				flag = 2
			case "join_voice":
				flag = 4
			default:
				flag = 0
			}

			if flag > 0 {
				var allowedCount int
				var deniedCount int

				err = dbPool.QueryRow(ctx, `
					SELECT 
						COUNT(CASE WHEN (deny_mask & $1) <> 0 THEN 1 END),
						COUNT(CASE WHEN (allow_mask & $1) <> 0 THEN 1 END)
					FROM channel_role_overrides
					WHERE channel_id = $2 AND role_id = ANY($3)
				`, flag, *channelID, user.Roles).Scan(&deniedCount, &allowedCount)
				if err != nil {
					return false, err
				}

				if deniedCount > 0 {
					return false, nil
				}
				if allowedCount > 0 {
					return true, nil
				}
			}
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	expectedToken := os.Getenv("TELOS_BOOTSTRAP_TOKEN")
	if expectedToken == "" || body.Token != expectedToken {
		http.Error(w, "Forbidden: Invalid bootstrap token", http.StatusForbidden)
		return
	}

	// Validate username length & character limits
	body.Username = strings.ToLower(strings.TrimSpace(body.Username))
	if len(body.Username) < 3 || len(body.Username) > 32 {
		http.Error(w, "Bad Request: Username must be 3-32 characters", http.StatusBadRequest)
		return
	}
	if len(body.Password) < 15 || len(body.Password) > 128 {
		http.Error(w, "Bad Request: Password must be 15-128 characters", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	var count int
	err := dbPool.QueryRow(ctx, "SELECT COUNT(*) FROM user_roles WHERE role_id = 'Owner'").Scan(&count)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if count > 0 {
		http.Error(w, "Forbidden: Owner already bootstrapped", http.StatusForbidden)
		return
	}

	// Create user
	hash, err := hashPassword(body.Password)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	tx, err := dbPool.Begin(ctx)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	var userID string
	err = tx.QueryRow(ctx, `
		INSERT INTO users (username, password_hash) VALUES ($1, $2) RETURNING id
	`, body.Username, hash).Scan(&userID)
	if err != nil {
		tx.Rollback(ctx)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id) VALUES ($1, 'Owner')
	`, userID)
	if err != nil {
		tx.Rollback(ctx)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Consume token in memory/logs (it will fail future boots as Owner exists in DB)
	log.Printf("Owner %q bootstrapped successfully. Bootstrap token consumed.", body.Username)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "success", "userId": userID})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	clientIP, _, _ := net.SplitHostPort(r.RemoteAddr)
	ctx := r.Context()

	throttled, err := isThrottled(ctx, body.Username, clientIP)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if throttled {
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}

	var userID string
	var hash string
	var active bool

	err = dbPool.QueryRow(ctx, `
		SELECT id, password_hash, active FROM users WHERE username = $1
	`, strings.ToLower(body.Username)).Scan(&userID, &hash, &active)
	if err != nil {
		recordFailedLogin(ctx, body.Username, clientIP)
		http.Error(w, "Unauthorized: Invalid credentials", http.StatusUnauthorized)
		return
	}

	if !active {
		http.Error(w, "Unauthorized: Account disabled", http.StatusUnauthorized)
		return
	}

	ok, err := verifyPassword(body.Password, hash)
	if err != nil || !ok {
		recordFailedLogin(ctx, body.Username, clientIP)
		http.Error(w, "Unauthorized: Invalid credentials", http.StatusUnauthorized)
		return
	}

	// Successful login
	resetFailedLogins(ctx, body.Username, clientIP)

	token, tokenHash := generateToken()
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
	// Decode error ignored on purpose — older clients send no body.
	if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.RoleID != "" {
		roleID = body.RoleID
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
	rawToken, tokenHash := generateToken()
	expiry := time.Now().Add(7 * 24 * time.Hour) // Invite valid for 7 days

	_, err := dbPool.Exec(r.Context(), `
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	body.Username = strings.ToLower(strings.TrimSpace(body.Username))
	if len(body.Username) < 3 || len(body.Username) > 32 {
		http.Error(w, "Bad Request: Username must be 3-32 characters", http.StatusBadRequest)
		return
	}
	if len(body.Password) < 15 || len(body.Password) > 128 {
		http.Error(w, "Bad Request: Password must be 15-128 characters", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	h := sha256.Sum256([]byte(body.Token))
	tokenHash := hex.EncodeToString(h[:])

	var expiresAt time.Time
	var usedAt *time.Time
	var inviteRole *string

	err := dbPool.QueryRow(ctx, `
		SELECT expires_at, used_at, role_id FROM invites WHERE token_hash = $1
	`, tokenHash).Scan(&expiresAt, &usedAt, &inviteRole)
	if err != nil {
		http.Error(w, "Invalid or expired invite token", http.StatusBadRequest)
		return
	}

	if usedAt != nil {
		http.Error(w, "Invite token already used", http.StatusGone)
		return
	}

	if time.Now().After(expiresAt) {
		http.Error(w, "Invite token expired", http.StatusGone)
		return
	}

	hash, err := hashPassword(body.Password)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	tx, err := dbPool.Begin(ctx)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	var userID string
	err = tx.QueryRow(ctx, `
		INSERT INTO users (username, password_hash) VALUES ($1, $2) RETURNING id
	`, body.Username, hash).Scan(&userID)
	if err != nil {
		tx.Rollback(ctx)
		http.Error(w, "Internal Server Error or Username Taken", http.StatusInternalServerError)
		return
	}

	// Assign the invite's role, defaulting to Member
	assignRole := "Member"
	if inviteRole != nil && *inviteRole != "" {
		assignRole = *inviteRole
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)
	`, userID, assignRole)
	if err != nil {
		tx.Rollback(ctx)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Mark invite as used
	_, err = tx.Exec(ctx, `
		UPDATE invites SET used_at = NOW(), used_by = $1 WHERE token_hash = $2
	`, userID, tokenHash)
	if err != nil {
		tx.Rollback(ctx)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "success", "userId": userID})
}

// ═══════════════════════════════════════════════════════════════════════════
// Handler — Health
// ═══════════════════════════════════════════════════════════════════════════

func handleHealth(w http.ResponseWriter, r *http.Request) {
	services := make(map[string]string)
	overallStatus := "healthy"

	if dbPool == nil {
		services["database"] = "uninitialized"
		overallStatus = "unhealthy"
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := dbPool.Ping(ctx); err != nil {
			services["database"] = fmt.Sprintf("unhealthy: %v", err)
			overallStatus = "unhealthy"
		} else {
			services["database"] = "healthy"
		}
	}

	if redisClient == nil {
		services["redis"] = "uninitialized"
		overallStatus = "unhealthy"
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := redisClient.Ping(ctx).Err(); err != nil {
			services["redis"] = fmt.Sprintf("unhealthy: %v", err)
			overallStatus = "unhealthy"
		} else {
			services["redis"] = "healthy"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if overallStatus == "unhealthy" {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	json.NewEncoder(w).Encode(HealthResponse{
		Status:   overallStatus,
		Services: services,
	})
}

// ═══════════════════════════════════════════════════════════════════════════
// Handler — Chat / WebSocket
// ═══════════════════════════════════════════════════════════════════════════

type wsClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (c *wsClient) writeJSON(v interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(v)
}

func (c *wsClient) writeMessage(messageType int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteMessage(messageType, data)
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	channelIDStr := r.URL.Query().Get("channel")
	if channelIDStr == "" {
		channelIDStr = "00000000-0000-0000-0000-000000000001" // Default general
	}

	user := r.Context().Value(userContextKey).(*UserContext)
	userID := user.ID

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()
	client := &wsClient{conn: conn}

	// Set connection limits
	conn.SetReadLimit(8192) // 8 KiB message frame limit

	ctx := r.Context()

	// Fetch & Send past 50 messages of the channel
	msgs, err := getMessagesForChannel(ctx, channelIDStr)
	if err != nil {
		log.Printf("Failed to fetch message history: %v", err)
	} else {
		historyNotification := WSNotification{
			Type:     "history",
			Messages: msgs,
		}
		if err := client.writeJSON(historyNotification); err != nil {
			log.Printf("Failed to send history: %v", err)
			return
		}
	}

	// Subscribe to Redis pub/sub channel for this chat room
	redisChanName := fmt.Sprintf("telos:chat:%s", channelIDStr)
	pubsub := redisClient.Subscribe(ctx, redisChanName)
	defer pubsub.Close()

	// Read messages from Redis and send them to the client WebSocket
	go func() {
		ch := pubsub.Channel()
		for redisMsg := range ch {
			err := client.writeMessage(websocket.TextMessage, []byte(redisMsg.Payload))
			if err != nil {
				return
			}
		}
	}()

	log.Printf("Client '%s' connected to channel '%s' via WebSocket", userID, channelIDStr)

	// Keep the connection open; we no longer accept inbound messages.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			log.Printf("WebSocket connection closed for '%s': %v", userID, err)
			break
		}
	}
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

func handleSendMessage(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("id")
	user := r.Context().Value(userContextKey).(*UserContext)
	var body struct {
		Content string `json:"content"`
		Embed   *struct {
			Kind string `json:"kind"`
			Ref  string `json:"ref"`
		} `json:"embed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", 400)
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

	var msgID string
	var ts time.Time
	err := dbPool.QueryRow(r.Context(), `
		INSERT INTO messages (channel_id, user_id, content) VALUES ($1, $2, $3)
		RETURNING id, created_at`, channelID, user.ID, body.Content).Scan(&msgID, &ts)
	if err != nil {
		http.Error(w, "persist failed", 500)
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", 400)
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
	channelID := r.PathValue("id")
	mid := r.PathValue("mid")
	user := r.Context().Value(userContextKey).(*UserContext)

	var body struct {
		Emoji string `json:"emoji"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", 400)
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
	Type string `json:"type"` // "text" or "voice"
}

func handleListChannels(w http.ResponseWriter, r *http.Request) {
	rows, err := dbPool.Query(r.Context(), `
		SELECT id::text, name, type FROM channels ORDER BY type, name
	`)
	if err != nil {
		http.Error(w, "Failed to list channels", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	channels := []ChannelResponse{}
	for rows.Next() {
		var c ChannelResponse
		if err := rows.Scan(&c.ID, &c.Name, &c.Type); err != nil {
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

	token := getJellyfinAdminToken()
	req, err := http.NewRequestWithContext(ctx, "GET", jellyfinBaseURL+"/Users", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Emby-Token", token)
	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))

	resp, err := http.DefaultClient.Do(req)
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

type LibraryItem struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`           // "video" or "audio"
	CollectionType string `json:"collectionType"` // Jellyfin's raw, lowercased collection type
}

func handleMedia(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if redisClient != nil {
		val, err := redisClient.Get(ctx, "telos:jellyfin:libraries").Result()
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
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("X-Emby-Token", token)
	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))

	resp, err := http.DefaultClient.Do(req)
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

	var libs []LibraryItem
	for _, item := range jResp.Items {
		mediaType := "video"
		cType := strings.ToLower(item.CollectionType)
		// Jellyfin 10.9+ has no distinct "audiobooks" collection type; audiobook
		// libraries are created as CollectionType "books". Telos gets text
		// e-books from Grimmory, so any Jellyfin "books" library is audio here.
		if cType == "music" || cType == "audiobooks" || cType == "audio" || cType == "podcasts" || cType == "books" {
			mediaType = "audio"
		}
		libs = append(libs, LibraryItem{
			ID:             item.ID,
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
		_ = redisClient.Set(ctx, "telos:jellyfin:libraries", string(respJSON), 5*time.Minute).Err()
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(respJSON)
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
	parentId := r.URL.Query().Get("parentId")
	if parentId == "" {
		parentId = r.URL.Query().Get("libraryId")
	}
	if parentId == "" {
		http.Error(w, "Missing parentId or libraryId parameter", http.StatusBadRequest)
		return
	}

	cacheKey := fmt.Sprintf("telos:jellyfin:library-items:%s", parentId)
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
	reqURL := fmt.Sprintf("%s/Users/%s/Items?ParentId=%s&SortBy=IndexNumber,SortName&SortOrder=Ascending&Fields=ChildCount", jellyfinBaseURL, userID, parentId)
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("X-Emby-Token", token)
	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))

	resp, err := http.DefaultClient.Do(req)
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

	items := mapJellyfinChildren(jResp.Items)

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

func proxyRequest(w http.ResponseWriter, r *http.Request, targetURLStr string, token string) {
	targetURL, err := url.Parse(targetURLStr)
	if err != nil {
		log.Printf("Failed to parse target URL %s: %v", targetURLStr, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = targetURL.Scheme
			req.URL.Host = targetURL.Host
			req.URL.Path = targetURL.Path
			if req.URL.RawQuery != "" && targetURL.RawQuery != "" {
				req.URL.RawQuery = targetURL.RawQuery + "&" + req.URL.RawQuery
			} else if targetURL.RawQuery != "" {
				req.URL.RawQuery = targetURL.RawQuery
			}
			req.Host = targetURL.Host
			if token != "" {
				req.Header.Set("X-Emby-Token", token)
				req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			// corsMiddleware is the single CORS authority; upstream CORS
			// headers would merge into illegal duplicates (browsers reject
			// "origin, *" on credentialed cross-origin dev requests).
			for _, h := range []string{
				"Access-Control-Allow-Origin",
				"Access-Control-Allow-Credentials",
				"Access-Control-Allow-Methods",
				"Access-Control-Allow-Headers",
			} {
				resp.Header.Del(h)
			}
			return nil
		},
	}
	proxy.ServeHTTP(w, r)
}

func handleStreamAudio(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Missing item ID", http.StatusBadRequest)
		return
	}
	token := getJellyfinAdminToken()
	targetURL := fmt.Sprintf("%s/Audio/%s/stream?static=true", jellyfinBaseURL, id)
	proxyRequest(w, r, targetURL, token)
}

func handleStreamVideo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Missing item ID", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	userID, err := getJellyfinUserID(ctx)
	if err != nil {
		http.Error(w, "Failed to resolve Jellyfin user ID: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	token := getJellyfinAdminToken()
	playbackInfoURL := fmt.Sprintf("%s/Items/%s/PlaybackInfo?UserId=%s", jellyfinBaseURL, id, userID)

	req, err := http.NewRequestWithContext(ctx, "POST", playbackInfoURL, strings.NewReader("{}"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Emby-Token", token)
	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))

	resp, err := http.DefaultClient.Do(req)
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

	redirectURL := fmt.Sprintf("/api/v1/stream/video/%s/main.m3u8?PlaySessionId=%s", id, jResp.PlaySessionId)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func handleStreamVideoSubpath(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	subpath := r.PathValue("path")
	if id == "" || subpath == "" {
		http.Error(w, "Missing item ID or subpath", http.StatusBadRequest)
		return
	}
	token := getJellyfinAdminToken()
	targetURL := fmt.Sprintf("%s/Videos/%s/%s", jellyfinBaseURL, id, subpath)
	proxyRequest(w, r, targetURL, token)
}

func handleJellyfinDirectProxy(w http.ResponseWriter, r *http.Request) {
	token := getJellyfinAdminToken()
	targetURL := fmt.Sprintf("http://jellyfin:8096%s", r.URL.Path)
	proxyRequest(w, r, targetURL, token)
}

// ═══════════════════════════════════════════════════════════════════════════
// Grimmory Proxy
// ═══════════════════════════════════════════════════════════════════════════

func handleGrimmoryProxy(w http.ResponseWriter, r *http.Request) {
	grimmoryURL, _ := url.Parse(grimmoryBaseURL)
	proxy := httputil.NewSingleHostReverseProxy(grimmoryURL)
	// Grimmory rejects static tokens; inject a JWT minted via admin login.
	tok, err := getGrimmoryToken(r.Context())
	if err != nil {
		http.Error(w, "Grimmory unreachable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	proxy.ServeHTTP(w, r)
}

// ═══════════════════════════════════════════════════════════════════════════
// LiveKit Voice Token (Hardened SDK)
// ═══════════════════════════════════════════════════════════════════════════

type VideoGrant struct {
	Room           string `json:"room,omitempty"`
	RoomJoin       bool   `json:"roomJoin,omitempty"`
	CanPublish     bool   `json:"canPublish,omitempty"`
	CanSubscribe   bool   `json:"canSubscribe,omitempty"`
	CanPublishData bool   `json:"canPublishData,omitempty"`
}

type LiveKitClaims struct {
	Exp   int64      `json:"exp"`
	Iss   string     `json:"iss"`
	Sub   string     `json:"sub"`
	Nbf   int64      `json:"nbf"`
	Video VideoGrant `json:"video"`
}

func GenerateLiveKitToken(apiKey, apiSecret, roomName, identity string) (string, error) {
	at := auth.NewAccessToken(apiKey, apiSecret)
	at.SetIdentity(identity)
	at.SetValidFor(5 * time.Minute) // 5 minutes room access bootstrap token

	grant := &auth.VideoGrant{
		Room:     roomName,
		RoomJoin: true,
	}
	grant.SetCanPublish(true)
	grant.SetCanSubscribe(true)
	grant.SetCanPublishData(false)

	at.AddGrant(grant)
	return at.ToJWT()
}

func handleVoiceToken(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("id")
	if channelID == "" {
		http.Error(w, "Missing channel ID", http.StatusBadRequest)
		return
	}

	user := r.Context().Value(userContextKey).(*UserContext)

	// Validate channel type is voice
	var cType string
	err := dbPool.QueryRow(r.Context(), "SELECT type FROM channels WHERE id = $1", channelID).Scan(&cType)
	if err != nil {
		http.Error(w, "Voice channel not found", http.StatusNotFound)
		return
	}
	if cType != "voice" {
		http.Error(w, "Channel is not a voice channel", http.StatusBadRequest)
		return
	}

	apiKey := os.Getenv("LIVEKIT_API_KEY")
	apiSecret := os.Getenv("LIVEKIT_API_SECRET")
	if apiKey == "" || apiSecret == "" {
		http.Error(w, "LiveKit credentials are not configured", http.StatusInternalServerError)
		return
	}

	// Fetch token (room name matches channel ID UUID)
	token, err := GenerateLiveKitToken(apiKey, apiSecret, channelID, user.ID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to generate voice token: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"token": token,
	})
}

// ═══════════════════════════════════════════════════════════════════════════
// Shared File Library & ClamAV Integration
// ═══════════════════════════════════════════════════════════════════════════

func scanFileWithClamAV(r io.Reader) (bool, string, error) {
	conn, err := net.DialTimeout("tcp", "telos-clamav:3310", 5*time.Second)
	if err != nil {
		return false, "unreachable", fmt.Errorf("failed to connect to ClamAV: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("zINSTREAM\000")); err != nil {
		return false, "error", fmt.Errorf("failed to initiate scan: %v", err)
	}

	buf := make([]byte, 8192)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			lengthBytes := []byte{
				byte(n >> 24),
				byte(n >> 16),
				byte(n >> 8),
				byte(n),
			}
			if _, err := conn.Write(lengthBytes); err != nil {
				return false, "error", fmt.Errorf("failed to write chunk size: %v", err)
			}
			if _, err := conn.Write(buf[:n]); err != nil {
				return false, "error", fmt.Errorf("failed to write chunk: %v", err)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, "error", fmt.Errorf("failed to read upload stream: %v", err)
		}
	}

	if _, err := conn.Write([]byte{0, 0, 0, 0}); err != nil {
		return false, "error", fmt.Errorf("failed to terminate scan stream: %v", err)
	}

	resp, err := io.ReadAll(conn)
	if err != nil {
		return false, "error", fmt.Errorf("failed to read Scan response: %v", err)
	}

	respStr := string(resp)
	log.Printf("ClamAV response: %s", respStr)
	if strings.Contains(respStr, "OK") {
		return true, "clean", nil
	}
	if strings.Contains(respStr, "FOUND") {
		return false, "infected", nil
	}
	return false, "failed", fmt.Errorf("unexpected scan response: %s", respStr)
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

func handleUploadBook(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	processUpload(w, r, user.ID, true)
}

var defaultUploadExts = map[string]bool{
	".pdf": true, ".epub": true, ".jpg": true, ".jpeg": true,
	".png": true, ".webp": true, ".mp3": true, ".m4a": true,
	".ogg": true, ".wav": true, ".mp4": true, ".webm": true,
}

func processUpload(w http.ResponseWriter, r *http.Request, uploaderID string, isBook bool) {
	processUploadWithLimits(w, r, uploaderID, isBook, 104857600, defaultUploadExts, true)
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

	if isBook && ext != ".pdf" && ext != ".epub" {
		http.Error(w, "Only PDF and EPUB are allowed for book uploads", http.StatusBadRequest)
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
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
