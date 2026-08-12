package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════════
// Signed stream tickets
// ═══════════════════════════════════════════════════════════════════════════
//
// A media element fetches its own source. There is no hook to attach an
// Authorization header to what an <audio> or a native <video> requests, and a
// token-mode client holds no cookie, so a plain stream URL comes back 401 no
// matter what the surrounding page is authenticated as. hls.js escapes this
// through xhrSetup; nothing else does.
//
// So the URL has to carry its own authority. A ticket names one audiobook, one
// track, one user and the session it was minted from, and is sealed with an
// HMAC. It is a capability, and the usual objection to capabilities — that they
// cannot be taken back — is answered by the session: the ticket carries the hash
// of the session that minted it, and using it goes through the same
// LoadAuthenticatedUser the cookie and bearer paths do. Log out, disable the
// account, revoke the device, and every outstanding ticket dies with the
// session, on the next range request.
//
// That revocability is what buys the long TTL. A media element re-requests over
// the whole of a listening session — ranges, seeks, a resume hours later — so a
// two-minute window like the HLS locator's would break playback constantly.

const streamTicketTTL = 12 * time.Hour

// streamTicket is the sealed claim set. Field names are terse to keep the URL
// short; nothing but this file parses one.
type streamTicket struct {
	Item  string `json:"i"`
	Track int    `json:"t"` // -1 for the whole book rather than one track
	User  string `json:"u"`
	Sess  string `json:"s"` // session token hash, so revocation reaches the ticket
	Exp   int64  `json:"e"`
}

var (
	errStreamTicketInvalid = errors.New("stream ticket invalid")
	errStreamTicketExpired = errors.New("stream ticket expired")
)

// streamTicketKey derives a subkey from the HLS signing secret rather than
// adding another operator-managed file.
//
// The derivation is not decoration. Both locators are JSON objects sealed with
// the same primitive, and an hlsLocator body unmarshals cleanly into a
// streamTicket — the field sets overlap on i, u, s and e, and the absent "t"
// would simply read as track 0. Signing both with one key would therefore let an
// HLS locator be spent as an audiobook ticket. A distinct key makes the two
// families unforgeable in each other's terms.
func streamTicketKey() []byte {
	mac := hmac.New(sha256.New, hlsSigningKey)
	mac.Write([]byte("telos-stream-ticket-v1"))
	return mac.Sum(nil)
}

func signStreamTicket(t streamTicket) string {
	body, _ := json.Marshal(t)
	mac := hmac.New(sha256.New, streamTicketKey())
	mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verifyStreamTicket authenticates and de-seals a ticket. The signature is
// compared in constant time and before anything in the body is believed.
func verifyStreamTicket(token string, now time.Time) (streamTicket, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return streamTicket{}, errStreamTicketInvalid
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return streamTicket{}, errStreamTicketInvalid
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return streamTicket{}, errStreamTicketInvalid
	}
	mac := hmac.New(sha256.New, streamTicketKey())
	mac.Write(body)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return streamTicket{}, errStreamTicketInvalid
	}
	var t streamTicket
	if err := json.Unmarshal(body, &t); err != nil {
		return streamTicket{}, errStreamTicketInvalid
	}
	if t.Item == "" || t.User == "" || t.Sess == "" {
		return streamTicket{}, errStreamTicketInvalid
	}
	if now.Unix() > t.Exp {
		return streamTicket{}, errStreamTicketExpired
	}
	return t, nil
}

// handleAudiobookStreamTicket mints a ticket for a caller already authorized to
// stream the audiobook by the ordinary route.
//
// The path is built here from the caller's own identity and the item they asked
// for; the client never supplies a path or a user. That is the difference
// between signing a capability and signing whatever a client sends.
func handleAudiobookStreamTicket(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(userContextKey).(*UserContext)
	if user == nil || user.ID == "" {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Unauthorized.")
		return
	}
	if user.SessionHash == "" {
		// Nothing to bind revocation to. Minting an unrevocable 12-hour
		// capability is not a thing to do quietly.
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Unauthorized.")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_id", "Audiobook ID required.")
		return
	}

	var body struct {
		TrackIndex *int `json:"trackIndex"`
	}
	// A body is optional: no track index means the whole book.
	if r.ContentLength > 0 {
		if err := decodeJSON(w, r, &body, securityConfig.AuthJSONBytes); err != nil {
			return
		}
	}
	track := -1
	if body.TrackIndex != nil {
		if *body.TrackIndex < 0 {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_track", "Invalid track index.")
			return
		}
		track = *body.TrackIndex
	}

	// Authorize exactly as the stream route does, now, rather than trusting that
	// the ticket will be checked later — the ticket's whole purpose is to be
	// spent without this check.
	resolution, err := resolveCatalogIdentity(r.Context(), id, SurfaceLibrary)
	if err != nil || resolution.Kind != "audiobook" || !resolution.Active || !resolution.Available {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return
	}

	ticket := signStreamTicket(streamTicket{
		Item:  resolution.ID,
		Track: track,
		User:  user.ID,
		Sess:  user.SessionHash,
		Exp:   time.Now().Add(streamTicketTTL).Unix(),
	})

	writeJSON(w, map[string]interface{}{
		"url":       "/api/v1/library/audiobook-stream/" + ticket,
		"expiresIn": int(streamTicketTTL.Seconds()),
	})
}

// handleTicketedAudiobookStream serves audio to a request that carries no
// credential of its own beyond the ticket in its path.
func handleTicketedAudiobookStream(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("ticket")
	if raw == "" {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return
	}

	ticket, err := verifyStreamTicket(raw, time.Now())
	if err != nil {
		// Both cases are 401 and say nothing about which: an expired ticket and
		// a forged one are the same answer to anyone who did not mint it.
		writeAPIError(w, r, http.StatusUnauthorized, "invalid_ticket", "This link is no longer valid.")
		return
	}

	// The session the ticket was minted from has to still be live. This is what
	// makes a 12-hour capability revocable, and it is the same lookup the cookie
	// and bearer paths perform.
	if dbPool == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "The request could not be completed.")
		return
	}
	user, err := LoadAuthenticatedUser(r.Context(), dbPool, ticket.Sess)
	if err != nil || user == nil || user.ID != ticket.User {
		writeAPIError(w, r, http.StatusUnauthorized, "invalid_ticket", "This link is no longer valid.")
		return
	}
	user.SessionHash = ticket.Sess

	// Permission is re-checked at use, not assumed from mint time: a role can be
	// taken away inside the ticket's lifetime.
	allowed, err := hasPermission(r.Context(), user, "view_library", nil)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	if !allowed {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "Forbidden.")
		return
	}

	resolution, err := resolveCatalogIdentity(r.Context(), ticket.Item, SurfaceLibrary)
	if err != nil || resolution.Kind != "audiobook" || !resolution.Active || !resolution.Available {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return
	}

	r = r.WithContext(context.WithValue(r.Context(), userContextKey, user))
	if releaseSlot, acquired := acquireStreamSlot(w, r); acquired {
		defer releaseSlot()
	} else {
		return
	}

	upstreamPath := "/api/v1/audiobooks/" + resolution.UpstreamID + "/stream"
	if ticket.Track >= 0 {
		upstreamPath = "/api/v1/audiobooks/" + resolution.UpstreamID +
			"/track/" + strconv.Itoa(ticket.Track) + "/stream"
	}
	proxyGrimmoryBinary(w, r, upstreamPath, "")
}
