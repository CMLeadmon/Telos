package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
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
			return true // In production, validate origin
		},
	}
	writeMutex sync.Mutex
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
	if databaseURL == "" {
		databaseURL = "postgres://telos:change-me@localhost:5432/telos?sslmode=disable"
	}

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://:change-me@localhost:6379/0"
	}

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

	// Mock endpoints from gateway spec
	mux.HandleFunc("GET /api/v1/media", handleMockMedia)
	mux.HandleFunc("GET /api/v1/media/items", handleMockMediaItems)
	mux.HandleFunc("GET /api/v1/stream/audio/{id}", handleMockStreamAudio)
	mux.HandleFunc("GET /api/v1/stream/video/{id}", handleMockStreamVideo)
	mux.HandleFunc("GET /api/v1/library/books", handleMockBooks)
	mux.HandleFunc("GET /api/v1/library/facets", handleMockFacets)
	mux.HandleFunc("POST /api/v1/library/progress", handleMockProgress)

	// Frontend static assets handler
	mux.Handle("/", fileServer)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	log.Printf("Server listening on port %s", port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed: %v", err)
	}
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

// Thread-safe WebSocket write wrappers
func safeWriteJSON(conn *websocket.Conn, v interface{}) error {
	writeMutex.Lock()
	defer writeMutex.Unlock()
	return conn.WriteJSON(v)
}

func safeWriteMessage(conn *websocket.Conn, messageType int, data []byte) error {
	writeMutex.Lock()
	defer writeMutex.Unlock()
	return conn.WriteMessage(messageType, data)
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	channelID := r.URL.Query().Get("channel")
	if channelID == "" {
		channelID = "general"
	}

	userID := r.URL.Query().Get("user")
	if userID == "" {
		userID = "cleadmon"
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	ctx := r.Context()

	// Auto-provision user if they do not exist
	if dbPool != nil {
		var exists bool
		err = dbPool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)", userID).Scan(&exists)
		if err != nil {
			log.Printf("DB error checking user: %v", err)
		} else if !exists {
			avatar := "US"
			if len(userID) >= 2 {
				avatar = userID[:2]
			}
			role := "Member"
			if userID == "cleadmon" {
				role = "Host"
			}
			_, err = dbPool.Exec(ctx, `
				INSERT INTO users (id, username, avatar, role) 
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (id) DO NOTHING
			`, userID, userID, avatar, role)
			if err != nil {
				log.Printf("Failed to auto-provision user %s: %v", userID, err)
			}
		}
	}

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
			if err := safeWriteJSON(conn, historyNotification); err != nil {
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
				err := safeWriteMessage(conn, websocket.TextMessage, []byte(redisMsg.Payload))
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
			_ = safeWriteMessage(conn, websocket.TextMessage, payload)
		}
	}
}

func getMessagesForChannel(ctx context.Context, channelID string) ([]WSMessage, error) {
	rows, err := dbPool.Query(ctx, `
		SELECT m.id::text, u.username, u.avatar, u.role, m.content, m.timestamp 
		FROM messages m 
		JOIN users u ON m.user_id = u.id 
		WHERE m.channel_id = $1 
		ORDER BY m.timestamp ASC 
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
	return msgs, nil
}

func handleMockMedia(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode([]map[string]interface{}{
		{"id": "movies", "name": "Movies", "type": "video"},
		{"id": "docs", "name": "Documentaries", "type": "video"},
		{"id": "audiobooks", "name": "Audiobooks", "type": "audio"},
	})
}

func handleMockMediaItems(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode([]map[string]interface{}{
		{"id": "m1", "title": "Sovereignty of Code", "duration": "1h 45m"},
		{"id": "m2", "title": "The Digital Enclosure", "duration": "42m"},
	})
}

func handleMockStreamAudio(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	w.Header().Set("Content-Type", "audio/mpeg")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf("Mock audio stream for id: %s", id)))
}

func handleMockStreamVideo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	w.Header().Set("Content-Type", "application/x-mpegURL")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf("Mock HLS stream for id: %s", id)))
}

func handleMockBooks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode([]map[string]interface{}{
		{"id": "b1", "title": "The Design Creed", "author": "Telos Core", "format": "EPUB"},
		{"id": "b2", "title": "Out of the Enclosure", "author": "Sovereign Citizen", "format": "PDF"},
	})
}

func handleMockFacets(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"authors": []string{"Telos Core", "Sovereign Citizen"},
		"formats": []string{"EPUB", "PDF"},
	})
}

func handleMockProgress(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "success", "message": "progress persisted"}`))
}
