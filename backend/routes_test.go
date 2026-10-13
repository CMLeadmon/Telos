package main

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// publicRouteAllowlist is the complete set of API routes registered WITHOUT
// withAuth. A route here authenticates inside its handler (a password, a refresh
// token, a signed capability) or carries no private data. Adding a bare route
// must be a deliberate edit of this list in the same change.
var publicRouteAllowlist = []string{
	"GET /api/v1/health",
	"GET /api/v1/health/live",
	"GET /api/v1/health/ready",
	"POST /api/v1/auth/bootstrap",
	"POST /api/v1/auth/devices/refresh",
	"POST /api/v1/auth/devices/register",
	"POST /api/v1/auth/invites/accept",
	"POST /api/v1/auth/login",
	"GET /api/v1/library/audiobook-stream/{ticket}",
	"GET /api/v1/hls/{locator}",
	"GET /api/v1/stream/audio-ticket/{ticket}",
}

// bareRoutes parses routes.go and returns every pattern registered with
// mux.HandleFunc (never wrapped in withAuth), plus the handler identifier.
func bareRoutes(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "routes.go", nil, 0)
	if err != nil {
		t.Fatalf("parse routes.go: %v", err)
	}
	found := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "HandleFunc" {
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		pattern, _ := strconv.Unquote(literal.Value)
		if ident, ok := call.Args[1].(*ast.Ident); ok {
			found[pattern] = ident.Name
		} else {
			found[pattern] = "<expression>"
		}
		return true
	})
	return found
}

