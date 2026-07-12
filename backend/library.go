package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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
