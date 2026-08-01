package main

import (
	"encoding/json"
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
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "Failed to load playback options.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(opts)
}
