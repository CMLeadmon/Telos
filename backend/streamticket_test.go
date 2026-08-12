package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func withStreamTicketKey(t *testing.T) {
	t.Helper()
	old := hlsSigningKey
	hlsSigningKey = []byte("test-hls-signing-key-at-least-32-bytes-long")
	t.Cleanup(func() { hlsSigningKey = old })
}

func TestStreamTicketRoundTrip(t *testing.T) {
	withStreamTicketKey(t)
	now := time.Now()

	want := streamTicket{
		Item:  "11111111-1111-4111-8111-111111111111",
		Track: 3,
		User:  "22222222-2222-4222-8222-222222222222",
		Sess:  "deadbeef",
		Exp:   now.Add(time.Hour).Unix(),
	}
	got, err := verifyStreamTicket(signStreamTicket(want), now)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got != want {
		t.Fatalf("round trip changed the claims: %+v want %+v", got, want)
	}
}

func TestStreamTicketRejectsTamperAndExpiry(t *testing.T) {
	withStreamTicketKey(t)
	now := time.Now()

	valid := signStreamTicket(streamTicket{
		Item: "item-a", Track: 0, User: "user-a", Sess: "sess-a",
		Exp: now.Add(time.Hour).Unix(),
	})

	// A body edited to name a different item, re-encoded, keeping the old MAC.
	parts := strings.Split(valid, ".")
	body, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var claims streamTicket
	if err := json.Unmarshal(body, &claims); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	claims.Item = "item-b"
	forged, _ := json.Marshal(claims)
	tampered := base64.RawURLEncoding.EncodeToString(forged) + "." + parts[1]
	if _, err := verifyStreamTicket(tampered, now); err == nil {
		t.Fatal("a ticket edited to name a different audiobook verified")
	}

	// The same claims signed with someone else's key.
	old := hlsSigningKey
	hlsSigningKey = []byte("a-completely-different-signing-key-value")
	otherKey := signStreamTicket(claims)
	hlsSigningKey = old
	if _, err := verifyStreamTicket(otherKey, now); err == nil {
		t.Fatal("a ticket signed with another key verified")
	}

	expired := signStreamTicket(streamTicket{
		Item: "item-a", User: "user-a", Sess: "sess-a",
		Exp: now.Add(-time.Second).Unix(),
	})
	if _, err := verifyStreamTicket(expired, now); err == nil {
		t.Fatal("an expired ticket verified")
	}

	for _, junk := range []string{"", "nodot", "a.b.c", "!!!.@@@"} {
		if _, err := verifyStreamTicket(junk, now); err == nil {
			t.Fatalf("malformed ticket %q verified", junk)
		}
	}
}

// The two locator families are JSON objects sealed with the same primitive, and
// their field sets overlap on i, u, s and e — an hlsLocator body unmarshals
// cleanly into a streamTicket, with the absent "t" reading as track 0. Signed
// with one key, an HLS locator would therefore be spendable as an audiobook
// ticket for whatever item it named. The derived subkey is what prevents that,
// and nothing else would.
func TestStreamTicketAndHLSLocatorAreNotInterchangeable(t *testing.T) {
	withStreamTicketKey(t)
	now := time.Now()

	loc := hlsLocator{
		Item: "11111111-1111-4111-8111-111111111111",
		User: "22222222-2222-4222-8222-222222222222",
		Sess: "binding",
		Res:  "master.m3u8",
		Exp:  now.Add(time.Hour).Unix(),
	}
	hlsToken := signHLSLocator(loc)

	// Establish that the bodies really are compatible, so this test is about the
	// key and not about a decode that would have failed anyway.
	body, _ := base64.RawURLEncoding.DecodeString(strings.Split(hlsToken, ".")[0])
	var asTicket streamTicket
	if err := json.Unmarshal(body, &asTicket); err != nil {
		t.Fatalf("the confusion this guards against is not even possible: %v", err)
	}
	if asTicket.Item != loc.Item || asTicket.Track != 0 {
		t.Fatalf("unexpected cross-decode: %+v", asTicket)
	}

	if _, err := verifyStreamTicket(hlsToken, now); err == nil {
		t.Fatal("an HLS locator was accepted as an audiobook stream ticket")
	}

	ticketToken := signStreamTicket(streamTicket{
		Item: loc.Item, Track: 2, User: loc.User, Sess: "sess", Exp: loc.Exp,
	})
	if _, err := verifyHLSLocator(ticketToken, now); err == nil {
		t.Fatal("a stream ticket was accepted as an HLS locator")
	}
}

// A ticket with no session names nothing that can be revoked, so it would be an
// unrevocable capability for its whole lifetime.
func TestStreamTicketRequiresIdentifyingClaims(t *testing.T) {
	withStreamTicketKey(t)
	now := time.Now()
	exp := now.Add(time.Hour).Unix()

	for _, incomplete := range []streamTicket{
		{Item: "", User: "u", Sess: "s", Exp: exp},
		{Item: "i", User: "", Sess: "s", Exp: exp},
		{Item: "i", User: "u", Sess: "", Exp: exp},
	} {
		if _, err := verifyStreamTicket(signStreamTicket(incomplete), now); err == nil {
			t.Fatalf("a ticket missing an identifying claim verified: %+v", incomplete)
		}
	}
}
