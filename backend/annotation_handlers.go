package main

import (
	"encoding/json"
	"net/http"
)

// requireCapabilityHTTP gates a request on a global capability, writing a 403
// and returning false when the caller lacks it.
func requireCapabilityHTTP(w http.ResponseWriter, r *http.Request, perm string) bool {
	user := r.Context().Value(userContextKey).(*UserContext)
	ok, err := hasPermission(r.Context(), user, perm, nil)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return false
	}
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "You do not have access to this target.")
		return false
	}
	return true
}

// authorizeAnnotationAccess gates read/comment access to a target by its type:
// a book uses the Grimmory per-book authorizer; streamed media and files use the
// corresponding view capability.
func authorizeAnnotationAccess(w http.ResponseWriter, r *http.Request, targetType, targetID string) bool {
	resolution, err := canonicalAnnotationTarget(r.Context(), targetType, targetID)
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return false
	}
	return authorizeResolvedAnnotationAccess(w, r, targetType, resolution)
}

func authorizeResolvedAnnotationAccess(w http.ResponseWriter, r *http.Request, targetType string, resolution CatalogResolution) bool {
	switch targetType {
	case "book":
		if resolution.Provider != ProviderGrimmory {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
			return false
		}
		return authorizeBookHTTP(w, r, resolution.UpstreamID, BookRead)
	case "media":
		if resolution.Provider != ProviderJellyfin {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
			return false
		}
		return requireCapabilityHTTP(w, r, "view_media")
	case "file":
		return requireCapabilityHTTP(w, r, "view_files")
	default:
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "Unknown annotation target.")
		return false
	}
}

// authorizeAnnotationTarget resolves an existing annotation's target and gates
// the request on access to it. Patch/delete/reply routes are annotation-scoped,
// so the target type is read from the row rather than trusted from the caller.
func authorizeAnnotationTarget(w http.ResponseWriter, r *http.Request, annotationID string) (string, string, bool) {
	targetType, resolution, err := canonicalizeAnnotationRow(r.Context(), annotationID)
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return "", "", false
	}
	if !authorizeResolvedAnnotationAccess(w, r, targetType, resolution) {
		return "", "", false
	}
	return targetType, resolution.ID, true
}

func handleListAnnotations(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	idStr, ok := libraryBookID(r)
	if !ok {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "Invalid book id.")
		return
	}
	resolution, err := canonicalAnnotationTarget(r.Context(), "book", idStr)
	if err != nil || resolution.Provider != ProviderGrimmory {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	if !authorizeBookHTTP(w, r, resolution.UpstreamID, BookRead) {
		return
	}
	items, err := ListAnnotations(r.Context(), "book", resolution.ID, user.ID, 100)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	writeJSON(w, map[string]any{"annotations": items})
}

func handleCreateAnnotation(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	idStr, ok := libraryBookID(r)
	if !ok {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "Invalid book id.")
		return
	}
	resolution, err := canonicalAnnotationTarget(r.Context(), "book", idStr)
	if err != nil || resolution.Provider != ProviderGrimmory {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	if !authorizeBookHTTP(w, r, resolution.UpstreamID, BookRead) {
		return
	}
	var body struct {
		Visibility   string          `json:"visibility"`
		Locator      json.RawMessage `json:"locator"`
		SelectedText string          `json:"selectedText"`
		Note         string          `json:"note"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}
	// Validate the locator against the book's server-reported format.
	reqCtx, cancel := upstreamRequestContext(r.Context())
	defer cancel()
	book, berr := fetchGrimmoryBook(reqCtx, resolution.UpstreamID)
	if berr != nil {
		writeLibraryError(w, http.StatusBadGateway, "the book service is unavailable")
		return
	}
	locator, verr := validateAnnotationLocator(book.Format, body.Locator)
	if verr != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "The annotation locator is invalid.")
		return
	}
	a, err := CreateAnnotation(r.Context(), "book", resolution.ID, user.ID, body.Visibility, locator, body.SelectedText, body.Note)
	if err != nil {
		if err == errAnnotationText {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "The annotation text exceeds the allowed length.")
			return
		}
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, a)
}

func handlePatchAnnotation(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	aid := r.PathValue("aid")
	if !looksLikeUUID(aid) {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "Invalid annotation id.")
		return
	}
	if _, _, ok := authorizeAnnotationTarget(w, r, aid); !ok {
		return
	}
	var body struct {
		Visibility string `json:"visibility"`
		Note       string `json:"note"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}
	if err := UpdateAnnotation(r.Context(), aid, user.ID, body.Visibility, body.Note); err != nil {
		switch err {
		case errAnnotationNotFound:
			writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		case errAnnotationText, errAnnotationLocator:
			writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "The annotation update is invalid.")
		default:
			writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		}
		return
	}
	writeJSON(w, map[string]string{"status": "success"})
}

