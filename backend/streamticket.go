package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════════
// Signed stream tickets
// ═══════════════════════════════════════════════════════════════════════════
//
// A media element fetches its own source, so the URL has to carry its own
// authority (see media_authority.go for the model). Two ticket families live
// here, each with its own key and type claim:
//
//   - grimmory-audiobook: one audiobook, one track or the whole book;
//   - jellyfin-audio: one Jellyfin audio item.
//
// Both name the stable AUTHORITY that minted them -- a browser session or a
// device, never a rotating access token -- and are re-checked against it on every
// range request, so logout, a revoked device, a disabled account or a removed role
// ends playback at the next request. That revocability is what buys the 12-hour
// lifetime: a media element re-requests over a whole listening session (ranges,
// seeks, a resume hours later).

const streamTicketTTL = mediaCapabilityTTL

// streamTicket is the sealed audiobook claim set. Field names are terse to keep
// the URL short; nothing but this file parses one.
type streamTicket struct {
	Type  string         `json:"y"`
	Item  string         `json:"i"`
	Track int            `json:"t"` // -1 for the whole book rather than one track
	User  string         `json:"u"`
	Auth  mediaAuthority `json:"a"`
	Exp   int64          `json:"e"`
}

// audioTicket is the sealed direct Jellyfin audio claim set.
type audioTicket struct {
	Type string         `json:"y"`
	Item string         `json:"i"`
	User string         `json:"u"`
	Auth mediaAuthority `json:"a"`
	Exp  int64          `json:"e"`
}

var (
	errStreamTicketInvalid = errors.New("stream ticket invalid")
	errStreamTicketExpired = errors.New("stream ticket expired")
)

func signStreamTicket(t streamTicket) string {
	t.Type = typeGrimmoryAudiob
	return sealClaims(labelStreamTicket, t)
}

// verifyStreamTicket authenticates and de-seals an audiobook ticket. The
// signature is compared in constant time and before anything in the body is
// believed; the type claim keeps every other family out even if a key ever leaked.
func verifyStreamTicket(token string, now time.Time) (streamTicket, error) {
	var t streamTicket
	if !openClaims(labelStreamTicket, token, &t) {
		return streamTicket{}, errStreamTicketInvalid
	}
	if t.Type != typeGrimmoryAudiob || t.Item == "" || t.User == "" || t.Auth.Kind == "" || t.Auth.ID == "" {
		return streamTicket{}, errStreamTicketInvalid
	}
	if now.Unix() > t.Exp {
		return streamTicket{}, errStreamTicketExpired
	}
	return t, nil
}

func signAudioTicket(t audioTicket) string {
	t.Type = typeJellyfinAudio
	return sealClaims(labelJellyfinAudio, t)
}

func verifyAudioTicket(token string, now time.Time) (audioTicket, error) {
	var t audioTicket
	if !openClaims(labelJellyfinAudio, token, &t) {
		return audioTicket{}, errStreamTicketInvalid
	}
	if t.Type != typeJellyfinAudio || t.Item == "" || t.User == "" || t.Auth.Kind == "" || t.Auth.ID == "" {
		return audioTicket{}, errStreamTicketInvalid
	}
	if now.Unix() > t.Exp {
		return audioTicket{}, errStreamTicketExpired
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
	// Nothing to bind revocation to means no ticket: minting an unrevocable
	// 12-hour capability is not a thing to do quietly.
	authority, err := authorityFromUser(user)
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Unauthorized.")
		return
	}
	now := time.Now()
	_, authorityEnd, err := loadAuthorizedMediaUser(r.Context(), authority, user.ID, "view_library", now)
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Unauthorized.")
		return
	}
	exp, err := capabilityExpiry(now, authorityEnd)
	if err != nil {
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
		Auth:  authority,
		Exp:   exp.Unix(),
	})

	w = withCapabilityHeaders(w)
	writeJSON(w, map[string]interface{}{
		"url":       "/api/v1/library/audiobook-stream/" + ticket,
		"expiresIn": int(exp.Sub(now).Seconds()),
	})
}

// handleTicketedAudiobookStream serves audio to a request that carries no
// credential of its own beyond the ticket in its path.
func handleTicketedAudiobookStream(w http.ResponseWriter, r *http.Request) {
	w = withCapabilityHeaders(w)
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

	// The authority that minted the ticket has to still be live, the account
	// active and the permission still held -- re-checked at use, not assumed from
	// mint time. This is what makes a 12-hour capability revocable. Every failure
	// is the same answer.
	user, _, err := loadAuthorizedMediaUser(r.Context(), ticket.Auth, ticket.User, "view_library", time.Now())
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "invalid_ticket", "This link is no longer valid.")
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

