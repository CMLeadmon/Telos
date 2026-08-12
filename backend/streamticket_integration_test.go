package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A stream ticket is a capability in a URL — the only form a media element can
// carry, since it fetches its own source and no header can be attached to that.
// The whole reason a 12-hour lifetime is acceptable is that the ticket names the
// session that minted it and is re-checked against it on every request. If that
// check does not hold, revoking a device leaves a working audio URL in someone's
// hands until the ticket expires on its own.
func TestStreamTicketDiesWithItsSession(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()
	withStreamTicketKey(t)

	var userID string
	if err := f.DB.QueryRow(ctx,
		`INSERT INTO users (username, password_hash) VALUES ('ticketstream','x') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	deviceID, _, access, err := registerDevice(ctx, userID, "Phone", "ios", "1.0.0")
	if err != nil {
		t.Fatalf("registerDevice: %v", err)
	}
	sessionHash := sha256Hex(access)

	ticket := signStreamTicket(streamTicket{
		Item:  "11111111-1111-4111-8111-111111111111",
		Track: 0,
		User:  userID,
		Sess:  sessionHash,
		Exp:   time.Now().Add(streamTicketTTL).Unix(),
	})

	// Before revocation the session behind the ticket resolves. Playback itself
	// needs Grimmory, which this fixture has no upstream for, so the assertion
	// is on the boundary the ticket exists to cross: the session lookup.
	if _, err := LoadAuthenticatedUser(ctx, f.DB, sessionHash); err != nil {
		t.Fatalf("session should be live before revocation: %v", err)
	}

	serve := func() int {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/library/audiobook-stream/"+ticket, nil)
		r.SetPathValue("ticket", ticket)
		rec := httptest.NewRecorder()
		handleTicketedAudiobookStream(rec, r)
		return rec.Code
	}

	// A live session gets past the ticket and session checks and fails later, on
	// the catalog lookup for an item this fixture never seeded — a 404, not a 401.
	if code := serve(); code == http.StatusUnauthorized {
		t.Fatal("a ticket from a live session was rejected as unauthorized")
	}

	if err := revokeDevice(ctx, deviceID); err != nil {
		t.Fatalf("revokeDevice: %v", err)
	}

	if code := serve(); code != http.StatusUnauthorized {
		t.Fatalf("after revoking the device the ticket still worked: status %d", code)
	}
}

// The ticket names a user as well as a session. They are checked against each
// other so a ticket cannot be re-pointed at another account by editing the
// claim — which the signature already prevents, but the two claims must also
// agree, or a valid ticket for one user could name another.
func TestStreamTicketRejectsUserSessionMismatch(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()
	withStreamTicketKey(t)

	var userA, userB string
	if err := f.DB.QueryRow(ctx,
		`INSERT INTO users (username, password_hash) VALUES ('ticketowner','x') RETURNING id`,
	).Scan(&userA); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := f.DB.QueryRow(ctx,
		`INSERT INTO users (username, password_hash) VALUES ('ticketother','x') RETURNING id`,
	).Scan(&userB); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	_, _, access, err := registerDevice(ctx, userA, "Phone", "ios", "1.0.0")
	if err != nil {
		t.Fatalf("registerDevice: %v", err)
	}

	// Signed properly, so only the mismatch between the claims can reject it.
	ticket := signStreamTicket(streamTicket{
		Item: "11111111-1111-4111-8111-111111111111",
		User: userB,
		Sess: sha256Hex(access),
		Exp:  time.Now().Add(streamTicketTTL).Unix(),
	})

	r := httptest.NewRequest(http.MethodGet, "/api/v1/library/audiobook-stream/"+ticket, nil)
	r.SetPathValue("ticket", ticket)
	rec := httptest.NewRecorder()
	handleTicketedAudiobookStream(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a ticket whose user and session disagree was served: status %d", rec.Code)
	}
}