func handleDeleteAnnotation(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	aid := r.PathValue("aid")
	if !looksLikeUUID(aid) {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "Invalid annotation id.")
		return
	}
	if _, _, ok := authorizeAnnotationTarget(w, r, aid); !ok {
		return
	}
	if err := DeleteAnnotation(r.Context(), aid, user); err != nil {
		switch err {
		case errAnnotationNotFound:
			writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		case errAnnotationDenied:
			writeAPIError(w, r, http.StatusForbidden, "forbidden", "You cannot remove this annotation.")
		default:
			writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		}
		return
	}
	writeJSON(w, map[string]string{"status": "success"})
}

func handleListAnnotationReplies(w http.ResponseWriter, r *http.Request) {
	aid := r.PathValue("aid")
	if !looksLikeUUID(aid) {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "Invalid annotation id.")
		return
	}
	if _, _, ok := authorizeAnnotationTarget(w, r, aid); !ok {
		return
	}
	replies, err := ListReplies(r.Context(), aid, 100)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		return
	}
	writeJSON(w, map[string]any{"replies": replies})
}

func handleCreateAnnotationReply(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	aid := r.PathValue("aid")
	if !looksLikeUUID(aid) {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "Invalid annotation id.")
		return
	}
	if _, _, ok := authorizeAnnotationTarget(w, r, aid); !ok {
		return
	}
	var body struct {
		Body string `json:"body"`
	}
	if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
		return
	}
	reply, err := CreateReply(r.Context(), aid, user.ID, body.Body)
	if err != nil {
		switch err {
		case errAnnotationNotFound:
			writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		case errReplyOnlyCommunity:
			writeAPIError(w, r, http.StatusConflict, "not_community", "Replies are only allowed on community annotations.")
		case errAnnotationText:
			writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "The reply is empty or too long.")
		default:
			writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		}
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, reply)
}

func handleDeleteAnnotationReply(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	rid := r.PathValue("rid")
	if !looksLikeUUID(rid) {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "Invalid reply id.")
		return
	}
	if err := DeleteReply(r.Context(), rid, user); err != nil {
		switch err {
		case errAnnotationNotFound:
			writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		case errAnnotationDenied:
			writeAPIError(w, r, http.StatusForbidden, "forbidden", "You cannot remove this reply.")
		default:
			writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		}
		return
	}
	writeJSON(w, map[string]string{"status": "success"})
}

// commentListHandler lists commentary on a media item or file. targetType fixes
// which kind of target the route serves; the id comes from the path.
func commentListHandler(targetType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := r.Context().Value(userContextKey).(*UserContext)
		id := r.PathValue("id")
		if id == "" {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "Invalid target id.")
			return
		}
		resolution, err := canonicalAnnotationTarget(r.Context(), targetType, id)
		if err != nil {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
			return
		}
		if !authorizeResolvedAnnotationAccess(w, r, targetType, resolution) {
			return
		}
		items, err := ListAnnotations(r.Context(), targetType, resolution.ID, user.ID, 100)
		if err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		writeJSON(w, map[string]any{"annotations": items})
	}
}

// commentCreateHandler posts a comment on a media item or file. Comments carry
// no locator or selected text — those are book-reader concepts — so the body is
// just a visibility and a note.
func commentCreateHandler(targetType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := r.Context().Value(userContextKey).(*UserContext)
		id := r.PathValue("id")
		if id == "" {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "Invalid target id.")
			return
		}
		resolution, err := canonicalAnnotationTarget(r.Context(), targetType, id)
		if err != nil {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
			return
		}
		if !authorizeResolvedAnnotationAccess(w, r, targetType, resolution) {
			return
		}
		var body struct {
			Visibility string `json:"visibility"`
			Note       string `json:"note"`
		}
		if err := decodeJSON(w, r, &body, securityConfig.JSONBytes); err != nil {
			return
		}
		a, err := CreateAnnotation(r.Context(), targetType, resolution.ID, user.ID, body.Visibility, json.RawMessage("{}"), "", body.Note)
		if err != nil {
			if err == errAnnotationText {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "The comment exceeds the allowed length.")
				return
			}
			writeAPIError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, a)
	}
}

// writeJSON is a compact JSON responder.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
