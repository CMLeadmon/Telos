package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

// SecurityConfig is the single parsed source of truth for the
// Internet-facing HTTP boundary.
type SecurityConfig struct {
	Environment        string
	PublicOrigin       *url.URL
	AllowedDevOrigins  map[string]struct{}
	TrustedProxyRanges []netip.Prefix
	MaxHeaderBytes     int
	MaxInFlightHTTP    int
	AuthJSONBytes      int64
	JSONBytes          int64
	BodyReadTimeout    time.Duration
	UploadReadIdle     time.Duration
}

var securityConfig SecurityConfig

// init gives securityConfig safe non-zero defaults so handlers invoked before
// main() calls loadSecurityConfig (notably in unit tests) still enforce
// sensible body limits and deadlines rather than a zero-byte limit.
func init() {
	securityConfig = SecurityConfig{
		Environment:       "production",
		AllowedDevOrigins: map[string]struct{}{},
		MaxHeaderBytes:    16 << 10,
		MaxInFlightHTTP:   128,
		AuthJSONBytes:     16 << 10,
		JSONBytes:         64 << 10,
		BodyReadTimeout:   15 * time.Second,
		UploadReadIdle:    15 * time.Second,
	}
}

// normalizeOrigin canonicalizes scheme://host[:port] with IDNA host folding
// and an explicit effective port. Anything else (path, query, userinfo) is
// rejected.
func normalizeOrigin(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("origin must be http(s): %s", u.Scheme)
	}
	if u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("origin must be scheme://host[:port] only")
	}
	host, err := idna.Lookup.ToASCII(strings.ToLower(u.Hostname()))
	if err != nil {
		return nil, fmt.Errorf("invalid origin host: %w", err)
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return &url.URL{Scheme: u.Scheme, Host: net.JoinHostPort(host, port)}, nil
}

// sameOrigin compares two already-normalized origins exactly by scheme,
// IDNA-normalized host, and effective port.
func sameOrigin(a, b *url.URL) bool {
	return a != nil && b != nil && a.Scheme == b.Scheme && a.Host == b.Host
}

func loadSecurityConfig() (SecurityConfig, error) {
	cfg := SecurityConfig{
		Environment:     os.Getenv("TELOS_ENV"),
		MaxHeaderBytes:  16 << 10,
		MaxInFlightHTTP: 128,
		AuthJSONBytes:   16 << 10,
		JSONBytes:       64 << 10,
		BodyReadTimeout: 15 * time.Second,
		UploadReadIdle:  15 * time.Second,
	}
	if cfg.Environment == "" {
		cfg.Environment = "production"
	}

	rawOrigin := os.Getenv("TELOS_PUBLIC_ORIGIN")
	if rawOrigin != "" {
		origin, err := normalizeOrigin(rawOrigin)
		if err != nil {
			return cfg, fmt.Errorf("TELOS_PUBLIC_ORIGIN is invalid: %w", err)
		}
		if cfg.Environment != "development" && origin.Scheme != "https" {
			return cfg, errors.New("TELOS_PUBLIC_ORIGIN must be https in production")
		}
		cfg.PublicOrigin = origin
	} else if cfg.Environment != "development" {
		return cfg, errors.New("TELOS_PUBLIC_ORIGIN is not set")
	}

	cfg.AllowedDevOrigins = map[string]struct{}{}
	if cfg.Environment == "development" {
		for _, o := range strings.Split(os.Getenv("TELOS_DEV_ORIGINS"), ",") {
			o = strings.TrimSpace(o)
			if o == "" {
				continue
			}
			norm, err := normalizeOrigin(o)
			if err != nil {
				return cfg, fmt.Errorf("TELOS_DEV_ORIGINS entry is invalid: %w", err)
			}
			cfg.AllowedDevOrigins[norm.String()] = struct{}{}
		}
	}

	for _, c := range strings.Split(os.Getenv("TELOS_TRUSTED_PROXY_CIDRS"), ",") {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		p, err := netip.ParsePrefix(c)
		if err != nil {
			return cfg, fmt.Errorf("TELOS_TRUSTED_PROXY_CIDRS entry is invalid: %w", err)
		}
		cfg.TrustedProxyRanges = append(cfg.TrustedProxyRanges, p)
	}
	return cfg, nil
}

// originAllowed reports whether a browser Origin header value is acceptable
// for state-changing requests and WebSocket upgrades.
func originAllowed(cfg SecurityConfig, rawOrigin string) bool {
	if rawOrigin == "" {
		return false
	}
	norm, err := normalizeOrigin(rawOrigin)
	if err != nil {
		return false
	}
	if sameOrigin(norm, cfg.PublicOrigin) {
		return true
	}
	if cfg.Environment == "development" {
		if _, ok := cfg.AllowedDevOrigins[norm.String()]; ok {
			return true
		}
	}
	return false
}

