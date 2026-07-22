package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
)

// annotationBookID returns the book id an annotation belongs to.
func annotationBookID(ctx context.Context, annotationID string) (int64, bool) {
	var bookID int64
	if err := dbPool.QueryRow(ctx, `SELECT book_id FROM annotations WHERE id = $1`, annotationID).Scan(&bookID); err != nil {
		return 0, false
	}
	return bookID, true
}

// authorizeAnnotationBook gates an annotation-scoped route on BookRead for the
// book the annotation belongs to.
func authorizeAnnotationBook(w http.ResponseWriter, r *http.Request, annotationID string) (int64, bool) {
	bookID, ok := annotationBookID(r.Context(), annotationID)
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return 0, false
	}
	if !authorizeBookHTTP(w, r, strconv.FormatInt(bookID, 10), BookRead) {
		return 0, false
	}
	return bookID, true
}

func handleListAnnotations(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userContextKey).(*UserContext)
	idStr, ok := libraryBookID(r)
	if !ok {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "Invalid book id.")
		return
	}
	if !authorizeBookHTTP(w, r, idStr, BookRead) {
		return
	}
	bookID, _ := strconv.ParseInt(idStr, 10, 64)
	items, err := ListAnnotations(r.Context(), bookID, user.ID, 100)
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
	if !authorizeBookHTTP(w, r, idStr, BookRead) {
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
	book, berr := fetchGrimmoryBook(reqCtx, idStr)
	if berr != nil {
		writeLibraryError(w, http.StatusBadGateway, "the book service is unavailable")
		return
	}
	locator, verr := validateAnnotationLocator(book.Format, body.Locator)
	if verr != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "The annotation locator is invalid.")
		return
	}
	bookID, _ := strconv.ParseInt(idStr, 10, 64)
	a, err := CreateAnnotation(r.Context(), bookID, user.ID, body.Visibility, locator, body.SelectedText, body.Note)
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
	if _, ok := authorizeAnnotationBook(w, r, aid); !ok {
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
	if _, ok := authorizeAnnotationBook(w, r, aid); !ok {
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
	if _, ok := authorizeAnnotationBook(w, r, aid); !ok {
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
	if _, ok := authorizeAnnotationBook(w, r, aid); !ok {
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

// writeJSON is a compact JSON responder.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
