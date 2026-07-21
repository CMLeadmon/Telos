package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// decodeJWTPayload returns the middle segment of a JWT as a generic map.
func decodeJWTPayload(t *testing.T, jwt string) map[string]any {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", jwt)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return m
}

func TestVoiceGrantIsMicrophoneOnly(t *testing.T) {
	jwt, err := mintVoiceToken("APIkey", "secretsecretsecretsecret", "room-1", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	payload := decodeJWTPayload(t, jwt)
	video, ok := payload["video"].(map[string]any)
	if !ok {
		t.Fatalf("no video grant in %v", payload)
	}

	if video["roomJoin"] != true {
		t.Error("roomJoin not granted")
	}
	if video["canSubscribe"] != true {
		t.Error("canSubscribe not granted")
	}
	// Publication is restricted to the microphone source.
	sources, ok := video["canPublishSources"].([]any)
	if !ok || len(sources) != 1 || sources[0] != "microphone" {
		t.Fatalf("canPublishSources = %v, want [microphone]", video["canPublishSources"])
	}
	// Privileged / disallowed capabilities must be absent or false.
	for _, k := range []string{"canPublishData", "roomAdmin", "roomCreate", "roomList", "recorder", "hidden", "ingressAdmin"} {
		if v, present := video[k]; present && v == true {
			t.Errorf("grant unexpectedly allows %s", k)
		}
	}
	// No wildcard room.
	if video["room"] != "room-1" {
		t.Errorf("room = %v, want room-1", video["room"])
	}
	// ~90-second validity (LiveKit sets nbf/exp around now).
	start, _ := payload["nbf"].(float64)
	if start == 0 {
		start, _ = payload["iat"].(float64)
	}
	exp, _ := payload["exp"].(float64)
	if d := exp - start; d < 80 || d > 120 {
		t.Errorf("token validity = %.0fs, want ~90s", d)
	}
}

func TestVoiceSeatConcurrentCapAt25(t *testing.T) {
	f := withFixture(t)
	store := newVoiceSeatStore(f.Redis)
	ctx := context.Background()

	const n = 100
	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			// Distinct users so the per-user rule doesn't mask the global cap.
			if _, err := store.Reserve(ctx, "user-"+itoa(i), "room-a"); err == nil {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if granted > maxVoiceParticipants {
		t.Fatalf("granted %d seats, cap is %d", granted, maxVoiceParticipants)
	}
	if granted == 0 {
		t.Fatal("no seats granted")
	}
}

func TestVoiceSeatOneRoomPerUser(t *testing.T) {
	f := withFixture(t)
	store := newVoiceSeatStore(f.Redis)
	ctx := context.Background()

	if _, err := store.Reserve(ctx, "u1", "room-a"); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	// Re-reserving (same user) is allowed and does not consume a second seat.
	if _, err := store.Reserve(ctx, "u1", "room-b"); err != nil {
		t.Fatalf("re-reserve: %v", err)
	}
	n, err := f.Redis.ZCard(ctx, voiceSeatsKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("user holds %d seats, want 1", n)
	}
}

func TestVoiceSeatReconcilePrunesStale(t *testing.T) {
	f := withFixture(t)
	store := newVoiceSeatStore(f.Redis)
	ctx := context.Background()

	store.Reserve(ctx, "u1", "room-a")
	store.Reserve(ctx, "u2", "room-a")
	// u2 is no longer active per the authoritative list.
	if err := store.Reconcile(ctx, []string{"u1"}); err != nil {
		t.Fatal(err)
	}
	members, _ := f.Redis.ZRange(ctx, voiceSeatsKey, 0, -1).Result()
	if len(members) != 1 || members[0] != "u1" {
		t.Fatalf("after reconcile seats = %v, want [u1]", members)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
