package main

import (
	"context"
	"errors"
	"net/http"
	"regexp"
)

// authorizeChannelHTTP resolves the {id} path value as a channel and enforces
// the given action, writing the public error and returning ok=false on
// failure. A nonexistent, malformed, or view-denied channel is reported as
// 404 so channel existence never leaks.
func authorizeChannelHTTP(w http.ResponseWriter, r *http.Request, action ChannelAction) (string, bool) {
	user, _ := r.Context().Value(userContextKey).(*UserContext)
	if user == nil {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return "", false
	}
	channelID := r.PathValue("id")
	if err := AuthorizeChannel(r.Context(), user, channelID, action); err != nil {
		if errors.Is(err, errChannelForbidden) {
			writeAPIError(w, r, http.StatusForbidden, "forbidden", "You cannot perform this action in this channel.")
		} else if errors.Is(err, errChannelNotFound) {
			writeAPIError(w, r, http.StatusNotFound, "channel_not_found", "Channel not found.")
		} else {
			writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		}
		return "", false
	}
	return channelID, true
}

// ChannelAction is the operation a caller wants to perform on a channel.
type ChannelAction string

const (
	ChannelView  ChannelAction = "view"
	ChannelSend  ChannelAction = "send"
	ChannelAdmin ChannelAction = "admin"
)

var (
	// errChannelNotFound is returned for a malformed, nonexistent, or
	// view-denied channel — the three are deliberately indistinguishable to
	// the client so channel existence never leaks across an authorization
	// boundary.
	errChannelNotFound  = errors.New("channel not found")
	errChannelForbidden = errors.New("channel action forbidden")
)

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// AuthorizeChannel enforces that channelID is a real channel the user may view,
// plus the action-specific permission. It must be called before any channel
// subscription, presence entry, history read, or mutation is allocated.
func AuthorizeChannel(ctx context.Context, user *UserContext, channelID string, action ChannelAction) error {
	if !uuidRE.MatchString(channelID) {
		return errChannelNotFound
	}
	var exists bool
	if err := dbPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM channels WHERE id = $1)`, channelID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errChannelNotFound
	}

	// View is a prerequisite for every action. A view-denied channel is
	// reported as not-found so its existence is not disclosed.
	canView, err := hasPermission(ctx, user, "view_channel", &channelID)
	if err != nil {
		return err
	}
	if !canView {
		return errChannelNotFound
	}

	var actionPerm string
	switch action {
	case ChannelView:
		return nil
	case ChannelSend:
		actionPerm = "send_messages"
	case ChannelAdmin:
		actionPerm = "manage_channels"
	default:
		return errChannelForbidden
	}

	ok, err := hasPermission(ctx, user, actionPerm, &channelID)
	if err != nil {
		return err
	}
	if !ok {
		return errChannelForbidden
	}
	return nil
}
