package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════════
// setup-proxy: the loopback bridge for first-run configuration
// ═══════════════════════════════════════════════════════════════════════════
//
// Jellyfin and Grimmory live on internal networks (no route off the node), and a
// container attached only to an internal network cannot have a published port: the
// runtime holds the host port but installs no forwarding. So the guided-setup overlay
// does not publish those two services at all. It runs this tiny relay instead, on an
// ordinary network, publishing 127.0.0.1 only, and forwarding to exactly the two
// upstream administration UIs.
//
// It is a relay, not a proxy: it never inspects, rewrites or logs payloads, accepts
// only the two fixed target pairs below (so it cannot be turned into a general
// relay), bounds concurrency and per-connection lifetime, and exits on SIGTERM. The
// CLI removes it with the rest of the setup containers and verifies the loopback
// ports are closed.

// setupProxyTargets are the only (listen port -> upstream) pairs the relay will serve.
var setupProxyTargets = map[string]string{
	"8096": "jellyfin:8096",
	"6060": "grimmory:6060",
}

const (
	setupProxyMaxConnections = 64
	setupProxyMaxLifetime    = 6 * time.Hour
	setupProxyDialTimeout    = 5 * time.Second
)

type setupProxyMapping struct{ listen, target string }

// parseSetupProxyMaps accepts "--map ADDR:PORT=HOST:PORT" pairs and rejects anything
// but the fixed targets, a duplicate listener, or a non-wildcard/loopback listen host.
func parseSetupProxyMaps(args []string) ([]setupProxyMapping, error) {
	var out []setupProxyMapping
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		if args[i] != "--map" || i+1 >= len(args) {
			return nil, fmt.Errorf("usage: setup-proxy --map LISTEN=TARGET [--map ...] (unexpected %q)", args[i])
		}
		i++
		listen, target, ok := strings.Cut(args[i], "=")
		if !ok {
			return nil, fmt.Errorf("--map %q is not LISTEN=TARGET", args[i])
		}
		host, port, err := net.SplitHostPort(listen)
		if err != nil {
			return nil, fmt.Errorf("--map listen address %q: %w", listen, err)
		}
		if host != "0.0.0.0" && host != "127.0.0.1" {
			return nil, fmt.Errorf("--map listen host %q must be 0.0.0.0 (inside the container) or 127.0.0.1", host)
		}
		want, allowed := setupProxyTargets[port]
		if !allowed || target != want {
			return nil, fmt.Errorf("--map %q is not one of the fixed setup relays", args[i])
		}
		if seen[port] {
			return nil, fmt.Errorf("port %s is mapped twice", port)
		}
		seen[port] = true
		out = append(out, setupProxyMapping{listen: listen, target: target})
	}
	if len(out) == 0 {
		return nil, errors.New("setup-proxy needs at least one --map")
	}
	return out, nil
}

// relay copies bytes both ways until either side closes, then closes both.
func relay(client, upstream net.Conn) {
	defer client.Close()
	defer upstream.Close()
	deadline := time.Now().Add(setupProxyMaxLifetime)
	_ = client.SetDeadline(deadline)
	_ = upstream.SetDeadline(deadline)
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if tcp, ok := dst.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		done <- struct{}{}
	}
	go pipe(upstream, client)
	go pipe(client, upstream)
	<-done
	<-done
}

func serveSetupProxy(ctx context.Context, listener net.Listener, target string, slots chan struct{}) {
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		client, err := listener.Accept()
		if err != nil {
			return
		}
		select {
		case slots <- struct{}{}:
		default:
			_ = client.Close() // over the connection bound
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			upstream, err := net.DialTimeout("tcp", target, setupProxyDialTimeout)
			if err != nil {
				_ = client.Close()
				return
			}
			relay(client, upstream)
		}()
	}
}

// runSetupProxyCommand serves until SIGTERM/SIGINT. Exit codes: 0 clean, 2 usage.
func runSetupProxyCommand(args []string) int {
	maps, err := parseSetupProxyMaps(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "setup-proxy:", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	slots := make(chan struct{}, setupProxyMaxConnections)
	var wg sync.WaitGroup
	for _, m := range maps {
		listener, err := net.Listen("tcp", m.listen)
		if err != nil {
			fmt.Fprintln(os.Stderr, "setup-proxy:", err)
			return 1
		}
		log.Printf("setup-proxy: relaying %s -> %s", m.listen, m.target)
		wg.Add(1)
		go func() {
			defer wg.Done()
			serveSetupProxy(ctx, listener, m.target, slots)
		}()
	}
	wg.Wait()
	return 0
}
