package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
)

// Public API errors carry a stable machine code, a human message, and the
// request correlation ID — never raw database/Redis/filesystem/upstream text.

type apiErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
}

type apiErrorBody struct {
	Error apiErrorDetail `json:"error"`
}

type requestIDKeyType struct{}

var requestIDKey requestIDKeyType

// requestIDMiddleware assigns every request a random correlation ID. Any
// inbound X-Request-Id is ignored: the edge is not trusted to correlate.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf [8]byte
		id := "unknown"
		if _, err := rand.Read(buf[:]); err == nil {
			id = hex.EncodeToString(buf[:])
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func requestIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return "unknown"
}

func writeAPIError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(apiErrorBody{Error: apiErrorDetail{
		Code:      code,
		Message:   message,
		RequestID: requestIDFrom(r.Context()),
	}}); err != nil {
		log.Printf("writeAPIError: encode failed request_id=%s", requestIDFrom(r.Context()))
	}
}
