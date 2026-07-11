package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

var (
	dbPool      *pgxpool.Pool
	redisClient *redis.Client
	upgrader    = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return isAllowedWSOrigin(r.Header.Get("Origin"), r.Host, os.Getenv("TELOS_DOMAIN"))
		},
	}
)

//go:embed db/schema.sql
var schemaSQL string

//go:embed all:out
var frontendFS embed.FS

type HealthResponse struct {
	Status   string            `json:"status"`
	Services map[string]string `json:"services"`
}

type WSMessage struct {
	ID        string `json:"id"`
	Sender    string `json:"sender"`
	Avatar    string `json:"avatar"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
}

type WSNotification struct {
	Type     string      `json:"type"`               // "history" or "message"
	Messages []WSMessage `json:"messages,omitempty"` // For history
	Message  *WSMessage  `json:"message,omitempty"`  // For individual message
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	databaseURL := os.Getenv("DATABASE_URL")
	redisURL := os.Getenv("REDIS_URL")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Initialize Postgres Pool
	var err error
	dbPool, err = pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Printf("Warning: Failed to connect to database: %v", err)
	} else {
		defer dbPool.Close()
		// Initialize tables if pool connected successfully
		if err := initDatabase(ctx); err != nil {
			log.Printf("Warning: Failed to initialize database: %v", err)
		} else {
			log.Println("Database schema checked/initialized successfully")
		}
	}

	// Initialize Redis Client
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Printf("Warning: Failed to parse Redis URL: %v", err)
	} else {
		redisClient = redis.NewClient(opt)
		defer redisClient.Close()
	}

	// Get sub-filesystem for the frontend static files
	subFS, err := fs.Sub(frontendFS, "out")
	if err != nil {
		log.Fatalf("Failed to locate frontend out/ directory: %v", err)
	}
	fileServer := http.FileServer(http.FS(subFS))

	// Create ServeMux using Go 1.22+ routing rules
	mux := http.NewServeMux()

	// API and Real-Time routes
	mux.HandleFunc("GET /api/v1/health", handleHealth)
	mux.HandleFunc("GET /api/v1/chat/ws", handleWebSocket)
	mux.HandleFunc("GET /api/v1/voice/token", handleLiveKitToken)

	// LiveKit WebSocket reverse proxy — allows the frontend to reach the
	// LiveKit signaling server through the Go gateway at /livekit/*.
	livekitTarget := os.Getenv("LIVEKIT_URL")
	if livekitTarget == "" {
		livekitTarget = "http://livekit:7880"
	}
	lkURL, err := url.Parse(livekitTarget)
	if err != nil {
		log.Fatalf("Invalid LIVEKIT_URL: %v", err)
	}
	livekitProxy := httputil.NewSingleHostReverseProxy(lkURL)
	originalDirector := livekitProxy.Director
	livekitProxy.Director = func(req *http.Request) {
		originalDirector(req)
		// Strip the /livekit prefix so the LiveKit server sees /rtc, /ws, etc.
		req.URL.Path = strings.TrimPrefix(req.URL.Path, "/livekit")
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		req.URL.RawPath = strings.TrimPrefix(req.URL.RawPath, "/livekit")
		req.Host = lkURL.Host
		// Preserve WebSocket upgrade headers
		if hdr := req.Header.Get("Upgrade"); hdr != "" {
			req.Header.Set("Upgrade", hdr)
			req.Header.Set("Connection", "Upgrade")
		}
	}
	mux.Handle("/livekit/", livekitProxy)

	// Real Jellyfin endpoints
	mux.HandleFunc("GET /api/v1/media", handleMedia)
	mux.HandleFunc("GET /api/v1/media/items", handleMediaItems)
	mux.HandleFunc("GET /api/v1/stream/audio/{id}", handleStreamAudio)
	mux.HandleFunc("GET /api/v1/stream/video/{id}", handleStreamVideo)
	mux.HandleFunc("GET /api/v1/stream/video/{id}/{path...}", handleStreamVideoSubpath)

	// Frontend static assets handler
	mux.Handle("/", fileServer)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      corsMiddleware(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	log.Printf("Server listening on port %s", port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed: %v", err)
	}
}

// isAllowedWSOrigin permits same-origin browsers, the configured public
// domain, local development hosts, and non-browser clients (no Origin header).
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
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, HEAD")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Emby-Token")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func initDatabase(ctx context.Context) error {
	log.Println("Executing database schema migration...")
	_, err := dbPool.Exec(ctx, schemaSQL)
	return err
}

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

// wsClient serializes writes to a single WebSocket connection; gorilla/websocket
// does not support concurrent writers on one connection.
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
	channelID := r.URL.Query().Get("channel")
	if channelID == "" {
		channelID = "general"
	}

	token := r.URL.Query().Get("token")
	expectedToken := os.Getenv("WS_AUTH_TOKEN")
	if expectedToken != "" && token != expectedToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	userID := r.URL.Query().Get("user")
	if userID == "" {
		http.Error(w, "Missing user ID", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()
	client := &wsClient{conn: conn}

	ctx := r.Context()

	// Fetch & Send past 50 messages of the channel
	if dbPool != nil {
		msgs, err := getMessagesForChannel(ctx, channelID)
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
	}

	// Subscribe to Redis pub/sub channel for this chat room
	var pubsub *redis.PubSub
	if redisClient != nil {
		redisChanName := fmt.Sprintf("telos:chat:%s", channelID)
		pubsub = redisClient.Subscribe(ctx, redisChanName)
		defer pubsub.Close()

		// Read messages from Redis and send them to the client WebSocket
		go func() {
			ch := pubsub.Channel()
			for redisMsg := range ch {
				err := client.writeMessage(websocket.TextMessage, []byte(redisMsg.Payload))
				if err != nil {
					log.Printf("Failed to send Redis broadcast to WS: %v", err)
					return
				}
			}
		}()
	}

	log.Printf("Client '%s' connected to channel '%s' via WebSocket", userID, channelID)

	// Read loop: receive messages from this user, persist, and broadcast
	for {
		_, p, err := conn.ReadMessage()
		if err != nil {
			log.Printf("WebSocket read ended for '%s': %v", userID, err)
			break
		}

		var incoming struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(p, &incoming); err != nil {
			log.Printf("Failed to unmarshal WS message: %v", err)
			continue
		}

		if incoming.Content == "" {
			continue
		}

		var msgID string
		var timestamp time.Time
		var username, avatar, role string

		if dbPool != nil {
			// Persist to PostgreSQL
			err = dbPool.QueryRow(ctx, `
				INSERT INTO messages (channel_id, user_id, content) 
				VALUES ($1, $2, $3) 
				RETURNING id, timestamp
			`, channelID, userID, incoming.Content).Scan(&msgID, &timestamp)
			if err != nil {
				log.Printf("Failed to persist message: %v", err)
				continue
			}

			// Get sender profile details
			err = dbPool.QueryRow(ctx, `
				SELECT username, avatar, role FROM users WHERE id = $1
			`, userID).Scan(&username, &avatar, &role)
			if err != nil {
				log.Printf("Failed to fetch sender profile: %v", err)
				continue
			}
		} else {
			// Fallback mock values if DB is uninitialized
			msgID = fmt.Sprintf("mock-%d", time.Now().UnixNano())
			timestamp = time.Now()
			username = userID
			avatar = "US"
			role = "Member"
		}

		// Create broadcast notification payload
		broadcastMsg := WSNotification{
			Type: "message",
			Message: &WSMessage{
				ID:        msgID,
				Sender:    username,
				Avatar:    avatar,
				Role:      role,
				Content:   incoming.Content,
				Timestamp: timestamp.Format("03:04 pm"),
			},
		}

		payload, err := json.Marshal(broadcastMsg)
		if err != nil {
			log.Printf("Failed to serialize broadcast message: %v", err)
			continue
		}

		if redisClient != nil {
			// Publish message to Redis, propagating to all active gateways/clients
			redisChanName := fmt.Sprintf("telos:chat:%s", channelID)
			err = redisClient.Publish(ctx, redisChanName, payload).Err()
			if err != nil {
				log.Printf("Failed to publish to Redis: %v", err)
			}
		} else {
			// Local connection loopback fallback if Redis is uninitialized
			_ = client.writeMessage(websocket.TextMessage, payload)
		}
	}
}

func getMessagesForChannel(ctx context.Context, channelID string) ([]WSMessage, error) {
	// Fetch the latest 50, then reverse so the client renders oldest-first.
	rows, err := dbPool.Query(ctx, `
		SELECT m.id::text, u.username, u.avatar, u.role, m.content, m.timestamp
		FROM messages m
		JOIN users u ON m.user_id = u.id
		WHERE m.channel_id = $1
		ORDER BY m.timestamp DESC
		LIMIT 50
	`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []WSMessage
	for rows.Next() {
		var msg WSMessage
		var t time.Time
		if err := rows.Scan(&msg.ID, &msg.Sender, &msg.Avatar, &msg.Role, &msg.Content, &t); err != nil {
			return nil, err
		}
		msg.Timestamp = t.Format("03:04 pm")
		msgs = append(msgs, msg)
	}
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

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
		log.Printf("Jellyfin Users API unmarshal error: %v. Raw Body: %s", err, string(bodyBytes))
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
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"` // "video" or "audio"
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

	var libs []LibraryItem
	var fetchFailed bool

	userID, err := getJellyfinUserID(ctx)
	if err != nil {
		log.Printf("Jellyfin User ID error: %v. Falling back to mock libraries.", err)
		fetchFailed = true
	} else {
		token := getJellyfinAdminToken()
		reqURL := fmt.Sprintf("%s/Users/%s/Views", jellyfinBaseURL, userID)
		req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if err != nil {
			log.Printf("Failed to create request: %v", err)
			fetchFailed = true
		} else {
			req.Header.Set("X-Emby-Token", token)
			req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				log.Printf("Jellyfin Views request failed: %v", err)
				fetchFailed = true
			} else {
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					log.Printf("Jellyfin Views API returned status %s", resp.Status)
					fetchFailed = true
				} else {
					var jResp struct {
						Items []struct {
							ID             string `json:"Id"`
							Name           string `json:"Name"`
							CollectionType string `json:"CollectionType"`
						} `json:"Items"`
					}
					if err := json.NewDecoder(resp.Body).Decode(&jResp); err != nil {
						log.Printf("Failed to decode Jellyfin Views response: %v", err)
						fetchFailed = true
					} else {
						for _, item := range jResp.Items {
							mediaType := "video"
							cType := strings.ToLower(item.CollectionType)
							if cType == "music" || cType == "audiobooks" || cType == "audio" || cType == "podcasts" {
								mediaType = "audio"
							}
							libs = append(libs, LibraryItem{
								ID:   item.ID,
								Name: item.Name,
								Type: mediaType,
							})
						}
					}
				}
			}
		}
	}

	if fetchFailed || len(libs) == 0 {
		log.Println("Serving fallback mock libraries")
		libs = []LibraryItem{
			{ID: "movies", Name: "Movies (Mock)", Type: "video"},
			{ID: "music", Name: "Music (Mock)", Type: "audio"},
			{ID: "books", Name: "Audiobooks (Mock)", Type: "audio"},
		}
		fetchFailed = false
	}

	respJSON, err := json.Marshal(libs)
	if err != nil {
		log.Printf("Failed to marshal response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if redisClient != nil && !fetchFailed {
		_ = redisClient.Set(ctx, "telos:jellyfin:libraries", string(respJSON), 5*time.Minute).Err()
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(respJSON)
}

type MediaPlayableItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Duration string `json:"duration"`
	Type     string `json:"type"` // e.g. "Movie", "Episode", "Audio"
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

	var items []MediaPlayableItem
	var fetchFailed bool

	userID, err := getJellyfinUserID(ctx)
	if err != nil {
		log.Printf("Jellyfin User ID error: %v. Falling back to mock items.", err)
		fetchFailed = true
	} else {
		token := getJellyfinAdminToken()
		reqURL := fmt.Sprintf("%s/Users/%s/Items?ParentId=%s&Recursive=true&IncludeItemTypes=Movie,Episode,Audio,Audiobook", jellyfinBaseURL, userID, parentId)
		req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if err != nil {
			log.Printf("Failed to create request: %v", err)
			fetchFailed = true
		} else {
			req.Header.Set("X-Emby-Token", token)
			req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				log.Printf("Jellyfin Items request failed: %v", err)
				fetchFailed = true
			} else {
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					log.Printf("Jellyfin Items API returned status %s", resp.Status)
					fetchFailed = true
				} else {
					var jResp struct {
						Items []struct {
							ID           string `json:"Id"`
							Name         string `json:"Name"`
							RunTimeTicks int64  `json:"RunTimeTicks"`
							Type         string `json:"Type"`
						} `json:"Items"`
					}
					if err := json.NewDecoder(resp.Body).Decode(&jResp); err != nil {
						log.Printf("Failed to decode Jellyfin Items response: %v", err)
						fetchFailed = true
					} else {
						for _, item := range jResp.Items {
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

							items = append(items, MediaPlayableItem{
								ID:       item.ID,
								Title:    item.Name,
								Duration: durationStr,
								Type:     item.Type,
							})
						}
					}
				}
			}
		}
	}

	if fetchFailed || len(items) == 0 {
		log.Printf("Serving fallback mock items for parentId: %s", parentId)
		if parentId == "movies" || parentId == "Movies (Mock)" {
			items = []MediaPlayableItem{
				{ID: "raising-helen", Title: "Raising Helen (2004)", Duration: "1h 59m", Type: "Movie"},
				{ID: "code-sovereignty", Title: "Sovereignty of Code", Duration: "1h 45m", Type: "Movie"},
			}
		} else if parentId == "music" || parentId == "Music (Mock)" {
			items = []MediaPlayableItem{
				{ID: "ambient-rain", Title: "Ambient Rain", Duration: "4m 12s", Type: "Audio"},
				{ID: "vaporwave-chill", Title: "Vaporwave Chill", Duration: "3m 45s", Type: "Audio"},
			}
		} else {
			items = []MediaPlayableItem{
				{ID: "sample-audio", Title: "Sample Audiobook Track", Duration: "12m 30s", Type: "Audiobook"},
			}
		}
		fetchFailed = false
	}

	respJSON, err := json.Marshal(items)
	if err != nil {
		log.Printf("Failed to marshal response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if redisClient != nil && !fetchFailed {
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
			req.Header.Set("X-Emby-Token", token)
			req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))
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
	log.Printf("[STREAM] handleStreamVideo called for ID: %s", id)
	if id == "" {
		http.Error(w, "Missing item ID", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	userID, err := getJellyfinUserID(ctx)
	if err != nil {
		log.Printf("Jellyfin User ID error: %v", err)
		http.Error(w, "Failed to resolve Jellyfin user ID", http.StatusInternalServerError)
		return
	}

	token := getJellyfinAdminToken()
	playbackInfoURL := fmt.Sprintf("%s/Items/%s/PlaybackInfo?UserId=%s", jellyfinBaseURL, id, userID)

	req, err := http.NewRequestWithContext(ctx, "POST", playbackInfoURL, strings.NewReader("{}"))
	if err != nil {
		log.Printf("Failed to create request for PlaybackInfo: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Emby-Token", token)
	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("Jellyfin PlaybackInfo request failed: %v", err)
		http.Error(w, "Failed to get playback info", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Jellyfin PlaybackInfo API returned status %s", resp.Status)
		http.Error(w, "Failed to get playback info from Jellyfin", resp.StatusCode)
		return
	}

	var jResp struct {
		PlaySessionId string `json:"PlaySessionId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jResp); err != nil {
		log.Printf("Failed to decode PlaybackInfo response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if jResp.PlaySessionId == "" {
		jResp.PlaySessionId = fmt.Sprintf("telos-session-%d", time.Now().UnixNano())
	}

	redirectURL := fmt.Sprintf("/api/v1/stream/video/%s/main.m3u8?PlaySessionId=%s", id, jResp.PlaySessionId)
	log.Printf("[STREAM] Redirecting ID %s to HLS playlist: %s", id, redirectURL)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func handleStreamVideoSubpath(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	subpath := r.PathValue("path")
	log.Printf("[STREAM] handleStreamVideoSubpath called for ID: %s, subpath: %s, query: %s", id, subpath, r.URL.RawQuery)
	if id == "" || subpath == "" {
		http.Error(w, "Missing item ID or subpath", http.StatusBadRequest)
		return
	}

	token := getJellyfinAdminToken()
	targetURL := fmt.Sprintf("%s/Videos/%s/%s", jellyfinBaseURL, id, subpath)

	proxyRequest(w, r, targetURL, token)
}



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

func base64URLEncode(b []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(b), "=")
}

func GenerateLiveKitToken(apiKey, apiSecret, roomName, identity string) (string, error) {
	header := map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	headerEncoded := base64URLEncode(headerBytes)

	now := time.Now().Unix()
	claims := LiveKitClaims{
		Exp: now + 3600, // 1 hour expiry
		Iss: apiKey,
		Sub: identity,
		Nbf: now - 5, // slightly in past to account for clock drift
		Video: VideoGrant{
			Room:           roomName,
			RoomJoin:       true,
			CanPublish:     true,
			CanSubscribe:   true,
			CanPublishData: true,
		},
	}
	claimsBytes, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payloadEncoded := base64URLEncode(claimsBytes)

	signingInput := headerEncoded + "." + payloadEncoded
	key := []byte(apiSecret)
	h := hmac.New(sha256.New, key)
	h.Write([]byte(signingInput))
	signature := h.Sum(nil)
	signatureEncoded := base64URLEncode(signature)

	return signingInput + "." + signatureEncoded, nil
}

func handleLiveKitToken(w http.ResponseWriter, r *http.Request) {
	room := r.URL.Query().Get("room")
	if room == "" {
		http.Error(w, "Missing room parameter", http.StatusBadRequest)
		return
	}

	user := r.URL.Query().Get("user")
	if user == "" {
		http.Error(w, "Missing user parameter", http.StatusBadRequest)
		return
	}

	apiKey := os.Getenv("LIVEKIT_API_KEY")
	apiSecret := os.Getenv("LIVEKIT_API_SECRET")
	if apiKey == "" || apiSecret == "" {
		http.Error(w, "LiveKit credentials are not configured", http.StatusInternalServerError)
		return
	}

	token, err := GenerateLiveKitToken(apiKey, apiSecret, room, user)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to generate token: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"token": token,
	})
}