// requireTrustedOrigin enforces the exact-origin policy on every
// state-changing request. GET/HEAD/OPTIONS pass through (they must be
// side-effect free).
func requireTrustedOrigin(next http.Handler, cfg SecurityConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			// Fall back to Referer's origin for older clients.
			if ref := r.Header.Get("Referer"); ref != "" {
				if u, err := url.Parse(ref); err == nil {
					origin = u.Scheme + "://" + u.Host
				}
			}
		}
		if !originAllowed(cfg, origin) {
			writeAPIError(w, r, http.StatusForbidden, "origin_forbidden", "Request origin is not allowed.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// admissionMiddleware bounds concurrent in-flight requests before any
// authentication, database, Redis, filesystem, or upstream work happens.
func admissionMiddleware(next http.Handler, max int) http.Handler {
	slots := make(chan struct{}, max)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			next.ServeHTTP(w, r)
		default:
			writeAPIError(w, r, http.StatusServiceUnavailable, "server_busy", "The server is at capacity; retry shortly.")
		}
	})
}

// securityHeaders applies the production response-header policy. The CSP
// connect-src carries exactly the public origin's wss endpoint — never bare
// wss:, wildcards, or reflected request origins.
func securityHeaders(next http.Handler, cfg SecurityConfig) http.Handler {
	csp := "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob:; media-src 'self' blob:; " +
		"connect-src 'self'%s; " +
		"font-src 'self'; worker-src 'self' blob:; frame-src 'self' blob:; " +
		"object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'; " +
		"upgrade-insecure-requests"
	wss := ""
	if cfg.PublicOrigin != nil && cfg.PublicOrigin.Scheme == "https" {
		wss = " wss://" + cfg.PublicOrigin.Host
	}
	rendered := fmt.Sprintf(csp, wss)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), geolocation=(), payment=(), usb=(), microphone=()")
		if cfg.Environment != "development" {
			h.Set("Content-Security-Policy", rendered)
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// devCORS allows credentialed cross-origin calls only from the finite
// development allowlist. Production emits no permissive CORS headers.
func devCORS(next http.Handler, cfg SecurityConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.Environment == "development" {
			origin := r.Header.Get("Origin")
			if origin != "" && originAllowed(cfg, origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Vary", "Origin")
				if r.Method == http.MethodOptions {
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Request body policy
// ---------------------------------------------------------------------------

// decodeJSON enforces the byte limit before any decoding, sets an absolute
// body-read deadline, decodes exactly one JSON document, and writes the
// public error itself. Callers must return on error.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	rc := http.NewResponseController(w)
	deadline := securityConfig.BodyReadTimeout
	if deadline <= 0 {
		deadline = 15 * time.Second
	}
	_ = rc.SetReadDeadline(time.Now().Add(deadline))

	body := http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(body)
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			writeAPIError(w, r, http.StatusRequestEntityTooLarge, "body_too_large", "Request body exceeds the allowed size.")
		case errors.Is(err, os.ErrDeadlineExceeded) || strings.Contains(err.Error(), "timeout"):
			writeAPIError(w, r, http.StatusRequestTimeout, "request_timeout", "Reading the request body timed out.")
		default:
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "Request body is not valid JSON.")
		}
		return err
	}
	// Exactly one document: any trailing token (even whitespace-separated
	// JSON) is rejected.
	if dec.More() {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "Request body must contain exactly one JSON document.")
		return errors.New("trailing JSON document")
	}
	if _, err := io.CopyN(io.Discard, body, 1); err != io.EOF {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "Request body must contain exactly one JSON document.")
		return errors.New("trailing request data")
	}
	return nil
}

// renewedBody renews the connection read deadline on every successful read,
// so an actively uploading client is never cut off by a fixed total-body
// deadline while a stalled one still times out.
type renewedBody struct {
	inner io.ReadCloser
	rc    *http.ResponseController
	idle  time.Duration
}

func (b *renewedBody) Read(p []byte) (int, error) {
	_ = b.rc.SetReadDeadline(time.Now().Add(b.idle))
	n, err := b.inner.Read(p)
	if n > 0 {
		_ = b.rc.SetReadDeadline(time.Now().Add(b.idle))
	}
	return n, err
}

func (b *renewedBody) Close() error { return b.inner.Close() }

// withRenewedBodyReadDeadline wraps the request body for streaming uploads
// (Phase 4 multipart) with a renewable idle-read deadline.
func withRenewedBodyReadDeadline(w http.ResponseWriter, r *http.Request, idle time.Duration) (io.ReadCloser, error) {
	if idle <= 0 {
		return nil, errors.New("idle deadline must be positive")
	}
	rc := http.NewResponseController(w)
	if err := rc.SetReadDeadline(time.Now().Add(idle)); err != nil {
		return nil, err
	}
	return &renewedBody{inner: r.Body, rc: rc, idle: idle}, nil
}

