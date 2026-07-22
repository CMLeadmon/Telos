package main

import (
	"errors"
	"net/http"
)

func wpActor(r *http.Request) *UserContext { return r.Context().Value(userContextKey).(*UserContext) }

func handleCreateWatchParty(w http.ResponseWriter, r *http.Request) {
	user := wpActor(r)
	var body struct {
		MediaItemID    string `json:"mediaItemId"`
		TextChannelID  string `json:"textChannelId"`
		VoiceChannelID string `json:"voiceChannelId"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}
	if !validJellyfinID(body.MediaItemID) {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "A valid media item is required.")
		return
	}
	// Authorize the media item and both channels before creating anything.
	if !authorizeJellyfinItem(w, r, body.MediaItemID) {
		return
	}
	if body.TextChannelID != "" {
		if err := AuthorizeChannel(r.Context(), user, body.TextChannelID, ChannelView); err != nil {
			writeAPIError(w, r, http.StatusForbidden, "forbidden", "You cannot use that text channel.")
			return
		}
	}
	if body.VoiceChannelID != "" {
		if err := AuthorizeChannel(r.Context(), user, body.VoiceChannelID, ChannelVoice); err != nil {
			writeAPIError(w, r, http.StatusForbidden, "forbidden", "You cannot use that voice channel.")
			return
		}
	}
	p, err := CreateParty(r.Context(), user.ID, body.MediaItemID, body.TextChannelID, body.VoiceChannelID)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The party could not be created.")
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, p)
}

func handleGetWatchParty(w http.ResponseWriter, r *http.Request) {
	p, err := GetParty(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	writeJSON(w, p)
}

func handleInviteWatchParty(w http.ResponseWriter, r *http.Request) {
	user := wpActor(r)
	var body struct {
		InviteeID string `json:"inviteeId"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}
	if !looksLikeUUID(body.InviteeID) {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "A valid invitee is required.")
		return
	}
	if err := Invite(r.Context(), r.PathValue("id"), user.ID, body.InviteeID); err != nil {
		wpWriteErr(w, r, err)
		return
	}
	writeJSON(w, map[string]string{"status": "success"})
}

func handleRespondWatchPartyInvite(w http.ResponseWriter, r *http.Request) {
	user := wpActor(r)
	var body struct {
		Accept bool `json:"accept"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}
	if err := RespondInvite(r.Context(), r.PathValue("id"), user.ID, body.Accept); err != nil {
		wpWriteErr(w, r, err)
		return
	}
	writeJSON(w, map[string]string{"status": "success"})
}

func wpMemberStateHandler(state string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := wpActor(r)
		if err := SetMemberState(r.Context(), r.PathValue("id"), user.ID, state); err != nil {
			wpWriteErr(w, r, err)
			return
		}
		writeJSON(w, map[string]string{"status": "success"})
	}
}

func handleEndWatchParty(w http.ResponseWriter, r *http.Request) {
	user := wpActor(r)
	if err := EndParty(r.Context(), r.PathValue("id"), user.ID); err != nil {
		wpWriteErr(w, r, err)
		return
	}
	writeJSON(w, map[string]string{"status": "success"})
}

func handleWatchPartyHeartbeat(w http.ResponseWriter, r *http.Request) {
	user := wpActor(r)
	expires, err := RenewHostLease(r.Context(), r.PathValue("id"), user.ID)
	if err != nil {
		wpWriteErr(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"leaseExpiresAt": expires, "serverTime": wpClock()})
}

func handleWatchPartyOfferHost(w http.ResponseWriter, r *http.Request) {
	user := wpActor(r)
	var body struct {
		SuccessorID string `json:"successorId"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}
	if err := OfferHost(r.Context(), r.PathValue("id"), user.ID, body.SuccessorID); err != nil {
		wpWriteErr(w, r, err)
		return
	}
	writeJSON(w, map[string]string{"status": "success"})
}

func handleWatchPartyAcceptHost(w http.ResponseWriter, r *http.Request) {
	user := wpActor(r)
	if err := AcceptHostOffer(r.Context(), r.PathValue("id"), user.ID); err != nil {
		wpWriteErr(w, r, err)
		return
	}
	writeJSON(w, map[string]string{"status": "success"})
}

func handleWatchPartyCancelHost(w http.ResponseWriter, r *http.Request) {
	user := wpActor(r)
	if err := CancelHostOffer(r.Context(), r.PathValue("id"), user.ID); err != nil {
		wpWriteErr(w, r, err)
		return
	}
	writeJSON(w, map[string]string{"status": "success"})
}

func handleWatchPartyClaimHost(w http.ResponseWriter, r *http.Request) {
	user := wpActor(r)
	if err := ClaimHostLease(r.Context(), r.PathValue("id"), user.ID); err != nil {
		wpWriteErr(w, r, err)
		return
	}
	writeJSON(w, map[string]string{"status": "success"})
}

func handleGetWatchPartyState(w http.ResponseWriter, r *http.Request) {
	s, err := GetPartyState(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	writeJSON(w, s)
}

func handlePutWatchPartyState(w http.ResponseWriter, r *http.Request) {
	user := wpActor(r)
	var body struct {
		Action          string  `json:"action"`
		PositionSeconds float64 `json:"positionSeconds"`
		PlaybackRate    float64 `json:"playbackRate"`
		MediaItemID     string  `json:"mediaItemId"`
		ExpectedVersion int64   `json:"expectedVersion"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}
	// A media change must be authorized before it is applied.
	if body.Action == "change_media" && !authorizeJellyfinItem(w, r, body.MediaItemID) {
		return
	}
	s, err := ApplyControl(r.Context(), r.PathValue("id"), user.ID, WatchPartyControlInput{
		Action: body.Action, PositionSeconds: body.PositionSeconds, PlaybackRate: body.PlaybackRate,
		MediaItemID: body.MediaItemID, ExpectedVersion: body.ExpectedVersion,
	})
	if err != nil {
		wpWriteErr(w, r, err)
		return
	}
	writeJSON(w, s)
}

// wpWriteErr maps store errors to HTTP responses.
func wpWriteErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errWPNotFound):
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
	case errors.Is(err, errWPNotHost), errors.Is(err, errWPNotSuccessor):
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "You are not permitted to do that.")
	case errors.Is(err, errWPStaleVersion):
		writeAPIError(w, r, http.StatusConflict, "stale_version", "The party state changed; reload and retry.")
	case errors.Is(err, errWPLeaseExpired):
		writeAPIError(w, r, http.StatusConflict, "lease_expired", "The host lease has expired.")
	case errors.Is(err, errWPHeartbeatFast):
		writeAPIError(w, r, http.StatusTooManyRequests, "too_frequent", "Heartbeat too frequent.")
	case errors.Is(err, errWPBadControl):
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "The playback control is invalid.")
	default:
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
	}
}
