package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════════
// Library — Grimmory client (JWT auth)
// ═══════════════════════════════════════════════════════════════════════════

// Grimmory (BookLore) does not accept static API tokens; the gateway logs in
// with admin credentials and holds a short-lived JWT (expires: 7200s).
var grimmoryBaseURL = "http://grimmory:6060"

var grimmoryTok struct {
	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func grimmoryLogin(ctx context.Context) (string, time.Duration, error) {
	payload, _ := json.Marshal(map[string]string{
		"username": os.Getenv("GRIMMORY_ADMIN_USER"),
		"password": os.Getenv("GRIMMORY_ADMIN_PASSWORD"),
	})
	req, err := http.NewRequestWithContext(ctx, "POST",
		grimmoryBaseURL+"/api/v1/auth/login", bytes.NewReader(payload))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("grimmory login returned %s", resp.Status)
	}
	var body struct {
		AccessToken string `json:"accessToken"`
		Expires     int    `json:"expires"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", 0, err
	}
	if body.AccessToken == "" {
		return "", 0, fmt.Errorf("grimmory login returned empty token")
	}
	return body.AccessToken, time.Duration(body.Expires) * time.Second, nil
}

func getGrimmoryToken(ctx context.Context) (string, error) {
	grimmoryTok.mu.Lock()
	defer grimmoryTok.mu.Unlock()
	// 60s slack so a token never expires mid-request.
	if grimmoryTok.token != "" && time.Now().Before(grimmoryTok.expiresAt.Add(-60*time.Second)) {
		return grimmoryTok.token, nil
	}
	tok, ttl, err := grimmoryLogin(ctx)
	if err != nil {
		return "", err
	}
	grimmoryTok.token = tok
	grimmoryTok.expiresAt = time.Now().Add(ttl)
	return tok, nil
}

// grimmoryGET performs an authenticated GET, re-logging-in once on 401
// (token revoked server-side, e.g. after a Grimmory restart).
func grimmoryGET(ctx context.Context, path string) (*http.Response, error) {
	do := func(tok string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", grimmoryBaseURL+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		return http.DefaultClient.Do(req)
	}
	tok, err := getGrimmoryToken(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := do(tok)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		grimmoryTok.mu.Lock()
		grimmoryTok.token = ""
		grimmoryTok.mu.Unlock()
		if tok, err = getGrimmoryToken(ctx); err != nil {
			return nil, err
		}
		return do(tok)
	}
	return resp, err
}

// ═══════════════════════════════════════════════════════════════════════════
// Library — books catalog (translated from Grimmory)
// ═══════════════════════════════════════════════════════════════════════════

type LibraryBook struct {
	ID         int64    `json:"id"`
	Title      string   `json:"title"`
	Authors    []string `json:"authors"`
	Categories []string `json:"categories"`
	Language   string   `json:"language"`
	Format     string   `json:"format"` // primaryFile.bookType: "EPUB" | "PDF"
	FileSizeKB int64    `json:"fileSizeKb"`
	AddedOn    string   `json:"addedOn"`
	Library    string   `json:"library"`
}

type FacetValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type LibraryFacets struct {
	Authors    []FacetValue `json:"authors"`
	Categories []FacetValue `json:"categories"`
	Languages  []FacetValue `json:"languages"`
	Formats    []FacetValue `json:"formats"`
}

// grimmoryBook is the verified upstream shape (BookLore GET /api/v1/books).
type grimmoryBook struct {
	ID          int64  `json:"id"`
	AddedOn     string `json:"addedOn"`
	LibraryName string `json:"libraryName"`
	Metadata    struct {
		Title      string   `json:"title"`
		Language   string   `json:"language"`
		Authors    []string `json:"authors"`
		Categories []string `json:"categories"`
	} `json:"metadata"`
	PrimaryFile struct {
		BookType   string `json:"bookType"`
		FileSizeKB int64  `json:"fileSizeKb"`
	} `json:"primaryFile"`
}

func fetchGrimmoryBooks(ctx context.Context) ([]LibraryBook, error) {
	resp, err := grimmoryGET(ctx, "/api/v1/books")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("grimmory books returned %s", resp.Status)
	}
	var raw []grimmoryBook
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	books := make([]LibraryBook, 0, len(raw))
	for _, b := range raw {
		books = append(books, LibraryBook{
			ID: b.ID, Title: b.Metadata.Title, Authors: b.Metadata.Authors,
			Categories: b.Metadata.Categories, Language: b.Metadata.Language,
			Format: b.PrimaryFile.BookType, FileSizeKB: b.PrimaryFile.FileSizeKB,
			AddedOn: b.AddedOn, Library: b.LibraryName,
		})
	}
	return books, nil
}

var mockLibraryBooks = []LibraryBook{
	{ID: 1, Title: "The Sovereign Stack (Mock)", Authors: []string{"Telos Docs Team (Mock)"},
		Categories: []string{"Technology"}, Language: "en", Format: "EPUB", FileSizeKB: 1024,
		AddedOn: "2026-01-01T00:00:00Z", Library: "Books (Mock)"},
	{ID: 2, Title: "Single Origin (Mock)", Authors: []string{"Gateway Author (Mock)"},
		Categories: []string{"Fiction"}, Language: "en", Format: "PDF", FileSizeKB: 2048,
		AddedOn: "2026-01-02T00:00:00Z", Library: "Books (Mock)"},
}

// getLibraryBooks serves from Redis (60s), then Grimmory, then mocks.
func getLibraryBooks(ctx context.Context) []LibraryBook {
	const cacheKey = "telos:grimmory:books"
	if redisClient != nil {
		if val, err := redisClient.Get(ctx, cacheKey).Result(); err == nil && val != "" {
			var books []LibraryBook
			if json.Unmarshal([]byte(val), &books) == nil {
				return books
			}
		}
	}
	books, err := fetchGrimmoryBooks(ctx)
	if err != nil {
		log.Printf("WARN: grimmory books unavailable, serving mock data: %v", err)
		return mockLibraryBooks
	}
	if redisClient != nil {
		if raw, err := json.Marshal(books); err == nil {
			_ = redisClient.Set(ctx, cacheKey, string(raw), 60*time.Second).Err()
		}
	}
	return books
}

func handleLibraryBooks(w http.ResponseWriter, r *http.Request) {
	books := getLibraryBooks(r.Context())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(books)
}

// Grimmory's /api/v1/books/facets endpoint does not exist in the deployed
// build (500s); facets are derived here from the book list instead.
func handleLibraryFacets(w http.ResponseWriter, r *http.Request) {
	books := getLibraryBooks(r.Context())
	count := func(pick func(LibraryBook) []string) []FacetValue {
		m := map[string]int{}
		for _, b := range books {
			for _, v := range pick(b) {
				if v != "" {
					m[v]++
				}
			}
		}
		out := make([]FacetValue, 0, len(m))
		for v, c := range m {
			out = append(out, FacetValue{Value: v, Count: c})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Count != out[j].Count {
				return out[i].Count > out[j].Count
			}
			return out[i].Value < out[j].Value
		})
		return out
	}
	facets := LibraryFacets{
		Authors:    count(func(b LibraryBook) []string { return b.Authors }),
		Categories: count(func(b LibraryBook) []string { return b.Categories }),
		Languages:  count(func(b LibraryBook) []string { return []string{b.Language} }),
		Formats:    count(func(b LibraryBook) []string { return []string{b.Format} }),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(facets)
}

// ═══════════════════════════════════════════════════════════════════════════
// Library — cover & content proxies
// ═══════════════════════════════════════════════════════════════════════════

func proxyGrimmoryBinary(w http.ResponseWriter, r *http.Request, path, forceContentType string) {
	resp, err := grimmoryGET(r.Context(), path)
	if err != nil {
		http.Error(w, "Grimmory unreachable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "Grimmory returned "+resp.Status, http.StatusBadGateway)
		return
	}
	ct := resp.Header.Get("Content-Type")
	if forceContentType != "" {
		ct = forceContentType
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=300")
	io.Copy(w, resp.Body)
}

func libraryBookID(r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if id == "" {
		return "", false
	}
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return "", false
	}
	return id, true
}

func handleLibraryBookCover(w http.ResponseWriter, r *http.Request) {
	id, ok := libraryBookID(r)
	if !ok {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}
	// Upstream serves JPEG bytes with a JSON content-type — force the real one.
	proxyGrimmoryBinary(w, r, "/api/v1/media/book/"+id+"/thumbnail", "image/jpeg")
}

func handleLibraryBookContent(w http.ResponseWriter, r *http.Request) {
	id, ok := libraryBookID(r)
	if !ok {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}
	proxyGrimmoryBinary(w, r, "/api/v1/books/"+id+"/content", "")
}

// ═══════════════════════════════════════════════════════════════════════════
// Library — per-user reading progress (Telos Postgres, not Grimmory: the
// gateway is a single admin account upstream, so progress must be local)
// ═══════════════════════════════════════════════════════════════════════════

func validateProgress(raw []byte) ([]byte, float64, error) {
	if len(raw) > 4096 {
		return nil, 0, errors.New("progress payload too large")
	}
	var body struct {
		Locator json.RawMessage `json:"locator"`
		Percent float64         `json:"percent"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, 0, errors.New("invalid JSON")
	}
	if body.Percent < 0 || body.Percent > 1 {
		return nil, 0, errors.New("percent must be between 0 and 1")
	}
	locator := []byte("{}")
	if len(body.Locator) > 0 {
		var probe map[string]any
		if err := json.Unmarshal(body.Locator, &probe); err != nil {
			return nil, 0, errors.New("locator must be a JSON object")
		}
		if len(body.Locator) > 2048 {
			return nil, 0, errors.New("locator too large")
		}
		locator = body.Locator
	}
	return locator, body.Percent, nil
}

func handleGetBookProgress(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	id, ok := libraryBookID(r)
	if !ok {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}
	var locator []byte
	var percent float64
	var updated time.Time
	err := dbPool.QueryRow(r.Context(), `
		SELECT locator, percent, updated_at FROM book_progress
		WHERE user_id = $1 AND book_id = $2
	`, user.ID, id).Scan(&locator, &percent, &updated)
	w.Header().Set("Content-Type", "application/json")
	if err != nil { // no row yet — zero progress
		w.Write([]byte(`{"locator":{},"percent":0,"updatedAt":null}`))
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"locator": json.RawMessage(locator), "percent": percent,
		"updatedAt": updated.Format(time.RFC3339),
	})
}

func handlePutBookProgress(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	id, ok := libraryBookID(r)
	if !ok {
		http.Error(w, "Invalid book id", http.StatusBadRequest)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	locator, percent, err := validateProgress(raw)
	if err != nil {
		http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := dbPool.Exec(r.Context(), `
		INSERT INTO book_progress (user_id, book_id, locator, percent, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (user_id, book_id)
		DO UPDATE SET locator = EXCLUDED.locator, percent = EXCLUDED.percent, updated_at = NOW()
	`, user.ID, id, locator, percent); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}