// handleMintAudioTicket mints a direct-stream ticket for a Jellyfin audio item.
// The caller is authenticated and holds view_media (the route's gate); the item
// is resolved and authorized now, because the ticket exists to be spent without
// that check being repeated by the client.
func handleMintAudioTicket(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(userContextKey).(*UserContext)
	authority, err := authorityFromUser(user)
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Unauthorized.")
		return
	}
	rawID := r.PathValue("id")
	if !validJellyfinID(rawID) {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_id", "Item ID required.")
		return
	}
	resolution, ok := resolveJellyfinItemHTTP(w, r, rawID)
	if !ok {
		return
	}
	now := time.Now()
	_, authorityEnd, err := loadAuthorizedMediaUser(r.Context(), authority, user.ID, "view_media", now)
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Unauthorized.")
		return
	}
	exp, err := capabilityExpiry(now, authorityEnd)
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Unauthorized.")
		return
	}
	ticket := signAudioTicket(audioTicket{Item: resolution.UpstreamID, User: user.ID, Auth: authority, Exp: exp.Unix()})
	w = withCapabilityHeaders(w)
	writeJSON(w, map[string]interface{}{
		"url":       "/api/v1/stream/audio-ticket/" + ticket,
		"expiresIn": int(exp.Sub(now).Seconds()),
	})
}

// handleTicketedJellyfinAudio serves audio to a request that carries no
// credential of its own beyond the ticket in its path. Range and HEAD requests
// work as the media proxy supports them; every request re-checks the minting
// authority and the item before any upstream work.
func handleTicketedJellyfinAudio(w http.ResponseWriter, r *http.Request) {
	w = withCapabilityHeaders(w)
	ticket, err := verifyAudioTicket(r.PathValue("ticket"), time.Now())
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "invalid_ticket", "This link is no longer valid.")
		return
	}
	user, _, err := loadAuthorizedMediaUser(r.Context(), ticket.Auth, ticket.User, "view_media", time.Now())
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "invalid_ticket", "This link is no longer valid.")
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), userContextKey, user))
	if !validJellyfinID(ticket.Item) {
		writeAPIError(w, r, http.StatusUnauthorized, "invalid_ticket", "This link is no longer valid.")
		return
	}
	resolution, ok := resolveJellyfinItemHTTP(w, r, ticket.Item)
	if !ok || resolution.UpstreamID != ticket.Item {
		return
	}
	release, ok := acquireStreamSlot(w, r)
	if !ok {
		return
	}
	defer release()
	target := fmt.Sprintf("%s/Audio/%s/stream?static=true", jellyfinBaseURL, resolution.UpstreamID)
	proxyRequest(w, r, target, getJellyfinAdminToken(), nil)
}

// handleMintPlaybackLocator negotiates playback with Jellyfin and returns the
// first locator of an HLS session: a bare URL a native <video> or hls.js can load
// with no credential. It names the negotiated manifest resource (never the
// caller's choice of path), the caller's stable authority, and an expiry no later
// than that authority's.
func handleMintPlaybackLocator(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(userContextKey).(*UserContext)
	authority, err := authorityFromUser(user)
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Unauthorized.")
		return
	}
	rawID := r.PathValue("id")
	if !validJellyfinID(rawID) {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_id", "Item ID required.")
		return
	}
	resolution, ok := resolveJellyfinItemHTTP(w, r, rawID)
	if !ok {
		return
	}
	now := time.Now()
	_, authorityEnd, err := loadAuthorizedMediaUser(r.Context(), authority, user.ID, "view_media", now)
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Unauthorized.")
		return
	}
	exp, err := capabilityExpiry(now, authorityEnd)
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Unauthorized.")
		return
	}
	playSession, status, err := negotiatePlaybackSession(r.Context(), resolution.UpstreamID)
	if err != nil {
		code := "upstream_unavailable"
		if status == http.StatusInternalServerError {
			code = "internal_error"
		}
		writeAPIError(w, r, status, code, "Playback could not be prepared.")
		return
	}
	locator := mintHLSLocatorURL(resolution.UpstreamID, user.ID, authority, "main.m3u8?PlaySessionId="+playSession, exp)
	w = withCapabilityHeaders(w)
	writeJSON(w, map[string]interface{}{
		"url":       locator,
		"expiresIn": int(exp.Sub(now).Seconds()),
	})
}