// ---------------------------------------------------------------------------
// Client address trust
// ---------------------------------------------------------------------------

func addrInPrefixes(addr netip.Addr, prefixes []netip.Prefix) bool {
	addr = addr.Unmap()
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// clientIP returns the real client address. Forwarding headers are honored
// only when the direct peer is inside the trusted (Traefik) ranges; the
// rightmost non-trusted X-Forwarded-For entry wins.
func clientIP(r *http.Request, trusted []netip.Prefix) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("unparseable peer address")
	}
	peer = peer.Unmap()
	if !addrInPrefixes(peer, trusted) {
		return peer, nil
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return peer, nil
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			return netip.Addr{}, fmt.Errorf("malformed forwarding header")
		}
		candidate = candidate.Unmap()
		if !addrInPrefixes(candidate, trusted) {
			return candidate, nil
		}
	}
	return peer, nil
}

// ---------------------------------------------------------------------------
// SSRF-safe public URL dialing
// ---------------------------------------------------------------------------

// lookupIPAddr is swappable for tests (DNS rebinding/mixed-answer cases).
var lookupIPAddr = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

var disallowedRanges = func() []netip.Prefix {
	raw := []string{
		"0.0.0.0/8",       // "this network"
		"127.0.0.0/8",     // loopback
		"10.0.0.0/8",      // private
		"172.16.0.0/12",   // private
		"192.168.0.0/16",  // private
		"169.254.0.0/16",  // link-local
		"100.64.0.0/10",   // CGNAT
		"224.0.0.0/4",     // multicast
		"240.0.0.0/4",     // reserved
		"::/128",          // unspecified
		"::1/128",         // loopback
		"fc00::/7",        // unique-local
		"fe80::/10",       // link-local
		"ff00::/8",        // multicast
		"64:ff9b::/96",    // NAT64 of anything — validate the mapped form
		"2001:db8::/32",   // documentation
	}
	out := make([]netip.Prefix, 0, len(raw))
	for _, c := range raw {
		out = append(out, netip.MustParsePrefix(c))
	}
	return out
}()

func publicAddrAllowed(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsUnspecified() {
		return false
	}
	return !addrInPrefixes(addr, disallowedRanges)
}

// publicURLIPs resolves a URL host once and rejects the entire answer set if
// any record is disallowed — mixed answers are treated as hostile.
func publicURLIPs(ctx context.Context, rawURL *url.URL) ([]netip.Addr, error) {
	if rawURL == nil {
		return nil, errors.New("nil URL")
	}
	if rawURL.Scheme != "http" && rawURL.Scheme != "https" {
		return nil, fmt.Errorf("disallowed scheme %q", rawURL.Scheme)
	}
	host := rawURL.Hostname()
	if host == "" {
		return nil, errors.New("URL has no host")
	}
	if literal, err := netip.ParseAddr(host); err == nil {
		if !publicAddrAllowed(literal) {
			return nil, fmt.Errorf("address is not publicly routable")
		}
		return []netip.Addr{literal.Unmap()}, nil
	}
	records, err := lookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolution failed")
	}
	if len(records) == 0 {
		return nil, errors.New("no addresses resolved")
	}
	addrs := make([]netip.Addr, 0, len(records))
	for _, rec := range records {
		addr, ok := netip.AddrFromSlice(rec.IP)
		if !ok {
			return nil, errors.New("unparseable resolved address")
		}
		if !publicAddrAllowed(addr) {
			return nil, fmt.Errorf("resolved address set contains a non-public address")
		}
		addrs = append(addrs, addr.Unmap())
	}
	return addrs, nil
}

// dialValidatedPublic dials exactly one pre-validated address, preserving the
// original host for SNI/verification so the transport can never re-resolve.
func dialValidatedPublic(ctx context.Context, network, address, serverName string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("address must be host:port")
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return nil, errors.New("address must be a validated IP literal")
	}
	if !publicAddrAllowed(addr) {
		return nil, errors.New("address is not publicly routable")
	}
	d := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, network, net.JoinHostPort(addr.Unmap().String(), port))
	if err != nil {
		return nil, err
	}
	if serverName != "" {
		tconn := tls.Client(conn, &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12})
		if err := tconn.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, err
		}
		return tconn, nil
	}
	return conn, nil
}

// redactedURL strips query strings and userinfo for request logging.
func redactedURL(u *url.URL) string {
	c := *u
	c.RawQuery = ""
	c.User = nil
	if u.RawQuery != "" {
		return c.String() + "?[redacted]"
	}
	return c.String()
}
