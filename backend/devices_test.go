package main

import (
	"testing"
	"time"
)

func TestGenerateSecureTokenEntropyAndHash(t *testing.T) {
	tok, hash, err := generateSecureTokenPair()
	if err != nil {
		t.Fatalf("generateSecureTokenPair failed: %v", err)
	}
	if len(tok) < 32 {
		t.Errorf("token too short: got %d chars", len(tok))
	}
	expectedHash := sha256Hex(tok)
	if hash != expectedHash {
		t.Errorf("hash mismatch: got %s, expected %s", hash, expectedHash)
	}
}

func TestWSTicketStoreExpiryAndSingleUse(t *testing.T) {
	store := newWSTicketStore(30 * time.Second)

	ticket, err := store.Issue("user-123", "device-456")
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}
	if ticket == "" {
		t.Fatal("expected non-empty ticket")
	}

	// Valid consumption
	userID, deviceID, err := store.Consume(ticket)
	if err != nil {
		t.Fatalf("Consume failed: %v", err)
	}
	if userID != "user-123" || deviceID != "device-456" {
		t.Fatalf("unexpected stored credentials: user=%s device=%s", userID, deviceID)
	}

	// Negative test 1: Replayed/already-consumed ticket must return error
	_, _, err = store.Consume(ticket)
	if err == nil {
		t.Fatalf("negative test failed: expected error on replayed ticket consumption")
	}

	// Negative test 2: Non-existent ticket must return error
	_, _, err = store.Consume("invalid-ticket-value")
	if err == nil {
		t.Fatalf("negative test failed: expected error on non-existent ticket")
	}

	// Negative test 3: Expired ticket must return error
	shortStore := newWSTicketStore(-1 * time.Second)
	expTicket, _ := shortStore.Issue("user-789", "device-999")
	_, _, err = shortStore.Consume(expTicket)
	if err == nil {
		t.Fatalf("negative test failed: expected error on expired ticket")
	}
}
