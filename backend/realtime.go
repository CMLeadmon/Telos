package main

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const wsWriteDeadline = 10 * time.Second

// wsClient is a single-writer WebSocket wrapper: all frames flow through a
// bounded outbound queue drained by one writer goroutine with a write
// deadline. A full queue means a slow/stuck consumer and closes the socket
// deterministically. It satisfies RevocableConnection.
type wsClient struct {
	conn      *websocket.Conn
	out       chan wsOutbound
	closeOnce sync.Once
	done      chan struct{}
}

type wsOutbound struct {
	messageType int
	data        []byte
}

func newWSClient(conn *websocket.Conn) *wsClient {
	c := &wsClient{
		conn: conn,
		out:  make(chan wsOutbound, wsWriteQueueDepth),
		done: make(chan struct{}),
	}
	go c.writeLoop()
	return c
}

func (c *wsClient) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case msg, ok := <-c.out:
			if !ok {
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteDeadline))
			if err := c.conn.WriteMessage(msg.messageType, msg.data); err != nil {
				c.close()
				return
			}
		}
	}
}

// enqueue queues a frame. A full queue (slow consumer) closes the socket and
// reports failure so callers stop feeding it.
func (c *wsClient) enqueue(messageType int, data []byte) bool {
	select {
	case <-c.done:
		return false
	default:
	}
	select {
	case c.out <- wsOutbound{messageType: messageType, data: data}:
		return true
	default:
		c.close()
		return false
	}
}

func (c *wsClient) sendJSON(v interface{}) bool {
	data, err := json.Marshal(v)
	if err != nil {
		return false
	}
	return c.enqueue(websocket.TextMessage, data)
}

func (c *wsClient) sendText(data []byte) bool {
	return c.enqueue(websocket.TextMessage, data)
}

// ping writes a control frame directly (bypassing the queue) with its own
// short deadline; control frames must not be delayed behind buffered data.
func (c *wsClient) ping() error {
	return c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
}

func (c *wsClient) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

// CloseWithCode sends a close control frame then tears down the socket.
func (c *wsClient) CloseWithCode(code int, reason string) {
	c.closeOnce.Do(func() {
		deadline := time.Now().Add(2 * time.Second)
		_ = c.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(code, reason), deadline)
		close(c.done)
		_ = c.conn.Close()
	})
}

// WebSocket limits (S05/S08).
const (
	maxSocketsPerUser     = 3
	maxSubscribersPerChan = 100
	wsWriteQueueDepth     = 128
	wsInboundLimitBytes   = 16 << 10
)

// RevocableConnection is a live socket that can be force-closed by a security
// mutation.
type RevocableConnection interface {
	CloseWithCode(code int, reason string)
}

// SessionRegistry tracks every live socket by session hash and user ID so a
// revocation, expiry, disable, delete, or permission change can close it
// immediately rather than waiting for the periodic revalidation fallback.
type SessionRegistry interface {
	Register(sessionHash, userID string, conn RevocableConnection) (release func(), ok bool)
	RevokeSession(sessionHash string)
	RevokeUser(userID string)
}

type registeredConn struct {
	sessionHash string
	userID      string
	conn        RevocableConnection
}

type sessionRegistry struct {
	mu      sync.Mutex
	bySess  map[string]map[*registeredConn]struct{}
	byUser  map[string]map[*registeredConn]struct{}
	perUser map[string]int
}

func newSessionRegistry() *sessionRegistry {
	return &sessionRegistry{
		bySess:  make(map[string]map[*registeredConn]struct{}),
		byUser:  make(map[string]map[*registeredConn]struct{}),
		perUser: make(map[string]int),
	}
}

// Register adds a socket. It returns ok=false (and registers nothing) when the
// user already holds maxSocketsPerUser live sockets.
func (r *sessionRegistry) Register(sessionHash, userID string, conn RevocableConnection) (func(), bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.perUser[userID] >= maxSocketsPerUser {
		return nil, false
	}
	rc := &registeredConn{sessionHash: sessionHash, userID: userID, conn: conn}
	if r.bySess[sessionHash] == nil {
		r.bySess[sessionHash] = make(map[*registeredConn]struct{})
	}
	r.bySess[sessionHash][rc] = struct{}{}
	if r.byUser[userID] == nil {
		r.byUser[userID] = make(map[*registeredConn]struct{})
	}
	r.byUser[userID][rc] = struct{}{}
	r.perUser[userID]++

	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.removeLocked(rc)
	}, true
}

func (r *sessionRegistry) removeLocked(rc *registeredConn) {
	if set := r.bySess[rc.sessionHash]; set != nil {
		if _, ok := set[rc]; ok {
			delete(set, rc)
			if len(set) == 0 {
				delete(r.bySess, rc.sessionHash)
			}
			if set := r.byUser[rc.userID]; set != nil {
				delete(set, rc)
				if len(set) == 0 {
					delete(r.byUser, rc.userID)
				}
			}
			r.perUser[rc.userID]--
			if r.perUser[rc.userID] <= 0 {
				delete(r.perUser, rc.userID)
			}
		}
	}
}

func (r *sessionRegistry) RevokeSession(sessionHash string) {
	r.mu.Lock()
	conns := make([]RevocableConnection, 0)
	for rc := range r.bySess[sessionHash] {
		conns = append(conns, rc.conn)
	}
	r.mu.Unlock()
	for _, c := range conns {
		c.CloseWithCode(websocket.ClosePolicyViolation, "session revoked")
	}
}

func (r *sessionRegistry) RevokeUser(userID string) {
	r.mu.Lock()
	conns := make([]RevocableConnection, 0)
	for rc := range r.byUser[userID] {
		conns = append(conns, rc.conn)
	}
	r.mu.Unlock()
	for _, c := range conns {
		c.CloseWithCode(websocket.ClosePolicyViolation, "access revoked")
	}
}

// CloseAll closes every live socket (used during graceful shutdown).
func (r *sessionRegistry) CloseAll(reason string) {
	r.mu.Lock()
	conns := make([]RevocableConnection, 0)
	for _, set := range r.byUser {
		for rc := range set {
			conns = append(conns, rc.conn)
		}
	}
	r.mu.Unlock()
	for _, c := range conns {
		c.CloseWithCode(websocket.CloseGoingAway, reason)
	}
}

// LiveCount returns the number of live sockets a user currently holds. Used by
// tests to wait for a handler to fully release before tearing down.
func (r *sessionRegistry) LiveCount(userID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.perUser[userID]
}

var sessionRegistryInstance = newSessionRegistry()

// revokeSessionHash / revokeUserSockets are the wiring points called by the
// auth mutation handlers (logout, password change, disable, delete, role
// change) so an affected socket is closed within the same request.
func revokeSessionHash(sessionHash string) { sessionRegistryInstance.RevokeSession(sessionHash) }
func revokeUserSockets(userID string)      { sessionRegistryInstance.RevokeUser(userID) }
