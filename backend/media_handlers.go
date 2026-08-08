package main

import (
	"encoding/json"
	"errors"
	"net/http"
)

func handleMediaContinue(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(userContextKey).(*UserContext)
	if user == nil || user.ID == "" {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Unauthorized.")
		return
	}
	catalog := currentJellyfinCatalog()
	details, err := catalog.Continue(r.Context(), user.ID, 20)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "Failed to load continue watching items.")
		return
	}
	if details == nil {
		details = []MediaDetail{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(details)
}

func handleMediaRecent(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(userContextKey).(*UserContext)
	if user == nil || user.ID == "" {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Unauthorized.")
		return
	}
	catalog := currentJellyfinCatalog()
	items, err := catalog.Recent(r.Context(), user.ID, 20)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "Failed to load recent media.")
		return
	}
	if items == nil {
		items = []MediaItem{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

func handleMediaRelated(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(userContextKey).(*UserContext)
	if user == nil || user.ID == "" {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Unauthorized.")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_id", "Media item ID required.")
		return
	}
	catalog := currentJellyfinCatalog()
	items, err := catalog.Related(r.Context(), user.ID, id, 12)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "Failed to load related media.")
		return
	}
	if items == nil {
		items = []MediaItem{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

func handleMediaPlaybackInfo(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(userContextKey).(*UserContext)
	if user == nil || user.ID == "" {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Unauthorized.")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_id", "Media item ID required.")
		return
	}
	opts, err := getPlaybackOptions(r.Context(), user.ID, id, nil, nil)
	if err != nil {
		// An id that resolves to nothing, or to a library outside the allowlist,
		// is a client-supplied value that does not address anything this caller
		// may see — a 404, not a server fault. Reporting 500 both misleads the
		// client and surfaces routine probing as an outage in monitoring. 404
		// rather than 403 so the response never confirms that an id exists in a
		// library the caller cannot reach.
		if errors.Is(err, errItemNotAuthorized) ||
			errors.Is(err, errCatalogNotFound) ||
			errors.Is(err, errCatalogInvalid) {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "Media item not found.")
			return
		}
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "Failed to load playback options.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(opts)
}
