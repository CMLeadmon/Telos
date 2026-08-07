package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseRoutesExtractsMethodAndPath(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "routes.go")
	fixture := `package main

func register() {
	mux.HandleFunc("GET /api/v1/health", handleHealth)
	mux.Handle("POST /api/v1/channels/{id}/pins", withAuth(nil, "manage_messages"))
	mux.Handle("/", frontend)
}
`
	if err := os.WriteFile(src, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}

	routes, err := parseRoutes(src)
	if err != nil {
		t.Fatalf("parseRoutes: %v", err)
	}
	// The unprefixed "/" catch-all serves the frontend and is not an API route.
	if len(routes) != 2 {
		t.Fatalf("want 2 routes, got %d: %+v", len(routes), routes)
	}
	// Results are sorted by path, then method.
	if routes[0].Method != "POST" || routes[0].Path != "/api/v1/channels/{id}/pins" {
		t.Errorf("routes[0] = %+v", routes[0])
	}
	if routes[1].Method != "GET" || routes[1].Path != "/api/v1/health" {
		t.Errorf("routes[1] = %+v", routes[1])
	}
}

func TestParseRoutesCoversEveryRegistrationInMainGo(t *testing.T) {
	routes, err := parseRoutes("../../main.go")
	if err != nil {
		t.Fatalf("parseRoutes: %v", err)
	}
	// 114 mux registrations exist; exactly one is the unprefixed catch-all.
	if len(routes) != 113 {
		t.Fatalf("want 113 API routes, got %d", len(routes))
	}
}