func TestPublicRoutesAreExactlyTheAuditedSet(t *testing.T) {
	got := bareRoutes(t)
	var patterns []string
	for pattern := range got {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	want := append([]string(nil), publicRouteAllowlist...)
	sort.Strings(want)
	if strings.Join(patterns, "\n") != strings.Join(want, "\n") {
		t.Fatalf("bare (non-withAuth) routes differ from the audited allowlist\n got: %v\nwant: %v", patterns, want)
	}
}

func TestDeviceRegistrationIsRegisteredBareAndHandlerOwned(t *testing.T) {
	if handler := bareRoutes(t)["POST /api/v1/auth/devices/register"]; handler != "handleRegisterDevice" {
		t.Fatalf("POST /api/v1/auth/devices/register must be registered with the bare handleRegisterDevice (it authenticates a password itself), got %q", handler)
	}
}

func productionChain(t *testing.T) http.Handler {
	t.Helper()
	cfg := testSecurityConfig(t, "production", "https://community.example.org")
	cfg.AllowedOrigins = map[string]struct{}{}
	for _, origin := range []string{"tauri://localhost", "http://tauri.localhost"} {
		normalized, err := normalizeOrigin(origin)
		if err != nil {
			t.Fatalf("normalizeOrigin(%q): %v", origin, err)
		}
		cfg.AllowedOrigins[normalized.String()] = struct{}{}
		cfg.AllowedOriginList = append(cfg.AllowedOriginList, normalized.String())
	}
	old := securityConfig
	securityConfig = cfg
	t.Cleanup(func() { securityConfig = old })
	mux := http.NewServeMux()
	registerRoutes(mux)
	return gatewayHandler(mux, cfg)
}

func registrationRequest(body, origin string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/devices/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}

func TestProductionDeviceRegistration(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()
	handler := productionChain(t)

	hash, err := hashPassword("a-valid-long-password")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	var userID string
	if err := f.DB.QueryRow(ctx,
		`INSERT INTO users (username, password_hash) VALUES ('desktopuser',$1) RETURNING id`, hash,
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	var audit bytes.Buffer
	log.SetOutput(&audit)
	previousOutput := log.Writer()
	t.Cleanup(func() { log.SetOutput(previousOutput) })

	t.Run("password exchange needs no prior session and is audited without secrets", func(t *testing.T) {
		const password = "a-valid-long-password"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, registrationRequest(
			`{"deviceName":"Desktop","platform":"linux","clientVersion":"0.1.0","username":"desktopuser","password":"`+password+`"}`,
			"tauri://localhost"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
		}
		var res struct {
			DeviceID, RefreshToken, AccessToken string
			AccessExpiresIn                     int
		}
		decoded := map[string]any{}
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode: %v", err)
		}
		res.DeviceID, _ = decoded["deviceId"].(string)
		res.RefreshToken, _ = decoded["refreshToken"].(string)
		res.AccessToken, _ = decoded["accessToken"].(string)
		if res.DeviceID == "" || res.RefreshToken == "" || res.AccessToken == "" {
			t.Fatalf("incomplete registration: %v", decoded)
		}
		var count int
		if err := f.DB.QueryRow(ctx, `SELECT COUNT(*) FROM devices WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, res.DeviceID, userID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("device row count = %d err=%v", count, err)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			// Non-preflight responses to an approved origin carry the exact origin.
			if got != "tauri://localhost" {
				t.Fatalf("ACAO = %q", got)
			}
		}
		logged := audit.String()
		if !strings.Contains(logged, "[AUDIT] Device registered") {
			t.Fatalf("no audit line: %q", logged)
		}
		for _, secret := range []string{password, res.RefreshToken, res.AccessToken} {
			if strings.Contains(logged, secret) {
				t.Fatalf("audit output leaked a secret: %q", logged)
			}
		}
	})

	t.Run("a reachable route still rejects an unauthenticated request without credentials", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, registrationRequest(`{"deviceName":"Desktop","platform":"linux","clientVersion":"0.1.0"}`, "tauri://localhost"))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		empty := httptest.NewRecorder()
		handler.ServeHTTP(empty, registrationRequest(`{}`, "tauri://localhost"))
		if empty.Code != http.StatusBadRequest {
			t.Fatalf("empty request status = %d, want 400", empty.Code)
		}
	})

	t.Run("wrong password and unknown user are indistinguishable and charged", func(t *testing.T) {
		body := func(user, password string) string {
			return `{"deviceName":"Desktop","platform":"linux","clientVersion":"0.1.0","username":"` + user + `","password":"` + password + `"}`
		}
		errorOf := func(rec *httptest.ResponseRecorder) string {
			var parsed struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
				t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
			}
			return parsed.Error.Code + "|" + parsed.Error.Message
		}
		wrong := httptest.NewRecorder()
		handler.ServeHTTP(wrong, registrationRequest(body("desktopuser", "not-the-password"), "tauri://localhost"))
		unknown := httptest.NewRecorder()
		handler.ServeHTTP(unknown, registrationRequest(body("nobody-here", "not-the-password"), "tauri://localhost"))
		if wrong.Code != http.StatusUnauthorized || unknown.Code != http.StatusUnauthorized {
			t.Fatalf("statuses = %d / %d, want 401 / 401", wrong.Code, unknown.Code)
		}
		if errorOf(wrong) != errorOf(unknown) {
			t.Fatalf("distinguishable failures:\n%s\n%s", wrong.Body.String(), unknown.Body.String())
		}
		// The per-(user, IP) cap is limitPair failures; every failure above and
		// below must have been charged, so the cap is reached and the next try
		// is throttled rather than verified.
		throttled := false
		for i := 0; i < limitPair+2; i++ {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, registrationRequest(body("desktopuser", "still-wrong"), "tauri://localhost"))
			if rec.Code == http.StatusTooManyRequests {
				throttled = true
				break
			}
		}
		if !throttled {
			t.Fatal("repeated wrong passwords were never throttled: failures are not charged")
		}
	})

	t.Run("a foreign origin is refused before the handler", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, registrationRequest(`{"deviceName":"x","platform":"x","clientVersion":"1","username":"desktopuser","password":"a-valid-long-password"}`, "https://evil.example"))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})
}
