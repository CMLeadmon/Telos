package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

// Staged shutdown budget (S08): stop admission, drain normal HTTP, close
// WebSockets, drain outbox, then close upstream bodies/pools.
const (
	httpDrainDeadline   = 30 * time.Second
	socketDrainDeadline = 10 * time.Second
	outboxDrainDeadline = 15 * time.Second
)

// OutboxDrainer flushes pending transactional-outbox work during shutdown.
// Phase 3 supplies the real implementation; until then a no-op adapter is used.
type OutboxDrainer interface {
	Drain(ctx context.Context) error
}

type noopOutboxDrainer struct{}

func (noopOutboxDrainer) Drain(context.Context) error { return nil }

var outboxDrainer OutboxDrainer = noopOutboxDrainer{}

// shuttingDown flips true once a signal arrives so admission can reject new
// upgrades and uploads with 503 shutting_down.
var shuttingDown atomic.Bool

// shuttingDownMiddleware rejects new work once graceful shutdown has begun.
func shuttingDownMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if shuttingDown.Load() {
			writeAPIError(w, r, http.StatusServiceUnavailable, "shutting_down", "The server is shutting down; retry shortly.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// runServer starts the HTTP server and performs a staged graceful shutdown on
// SIGINT/SIGTERM. It returns a nonzero-style error only if a stage exceeds its
// deadline; callers exit accordingly.
func runServer(server *http.Server) {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("Server listening on %s", server.Addr)
		serveErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
		return
	case sig := <-stop:
		log.Printf("shutdown: received %s, draining", sig)
	}

	// 1. Stop admitting new work.
	shuttingDown.Store(true)
	overran := false

	// 2. Drain in-flight normal HTTP.
	httpCtx, cancel := context.WithTimeout(context.Background(), httpDrainDeadline)
	if err := server.Shutdown(httpCtx); err != nil {
		log.Printf("shutdown: HTTP drain exceeded deadline: %v", err)
		overran = true
	}
	cancel()

	// 3. Close WebSockets.
	socketDone := make(chan struct{})
	go func() {
		sessionRegistryInstance.CloseAll("server shutting down")
		close(socketDone)
	}()
	select {
	case <-socketDone:
	case <-time.After(socketDrainDeadline):
		log.Printf("shutdown: socket drain exceeded deadline")
		overran = true
	}

	// 4. Drain the transactional outbox.
	outboxCtx, cancelOutbox := context.WithTimeout(context.Background(), outboxDrainDeadline)
	if err := outboxDrainer.Drain(outboxCtx); err != nil {
		log.Printf("shutdown: outbox drain error: %v", err)
		overran = true
	}
	cancelOutbox()

	// 5. Upstream bodies/pools are closed by main's deferred dbPool/redis
	// Close on return.

	if overran {
		log.Printf("shutdown: completed with at least one stage over deadline")
		os.Exit(1)
	}
	log.Printf("shutdown: clean")
}
